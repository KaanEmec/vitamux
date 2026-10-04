//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/documents"
	"github.com/KaanEmec/vitamux/internal/documents/extract"
	"github.com/KaanEmec/vitamux/internal/documents/review"
)

// reviewExtractor replaces the fake provider with a fixed synthetic extraction, so any test
// PDF can be reviewed. Its rows carry a seeded wrong decimal and an unknown analyte.
type reviewExtractor struct{}

func (reviewExtractor) ID() string     { return extract.Fake }
func (reviewExtractor) External() bool { return false }
func (reviewExtractor) Model() string  { return "" }

const reviewTruth = `{"schema": "vitamux.lab.extraction/1", "synthetic": true, "warnings": [],
 "document": {"laboratory": "Synthetic Lab", "specimen_type": "Serum", "collected_at": "2025-03-04T08:15",
  "collected_at_text": "2025-03-04 08:15", "reported_at": "2025-03-05", "reported_at_text": "2025-03-05", "page_count": 1},
 "rows": [
  {"page": 1, "row_index": 0, "analyte_label": "Glucose", "value_text": "100", "value_numeric": 100, "comparator": null,
   "unit_text": "mg/dL", "reference_range_text": "70 - 99", "ref_low": 70, "ref_high": 99, "printed_flag": "H",
   "specimen_type": "Serum", "collected_at": "2025-03-04T08:15", "reported_at": "2025-03-05", "laboratory": "Synthetic Lab",
   "evidence_text": "Glucose 100 H mg/dL 70 - 99", "bbox": {"x0": 0.1, "y0": 0.2, "x1": 0.9, "y1": 0.25}, "confidence": 1, "warnings": []},
  {"page": 1, "row_index": 1, "analyte_label": "Zentrix marker", "value_text": "Negative", "value_numeric": null, "comparator": null,
   "unit_text": null, "reference_range_text": "Negative", "ref_low": null, "ref_high": null, "printed_flag": null,
   "specimen_type": "Serum", "collected_at": "2025-03-04T08:15", "reported_at": "2025-03-05", "laboratory": "Synthetic Lab",
   "evidence_text": "Zentrix marker Negative Negative", "bbox": null, "confidence": 0.8, "warnings": []},
  {"page": 1, "row_index": 2, "analyte_label": "Creatinine", "value_text": "0.75", "value_numeric": 7.5, "comparator": null,
   "unit_text": "mg/dL", "reference_range_text": "0.60 - 1.30", "ref_low": 0.6, "ref_high": 1.3, "printed_flag": null,
   "specimen_type": "Serum", "collected_at": "2025-03-04T08:15", "reported_at": "2025-03-05", "laboratory": "Synthetic Lab",
   "evidence_text": "Creatinine 0.75 mg/dL 0.60 - 1.30", "bbox": null, "confidence": 0.9, "warnings": []}]}`

func (reviewExtractor) Extract(context.Context, extract.Request) (extract.Response, error) {
	x, err := documents.DecodeExtraction([]byte(reviewTruth))
	return extract.Response{Raw: []byte(reviewTruth), Extraction: x, ModelID: "synthetic"}, err
}

// newReviewEnv returns the env, the extraction service and a counter of audit events by action.
func newReviewEnv(t *testing.T) (*docEnv, *extract.Service, func(action string) int) {
	t.Helper()
	u, app := dbtest.Migrated(t)
	owner := dbtest.Pool(t, u, db.OwnerRole)
	e := &docEnv{t: t, user: uuid.New()}
	if _, err := owner.Exec(t.Context(), `INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')`, e.user); err != nil {
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
	svc, err := extract.New(d, blobs, kr, slog.New(slog.DiscardHandler), reviewExtractor{})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), Options{DB: d, Blobs: blobs, Keys: kr, Extract: svc})
	if err != nil {
		t.Fatal(err)
	}
	e.h = rt.handler()
	audits := func(action string) int {
		t.Helper()
		var n int
		if err := owner.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action = $1`, action).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	return e, svc, audits
}

// TestReviewEndpoints: upload → extract → review edits → confirm → results with provenance →
// revise → unconfirm, each response checked against the spec and every mutation audited.
func TestReviewEndpoints(t *testing.T) {
	e, svc, audits := newReviewEnv(t)
	ctx := t.Context()
	const (
		getX, patchRow, confirm, unconfirm = "GET /api/v1/extractions/{id}", "PATCH /api/v1/extractions/{id}/rows/{row}",
			"POST /api/v1/extractions/{id}/confirm", "POST /api/v1/extractions/{id}/unconfirm"
		results, history = "GET /api/v1/lab-results", "GET /api/v1/lab-results/{id}/history"
	)
	var doc oapi.Document
	_ = json.Unmarshal(e.do(http.MethodPost, "POST /api/v1/documents", "/api/v1/documents", "application/pdf",
		bytes.NewReader(labPDF(1, "synthetic-review")), http.StatusCreated), &doc)
	var run oapi.Extraction
	_ = json.Unmarshal(e.do(http.MethodPost, "POST /api/v1/documents/{id}/extractions", "/api/v1/documents/"+doc.ID+"/extractions",
		"application/json", strings.NewReader(`{"provider": "fake"}`), http.StatusAccepted), &run)
	base := "/api/v1/extractions/" + run.ID
	// Before the run succeeds there is nothing to review.
	if p := e.problem(http.MethodPatch, patchRow, base+"/rows/0", `{"review": "accept"}`, http.StatusConflict); p.Code != CodeConflict {
		t.Errorf("row of a queued run: %s", p.Code)
	}
	if err := svc.Handle(ctx, jobFor(t, run.ID)); err != nil {
		t.Fatal(err)
	}

	get := func() oapi.Extraction {
		t.Helper()
		var x oapi.Extraction
		if err := json.Unmarshal(e.do(http.MethodGet, getX, base, "", nil, http.StatusOK), &x); err != nil || x.Rows == nil {
			t.Fatalf("get: %v", err)
		}
		return x
	}
	x := get()
	rows := *x.Rows
	if len(rows) != 3 || !slices.Equal(rows[2].Validation, []string{review.WarnValueMismatch}) || rows[0].Analyte == nil ||
		*rows[0].Analyte != "glucose" || rows[1].Analyte != nil || !slices.Equal(rows[1].Validation, []string{review.WarnUnknownAnalyte}) ||
		rows[0].Bbox == nil || rows[0].ReviewStatus != "pending" {
		t.Fatalf("rows: %+v", rows)
	}

	if p := e.problem(http.MethodPost, confirm, base+"/confirm", "", http.StatusUnprocessableEntity); len(p.Errors) != 3 || p.Errors[0].Pointer != "/rows/0" {
		t.Errorf("confirm before review: %+v", p)
	}
	if p := e.problem(http.MethodPatch, patchRow, base+"/rows/0", `{"value_numeric": "high", "colour": "red"}`, http.StatusUnprocessableEntity); len(p.Errors) != 2 {
		t.Errorf("bad patch: %+v", p)
	}
	e.problem(http.MethodPatch, patchRow, base+"/rows/9", `{"review": "accept"}`, http.StatusNotFound)
	e.problem(http.MethodPatch, patchRow, base+"/rows/x", `{"review": "accept"}`, http.StatusNotFound)

	patch := func(i, body string) oapi.ExtractionRow {
		t.Helper()
		var r oapi.ExtractionRow
		_ = json.Unmarshal(e.do(http.MethodPatch, patchRow, base+"/rows/"+i, "application/json", strings.NewReader(body), http.StatusOK), &r)
		return r
	}
	if r := patch("0", `{"review": "accept"}`); r.ReviewStatus != "accepted" || len(r.Edits) != 1 {
		t.Errorf("accept: %+v", r)
	}
	if r := patch("1", `{"review": "accept"}`); r.ReviewStatus != "accepted" || r.UnitText != nil {
		t.Errorf("unitless accept: %+v", r)
	}
	if r := patch("2", `{"value_numeric": 0.75}`); r.ReviewStatus != "edited" || len(r.Validation) != 0 || !strings.Contains(string(r.Edits[0].Changes), `"from":7.5`) {
		t.Errorf("edit: %+v", r)
	}

	var confirmed oapi.Extraction
	_ = json.Unmarshal(e.do(http.MethodPost, confirm, base+"/confirm", "", nil, http.StatusOK), &confirmed)
	if confirmed.Status != "confirmed" || (*confirmed.Rows)[0].LabResultID == nil {
		t.Fatalf("confirmed: %+v", confirmed)
	}
	var page oapi.LabResultPage
	_ = json.Unmarshal(e.do(http.MethodGet, results, "/api/v1/lab-results?start_date=2025-03-04&end_date=2025-03-04&limit=2", "", nil, http.StatusOK), &page)
	if len(page.LabResults) != 2 || !page.HasMore || page.NextCursor == nil {
		t.Fatalf("first page: %+v", page)
	}
	var rest oapi.LabResultPage
	_ = json.Unmarshal(e.do(http.MethodGet, results, "/api/v1/lab-results?start_date=2025-03-04&end_date=2025-03-04&limit=2&cursor="+*page.NextCursor, "", nil, http.StatusOK), &rest)
	all := append(page.LabResults, rest.LabResults...)
	if len(all) != 3 || rest.HasMore {
		t.Fatalf("pages: %d", len(all))
	}
	for _, r := range all {
		pv := r.Provenance
		if pv.DocumentID != doc.ID || pv.ExtractionID == nil || *pv.ExtractionID != run.ID || pv.RowIndex == nil || r.Page == nil ||
			r.EvidenceText == nil || pv.Provider != "fake" || pv.Model == nil || *pv.Model != "synthetic" || r.CollectedDate.String() != "2025-03-04" ||
			r.CollectedAt != nil { // no timezone period configured: the printed date only
			t.Errorf("provenance: %+v", r)
		}
		switch r.OriginalLabel {
		case "Zentrix marker":
			if r.Analyte != nil || r.CanonicalValue != nil || r.UnitText != nil || r.ValueText != "Negative" {
				t.Errorf("unknown analyte: %+v", r)
			}
		case "Creatinine":
			if r.CanonicalUnit == nil || *r.ValueNumeric != 0.75 || *r.UnitText != "mg/dL" {
				t.Errorf("creatinine: %+v", r)
			}
		}
	}
	var none oapi.LabResultPage
	_ = json.Unmarshal(e.do(http.MethodGet, results, "/api/v1/lab-results?start_date=2025-03-05", "", nil, http.StatusOK), &none)
	if len(none.LabResults) != 0 {
		t.Errorf("date filter: %d", len(none.LabResults))
	}

	// Revise: edit after confirmation, confirm again, two revisions in the history.
	patch("0", `{"value_text": "101", "value_numeric": 101}`)
	e.do(http.MethodPost, confirm, base+"/confirm", "", nil, http.StatusOK)
	var hist struct{ Revisions []oapi.LabResult }
	_ = json.Unmarshal(e.do(http.MethodGet, history, "/api/v1/lab-results/"+*(*confirmed.Rows)[0].LabResultID+"/history", "", nil, http.StatusOK), &hist)
	if len(hist.Revisions) != 2 || hist.Revisions[0].ValueText != "101" || hist.Revisions[1].ValueText != "100" || hist.Revisions[1].Revision != 1 {
		t.Errorf("history: %+v", hist.Revisions)
	}
	e.do(http.MethodGet, history, "/api/v1/lab-results/lab_00000000000000000000000000000000/history", "", nil, http.StatusNotFound)

	var back oapi.Extraction
	_ = json.Unmarshal(e.do(http.MethodPost, unconfirm, base+"/unconfirm", "", nil, http.StatusOK), &back)
	if back.Status != "succeeded" || (*back.Rows)[0].LabResultID != nil {
		t.Errorf("unconfirmed: %+v", back)
	}
	if p := e.problem(http.MethodPost, unconfirm, base+"/unconfirm", "", http.StatusConflict); p.Code != CodeConflict {
		t.Errorf("unconfirm twice: %s", p.Code)
	}
	e.do(http.MethodGet, getX, "/api/v1/extractions/ext_00000000000000000000000000000000", "", nil, http.StatusNotFound)

	for action, want := range map[string]int{"extraction_row.review": 4, "extraction.confirm": 2, "extraction.unconfirm": 1} {
		if n := audits(action); n != want {
			t.Errorf("%s: %d audit events, want %d", action, n, want)
		}
	}
}

// TestDocumentSettings: external extractor enablement and the retention policy through
// /api/v1/settings, with null keeping originals.
func TestDocumentSettings(t *testing.T) {
	e, _, audits := newReviewEnv(t)
	const get, patch = "GET /api/v1/settings", "PATCH /api/v1/settings"
	var s oapi.Settings
	_ = json.Unmarshal(e.do(http.MethodGet, get, "/api/v1/settings", "", nil, http.StatusOK), &s)
	if s.DocumentsExternalAiGeminiEnabled == nil || *s.DocumentsExternalAiGeminiEnabled || string(s.DocumentsRetentionDays) != "null" ||
		s.DocumentsDeleteOriginalAfterConfirmation == nil || *s.DocumentsDeleteOriginalAfterConfirmation {
		t.Fatalf("defaults: %+v", s)
	}
	_ = json.Unmarshal(e.do(http.MethodPatch, patch, "/api/v1/settings", "application/json", strings.NewReader(
		`{"documents.external_ai.openai.enabled": true, "documents.retention_days": 30}`), http.StatusOK), &s)
	if !*s.DocumentsExternalAiOpenaiEnabled || *s.DocumentsExternalAiGeminiEnabled || string(s.DocumentsRetentionDays) != "30" {
		t.Errorf("after patch: %+v", s)
	}
	_ = json.Unmarshal(e.do(http.MethodPatch, patch, "/api/v1/settings", "application/json", strings.NewReader(
		`{"documents.retention_days": null, "documents.delete_original_after_confirmation": true}`), http.StatusOK), &s)
	if string(s.DocumentsRetentionDays) != "null" || !*s.DocumentsDeleteOriginalAfterConfirmation || !*s.DocumentsExternalAiOpenaiEnabled {
		t.Errorf("null retention: %+v", s)
	}
	for _, body := range []string{`{"documents.retention_days": 0}`, `{"documents.retention_days": 2.5}`} {
		if p := e.problem(http.MethodPatch, patch, "/api/v1/settings", body, http.StatusUnprocessableEntity); p.Code != CodeValidationFailed {
			t.Errorf("%s: %s", body, p.Code)
		}
	}
	for action, want := range map[string]int{"documents.external_ai": 1, "documents.retention_policy": 2} {
		if n := audits(action); n != want {
			t.Errorf("%s: %d audit events, want %d", action, n, want)
		}
	}
}

// problem sends a JSON body and decodes the problem answer.
func (e *docEnv) problem(method, pattern, target, body string, want int) problem {
	e.t.Helper()
	var p problem
	ct := ""
	if body != "" {
		ct = "application/json"
	}
	_ = json.Unmarshal(e.do(method, pattern, target, ct, strings.NewReader(body), want), &p)
	return p
}
