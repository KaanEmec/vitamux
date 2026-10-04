//go:build integration

package extract

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/documents"
	"github.com/KaanEmec/vitamux/internal/httpx"
	"github.com/KaanEmec/vitamux/internal/jobs"
	"github.com/KaanEmec/vitamux/internal/obs"
)

type env struct {
	t     *testing.T
	svc   *Service
	d     *db.DB
	blobs *blob.Store
	docs  *documents.Store
	user  uuid.UUID
	logs  *bytes.Buffer
	scan  func(dest any, sql string, args ...any)
	exec  func(sql string, args ...any)
}

// newEnv builds a Service with the fake extractor plus extra, logging through the redacting
// handler into env.logs at debug level.
func newEnv(t *testing.T, extra ...Extractor) *env {
	t.Helper()
	u, app := dbtest.Migrated(t)
	owner := dbtest.Pool(t, u, db.OwnerRole)
	e := &env{t: t, d: db.New(app), user: uuid.New(), logs: &bytes.Buffer{}}
	e.scan = func(dest any, sql string, args ...any) {
		t.Helper()
		if err := owner.QueryRow(t.Context(), sql, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	e.exec = func(sql string, args ...any) {
		t.Helper()
		if _, err := owner.Exec(t.Context(), sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	if _, err := owner.Exec(t.Context(), `INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')`, e.user); err != nil {
		t.Fatal(err)
	}
	keyAt := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.WriteKeyFile(keyAt); err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.Load(keyAt)
	if err != nil {
		t.Fatal(err)
	}
	if e.blobs, err = blob.Open(filepath.Join(t.TempDir(), "blobs"), kr); err != nil {
		t.Fatal(err)
	}
	log := slog.New(obs.NewRedactingHandler(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if e.svc, err = New(e.d, e.blobs, kr, log, extra...); err != nil {
		t.Fatal(err)
	}
	e.docs = documents.New(e.d, e.blobs, kr)
	return e
}

func (e *env) upload(pdf []byte) uuid.UUID {
	e.t.Helper()
	d, _, err := e.docs.Upload(e.t.Context(), e.user, audit.Owner, "report.pdf", bytes.NewReader(pdf))
	if err != nil {
		e.t.Fatal(err)
	}
	return d.ID
}

// handle runs the job of run like the runner would, as attempt n of maxAttempts.
func (e *env) handle(run Run, n int32) error {
	e.t.Helper()
	p, _ := json.Marshal(payload{RunID: run.ID})
	return e.svc.Handle(e.t.Context(), jobs.Job{Kind: Kind, Payload: p, Attempt: n, MaxAttempts: maxAttempts})
}

func (e *env) run(doc uuid.UUID) Run {
	e.t.Helper()
	runs, err := e.svc.List(e.t.Context(), e.user, doc)
	if err != nil || len(runs) == 0 {
		e.t.Fatalf("runs: %v (%d)", err, len(runs))
	}
	return runs[0]
}

func (e *env) docStatus(doc uuid.UUID) string {
	e.t.Helper()
	d, err := e.docs.Get(e.t.Context(), e.user, doc)
	if err != nil {
		e.t.Fatal(err)
	}
	return d.Status
}

// minimalPDF is a one-page synthetic PDF that no fixture matches; salt makes it unique.
func minimalPDF(salt string) []byte {
	return []byte("%PDF-1.4\n%" + salt + "\n1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n" +
		"2 0 obj << /Type /Pages /Count 1 >> endobj\n3 0 obj << /Type /Page /Parent 2 0 R >> endobj\n" +
		"trailer << /Root 1 0 R >>\n%%EOF\n")
}

// fixturePDFs generates the synthetic lab PDFs (they are git-ignored) and returns their directory.
func fixturePDFs(t *testing.T) string {
	t.Helper()
	out := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "go", "run", "./tools/fixturegen", "labpdf", "-out", out, "-truth", filepath.Join(out, "truth"))
	cmd.Dir = "../../.."
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixturegen: %v\n%s", err, b)
	}
	return out
}

// TestFakeEndToEnd: upload a fixture PDF, extract with the fake provider, and get exactly the
// ground truth rows, a sealed raw response, needs_review, and a clean deletion.
func TestFakeEndToEnd(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	dir := fixturePDFs(t)
	for _, id := range []string{"lab-01", "lab-07"} {
		pdf, err := os.ReadFile(filepath.Join(dir, id+".pdf"))
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile("../../../fixtures/lab/" + id + ".json")
		if err != nil {
			t.Fatal(err)
		}
		truth, err := documents.DecodeExtraction(want)
		if err != nil {
			t.Fatal(err)
		}
		doc := e.upload(pdf)
		run, err := e.svc.Start(ctx, e.user, doc, audit.Owner, Fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != "queued" || run.External || run.Consent != nil || run.PromptVersion != documents.PromptVersion || e.docStatus(doc) != documents.StatusExtracting {
			t.Fatalf("%s: queued run %+v", id, run)
		}
		if err := e.handle(run, 1); err != nil {
			t.Fatal(err)
		}
		run = e.run(doc)
		if run.Status != "succeeded" || run.RowCount != len(truth.Rows) || e.docStatus(doc) != documents.StatusNeedsReview {
			t.Fatalf("%s: run %s rows %d (want %d), doc %s", id, run.Status, run.RowCount, len(truth.Rows), e.docStatus(doc))
		}
		var meta documents.DocumentMeta
		if err := json.Unmarshal(run.DocMeta, &meta); err != nil || *meta.Laboratory != *truth.Document.Laboratory || meta.PageCount != truth.Document.PageCount {
			t.Errorf("%s: doc meta %s", id, run.DocMeta)
		}

		// Rows match the ground truth field by field.
		var got []documents.Row
		e.scan(&got, `SELECT coalesce(json_agg(json_build_object('page', page, 'row_index', row_index, 'analyte_label', analyte_label,
			'value_text', value_text, 'value_numeric', value_numeric, 'comparator', comparator, 'unit_text', unit_text,
			'reference_range_text', reference_range_text, 'ref_low', ref_low, 'ref_high', ref_high, 'printed_flag', abnormal_flag_printed,
			'specimen_type', specimen_type, 'collected_at', collected_at, 'reported_at', reported_at, 'laboratory', laboratory,
			'evidence_text', evidence_text, 'bbox', bbox, 'confidence', confidence, 'warnings', warnings) ORDER BY row_index), '[]')
			FROM lab_extracted_rows WHERE run_id = $1`, run.ID)
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(truth.Rows)
		if !bytes.Equal(gotJSON, wantJSON) {
			t.Errorf("%s: rows differ from the ground truth\n got %s\nwant %s", id, gotJSON, wantJSON)
		}

		// The raw response is sealed with the document key: the blob hides it, Open restores it.
		var sum []byte
		e.scan(&sum, `SELECT response_blob_sha256 FROM extraction_runs WHERE id = $1`, run.ID)
		sealed, err := e.blobs.Get(sum)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(sealed, []byte(*truth.Document.Laboratory)) {
			t.Error("raw response stored in the clear")
		}
		raw, err := e.docs.Open(ctx, e.d.Q(), doc, "extraction_response:"+run.ID.String(), sealed)
		if err != nil || !bytes.Equal(raw, want) {
			t.Errorf("%s: sealed response does not open to the provider answer: %v", id, err)
		}

		// Deleting the document drops the runs and releases the response blob.
		if _, err := e.docs.Delete(ctx, e.user, doc, documents.KeepDerived, audit.Owner); err != nil {
			t.Fatal(err)
		}
		var refs int
		e.scan(&refs, `SELECT refcount FROM blobs WHERE sha256 = $1`, sum)
		if runs, err := e.svc.List(ctx, e.user, doc); err != nil || len(runs) != 0 || refs != 0 {
			t.Errorf("%s: after delete: %d runs, refcount %d, %v", id, len(runs), refs, err)
		}
	}
}

func TestFakeUnknownPDFFailsClearly(t *testing.T) {
	e := newEnv(t)
	doc := e.upload(minimalPDF("one"))
	run, err := e.svc.Start(t.Context(), e.user, doc, audit.Owner, Fake, nil)
	if err != nil {
		t.Fatal(err)
	}
	var perm interface{ Unwrap() error }
	if err := e.handle(run, 1); !errors.As(err, &perm) {
		t.Fatalf("want a permanent job error, got %v", err)
	}
	run = e.run(doc)
	if run.Status != "failed" || run.ErrorClass == nil || *run.ErrorClass != ClassUnknownDocument || run.FinishedAt == nil {
		t.Fatalf("run %+v", run)
	}
	if e.docStatus(doc) != documents.StatusUploaded {
		t.Errorf("document %s, want uploaded", e.docStatus(doc))
	}
	// A failed run does not block a new one once its job has ended.
	e.exec(`UPDATE jobs SET status = 'dead'`)
	if _, err := e.svc.Start(t.Context(), e.user, doc, audit.Owner, Fake, nil); err != nil {
		t.Errorf("re-extraction: %v", err)
	}
}

func TestConsentEnforcement(t *testing.T) {
	ctx := t.Context()
	g := NewGemini(GeminiConfig{APIKey: sentinelKey, Model: "gemini-test", BaseURL: "http://127.0.0.1:1", Client: httpx.New(httpx.Options{})})
	e := newEnv(t, g)
	doc := e.upload(minimalPDF("one"))
	now := time.Now()
	good := &Consent{Provider: Gemini, Model: "gemini-test", AcknowledgedAt: now}
	start := func(provider string, c *Consent) error {
		_, err := e.svc.Start(ctx, e.user, doc, audit.Owner, provider, c)
		return err
	}
	for name, tc := range map[string]struct {
		provider string
		consent  *Consent
		want     error
	}{
		"unknown provider":     {"acme", good, ErrUnknownProvider},
		"not configured":       {OpenAI, &Consent{Provider: OpenAI, Model: "x", AcknowledgedAt: now}, ErrNotConfigured},
		"disabled":             {Gemini, good, ErrDisabled},
		"disabled, no consent": {Gemini, nil, ErrDisabled},
	} {
		if err := start(tc.provider, tc.consent); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	if err := SetEnabled(ctx, e.d, e.user, audit.Owner, Gemini, true); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		consent *Consent
		want    error
	}{
		"no consent":       {nil, ErrConsentRequired},
		"no timestamp":     {&Consent{Provider: Gemini, Model: "gemini-test"}, ErrConsentRequired},
		"future timestamp": {&Consent{Provider: Gemini, Model: "gemini-test", AcknowledgedAt: now.Add(time.Hour)}, ErrConsentRequired},
		"other model":      {&Consent{Provider: Gemini, Model: "gemini-other", AcknowledgedAt: now}, ErrConsentMismatch},
		"other provider":   {&Consent{Provider: OpenAI, Model: "gemini-test", AcknowledgedAt: now}, ErrConsentMismatch},
	} {
		if err := start(Gemini, tc.consent); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	var runs int
	e.scan(&runs, `SELECT count(*) FROM extraction_runs`)
	if runs != 0 {
		t.Fatalf("refused requests created %d runs", runs)
	}
	run, err := e.svc.Start(ctx, e.user, doc, "api_key:test", Gemini, good)
	if err != nil {
		t.Fatal(err)
	}
	if !run.External || run.Consent == nil || run.Consent.Model != "gemini-test" || !run.Consent.AcknowledgedAt.Equal(now.UTC().Truncate(time.Microsecond)) && !run.Consent.AcknowledgedAt.Equal(now.UTC()) || run.CreatedBy != "api_key:test" {
		t.Errorf("consent not recorded: %+v", run.Consent)
	}
	if err := start(Gemini, good); !errors.Is(err, ErrRunning) {
		t.Errorf("second start: %v, want ErrRunning", err)
	}
	// Disabling after queueing fails the run when it starts; the PDF never leaves.
	if err := SetEnabled(ctx, e.d, e.user, audit.Owner, Gemini, false); err != nil {
		t.Fatal(err)
	}
	if err := e.handle(run, 1); err == nil {
		t.Fatal("disabled provider ran")
	}
	if r := e.run(doc); r.Status != "failed" || *r.ErrorClass != ClassProviderDisabled {
		t.Errorf("run %s %v", r.Status, r.ErrorClass)
	}
	var actions string
	e.scan(&actions, `SELECT string_agg(action, ',' ORDER BY id) FROM audit_events`)
	if !strings.Contains(actions, "documents.external_ai") || !strings.Contains(actions, "document.extract") {
		t.Errorf("audit: %s", actions)
	}
}

// TestGeminiJobNeverLeaksTheKey runs real jobs against an httptest Gemini: success, a retried
// 503 and a final rejection that echoes the key. The key appears in no log, audit event, run
// or job row.
func TestGeminiJobNeverLeaksTheKey(t *testing.T) {
	ctx := t.Context()
	truthJSON := truth(t)
	replies := []func(http.ResponseWriter){
		jsonReply(503, map[string]any{"error": map[string]any{"message": "overloaded " + sentinelKey}}),
		jsonReply(200, geminiOK(truthJSON)),
		jsonReply(401, map[string]any{"error": map[string]any{"message": "API key not valid: " + sentinelKey, "status": "UNAUTHENTICATED"}}),
	}
	n := 0
	_, srv := provider(t, func(w http.ResponseWriter) { replies[n](w); n++ })
	var logs bytes.Buffer
	client := httpx.New(httpx.Options{Logger: slog.New(obs.NewRedactingHandler(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))})
	e := newEnv(t, NewGemini(GeminiConfig{APIKey: sentinelKey, Model: "gemini-test", BaseURL: srv.URL, Client: client}))
	if err := SetEnabled(ctx, e.d, e.user, audit.Owner, Gemini, true); err != nil {
		t.Fatal(err)
	}
	consent := &Consent{Provider: Gemini, Model: "gemini-test", AcknowledgedAt: time.Now()}

	doc := e.upload(minimalPDF("one"))
	run, err := e.svc.Start(ctx, e.user, doc, audit.Owner, Gemini, consent)
	if err != nil {
		t.Fatal(err)
	}
	var retry *Error
	if err := e.handle(run, 1); !errors.As(err, &retry) || retry.Class != ClassTransient {
		t.Fatalf("attempt 1: %v", err)
	}
	if r := e.run(doc); r.Status != "queued" || *r.ErrorClass != ClassTransient {
		t.Fatalf("after a transient failure: %s %v", r.Status, r.ErrorClass)
	}
	if err := e.handle(run, 2); err != nil {
		t.Fatal(err)
	}
	r := e.run(doc)
	if r.Status != "succeeded" || r.RowCount != 13 || *r.Model != "gemini-test-001" || *r.ProviderRequestID != "gem-req-1" || r.ErrorClass != nil {
		t.Fatalf("run %+v", r)
	}
	var usage Usage
	if json.Unmarshal(r.Usage, &usage) != nil || usage.TotalTokens != 2000 {
		t.Errorf("usage %s", r.Usage)
	}

	doc2 := e.upload(minimalPDF("two"))
	run2, err := e.svc.Start(ctx, e.user, doc2, audit.Owner, Gemini, consent)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.handle(run2, 1); err == nil {
		t.Fatal("401 did not fail the job")
	}
	if r := e.run(doc2); r.Status != "failed" || *r.ErrorClass != ClassAuth || e.docStatus(doc2) != documents.StatusUploaded {
		t.Fatalf("after 401: %s %v doc %s", r.Status, r.ErrorClass, e.docStatus(doc2))
	}

	var stored string
	e.scan(&stored, `SELECT concat_ws(' ', (SELECT string_agg(detail::text, ' ') FROM audit_events),
		(SELECT string_agg(row_to_json(r)::text, ' ') FROM extraction_runs r),
		(SELECT string_agg(payload::text, ' ') FROM jobs), (SELECT string_agg(value::text, ' ') FROM settings))`)
	for what, s := range map[string]string{"service logs": e.logs.String(), "http logs": logs.String(), "database": stored} {
		if strings.Contains(s, sentinelKey) || strings.Contains(s, "not valid") {
			t.Errorf("%s leak the key or a provider message", what)
		}
	}
	if !strings.Contains(e.logs.String(), "error_class=auth") {
		t.Errorf("failure not logged with its class: %s", e.logs.String())
	}
}
