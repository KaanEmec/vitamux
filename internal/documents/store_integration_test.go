//go:build integration

package documents

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

// marker is synthetic content that must never appear outside the sealed PDF.
const marker = "SYNTHETIC-LAB-MARKER-7f3a"

type env struct {
	t     *testing.T
	s     *Store
	d     *db.DB
	kr    *crypto.Keyring
	keyAt string
	user  uuid.UUID
	exec  func(sql string, args ...any)
	scan  func(dest any, sql string, args ...any)
}

func newEnv(t *testing.T) *env {
	t.Helper()
	u, app := dbtest.Migrated(t)
	owner := dbtest.Pool(t, u, db.OwnerRole)
	e := &env{t: t, d: db.New(app), user: uuid.New(), keyAt: filepath.Join(t.TempDir(), "master.key")}
	e.exec = func(sql string, args ...any) {
		t.Helper()
		if _, err := owner.Exec(context.Background(), sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	e.scan = func(dest any, sql string, args ...any) {
		t.Helper()
		if err := owner.QueryRow(context.Background(), sql, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	e.exec(`INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')`, e.user)
	if _, err := crypto.WriteKeyFile(e.keyAt); err != nil {
		t.Fatal(err)
	}
	var err error
	if e.kr, err = crypto.Load(e.keyAt); err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.Open(filepath.Join(t.TempDir(), "blobs"), e.kr)
	if err != nil {
		t.Fatal(err)
	}
	e.s = New(e.d, blobs, e.kr)
	return e
}

func (e *env) upload(pdf []byte, name string) (Document, bool) {
	e.t.Helper()
	d, existing, err := e.s.Upload(context.Background(), e.user, audit.Owner, name, bytes.NewReader(pdf))
	if err != nil {
		e.t.Fatal(err)
	}
	return d, existing
}

func TestUploadDedupeAndRead(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	pdf := testPDF(3, true, false, marker)
	d, existing := e.upload(pdf, "report-2026.pdf")
	if existing || d.Pages != 3 || d.Status != StatusUploaded || d.Filename != "report-2026.pdf" || d.SizeBytes != int64(len(pdf)) {
		t.Fatalf("upload: %+v existing=%v", d, existing)
	}
	again, existing := e.upload(pdf, "other-name.pdf")
	if !existing || again.ID != d.ID || again.Filename != "report-2026.pdf" {
		t.Fatalf("re-upload: %+v existing=%v", again, existing)
	}
	var n int
	e.scan(&n, `SELECT count(*) FROM documents`)
	if n != 1 {
		t.Fatalf("%d documents after a duplicate upload", n)
	}
	got, err := e.s.File(ctx, e.user, d.ID)
	if err != nil || !bytes.Equal(got, pdf) {
		t.Fatalf("file: %d bytes, %v", len(got), err)
	}
	// Another user's upload of the same content is a separate document.
	other := uuid.New()
	e.exec(`INSERT INTO users (id, username, password_hash) VALUES ($1, 'other', 'synthetic')`, other)
	od, existing, err := e.s.Upload(ctx, other, audit.Owner, "", bytes.NewReader(pdf))
	if err != nil || existing || od.ID == d.ID || od.Filename != "" {
		t.Fatalf("other user: %+v %v %v", od, existing, err)
	}
	if _, err := e.s.Get(ctx, other, d.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("cross-user get: %v", err)
	}
	list, err := e.s.List(ctx, e.user, nil, 10)
	if err != nil || len(list) != 1 || list[0].ID != d.ID {
		t.Fatalf("list: %+v %v", list, err)
	}
	// The blob holds ciphertext only: neither the PDF nor its hash is visible in storage.
	var blobSum []byte
	e.scan(&blobSum, `SELECT blob_sha256 FROM documents WHERE id = $1`, d.ID)
	stored, err := e.s.blobs.Get(blobSum)
	if err != nil || bytes.Contains(stored, []byte(marker)) || bytes.Equal(blobSum, d.SHA256) {
		t.Fatalf("stored blob is not sealed (err %v)", err)
	}
}

func TestUploadRejects(t *testing.T) {
	e := newEnv(t)
	e.s.limits = Limits{MaxBytes: 2048, MaxPages: 3}
	for name, tc := range map[string]struct {
		pdf  []byte
		want Reason
	}{
		"not a pdf":      {[]byte("GIF89a" + marker), ReasonNotPDF},
		"encrypted":      {testPDF(1, false, true, marker), ReasonEncrypted},
		"too many pages": {testPDF(4, false, false, marker), ReasonTooManyPages},
		"oversized":      {append(testPDF(1, false, false, marker), make([]byte, 4096)...), ReasonTooLarge},
	} {
		_, _, err := e.s.Upload(context.Background(), e.user, audit.Owner, "x.pdf", bytes.NewReader(tc.pdf))
		var re *RejectError
		if !errors.As(err, &re) || re.Reason != tc.want || strings.Contains(err.Error(), marker) {
			t.Errorf("%s: %v, want %s", name, err, tc.want)
		}
	}
	var n int
	e.scan(&n, `SELECT count(*) FROM documents`)
	if n != 0 {
		t.Fatalf("%d documents stored from rejected uploads", n)
	}
}

// addReport inserts a confirmed report with one result for document id, as J12.4 would.
func (e *env) addReport(id uuid.UUID) {
	e.t.Helper()
	report := uuid.New()
	e.exec(`INSERT INTO lab_reports (id, user_id, document_id, provider, schema_version, prompt_version, confirmed_by)
		VALUES ($1, $2, $3, 'fake', 'vitamux.lab.extraction/1', 'lab-extraction/v1', 'owner')`, report, e.user, id)
	e.exec(`INSERT INTO lab_results (id, user_id, report_id, analyte_id, original_label, value_text, value_numeric, unit_text,
		canonical_value, canonical_unit, conversion_factor, conversion_offset, catalog_version, collected_date)
		VALUES ($1, $2, $3, (SELECT id FROM analytes WHERE code = 'glucose'), 'Glucose', '90', 90, 'mg/dL', 4.9959, 'mmol/L', 0.05551, 0, 1, '2026-09-01')`,
		uuid.New(), e.user, report)
}

func TestDeleteCryptoShreds(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	d, _ := e.upload(testPDF(2, false, false, marker), "secret-name.pdf")
	e.addReport(d.ID)
	run := uuid.New()
	e.exec(`INSERT INTO extraction_runs (id, document_id, user_id, provider, external, schema_version, prompt_version, created_by)
		VALUES ($1, $2, $3, 'fake', false, 'vitamux.lab.extraction/1', 'lab-extraction/v1', 'owner')`, run, d.ID, e.user)
	var sealedResponse []byte
	err := e.d.Tx(ctx, func(q *dbq.Queries) (err error) {
		sealedResponse, err = e.s.Seal(ctx, q, d.ID, "extraction_response:"+run.String(), []byte(marker))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var blobSum, sealedKey []byte
	e.scan(&blobSum, `SELECT blob_sha256 FROM documents WHERE id = $1`, d.ID)
	e.scan(&sealedKey, `SELECT ciphertext FROM document_keys WHERE document_id = $1`, d.ID)
	stored, err := e.s.blobs.Get(blobSum)
	if err != nil {
		t.Fatal(err)
	}

	res, err := e.s.Delete(ctx, e.user, d.ID, KeepDerived, audit.Owner)
	if err != nil || !res.Shredded || res.Reports != 0 {
		t.Fatalf("delete: %+v %v", res, err)
	}

	// The key is gone; with the master key alone nothing opens the PDF or sealed values.
	var keys int
	e.scan(&keys, `SELECT count(*) FROM document_keys WHERE document_id = $1`, d.ID)
	if keys != 0 {
		t.Fatal("document key survived deletion")
	}
	if _, err := e.s.File(ctx, e.user, d.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("file after delete: %v", err)
	}
	if _, err := e.s.Open(ctx, e.d.Q(), d.ID, "extraction_response:"+run.String(), sealedResponse); !errors.Is(err, ErrShredded) {
		t.Fatalf("sealed value after delete: %v", err)
	}
	for _, aad := range [][]byte{fieldAAD(d.ID, "pdf"), keyAAD(d.ID), nil} {
		if _, err := e.kr.Open(crypto.Documents, stored, aad); err == nil {
			t.Fatal("the master key opened the stored PDF")
		}
	}
	// Only the deleted key row could unwrap the data key: the stored blob is unreadable without it.
	key, err := e.kr.Open(crypto.Documents, sealedKey, keyAAD(d.ID))
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := openWith(key, stored, fieldAAD(d.ID, "pdf")); err != nil || !bytes.Contains(pt, []byte(marker)) {
		t.Fatal("test setup: the captured key should open the blob")
	}

	got, err := e.s.Get(ctx, e.user, d.ID)
	if err != nil || got.Status != StatusDeleted || got.SHA256 != nil || got.Filename != "" || got.DeletedAt == nil {
		t.Fatalf("tombstone: %+v %v", got, err)
	}
	var runs, reports int
	e.scan(&runs, `SELECT count(*) FROM extraction_runs WHERE document_id = $1`, d.ID)
	e.scan(&reports, `SELECT count(*) FROM lab_reports WHERE document_id = $1`, d.ID)
	if runs != 0 || reports != 1 {
		t.Fatalf("derived=keep: %d runs, %d reports", runs, reports)
	}
	var refcount int
	e.scan(&refcount, `SELECT refcount FROM blobs WHERE sha256 = $1`, blobSum)
	if refcount != 0 {
		t.Fatalf("blob refcount %d after delete", refcount)
	}
	// A swept blob is gone from disk as well.
	e.exec(`UPDATE blobs SET created_at = now() - interval '2 days' WHERE sha256 = $1`, blobSum)
	if _, err := blob.Sweep(ctx, e.d, e.s.blobs, blob.DefaultGrace); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.blobs.Get(blobSum); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("blob after sweep: %v", err)
	}

	// Deleting again with derived=delete removes the kept results.
	res, err = e.s.Delete(ctx, e.user, d.ID, DeleteDerived, audit.Owner)
	if err != nil || res.Shredded || res.Reports != 1 || res.Results != 1 {
		t.Fatalf("delete derived: %+v %v", res, err)
	}

	// Re-uploading the same content after deletion stores a new document.
	again, existing := e.upload(testPDF(2, false, false, marker), "")
	if existing || again.ID == d.ID {
		t.Fatalf("re-upload after delete: %+v %v", again, existing)
	}

	// Audit records hold counts and ids, never content.
	var events string
	e.scan(&events, `SELECT string_agg(action || ' ' || coalesce(target_id, '') || ' ' || detail::text, E'\n') FROM audit_events`)
	for _, secret := range []string{marker, "secret-name", hex.EncodeToString(d.SHA256)} {
		if strings.Contains(events, secret) {
			t.Fatalf("audit holds document content %q:\n%s", secret, events)
		}
	}
	if !strings.Contains(events, "document.delete") || !strings.Contains(events, `"derived": "keep"`) {
		t.Fatalf("audit lacks the deletion:\n%s", events)
	}
}

func TestRetention(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	old, _ := e.upload(testPDF(1, false, false, "old"), "")
	fresh, _ := e.upload(testPDF(1, false, false, "fresh"), "")
	confirmed, _ := e.upload(testPDF(1, false, false, "confirmed"), "")
	e.addReport(old.ID)
	e.exec(`UPDATE documents SET uploaded_at = now() - interval '40 days' WHERE id = $1`, old.ID)
	e.exec(`UPDATE documents SET status = 'confirmed' WHERE id = $1`, confirmed.ID)

	days := 30
	if err := SetPolicy(ctx, e.d, e.user, audit.Owner, Policy{RetentionDays: &days}); err != nil {
		t.Fatal(err)
	}
	if got, err := GetPolicy(ctx, e.d.Q(), e.user); err != nil || got.RetentionDays == nil || *got.RetentionDays != 30 || got.DeleteOriginalAfterConfirmation {
		t.Fatalf("policy: %+v %v", got, err)
	}
	if d, _ := e.s.Get(ctx, e.user, fresh.ID); d.RetentionUntil == nil || d.RetentionUntil.Before(time.Now().Add(29*24*time.Hour)) {
		t.Fatalf("retention_until not applied: %+v", d.RetentionUntil)
	}
	job := e.s.RetentionJob(slog.New(slog.DiscardHandler))
	if err := job(ctx, jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	status := func(id uuid.UUID) string {
		d, err := e.s.Get(ctx, e.user, id)
		if err != nil {
			t.Fatal(err)
		}
		return d.Status
	}
	if status(old.ID) != StatusDeleted || status(fresh.ID) != StatusUploaded || status(confirmed.ID) != StatusConfirmed {
		t.Fatalf("after retention: old %s, fresh %s, confirmed %s", status(old.ID), status(fresh.ID), status(confirmed.ID))
	}
	var reports int
	e.scan(&reports, `SELECT count(*) FROM lab_reports WHERE document_id = $1`, old.ID)
	if reports != 1 {
		t.Fatal("retention deleted derived results")
	}

	// Delete originals after confirmation; dropping the period clears retention_until.
	if err := SetPolicy(ctx, e.d, e.user, audit.Owner, Policy{DeleteOriginalAfterConfirmation: true}); err != nil {
		t.Fatal(err)
	}
	if err := job(ctx, jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if status(confirmed.ID) != StatusDeleted || status(fresh.ID) != StatusUploaded {
		t.Fatalf("after confirmation policy: confirmed %s, fresh %s", status(confirmed.ID), status(fresh.ID))
	}
	if d, _ := e.s.Get(ctx, e.user, fresh.ID); d.RetentionUntil != nil {
		t.Fatal("retention_until kept after the period was removed")
	}
	bad := 0
	if err := SetPolicy(ctx, e.d, e.user, audit.Owner, Policy{RetentionDays: &bad}); err == nil {
		t.Fatal("accepted retention_days 0")
	}
}

func TestRotateDocumentKeys(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	pdf := testPDF(1, false, false, marker)
	d, _ := e.upload(pdf, "a.pdf")

	newKey := filepath.Join(t.TempDir(), "new.key")
	if _, err := crypto.WriteKeyFile(newKey); err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.Load(newKey, e.keyAt)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.d.Tx(ctx, func(q *dbq.Queries) (err error) { n, err = RotateKeyBatch(ctx, q, kr, 10); return }); err != nil || n != 1 {
		t.Fatalf("rotate: %d, %v", n, err)
	}
	var keyID string
	e.scan(&keyID, `SELECT key_id FROM document_keys WHERE document_id = $1`, d.ID)
	if keyID != kr.KeyID() {
		t.Fatal("key not re-sealed under the new master key")
	}
	// Only the new key is needed from now on.
	only, err := crypto.Load(newKey)
	if err != nil {
		t.Fatal(err)
	}
	s := New(e.d, e.s.blobs, only)
	if got, err := s.File(ctx, e.user, d.ID); err != nil || !bytes.Equal(got, pdf) {
		t.Fatalf("file after rotation: %v", err)
	}
}
