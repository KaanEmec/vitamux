package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/obs"
)

// KindSync is the job kind the scheduler enqueues. The other kinds are listed in the
// jobs.kind CHECK constraint (docs/architecture/reliability.md#job-queue).
const KindSync = "sync"

// Priorities; lower runs first.
const (
	PriorityHigh   int16 = 10 // manual, user-initiated work
	PriorityNormal int16 = 100
	PriorityLow    int16 = 200 // correction syncs, maintenance
)

const (
	defaultMaxAttempts = 5
	notifyChannel      = "vitamux_jobs"
	maxErrorMessage    = 500
)

// NewJob describes a job to enqueue.
type NewJob struct {
	Kind         string
	ConnectionID *uuid.UUID
	Exclusive    bool      // at most one exclusive job runs per connection; needs ConnectionID
	Priority     int16     // 0 means PriorityNormal
	RunAt        time.Time // zero means now
	DedupeKey    string    // while a job with this key is queued or running, Enqueue returns it
	Payload      any       // marshalled to JSON; never secrets, health values or bodies
	MaxAttempts  int32     // 0 means 5
}

// Enqueue inserts j and wakes the workers. Pass the Queries of a transaction to enqueue
// atomically with other writes (the wake-up is then sent on commit). created is false when
// an active job with the same dedupe key exists; its id is returned instead.
func Enqueue(ctx context.Context, q *dbq.Queries, j NewJob) (id uuid.UUID, created bool, err error) {
	payload := []byte("{}")
	if j.Payload != nil {
		if payload, err = json.Marshal(j.Payload); err != nil {
			return uuid.Nil, false, fmt.Errorf("job payload: %w", err)
		}
	}
	p := dbq.InsertJobParams{
		ID: uuid.New(), Kind: j.Kind, ConnectionID: j.ConnectionID, Exclusive: j.Exclusive,
		Priority: j.Priority, Payload: payload, MaxAttempts: j.MaxAttempts,
	}
	if p.Priority == 0 {
		p.Priority = PriorityNormal
	}
	if p.MaxAttempts == 0 {
		p.MaxAttempts = defaultMaxAttempts
	}
	if !j.RunAt.IsZero() {
		p.RunAt = &j.RunAt
	}
	if j.DedupeKey != "" {
		p.DedupeKey = &j.DedupeKey
	}
	// The active duplicate can finish between the insert and the lookup; then insert again.
	for range 3 {
		id, err = q.InsertJob(ctx, p)
		if err = db.MapErr(err); err == nil {
			return id, true, q.NotifyJobs(ctx, notifyChannel)
		}
		if !errors.Is(err, db.ErrNotFound) { // DO NOTHING returns no row on a duplicate
			return uuid.Nil, false, err
		}
		id, err = q.GetActiveJobID(ctx, p.DedupeKey)
		if err = db.MapErr(err); !errors.Is(err, db.ErrNotFound) {
			return id, false, err
		}
	}
	return uuid.Nil, false, fmt.Errorf("enqueue %s: dedupe key keeps changing state", j.Kind)
}

// Job is one claimed execution handed to a Handler.
type Job struct {
	ID           uuid.UUID
	Kind         string
	ConnectionID *uuid.UUID
	Payload      json.RawMessage
	Checkpoint   json.RawMessage // last saved progress; nil on a fresh job
	Attempt      int32           // 1 on the first run; reschedules do not count
	MaxAttempts  int32

	lease *lease
}

// lease fences every write of a running job: owner and attempt must still match.
type lease struct {
	r     *Runner
	owner string
	runID int64
}

// SaveCheckpoint stores v (JSON) as the job's progress and extends its lease. A retry or a
// re-claim after a crash gets it back in Job.Checkpoint. It returns ErrLeaseLost when the job
// was reaped meanwhile; the handler should then stop.
func (j Job) SaveCheckpoint(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	}
	n, err := j.lease.r.db.Q().SaveJobCheckpoint(ctx, dbq.SaveJobCheckpointParams{
		Checkpoint: b, Lease: j.lease.r.cfg.Lease, ID: j.ID, Owner: j.lease.owner, Attempt: j.Attempt,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLeaseLost
	}
	return nil
}

// ErrLeaseLost means another worker may now own the job; the current run must stop.
var ErrLeaseLost = errors.New("jobs: lease lost")

// A handler error with an ErrorClass method sets job_runs.error_class (e.g. "transient",
// "schema_drift"); others are recorded as "error".
type classifier interface{ ErrorClass() string }

// Permanent marks err as not retryable: the job goes straight to dead.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err}
}

type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// RescheduleAt requeues the job to run at (or after) at without consuming an attempt, e.g.
// for a provider's Retry-After. cause, if any, is recorded on the run.
func RescheduleAt(at time.Time, cause error) error { return &rescheduleError{at, cause} }

type rescheduleError struct {
	at    time.Time
	cause error
}

func (e *rescheduleError) Error() string {
	if e.cause == nil {
		return "rescheduled"
	}
	return "rescheduled: " + e.cause.Error()
}
func (e *rescheduleError) Unwrap() error { return e.cause }

func errorClass(err error) string {
	var c classifier
	var p *permanentError
	switch {
	case errors.As(err, &c):
		return c.ErrorClass()
	case errors.As(err, &p):
		return "permanent"
	}
	return "error"
}

// errorMessage is what job_runs keeps: URL credentials and tokens masked, bounded length.
func errorMessage(err error) string {
	s := obs.RedactString(err.Error())
	if len(s) > maxErrorMessage {
		s = strings.ToValidUTF8(s[:maxErrorMessage], "")
	}
	return s
}

// RetryDelay is min(max, base·2^attempt)·U(0.5,1) (reliability.md#job-queue).
func RetryDelay(attempt int32, base, maxDelay time.Duration) time.Duration {
	d := base
	for i := int32(0); i < attempt && d < maxDelay; i++ {
		d *= 2
	}
	d = min(d, maxDelay)
	return d/2 + rand.N(d/2+1) //nolint:gosec // jitter, not security
}
