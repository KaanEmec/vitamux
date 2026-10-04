//go:build integration

package lifecycle_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/documents"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/lifecycle"
)

type env struct {
	t *testing.T
	d *db.DB
	// run and scan execute SQL as the owner role.
	run          func(sql string, args ...any) error
	scan         func(sql string, dest []any, args ...any) error
	kr           *crypto.Keyring
	blobs        *blob.Store
	blobDir      string
	nvOld, nvNew int32 // two versions of one normalizer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	url, app := dbtest.Migrated(t)
	owner := dbtest.Pool(t, url, db.OwnerRole)
	e := &env{t: t, d: db.New(app), blobDir: filepath.Join(t.TempDir(), "blobs"),
		run: func(sql string, args ...any) error {
			_, err := owner.Exec(t.Context(), sql, args...)
			return err
		},
		scan: func(sql string, dest []any, args ...any) error {
			return owner.QueryRow(t.Context(), sql, args...).Scan(dest...)
		},
	}
	keyAt := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.WriteKeyFile(keyAt); err != nil {
		t.Fatal(err)
	}
	var err error
	if e.kr, err = crypto.Load(keyAt); err != nil {
		t.Fatal(err)
	}
	if e.blobs, err = blob.Open(e.blobDir, e.kr); err != nil {
		t.Fatal(err)
	}
	e.nvOld = int32(e.int(`INSERT INTO normalizer_versions (name, version, git_sha) VALUES ('withings.measures', 1, 'synthetic') RETURNING id`))
	e.nvNew = int32(e.int(`INSERT INTO normalizer_versions (name, version, git_sha) VALUES ('withings.measures', 2, 'synthetic') RETURNING id`))
	return e
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if err := e.run(sql, args...); err != nil {
		e.t.Fatalf("%v\n%s", err, sql)
	}
}

func (e *env) int(sql string, args ...any) int64 {
	e.t.Helper()
	var n int64
	if err := e.scan(sql, []any{&n}, args...); err != nil {
		e.t.Fatalf("%v\n%s", err, sql)
	}
	return n
}

func newID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// snapshot digests every table except those in skip.
func (e *env) snapshot(skip ...string) map[string]string {
	e.t.Helper()
	tables := e.strings(`SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename <> 'goose_db_version'`)
	out := map[string]string{}
	for _, table := range tables {
		if slices.Contains(skip, table) {
			continue
		}
		var n int64
		var sum string
		err := e.scan(`SELECT count(*), coalesce(md5(string_agg(r, E'\n' ORDER BY r)), '')
			FROM (SELECT to_jsonb(t)::text AS r FROM `+table+` t) x`, []any{&n, &sum})
		if err != nil {
			e.t.Fatal(err)
		}
		out[table] = fmt.Sprintf("%d rows %s", n, sum)
	}
	return out
}

func (e *env) sameAs(before map[string]string, skip ...string) {
	e.t.Helper()
	after := e.snapshot(skip...)
	for _, table := range slices.Sorted(maps.Keys(before)) {
		if !slices.Contains(skip, table) && before[table] != after[table] {
			e.t.Errorf("%s changed: %s -> %s", table, before[table], after[table])
		}
	}
}

// files lists the blob files on disk.
func (e *env) files() []string {
	e.t.Helper()
	var out []string
	err := filepath.WalkDir(e.blobDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Dir(p) != e.blobDir {
			out = append(out, p)
		}
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

func (e *env) sweep() {
	e.t.Helper()
	// A negative grace tolerates the database clock running ahead (nothing writes meanwhile).
	if _, err := blob.Sweep(e.t.Context(), e.d, e.blobs, -time.Minute); err != nil {
		e.t.Fatal(err)
	}
}

type seeded struct {
	user, conn uuid.UUID
}

func (e *env) newUser(name string) seeded {
	e.t.Helper()
	s := seeded{user: newID(), conn: newID()}
	e.exec(`INSERT INTO users (id, username, password_hash) VALUES ($1, $2, 'synthetic')`, s.user, name)
	e.exec(`INSERT INTO connections (id, user_id, provider_id, account_key, mode, status)
		SELECT $1, $2, id, sha256($3::bytea), 'in_process', 'active' FROM providers WHERE code = 'withings'`, s.conn, s.user, []byte(name))
	return s
}

// raw stores synthetic payloads (external key -> body) in one sync batch of conn and returns
// their ids; a changed body for a known key is a new version.
func (e *env) raw(s seeded, conn uuid.UUID, items map[string]string) map[string]int64 {
	e.t.Helper()
	out := map[string]int64{}
	err := e.d.Tx(e.t.Context(), func(q *dbq.Queries) error {
		b, err := ingest.CreateBatch(e.t.Context(), q, ingest.BatchInfo{UserID: s.user, ConnectionID: conn, SourceKind: ingest.SourceSync})
		if err != nil {
			return err
		}
		var list []ingest.RawItem
		for _, k := range slices.Sorted(maps.Keys(items)) {
			list = append(list, ingest.RawItem{Stream: "withings.measures", ExternalKey: k, ContentType: "application/json",
				FetchedAt: time.Now(), Body: []byte(items[k])})
		}
		res, err := ingest.StoreRaw(e.t.Context(), q, e.blobs, b, list)
		for _, r := range res {
			out[r.ExternalKey] = r.RawPayloadID
		}
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

// normalized marks raw ids as normalized by nv, stored daysAgo days ago.
func (e *env) normalized(nv int32, daysAgo int, ids ...int64) {
	e.t.Helper()
	e.exec(`UPDATE raw_payloads SET status = 'normalized', normalizer_version_id = $1, normalized_at = now(),
		stored_at = now() - make_interval(days => $2) WHERE id = ANY($3)`, nv, daysAgo, ids)
}

func (e *env) measurement(s seeded, conn uuid.UUID, raw any, nv int32, key string) int64 {
	e.t.Helper()
	return e.int(`INSERT INTO measurements (user_id, metric_id, kind, start_at, local_date, value, provider_id, connection_id,
		dedupe_key, raw_payload_id, normalizer_version_id)
		SELECT $1, (SELECT id FROM metric_catalog WHERE code = 'weight'), 'sample', '2026-09-01T07:00:00Z', '2026-09-01', 70,
		c.provider_id, c.id, substr(sha256($3::bytea), 1, 16), $4, $5 FROM connections c WHERE c.id = $2 RETURNING id`,
		s.user, conn, []byte(key), raw, nv)
}

// correct supersedes measurement old (superseded daysAgo days ago) by a new row from raw.
func (e *env) correct(s seeded, conn uuid.UUID, old int64, daysAgo int, raw any, nv int32, key string) int64 {
	e.t.Helper()
	e.exec(`UPDATE measurements SET superseded_at = now() - make_interval(days => $2) WHERE id = $1`, old, daysAgo)
	n := e.measurement(s, conn, raw, nv, key)
	e.exec(`UPDATE measurements SET superseded_by = $2 WHERE id = $1`, old, n)
	return n
}

// seedAll gives s a bit of everything an owner can have.
func (e *env) seedAll(s seeded, name string) {
	e.t.Helper()
	ctx := e.t.Context()
	e.exec(`INSERT INTO credentials (connection_id, ciphertext, key_id) VALUES ($1, '\x00', 'synthetic')`, s.conn)
	e.exec(`INSERT INTO schedules (id, connection_id, stream, run_interval, next_run_at) VALUES ($1, $2, 'withings.measures', '1 hour', now())`, newID(), s.conn)
	e.exec(`INSERT INTO sync_cursors (connection_id, stream, cursor) VALUES ($1, 'withings.measures', '{"offset": 1}')`, s.conn)
	e.exec(`INSERT INTO devices (id, user_id, provider_id, fingerprint, device_type) SELECT $1, $2, id, 'scale-1', 'scale' FROM providers WHERE code = 'withings'`, newID(), s.user)
	e.exec(`INSERT INTO data_origins (id, user_id, provider_id, origin_key) SELECT $1, $2, id, 'synthetic-app' FROM providers WHERE code = 'withings'`, newID(), s.user)
	client := newID()
	e.exec(`INSERT INTO clients (id, user_id, connection_id, kind, name, token_hash) VALUES ($1, $2, $3, 'collector', 'synthetic', sha256($4::bytea))`, client, s.user, s.conn, []byte(name))
	e.exec(`INSERT INTO idempotency_keys (client_id, key, request_sha256, response_status, response_body) VALUES ($1, 'k1', sha256('r'), 202, '{}')`, client)

	v1 := e.raw(s, s.conn, map[string]string{"m1": `{"synthetic":"` + name + `","v":1}`, "m2": `{"synthetic":"` + name + `"}`, "shared": `{"shared":true}`})
	v2 := e.raw(s, s.conn, map[string]string{"m1": `{"synthetic":"` + name + `","v":2}`})
	old := e.measurement(s, s.conn, v1["m1"], e.nvNew, name+"m1")
	e.correct(s, s.conn, old, 1, v2["m1"], e.nvNew, name+"m1")
	gone := e.measurement(s, s.conn, v1["m2"], e.nvNew, name+"gone")
	e.exec(`UPDATE measurements SET deleted_at = now(), deleted_by_raw_id = $2 WHERE id = $1`, gone, v1["shared"])
	group := e.int(`INSERT INTO measurement_groups (user_id, kind, measured_at, local_date, provider_id, connection_id, dedupe_key, raw_payload_id, normalizer_version_id)
		SELECT $1, 'bp_reading', '2026-09-01T07:00:00Z', '2026-09-01', c.provider_id, c.id, substr(sha256($3::bytea), 1, 16), $4, $5
		FROM connections c WHERE c.id = $2 RETURNING id`, s.user, s.conn, []byte(name+"g"), v1["m2"], e.nvNew)
	e.exec(`UPDATE measurements SET group_id = $2 WHERE id = $1`, e.measurement(s, s.conn, v1["m2"], e.nvNew, name+"in-group"), group)
	sleep, workout := newID(), newID()
	e.exec(`INSERT INTO sleep_sessions (id, user_id, start_at, end_at, sleep_date, has_stages, totals_basis, provider_id, connection_id, dedupe_key, raw_payload_id, normalizer_version_id)
		SELECT $1, $2, '2026-09-01T22:00:00Z', '2026-09-02T06:00:00Z', '2026-09-02', true, 'stages', c.provider_id, c.id, substr(sha256($4::bytea), 1, 16), $5, $6
		FROM connections c WHERE c.id = $3`, sleep, s.user, s.conn, []byte(name+"s"), v1["m2"], e.nvNew)
	e.exec(`INSERT INTO sleep_stages (session_id, stage, start_at, end_at) VALUES ($1, 'deep', '2026-09-01T23:00:00Z', '2026-09-02T00:00:00Z')`, sleep)
	e.exec(`INSERT INTO workouts (id, user_id, start_at, end_at, local_date, sport, provider_id, connection_id, dedupe_key, raw_payload_id, normalizer_version_id)
		SELECT $1, $2, '2026-09-01T07:00:00Z', '2026-09-01T08:00:00Z', '2026-09-01', 'running', c.provider_id, c.id, substr(sha256($4::bytea), 1, 16), $5, $6
		FROM connections c WHERE c.id = $3`, workout, s.user, s.conn, []byte(name+"w"), v1["m2"], e.nvNew)
	e.exec(`INSERT INTO workout_segments (workout_id, seq, kind, start_at) VALUES ($1, 1, 'lap', '2026-09-01T07:00:00Z')`, workout)
	run := newID()
	e.exec(`INSERT INTO import_runs (id, user_id, connection_id, source, status) VALUES ($1, $2, $3, 'ndjson', 'done')`, run, s.user, s.conn)
	e.exec(`INSERT INTO import_items (import_run_id, source, item_key, checksum, status, raw_payload_id) VALUES ($1, 'ndjson', $2, sha256($3::bytea), 'done', $4)`,
		run, name, []byte(name), v1["m2"])
	e.exec(`INSERT INTO resolution_dirty (user_id, metric_id, local_date) SELECT $1, id, '2026-09-01' FROM metric_catalog WHERE code = 'weight'`, s.user)

	e.exec(`INSERT INTO settings (user_id, key, value) VALUES ($1, 'units', '{"weight": "kg"}')`, s.user)
	e.exec(`INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES ($1, $2, 'Europe/Berlin', '2024-01-01Z')`, newID(), s.user)
	e.exec(`INSERT INTO sessions (id, user_id, token_hash, expires_at) VALUES ($1, $2, sha256($3::bytea), now() + interval '1 day')`, newID(), s.user, []byte(name+"session"))
	e.exec(`INSERT INTO api_keys (id, user_id, name, secret_hash, scopes) VALUES ($1, $2, 'synthetic', sha256('k'), '{read:health}')`, newID(), s.user)
	e.exec(`INSERT INTO recovery_codes (user_id, code_hash) VALUES ($1, sha256('c'))`, s.user)
	e.exec(`INSERT INTO resolution_rules (id, user_id, metric, version, spec, created_by) VALUES ($1, $2, 'steps', 1, '{"strategy": "max"}', 'owner')`, newID(), s.user)
	e.exec(`INSERT INTO active_rules (user_id, metric, version, activated_by) VALUES ($1, 'steps', 1, 'owner')`, s.user)
	e.exec(`INSERT INTO manual_overrides (id, user_id, metric, window_kind, window_key, local_date, action, input_id, created_by)
		VALUES ($1, $2, 'weight', 'local_day', '2026-09-01', '2026-09-01', 'exclude_input', $3, 'owner')`, newID(), s.user, old)
	e.exec(`INSERT INTO audit_events (user_id, actor, action) VALUES ($1, 'owner', 'synthetic.event')`, s.user)

	if _, _, err := documents.New(e.d, e.blobs, e.kr).Upload(ctx, s.user, audit.Owner, "lab.pdf", bytes.NewReader(minimalPDF(name))); err != nil {
		e.t.Fatal(err)
	}
	job := newID()
	e.exec(`INSERT INTO jobs (id, kind, status, finished_at) VALUES ($1, 'export', 'succeeded', now())`, job)
	err := e.d.Tx(ctx, func(q *dbq.Queries) error {
		info, err := e.blobs.Put(ctx, q, bytes.NewReader([]byte("synthetic export "+name)), blob.Plain)
		if err != nil {
			return err
		}
		return blob.Retain(ctx, q, info.SHA256) // the export row below holds the reference
	})
	if err != nil {
		e.t.Fatal(err)
	}
	e.exec(`INSERT INTO exports (id, user_id, job_id, format, include_raw, finished_at, blob_sha256)
		VALUES ($1, $2, $3, 'ndjson', false, now(), sha256($4::bytea))`, newID(), s.user, job, []byte("synthetic export "+name))
}

func minimalPDF(salt string) []byte {
	return []byte("%PDF-1.4\n%" + salt + "\n1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n" +
		"2 0 obj << /Type /Pages /Count 1 >> endobj\n3 0 obj << /Type /Page /Parent 2 0 R >> endobj\n" +
		"trailer << /Root 1 0 R >>\n%%EOF\n")
}

// userTables lists the tables with a user_id column.
func (e *env) userTables() []string {
	e.t.Helper()
	return e.strings(`SELECT table_name FROM information_schema.columns WHERE table_schema = 'public' AND column_name = 'user_id'`)
}

// strings returns the one-column result of sql, sorted.
func (e *env) strings(sql string) []string {
	e.t.Helper()
	var out []string
	if err := e.scan(`SELECT coalesce(array_agg(x ORDER BY x), '{}')::text[] FROM (`+sql+`) q(x)`, []any{&out}); err != nil {
		e.t.Fatal(err)
	}
	return out
}

// TestPurge: purging one owner leaves exactly the database (and blob files) as they were
// before that owner existed, except for unlinked audit events.
func TestPurge(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	other := e.newUser("other")
	e.seedAll(other, "other")
	before, files := e.snapshot("audit_events"), e.files()

	s := e.newUser("owner")
	e.seedAll(s, "owner")
	audits := e.int(`SELECT count(*) FROM audit_events WHERE user_id = $1`, s.user)
	seededState := e.snapshot()

	// A dry run reports and changes nothing; a running job blocks the purge.
	counts, err := lifecycle.Purge(ctx, e.d, s.user, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, c := range counts {
		got[c.Table] = c.Rows
	}
	for table, n := range map[string]int64{"measurements": 4, "raw_payloads": 4, "document_keys": 1, "documents": 1, "exports": 1, "jobs": 1,
		"connections": 1, "resolution_rules": 1, "manual_overrides": 1, "users": 1} {
		if got[table] != n {
			t.Errorf("dry run %s: %d, want %d (%v)", table, got[table], n, got)
		}
	}
	e.sameAs(seededState)
	job := newID()
	e.exec(`INSERT INTO jobs (id, kind, status, connection_id, lease_owner, lease_expires_at, started_at)
		VALUES ($1, 'sync', 'running', $2, 'synthetic', now() + interval '1 hour', now())`, job, s.conn)
	if _, err := lifecycle.Purge(ctx, e.d, s.user, "admin", false); !errors.Is(err, lifecycle.ErrJobRunning) {
		t.Fatalf("purge with a running job: %v", err)
	}
	e.exec(`DELETE FROM jobs WHERE id = $1`, job)

	if _, err := lifecycle.Purge(ctx, e.d, s.user, "admin", false); err != nil {
		t.Fatal(err)
	}
	e.sweep()
	for _, table := range e.userTables() {
		if n := e.int(`SELECT count(*) FROM `+table+` WHERE user_id = $1`, s.user); n != 0 {
			t.Errorf("%s: %d rows of the purged owner left", table, n)
		}
	}
	e.sameAs(before, "audit_events")
	if after := e.files(); !slices.Equal(after, files) {
		t.Errorf("blob files: %d after, %d before the owner", len(after), len(files))
	}
	if n := e.int(`SELECT count(*) FROM audit_events WHERE user_id IS NULL AND action <> 'user.purged'`); n != audits {
		t.Errorf("unlinked audit events: %d, want %d", n, audits)
	}
	if n := e.int(`SELECT count(*) FROM audit_events WHERE action = 'user.purged' AND target_id = $1 AND detail->>'measurements' = '4'`, s.user.String()); n != 1 {
		t.Errorf("purge audit events: %d", n)
	}
	if _, err := lifecycle.Purge(ctx, e.d, s.user, "admin", false); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("second purge: %v", err)
	}
}

// TestDeleteConnection: deleting a connection with its data removes its rows and blobs and
// changes nothing else but re-resolution marks.
func TestDeleteConnection(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.newUser("owner")
	e.seedAll(s, "owner")
	before, files := e.snapshot(), e.files()

	conn := newID()
	e.exec(`INSERT INTO connections (id, user_id, provider_id, mode, status) SELECT $1, $2, id, 'push', 'active' FROM providers WHERE code = 'apple_health'`, conn, s.user)
	client := newID()
	e.exec(`INSERT INTO clients (id, user_id, connection_id, kind, name, token_hash) VALUES ($1, $2, $3, 'device', 'synthetic phone', sha256('p'))`, client, s.user, conn)
	e.exec(`INSERT INTO idempotency_keys (client_id, key, request_sha256, response_status, response_body) VALUES ($1, 'k1', sha256('r'), 202, '{}')`, client)
	r := e.raw(s, conn, map[string]string{"a": `{"synthetic":"phone","v":1}`, "shared": `{"shared":true}`})
	r2 := e.raw(s, conn, map[string]string{"a": `{"synthetic":"phone","v":2}`})
	old := e.measurement(s, conn, r["a"], e.nvNew, "phone-a")
	e.correct(s, conn, old, 1, r2["a"], e.nvNew, "phone-a")
	e.exec(`INSERT INTO schedules (id, connection_id, stream, run_interval, next_run_at) VALUES ($1, $2, 'apple_health.samples', '1 hour', now())`, newID(), conn)

	err := e.d.Tx(ctx, func(q *dbq.Queries) error {
		counts, err := lifecycle.DeleteConnection(ctx, q, s.user, conn)
		if counts["measurements"] != 2 || counts["raw_payloads"] != 3 {
			t.Errorf("counts: %v", counts)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	e.sweep()
	e.sameAs(before, "resolution_dirty")
	if after := e.files(); !slices.Equal(after, files) {
		t.Errorf("blob files: %d after, %d before", len(after), len(files))
	}
	if err := e.d.Tx(ctx, func(q *dbq.Queries) error { _, err := lifecycle.DeleteConnection(ctx, q, s.user, conn); return err }); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

// TestDeleteDocument: a document deletion leaves a tombstone and nothing else of the document.
func TestDeleteDocument(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.newUser("owner")
	e.seedAll(s, "owner")
	before, files := e.snapshot(), e.files()
	store := documents.New(e.d, e.blobs, e.kr)
	doc, _, err := store.Upload(ctx, s.user, audit.Owner, "second.pdf", bytes.NewReader(minimalPDF("second")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Delete(ctx, s.user, doc.ID, documents.DeleteDerived, audit.Owner); err != nil {
		t.Fatal(err)
	}
	e.sweep()
	e.sameAs(before, "documents", "audit_events")
	if after := e.files(); !slices.Equal(after, files) {
		t.Errorf("blob files: %d after, %d before", len(after), len(files))
	}
	if n := e.int(`SELECT count(*) FROM documents WHERE id = $1 AND sha256 IS NULL AND blob_sha256 IS NULL AND status = 'deleted'`, doc.ID); n != 1 {
		t.Errorf("tombstone: %d", n)
	}
}

// TestPruneRaw covers each prune_raw safety rule.
func TestPruneRaw(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.newUser("owner")
	k := func(n string) string { return `{"synthetic":"` + n + `"}` }
	r := e.raw(s, s.conn, map[string]string{"chain": k("chain-1"), "active": k("active"), "replaced": k("replaced"), "stale-rows": k("stale-rows"),
		"stale-raw": k("stale-raw"), "fresh": k("fresh"), "failed": k("failed"), "tombstone": k("tombstone"), "imported": k("imported")})
	chain2 := e.raw(s, s.conn, map[string]string{"chain": k("chain-2")})["chain"]
	e.normalized(e.nvNew, 60, r["chain"], chain2, r["active"], r["replaced"], r["stale-rows"], r["tombstone"], r["imported"])
	e.normalized(e.nvOld, 60, r["stale-raw"])
	e.normalized(e.nvNew, 1, r["fresh"])
	e.exec(`UPDATE raw_payloads SET status = 'normalize_failed', stored_at = now() - interval '60 days' WHERE id = $1`, r["failed"])

	// chain: version 1's row superseded by version 2's (active). replaced: its only row was
	// superseded by a row from active. stale-rows: same, but its row came from the old normalizer.
	old := e.measurement(s, s.conn, r["chain"], e.nvNew, "chain")
	chainRow := e.correct(s, s.conn, old, 1, chain2, e.nvNew, "chain")
	e.correct(s, s.conn, e.measurement(s, s.conn, r["replaced"], e.nvNew, "replaced"), 1, r["active"], e.nvNew, "replaced")
	e.correct(s, s.conn, e.measurement(s, s.conn, r["stale-rows"], e.nvOld, "stale-rows"), 1, r["active"], e.nvNew, "stale-rows")
	// tombstone deleted a row of active; imported is named by an import item only.
	gone := e.measurement(s, s.conn, r["active"], e.nvNew, "gone")
	e.exec(`UPDATE measurements SET deleted_at = now(), deleted_by_raw_id = $2 WHERE id = $1`, gone, r["tombstone"])
	run := newID()
	e.exec(`INSERT INTO import_runs (id, user_id, connection_id, source, status) VALUES ($1, $2, $3, 'ndjson', 'done')`, run, s.user, s.conn)
	e.exec(`INSERT INTO import_items (import_run_id, source, item_key, checksum, status, raw_payload_id) VALUES ($1, 'ndjson', 'i', sha256('i'), 'done', $2)`, run, r["imported"])
	// Another provider's old raw has no retention.
	push := newID()
	e.exec(`INSERT INTO connections (id, user_id, provider_id, mode, status) SELECT $1, $2, id, 'push', 'active' FROM providers WHERE code = 'apple_health'`, push, s.user)
	e.normalized(e.nvNew, 60, e.raw(s, push, map[string]string{"x": k("apple")})["x"])

	active := `SELECT md5(string_agg(to_jsonb(m)::text, ',' ORDER BY id)) FROM measurements m WHERE superseded_at IS NULL AND deleted_at IS NULL`
	var activeBefore, activeAfter string
	if err := e.scan(active, []any{&activeBefore}); err != nil {
		t.Fatal(err)
	}

	// Off by default.
	if st, err := lifecycle.PruneRaw(ctx, e.d, time.Now()); err != nil || st.Pruned != 0 {
		t.Fatalf("default policy pruned %+v, %v", st, err)
	}
	setRetention(t, e, s.user, lifecycle.Retention{RawDays: map[string]int{"withings": 30}, IdempotencyKeyDays: 30})
	st, err := lifecycle.PruneRaw(ctx, e.d, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	wantGone := []string{"chain", "replaced", "tombstone", "imported"}
	if st.Pruned != int64(len(wantGone)) || st.RefusedStale != 2 {
		t.Errorf("stats %+v, want %d pruned and 2 refused", st, len(wantGone))
	}
	for key, id := range r {
		n := e.int(`SELECT count(*) FROM raw_payloads WHERE id = $1`, id)
		if want := int64(1 - btoi(slices.Contains(wantGone, key))); n != want {
			t.Errorf("raw %s: %d rows, want %d", key, n, want)
		}
	}
	if err := e.scan(active, []any{&activeAfter}); err != nil || activeAfter != activeBefore {
		t.Errorf("active rows changed (%v)", err)
	}
	if n := e.int(`SELECT count(*) FROM raw_payloads WHERE id = $1 AND supersedes_id IS NULL AND version = 2`, chain2); n != 1 {
		t.Error("version 2 still points at the pruned version 1")
	}
	if n := e.int(`SELECT count(*) FROM measurements WHERE id = $1 AND raw_payload_id IS NULL AND superseded_by = $2`, old, chainRow); n != 1 {
		t.Error("superseded row of the pruned raw: reference not cleared or history lost")
	}
	if n := e.int(`SELECT count(*) FROM measurements WHERE id = $1 AND deleted_at IS NOT NULL AND deleted_by_raw_id IS NULL`, gone); n != 1 {
		t.Error("tombstoned row: deleted_by_raw_id not cleared")
	}
	if n := e.int(`SELECT count(*) FROM import_items WHERE raw_payload_id IS NULL AND status = 'done'`); n != 1 {
		t.Error("import item: reference not cleared")
	}
	e.sweep()
	if n := e.int(`SELECT count(*) FROM blobs b WHERE NOT EXISTS (SELECT 1 FROM raw_payloads r WHERE r.content_sha256 = b.sha256)`); n != 0 {
		t.Errorf("%d blobs of pruned raw left after the sweep", n)
	}
	if n, want := len(e.files()), int(e.int(`SELECT count(*) FROM blobs`)); n != want {
		t.Errorf("%d blob files, %d blob rows", n, want)
	}
	if n := e.int(`SELECT count(*) FROM audit_events WHERE action = 'retention.prune_raw' AND detail->>'raw_payloads' = '4'`); n != 1 {
		t.Errorf("audit events: %d", n)
	}
	// Idempotent; once stale-rows' old row is gone (prune_superseded), it is prunable too.
	if st, err := lifecycle.PruneRaw(ctx, e.d, time.Now()); err != nil || st.Pruned != 0 {
		t.Errorf("second run: %+v, %v", st, err)
	}
	e.exec(`DELETE FROM measurements WHERE raw_payload_id = $1`, r["stale-rows"])
	if st, err := lifecycle.PruneRaw(ctx, e.d, time.Now()); err != nil || st.Pruned != 1 || st.RefusedStale != 1 {
		t.Errorf("after the stale row went: %+v, %v", st, err)
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func setRetention(t *testing.T, e *env, user uuid.UUID, r lifecycle.Retention) {
	t.Helper()
	err := e.d.Tx(t.Context(), func(q *dbq.Queries) error {
		return lifecycle.SetRetention(t.Context(), q, user, audit.Owner, r)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPruneSuperseded(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.newUser("owner")
	raw := e.raw(s, s.conn, map[string]string{"a": `{"synthetic":"a"}`})["a"]
	// m1 -> m2 -> m3 (active): superseded 100 and 50 days ago.
	m1 := e.measurement(s, s.conn, raw, e.nvNew, "m")
	m2 := e.correct(s, s.conn, m1, 100, raw, e.nvNew, "m")
	m3 := e.correct(s, s.conn, m2, 50, raw, e.nvNew, "m")
	// A superseded group still named by an active measurement, and one that is not.
	group := func(key string) int64 {
		return e.int(`INSERT INTO measurement_groups (user_id, kind, measured_at, local_date, provider_id, connection_id, dedupe_key, normalizer_version_id, superseded_at)
			SELECT $1, 'bp_reading', '2026-09-01T07:00:00Z', '2026-09-01', c.provider_id, c.id, substr(sha256($3::bytea), 1, 16), $4, now() - interval '100 days'
			FROM connections c WHERE c.id = $2 RETURNING id`, s.user, s.conn, []byte(key), e.nvNew)
	}
	named, unnamed := group("g1"), group("g2")
	e.exec(`UPDATE measurements SET group_id = $2 WHERE id = $1`, m3, named)
	sleep := newID()
	e.exec(`INSERT INTO sleep_sessions (id, user_id, start_at, end_at, sleep_date, has_stages, totals_basis, provider_id, connection_id, dedupe_key, normalizer_version_id, superseded_at)
		SELECT $1, $2, '2026-09-01T22:00:00Z', '2026-09-02T06:00:00Z', '2026-09-02', true, 'stages', c.provider_id, c.id, substr(sha256('s'), 1, 16), $4, now() - interval '100 days'
		FROM connections c WHERE c.id = $3`, sleep, s.user, s.conn, e.nvNew)
	e.exec(`INSERT INTO sleep_stages (session_id, stage, start_at, end_at) VALUES ($1, 'deep', '2026-09-01T23:00:00Z', '2026-09-02T00:00:00Z')`, sleep)
	other := e.newUser("other")
	o1 := e.measurement(other, other.conn, nil, e.nvNew, "o")
	e.correct(other, other.conn, o1, 100, nil, e.nvNew, "o")

	if n, err := lifecycle.PruneSuperseded(ctx, e.d, time.Now()); err != nil || len(n) != 0 {
		t.Fatalf("default policy: %v, %v", n, err)
	}
	setRetention(t, e, s.user, lifecycle.Retention{SupersededDays: 60, IdempotencyKeyDays: 30})
	n, err := lifecycle.PruneSuperseded(ctx, e.d, time.Now())
	if err != nil || n["measurements"] != 1 || n["measurement_groups"] != 1 || n["sleep_sessions"] != 1 {
		t.Fatalf("pruned %v, %v", n, err)
	}
	ids := func(sql string, args ...any) string {
		var s string
		if err := e.scan(sql, []any{&s}, args...); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if got, want := ids(`SELECT string_agg(id::text, ',' ORDER BY id) FROM measurements WHERE user_id = $1`, s.user), fmt.Sprintf("%d,%d", m2, m3); got != want {
		t.Errorf("measurements left %s, want %s", got, want)
	}
	if got, want := ids(`SELECT string_agg(id::text, ',') FROM measurement_groups`), fmt.Sprint(named); got != want || unnamed == named {
		t.Errorf("groups left %s, want %s", got, want)
	}
	if c := e.int(`SELECT count(*) FROM sleep_stages`) + e.int(`SELECT count(*) FROM measurements WHERE user_id = $1`, other.user); c != 2 {
		t.Errorf("stages not cascaded or other owner's rows touched: %d", c)
	}
	setRetention(t, e, s.user, lifecycle.Retention{SupersededDays: 30, IdempotencyKeyDays: 30})
	if n, err := lifecycle.PruneSuperseded(ctx, e.d, time.Now()); err != nil || n["measurements"] != 1 {
		t.Fatalf("second prune %v, %v", n, err)
	}
	if got := ids(`SELECT string_agg(id::text, ',') FROM measurements WHERE user_id = $1`, s.user); got != fmt.Sprint(m3) {
		t.Errorf("measurements left %s", got)
	}
}

func TestPruneIdempotencyKeysAndSettings(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.newUser("owner")
	client := newID()
	e.exec(`INSERT INTO clients (id, user_id, connection_id, kind, name, token_hash) VALUES ($1, $2, $3, 'collector', 'synthetic', sha256('t'))`, client, s.user, s.conn)
	e.exec(`INSERT INTO idempotency_keys (client_id, key, request_sha256, response_status, response_body, created_at)
		VALUES ($1, 'old', sha256('r'), 202, '{}', now() - interval '40 days'), ($1, 'new', sha256('r'), 202, '{}', now() - interval '20 days')`, client)
	if n, err := lifecycle.PruneIdempotencyKeys(ctx, e.d, time.Now()); err != nil || n != 1 {
		t.Fatalf("default 30 days: %d, %v", n, err)
	}
	setRetention(t, e, s.user, lifecycle.Retention{IdempotencyKeyDays: 7})
	if n, err := lifecycle.PruneIdempotencyKeys(ctx, e.d, time.Now()); err != nil || n != 1 {
		t.Fatalf("7 days: %d, %v", n, err)
	}

	for _, bad := range []lifecycle.Retention{
		{IdempotencyKeyDays: 6},
		{IdempotencyKeyDays: 30, SupersededDays: -1},
		{IdempotencyKeyDays: 30, RawDays: map[string]int{"nope": 30}},
		{IdempotencyKeyDays: 30, RawDays: map[string]int{"withings": -1}},
	} {
		err := e.d.Tx(ctx, func(q *dbq.Queries) error { return lifecycle.SetRetention(ctx, q, s.user, audit.Owner, bad) })
		if !errors.Is(err, lifecycle.ErrInvalidRetention) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	setRetention(t, e, s.user, lifecycle.Retention{RawDays: map[string]int{"withings": 90, "apple_health": 0}, SupersededDays: 365, IdempotencyKeyDays: 14})
	got, err := lifecycle.GetRetention(ctx, e.d.Q(), s.user)
	if err != nil || len(got.RawDays) != 1 || got.RawDays["withings"] != 90 || got.SupersededDays != 365 || got.IdempotencyKeyDays != 14 {
		t.Errorf("stored %+v, %v", got, err)
	}
	if n := e.int(`SELECT count(*) FROM audit_events WHERE action = 'settings.update' AND target_id = 'retention'`); n != 2 {
		t.Errorf("audit events: %d", n)
	}
}
