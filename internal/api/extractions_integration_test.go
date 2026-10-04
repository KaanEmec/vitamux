//go:build integration

package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/documents/extract"
	"github.com/KaanEmec/vitamux/internal/httpx"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

func newExtractEnv(t *testing.T) (*docEnv, *extract.Service, *db.DB) {
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
	d := db.New(app)
	// Gemini is configured but never reached: no job runs against it here.
	gemini := extract.NewGemini(extract.GeminiConfig{APIKey: "synthetic-unused", Model: "gemini-test", BaseURL: "http://127.0.0.1:1", Client: httpx.New(httpx.Options{})})
	svc, err := extract.New(d, blobs, kr, slog.New(slog.DiscardHandler), gemini)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), Options{DB: d, Blobs: blobs, Keys: kr, Extract: svc})
	if err != nil {
		t.Fatal(err)
	}
	e.h = rt.handler()
	return e, svc, d
}

func TestExtractionEndpoints(t *testing.T) {
	e, svc, d := newExtractEnv(t)
	const create, list = "POST /api/v1/documents/{id}/extractions", "GET /api/v1/documents/{id}/extractions"
	var doc oapi.Document
	if err := json.Unmarshal(e.do(http.MethodPost, "POST /api/v1/documents", "/api/v1/documents", "application/pdf",
		bytes.NewReader(labPDF(1, "synthetic-extract")), http.StatusCreated), &doc); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/documents/" + doc.ID + "/extractions"
	post := func(body string, want int) problem {
		t.Helper()
		var p problem
		_ = json.Unmarshal(e.do(http.MethodPost, create, path, "application/json", strings.NewReader(body), want), &p)
		return p
	}
	ack := time.Now().UTC().Format(time.RFC3339)

	// Refusals: each with a clear status and code, none creating a run.
	for name, tc := range map[string]struct {
		body string
		want int
		code Code
	}{
		"unknown provider": {`{"provider": "acme"}`, http.StatusUnprocessableEntity, CodeValidationFailed},
		"no body field":    {`{}`, http.StatusUnprocessableEntity, CodeValidationFailed},
		"not configured":   {`{"provider": "openai", "consent": {"provider": "openai", "model": "x", "acknowledged_at": "` + ack + `"}}`, http.StatusForbidden, CodeForbidden},
		"disabled":         {`{"provider": "gemini", "consent": {"provider": "gemini", "model": "gemini-test", "acknowledged_at": "` + ack + `"}}`, http.StatusForbidden, CodeForbidden},
	} {
		if p := post(tc.body, tc.want); p.Code != tc.code {
			t.Errorf("%s: code %s, want %s", name, p.Code, tc.code)
		}
	}
	if err := extract.SetEnabled(t.Context(), d, e.user, audit.Owner, extract.Gemini, true); err != nil {
		t.Fatal(err)
	}
	if p := post(`{"provider": "gemini"}`, http.StatusConflict); p.Code != CodeConsentRequired {
		t.Errorf("no consent: %s", p.Code)
	}
	if p := post(`{"provider": "gemini", "consent": {"provider": "gemini", "model": "gemini-other", "acknowledged_at": "`+ack+`"}}`, http.StatusConflict); p.Code != CodeConsentRequired || !strings.Contains(p.Detail, "gemini-test") {
		t.Errorf("mismatched consent: %s %q", p.Code, p.Detail)
	}
	var body struct{ Extractions []oapi.Extraction }
	if err := json.Unmarshal(e.do(http.MethodGet, list, path, "", nil, http.StatusOK), &body); err != nil || len(body.Extractions) != 0 {
		t.Fatalf("refusals created runs: %v %d", err, len(body.Extractions))
	}

	// Consent given: queued, recorded, and a second request conflicts.
	var run oapi.Extraction
	if err := json.Unmarshal(e.do(http.MethodPost, create, path, "application/json", strings.NewReader(
		`{"provider": "gemini", "consent": {"provider": "gemini", "model": "gemini-test", "acknowledged_at": "`+ack+`"}}`), http.StatusAccepted), &run); err != nil {
		t.Fatal(err)
	}
	if run.Status != "queued" || !run.External || run.Consent == nil || run.Consent.Model != "gemini-test" || run.DocumentID != doc.ID {
		t.Fatalf("run: %+v", run)
	}
	if p := post(`{"provider": "fake"}`, http.StatusConflict); p.Code != CodeConflict {
		t.Errorf("concurrent run: %s", p.Code)
	}
	e.do(http.MethodPost, create, "/api/v1/documents/doc_00000000000000000000000000000000/extractions", "application/json", strings.NewReader(`{"provider": "fake"}`), http.StatusNotFound)

	// A provider that cannot be reached leaves the run queued for a retry, with its class shown.
	if err := svc.Handle(t.Context(), jobFor(t, run.ID)); err == nil {
		t.Fatal("Gemini at a closed port succeeded")
	}
	var got oapi.Document
	_ = json.Unmarshal(e.do(http.MethodGet, "GET /api/v1/documents/{id}", "/api/v1/documents/"+doc.ID, "", nil, http.StatusOK), &got)
	if err := json.Unmarshal(e.do(http.MethodGet, list, path, "", nil, http.StatusOK), &body); err != nil || len(body.Extractions) != 1 {
		t.Fatalf("list: %v", err)
	}
	if r := body.Extractions[0]; r.Status != "queued" || r.ErrorClass == nil || *r.ErrorClass != extract.ClassTransient || got.Status != "extracting" {
		t.Errorf("after a transient failure: run %s %v, document %s", r.Status, r.ErrorClass, got.Status)
	}
	if strings.Contains(string(e.do(http.MethodGet, list, path, "", nil, http.StatusOK)), "response_blob") {
		t.Error("the list exposes the response blob")
	}
}

// jobFor is the first attempt of the extraction job of run (ext_<hex>).
func jobFor(t *testing.T, id string) jobs.Job {
	t.Helper()
	runID, err := uuid.Parse(strings.TrimPrefix(id, "ext_"))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(map[string]any{"run_id": runID})
	return jobs.Job{Kind: extract.Kind, Payload: p, Attempt: 1, MaxAttempts: 3}
}
