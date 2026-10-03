package db

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// DB is the query layer: sqlc queries, transactions and bulk COPY over one pool.
type DB struct{ pool *pgxpool.Pool }

// New wraps a pool from Open. The caller keeps ownership of the pool and closes it.
func New(pool *pgxpool.Pool) *DB { return &DB{pool} }

// Q returns the generated queries on the pool, outside a transaction. Wrap their errors
// with MapErr when callers branch on ErrNotFound or ErrConflict.
func (d *DB) Q() *dbq.Queries { return dbq.New(d.pool) }

// Ping checks that the database answers.
func (d *DB) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

// TxOption changes how Tx begins its transaction.
type TxOption func(*pgx.TxOptions)

// Serializable runs the transaction at SERIALIZABLE isolation instead of READ COMMITTED.
func Serializable() TxOption {
	return func(o *pgx.TxOptions) { o.IsoLevel = pgx.Serializable }
}

const (
	txAttempts = 4
	txBackoff  = 10 * time.Millisecond
)

// Tx runs fn in a transaction: commit on nil, roll back on error. A serialization failure
// (40001) or deadlock (40P01) retries the whole of fn a few times with jittered backoff, so
// fn must be safe to run again and must not keep state from a failed attempt. The returned
// error passes through MapErr.
func (d *DB) Tx(ctx context.Context, fn func(*dbq.Queries) error, opts ...TxOption) error {
	var o pgx.TxOptions // read committed unless an option says otherwise
	for _, opt := range opts {
		opt(&o)
	}
	var err error
	for attempt := range txAttempts {
		if attempt > 0 {
			// Full jitter over an exponentially growing window.
			wait := rand.N(txBackoff << attempt) //nolint:gosec // jitter, not security
			select {
			case <-ctx.Done():
				return MapErr(ctx.Err())
			case <-time.After(wait):
			}
		}
		if err = d.tryTx(ctx, o, fn); !sqlState(err, codeSerializationFailure, codeDeadlockDetected) {
			break
		}
	}
	return MapErr(err)
}

func (d *DB) tryTx(ctx context.Context, o pgx.TxOptions, fn func(*dbq.Queries) error) error {
	tx, err := d.pool.BeginTx(ctx, o)
	if err != nil {
		return err
	}
	if err := fn(dbq.New(tx)); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// CopyFrom bulk-inserts rows into table with the binary COPY protocol and returns the number
// copied. Use it for high-volume inserts (measurements, raw batches); it fails on any
// conflict instead of skipping rows, and is all-or-nothing.
func (d *DB) CopyFrom(ctx context.Context, table string, columns []string, rows [][]any) (int64, error) {
	n, err := d.pool.CopyFrom(ctx, pgx.Identifier{table}, columns, pgx.CopyFromRows(rows))
	return n, MapErr(err)
}
