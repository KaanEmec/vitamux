//go:build integration

package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/export"
)

func TestExportEndpoints(t *testing.T) {
	_, pool := dbtest.Migrated(t)
	d := db.New(pool)
	keyPath := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.WriteKeyFile(keyPath); err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.Load(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.New(d, kr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.CreateOwner(t.Context(), d, ownerName, ownerPassword); err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.Open(filepath.Join(t.TempDir(), "blobs"), kr)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), Options{Auth: svc, DB: d, Blobs: blobs})
	if err != nil {
		t.Fatal(err)
	}
	h := rt.handler()

	do := func(method, path, body string, hdr map[string]string) (*http.Response, []byte) {
		t.Helper()
		var r io.Reader
		if body != "" {
			r = strings.NewReader(body)
		}
		req := request(t, method, path, r)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res := serve(t, h, req)
		b, _ := io.ReadAll(res.Body)
		return res, b
	}
	res, body := do(http.MethodPost, "/api/v1/auth/login", loginBody(ownerPassword), nil)
	var login struct {
		CSRF string `json:"csrf_token"`
	}
	if res.StatusCode != http.StatusOK || json.Unmarshal(body, &login) != nil {
		t.Fatalf("login: %d %s", res.StatusCode, body)
	}
	var cookie string
	for _, c := range res.Cookies() {
		if c.Name == auth.SessionCookie {
			cookie = c.Name + "=" + c.Value
		}
	}
	owner := map[string]string{"Cookie": cookie, csrfHeader: login.CSRF}

	const create = "POST /api/v1/exports"
	res, body = do(http.MethodPost, "/api/v1/exports", `{"format":"xml"}`, owner)
	checkBody(t, create, res.StatusCode, res.Header.Get("Content-Type"), body)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad format: %d %s", res.StatusCode, body)
	}
	res, body = do(http.MethodPost, "/api/v1/exports", `{"format":"csv","include_raw":true}`, owner)
	checkBody(t, create, res.StatusCode, res.Header.Get("Content-Type"), body)
	var created map[string]any
	if res.StatusCode != http.StatusAccepted || json.Unmarshal(body, &created) != nil || created["status"] != "queued" {
		t.Fatalf("create: %d %s", res.StatusCode, body)
	}
	id := created["id"].(string)
	res, body = do(http.MethodPost, "/api/v1/exports", `{"format":"ndjson"}`, owner)
	checkBody(t, create, res.StatusCode, res.Header.Get("Content-Type"), body)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("second create while queued: %d %s", res.StatusCode, body)
	}

	// The worker builds it.
	if err := export.Run(t.Context(), d, blobs, uuid.MustParse(id)); err != nil {
		t.Fatal(err)
	}
	const get = "GET /api/v1/exports/{id}"
	res, body = do(http.MethodGet, "/api/v1/exports/"+id, "", owner)
	checkBody(t, get, res.StatusCode, res.Header.Get("Content-Type"), body)
	var done map[string]any
	if err := json.Unmarshal(body, &done); err != nil || done["status"] != "done" || done["download_url"] == nil {
		t.Fatalf("get: %d %s", res.StatusCode, body)
	}
	url := done["download_url"].(string)

	// A read:health key may not export, download or even look.
	res, body = do(http.MethodPost, "/api/v1/api-keys", `{"name":"dash","scopes":["read:health"]}`, owner)
	var key map[string]any
	if res.StatusCode != http.StatusCreated || json.Unmarshal(body, &key) != nil {
		t.Fatalf("api key: %d %s", res.StatusCode, body)
	}
	reader := map[string]string{"Authorization": "Bearer " + key["token"].(string)}
	for _, path := range []string{"/api/v1/exports/" + id, url} {
		if res, _ := do(http.MethodGet, path, "", reader); res.StatusCode != http.StatusForbidden {
			t.Errorf("read:health GET %s: %d", path, res.StatusCode)
		}
	}

	const download = "GET /api/v1/exports/{id}/download"
	res, body = do(http.MethodGet, url, "", owner)
	checkBody(t, download, res.StatusCode, res.Header.Get("Content-Type"), body)
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: %d %v", res.StatusCode, res.Header)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{export.ManifestName, "measurements.ndjson", "measurements.csv", "blob_content.ndjson", "connections.ndjson"} {
		if !names[want] {
			t.Errorf("zip lacks %s: %v", want, names)
		}
	}
	if names["credentials.ndjson"] || names["sessions.ndjson"] || names["api_keys.ndjson"] {
		t.Errorf("zip holds secrets: %v", names)
	}

	// The token works once.
	res, body = do(http.MethodGet, url, "", owner)
	checkBody(t, download, res.StatusCode, res.Header.Get("Content-Type"), body)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("second download: %d", res.StatusCode)
	}
	res, body = do(http.MethodGet, "/api/v1/exports/"+uuid.NewString(), "", owner)
	checkBody(t, get, res.StatusCode, res.Header.Get("Content-Type"), body)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown export: %d", res.StatusCode)
	}
}
