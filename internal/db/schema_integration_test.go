//go:build integration

package db_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
)

const (
	codeUnique    = "23505"
	codeForbidden = "42501"
)

func TestMigrationsDownUp(t *testing.T) {
	ctx := t.Context()
	owner := dbtest.Pool(t, dbtest.Empty(t), db.OwnerRole)
	m, err := db.NewMigrator(owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DownTo(ctx, 0); err != nil {
		t.Fatal(err)
	}
	var left int
	err = owner.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'S', 'v', 'm') AND c.relname NOT LIKE 'goose_db_version%'`, db.Schema).Scan(&left)
	if err != nil || left != 0 {
		t.Fatalf("after down-to 0: %d relations left (err %v)", left, err)
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("up after down: %v", err)
	}
}

func TestAppRolePrivileges(t *testing.T) {
	_, app := dbtest.Migrated(t)
	for _, tc := range []struct {
		stmt     string
		wantCode string
	}{
		{"INSERT INTO audit_events (actor, action) VALUES ('system', 'test')", ""},
		{"SELECT count(*) FROM audit_events", ""},
		{"UPDATE audit_events SET action = 'changed'", codeForbidden},
		{"DELETE FROM audit_events", codeForbidden},
		{"TRUNCATE audit_events", codeForbidden},
		{"TRUNCATE jobs", codeForbidden},
		{"INSERT INTO providers (code, name) VALUES ('fake', 'Fake')", codeForbidden},
		{"UPDATE providers SET name = 'Renamed'", codeForbidden},
		{"SELECT register_provider('Not A Code', 'Bad')", "23514"},
		{"UPDATE metric_catalog SET code = code", codeForbidden},
		{"INSERT INTO known_relay_origins (provider_id, origin_pattern, relayed_provider_id) VALUES (1, 'org.example.%', 1)", ""},
	} {
		assertCode(t, app, []string{tc.stmt}, tc.wantCode)
	}
	// Sidecar providers are added only through register_provider; a known code is kept.
	for _, stmt := range []string{"SELECT register_provider('fake_sidecar', 'Fake sidecar')", "SELECT register_provider('withings', 'Renamed')"} {
		if _, err := app.Exec(t.Context(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	var names string
	if err := app.QueryRow(t.Context(), "SELECT string_agg(name, ',' ORDER BY code) FROM providers WHERE code IN ('fake_sidecar', 'withings')").Scan(&names); err != nil {
		t.Fatal(err)
	}
	if names != "Fake sidecar,Withings" {
		t.Fatalf("providers after register_provider: %s", names)
	}
}

// Synthetic fixture ids (UUIDv7 layout, fixed for readability).
const (
	userID  = "01900000-0000-7000-8000-000000000001"
	connID  = "01900000-0000-7000-8000-0000000000c1"
	batchID = "01900000-0000-7000-8000-0000000000b1"
)

func TestConstraints(t *testing.T) {
	ctx := t.Context()
	u, app := dbtest.Migrated(t)
	// Catalogues are read-only for the app role, so the fixture is written as owner. The metric_catalog seed (00010) provides metric id 1.
	owner := dbtest.Pool(t, u, db.OwnerRole)
	for _, s := range []string{
		"INSERT INTO users (id, username, password_hash) VALUES ('" + userID + "', 'owner', 'synthetic')",
		"INSERT INTO connections (id, user_id, provider_id, account_key, mode, status) VALUES ('" + connID + "', '" + userID +
			"', (SELECT id FROM providers WHERE code = 'withings'), sha256('acct-1'), 'in_process', 'active')",
		"INSERT INTO normalizer_versions (name, version, git_sha) VALUES ('test', 1, 'dev')",
		"INSERT INTO blobs (sha256, size_bytes, stored_bytes, compression) VALUES (sha256('a'), 1, 1, 'none'), (sha256('b'), 1, 1, 'none')",
		"INSERT INTO ingest_batches (id, user_id, connection_id, source_kind) VALUES ('" + batchID + "', '" + userID + "', '" + connID + "', 'sync')",
	} {
		if _, err := owner.Exec(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}

	conn := func(id, accountKey string) string {
		return fmt.Sprintf("INSERT INTO connections (id, user_id, provider_id, account_key, mode, status) VALUES ('%s', '%s', 1, %s, 'push', 'active')",
			id, userID, accountKey)
	}
	device := func(id, fp string) string {
		return fmt.Sprintf("INSERT INTO devices (id, user_id, provider_id, fingerprint) VALUES ('%s', '%s', 1, '%s')", id, userID, fp)
	}
	job := func(id, status string, exclusive bool, dedupe string) string {
		return fmt.Sprintf(`INSERT INTO jobs (id, kind, status, connection_id, exclusive, dedupe_key, lease_owner, lease_expires_at)
			VALUES ('%s', 'sync', '%s', '%s', %t, %s, 'w1', now())`, id, status, connID, exclusive, dedupe)
	}
	raw := func(version int, blob string) string {
		supersedes := "(SELECT max(id) FROM raw_payloads)"
		if version == 1 {
			supersedes = "NULL"
		}
		return fmt.Sprintf(`INSERT INTO raw_payloads (user_id, connection_id, batch_id, stream, external_key, version, supersedes_id,
			content_sha256, content_type, fetched_at)
			VALUES ('%s', '%s', '%s', 'withings.measures', 'grp-1', %d, %s, sha256('%s'), 'application/json', now())`,
			userID, connID, batchID, version, supersedes, blob)
	}
	measurement := func(supersededAt string) string {
		return fmt.Sprintf(`INSERT INTO measurements (user_id, metric_id, kind, start_at, local_date, value, provider_id, connection_id,
			dedupe_key, normalizer_version_id, superseded_at)
			VALUES ('%s', 1, 'sample', '2026-01-01T08:00:00Z', '2026-01-01', 60, 1, '%s', substring(sha256('k') for 16), 1, %s)`,
			userID, connID, supersededAt)
	}
	const (
		id1 = "01900000-0000-7000-8000-000000000101"
		id2 = "01900000-0000-7000-8000-000000000102"
	)

	for _, tc := range []struct {
		name     string
		stmts    []string // run in one rolled-back transaction; only the last may fail
		wantCode string
	}{
		{"connection same account", []string{conn(id1, "sha256('acct-1')")}, codeUnique},
		{"connection other account", []string{conn(id1, "sha256('acct-2')")}, ""},
		{"connection unknown accounts", []string{conn(id1, "NULL"), conn(id2, "NULL")}, ""},
		{"device same fingerprint", []string{device(id1, "fp"), device(id2, "fp")}, codeUnique},
		{"device other fingerprint", []string{device(id1, "fp"), device(id2, "fp2")}, ""},
		{"two exclusive running", []string{job(id1, "running", true, "NULL"), job(id2, "running", true, "NULL")}, codeUnique},
		{"exclusive and shared running", []string{job(id1, "running", true, "NULL"), job(id2, "running", false, "NULL")}, ""},
		{"dedupe queued twice", []string{job(id1, "queued", false, "'s:1'"), job(id2, "queued", false, "'s:1'")}, codeUnique},
		{"dedupe queued and running", []string{job(id1, "running", false, "'s:1'"), job(id2, "queued", false, "'s:1'")}, codeUnique},
		{"dedupe after success", []string{job(id1, "succeeded", false, "'s:1'"), job(id2, "queued", false, "'s:1'")}, ""},
		{"raw same version", []string{raw(1, "a"), raw(1, "b")}, codeUnique},
		{"raw new version", []string{raw(1, "a"), raw(2, "b")}, ""},
		{"raw reverted content", []string{raw(1, "a"), raw(2, "b"), raw(3, "a")}, ""},
		{"measurement active twice", []string{measurement("NULL"), measurement("NULL")}, codeUnique},
		{"measurement superseded", []string{measurement("now()"), measurement("NULL")}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) { assertCode(t, app, tc.stmts, tc.wantCode) })
	}
}

// assertCode runs stmts in a transaction it rolls back and checks the SQLSTATE of the last one.
func assertCode(t *testing.T, pool *pgxpool.Pool, stmts []string, wantCode string) {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for i, s := range stmts {
		_, err = tx.Exec(ctx, s)
		if i < len(stmts)-1 && err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}
	var pgErr *pgconn.PgError
	switch {
	case wantCode == "" && err != nil:
		t.Errorf("%s: unexpected error %v", stmts[len(stmts)-1], err)
	case wantCode != "" && (!errors.As(err, &pgErr) || pgErr.Code != wantCode):
		t.Errorf("%s: want SQLSTATE %s, got %v", stmts[len(stmts)-1], wantCode, err)
	}
}
