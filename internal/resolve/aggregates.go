package resolve

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/jobs"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// KindRebuildAggregates is the job that consumes resolution_dirty: it rebuilds the
// source_hourly_aggregates of the marked days and deletes the marks in the same transaction.
// Requests that could not cache a date because of a pending mark queue it; the scheduler also
// runs it daily.
const KindRebuildAggregates = "rebuild_aggregates"

// MarkSettle is how old a dirty mark must be before the rebuild consumes it. Until then a
// request that read rows before the marking writer committed cannot cache them (PutResolvedCache
// checks for pending marks), so only a request running longer than this could cache a stale date.
const MarkSettle = time.Minute

const (
	rebuildBatch = 1000 // marks per transaction
	rebuildChunk = 31   // local days per aggregation statement
)

// Register adds the rebuild job to the runner and the daily schedule.
func Register(runner *jobs.Runner, sch *jobs.Scheduler, d *db.DB) {
	runner.Register(KindRebuildAggregates, RebuildJob(d))
	sch.Daily(KindRebuildAggregates)
}

// RebuildJob returns the KindRebuildAggregates handler: RebuildAggregates until no settled mark
// is left. Each batch commits its rebuilt hours with the consumed marks, so a restarted job goes
// on with the marks still left; the checkpoint counts the marks consumed across attempts.
func RebuildJob(d *db.DB) jobs.Handler {
	return func(ctx context.Context, j jobs.Job) error {
		var cp struct {
			Consumed int `json:"consumed"`
		}
		if len(j.Checkpoint) > 0 {
			if err := json.Unmarshal(j.Checkpoint, &cp); err != nil {
				return fmt.Errorf("rebuild_aggregates: checkpoint: %w", err)
			}
		}
		for {
			n, err := RebuildAggregates(ctx, d, time.Now().Add(-MarkSettle))
			if err != nil || n == 0 {
				return err
			}
			cp.Consumed += n
			if err := j.SaveCheckpoint(ctx, cp); err != nil {
				return err
			}
			if n < rebuildBatch {
				return nil
			}
		}
	}
}

// RebuildAggregates consumes up to one batch of dirty marks set before before: for each marked
// (owner, metric) it rebuilds the hourly aggregates of the marked days and their neighbours (a
// row's own local date can fall on the next or previous day of the owner's timeline), then
// deletes the marks, all in one transaction. Sleep-derived and derived codes have no aggregates;
// their marks are only consumed. It returns the number of marks consumed.
func RebuildAggregates(ctx context.Context, d *db.DB, before time.Time) (int, error) {
	var n int
	err := d.Tx(ctx, func(q *dbq.Queries) error {
		marks, err := q.ClaimDirtyMarks(ctx, dbq.ClaimDirtyMarksParams{Before: before, MaxRows: rebuildBatch})
		if err != nil {
			return err
		}
		if err := q.DisableJIT(ctx); err != nil {
			return err
		}
		n = len(marks)
		var del dbq.DeleteDirtyMarksParams
		timelines := map[uuid.UUID]normalize.Timeline{}
		for i := 0; i < len(marks); {
			j := i
			for j < len(marks) && marks[j].UserID == marks[i].UserID && marks[j].MetricID == marks[i].MetricID {
				del.UserIds, del.MetricIds = append(del.UserIds, marks[j].UserID), append(del.MetricIds, marks[j].MetricID)
				del.Dates, del.MarkedAt = append(del.Dates, marks[j].LocalDate), append(del.MarkedAt, marks[j].MarkedAt)
				j++
			}
			m, ok := catalog.Lookup(marks[i].Metric)
			if ok && len(m.Kinds) > 0 {
				tl, ok := timelines[marks[i].UserID]
				if !ok {
					if tl, err = normalize.NewPeriods(d).Timeline(ctx, marks[i].UserID); err != nil {
						return err
					}
					timelines[marks[i].UserID] = tl
				}
				if len(tl) == 0 { // no timezone yet: no local hours; the marks are still consumed
					i = j
					continue
				}
				dates := make([]time.Time, 0, j-i)
				for _, mk := range marks[i:j] {
					dates = append(dates, midnightUTC(mk.LocalDate))
				}
				if err := rebuildDays(ctx, q, marks[i].UserID, marks[i].MetricID, dates, tl); err != nil {
					return err
				}
			}
			i = j
		}
		if n > 0 {
			_, err = q.DeleteDirtyMarks(ctx, del)
		}
		return err
	})
	return n, err
}

// rebuildDays rebuilds the aggregates of each run of consecutive marked dates, one day wider on
// both sides, in chunks of rebuildChunk days. dates are sorted.
func rebuildDays(ctx context.Context, q *dbq.Queries, userID uuid.UUID, metricID int16, dates []time.Time, tl normalize.Timeline) error {
	for i := 0; i < len(dates); {
		j := i + 1
		for j < len(dates) && !dates[j].After(dates[j-1].AddDate(0, 0, 1)) {
			j++
		}
		for from, to := dates[i].AddDate(0, 0, -1), dates[j-1].AddDate(0, 0, 1); !from.After(to); from = from.AddDate(0, 0, rebuildChunk) {
			if err := rebuildHours(ctx, q, userID, metricID, from, timeMin(to, from.AddDate(0, 0, rebuildChunk-1)), tl); err != nil {
				return err
			}
		}
		i = j
	}
	return nil
}

// rebuildHours replaces the aggregates of the owner's local hours on the dates from through to.
// The database aggregates the rows (RebuildHourlyAggregates): those starting from a day before
// the first hour, so intervals crossing into it count. Go only lists the hours, which follow the
// owner's timeline (DST, travel).
func rebuildHours(ctx context.Context, q *dbq.Queries, userID uuid.UUID, metricID int16, from, to time.Time, tl normalize.Timeline) error {
	p := dbq.RebuildHourlyAggregatesParams{UserID: userID, MetricID: metricID}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		hs, err := Buckets(d, time.Hour, tl)
		if err != nil {
			return err
		}
		for _, h := range hs {
			p.HourStarts, p.HourEnds, p.LocalDates = append(p.HourStarts, h.Start), append(p.HourEnds, h.End), append(p.LocalDates, h.Date)
		}
	}
	if len(p.HourStarts) == 0 {
		return nil
	}
	start, end := p.HourStarts[0], p.HourEnds[len(p.HourEnds)-1]
	if err := q.DeleteHourlyAggregates(ctx, dbq.DeleteHourlyAggregatesParams{UserID: userID, MetricID: metricID, FromAt: start, ToAt: end}); err != nil {
		return err
	}
	p.FromAt, p.ToAt = start.Add(-24*time.Hour), end
	_, err := q.RebuildHourlyAggregates(ctx, p)
	return err
}
