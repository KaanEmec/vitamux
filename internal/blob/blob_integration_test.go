//go:build integration

package blob

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

var errCrash = errors.New("simulated crash before the referencing row")

// rawSQL runs test-only statements on the pool (pgx types stay inside internal/db).
type rawSQL struct {
	Exec     func(sql string, args ...any) error
	QueryRow func(sql string, args []any, dest ...any) error
}

func setup(t *testing.T) (*Store, *db.DB, rawSQL) {
	t.Helper()
	_, pool := dbtest.Migrated(t)
	kr, _ := testKeyring(t, t.TempDir())
	s, err := Open(t.TempDir(), kr)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	return s, db.New(pool), rawSQL{
		Exec: func(sql string, args ...any) error { _, err := pool.Exec(ctx, sql, args...); return err },
		QueryRow: func(sql string, args []any, dest ...any) error {
			return pool.QueryRow(ctx, sql, args...).Scan(dest...)
		},
	}
}

func put(t *testing.T, s *Store, d *db.DB, content []byte, mode Mode, retain bool) Info {
	t.Helper()
	var info Info
	err := d.Tx(context.Background(), func(q *dbq.Queries) error {
		var err error
		if info, err = s.Put(context.Background(), q, bytes.NewReader(content), mode); err != nil {
			return err
		}
		if retain {
			return Retain(context.Background(), q, info.SHA256)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func sweep(t *testing.T, s *Store, d *db.DB, grace time.Duration) SweepStats {
	t.Helper()
	st, err := Sweep(context.Background(), d, s, grace)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestCrashOrphanSweptOnlyAfterGrace(t *testing.T) {
	s, d, _ := setup(t)
	ctx := context.Background()
	var sum []byte
	err := d.Tx(ctx, func(q *dbq.Queries) error {
		info, err := s.Put(ctx, q, bytes.NewReader(synthetic(50)), Plain)
		sum = info.SHA256
		if err != nil {
			return err
		}
		return errCrash
	})
	if !errors.Is(err, errCrash) {
		t.Fatalf("got %v", err)
	}
	path := s.path(sum)
	if !exists(path) {
		t.Fatal("blob file should survive the rolled-back transaction")
	}

	if st := sweep(t, s, d, DefaultGrace); st.Files != 0 || !exists(path) {
		t.Fatalf("orphan removed inside the grace period: %+v", st)
	}
	old := time.Now().Add(-DefaultGrace - time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if st := sweep(t, s, d, DefaultGrace); st.Files != 1 || exists(path) {
		t.Fatalf("orphan not removed after the grace period: %+v", st)
	}
}

func TestUnreferencedRowsSweptAfterGrace(t *testing.T) {
	s, d, sql := setup(t)
	ctx := context.Background()
	kept := put(t, s, d, synthetic(1), Plain, true)
	loose := put(t, s, d, synthetic(2), Plain, false)

	if st := sweep(t, s, d, DefaultGrace); st.Rows != 0 {
		t.Fatalf("fresh unreferenced row swept: %+v", st)
	}
	if err := sql.Exec("UPDATE blobs SET created_at = now() - interval '2 days'"); err != nil {
		t.Fatal(err)
	}
	if st := sweep(t, s, d, DefaultGrace); st.Rows != 1 || exists(s.path(loose.SHA256)) {
		t.Fatalf("unreferenced row not swept: %+v", st)
	}
	if got, err := s.Get(kept.SHA256); err != nil || !bytes.Equal(got, synthetic(1)) {
		t.Fatalf("referenced blob damaged: %v", err)
	}

	if err := d.Tx(ctx, func(q *dbq.Queries) error { return Release(ctx, q, kept.SHA256) }); err != nil {
		t.Fatal(err)
	}
	if st := sweep(t, s, d, DefaultGrace); st.Rows != 1 || exists(s.path(kept.SHA256)) {
		t.Fatalf("released blob not swept: %+v", st)
	}
	err := d.Tx(ctx, func(q *dbq.Queries) error { return Retain(ctx, q, kept.SHA256) })
	if !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("Retain of a swept blob: got %v, want ErrNotFound", err)
	}
}

func TestPutDeduplicatesAndHeals(t *testing.T) {
	s, d, sql := setup(t)
	ctx := context.Background()
	a := put(t, s, d, synthetic(7), Plain, true)
	b := put(t, s, d, synthetic(7), Plain, true)
	if !bytes.Equal(a.SHA256, b.SHA256) {
		t.Fatal("same content, different hash")
	}
	var refs int
	if err := sql.QueryRow("SELECT refcount FROM blobs WHERE sha256 = $1", []any{a.SHA256}, &refs); err != nil || refs != 2 {
		t.Fatalf("refcount = %d, %v; want 2", refs, err)
	}

	if err := os.Remove(s.path(a.SHA256)); err != nil {
		t.Fatal(err)
	}
	put(t, s, d, synthetic(7), Plain, false)
	if _, err := s.Get(a.SHA256); err != nil {
		t.Fatalf("missing file not healed: %v", err)
	}

	err := d.Tx(ctx, func(q *dbq.Queries) error {
		_, err := s.Put(ctx, q, bytes.NewReader(synthetic(7)), Sealed)
		return err
	})
	if !errors.Is(err, ErrNotSealed) {
		t.Fatalf("sealed put over plain content: got %v", err)
	}
	sealed := put(t, s, d, synthetic(8), Sealed, true)
	var keyID *string
	if err := sql.QueryRow("SELECT key_id FROM blobs WHERE sha256 = $1", []any{sealed.SHA256}, &keyID); err != nil || keyID == nil {
		t.Fatalf("sealed blob has no key_id: %v", err)
	}
	if got, err := s.Get(sealed.SHA256); err != nil || !bytes.Equal(got, synthetic(8)) {
		t.Fatalf("sealed round trip: %v", err)
	}
}

// TestSweepJob runs the registered handler through a real Runner: an old unreferenced blob is
// gone once the enqueued job has succeeded, a referenced one stays.
func TestSweepJob(t *testing.T) {
	s, d, sql := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	kept := put(t, s, d, synthetic(1), Plain, true)
	loose := put(t, s, d, synthetic(2), Plain, false)
	if err := sql.Exec("UPDATE blobs SET created_at = now() - interval '2 days'"); err != nil {
		t.Fatal(err)
	}

	r := jobs.NewRunner(d, jobs.Config{Poll: 50 * time.Millisecond})
	r.Register(KindSweep, SweepJob(d, s, slog.New(slog.DiscardHandler)))
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	var id uuid.UUID
	if err := d.Tx(ctx, func(q *dbq.Queries) (err error) {
		id, _, err = jobs.Enqueue(ctx, q, jobs.NewJob{Kind: KindSweep})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var status string
	for range 100 {
		if err := sql.QueryRow("SELECT status FROM jobs WHERE id = $1", []any{id}, &status); err != nil {
			t.Fatal(err)
		}
		if status == "succeeded" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if status != "succeeded" {
		t.Fatalf("job status %q", status)
	}
	if exists(s.path(loose.SHA256)) || !exists(s.path(kept.SHA256)) {
		t.Fatal("sweep job did not remove exactly the unreferenced blob")
	}
}
