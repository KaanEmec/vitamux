//go:build integration

package export_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/db/fixtureload"
	"github.com/KaanEmec/vitamux/internal/export"
)

// instance is one migrated test database. SQL helpers run as the owner role.
type instance struct {
	t    *testing.T
	d    *db.DB
	user uuid.UUID
	// run executes a statement, scan a one-row query into dest; load fills the database with
	// a fixturegen slice.
	run  func(sql string, args ...any) error
	scan func(sql string, dest []any, args ...any) error
	load func(dir string) (fixtureload.Stats, error)
}

func newInstance(t *testing.T) *instance {
	t.Helper()
	url, app := dbtest.Migrated(t)
	owner := dbtest.Pool(t, url, db.OwnerRole)
	return &instance{t: t, d: db.New(app),
		run: func(sql string, args ...any) error {
			_, err := owner.Exec(context.Background(), sql, args...)
			return err
		},
		scan: func(sql string, dest []any, args ...any) error {
			return owner.QueryRow(context.Background(), sql, args...).Scan(dest...)
		},
		load: func(dir string) (fixtureload.Stats, error) { return fixtureload.Load(t.Context(), app, dir) },
	}
}

func (in *instance) exec(sql string, args ...any) {
	in.t.Helper()
	if err := in.run(sql, args...); err != nil {
		in.t.Fatalf("%v\n%s", err, sql)
	}
}

func (in *instance) int(sql string, args ...any) int64 {
	in.t.Helper()
	var n int64
	if err := in.scan(sql, []any{&n}, args...); err != nil {
		in.t.Fatalf("%v\n%s", err, sql)
	}
	return n
}

func (in *instance) str(sql string) string {
	in.t.Helper()
	var s string
	if err := in.scan(sql, []any{&s}); err != nil {
		in.t.Fatalf("%v\n%s", err, sql)
	}
	return s
}

// createOwner gives an empty instance its owner, as `vitamux admin create-owner` would.
func (in *instance) createOwner() {
	in.user = uuid.Must(uuid.NewV7())
	in.exec(`INSERT INTO users (id, username, password_hash) VALUES ($1, 'target-owner', 'not-a-real-hash')`, in.user)
}

var (
	datasets   = map[string]string{}
	datasetsMu sync.Mutex
)

// dataset generates a synthetic fixturegen slice once per test binary.
func dataset(t *testing.T, start string, days int) string {
	t.Helper()
	datasetsMu.Lock()
	defer datasetsMu.Unlock()
	key := fmt.Sprintf("%s-%d", start, days)
	if dir, ok := datasets[key]; ok {
		return dir
	}
	dir, err := os.MkdirTemp("", "vitamux-export-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	cmd := exec.CommandContext(t.Context(), "go", "run", "./tools/fixturegen", "-out", dir, "-start", start, "-days", fmt.Sprint(days)) //nolint:gosec // fixed tool
	cmd.Dir = filepath.Join(filepath.Dir(file), "..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixturegen: %v\n%s", err, out)
	}
	datasets[key] = dir
	return dir
}

func TestMain(m *testing.M) {
	code := m.Run()
	for _, dir := range datasets {
		_ = os.RemoveAll(dir)
	}
	os.Exit(code)
}

// loaded is an instance holding the synthetic slice plus the history fixtureload leaves out.
func loaded(t *testing.T, start string, days int) *instance {
	t.Helper()
	in := newInstance(t)
	st, err := in.load(dataset(t, start, days))
	if err != nil {
		t.Fatal(err)
	}
	in.user = st.UserID
	return in
}

// addHistory adds what the round trip must also preserve: superseded chains, an upstream
// deletion, a raw version, a client, settings, rules, a workout and an import run.
func (in *instance) addHistory() {
	in.exec(`INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES ($1, $2, 'Europe/Berlin', '2024-01-01Z')`, uuid.Must(uuid.NewV7()), in.user)
	in.exec(`INSERT INTO settings (user_id, key, value) VALUES ($1, 'units', '{"weight": "kg"}')`, in.user)
	rule := uuid.Must(uuid.NewV7())
	in.exec(`INSERT INTO resolution_rules (id, user_id, metric, version, spec, created_by) VALUES ($1, $2, 'steps', 1, '{"strategy": "max"}', 'owner')`, rule, in.user)
	in.exec(`INSERT INTO active_rules (user_id, metric, version, activated_by) VALUES ($1, 'steps', 1, 'owner')`, in.user)
	in.exec(`INSERT INTO manual_overrides (id, user_id, metric, window_kind, window_key, local_date, action, input_id, created_by)
		VALUES ($1, $2, 'heart_rate', 'local_day', '2025-03-02', '2025-03-02', 'exclude_input', (SELECT min(id) FROM measurements), 'owner')`,
		uuid.Must(uuid.NewV7()), in.user)

	// A correction of a measurement, of a group and of a sleep session; one upstream deletion.
	in.exec(`UPDATE measurements SET superseded_at = now() WHERE id = (SELECT min(id) FROM measurements WHERE group_id IS NULL)`)
	in.exec(`INSERT INTO measurements (user_id, metric_id, kind, start_at, end_at, tz_offset_min, local_date, value, source_value, source_unit_id,
		provider_id, connection_id, device_id, origin_id, group_id, external_id, dedupe_key, quality_flags, raw_payload_id, normalizer_version_id)
		SELECT user_id, metric_id, kind, start_at, end_at, tz_offset_min, local_date, value + 1, source_value, source_unit_id,
		provider_id, connection_id, device_id, origin_id, group_id, external_id, dedupe_key, quality_flags, raw_payload_id, normalizer_version_id
		FROM measurements WHERE superseded_at IS NOT NULL`)
	in.exec(`UPDATE measurements o SET superseded_by = n.id FROM measurements n
		WHERE o.superseded_at IS NOT NULL AND n.dedupe_key = o.dedupe_key AND n.superseded_at IS NULL`)
	in.exec(`UPDATE measurements SET deleted_at = now(), deleted_by_raw_id = raw_payload_id WHERE id = (SELECT max(id) FROM measurements)`)
	in.exec(`UPDATE measurement_groups SET superseded_at = now() WHERE id = (SELECT min(id) FROM measurement_groups)`)
	in.exec(`INSERT INTO measurement_groups (user_id, kind, measured_at, tz_offset_min, local_date, context, provider_id, connection_id,
		device_id, origin_id, external_id, dedupe_key, raw_payload_id, normalizer_version_id)
		SELECT user_id, kind, measured_at, tz_offset_min, local_date, '{"corrected": true}', provider_id, connection_id,
		device_id, origin_id, external_id, dedupe_key, raw_payload_id, normalizer_version_id FROM measurement_groups WHERE superseded_at IS NOT NULL`)
	in.exec(`UPDATE measurement_groups o SET superseded_by = n.id FROM measurement_groups n
		WHERE o.superseded_at IS NOT NULL AND n.dedupe_key = o.dedupe_key AND n.superseded_at IS NULL`)
	in.exec(`UPDATE sleep_sessions SET superseded_at = now() WHERE id = (SELECT id FROM sleep_sessions ORDER BY id LIMIT 1)`)
	in.exec(`INSERT INTO sleep_sessions (id, user_id, start_at, end_at, tz_offset_min, sleep_date, is_nap, has_stages, totals_basis, asleep_s,
		provider_id, connection_id, device_id, origin_id, external_id, dedupe_key, raw_payload_id, normalizer_version_id)
		SELECT $1, user_id, start_at, end_at, tz_offset_min, sleep_date, is_nap, false, 'provider', asleep_s - 60,
		provider_id, connection_id, device_id, origin_id, external_id, dedupe_key, raw_payload_id, normalizer_version_id
		FROM sleep_sessions WHERE superseded_at IS NOT NULL`, uuid.Must(uuid.NewV7()))
	in.exec(`UPDATE sleep_sessions o SET superseded_by = n.id FROM sleep_sessions n
		WHERE o.superseded_at IS NOT NULL AND n.dedupe_key = o.dedupe_key AND n.superseded_at IS NULL`)

	// A second raw version, a pushing client with its batch, an import run.
	in.exec(`INSERT INTO blobs (sha256, size_bytes, stored_bytes, compression, refcount) VALUES (sha256('v2'), 10, 10, 'zstd', 1)`)
	in.exec(`INSERT INTO raw_payloads (user_id, connection_id, batch_id, stream, external_key, version, supersedes_id, content_sha256, content_type, fetched_at, status)
		SELECT user_id, connection_id, batch_id, stream, external_key, 2, id, sha256('v2'), content_type, fetched_at + interval '1 day', 'normalized'
		FROM raw_payloads WHERE id = (SELECT min(id) FROM raw_payloads)`)
	client, batch := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	in.exec(`INSERT INTO clients (id, user_id, connection_id, kind, name, token_hash) SELECT $1, user_id, id, 'collector', 'synthetic collector', sha256('secret')
		FROM connections ORDER BY id LIMIT 1`, client)
	in.exec(`INSERT INTO ingest_batches (id, user_id, connection_id, client_id, source_kind, idempotency_key) SELECT $1, user_id, connection_id, id, 'push', 'k1'
		FROM clients WHERE id = $2`, batch, client)
	run := uuid.Must(uuid.NewV7())
	in.exec(`INSERT INTO import_runs (id, user_id, source, status, stats) VALUES ($1, $2, 'apple_health_export', 'done', '{"items": 1}')`, run, in.user)
	in.exec(`INSERT INTO import_items (import_run_id, source, item_key, checksum, status, raw_payload_id)
		VALUES ($1, 'apple_health_export', 'item-1', sha256('item-1'), 'done', (SELECT min(id) FROM raw_payloads))`, run)

	// A workout with an activity file and a lap.
	in.exec(`INSERT INTO blobs (sha256, size_bytes, stored_bytes, compression, refcount) VALUES (sha256('fit'), 20, 20, 'zstd', 1)`)
	workout := uuid.Must(uuid.NewV7())
	in.exec(`INSERT INTO workouts (id, user_id, start_at, end_at, tz_offset_min, local_date, sport, distance_m, file_blob_sha256,
		provider_id, connection_id, external_id, dedupe_key, raw_payload_id, normalizer_version_id)
		SELECT $1, r.user_id, '2025-03-02T07:00:00Z', '2025-03-02T07:45:00Z', 60, '2025-03-02', 'running', 8000.5, sha256('fit'),
		c.provider_id, r.connection_id, 'run-1', substr(sha256('run-1'), 1, 16), r.id, (SELECT min(id) FROM normalizer_versions)
		FROM raw_payloads r JOIN connections c ON c.id = r.connection_id WHERE r.id = (SELECT min(id) FROM raw_payloads)`, workout)
	in.exec(`INSERT INTO workout_segments (workout_id, seq, kind, start_at, end_at, data) VALUES ($1, 1, 'lap', '2025-03-02T07:00:00Z', '2025-03-02T07:05:00Z', '{"m": 1000}')`, workout)
	in.exec(`INSERT INTO audit_events (user_id, actor, action, target_type, target_id) VALUES ($1, 'owner', 'rule.activate', 'rule', $2)`, in.user, rule.String())
}

// hashes digests every exported table, without the owner id (it changes) and the columns the
// importer deliberately resets.
func (in *instance) hashes() map[string]string {
	in.t.Helper()
	out := map[string]string{}
	for table, filter := range map[string]string{
		"timezone_periods": "", "settings": "", "resolution_rules": "", "active_rules": "", "connections": "", "devices": "",
		"data_origins": "", "clients": "", "ingest_batches": "", "blobs": "", "raw_payloads": "", "normalizer_versions": "",
		"import_runs": "WHERE source <> 'ndjson'", "import_items": "", "measurement_groups": "", "measurements": "",
		"sleep_sessions": "", "sleep_stages": "", "workouts": "", "workout_segments": "",
		"manual_overrides": "", "audit_events": "WHERE action IN ('rule.activate')",
	} {
		var n int64
		var sum string
		err := in.scan(`SELECT count(*), coalesce(md5(string_agg(r, E'\n' ORDER BY r)), '')
			FROM (SELECT (to_jsonb(t) - 'user_id' - 'token_hash' - 'hook_token_hash' - 'revoked_at')::text AS r FROM `+table+` t `+filter+`) x`, []any{&n, &sum})
		if err != nil {
			in.t.Fatal(err)
		}
		out[table] = fmt.Sprintf("%d rows %s", n, sum)
	}
	return out
}

func writeExport(t *testing.T, in *instance, blobs *blob.Store, o export.Options) (string, *export.Manifest) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "export.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	o.UserID = in.user
	m, err := export.Write(t.Context(), in.d, blobs, f, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path, m
}

func importZip(t *testing.T, in *instance, path string, o export.ImportOptions) (export.ImportStats, error) {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	return export.Import(t.Context(), in.d, zr, o)
}

func inserted(st export.ImportStats) map[string]int64 {
	out := map[string]int64{}
	for _, t := range st.Tables {
		out[t.Name] = t.Inserted
	}
	return out
}

// TestRoundTrip: a synthetic slice (7 days; the full 30 days with VITAMUX_EXPORT_FULL=1, which
// takes several minutes), exported and imported into a fresh instance, reproduces every row and
// its provenance; a second import is refused, a merge adds nothing.
func TestRoundTrip(t *testing.T) {
	days := 7
	if os.Getenv("VITAMUX_EXPORT_FULL") != "" {
		days = 30
	}
	src := loaded(t, "2025-03-01", days)
	src.addHistory()
	t.Run("memory bounded", func(t *testing.T) { boundedExport(t, src) })
	path, m := writeExport(t, src, nil, export.Options{Format: export.FormatCSV})
	if f := m.Files; len(f) == 0 || m.SchemaVersion != db.ExpectedVersion() {
		t.Fatalf("manifest: %+v", m)
	}

	dst := newInstance(t)
	dst.createOwner()
	st, err := importZip(t, dst, path, export.ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want, got := src.hashes(), dst.hashes()
	for table := range want {
		if want[table] != got[table] {
			t.Errorf("%s: exported %s, imported %s", table, want[table], got[table])
		}
	}
	if n := dst.int(`SELECT count(*) FROM measurements WHERE user_id <> $1`, dst.user); n != 0 {
		t.Errorf("%d rows not moved to the target owner", n)
	}
	if n := dst.int(`SELECT count(*) FROM clients WHERE revoked_at IS NULL`); n != 0 {
		t.Errorf("%d imported clients can still authenticate", n)
	}
	if n := dst.int(`SELECT count(*) FROM resolution_dirty`); n == 0 {
		t.Error("imported days are not marked for resolution")
	}
	if got := inserted(st)["measurements"]; got != src.int(`SELECT count(*) FROM measurements`) {
		t.Errorf("inserted %d measurements", got)
	}
	// New rows after the import must not collide with imported ids.
	dst.exec(`INSERT INTO normalizer_versions (name, version, git_sha) VALUES ('after-import', 1, 'x')`)

	if _, err := importZip(t, dst, path, export.ImportOptions{}); !errors.Is(err, export.ErrNotEmpty) {
		t.Fatalf("second import: %v, want ErrNotEmpty", err)
	}
	st, err = importZip(t, dst, path, export.ImportOptions{Merge: true})
	if err != nil {
		t.Fatal(err)
	}
	for table, n := range inserted(st) {
		if n != 0 {
			t.Errorf("merge of the same export inserted %d %s rows", n, table)
		}
	}
	after := dst.hashes()
	for table := range got {
		if table != "normalizer_versions" && after[table] != got[table] {
			t.Errorf("%s changed by an idempotent merge", table)
		}
	}
}

// TestMergeIntoOtherData merges an export into an instance with its own, unrelated rows:
// everything is added in fresh id ranges, and registry rows the target already has (same
// account, device fingerprint, origin) are reused.
func TestMergeIntoOtherData(t *testing.T) {
	src := loaded(t, "2025-03-01", 3)
	path, _ := writeExport(t, src, nil, export.Options{Format: export.FormatNDJSON})
	dst := loaded(t, "2025-06-01", 2)
	before := dst.int(`SELECT count(*) FROM measurements`)
	conns := dst.int(`SELECT count(*) FROM connections`)

	if _, err := importZip(t, dst, path, export.ImportOptions{}); !errors.Is(err, export.ErrNotEmpty) {
		t.Fatalf("import without --merge: %v", err)
	}
	st, err := importZip(t, dst, path, export.ImportOptions{Merge: true})
	if err != nil {
		t.Fatal(err)
	}
	want := src.int(`SELECT count(*) FROM measurements`)
	if got := dst.int(`SELECT count(*) FROM measurements`); got != before+want || inserted(st)["measurements"] != want {
		t.Fatalf("measurements: %d after merge, want %d + %d", got, before, want)
	}
	if got := dst.int(`SELECT count(*) FROM connections`); got != conns {
		t.Errorf("connections: %d, want the target's %d reused", got, conns)
	}
	if n := dst.int(`SELECT count(*) FROM measurements m JOIN raw_payloads r ON r.id = m.raw_payload_id
		WHERE r.connection_id <> m.connection_id OR right(r.external_key, 10) <> m.local_date::text`); n != 0 {
		t.Errorf("%d merged measurements point at the wrong raw payload", n)
	}
	// The same values arrived: compare the source's rows by dedupe key.
	q := `SELECT md5(string_agg(encode(dedupe_key, 'hex') || value::text || start_at::text, ',' ORDER BY dedupe_key)) FROM measurements
		WHERE local_date < '2025-04-01'`
	if src.str(q) != dst.str(q) {
		t.Error("merged measurements differ from the source")
	}
	st, err = importZip(t, dst, path, export.ImportOptions{Merge: true})
	if err != nil {
		t.Fatal(err)
	}
	if n := inserted(st)["measurements"] + inserted(st)["raw_payloads"]; n != 0 {
		t.Errorf("second merge inserted %d rows", n)
	}
}

func testBlobs(t *testing.T) *blob.Store {
	t.Helper()
	keyPath := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.WriteKeyFile(keyPath); err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.Load(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	s, err := blob.Open(filepath.Join(t.TempDir(), "blobs"), kr)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestRawContent: include_raw carries raw payload and workout file content to the target's
// blob store; content missing at the source is counted and imported as a reference only.
func TestRawContent(t *testing.T) {
	src := loaded(t, "2025-03-01", 1)
	srcBlobs := testBlobs(t)
	contents := [][]byte{[]byte(`{"synthetic": "page 1"}`), make([]byte, 300_000)}
	_, _ = rand.Read(contents[1])
	var sums [][]byte
	for _, c := range contents {
		var sum []byte
		err := src.d.Tx(t.Context(), func(q *dbq.Queries) error {
			info, err := srcBlobs.Put(t.Context(), q, bytes.NewReader(c), blob.Plain)
			sum = info.SHA256
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		sums = append(sums, sum)
	}
	// Point two raw payloads at stored content; the others keep their fixture blobs (no file).
	src.exec(`UPDATE blobs SET refcount = refcount + 1 WHERE sha256 = $1`, sums[0])
	src.exec(`UPDATE blobs SET refcount = refcount - 1 WHERE sha256 = (SELECT content_sha256 FROM raw_payloads ORDER BY id LIMIT 1)`)
	src.exec(`UPDATE raw_payloads SET content_sha256 = $1 WHERE id = (SELECT min(id) FROM raw_payloads)`, sums[0])
	src.exec(`UPDATE blobs SET refcount = refcount + 1 WHERE sha256 = $1`, sums[1])
	src.exec(`INSERT INTO workouts (id, user_id, start_at, end_at, local_date, sport, file_blob_sha256, provider_id, connection_id, dedupe_key, raw_payload_id, normalizer_version_id)
		SELECT $1, r.user_id, '2025-03-01T07:00:00Z', '2025-03-01T08:00:00Z', '2025-03-01', 'cycling', $2, c.provider_id, r.connection_id,
		substr(sha256('ride'), 1, 16), r.id, (SELECT min(id) FROM normalizer_versions)
		FROM raw_payloads r JOIN connections c ON c.id = r.connection_id ORDER BY r.id LIMIT 1`, uuid.Must(uuid.NewV7()), sums[1])
	raws := src.int(`SELECT count(*) FROM raw_payloads`)

	path, m := writeExport(t, src, srcBlobs, export.Options{Format: export.FormatNDJSON, IncludeRaw: true})
	if m.RawMissing != int(raws-1) {
		t.Errorf("raw_missing %d, want %d", m.RawMissing, raws-1)
	}

	dst := newInstance(t)
	dst.createOwner()
	if _, err := importZip(t, dst, path, export.ImportOptions{}); err == nil || !strings.Contains(err.Error(), "raw content") {
		t.Fatalf("import without a blob store: %v", err)
	}
	dstBlobs := testBlobs(t)
	st, err := importZip(t, dst, path, export.ImportOptions{Blobs: dstBlobs})
	if err != nil {
		t.Fatal(err)
	}
	if st.RawContent != 2 {
		t.Errorf("stored %d contents, want 2", st.RawContent)
	}
	for i, sum := range sums {
		got, err := dstBlobs.Get(sum)
		if err != nil || !bytes.Equal(got, contents[i]) {
			t.Errorf("content %d: %v", i, err)
		}
	}
	// Stored content gets the target's own stored size, so compare the references.
	q := `SELECT md5(string_agg(encode(sha256, 'hex') || ':' || refcount, ',' ORDER BY sha256)) FROM blobs WHERE refcount > 0`
	if src.str(q) != dst.str(q) {
		t.Error("blob references differ")
	}
}

// boundedExport checks that exporting the slice (over half a million rows and hundreds of MiB of
// NDJSON for the full 30 days) keeps the heap within a small, fixed budget.
func boundedExport(t *testing.T, src *instance) {
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var peak uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
				runtime.ReadMemStats(&ms)
				peak = max(peak, ms.HeapAlloc)
			}
		}
	})
	m, err := export.Write(t.Context(), src.d, nil, io.Discard, export.Options{UserID: src.user, Format: export.FormatCSV})
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	var written int64
	for _, f := range m.Files {
		written += f.Bytes
	}
	growth := int64(peak) - int64(base.HeapAlloc)
	t.Logf("wrote %d MiB uncompressed; heap grew at most %d MiB", written>>20, growth>>20)
	if written < 64<<20 { // the 7-day slice writes ~110 MiB; the full slice ~470 MiB
		t.Fatalf("only %d bytes written; the dataset is too small to prove anything", written)
	}
	if growth > 48<<20 {
		t.Errorf("heap grew by %d MiB while exporting", growth>>20)
	}
}
