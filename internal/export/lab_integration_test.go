//go:build integration

package export_test

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/export"
)

// addLab stores a confirmed lab report the way review would leave it: a live document with
// its key, a run with two rows (one rejected), a result with a revision, and an owner alias.
func (in *instance) addLab() (doc uuid.UUID) {
	doc, run, report, result := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	blobHash := bytes.Repeat([]byte{0xab}, 32)
	in.exec(`INSERT INTO blobs (sha256, size_bytes, stored_bytes, compression, key_id, refcount) VALUES ($1, 10, 10, 'none', 'k1', 1)`, blobHash)
	in.exec(`INSERT INTO documents (id, user_id, status, sha256, blob_sha256, filename_ciphertext, size_bytes, page_count, uploaded_by, retention_until)
		VALUES ($1, $2, 'confirmed', $3, $4, '\xdeadbeef', 10, 1, 'owner', now() + interval '30 days')`, doc, in.user, bytes.Repeat([]byte{0xcd}, 32), blobHash)
	in.exec(`INSERT INTO document_keys (document_id, ciphertext, key_id) VALUES ($1, '\x0102', 'k1')`, doc)
	in.exec(`INSERT INTO extraction_runs (id, document_id, user_id, status, provider, external, schema_version, prompt_version, response_blob_sha256, created_by)
		VALUES ($1, $2, $3, 'confirmed', 'fake', false, 's1', 'p1', $4, 'owner')`, run, doc, in.user, blobHash)
	in.exec(`INSERT INTO lab_extracted_rows (run_id, row_index, analyte_label, value_text, review_status) VALUES ($1, 0, 'Glucose', '5.1', 'accepted'), ($1, 1, 'Smudge', NULL, 'rejected')`, run)
	in.exec(`INSERT INTO extraction_row_edits (row_id, action, actor) SELECT id, 'accept', 'owner' FROM lab_extracted_rows WHERE run_id = $1`, run)
	in.exec(`INSERT INTO lab_reports (id, user_id, document_id, run_id, provider, schema_version, prompt_version, confirmed_by) VALUES ($1, $2, $3, $4, 'fake', 's1', 'p1', 'owner')`, report, in.user, doc, run)
	in.exec(`INSERT INTO lab_results (id, user_id, report_id, source_row_id, analyte_id, original_label, value_text, unit_text, reference_range_text, collected_date, revision)
		SELECT $1, $2, $3, id, (SELECT id FROM analytes WHERE code = 'glucose'), 'Glucose', '5.1', 'mmol/L', '3.9 - 5.5', '2025-03-01', 2
		FROM lab_extracted_rows WHERE run_id = $4 AND row_index = 0`, result, in.user, report, run)
	in.exec(`INSERT INTO lab_result_revisions (result_id, revision, snapshot, changed_by) VALUES ($1, 1, '{"value_text": "5.0"}', 'owner')`, result)
	in.exec(`INSERT INTO analyte_aliases (analyte_id, label, label_key, user_id, created_by) SELECT id, 'Blutzucker X', 'blutzucker x', $1, 'owner' FROM analytes WHERE code = 'glucose'`, in.user)
	return doc
}

// TestLabRoundTrip: lab metadata and confirmed results travel; the PDF, its filename, the
// document key and raw responses never do, and the imported document is a tombstone.
func TestLabRoundTrip(t *testing.T) {
	src := newInstance(t)
	src.createOwner()
	doc := src.addLab()
	path, _ := writeExport(t, src, nil, export.Options{Format: export.FormatNDJSON})

	files := readZip(t, path)
	for _, name := range []string{"documents.ndjson", "extraction_runs.ndjson", "lab_extracted_rows.ndjson", "extraction_row_edits.ndjson",
		"lab_reports.ndjson", "lab_results.ndjson", "lab_result_revisions.ndjson", "analyte_aliases.ndjson", "analytes.ndjson"} {
		if len(files[name]) == 0 {
			t.Errorf("%s missing or empty", name)
		}
	}
	for name, b := range files {
		for _, secret := range []string{"filename_ciphertext", "blob_sha256", "response_blob_sha256", `"sha256"`, "deadbeef", "document_keys"} {
			if name != "manifest.json" && bytes.Contains(b, []byte(secret)) {
				t.Errorf("%s contains %s", name, secret)
			}
		}
	}
	if bytes.Count(files["analyte_aliases.ndjson"], []byte("\n")) != 1 {
		t.Errorf("seeded aliases exported: %s", files["analyte_aliases.ndjson"])
	}

	dst := newInstance(t)
	dst.createOwner()
	st, err := importZip(t, dst, path, export.ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s := dst.str(`SELECT status || ' ' || (sha256 IS NULL) || ' ' || (filename_ciphertext IS NULL) || ' ' || (deleted_at IS NOT NULL) || ' ' || (retention_until IS NULL) FROM documents WHERE id = '` + doc.String() + `'`); s != "deleted true true true true" {
		t.Errorf("imported document: %s", s)
	}
	for sql, want := range map[string]int64{
		`SELECT count(*) FROM document_keys`:                                     0,
		`SELECT count(*) FROM extraction_runs`:                                   0,
		`SELECT count(*) FROM lab_reports WHERE run_id IS NULL AND user_id = $1`: 1,
		`SELECT count(*) FROM lab_results WHERE source_row_id IS NULL AND user_id = $1 AND revision = 2 AND value_text = '5.1'`: 1,
		`SELECT count(*) FROM lab_result_revisions`:               1,
		`SELECT count(*) FROM analyte_aliases WHERE user_id = $1`: 1,
	} {
		var n int64
		if err := dst.scan(sql, []any{&n}, argsFor(sql, dst.user)...); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("%s: %d, want %d", sql, n, want)
		}
	}
	if got := inserted(st); got["lab_results"] != 1 || got["extraction_runs"] != 0 {
		t.Errorf("inserted: %v", got)
	}
	// New aliases after the import must not collide with imported ids.
	dst.exec(`INSERT INTO analyte_aliases (analyte_id, label, label_key, user_id, created_by) SELECT id, 'After', 'after', $1, 'owner' FROM analytes WHERE code = 'glucose'`, dst.user)

	st, err = importZip(t, dst, path, export.ImportOptions{Merge: true})
	if err != nil {
		t.Fatal(err)
	}
	for table, n := range inserted(st) {
		if n != 0 {
			t.Errorf("merge of the same export inserted %d %s rows", n, table)
		}
	}
}

func argsFor(sql string, user uuid.UUID) []any {
	if bytes.Contains([]byte(sql), []byte("$1")) {
		return []any{user}
	}
	return nil
}

func readZip(t *testing.T, path string) map[string][]byte {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name], err = io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}
