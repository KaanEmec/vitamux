package normalize

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Recomputed counts the rows whose local date changed, per table.
type Recomputed struct{ Measurements, Groups, Workouts, SleepSessions, Events int }

var recomputeBatch int32 = 2000 // a var so tests can force paging

// item is one row of any table under recomputation. Measurements and groups use n as their id,
// workouts and sleep sessions use uid.
type item struct {
	n    int64
	uid  uuid.UUID
	at   time.Time // the instant that defines the local date
	date time.Time
}

type cursor struct {
	at  time.Time
	n   int64
	uid uuid.UUID
}

// RecomputeLocalDates re-derives local dates from the owner's current timezone periods for
// active rows in r that have no stored offset (tz_offset_min NULL). Rows with a record offset or
// record zone, superseded rows and deleted rows are never touched, and rows whose date does not
// change are not written, so the work and the writes are proportional to what the edit changed.
// Measurements whose date changed mark both the old and the new date in resolution_dirty.
//
// It runs in batches of short transactions and is safe to repeat or resume after a failure.
// RecomputeJob wraps it as the recompute_local_dates job, which period edits enqueue.
func RecomputeLocalDates(ctx context.Context, d *db.DB, userID uuid.UUID, r Range) (Recomputed, error) {
	var out Recomputed
	tl, err := loadTimeline(ctx, d.Q(), userID)
	if err != nil {
		return out, err
	}
	if len(tl) == 0 {
		return out, ErrNoTimezone
	}
	from, until := r.From, r.To
	if until.IsZero() {
		until = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	}

	metricIDs, err := d.Q().ListMetricIDs(ctx)
	if err != nil {
		return out, db.MapErr(err)
	}
	for _, id := range metricIDs {
		n, err := recomputeTable(ctx, d, tl, from, until, tableOps{
			list: func(q *dbq.Queries, c cursor) ([]item, error) {
				rows, err := q.ListMeasurementsForLocalDate(ctx, dbq.ListMeasurementsForLocalDateParams{
					UserID: userID, MetricID: id, FromAt: from, UntilAt: until, AfterAt: c.at, AfterID: c.n, Batch: recomputeBatch})
				return mapItems(rows, func(r dbq.ListMeasurementsForLocalDateRow) item { return item{n: r.ID, at: r.At, date: r.LocalDate} }), err
			},
			set: func(q *dbq.Queries, ch []item, dates []time.Time) error {
				if err := q.SetMeasurementLocalDates(ctx, dbq.SetMeasurementLocalDatesParams{Ids: ints(ch), Dates: dates}); err != nil {
					return err
				}
				// Both the day the row leaves and the day it joins need new resolved values.
				all := append(slices.Clip(dates), dates0(ch)...)
				return q.MarkLocalDatesDirty(ctx, dbq.MarkLocalDatesDirtyParams{UserID: userID, MetricID: id, Dates: all})
			},
		})
		out.Measurements += n
		if err != nil {
			return out, err
		}
	}

	out.Groups, err = recomputeTable(ctx, d, tl, from, until, tableOps{
		list: func(q *dbq.Queries, c cursor) ([]item, error) {
			rows, err := q.ListMeasurementGroupsForLocalDate(ctx, dbq.ListMeasurementGroupsForLocalDateParams{
				UserID: userID, FromAt: from, UntilAt: until, AfterAt: c.at, AfterID: c.n, Batch: recomputeBatch})
			return mapItems(rows, func(r dbq.ListMeasurementGroupsForLocalDateRow) item {
				return item{n: r.ID, at: r.At, date: r.LocalDate}
			}), err
		},
		set: func(q *dbq.Queries, ch []item, dates []time.Time) error {
			return q.SetMeasurementGroupLocalDates(ctx, dbq.SetMeasurementGroupLocalDatesParams{Ids: ints(ch), Dates: dates})
		},
	})
	if err != nil {
		return out, err
	}
	out.Workouts, err = recomputeTable(ctx, d, tl, from, until, tableOps{
		list: func(q *dbq.Queries, c cursor) ([]item, error) {
			rows, err := q.ListWorkoutsForLocalDate(ctx, dbq.ListWorkoutsForLocalDateParams{
				UserID: userID, FromAt: from, UntilAt: until, AfterAt: c.at, AfterID: c.uid, Batch: recomputeBatch})
			return mapItems(rows, func(r dbq.ListWorkoutsForLocalDateRow) item { return item{uid: r.ID, at: r.At, date: r.LocalDate} }), err
		},
		set: func(q *dbq.Queries, ch []item, dates []time.Time) error {
			return q.SetWorkoutLocalDates(ctx, dbq.SetWorkoutLocalDatesParams{Ids: uids(ch), Dates: dates})
		},
	})
	if err != nil {
		return out, err
	}
	out.SleepSessions, err = recomputeTable(ctx, d, tl, from, until, tableOps{
		list: func(q *dbq.Queries, c cursor) ([]item, error) {
			rows, err := q.ListSleepSessionsForLocalDate(ctx, dbq.ListSleepSessionsForLocalDateParams{
				UserID: userID, FromAt: from, UntilAt: until, AfterAt: c.at, AfterID: c.uid, Batch: recomputeBatch})
			return mapItems(rows, func(r dbq.ListSleepSessionsForLocalDateRow) item { return item{uid: r.ID, at: r.At, date: r.LocalDate} }), err
		},
		set: func(q *dbq.Queries, ch []item, dates []time.Time) error {
			return q.SetSleepSessionDates(ctx, dbq.SetSleepSessionDatesParams{Ids: uids(ch), Dates: dates})
		},
	})
	if err != nil {
		return out, err
	}
	out.Events, err = recomputeTable(ctx, d, tl, from, until, tableOps{
		list: func(q *dbq.Queries, c cursor) ([]item, error) {
			rows, err := q.ListEventsForLocalDate(ctx, dbq.ListEventsForLocalDateParams{
				UserID: userID, FromAt: from, UntilAt: until, AfterAt: c.at, AfterID: c.uid, Batch: recomputeBatch})
			return mapItems(rows, func(r dbq.ListEventsForLocalDateRow) item { return item{uid: r.ID, at: r.At, date: r.LocalDate} }), err
		},
		set: func(q *dbq.Queries, ch []item, dates []time.Time) error {
			return q.SetEventLocalDates(ctx, dbq.SetEventLocalDatesParams{Ids: uids(ch), Dates: dates})
		},
	})
	return out, err
}

type tableOps struct {
	list func(q *dbq.Queries, after cursor) ([]item, error)
	set  func(q *dbq.Queries, changed []item, newDates []time.Time) error
}

// recomputeTable pages through one table, one transaction per page, and returns how many rows changed.
func recomputeTable(ctx context.Context, d *db.DB, tl Timeline, from, until time.Time, ops tableOps) (int, error) {
	total := 0
	cur := cursor{at: from}
	for {
		var page []item
		changed := 0
		err := d.Tx(ctx, func(q *dbq.Queries) error {
			var err error
			if page, err = ops.list(q, cur); err != nil {
				return err
			}
			var ch []item
			var dates []time.Time
			for _, it := range page {
				l, err := LocalDate(it.at, Zone{}, tl)
				if err != nil {
					return err
				}
				if !l.Date.Equal(it.date) {
					ch = append(ch, it)
					dates = append(dates, l.Date)
				}
			}
			changed = len(ch)
			if changed == 0 {
				return nil
			}
			return ops.set(q, ch, dates)
		})
		if err != nil {
			return total, err
		}
		total += changed
		if len(page) < int(recomputeBatch) {
			return total, nil
		}
		last := page[len(page)-1]
		cur = cursor{at: last.at, n: last.n, uid: last.uid}
	}
}

func mapItems[R any](rows []R, f func(R) item) []item {
	out := make([]item, len(rows))
	for i, r := range rows {
		out[i] = f(r)
	}
	return out
}

func ints(items []item) []int64 {
	out := make([]int64, len(items))
	for i, it := range items {
		out[i] = it.n
	}
	return out
}

func uids(items []item) []uuid.UUID {
	out := make([]uuid.UUID, len(items))
	for i, it := range items {
		out[i] = it.uid
	}
	return out
}

// dates0 returns the dates the changed rows had before the update.
func dates0(items []item) []time.Time {
	out := make([]time.Time, len(items))
	for i, it := range items {
		out[i] = it.date
	}
	return out
}
