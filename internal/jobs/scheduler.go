package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Schedule modes.
const (
	ModeIncremental = "incremental" // follow the stream cursor
	ModeCorrection  = "correction"  // re-fetch the lookback window ending at the slot
)

const (
	schedulerLockKey = 0x766d785f736368 // "vmx_sch"
	schedulerTick    = 15 * time.Second
	minInterval      = time.Minute
	dueBatch         = 100
)

// ErrInvalidSchedule wraps validation failures of a schedule spec or update.
var ErrInvalidSchedule = errors.New("jobs: invalid schedule")

// ScheduleSpec is one periodic sync of a connection's stream.
type ScheduleSpec struct {
	ConnectionID uuid.UUID
	Stream       string
	Mode         string        // ModeIncremental (default) or ModeCorrection
	Interval     time.Duration // at least one minute
	Lookback     time.Duration // window a correction run re-fetches; required for ModeCorrection
}

// Schedule is a stored schedule.
type Schedule struct {
	ID uuid.UUID
	ScheduleSpec
	NextRunAt time.Time
	Enabled   bool
}

// ScheduleUpdate is what the owner may change.
type ScheduleUpdate struct {
	Interval time.Duration
	Lookback time.Duration
	Enabled  bool
}

// SyncPayload is the payload of the sync jobs the scheduler enqueues.
type SyncPayload struct {
	ScheduleID uuid.UUID  `json:"schedule_id"`
	Stream     string     `json:"stream"`
	Mode       string     `json:"mode"`
	Slot       time.Time  `json:"slot"`
	From       *time.Time `json:"from,omitempty"` // correction: re-fetch [From, Slot)
}

// EnsureSchedule creates the schedule for spec's connection, stream and mode unless it exists,
// and returns the stored one. An existing schedule keeps its (possibly owner-edited) settings,
// so connector defaults can be applied on every connect. The first run is one interval away.
func EnsureSchedule(ctx context.Context, q *dbq.Queries, spec ScheduleSpec) (Schedule, error) {
	if spec.Mode == "" {
		spec.Mode = ModeIncremental
	}
	if err := validate(spec.Mode, spec.Stream, spec.Interval, spec.Lookback); err != nil {
		return Schedule{}, err
	}
	row, err := q.InsertSchedule(ctx, dbq.InsertScheduleParams{
		ID: uuid.New(), ConnectionID: spec.ConnectionID, Stream: spec.Stream, Mode: spec.Mode,
		RunInterval: spec.Interval, Lookback: spec.Lookback,
	})
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) { // already there
		row, err = q.GetScheduleByStream(ctx, dbq.GetScheduleByStreamParams{
			ConnectionID: spec.ConnectionID, Stream: spec.Stream, Mode: spec.Mode,
		})
		err = db.MapErr(err)
	}
	return fromRow(row), err
}

// UpdateSchedule changes a schedule's interval, lookback and enabled flag. A shorter interval
// brings the next run closer; it never moves further out. ErrNotFound if id does not exist.
func UpdateSchedule(ctx context.Context, q *dbq.Queries, id uuid.UUID, u ScheduleUpdate) (Schedule, error) {
	cur, err := q.GetSchedule(ctx, id)
	if err = db.MapErr(err); err != nil {
		return Schedule{}, err
	}
	if err := validate(cur.Mode, cur.Stream, u.Interval, u.Lookback); err != nil {
		return Schedule{}, err
	}
	row, err := q.UpdateSchedule(ctx, dbq.UpdateScheduleParams{
		RunInterval: u.Interval, Lookback: u.Lookback, Enabled: u.Enabled, ID: id,
	})
	return fromRow(row), db.MapErr(err)
}

// ListSchedules returns all schedules, or those of one connection.
func ListSchedules(ctx context.Context, q *dbq.Queries, connectionID *uuid.UUID) ([]Schedule, error) {
	rows, err := q.ListSchedules(ctx, connectionID)
	out := make([]Schedule, len(rows))
	for i, row := range rows {
		out[i] = fromRow(row)
	}
	return out, db.MapErr(err)
}

func validate(mode, stream string, interval, lookback time.Duration) error {
	switch {
	case mode != ModeIncremental && mode != ModeCorrection:
		return fmt.Errorf("%w: unknown mode %q", ErrInvalidSchedule, mode)
	case stream == "":
		return fmt.Errorf("%w: stream is required", ErrInvalidSchedule)
	case interval < minInterval:
		return fmt.Errorf("%w: interval must be at least %s", ErrInvalidSchedule, minInterval)
	case lookback < 0:
		return fmt.Errorf("%w: lookback must not be negative", ErrInvalidSchedule)
	case mode == ModeCorrection && lookback == 0:
		return fmt.Errorf("%w: a correction schedule needs a lookback", ErrInvalidSchedule)
	}
	return nil
}

func fromRow(r dbq.Schedule) Schedule {
	return Schedule{
		ID: r.ID, NextRunAt: r.NextRunAt, Enabled: r.Enabled,
		ScheduleSpec: ScheduleSpec{
			ConnectionID: r.ConnectionID, Stream: r.Stream, Mode: r.Mode, Interval: r.RunInterval, Lookback: r.Lookback,
		},
	}
}

// Scheduler turns due schedules into sync jobs. Every process runs one; only the holder of
// a PostgreSQL advisory lock materializes, and any other takes over within one tick.
type Scheduler struct {
	db   *db.DB
	log  *slog.Logger
	now  func() time.Time // tests simulate days with it
	lock *db.SessionLock

	daily   []string          // kinds of global maintenance jobs, see Daily
	dailyAt map[string]string // kind -> UTC day last enqueued by this process
}

// Daily makes the leader enqueue a connection-less job of kind once per UTC day (after Run
// starts, or on the first tick of a new leader). Handlers are idempotent, so a leadership
// change may enqueue a second run on the same day. Call it before Run.
func (s *Scheduler) Daily(kind string) { s.daily = append(s.daily, kind) }

// NewScheduler returns a scheduler; call Run.
func NewScheduler(d *db.DB, log *slog.Logger) *Scheduler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Scheduler{db: d, log: log, now: time.Now}
}

// Run ticks every 15 s until ctx ends, then gives up leadership.
func (s *Scheduler) Run(ctx context.Context) {
	defer s.resign()
	t := time.NewTicker(schedulerTick)
	defer t.Stop()
	for {
		s.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// tick materializes due slots if this scheduler is (or becomes) the leader.
func (s *Scheduler) tick(ctx context.Context) (leader bool) {
	if s.lock != nil {
		if err := s.lock.Held(ctx); err != nil {
			s.log.Warn("scheduler lost leadership", "err", err)
			s.resign()
		}
	}
	if s.lock == nil {
		l, err := s.db.TryLock(ctx, schedulerLockKey)
		if err != nil {
			if ctx.Err() == nil {
				s.log.Error("scheduler leader election", "err", err)
			}
			return false
		}
		if l == nil {
			return false
		}
		s.lock = l
		s.log.Info("scheduler is leader")
	}
	if _, err := s.materialize(ctx, s.now()); err != nil && ctx.Err() == nil {
		s.log.Error("materialize schedules", "err", err)
	}
	s.enqueueDaily(ctx, s.now())
	return true
}

func (s *Scheduler) enqueueDaily(ctx context.Context, now time.Time) {
	day := now.UTC().Format(time.DateOnly)
	for _, kind := range s.daily {
		if s.dailyAt[kind] == day {
			continue
		}
		err := s.db.Tx(ctx, func(q *dbq.Queries) error {
			_, _, err := Enqueue(ctx, q, NewJob{Kind: kind, Priority: PriorityLow, DedupeKey: kind + ":" + day})
			return err
		})
		if err != nil {
			if ctx.Err() == nil {
				s.log.Error("enqueue daily job", "kind", kind, "err", err)
			}
			continue
		}
		if s.dailyAt == nil {
			s.dailyAt = map[string]string{}
		}
		s.dailyAt[kind] = day
	}
}

func (s *Scheduler) resign() {
	if s.lock != nil {
		s.lock.Release()
		s.lock = nil
	}
}

// materialize enqueues one sync job per due schedule with dedupe key schedule_id:slot and
// advances next_run_at in the same transaction, so even two concurrent callers cannot
// duplicate a slot. Slots missed while no leader ran are coalesced into the latest one; the
// cursor covers the gap.
func (s *Scheduler) materialize(ctx context.Context, now time.Time) (int, error) {
	total := 0
	for {
		n := 0
		err := s.db.Tx(ctx, func(q *dbq.Queries) error {
			due, err := q.LockDueSchedules(ctx, dbq.LockDueSchedulesParams{Now: now, MaxRows: dueBatch})
			if err != nil {
				return err
			}
			n = len(due)
			for _, sc := range due {
				slot, next := slotAt(sc.NextRunAt, sc.RunInterval, now)
				p := SyncPayload{ScheduleID: sc.ID, Stream: sc.Stream, Mode: sc.Mode, Slot: slot}
				prio := PriorityNormal
				if sc.Mode == ModeCorrection {
					from := slot.Add(-sc.Lookback)
					p.From, prio = &from, PriorityLow
				}
				cid := sc.ConnectionID
				if _, _, err := Enqueue(ctx, q, NewJob{
					Kind: KindSync, ConnectionID: &cid, Exclusive: true, Priority: prio, RunAt: slot,
					DedupeKey: sc.ID.String() + ":" + slot.UTC().Format(time.RFC3339), Payload: p,
				}); err != nil {
					return err
				}
				if err := q.AdvanceSchedule(ctx, dbq.AdvanceScheduleParams{NextRunAt: next, ID: sc.ID}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		total += n
		if n < dueBatch {
			return total, nil
		}
	}
}

// slotAt returns the latest slot at or before now on the grid next + k·interval, and the
// slot after it.
func slotAt(next time.Time, interval time.Duration, now time.Time) (slot, after time.Time) {
	k := now.Sub(next) / interval
	slot = next.Add(k * interval)
	return slot, slot.Add(interval)
}
