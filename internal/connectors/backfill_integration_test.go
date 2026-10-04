//go:build integration

package connectors

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

// unitFake answers each backfill unit with one record whose body carries the fetch count, so
// a second committed fetch of a unit would show up as a new raw version.
type unitFake struct {
	mu      sync.Mutex
	fetches map[time.Time]int
	last    time.Time // unit of the latest fetch; units run one at a time per connection
	fail    func(u WorkUnit) error
}

func newUnitFake() (*fake, *unitFake) {
	uf := &unitFake{fetches: map[time.Time]int{}}
	f := &fake{fetch: func(ctx context.Context, _ Conn, _ Credentials, u WorkUnit, out *RawSink) (FetchResult, error) {
		if err := ctx.Err(); err != nil {
			return FetchResult{}, err // an interrupted run makes no provider call
		}
		uf.mu.Lock()
		defer uf.mu.Unlock()
		if uf.fail != nil {
			if err := uf.fail(u); err != nil {
				return FetchResult{}, err
			}
		}
		uf.fetches[u.From.UTC()]++
		uf.last = u.From.UTC()
		out.Put(ingest.RawItem{
			ExternalKey: u.From.UTC().Format(time.RFC3339), ContentType: "application/json",
			Body: fmt.Appendf(nil, `{"synthetic_day":%q,"fetch":%d}`, u.From.UTC().Format(time.DateOnly), uf.fetches[u.From.UTC()]),
		})
		return FetchResult{Done: true, NextCursor: []byte(`{"never":"stored"}`)}, nil
	}}
	return f, uf
}

func (uf *unitFake) count(u time.Time) int {
	uf.mu.Lock()
	defer uf.mu.Unlock()
	return uf.fetches[u.UTC()]
}

// runBackfill runs a job runner with rt until stop is true or for at most d, then stops it
// with a 1 ms grace period, so a running unit is cut off wherever it is.
func (e *env) runBackfill(t *testing.T, rt *Runtime, d time.Duration, stop func() bool) {
	t.Helper()
	r := jobs.NewRunner(e.d, jobs.Config{
		Poll: 20 * time.Millisecond, ReapEvery: 100 * time.Millisecond, Lease: 3 * time.Second,
		Heartbeat: 500 * time.Millisecond, Grace: time.Millisecond, RetryBase: time.Millisecond, RetryMax: 5 * time.Millisecond,
	})
	r.Register(KindBackfillUnit, rt.HandleBackfillUnit)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	for end := time.Now().Add(d); time.Now().Before(end) && !stop(); {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}

func (e *env) backfill(t *testing.T, rt *Runtime, id uuid.UUID) Backfill {
	t.Helper()
	bs, err := rt.ListBackfills(t.Context(), e.conn)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bs {
		if b.ID == id {
			return b
		}
	}
	t.Fatalf("backfill %s not listed", id)
	return Backfill{}
}

func (e *env) createBackfill(t *testing.T, days int) (uuid.UUID, time.Time) {
	t.Helper()
	from := time.Now().AddDate(0, 0, -days-1).UTC().Truncate(24 * time.Hour)
	id, err := e.rt.CreateBackfill(t.Context(), BackfillSpec{
		ConnectionID: e.conn, Stream: stream, From: from, To: from.AddDate(0, 0, days),
	})
	if err != nil {
		t.Fatal(err)
	}
	return id, from
}

// J06.6: a 365-unit backfill interrupted at random points (hard stops of the worker process
// and crashes between fetch and commit) resumes and finishes every unit exactly once.
func TestBackfillResumesAndFinishesEachUnitOnce(t *testing.T) {
	f, uf := newUnitFake()
	e := setup(t, f)
	id, from := e.createBackfill(t, 365)
	if b := e.backfill(t, e.rt, id); b.Pending != 365 || b.Status != "running" {
		t.Fatalf("created: %+v", b)
	}

	crashed := map[time.Time]bool{} // at most one injected crash per unit, so no unit runs out of attempts
	finished := func() bool { return e.backfill(t, e.rt, id).Status != "running" }
	restarts := 0
	for deadline := time.Now().Add(90 * time.Second); !finished(); restarts++ {
		if time.Now().After(deadline) {
			t.Fatalf("backfill not finished after %d restarts: %+v", restarts, e.backfill(t, e.rt, id))
		}
		rt := e.newRuntime()
		rt.beforeCommit = func() error {
			uf.mu.Lock()
			defer uf.mu.Unlock()
			if !crashed[uf.last] && rand.N(20) == 0 {
				crashed[uf.last] = true
				return errors.New("injected crash before commit")
			}
			return nil
		}
		e.runBackfill(t, rt, 10*time.Millisecond+rand.N(150*time.Millisecond), finished)
	}

	if b := e.backfill(t, e.rt, id); b.Status != "done" || b.Done != 365 || b.FinishedAt == nil {
		t.Fatalf("after %d restarts: %+v", restarts, b)
	}
	// Each unit committed once: one raw row, first version, no re-fetch after it was done.
	var keys, rows, firstVersions int
	e.scan(`SELECT count(DISTINCT external_key), count(*), count(*) FILTER (WHERE supersedes_id IS NULL) FROM raw_payloads`,
		nil, &keys, &rows, &firstVersions)
	if keys != 365 || rows != 365 || firstVersions != 365 {
		t.Fatalf("raw: %d keys, %d rows, %d first versions; want 365 each", keys, rows, firstVersions)
	}
	wasted := 0
	for d := range 365 {
		u := from.AddDate(0, 0, d)
		n := uf.count(u)
		if n < 1 {
			t.Fatalf("unit %s never fetched", u.Format(time.DateOnly))
		}
		if crashed[u.UTC()] {
			n--
		}
		wasted += max(n-1, 0)
	}
	if wasted > restarts { // a hard stop can cut off at most the fetch in flight
		t.Fatalf("%d re-fetches of committed or uncrashed units over %d restarts", wasted, restarts)
	}
	if cur, _ := e.cursor(t); cur != "" {
		t.Fatalf("backfill moved the stream cursor to %s", cur)
	}
	if n := e.normalizeJobs(t); n != 365 {
		t.Fatalf("%d normalize jobs, want 365", n)
	}
	t.Logf("%d restarts, %d injected crashes, %d re-fetches after hard stops", restarts, len(crashed), wasted)
}

// Units that keep failing end failed, are listed with their error class, and can be retried
// one at a time without touching the others.
func TestBackfillFailedUnitsRetryIndividually(t *testing.T) {
	f, uf := newUnitFake()
	e := setup(t, f)
	id, from := e.createBackfill(t, 10)
	bad := map[time.Time]bool{from.AddDate(0, 0, 3): true, from.AddDate(0, 0, 7): true}
	failing := true
	uf.fail = func(u WorkUnit) error {
		if failing && bad[u.From.UTC()] {
			return fmt.Errorf("synthetic 503: %w", ErrTransient)
		}
		return nil
	}
	ended := func(want string) func() bool {
		return func() bool { return e.backfill(t, e.rt, id).Status == want }
	}
	e.runBackfill(t, e.rt, 20*time.Second, ended("failed"))
	b := e.backfill(t, e.rt, id)
	failed, err := e.rt.BackfillUnits(t.Context(), id, "failed")
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != "failed" || b.Done != 8 || b.Failed != 2 || len(failed) != 2 {
		t.Fatalf("backfill %+v, failed units %+v", b, failed)
	}
	for _, u := range failed {
		if !bad[u.From.UTC()] || u.ErrorClass != ClassTransient || u.Attempts != 5 || !u.To.Equal(u.From.AddDate(0, 0, 1)) {
			t.Fatalf("failed unit %+v", u)
		}
	}
	if c := e.connection(t); c.status != "active" {
		t.Fatalf("connection %+v; a failing unit must not disable it", c)
	}

	uf.mu.Lock()
	failing = false
	uf.mu.Unlock()
	first, second := failed[0].From, failed[1].From
	if n, err := e.rt.RetryBackfill(t.Context(), id, &first); err != nil || n != 1 {
		t.Fatalf("retry one unit: %d, %v", n, err)
	}
	e.runBackfill(t, e.rt, 10*time.Second, ended("failed"))
	b = e.backfill(t, e.rt, id)
	if b.Status != "failed" || b.Done != 9 || b.Failed != 1 || uf.count(first) != 1 || uf.count(second) != 0 {
		t.Fatalf("after retrying one unit: %+v, fetches %d/%d", b, uf.count(first), uf.count(second))
	}

	if n, err := e.rt.RetryBackfill(t.Context(), id, nil); err != nil || n != 1 {
		t.Fatalf("retry the rest: %d, %v", n, err)
	}
	e.runBackfill(t, e.rt, 10*time.Second, ended("done"))
	if b = e.backfill(t, e.rt, id); b.Status != "done" || b.Done != 10 || uf.count(second) != 1 {
		t.Fatalf("after retrying all: %+v", b)
	}
	if n, err := e.rt.RetryBackfill(t.Context(), id, nil); err != nil || n != 0 {
		t.Fatalf("retry of a done backfill: %d, %v", n, err)
	}
}

func TestBackfillCancel(t *testing.T) {
	f, uf := newUnitFake()
	e := setup(t, f)
	id, _ := e.createBackfill(t, 30)
	if err := e.rt.CancelBackfill(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	var queued, cancelled int
	e.scan(`SELECT count(*) FILTER (WHERE status = 'queued'), count(*) FILTER (WHERE status = 'cancelled') FROM jobs WHERE kind = $1`,
		[]any{KindBackfillUnit}, &queued, &cancelled)
	if queued != 0 || cancelled != 30 {
		t.Fatalf("jobs: %d queued, %d cancelled", queued, cancelled)
	}
	e.runBackfill(t, e.rt, 200*time.Millisecond, func() bool { return false })
	if b := e.backfill(t, e.rt, id); b.Status != "cancelled" || b.FinishedAt == nil || len(uf.fetches) != 0 {
		t.Fatalf("after cancel: %+v, %d units fetched", b, len(uf.fetches))
	}
	if err := e.rt.CancelBackfill(t.Context(), id); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("second cancel: %v", err)
	}
	if n, err := e.rt.RetryBackfill(t.Context(), id, nil); err != nil || n != 0 {
		t.Fatalf("retry of a cancelled backfill: %d, %v", n, err)
	}
}

func TestCreateBackfillValidation(t *testing.T) {
	f, _ := newUnitFake()
	e := setup(t, f)
	now := time.Now()
	for name, spec := range map[string]BackfillSpec{
		"tiny units":     {From: now.Add(-time.Hour), UnitSize: time.Second},
		"empty range":    {From: now.Add(time.Hour)},
		"too far back":   {From: now.AddDate(-3, 0, 0)},
		"unknown":        {From: now.Add(-24 * time.Hour), Stream: "withings.unknown"},
		"too many units": {From: now.AddDate(-1, 0, 0), UnitSize: time.Minute},
	} {
		spec.ConnectionID = e.conn
		if spec.Stream == "" {
			spec.Stream = stream
		}
		if _, err := e.rt.CreateBackfill(t.Context(), spec); !errors.Is(err, ErrInvalidBackfill) {
			t.Errorf("%s: %v, want ErrInvalidBackfill", name, err)
		}
	}
	e.exec(`UPDATE connections SET status = 'needs_reauth' WHERE id = $1`, e.conn)
	if _, err := e.rt.CreateBackfill(t.Context(), BackfillSpec{ConnectionID: e.conn, Stream: stream, From: now.Add(-48 * time.Hour)}); !errors.Is(err, ErrInvalidBackfill) {
		t.Errorf("inactive connection: %v", err)
	}
	var n int
	e.scan(`SELECT count(*) FROM backfills`, nil, &n)
	if n != 0 {
		t.Fatalf("%d backfills stored by invalid requests", n)
	}
}
