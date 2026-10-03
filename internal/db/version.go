package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// SchemaVersion returns the newest applied migration, 0 before the first. It only reads, so
// it works with the app role (`vitamux backup` records it in the manifest).
func (d *DB) SchemaVersion(ctx context.Context) (int64, error) {
	var v int64
	err := d.pool.QueryRow(ctx, "SELECT coalesce(max(version_id), 0) FROM goose_db_version").Scan(&v)
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "42P01" {
		return 0, nil // undefined_table: never migrated
	}
	return v, err
}
