//go:build integration

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
)

// labPDF is a minimal synthetic PDF with n pages; salt makes the content unique.
func labPDF(n int, salt string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%%PDF-1.4\n%%%s\n1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n2 0 obj << /Type /Pages /Count %d >> endobj\n", salt, n)
	for i := range n {
		fmt.Fprintf(&b, "%d 0 obj << /Type /Page /Parent 2 0 R >> endobj\n", 3+i)
	}
	b.WriteString("trailer << /Root 1 0 R >>\n%%EOF\n")
	return b.Bytes()
}

type docEnv struct {
	t    *testing.T
	h    http.Handler
	user uuid.UUID
}

func newDocEnv(t *testing.T) *docEnv {
	t.Helper()
	u, app := dbtest.Migrated(t)
	e := &docEnv{t: t, user: uuid.New()}
	if _, err := dbtest.Pool(t, u, db.OwnerRole).Exec(t.Context(),
		`INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')`, e.user); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.WriteKeyFile(keyPath); err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.Load(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.Open(filepath.Join(t.TempDir(), "blobs"), kr)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), Options{DB: db.New(app), Blobs: blobs, Keys: kr})
	if err != nil {
		t.Fatal(err)
	}
	e.h = rt.handler() // with body limits; the principal is set on the request
	return e
}

// do sends a request as an API key with every owner scope and checks the response against the spec.
func (e *docEnv) do(method, pattern, target, contentType string, body io.Reader, want int) []byte {
	e.t.Helper()
	req := request(e.t, method, target, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	p := &auth.Principal{Kind: auth.APIKey, UserID: e.user, ID: uuid.New(), Scopes: []auth.Scope{auth.Admin}}
	res := serve(e.t, e.h, req.WithContext(auth.WithPrincipal(req.Context(), p)))
	out := checkResponse(e.t, pattern, res)
	if res.StatusCode != want {
		e.t.Fatalf("%s %s: %d, want %d: %s", method, target, res.StatusCode, want, out)
	}
	return out
}

func multipartPDF(t *testing.T, name string, pdf []byte) (string, io.Reader) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	_ = w.WriteField("note", "ignored")
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(pdf)
	_ = w.Close()
	return w.FormDataContentType(), &b
}

func TestDocumentEndpoints(t *testing.T) {
	e := newDocEnv(t)
	const upload = "POST /api/v1/documents"
	pdf := labPDF(2, "synthetic-a")

	ct, body := multipartPDF(t, "lab report.pdf", pdf)
	var doc oapi.Document
	if err := json.Unmarshal(e.do(http.MethodPost, upload, "/api/v1/documents", ct, body, http.StatusCreated), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Status != "uploaded" || doc.PageCount != 2 || doc.Filename == nil || *doc.Filename != "lab report.pdf" || doc.Sha256 == nil {
		t.Fatalf("uploaded: %+v", doc)
	}
	// The same content as a raw body links to the existing document.
	var again oapi.Document
	if err := json.Unmarshal(e.do(http.MethodPost, upload, "/api/v1/documents", "application/pdf", bytes.NewReader(pdf), http.StatusOK), &again); err != nil {
		t.Fatal(err)
	}
	if again.ID != doc.ID {
		t.Fatalf("re-upload created %s", again.ID)
	}

	for reason, payload := range map[string][]byte{
		"not_pdf":   []byte("<html>synthetic</html>"),
		"encrypted": bytes.Replace(labPDF(1, "enc"), []byte("/Root 1 0 R"), []byte("/Root 1 0 R /Encrypt 9 0 R"), 1),
		"empty":     {},
	} {
		var p problem
		if err := json.Unmarshal(e.do(http.MethodPost, upload, "/api/v1/documents", "application/pdf", bytes.NewReader(payload), http.StatusUnprocessableEntity), &p); err != nil {
			t.Fatal(err)
		}
		if len(p.Errors) != 1 || p.Errors[0].Detail != reason {
			t.Errorf("%s: %+v", reason, p.Errors)
		}
	}
	tooBig := append(labPDF(1, "big"), make([]byte, 21<<20)...)
	e.do(http.MethodPost, upload, "/api/v1/documents", "application/pdf", bytes.NewReader(tooBig), http.StatusRequestEntityTooLarge)
	ct, body = multipartPDF(t, "huge.pdf", append(tooBig, make([]byte, 5<<20)...))
	e.do(http.MethodPost, upload, "/api/v1/documents", ct, body, http.StatusRequestEntityTooLarge)
	e.do(http.MethodPost, upload, "/api/v1/documents", "text/plain", strings.NewReader("x"), http.StatusUnprocessableEntity)

	var page oapi.DocumentPage
	if err := json.Unmarshal(e.do(http.MethodGet, "GET /api/v1/documents", "/api/v1/documents?limit=1", "", nil, http.StatusOK), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Documents) != 1 || page.Documents[0].ID != doc.ID || page.HasMore {
		t.Fatalf("list: %+v", page)
	}
	file := e.do(http.MethodGet, "GET /api/v1/documents/{id}/file", "/api/v1/documents/"+doc.ID+"/file", "", nil, http.StatusOK)
	if !bytes.Equal(file, pdf) {
		t.Fatal("downloaded file differs")
	}
	e.do(http.MethodGet, "GET /api/v1/documents/{id}", "/api/v1/documents/doc_nothex", "", nil, http.StatusNotFound)
	e.do(http.MethodDelete, "DELETE /api/v1/documents/{id}", "/api/v1/documents/"+doc.ID+"?derived=maybe", "", nil, http.StatusUnprocessableEntity)
	e.do(http.MethodDelete, "DELETE /api/v1/documents/{id}", "/api/v1/documents/"+doc.ID+"?derived=keep", "", nil, http.StatusNoContent)
	e.do(http.MethodGet, "GET /api/v1/documents/{id}/file", "/api/v1/documents/"+doc.ID+"/file", "", nil, http.StatusNotFound)
	var gone oapi.Document
	if err := json.Unmarshal(e.do(http.MethodGet, "GET /api/v1/documents/{id}", "/api/v1/documents/"+doc.ID, "", nil, http.StatusOK), &gone); err != nil {
		t.Fatal(err)
	}
	if gone.Status != "deleted" || gone.Sha256 != nil || gone.Filename != nil || gone.DeletedAt == nil {
		t.Fatalf("deleted document: %+v", gone)
	}
}

func TestAnalyteAliasEndpoints(t *testing.T) {
	e := newDocEnv(t)
	const list, create, remove = "GET /api/v1/analytes/aliases", "POST /api/v1/analytes/aliases", "DELETE /api/v1/analytes/aliases/{id}"
	var all struct{ Aliases []oapi.AnalyteAlias }
	if err := json.Unmarshal(e.do(http.MethodGet, list, "/api/v1/analytes/aliases", "", nil, http.StatusOK), &all); err != nil {
		t.Fatal(err)
	}
	if len(all.Aliases) == 0 || all.Aliases[0].Source != "seed" {
		t.Fatalf("seeded aliases: %d", len(all.Aliases))
	}
	var a oapi.AnalyteAlias
	body := `{"label": "Glukose (nüchtern)", "analyte": "glucose"}`
	if err := json.Unmarshal(e.do(http.MethodPost, create, "/api/v1/analytes/aliases", "application/json", strings.NewReader(body), http.StatusCreated), &a); err != nil {
		t.Fatal(err)
	}
	if a.Source != "owner" || a.Analyte != "glucose" {
		t.Fatalf("created: %+v", a)
	}
	e.do(http.MethodPost, create, "/api/v1/analytes/aliases", "application/json", strings.NewReader(body), http.StatusConflict)
	e.do(http.MethodPost, create, "/api/v1/analytes/aliases", "application/json", strings.NewReader(`{"label": "x", "analyte": "nope"}`), http.StatusUnprocessableEntity)
	e.do(http.MethodDelete, remove, "/api/v1/analytes/aliases/"+all.Aliases[0].ID, "", nil, http.StatusConflict)
	e.do(http.MethodDelete, remove, "/api/v1/analytes/aliases/"+a.ID, "", nil, http.StatusNoContent)
	e.do(http.MethodDelete, remove, "/api/v1/analytes/aliases/"+a.ID, "", nil, http.StatusNotFound)
}
