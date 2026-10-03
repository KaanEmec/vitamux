package jobs

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/metrics"
)

// Handler runs one job. Return nil on success, RescheduleAt to run later without using an
// attempt, Permanent to stop retrying, or any other error to retry with backoff. ctx ends when
// the lease is lost or the shutdown grace period is over; save progress with SaveCheckpoint.
type Handler func(ctx context.Context, j Job) error

// Config tunes a Runner. Zero values take the defaults from reliability.md#job-queue.
type Config struct {
	Log       *slog.Logger
	Owner     string        // lease owner; default host:pid:random
	Workers   int           // concurrent handlers; default 4
	Lease     time.Duration // default 2m
	Heartbeat time.Duration // lease extension interval; default 30s
	Poll      time.Duration // fallback when no NOTIFY arrives; default 5s
	ReapEvery time.Duration // default 30s
	RetryBase time.Duration // default 30s
	RetryMax  time.Duration // default 30m
	Grace     time.Duration // how long a shutdown waits for running handlers; default 50s
}

// Runner claims and executes jobs with registered handlers, heartbeats their leases, and
// reaps expired leases of crashed processes.
type Runner struct {
	db       *db.DB
	cfg      Config
	log      *slog.Logger
	handlers map[string]Handler
	wake     broadcast
	lastReap atomic.Int64 // unix nanos of the last successful reaper pass
}

// NewRunner returns a Runner; register handlers before calling Run.
func NewRunner(d *db.DB, cfg Config) *Runner {
	cfg.Owner = cmp.Or(cfg.Owner, defaultOwner())
	cfg.Workers = cmp.Or(cfg.Workers, 4)
	cfg.Lease = cmp.Or(cfg.Lease, 2*time.Minute)
	cfg.Heartbeat = cmp.Or(cfg.Heartbeat, 30*time.Second)
	cfg.Poll = cmp.Or(cfg.Poll, 5*time.Second)
	cfg.ReapEvery = cmp.Or(cfg.ReapEvery, 30*time.Second)
	cfg.RetryBase = cmp.Or(cfg.RetryBase, 30*time.Second)
	cfg.RetryMax = cmp.Or(cfg.RetryMax, 30*time.Minute)
	cfg.Grace = cmp.Or(cfg.Grace, 50*time.Second)
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Runner{db: d, cfg: cfg, log: log, handlers: map[string]Handler{}}
}

// Register sets the handler for kind. It panics on a duplicate, and must happen before Run.
func (r *Runner) Register(kind string, h Handler) {
	if _, dup := r.handlers[kind]; dup {
		panic("jobs: duplicate handler for " + kind)
	}
	r.handlers[kind] = h
}

// Ready is a readiness check: the reaper reached the database within the last 60 s.
func (r *Runner) Ready(context.Context) error {
	last := r.lastReap.Load()
	if last == 0 || time.Since(time.Unix(0, last)) > 60*time.Second {
		return errors.New("job workers are not running")
	}
	return nil
}

// Run works until ctx ends, then drains: it stops claiming, lets running handlers finish
// within the grace period, cancels the rest and releases their leases without counting
// the attempt.
func (r *Runner) Run(ctx context.Context) {
	kinds := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		kinds = append(kinds, k)
	}
	handlerCtx, cancelHandlers := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelHandlers()

	var bg, workers sync.WaitGroup
	bg.Go(func() { r.reapLoop(ctx) })
	if len(kinds) > 0 {
		bg.Go(func() { r.listen(ctx) })
		for range r.cfg.Workers {
			workers.Go(func() { r.work(ctx, handlerCtx, kinds) })
		}
	}
	<-ctx.Done()

	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(r.cfg.Grace):
		r.log.Warn("shutdown grace period over; cancelling running jobs")
		cancelHandlers()
		select {
		case <-done:
		case <-time.After(min(r.cfg.Grace, 5*time.Second)):
		}
	}
	// Handlers that ignored cancellation lose their lease here; their late writes are fenced off.
	r.releaseAll()
	bg.Wait()
}

func (r *Runner) work(stop, handlerCtx context.Context, kinds []string) {
	for stop.Err() == nil {
		wake := r.wake.wait() // before claiming, so a NOTIFY during the claim is not missed
		if j, h, ok := r.claim(stop, kinds); ok {
			r.execute(handlerCtx, j, h)
			continue
		}
		select {
		case <-stop.Done():
		case <-wake:
		case <-time.After(r.cfg.Poll):
		}
	}
}

func (r *Runner) claim(ctx context.Context, kinds []string) (Job, Handler, bool) {
	// A racing claim of another exclusive job for the same connection hits the unique index;
	// try again, the NOT EXISTS filter then skips that connection.
	for range 3 {
		var j Job
		err := r.db.Tx(ctx, func(q *dbq.Queries) error {
			row, err := q.ClaimJob(ctx, dbq.ClaimJobParams{Owner: r.cfg.Owner, Lease: r.cfg.Lease, Kinds: kinds})
			if err != nil {
				return err
			}
			runID, err := q.InsertJobRun(ctx, dbq.InsertJobRunParams{JobID: row.ID, Attempt: row.Attempts})
			j = Job{
				ID: row.ID, Kind: row.Kind, ConnectionID: row.ConnectionID, Payload: row.Payload,
				Checkpoint: row.Checkpoint, Attempt: row.Attempts, MaxAttempts: row.MaxAttempts,
				lease: &lease{r: r, owner: r.cfg.Owner, runID: runID},
			}
			return err
		})
		if err == nil {
			return j, r.handlers[j.Kind], true
		}
		if !errors.Is(err, db.ErrConflict) {
			if !errors.Is(err, db.ErrNotFound) && ctx.Err() == nil {
				r.log.Error("claim job", "err", err)
			}
			break
		}
	}
	return Job{}, nil, false
}

func (r *Runner) execute(handlerCtx context.Context, j Job, h Handler) {
	ctx, cancel := context.WithCancel(handlerCtx)
	var lost atomic.Bool
	beats := make(chan struct{})
	go func() {
		defer close(beats)
		r.heartbeat(ctx, j, &lost, cancel)
	}()
	began := time.Now()
	err := call(ctx, h, j)
	cancel()
	<-beats
	metrics.JobDuration.WithLabelValues(j.Kind).Observe(time.Since(began).Seconds())

	log := r.log.With("job_id", j.ID, "kind", j.Kind, "attempt", j.Attempt)
	if j.ConnectionID != nil {
		log = log.With("connection_id", *j.ConnectionID)
	}
	if lost.Load() {
		log.Warn("job lease lost; another worker may run it")
		return
	}
	fctx, fcancel := context.WithTimeout(context.WithoutCancel(handlerCtx), 10*time.Second)
	defer fcancel()
	if ferr := r.finish(fctx, j, err, handlerCtx.Err() != nil, log); ferr != nil {
		log.Error("record job outcome", "err", ferr)
	}
	r.wake.signal() // a finished exclusive job may unblock the next one for its connection
}

func (r *Runner) heartbeat(ctx context.Context, j Job, lost *atomic.Bool, cancel context.CancelFunc) {
	t := time.NewTicker(r.cfg.Heartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n, err := r.db.Q().HeartbeatJob(ctx, dbq.HeartbeatJobParams{
			Lease: r.cfg.Lease, ID: j.ID, Owner: j.lease.owner, Attempt: j.Attempt,
		})
		switch {
		case err != nil && ctx.Err() == nil:
			r.log.Warn("job heartbeat", "job_id", j.ID, "err", err) // the lease may expire; the reaper then requeues
		case err == nil && n == 0:
			lost.Store(true)
			cancel()
			return
		}
	}
}

func call(ctx context.Context, h Handler, j Job) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = panicError{stack: debug.Stack()}
		}
	}()
	return h(ctx, j)
}

type panicError struct{ stack []byte }

func (panicError) Error() string      { return "handler panicked" }
func (panicError) ErrorClass() string { return "panic" }

// finish records the outcome of a run. Every job update is fenced; zero rows means the lease
// was reaped meanwhile and nothing is written.
func (r *Runner) finish(ctx context.Context, j Job, herr error, shuttingDown bool, log *slog.Logger) error {
	fence := func(n int64, err error) error {
		if err == nil && n == 0 {
			return ErrLeaseLost
		}
		return err
	}
	run := dbq.FinishJobRunParams{ID: j.lease.runID}
	var resched *rescheduleError
	var perm *permanentError
	err := r.db.Tx(ctx, func(q *dbq.Queries) error {
		var err error
		switch {
		case herr == nil:
			run.Outcome = "succeeded"
			err = fence(q.CompleteJob(ctx, dbq.CompleteJobParams{ID: j.ID, Owner: j.lease.owner, Attempt: j.Attempt}))
		case shuttingDown:
			run.Outcome, run.ErrorClass = "rescheduled", ptr("shutdown")
			err = fence(q.RequeueJob(ctx, dbq.RequeueJobParams{ID: j.ID, Owner: j.lease.owner, Attempt: j.Attempt}))
		case errors.As(herr, &resched):
			run.Outcome = "rescheduled"
			if resched.cause != nil {
				run.ErrorClass, run.ErrorMessage = ptr(errorClass(resched.cause)), ptr(errorMessage(resched.cause))
			}
			err = fence(q.RequeueJob(ctx, dbq.RequeueJobParams{RunAt: &resched.at, ID: j.ID, Owner: j.lease.owner, Attempt: j.Attempt}))
		case errors.As(herr, &perm) || j.Attempt >= j.MaxAttempts:
			run.Outcome, run.ErrorClass, run.ErrorMessage = "failed", ptr(errorClass(herr)), ptr(errorMessage(herr))
			err = fence(q.KillJob(ctx, dbq.KillJobParams{ID: j.ID, Owner: j.lease.owner, Attempt: j.Attempt}))
		default:
			run.Outcome, run.ErrorClass, run.ErrorMessage = "failed", ptr(errorClass(herr)), ptr(errorMessage(herr))
			err = fence(q.RetryJob(ctx, dbq.RetryJobParams{
				Delay: RetryDelay(j.Attempt, r.cfg.RetryBase, r.cfg.RetryMax), ID: j.ID, Owner: j.lease.owner, Attempt: j.Attempt,
			}))
		}
		if err != nil {
			return err
		}
		return q.FinishJobRun(ctx, run)
	})
	if errors.Is(err, ErrLeaseLost) {
		log.Warn("job lease lost before its outcome was recorded")
		return nil
	}
	if err != nil {
		return err
	}
	outcome := run.Outcome
	if outcome == "failed" && (perm != nil || j.Attempt >= j.MaxAttempts) {
		outcome = "dead" // no retry left
	}
	metrics.JobRuns.WithLabelValues(j.Kind, outcome).Inc()
	var p panicError
	switch {
	case herr == nil:
		log.Debug("job succeeded")
	case errors.As(herr, &p):
		log.Error("job handler panicked", "stack", string(p.stack))
	case run.Outcome == "failed":
		log.Warn("job failed", "error_class", *run.ErrorClass, "err", *run.ErrorMessage,
			"dead", perm != nil || j.Attempt >= j.MaxAttempts)
	default:
		log.Info("job rescheduled", "error_class", deref(run.ErrorClass))
	}
	return nil
}

func (r *Runner) reapLoop(ctx context.Context) {
	t := time.NewTicker(r.cfg.ReapEvery)
	defer t.Stop()
	for {
		r.reap(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// reap requeues jobs whose lease expired (the holder crashed or hung). The expired attempt
// stays counted, and a job out of attempts becomes dead.
func (r *Runner) reap(ctx context.Context) {
	var rows []dbq.ReapExpiredJobsRow
	err := r.db.Tx(ctx, func(q *dbq.Queries) error {
		var err error
		if rows, err = q.ReapExpiredJobs(ctx); err != nil || len(rows) == 0 {
			return err
		}
		ids := make([]uuid.UUID, len(rows))
		for i, row := range rows {
			ids[i] = row.ID
		}
		return q.CloseOpenJobRuns(ctx, dbq.CloseOpenJobRunsParams{Outcome: "lease_expired", ErrorClass: "lease_expired", JobIds: ids})
	})
	if err != nil {
		if ctx.Err() == nil {
			r.log.Error("reap expired jobs", "err", err)
		}
		return
	}
	r.lastReap.Store(time.Now().UnixNano())
	for _, row := range rows {
		r.log.Warn("job lease expired", "job_id", row.ID, "kind", row.Kind, "attempt", row.Attempts, "status", row.Status)
	}
	if len(rows) > 0 {
		r.wake.signal()
	}
}

// releaseAll gives back every lease this runner still holds, without counting the attempt.
func (r *Runner) releaseAll() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := r.db.Tx(ctx, func(q *dbq.Queries) error {
		ids, err := q.ReleaseOwnerJobs(ctx, r.cfg.Owner)
		if err != nil || len(ids) == 0 {
			return err
		}
		r.log.Warn("released job leases at shutdown", "jobs", len(ids))
		return q.CloseOpenJobRuns(ctx, dbq.CloseOpenJobRunsParams{Outcome: "rescheduled", ErrorClass: "shutdown", JobIds: ids})
	})
	if err != nil {
		r.log.Error("release job leases", "err", err) // they expire and the reaper requeues them
	}
}

// listen turns NOTIFY into wake-ups. Polling covers the gaps while it reconnects.
func (r *Runner) listen(ctx context.Context) {
	for ctx.Err() == nil {
		err := r.db.Listen(ctx, notifyChannel, func(string) { r.wake.signal() })
		if ctx.Err() != nil {
			return
		}
		r.log.Warn("job listener stopped; polling until it reconnects", "err", err)
		select {
		case <-ctx.Done():
		case <-time.After(r.cfg.Poll):
		}
	}
}

// broadcast wakes every waiting worker at once.
type broadcast struct {
	mu sync.Mutex
	ch chan struct{}
}

func (b *broadcast) wait() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ch == nil {
		b.ch = make(chan struct{})
	}
	return b.ch
}

func (b *broadcast) signal() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ch != nil {
		close(b.ch)
		b.ch = nil
	}
}

func defaultOwner() string {
	host, _ := os.Hostname()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s:%d:%s", host, os.Getpid(), hex.EncodeToString(b))
}

func ptr[T any](v T) *T { return &v }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
