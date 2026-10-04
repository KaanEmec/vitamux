package connectors

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

// KindBackfillUnit is the job that fetches one backfill unit; register HandleBackfillUnit for it.
const KindBackfillUnit = "backfill_unit"

const (
	maxBackfillUnits = 10000
	minUnitSize      = time.Minute
)

// ErrInvalidBackfill wraps validation failures of a backfill request.
var ErrInvalidBackfill = errors.New("connectors: invalid backfill")

// errUnitDone aborts a commit for a unit that another run already finished.
var errUnitDone = errors.New("backfill unit already done")

// BackfillSpec asks for a historical import of [From, To) of one stream of a connection.
type BackfillSpec struct {
	ConnectionID uuid.UUID
	Stream       string
	From, To     time.Time     // To zero = now
	UnitSize     time.Duration // 0 = the stream's StreamSpec.UnitSize
	DailyLimit   int           // > 0 = a paced backfill: at most this many units start a UTC day
}

// Backfill is a stored backfill and how many of its units are in each state.
type Backfill struct {
	ID, ConnectionID               uuid.UUID
	Stream                         string
	From, To                       time.Time
	DailyLimit                     *int   // units a UTC day for a paced backfill
	Status                         string // running | done | failed | cancelled
	CreatedAt                      time.Time
	FinishedAt                     *time.Time
	Pending, Running, Done, Failed int
}

// BackfillUnit is one unit's state. Running means started and not finished, which includes
// a unit interrupted by a crash and waiting for its job to be retried.
type BackfillUnit struct {
	From, To   time.Time
	Status     string // pending | running | done | failed
	Attempts   int
	ErrorClass string // class of the last failure, if any
	UpdatedAt  time.Time
}

type backfillPayload struct {
	BackfillID uuid.UUID `json:"backfill_id"`
	Start      time.Time `json:"start"`
}

// CreateBackfill splits spec's range into units and queues one job per unit. The jobs are
// exclusive per connection and low priority, so incremental syncs run between them. A
// backfill never moves the stream cursor. A paced backfill (DailyLimit) queues the same jobs, but
// each waits for the next UTC day once the day's limit is spent.
func (rt *Runtime) CreateBackfill(ctx context.Context, spec BackfillSpec) (uuid.UUID, error) {
	row, err := rt.db.Q().GetSyncConnection(ctx, spec.ConnectionID)
	if err = db.MapErr(err); err != nil {
		return uuid.Nil, err
	}
	if row.Status != "active" && row.Status != "degraded" {
		return uuid.Nil, fmt.Errorf("%w: connection is %s", ErrInvalidBackfill, row.Status)
	}
	c, ok := rt.reg.Get(row.Provider)
	if !ok {
		return uuid.Nil, fmt.Errorf("%w: no connector registered for %s", ErrInvalidBackfill, row.Provider)
	}
	d := c.Describe()
	s, ok := d.stream(spec.Stream)
	if !ok || !d.Capabilities.Backfill {
		return uuid.Nil, fmt.Errorf("%w: %s cannot backfill stream %q", ErrInvalidBackfill, row.Provider, spec.Stream)
	}
	size := cmp.Or(spec.UnitSize, s.UnitSize)
	to := spec.To
	if to.IsZero() {
		to = time.Now()
	}
	from, to := spec.From.Truncate(time.Microsecond), to.Truncate(time.Microsecond) // PostgreSQL precision
	switch {
	case size < minUnitSize:
		return uuid.Nil, fmt.Errorf("%w: unit size must be at least %s", ErrInvalidBackfill, minUnitSize)
	case spec.DailyLimit < 0:
		return uuid.Nil, fmt.Errorf("%w: daily limit must be positive", ErrInvalidBackfill)
	case !to.After(from):
		return uuid.Nil, fmt.Errorf("%w: range end must be after its start", ErrInvalidBackfill)
	case from.Before(time.Now().Add(-s.MaxBackfill)):
		return uuid.Nil, fmt.Errorf("%w: %s reaches back at most %s", ErrInvalidBackfill, spec.Stream, s.MaxBackfill)
	case (to.Sub(from)+size-1)/size > maxBackfillUnits:
		return uuid.Nil, fmt.Errorf("%w: more than %d units", ErrInvalidBackfill, maxBackfillUnits)
	}
	var starts, ends []time.Time
	for t := from; t.Before(to); t = t.Add(size) {
		end := t.Add(size)
		if end.After(to) {
			end = to
		}
		starts, ends = append(starts, t), append(ends, end)
	}
	id := uuid.New()
	var limit *int32
	if spec.DailyLimit > 0 {
		limit = new(int32(spec.DailyLimit))
	}
	err = rt.db.Tx(ctx, func(q *dbq.Queries) error {
		if err := q.InsertBackfill(ctx, dbq.InsertBackfillParams{
			ID: id, ConnectionID: row.ID, Stream: spec.Stream, RangeStart: from, RangeEnd: to, DailyLimit: limit,
		}); err != nil {
			return err
		}
		if err := q.InsertBackfillUnits(ctx, dbq.InsertBackfillUnitsParams{BackfillID: id, Starts: starts, Ends: ends}); err != nil {
			return err
		}
		for _, start := range starts {
			if err := enqueueUnit(ctx, q, row.ID, id, start); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

// enqueueUnit queues the unit's job unless it has an active one.
func enqueueUnit(ctx context.Context, q *dbq.Queries, connectionID, backfillID uuid.UUID, start time.Time) error {
	_, _, err := jobs.Enqueue(ctx, q, jobs.NewJob{
		Kind: KindBackfillUnit, ConnectionID: &connectionID, Exclusive: true, Priority: jobs.PriorityLow,
		DedupeKey: fmt.Sprintf("%s:%s:%d", KindBackfillUnit, backfillID, start.UnixMicro()),
		Payload:   backfillPayload{BackfillID: backfillID, Start: start},
	})
	return err
}

// HandleBackfillUnit is the KindBackfillUnit handler. A unit finishes exactly once: its last
// page commits together with the done mark, and a done unit is skipped. Pages before the last
// resume from the job checkpoint. A unit fails on a permanent error or its last attempt.
func (rt *Runtime) HandleBackfillUnit(ctx context.Context, j jobs.Job) error {
	var p backfillPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil || j.ConnectionID == nil {
		return jobs.Permanent(errors.New("backfill unit job: invalid payload or no connection"))
	}
	u, err := rt.db.Q().GetBackfillUnit(ctx, dbq.GetBackfillUnitParams{BackfillID: p.BackfillID, RangeStart: p.Start})
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return jobs.Permanent(err)
	} else if err != nil {
		return err
	}
	if u.Status == "done" || u.BackfillStatus == "cancelled" {
		return nil
	}
	r, err := rt.prepare(ctx, u.ConnectionID, u.Stream)
	switch {
	case err != nil && r.c != nil: // an unreachable sidecar: retry the unit later
		return rt.settle(ctx, r, u.Stream, err)
	case errors.As(err, new(*classError)): // the connection cannot sync: fail the unit now
		return cmp.Or(rt.endUnit(ctx, p, errorClassOf(err)), err)
	case err != nil:
		return err
	}
	if until, err := rt.blockedUntil(ctx, r); err != nil {
		return err
	} else if !until.IsZero() {
		return jobs.RescheduleAt(until, &RateLimitedError{RetryAfter: time.Until(until)})
	}
	if next, err := rt.pacedUntil(ctx, u, p); err != nil || !next.IsZero() {
		return cmp.Or(err, jobs.RescheduleAt(next, nil))
	}
	if err := rt.db.Q().StartBackfillUnit(ctx, dbq.StartBackfillUnitParams{BackfillID: p.BackfillID, RangeStart: p.Start}); err != nil {
		return err
	}
	done := func(q *dbq.Queries) error {
		n, err := q.EndBackfillUnit(ctx, dbq.EndBackfillUnitParams{Status: "done", BackfillID: p.BackfillID, RangeStart: p.Start})
		if err != nil {
			return err
		}
		if n == 0 {
			return errUnitDone
		}
		return q.FinishBackfill(ctx, p.BackfillID)
	}
	req := PlanRequest{Mode: ModeBackfill, Stream: u.Stream, From: u.RangeStart, To: u.RangeEnd}
	resume, err := rt.run(ctx, j, r, req, done)
	switch {
	case errors.Is(err, errUnitDone):
		return nil
	case err == nil && !resume.IsZero():
		return jobs.RescheduleAt(resume, nil)
	}
	out := rt.settle(ctx, r, u.Stream, err)
	if err != nil && unitFails(ctx, j, err) {
		if ferr := rt.endUnit(ctx, p, errorClassOf(err)); ferr != nil {
			return ferr
		}
	}
	return out
}

// pacedUntil returns the start of the next UTC day when the unit's backfill is paced and the
// stream has already started its daily limit of units today, else the zero time. The count spans
// the connection's backfills of the stream, so two of them share one limit.
func (rt *Runtime) pacedUntil(ctx context.Context, u dbq.GetBackfillUnitRow, p backfillPayload) (time.Time, error) {
	if u.DailyLimit == nil {
		return time.Time{}, nil
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	n, err := rt.db.Q().CountUnitsStartedSince(ctx, dbq.CountUnitsStartedSinceParams{
		ConnectionID: u.ConnectionID, Stream: u.Stream, Since: today, BackfillID: p.BackfillID, RangeStart: p.Start,
	})
	if err != nil || n < int64(*u.DailyLimit) {
		return time.Time{}, err
	}
	return today.AddDate(0, 0, 1), nil
}

// unitFails reports whether err ends the unit's job for good.
func unitFails(ctx context.Context, j jobs.Job, err error) bool {
	var rl *RateLimitedError
	var drift *SchemaDriftError
	switch {
	case ctx.Err() != nil || errors.Is(err, jobs.ErrLeaseLost) || errors.As(err, &rl):
		return false
	case errors.Is(err, ErrReauthRequired) || errors.As(err, &drift) || errors.Is(err, ErrPermanent):
		return true
	}
	return j.Attempt >= j.MaxAttempts
}

func errorClassOf(err error) string {
	if c, ok := errors.AsType[interface {
		error
		ErrorClass() string
	}](err); ok {
		return c.ErrorClass()
	}
	return ClassTransient
}

// endUnit marks the unit failed and finishes the backfill if it was the last open unit.
func (rt *Runtime) endUnit(ctx context.Context, p backfillPayload, class string) error {
	return rt.db.Tx(ctx, func(q *dbq.Queries) error {
		if _, err := q.EndBackfillUnit(ctx, dbq.EndBackfillUnitParams{
			Status: "failed", ErrorClass: &class, BackfillID: p.BackfillID, RangeStart: p.Start,
		}); err != nil {
			return err
		}
		return q.FinishBackfill(ctx, p.BackfillID)
	})
}

// RetryBackfill requeues the failed units of a running or failed backfill, or only the unit
// starting at unit, and re-enqueues units left unfinished. It returns how many units are queued.
func (rt *Runtime) RetryBackfill(ctx context.Context, id uuid.UUID, unit *time.Time) (int, error) {
	var n int
	err := rt.db.Tx(ctx, func(q *dbq.Queries) error {
		rows, err := q.ReopenBackfillUnits(ctx, dbq.ReopenBackfillUnitsParams{BackfillID: id, RangeStart: unit})
		if err != nil || len(rows) == 0 {
			return err
		}
		if err := q.ReopenBackfill(ctx, id); err != nil {
			return err
		}
		for _, row := range rows {
			if err := enqueueUnit(ctx, q, row.ConnectionID, id, row.RangeStart); err != nil {
				return err
			}
		}
		n = len(rows)
		return nil
	})
	return n, err
}

// CancelBackfill stops a running or failed backfill: queued unit jobs are cancelled and a
// running one stops after its current unit. db.ErrNotFound means there is no such backfill
// left to cancel. Fetched data is kept.
func (rt *Runtime) CancelBackfill(ctx context.Context, id uuid.UUID) error {
	return rt.db.Tx(ctx, func(q *dbq.Queries) error {
		connID, err := q.CancelBackfill(ctx, id)
		if err != nil {
			return err
		}
		return q.CancelBackfillJobs(ctx, dbq.CancelBackfillJobsParams{ConnectionID: &connID, BackfillID: id.String()})
	})
}

// ListBackfills returns a connection's backfills, newest first.
func (rt *Runtime) ListBackfills(ctx context.Context, connectionID uuid.UUID) ([]Backfill, error) {
	rows, err := rt.db.Q().ListBackfills(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	out := make([]Backfill, len(rows))
	for i, r := range rows {
		out[i] = Backfill{
			ID: r.ID, ConnectionID: r.ConnectionID, Stream: r.Stream, From: r.RangeStart, To: r.RangeEnd,
			Status: r.Status, CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt,
			Pending: int(r.Pending), Running: int(r.Running), Done: int(r.Done), Failed: int(r.Failed),
		}
		if r.DailyLimit != nil {
			out[i].DailyLimit = new(int(*r.DailyLimit))
		}
	}
	return out, nil
}

// BackfillUnits returns a backfill's units in range order, only those with status unless it is "".
func (rt *Runtime) BackfillUnits(ctx context.Context, id uuid.UUID, status string) ([]BackfillUnit, error) {
	p := dbq.ListBackfillUnitsParams{BackfillID: id}
	if status != "" {
		p.Status = &status
	}
	rows, err := rt.db.Q().ListBackfillUnits(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]BackfillUnit, len(rows))
	for i, r := range rows {
		out[i] = BackfillUnit{From: r.RangeStart, To: r.RangeEnd, Status: r.Status, Attempts: int(r.Attempts), UpdatedAt: r.UpdatedAt}
		if r.LastErrorClass != nil {
			out[i].ErrorClass = *r.LastErrorClass
		}
	}
	return out, nil
}
