//go:build integration

package jobs

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
)

// rawDB gives tests plain SQL without importing pgx (depguard: pgx only in internal/db).
type rawDB struct {
	Exec     func(ctx context.Context, sql string, args ...any) error
	QueryRow func(ctx context.Context, sql string, args ...any) interface{ Scan(...any) error }
}

func setup(t *testing.T) (*db.DB, rawDB) {
	t.Helper()
	_, p := dbtest.Migrated(t)
	pool := rawDB{
		Exec: func(ctx context.Context, sql string, args ...any) error {
			_, err := p.Exec(ctx, sql, args...)
			return err
		},
		QueryRow: func(ctx context.Context, sql string, args ...any) interface{ Scan(...any) error } {
			return p.QueryRow(ctx, sql, args...)
		},
	}
	exec(t, pool, "INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')", ownerID)
	return db.New(p), pool
}

var ownerID = uuid.MustParse("00000000-0000-4000-8000-000000000001")

func exec(t *testing.T, pool rawDB, sql string, args ...any) {
	t.Helper()
	if err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func newConnection(t *testing.T, pool rawDB, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, pool, `INSERT INTO connections (id, user_id, provider_id, mode, status)
		VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'withings'), 'in_process', $3)`, id, ownerID, status)
	return id
}

func fast(cfg Config) Config {
	cfg.Poll = 20 * time.Millisecond
	cfg.ReapEvery = 100 * time.Millisecond
	cfg.RetryBase, cfg.RetryMax = time.Millisecond, 5*time.Millisecond
	cfg.Lease = cmp.Or(cfg.Lease, 2*time.Second)
	cfg.Heartbeat = cmp.Or(cfg.Heartbeat, 300*time.Millisecond)
	cfg.Grace = cmp.Or(cfg.Grace, 2*time.Second)
	return cfg
}

// start runs r until the returned stop function is called; stop waits for the drain.
func start(r *Runner) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	return func() { cancel(); <-done }
}

func enqueue(t *testing.T, d *db.DB, j NewJob) uuid.UUID {
	t.Helper()
	id, _, err := Enqueue(context.Background(), d.Q(), j)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func job(t *testing.T, d *db.DB, id uuid.UUID) dbq.Job {
	t.Helper()
	j, err := d.Q().GetJob(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func runs(t *testing.T, d *db.DB, id uuid.UUID) []dbq.JobRun {
	t.Helper()
	rs, err := d.Q().ListJobRuns(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func outcomes(rs []dbq.JobRun) string {
	s := ""
	for _, r := range rs {
		s += fmt.Sprintf("[%s %s]", deref(r.Outcome), deref(r.ErrorClass))
	}
	return s
}

func TestEnqueueDedupe(t *testing.T) {
	d, pool := setup(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	ids := make([]uuid.UUID, 20)
	created := atomic.Int32{}
	for i := range ids {
		wg.Go(func() {
			id, c, err := Enqueue(ctx, d.Q(), NewJob{Kind: "sweep_blobs", DedupeKey: "manual:sweep"})
			if err != nil {
				t.Error(err)
			}
			if c {
				created.Add(1)
			}
			ids[i] = id
		})
	}
	wg.Wait()
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM jobs").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 || created.Load() != 1 {
		t.Fatalf("%d jobs, %d created; want 1", n, created.Load())
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("duplicate enqueue returned %s, want %s", id, ids[0])
		}
	}

	// Once the job is no longer active, the key is free again.
	exec(t, pool, "UPDATE jobs SET status = 'succeeded'")
	if _, c, err := Enqueue(ctx, d.Q(), NewJob{Kind: "sweep_blobs", DedupeKey: "manual:sweep"}); err != nil || !c {
		t.Fatalf("re-enqueue after completion: created %v, err %v", c, err)
	}
}

// TestStress runs 1000 jobs on 8 workers in two runners (two "processes"): every job ends
// terminal with the expected attempts, and exclusive jobs never overlap per connection.
func TestStress(t *testing.T) {
	d, pool := setup(t)
	const total, conns, maxAttempts = 1000, 10, 3
	connIDs := make([]uuid.UUID, conns)
	inFlight := map[uuid.UUID]*atomic.Int32{}
	for i := range connIDs {
		connIDs[i] = newConnection(t, pool, "active")
		inFlight[connIDs[i]] = &atomic.Int32{}
	}
	var calls sync.Map // job id → *atomic.Int32
	var overlaps, badAttempts atomic.Int32
	handler := func(_ context.Context, j Job) error {
		var p struct {
			Fail      int32 `json:"fail"`
			Exclusive bool  `json:"exclusive"`
		}
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return Permanent(err)
		}
		c, _ := calls.LoadOrStore(j.ID, &atomic.Int32{})
		if n := c.(*atomic.Int32).Add(1); n != j.Attempt {
			badAttempts.Add(1)
		}
		if p.Exclusive {
			f := inFlight[*j.ConnectionID]
			if f.Add(1) > 1 {
				overlaps.Add(1)
			}
			defer f.Add(-1)
		}
		time.Sleep(time.Duration(rand.N(500)) * time.Microsecond) //nolint:gosec // test jitter
		if j.Attempt <= p.Fail {
			return errors.New("synthetic failure")
		}
		return nil
	}
	for range 2 {
		r := NewRunner(d, fast(Config{Workers: 4}))
		r.Register("normalize_batch", handler)
		t.Cleanup(start(r))
	}

	type expect struct {
		attempts int32
		status   string
	}
	want := map[uuid.UUID]expect{}
	for i := range total {
		fail := int32(0)
		switch {
		case i%50 == 0:
			fail = 100 // always fails: dead after maxAttempts
		case i%7 == 0:
			fail = 2
		}
		exclusive := i%2 == 0
		cid := connIDs[i%conns]
		id := enqueue(t, d, NewJob{
			Kind: "normalize_batch", ConnectionID: &cid, Exclusive: exclusive, MaxAttempts: maxAttempts,
			Payload: map[string]any{"fail": fail, "exclusive": exclusive},
		})
		want[id] = expect{min(fail+1, maxAttempts), "succeeded"}
		if fail >= maxAttempts {
			want[id] = expect{maxAttempts, "dead"}
		}
	}

	waitFor(t, 2*time.Minute, "all jobs terminal", func() bool {
		var open int
		_ = pool.QueryRow(context.Background(), "SELECT count(*) FROM jobs WHERE status NOT IN ('succeeded', 'dead')").Scan(&open)
		return open == 0
	})
	if overlaps.Load() > 0 || badAttempts.Load() > 0 {
		t.Fatalf("%d exclusive overlaps, %d attempt mismatches", overlaps.Load(), badAttempts.Load())
	}
	for id, w := range want {
		j, rs := job(t, d, id), runs(t, d, id)
		if j.Attempts != w.attempts || j.Status != w.status || len(rs) != int(w.attempts) {
			t.Fatalf("job %s: status %s attempts %d runs %d, want %+v", id, j.Status, j.Attempts, len(rs), w)
		}
		for _, r := range rs {
			if r.Outcome == nil {
				t.Fatalf("job %s has an open run", id)
			}
		}
	}
}

// TestCrashedWorkerIsReclaimed simulates kill -9: the first runner's handler saves a
// checkpoint and then hangs without heartbeats. After the lease expires another runner
// re-claims the job and resumes from the checkpoint; the stale worker's late result is fenced.
func TestCrashedWorkerIsReclaimed(t *testing.T) {
	d, _ := setup(t)
	hung := make(chan struct{})
	started := make(chan struct{})
	crashed := NewRunner(d, fast(Config{Workers: 1, Lease: time.Second, Heartbeat: time.Hour}))
	crashed.Register("backfill_unit", func(ctx context.Context, j Job) error {
		if err := j.SaveCheckpoint(ctx, map[string]int{"page": 3}); err != nil {
			return err
		}
		close(started)
		<-hung // frozen: no heartbeat, no outcome, ignores ctx
		return nil
	})
	t.Cleanup(start(crashed))
	id := enqueue(t, d, NewJob{Kind: "backfill_unit"})
	<-started

	var resumed atomic.Value
	healthy := NewRunner(d, fast(Config{}))
	healthy.Register("backfill_unit", func(_ context.Context, j Job) error {
		resumed.Store(fmt.Sprintf("attempt %d checkpoint %s", j.Attempt, j.Checkpoint))
		return nil
	})
	t.Cleanup(start(healthy))

	waitFor(t, 10*time.Second, "re-claim", func() bool { return job(t, d, id).Status == "succeeded" })
	if got := resumed.Load(); got != `attempt 2 checkpoint {"page": 3}` {
		t.Fatalf("resumed with %v", got)
	}
	close(hung) // the stale worker finishes late
	time.Sleep(200 * time.Millisecond)
	j, rs := job(t, d, id), runs(t, d, id)
	if j.Status != "succeeded" || j.Attempts != 2 || outcomes(rs) != "[lease_expired lease_expired][succeeded ]" {
		t.Fatalf("status %s attempts %d runs %s", j.Status, j.Attempts, outcomes(rs))
	}
}

func TestHeartbeatKeepsLease(t *testing.T) {
	d, _ := setup(t)
	r := NewRunner(d, fast(Config{Lease: time.Second, Heartbeat: 200 * time.Millisecond}))
	r.Register("export", func(ctx context.Context, _ Job) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2500 * time.Millisecond):
			return nil
		}
	})
	t.Cleanup(start(r))
	id := enqueue(t, d, NewJob{Kind: "export"})
	waitFor(t, 10*time.Second, "job done", func() bool { return job(t, d, id).Status == "succeeded" })
	if j, rs := job(t, d, id), runs(t, d, id); j.Attempts != 1 || len(rs) != 1 {
		t.Fatalf("attempts %d runs %s", j.Attempts, outcomes(rs))
	}
}

type rateLimited struct{}

func (rateLimited) Error() string      { return "429 from provider" }
func (rateLimited) ErrorClass() string { return "rate_limited" }

func TestOutcomes(t *testing.T) {
	d, _ := setup(t)
	r := NewRunner(d, fast(Config{}))
	r.Register("export", func(_ context.Context, j Job) error {
		switch string(j.Payload) {
		case `"reschedule"`:
			return RescheduleAt(time.Now().Add(time.Hour), rateLimited{})
		case `"permanent"`:
			return Permanent(errors.New("reauth required"))
		case `"panic"`:
			panic("synthetic")
		}
		return errors.New("transient")
	})
	t.Cleanup(start(r))
	resched := enqueue(t, d, NewJob{Kind: "export", Payload: "reschedule"})
	perm := enqueue(t, d, NewJob{Kind: "export", Payload: "permanent"})
	panics := enqueue(t, d, NewJob{Kind: "export", Payload: "panic", MaxAttempts: 2})
	retry := enqueue(t, d, NewJob{Kind: "export", Payload: "retry", MaxAttempts: 4})

	waitFor(t, 10*time.Second, "outcomes", func() bool {
		return len(runs(t, d, resched)) == 1 && job(t, d, perm).Status == "dead" &&
			job(t, d, panics).Status == "dead" && job(t, d, retry).Status == "dead"
	})
	if j, rs := job(t, d, resched), runs(t, d, resched); j.Status != "queued" || j.Attempts != 0 ||
		time.Until(j.RunAt) < 59*time.Minute || outcomes(rs) != "[rescheduled rate_limited]" {
		t.Fatalf("reschedule: status %s attempts %d run_at in %s runs %s", j.Status, j.Attempts, time.Until(j.RunAt), outcomes(rs))
	}
	if j, rs := job(t, d, perm), runs(t, d, perm); j.Attempts != 1 || outcomes(rs) != "[failed permanent]" {
		t.Fatalf("permanent: attempts %d runs %s", j.Attempts, outcomes(rs))
	}
	if rs := runs(t, d, panics); outcomes(rs) != "[failed panic][failed panic]" {
		t.Fatalf("panic: runs %s", outcomes(rs))
	}
	if j := job(t, d, retry); j.Attempts != 4 || len(runs(t, d, retry)) != 4 {
		t.Fatalf("retry: attempts %d", j.Attempts)
	}
}

// TestSIGTERMDrain sends a real SIGTERM: the runner stops claiming and the running job
// completes within the grace period.
func TestSIGTERMDrain(t *testing.T) {
	d, _ := setup(t)
	started := make(chan struct{}, 2)
	r := NewRunner(d, fast(Config{Workers: 1, Grace: 5 * time.Second}))
	r.Register("export", func(context.Context, Job) error {
		started <- struct{}{}
		time.Sleep(300 * time.Millisecond)
		return nil
	})
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	first := enqueue(t, d, NewJob{Kind: "export"})
	<-started
	second := enqueue(t, d, NewJob{Kind: "export"})
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not drain")
	}
	if j := job(t, d, first); j.Status != "succeeded" {
		t.Fatalf("running job: %s", j.Status)
	}
	if j := job(t, d, second); j.Status != "queued" || j.Attempts != 0 {
		t.Fatalf("job claimed after SIGTERM: %s attempts %d", j.Status, j.Attempts)
	}
}

// TestShutdownReleasesAfterGrace: a cooperative handler that outlives the grace period is
// cancelled and requeued with its checkpoint; one that ignores cancellation loses its lease.
// Neither consumes an attempt.
func TestShutdownReleasesAfterGrace(t *testing.T) {
	d, _ := setup(t)
	stuck := make(chan struct{})
	var started sync.WaitGroup
	started.Add(2)
	r := NewRunner(d, fast(Config{Workers: 2, Grace: 300 * time.Millisecond}))
	r.Register("export", func(ctx context.Context, j Job) error {
		if err := j.SaveCheckpoint(ctx, map[string]int{"page": 7}); err != nil {
			return err
		}
		started.Done()
		if string(j.Payload) == `"stuck"` {
			<-stuck
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	coop := enqueue(t, d, NewJob{Kind: "export", Payload: "cooperative"})
	hung := enqueue(t, d, NewJob{Kind: "export", Payload: "stuck"})
	started.Wait()

	begin := time.Now()
	cancel()
	<-done
	if took := time.Since(begin); took > 2*time.Second {
		t.Fatalf("drain took %s", took)
	}
	for _, id := range []uuid.UUID{coop, hung} {
		j, rs := job(t, d, id), runs(t, d, id)
		if j.Status != "queued" || j.Attempts != 0 || string(j.Checkpoint) != `{"page": 7}` ||
			outcomes(rs) != "[rescheduled shutdown]" {
			t.Fatalf("%s: status %s attempts %d checkpoint %s runs %s", j.Payload, j.Status, j.Attempts, j.Checkpoint, outcomes(rs))
		}
	}
	close(stuck) // its late success must not touch the released job
	time.Sleep(200 * time.Millisecond)
	if j := job(t, d, hung); j.Status != "queued" {
		t.Fatalf("stale worker changed a released job: %s", j.Status)
	}
}

func TestScheduleService(t *testing.T) {
	d, pool := setup(t)
	ctx := context.Background()
	cid := newConnection(t, pool, "active")
	spec := ScheduleSpec{ConnectionID: cid, Stream: "measures", Interval: time.Hour}
	a, err := EnsureSchedule(ctx, d.Q(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSchedule(ctx, d.Q(), a.ID, ScheduleUpdate{Interval: 10 * time.Minute, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// Applying defaults again keeps the owner's edit; the shorter interval moved the next run
	// closer (with a minute of slack for clock skew between test and database).
	b, err := EnsureSchedule(ctx, d.Q(), spec)
	if err != nil || b.ID != a.ID || b.Interval != 10*time.Minute || time.Until(b.NextRunAt) > 11*time.Minute {
		t.Fatalf("ensure after edit: %+v, %v", b, err)
	}
	if _, err := EnsureSchedule(ctx, d.Q(), ScheduleSpec{ConnectionID: cid, Stream: "measures", Mode: ModeCorrection,
		Interval: 24 * time.Hour, Lookback: 7 * 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSchedule(ctx, d.Q(), a.ID, ScheduleUpdate{Interval: time.Second}); !errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("invalid update: %v", err)
	}
	if _, err := UpdateSchedule(ctx, d.Q(), uuid.New(), ScheduleUpdate{Interval: time.Hour}); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("missing schedule: %v", err)
	}
	all, err := ListSchedules(ctx, d.Q(), &cid)
	if err != nil || len(all) != 2 || all[0].Mode != ModeCorrection || all[1].Interval != 10*time.Minute {
		t.Fatalf("list: %+v, %v", all, err)
	}
}

// TestSchedulersOverSimulatedDay runs two schedulers over a simulated day with a one-minute
// tick. Exactly one leads on every tick, the leader "crashes" mid-day and the follower takes
// over on that same tick, a third joins later, and every slot gets exactly one job.
func TestSchedulersOverSimulatedDay(t *testing.T) {
	d, pool := setup(t)
	ctx := context.Background()
	active, paused := newConnection(t, pool, "active"), newConnection(t, pool, "paused")
	specs := []ScheduleSpec{
		{ConnectionID: active, Stream: "measures", Interval: time.Hour},
		{ConnectionID: active, Stream: "activity", Interval: 15 * time.Minute},
		{ConnectionID: active, Stream: "measures", Mode: ModeCorrection, Interval: 6 * time.Hour, Lookback: 48 * time.Hour},
		{ConnectionID: paused, Stream: "measures", Interval: time.Hour},
	}
	var scheds []Schedule
	for _, s := range specs {
		sc, err := EnsureSchedule(ctx, d.Q(), s)
		if err != nil {
			t.Fatal(err)
		}
		scheds = append(scheds, sc)
	}

	begin := time.Now()
	end := begin.Add(24 * time.Hour)
	var sim atomic.Int64
	clock := func() time.Time { return time.Unix(0, sim.Load()) }
	newSched := func() *Scheduler {
		s := NewScheduler(d, nil)
		s.now = clock
		t.Cleanup(s.resign)
		return s
	}
	alive := []*Scheduler{newSched(), newSched()}
	for step, now := 0, begin; !now.After(end); step, now = step+1, now.Add(time.Minute) {
		sim.Store(now.UnixNano())
		var crashed *Scheduler
		if step == 600 { // the leader dies: its session ends and it never ticks again
			for i, s := range alive {
				if s.lock != nil {
					crashed = s
					s.resign()
					alive = append(alive[:i], alive[i+1:]...)
					break
				}
			}
		}
		if step == 900 {
			alive = append(alive, newSched())
		}
		leaders := atomic.Int32{}
		var wg sync.WaitGroup
		for _, s := range alive {
			wg.Go(func() {
				if s.tick(ctx) {
					leaders.Add(1)
				}
			})
		}
		wg.Wait()
		if leaders.Load() != 1 {
			t.Fatalf("step %d: %d leaders (crashed: %v)", step, leaders.Load(), crashed != nil)
		}
	}

	for _, sc := range scheds {
		want := 0
		if sc.ConnectionID == active {
			want = int(end.Sub(sc.NextRunAt)/sc.Interval) + 1
		}
		var n, keys int
		var minSlot, maxSlot *time.Time
		err := pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT dedupe_key), min(run_at), max(run_at)
			FROM jobs WHERE payload->>'schedule_id' = $1`, sc.ID.String()).Scan(&n, &keys, &minSlot, &maxSlot)
		if err != nil {
			t.Fatal(err)
		}
		if n != want || keys != want {
			t.Fatalf("%s %s every %s: %d jobs (%d keys), want %d", sc.Stream, sc.Mode, sc.Interval, n, keys, want)
		}
		if want > 0 && (!minSlot.Equal(sc.NextRunAt) || !maxSlot.Equal(sc.NextRunAt.Add(time.Duration(want-1)*sc.Interval))) {
			t.Fatalf("%s %s: slots %s..%s", sc.Stream, sc.Mode, minSlot, maxSlot)
		}
	}
	var p SyncPayload
	var raw []byte
	if err := pool.QueryRow(ctx, "SELECT payload FROM jobs WHERE payload->>'mode' = 'correction' LIMIT 1").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.From == nil || p.Slot.Sub(*p.From) != 48*time.Hour {
		t.Fatalf("correction payload %s: %v", raw, err)
	}
}

// TestConcurrentMaterializeNoDuplicates bypasses leader election: even concurrent
// materializers create one job per slot.
func TestConcurrentMaterializeNoDuplicates(t *testing.T) {
	d, pool := setup(t)
	ctx := context.Background()
	cid := newConnection(t, pool, "active")
	sc, err := EnsureSchedule(ctx, d.Q(), ScheduleSpec{ConnectionID: cid, Stream: "measures", Interval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(d, nil)
	for i := range 30 {
		now := sc.NextRunAt.Add(time.Duration(i) * time.Minute)
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				if _, err := s.materialize(ctx, now); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
	}
	var n, keys int
	if err := pool.QueryRow(ctx, "SELECT count(*), count(DISTINCT dedupe_key) FROM jobs").Scan(&n, &keys); err != nil {
		t.Fatal(err)
	}
	if n != 30 || keys != 30 {
		t.Fatalf("%d jobs, %d distinct slots; want 30", n, keys)
	}
}
