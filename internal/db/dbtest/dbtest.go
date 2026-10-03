// Package dbtest gives integration tests (build tag `integration`) a fresh database per test.
// It needs VITAMUX_DATABASE_URL pointing at a superuser, as in the dev compose file and CI.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KaanEmec/vitamux/internal/db"
)

// Empty creates a database with roles.sql applied but no migrations, and returns its URL.
func Empty(t testing.TB) string {
	t.Helper()
	admin := os.Getenv("VITAMUX_DATABASE_URL")
	if admin == "" {
		t.Fatal("VITAMUX_DATABASE_URL is not set (see .env.example)")
	}
	ctx := context.Background()
	conn := connect(t, admin)
	name := "vitamux_test_" + randHex()
	exec(t, conn, "CREATE DATABASE "+name)
	t.Cleanup(func() {
		exec(t, conn, "DROP DATABASE "+name+" WITH (FORCE)")
		_ = conn.Close(ctx)
	})

	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal("VITAMUX_DATABASE_URL must be a postgres:// URL")
	}
	u.Path = "/" + name
	dbURL := u.String()
	roles, err := os.ReadFile(rolesSQL())
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, dbURL)
	defer func() { _ = c.Close(ctx) }()
	// Roles are cluster-wide; serialize their creation across parallel test packages. Advisory
	// locks are scoped to one database, so take it on the shared admin database, not the new one.
	exec(t, conn, "SELECT pg_advisory_lock(7461626)")
	exec(t, c, string(roles))
	exec(t, conn, "SELECT pg_advisory_unlock(7461626)")
	return dbURL
}

// Migrated returns the URL of a fully migrated database and an app-role pool on it.
func Migrated(t testing.TB) (string, *pgxpool.Pool) {
	t.Helper()
	u := Empty(t)
	ctx := context.Background()
	owner := Pool(t, u, db.OwnerRole)
	m, err := db.NewMigrator(owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
	return u, Pool(t, u, db.AppRole)
}

// seeded lists tables that migrations fill with reference rows although the app role may write them.
// Truncate keeps them, along with every table the app role cannot insert into (providers, metric_catalog, ...).
var seeded = []string{"goose_db_version", "known_relay_origins"}

// Truncate empties every table the app role writes, so one migrated database can serve several
// scenarios inside a test. Reference data seeded by migrations stays. It runs as the owner role.
func Truncate(t testing.TB, dbURL string) {
	t.Helper()
	ctx := context.Background()
	owner := Pool(t, dbURL, db.OwnerRole)
	rows, err := owner.Query(ctx, `SELECT format('%I.%I', schemaname, tablename) FROM pg_tables
		WHERE schemaname = $1 AND tablename <> ALL($2)
		  AND has_table_privilege($3, format('%I.%I', schemaname, tablename), 'INSERT')
		ORDER BY 1`, db.Schema, seeded, string(db.AppRole))
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) == 0 {
		return
	}
	if _, err := owner.Exec(ctx, "TRUNCATE "+strings.Join(tables, ", ")+" RESTART IDENTITY CASCADE"); err != nil {
		t.Fatal(err)
	}
}

// Pool opens a pool as role and closes it when the test ends.
func Pool(t testing.TB, dbURL string, role db.Role) *pgxpool.Pool {
	t.Helper()
	p, err := db.Open(context.Background(), dbURL, role)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func connect(t testing.TB, dbURL string) *pgx.Conn {
	t.Helper()
	c, err := pgx.Connect(context.Background(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func exec(t testing.TB, c *pgx.Conn, sql string) {
	t.Helper()
	if _, err := c.Exec(context.Background(), sql); err != nil {
		t.Fatal(err)
	}
}

func randHex() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func rolesSQL() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "deploy", "sql", "roles.sql")
}
