//go:build integration

package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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
	"github.com/KaanEmec/vitamux/internal/documents/extract"
	"github.com/KaanEmec/vitamux/internal/jobs"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

type env struct {
	t     *testing.T
	ctx   context.Context
	user  uuid.UUID
	exec  func(sql string, args ...any)
	count func(sql string, args ...any) int
	docs  *documents.Store
	ex    *extract.Service
	svc   *Service
	pdfs  string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	u, app := dbtest.Migrated(t)
	e := &env{t: t, ctx: context.Background(), user: uuid.New(), pdfs: fixturePDFs(t)}
	owner := dbtest.Pool(t, u, db.OwnerRole)
	e.exec = func(sql string, args ...any) {
		t.Helper()
		if _, err := owner.Exec(e.ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	e.count = func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := owner.QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return n
	}
	e.exec(`INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')`, e.user)
	keyAt := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.WriteKeyFile(keyAt); err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.Load(keyAt)
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.Open(filepath.Join(t.TempDir(), "blobs"), kr)
	if err != nil {
		t.Fatal(err)
	}
	d := db.New(app)
	e.docs = documents.New(d, blobs, kr)
	if e.ex, err = extract.New(d, blobs, kr, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	e.svc = New(d, e.docs)
	e.svc.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	if _, _, err := normalize.NewPeriods(d).Add(e.ctx, e.user, audit.Owner, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), "Europe/Berlin"); err != nil {
		t.Fatal(err)
	}
	return e
}

// extract uploads a fixture PDF (or finds it again) and runs a fake extraction to completion.
func (e *env) extract(id string) (doc, run uuid.UUID) {
	e.t.Helper()
	pdf, err := os.ReadFile(filepath.Join(e.pdfs, id+".pdf"))
	if err != nil {
		e.t.Fatal(err)
	}
	d, _, err := e.docs.Upload(e.ctx, e.user, audit.Owner, id+".pdf", bytes.NewReader(pdf))
	if err != nil {
		e.t.Fatal(err)
	}
	r, err := e.ex.Start(e.ctx, e.user, d.ID, audit.Owner, extract.Fake, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	p, _ := json.Marshal(map[string]any{"run_id": r.ID})
	if err := e.ex.Handle(e.ctx, jobs.Job{Kind: extract.Kind, Payload: p, Attempt: 1, MaxAttempts: 3}); err != nil {
		e.t.Fatal(err)
	}
	e.exec(`UPDATE jobs SET status = 'succeeded', finished_at = now() WHERE kind = $1`, extract.Kind) // as the runner would
	return d.ID, r.ID
}

func (e *env) get(run uuid.UUID) Extraction {
	e.t.Helper()
	x, err := e.svc.Get(e.ctx, e.user, run)
	if err != nil {
		e.t.Fatal(err)
	}
	return x
}

func (e *env) edit(run uuid.UUID, index int, patch string) Row {
	e.t.Helper()
	r, err := e.svc.EditRow(e.ctx, e.user, audit.Owner, run, index, json.RawMessage(patch))
	if err != nil {
		e.t.Fatalf("edit row %d with %s: %v", index, patch, err)
	}
	return r
}

func rowByLabel(t *testing.T, x Extraction, label string) Row {
	t.Helper()
	for _, r := range x.Rows {
		if r.AnalyteLabel == label {
			return r
		}
	}
	t.Fatalf("no row %q", label)
	return Row{}
}

func pointers(err error) []string {
	var inv *InvalidError
	if !errors.As(err, &inv) {
		return nil
	}
	var out []string
	for _, p := range inv.Problems {
		out = append(out, p.Pointer)
	}
	return out
}

// TestReviewAndConfirm: seeded model errors raise their warnings; confirmation needs every row
// reviewed; confirmed results trace to document, run, row, page, evidence and versions; a
// second confirmation after an edit revises a result; every mutation is audited without values.
func TestReviewAndConfirm(t *testing.T) {
	e := newEnv(t)
	doc, run := e.extract("lab-01")
	x := e.get(run)
	n := len(x.Rows)

	// Seeded model errors: a wrong decimal, a swapped unit and an invented row.
	e.exec(`UPDATE lab_extracted_rows SET value_numeric = 7.5 WHERE run_id = $1 AND analyte_label = 'Creatinine'`, run)
	e.exec(`UPDATE lab_extracted_rows SET unit_text = 'mIU/L' WHERE run_id = $1 AND analyte_label = 'Glucose'`, run)
	e.exec(`INSERT INTO lab_extracted_rows (run_id, row_index, page, analyte_label, value_text, value_numeric, unit_text, collected_at, evidence_text, confidence)
		VALUES ($1, $2, 1, 'Ferritin', '88', 88, 'ng/mL', '2025-07-17T07:02', 'Ferritin 88 ng/mL', 0.9)`, run, n)
	x = e.get(run)
	for label, want := range map[string]string{"Creatinine": WarnValueMismatch, "Glucose": WarnUnknownUnit, "Ferritin": WarnEvidenceUnverified} {
		if r := rowByLabel(t, x, label); !slices.Equal(r.Validation, []string{want}) || r.Suggested == "" || r.Analyte != r.Suggested {
			t.Errorf("%s: validation %v, suggestion %q", label, r.Validation, r.Suggested)
		}
	}
	for _, r := range x.Rows {
		if !slices.Contains([]string{"Creatinine", "Glucose", "Ferritin"}, r.AnalyteLabel) && len(r.Validation) > 0 &&
			!slices.Equal(r.Validation, []string{WarnUnknownAnalyte}) {
			t.Errorf("clean row %s: %v", r.AnalyteLabel, r.Validation)
		}
	}

	// Nothing reviewed yet: confirmation names every row.
	if _, err := e.svc.Confirm(e.ctx, e.user, audit.Owner, run); len(pointers(err)) != n+1 {
		t.Fatalf("unreviewed confirm: %v", err)
	}
	glucose, creatinine, ferritin := rowByLabel(t, x, "Glucose").RowIndex, rowByLabel(t, x, "Creatinine").RowIndex, n
	for i := range n + 1 {
		switch i {
		case creatinine:
			if r := e.edit(run, i, `{"value_numeric": 0.75}`); r.Status != Edited || len(r.Validation) != 0 || len(r.Edits) != 1 {
				t.Errorf("fixed decimal: %+v", r)
			}
		case glucose:
			e.edit(run, i, `{"unit_text": "mg/dL", "review": "accept"}`)
		case ferritin:
			if r := e.edit(run, i, `{"review": "reject"}`); r.Status != Rejected {
				t.Errorf("reject: %s", r.Status)
			}
		default:
			e.edit(run, i, `{"review": "accept"}`)
		}
	}
	if _, err := e.svc.EditRow(e.ctx, e.user, audit.Owner, run, 0, json.RawMessage(`{"value_numeric": "x", "nope": 1}`)); !slices.Equal(pointers(err), []string{"/nope", "/value_numeric"}) {
		t.Errorf("bad patch: %v", err)
	}

	res, err := e.svc.Confirm(e.ctx, e.user, audit.Owner, run)
	if err != nil || res.Created != n || res.Removed != 0 {
		t.Fatalf("confirm: %+v %v", res, err)
	}
	if st, _ := e.docs.Get(e.ctx, e.user, doc); st.Status != documents.StatusConfirmed {
		t.Errorf("document %s", st.Status)
	}
	results, err := e.svc.ListResults(e.ctx, e.user, ResultFilter{Limit: 100})
	if err != nil || len(results) != n {
		t.Fatalf("results: %d %v", len(results), err)
	}
	for _, r := range results {
		if r.DocumentID != doc || r.RunID == nil || *r.RunID != run || r.RowIndex == nil || r.Page == nil || r.EvidenceText == nil ||
			r.Provider != extract.Fake || r.PromptVersion != documents.PromptVersion || r.SchemaVersion != documents.ExtractionSchema ||
			r.CollectedDate != "2025-07-17" || r.CollectedAt == nil || !r.CollectedAt.Equal(time.Date(2025, 7, 17, 5, 2, 0, 0, time.UTC)) {
			t.Errorf("provenance of %s: %+v", r.OriginalLabel, r)
		}
		if r.OriginalLabel == "Glucose" && (r.Analyte == nil || *r.Analyte != "glucose" || r.CanonicalValue == nil ||
			*r.CanonicalUnit != "mmol/L" || *r.ConversionFactor != 0.05551 || *r.UnitText != "mg/dL" || r.ValueText != "100") {
			t.Errorf("glucose: %+v", r)
		}
	}

	// A later edit is applied by confirming again: the result gets revision 2, history keeps 1.
	bun := rowByLabel(t, x, "BUN").RowIndex
	e.edit(run, bun, `{"value_text": "11", "value_numeric": 11}`)
	if res, err = e.svc.Confirm(e.ctx, e.user, audit.Owner, run); err != nil || res.Revised != 1 || res.Created != 0 || res.Unchanged != n-1 {
		t.Fatalf("confirm again: %+v %v", res, err)
	}
	r := rowByLabel(t, e.get(run), "BUN")
	hist, err := e.svc.History(e.ctx, e.user, *r.ResultID)
	if err != nil || len(hist) != 2 || hist[0].Revision != 2 || hist[0].ValueText != "11" || hist[1].Revision != 1 || hist[1].ValueText != "10" {
		t.Fatalf("history: %+v %v", hist, err)
	}

	// Re-extraction: the new run sees the confirmed results as duplicates and cannot be
	// confirmed while the first run is.
	_, run2 := e.extract("lab-01")
	x2 := e.get(run2)
	if g := rowByLabel(t, x2, "Glucose"); !slices.Contains(g.Validation, WarnAlreadyConfirmed) {
		t.Errorf("duplicate: %v", g.Validation)
	}
	for i := range x2.Rows {
		e.edit(run2, i, `{"review": "accept"}`)
	}
	if _, err := e.svc.Confirm(e.ctx, e.user, audit.Owner, run2); !errors.Is(err, ErrOtherConfirmed) {
		t.Errorf("second run: %v", err)
	}

	// Unconfirm deletes the results and returns the document to review.
	if err := e.svc.Unconfirm(e.ctx, e.user, audit.Owner, run); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM lab_results WHERE user_id = $1`, e.user); n != 0 {
		t.Errorf("%d results after unconfirm", n)
	}
	if err := e.svc.Unconfirm(e.ctx, e.user, audit.Owner, run); !errors.Is(err, ErrNotConfirmed) {
		t.Errorf("unconfirm twice: %v", err)
	}
	if x := e.get(run); x.Status != "succeeded" {
		t.Errorf("run after unconfirm: %s", x.Status)
	}

	// Audit: one event per effective edit, two confirmations, one unconfirm; never values.
	edits := n + 1 + 1 + len(x2.Rows)
	for action, want := range map[string]int{"extraction_row.review": edits, "extraction.confirm": 2, "extraction.unconfirm": 1} {
		if got := e.count(`SELECT count(*) FROM audit_events WHERE action = $1`, action); got != want {
			t.Errorf("%s: %d events, want %d", action, got, want)
		}
	}
	if n := e.count(`SELECT count(*) FROM audit_events WHERE action LIKE 'extraction%' AND (detail::text LIKE '%mg/dL%' OR detail::text LIKE '%0.75%' OR detail::text LIKE '%Creatinine%')`); n != 0 {
		t.Errorf("%d audit events hold values", n)
	}
	if n := e.count(`SELECT count(*) FROM extraction_row_edits e JOIN lab_extracted_rows x ON x.id = e.row_id WHERE x.run_id = $1 AND e.changes::text LIKE '%"from": 7.5%'`, run); n != 1 {
		t.Errorf("the original value is not kept in the edit trail (%d)", n)
	}
}

// TestAmbiguousDateAndUnknownAnalyte: an ambiguous printed date blocks confirmation until the
// owner enters the date; an analyte set to unknown is confirmed with its printed values only.
func TestAmbiguousDateAndUnknownAnalyte(t *testing.T) {
	e := newEnv(t)
	_, run := e.extract("lab-07")
	x := e.get(run)
	for _, r := range x.Rows {
		if !slices.Contains(r.Validation, WarnDateAmbiguous) || !slices.Contains(r.Validation, WarnDateMissing) {
			t.Errorf("%s: %v", r.AnalyteLabel, r.Validation)
		}
		e.edit(run, r.RowIndex, `{"review": "accept"}`)
	}
	_, err := e.svc.Confirm(e.ctx, e.user, audit.Owner, run)
	if p := pointers(err); len(p) != len(x.Rows) || !strings.HasSuffix(p[0], "/collected_at") {
		t.Fatalf("confirm without dates: %v", err)
	}
	for _, r := range x.Rows {
		patch := `{"collected_at": "2025-09-10T09:31"}`
		if r.RowIndex == 0 {
			patch = `{"collected_at": "2025-09-10T09:31", "analyte": null}`
		}
		if got := e.edit(run, r.RowIndex, patch); slices.Contains(got.Validation, WarnDateAmbiguous) || slices.Contains(got.Validation, WarnDateMissing) {
			t.Errorf("%s after the date edit: %v", r.AnalyteLabel, got.Validation)
		}
	}
	if r := e.get(run).Rows[0]; r.Analyte != "" || r.Suggested == "" || !slices.Contains(r.Validation, WarnUnknownAnalyte) {
		t.Errorf("unknown analyte row: %+v", r)
	}
	// A corrected label brings its own suggestion unless the patch sets the analyte.
	if r := e.edit(run, 1, `{"analyte_label": "Free T3"}`); r.Analyte != "ft3" && r.Analyte != r.Suggested {
		t.Errorf("relabelled row: analyte %q, suggested %q", r.Analyte, r.Suggested)
	}
	if _, err := e.svc.Confirm(e.ctx, e.user, audit.Owner, run); err != nil {
		t.Fatal(err)
	}
	results, _ := e.svc.ListResults(e.ctx, e.user, ResultFilter{Limit: 100})
	start := time.Date(2025, 9, 10, 0, 0, 0, 0, time.UTC)
	if page, _ := e.svc.ListResults(e.ctx, e.user, ResultFilter{Start: &start, End: &start, Limit: 3}); len(page) != 3 {
		t.Errorf("filtered page: %d", len(page))
	}
	for _, r := range results {
		if r.CollectedDate != "2025-09-10" || !r.CollectedAt.Equal(time.Date(2025, 9, 10, 7, 31, 0, 0, time.UTC)) {
			t.Errorf("%s collected %s %v", r.OriginalLabel, r.CollectedDate, r.CollectedAt)
		}
		if r.OriginalLabel == x.Rows[0].AnalyteLabel && (r.Analyte != nil || r.CanonicalValue != nil || r.ValueText == "") {
			t.Errorf("unknown analyte result: %+v", r)
		}
	}
}
