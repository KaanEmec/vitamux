//go:build integration

package imports

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/connectors/garmin"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// garminEnv is an import env with the Garmin normalizers and a paused Garmin connection, as the
// sidecar sign-in leaves it.
func garminEnv(t *testing.T) (*importEnv, uuid.UUID) {
	e := newImportEnv(t)
	reg, err := normalize.NewRegistry(garmin.Normalizers()...)
	if err != nil {
		t.Fatal(err)
	}
	e.proc.Registry = reg
	conn := uuid.New()
	var ok int
	if err := e.scan(&ok, `INSERT INTO connections (id, user_id, provider_id, mode, status, account_key)
		VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'garmin'), 'remote', 'paused', sha256('synthetic-account')) RETURNING 1`,
		conn, e.user); err != nil {
		t.Fatal(err)
	}
	return e, conn
}

// writeBatches writes one batch file with a sleep day and an activity (the synthetic goldens'
// raw bodies) and the activity's FIT file as a blob item; it returns the directory.
func writeBatches(t *testing.T, conn uuid.UUID) string {
	t.Helper()
	dir := t.TempDir()
	read := func(p string) json.RawMessage {
		data, err := os.ReadFile(filepath.Join("..", "connectors", "garmin", "testdata", p))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	fit := []byte("synthetic FIT archive")
	sum := sha256.Sum256(fit)
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blobs", hex.EncodeToString(sum[:])), fit, 0o600); err != nil {
		t.Fatal(err)
	}
	src := "garmin_collector_archive"
	b := ingest.Batch{Schema: ingest.BatchSchema, ConnectionID: ingest.FormatConnectionID(conn),
		Client:     ingest.Client{Kind: "importer", Name: "synthetic-archive", Version: "1"},
		Provenance: &ingest.Provenance{MigrationSource: &src},
		Items: []ingest.Item{
			{Stream: "garmin.sleep", ExternalKey: "garmin.sleep:2026-06-15", FetchedAt: "2026-06-15T09:00:00Z",
				ContentType: "application/json", Body: read("garmin.sleep/night.raw.json"),
				Request: &ingest.ItemRequest{Endpoint: "/wellness-service/wellness/dailySleepData/synthetic", Params: json.RawMessage(`{"date":"2026-06-15"}`)}},
			{Stream: "garmin.activities", ExternalKey: "garmin.activities:90000000001", FetchedAt: "2026-06-15T09:00:00Z",
				ContentType: "application/json", Body: read("garmin.activities/run.raw.json")},
			{Stream: "garmin.activities", ExternalKey: "garmin.activities:90000000001:fit", FetchedAt: "2026-06-15T09:00:00Z",
				ContentType: "application/zip", BlobSHA256: hex.EncodeToString(sum[:])},
		}}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "batch-00001.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestImportBatches(t *testing.T) {
	e, conn := garminEnv(t)
	dir := writeBatches(t, conn)
	run := func(dry bool) BatchesReport {
		t.Helper()
		rep, err := ImportBatches(t.Context(), dir, BatchesOptions{Processor: e.proc, DryRun: dry})
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}

	dry := run(true)
	if dry.Items != 3 || len(dry.Failures) != 0 || dry.Streams["garmin.sleep"].Records == 0 || dry.Streams["garmin.activities"].Records == 0 {
		t.Fatalf("dry run: %+v", dry)
	}
	for _, table := range []string{"raw_payloads", "ingest_batches", "import_runs", "sleep_sessions"} {
		if n := e.int(`SELECT count(*) FROM ` + table); n != 0 {
			t.Errorf("dry run wrote %d %s rows", n, table)
		}
	}

	first := run(false)
	if first.New != 3 || first.Duplicates != 0 || first.Normalized.Failed != 0 || first.Normalized.Inserted == 0 {
		t.Fatalf("import: %+v", first)
	}
	if n := e.int(`SELECT count(*) FROM raw_payloads WHERE connection_id = $1 AND status = 'normalized'`, conn); n != 3 {
		t.Errorf("%d normalized raw rows on the connection, want 3", n)
	}
	if n := e.int(`SELECT count(*) FROM ingest_batches WHERE connection_id = $1 AND source_kind = 'import'
		AND migration_source = 'garmin_collector_archive'`, conn); n != 1 {
		t.Errorf("%d migration batches, want 1", n)
	}
	if e.int(`SELECT count(*) FROM sleep_sessions`) == 0 || e.int(`SELECT count(*) FROM workouts`) != 1 {
		t.Error("the sleep night or the activity was not normalized")
	}

	// Running it again stores and normalizes nothing, and leaves no empty batch behind.
	before := e.counts()
	again := run(false)
	if again.New != 0 || again.Duplicates != 3 || again.Normalized != (normalize.Summary{}) {
		t.Errorf("second import: %+v", again)
	}
	if after := e.counts(); after["raw_payloads"] != before["raw_payloads"] || after["sleep_sessions"] != before["sleep_sessions"] ||
		e.int(`SELECT count(*) FROM ingest_batches`) != 1 {
		t.Errorf("second import changed rows: %v → %v", before, after)
	}
	if n := e.int(`SELECT count(*) FROM import_runs WHERE source = 'batches' AND connection_id = $1`, conn); n != 2 {
		t.Errorf("%d import runs, want 2", n)
	}
}

func TestImportBatchesRejects(t *testing.T) {
	e, conn := garminEnv(t)
	for name, tc := range map[string]struct {
		edit func(dir string)
		want string
	}{
		"unknown connection": {func(dir string) {
			p := filepath.Join(dir, "batch-00001.json")
			data, _ := os.ReadFile(p)
			other := ingest.FormatConnectionID(uuid.New())
			_ = os.WriteFile(p, []byte(strings.ReplaceAll(string(data), ingest.FormatConnectionID(conn), other)), 0o600)
		}, "not a connection of the owner"},
		"blob hash mismatch": {func(dir string) {
			matches, _ := filepath.Glob(filepath.Join(dir, "blobs", "*"))
			_ = os.WriteFile(matches[0], []byte("other content"), 0o600)
		}, "does not match its name"},
		"invalid batch": {func(dir string) {
			_ = os.WriteFile(filepath.Join(dir, "batch-00000.json"), []byte(`{"schema": "vitamux.ingest.batch/1"}`), 0o600)
		}, "batch-00000.json"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeBatches(t, conn)
			tc.edit(dir)
			_, err := ImportBatches(t.Context(), dir, BatchesOptions{Processor: e.proc})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if n := e.int(`SELECT count(*) FROM raw_payloads`); n != 0 {
		t.Errorf("rejected imports stored %d raw rows", n)
	}
}
