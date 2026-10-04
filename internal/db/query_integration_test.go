//go:build integration

package db_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
)

func newDB(t *testing.T) (*db.DB, *pgxpool.Pool) {
	t.Helper()
	_, pool := dbtest.Migrated(t)
	return db.New(pool), pool
}

func count(t *testing.T, pool *pgxpool.Pool, table string) (n int) {
	t.Helper()
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCopyFrom100kRows(t *testing.T) {
	d, pool := newDB(t)
	const total = 100_000
	rows := make([][]any, total)
	for i := range rows {
		rows[i] = []any{"system", "synthetic.copy", json.RawMessage(fmt.Sprintf(`{"i":%d}`, i))}
	}
	n, err := d.CopyFrom(t.Context(), "audit_events", []string{"actor", "action", "detail"}, rows)
	if err != nil || n != total {
		t.Fatalf("copied %d rows, err %v", n, err)
	}
	if got := count(t, pool, "audit_events"); got != total {
		t.Fatalf("stored %d rows, want %d", got, total)
	}
}

func TestNotFoundAndConflict(t *testing.T) {
	d, _ := newDB(t)
	ctx := t.Context()

	_, err := d.Q().GetUserByUsername(ctx, "nobody")
	if err = db.MapErr(err); !errors.Is(err, db.ErrNotFound) || !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("direct query: got %v", err)
	}
	err = d.Tx(ctx, func(q *dbq.Queries) error {
		_, err := q.GetUserByUsername(ctx, "nobody")
		return err
	})
	if !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("in Tx: got %v", err)
	}

	cols := []string{"id", "username", "password_hash"}
	insert := func() error {
		_, err := d.CopyFrom(ctx, "users", cols, [][]any{{uuid.Must(uuid.NewV7()), "owner", "$argon2id$synthetic"}})
		return err
	}
	if err := insert(); err != nil {
		t.Fatal(err)
	}
	err = insert()
	var pg *pgconn.PgError
	if !errors.Is(err, db.ErrConflict) || !errors.As(err, &pg) || pg.Code != "23505" {
		t.Fatalf("duplicate username: got %v", err)
	}
	u, err := d.Q().GetUserByUsername(ctx, "owner")
	if err != nil || u.Username != "owner" || u.TotpKeyID != nil {
		t.Fatalf("get user: %+v, %v", u, err)
	}
}

func TestTxCommitAndRollback(t *testing.T) {
	d, pool := newDB(t)
	ctx := t.Context()
	insert := func(q *dbq.Queries) error {
		_, err := q.InsertAuditEvent(ctx, dbq.InsertAuditEventParams{Actor: "system", Action: "test", Detail: json.RawMessage(`{}`)})
		return err
	}
	if err := d.Tx(ctx, insert); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := d.Tx(ctx, func(q *dbq.Queries) error {
		if err := insert(q); err != nil {
			return err
		}
		return boom
	}, db.Serializable())
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if got := count(t, pool, "audit_events"); got != 1 {
		t.Fatalf("%d rows after one commit and one rollback, want 1", got)
	}
}

func TestTxRetriesTransientFailures(t *testing.T) {
	d, pool := newDB(t)
	ctx := t.Context()
	for _, code := range []string{"40001", "40P01"} {
		attempts := 0
		err := d.Tx(ctx, func(q *dbq.Queries) error {
			attempts++
			if _, err := q.InsertAuditEvent(ctx, dbq.InsertAuditEventParams{Actor: "system", Action: code, Detail: json.RawMessage(`{}`)}); err != nil {
				return err
			}
			if attempts < 3 {
				return fmt.Errorf("attempt %d: %w", attempts, &pgconn.PgError{Code: code})
			}
			return nil
		})
		if err != nil || attempts != 3 {
			t.Fatalf("%s: %d attempts, err %v", code, attempts, err)
		}
	}
	if got := count(t, pool, "audit_events"); got != 2 {
		t.Fatalf("%d rows, want one per committed attempt only", got)
	}

	attempts := 0
	err := d.Tx(ctx, func(*dbq.Queries) error {
		attempts++
		return &pgconn.PgError{Code: "40001"}
	})
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || attempts < 2 || attempts > 10 {
		t.Fatalf("persistent failure: %d attempts, err %v", attempts, err)
	}

	attempts = 0
	err = d.Tx(ctx, func(*dbq.Queries) error {
		attempts++
		return &pgconn.PgError{Code: "23505"}
	})
	if !errors.Is(err, db.ErrConflict) || attempts != 1 {
		t.Fatalf("conflict: %d attempts, err %v", attempts, err)
	}
}
