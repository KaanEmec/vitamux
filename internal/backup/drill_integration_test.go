//go:build integration

package backup_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/backup"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/db/fixtureload"
	"github.com/KaanEmec/vitamux/internal/resolve"
)

// TestRestoreDrill is the CI restore drill (J13.5): generate a fixturegen slice, load it, back
// it up, reject a tampered copy, restore into a fresh database and data directory, then compare
// row counts, privileges, blob files and resolved results. It needs pg_dump and pg_restore of
// the server's major version on PATH (the CI integration job installs them).
func TestRestoreDrill(t *testing.T) {
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("CI") != "" {
				t.Fatalf("%s is required in CI", tool)
			}
			t.Skipf("%s not on PATH", tool)
		}
	}
	ctx := t.Context()
	keyFile := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.WriteKeyFile(keyFile); err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.Load(keyFile)
	if err != nil {
		t.Fatal(err)
	}

	// Source instance: a five-day slice plus real blob files for its raw payloads.
	srcURL, srcApp := dbtest.Migrated(t)
	// pg_dump refuses a server newer than itself; skip locally when the majors differ.
	var serverVersion string // SHOW returns text
	if err := srcApp.QueryRow(ctx, "SHOW server_version_num").Scan(&serverVersion); err != nil {
		t.Fatal(err)
	}
	serverNum, _ := strconv.Atoi(serverVersion)
	if out, err := exec.CommandContext(ctx, "pg_dump", "--version").Output(); err == nil {
		var major int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "pg_dump (PostgreSQL) %d", &major); err == nil && major != serverNum/10000 {
			if os.Getenv("CI") != "" {
				t.Fatalf("pg_dump %d does not match server %d", major, serverNum/10000)
			}
			t.Skipf("pg_dump %d does not match server %d", major, serverNum/10000)
		}
	}
	stats, err := fixtureload.Load(ctx, srcApp, generate(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srcApp.Exec(ctx, `INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES ($1, $2, 'Europe/Berlin', '2024-01-01T00:00:00Z')`,
		uuid.New(), stats.UserID); err != nil {
		t.Fatal(err)
	}
	src := db.New(srcApp)
	srcData := t.TempDir()
	store, err := blob.Open(filepath.Join(srcData, "blobs"), kr)
	if err != nil {
		t.Fatal(err)
	}
	// fixtureload inserts blob rows whose content is the payload's external key; Put heals the files.
	rows, err := srcApp.Query(ctx, `SELECT DISTINCT external_key FROM raw_payloads`)
	if err != nil {
		t.Fatal(err)
	}
	var payloads []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, k)
	}
	if rows.Err() != nil || len(payloads) == 0 {
		t.Fatalf("raw payloads: %d, %v", len(payloads), rows.Err())
	}
	lab := "synthetic lab report, not a real result"
	var labSum []byte
	if err := src.Tx(ctx, func(q *dbq.Queries) error {
		for _, k := range payloads {
			if _, err := store.Put(ctx, q, strings.NewReader(k), blob.Plain); err != nil {
				return err
			}
		}
		info, err := store.Put(ctx, q, strings.NewReader(lab), blob.Sealed)
		labSum = info.SHA256
		return errors.Join(err, blob.Retain(ctx, q, info.SHA256))
	}); err != nil {
		t.Fatal(err)
	}

	// A provider app secret entered in the panel (ADR-0021) must open after the restore.
	app := connectors.AppCredentials{ClientID: "synthetic-client", ClientSecret: "synthetic-app-secret"}
	if _, err := connectors.NewApps(src, kr, nil).Put(ctx, "withings", app, stats.UserID, "test"); err != nil {
		t.Fatal(err)
	}

	// Backup, as the app role.
	dir, m, err := backup.Create(ctx, backup.Options{DatabaseURL: srcURL, DB: src, DataDir: srcData, Keys: kr, Out: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if m.SchemaVersion != db.ExpectedVersion() || m.BlobCount != len(payloads)+1 || m.KeyID != kr.KeyID() {
		t.Fatalf("manifest = %+v, want schema %d and %d blobs", m, db.ExpectedVersion(), len(payloads)+1)
	}

	dstURL := dbtest.Empty(t)
	dstOwner := dbtest.Pool(t, dstURL, db.OwnerRole)
	dstData := t.TempDir()
	opts := backup.RestoreOptions{From: dir, DatabaseURL: dstURL, DB: db.New(dstOwner), DataDir: dstData, Keys: kr}

	// A tampered manifest is refused before anything is written.
	tampered := filepath.Join(t.TempDir(), filepath.Base(dir))
	if err := os.CopyFS(tampered, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(tampered, backup.ManifestFile))
	raw = []byte(strings.Replace(string(raw), `"blob_count": `, `"blob_count": 1`, 1))
	if err := os.WriteFile(filepath.Join(tampered, backup.ManifestFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := opts
	bad.From = tampered
	if _, err := backup.Restore(ctx, bad); !errors.Is(err, backup.ErrTampered) {
		t.Fatalf("tampered restore: err = %v, want ErrTampered", err)
	}

	// Restore into the fresh database and data directory, then again: the second is refused.
	if _, err := backup.Restore(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Restore(ctx, opts); !errors.Is(err, backup.ErrNotEmpty) {
		t.Fatalf("second restore: err = %v, want ErrNotEmpty", err)
	}

	srcOwner := dbtest.Pool(t, srcURL, db.OwnerRole)
	// Row counts of every table, and every relation's ACL and the default ACLs (the app role
	// must not regain privileges the migrations revoked).
	for _, q := range []string{
		`SELECT format('%I', tablename), (xpath('/row/c/text()', query_to_xml(format('SELECT count(*) AS c FROM %I.%I', schemaname, tablename), false, true, '')))[1]::text
			FROM pg_tables WHERE schemaname = 'vitamux'`,
		`SELECT c.relname::text, coalesce(c.relacl::text, '') FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'vitamux'`,
		`SELECT d.defaclobjtype::text, d.defaclacl::text FROM pg_default_acl d JOIN pg_namespace n ON n.oid = d.defaclnamespace WHERE n.nspname = 'vitamux'`,
	} {
		want, got := pairs(t, srcOwner.Query, q), pairs(t, dstOwner.Query, q)
		if !maps.Equal(want, got) {
			t.Errorf("%s\nsource:   %v\nrestored: %v", q, want, got)
		}
	}
	if !maps.Equal(fileSums(t, filepath.Join(srcData, "blobs")), fileSums(t, filepath.Join(dstData, "blobs"))) {
		t.Error("blob files differ after restore")
	}
	restored, err := blob.Open(filepath.Join(dstData, "blobs"), kr)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := restored.Get(labSum); err != nil || string(got) != lab {
		t.Errorf("sealed blob after restore: %q, %v", got, err)
	}

	// Resolution reproduces on the restored instance (read as the app role).
	dst := db.New(dbtest.Pool(t, dstURL, db.AppRole))
	if got, err := connectors.NewApps(dst, kr, nil).Get(ctx, "withings"); err != nil || got != app {
		t.Errorf("provider app credentials after restore: %v", err)
	}
	now := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	for _, metric := range []string{"heart_rate", "steps", "weight", "blood_pressure", "sleep"} {
		req := resolve.Request{UserID: stats.UserID, Metric: metric, From: day("2025-02-15"), To: day("2025-02-19"), Now: now}
		if metric == "heart_rate" {
			req.Kind = catalog.WindowLocalDay
		}
		want, got := resolved(t, src, req), resolved(t, dst, req)
		if want != got {
			t.Errorf("%s: resolved results differ after restore\nsource:   %.400s\nrestored: %.400s", metric, want, got)
		}
	}
}

func generate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	_, file, _, _ := runtime.Caller(0)
	cmd := exec.CommandContext(t.Context(), "go", "run", "./tools/fixturegen", "-out", dir, "-start", "2025-02-15", "-days", "5", "-hr-step", "60")
	cmd.Dir = filepath.Join(filepath.Dir(file), "..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixturegen: %v\n%s", err, out)
	}
	return dir
}

func day(s string) time.Time {
	d, _ := time.Parse(time.DateOnly, s)
	return d
}

// resolved runs req and returns the results as JSON, without the computation time.
func resolved(t *testing.T, d *db.DB, req resolve.Request) string {
	t.Helper()
	rs, err := resolve.Run(t.Context(), d, req)
	if err != nil {
		t.Fatal(err)
	}
	for i := range rs {
		rs[i].ComputedAt = time.Time{}
	}
	b, err := json.Marshal(rs)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// pairs runs a two-column text query.
func pairs[R interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}](t *testing.T, query func(ctx context.Context, sql string, args ...any) (R, error), q string) map[string]string {
	t.Helper()
	rows, err := query(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// fileSums maps every file under dir to its SHA-256.
func fileSums(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		sum := sha256.Sum256(b)
		rel, _ := filepath.Rel(dir, p)
		out[rel] = hex.EncodeToString(sum[:])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
