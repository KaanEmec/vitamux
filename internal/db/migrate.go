package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrator applies the embedded migrations under a PostgreSQL advisory lock, so concurrent
// runs serialize and the later one finds nothing to do.
type Migrator struct{ p *goose.Provider }

// NewMigrator expects a pool opened with OwnerRole.
func NewMigrator(pool *pgxpool.Pool) (*Migrator, error) {
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 600))
	if err != nil {
		return nil, err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), migrationFS(),
		goose.WithSessionLocker(locker), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return nil, err
	}
	return &Migrator{p}, nil
}

func (m *Migrator) Up(ctx context.Context) ([]*goose.MigrationResult, error) { return m.p.Up(ctx) }

func (m *Migrator) Status(ctx context.Context) ([]*goose.MigrationStatus, error) {
	return m.p.Status(ctx)
}

// DownTo rolls back to version. The CLI allows it in development only.
func (m *Migrator) DownTo(ctx context.Context, version int64) ([]*goose.MigrationResult, error) {
	return m.p.DownTo(ctx, version)
}

func migrationFS() fs.FS {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		panic(err) // the directory is embedded at build time
	}
	return sub
}

// ExpectedVersion is the newest embedded migration, i.e. the schema this binary needs.
func ExpectedVersion() int64 {
	var v int64
	entries, _ := fs.ReadDir(migrationFS(), ".")
	for _, e := range entries {
		if n, err := goose.NumericComponent(e.Name()); err == nil && n > v {
			v = n
		}
	}
	return v
}

// CheckSchema fails unless the database is at exactly ExpectedVersion. It only reads, so it
// works with the app role.
func CheckSchema(ctx context.Context, pool *pgxpool.Pool) error {
	var current int64
	err := pool.QueryRow(ctx, "SELECT coalesce(max(version_id), 0) FROM goose_db_version").Scan(&current)
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "42P01" {
		current, err = 0, nil // undefined_table: never migrated
	}
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	switch want := ExpectedVersion(); {
	case current < want:
		return fmt.Errorf("database schema is at version %d, this binary needs %d: run `vitamux migrate up`", current, want)
	case current > want:
		return fmt.Errorf("database schema is at version %d, newer than this binary (%d): upgrade vitamux", current, want)
	}
	return nil
}
