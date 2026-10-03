package resolve

import (
	"context"
	"encoding/json"
	"sort"
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
	rebuildBatch    = 1000            // marks per transaction
	aggregateBucket = 5 * time.Minute // buckets counted in source_hourly_aggregates
)

// Register adds the rebuild job to the runner and the daily schedule.
func Register(runner *jobs.Runner, sch *jobs.Scheduler, d *db.DB) {
	runner.Register(KindRebuildAggregates, RebuildJob(d))
	sch.Daily(KindRebuildAggregates)
}

// RebuildJob returns the KindRebuildAggregates handler: RebuildAggregates until no settled mark
// is left.
func RebuildJob(d *db.DB) jobs.Handler {
	return func(ctx context.Context, _ jobs.Job) error {
		for {
			n, err := RebuildAggregates(ctx, d, time.Now().Add(-MarkSettle))
			if err != nil || n < rebuildBatch {
				return err
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
// both sides. dates are sorted.
func rebuildDays(ctx context.Context, q *dbq.Queries, userID uuid.UUID, metricID int16, dates []time.Time, tl normalize.Timeline) error {
	for i := 0; i < len(dates); {
		j := i + 1
		for j < len(dates) && !dates[j].After(dates[j-1].AddDate(0, 0, 1)) {
			j++
		}
		if err := rebuildHours(ctx, q, userID, metricID, dates[i].AddDate(0, 0, -1), dates[j-1].AddDate(0, 0, 1), tl); err != nil {
			return err
		}
		i = j
	}
	return nil
}

// HourlyAggregate is one source_hourly_aggregates row: one source's active sample and interval
// rows of a metric in one local hour (daily values excluded). Buckets counts the UTC-aligned
// 5-minute buckets holding a sample or part of an interval, BucketMeanSum adds each bucket's
// sample mean, IntervalSum the intervals pro-rated linearly to the hour; First and Last bound the
// contributing instants. The JSON names are the columns.
type HourlyAggregate struct {
	UserID        uuid.UUID  `json:"user_id"`
	MetricID      int16      `json:"metric_id"`
	SourceKey     string     `json:"source_key"` // connection/device/origin ids, - when missing
	HourStart     time.Time  `json:"hour_start"`
	LocalDate     string     `json:"local_date"`
	ConnectionID  uuid.UUID  `json:"connection_id"`
	DeviceID      *uuid.UUID `json:"device_id"`
	OriginID      *uuid.UUID `json:"origin_id"`
	Samples       int        `json:"samples"`
	Buckets       int        `json:"buckets"`
	BucketMeanSum float64    `json:"bucket_mean_sum"`
	MinValue      float64    `json:"min_value"`
	MaxValue      float64    `json:"max_value"`
	IntervalSum   float64    `json:"interval_sum"`
	FirstAt       time.Time  `json:"first_at"`
	LastAt        time.Time  `json:"last_at"`

	means map[int64][2]float64 // while building: bucket start -> sample sum, count
}

// rebuildHours replaces the aggregates of the owner's local hours on the dates from through to.
func rebuildHours(ctx context.Context, q *dbq.Queries, userID uuid.UUID, metricID int16, from, to time.Time, tl normalize.Timeline) error {
	var hours []Window
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		hs, err := Buckets(d, time.Hour, tl)
		if err != nil {
			return err
		}
		hours = append(hours, hs...)
	}
	start, end := hours[0].Start, hours[len(hours)-1].End
	if err := q.DeleteHourlyAggregates(ctx, dbq.DeleteHourlyAggregatesParams{UserID: userID, MetricID: metricID, FromAt: start, ToAt: end}); err != nil {
		return err
	}
	rows, err := q.AggregateRows(ctx, dbq.AggregateRowsParams{UserID: userID, MetricID: metricID, FromAt: start.Add(-24 * time.Hour), ToAt: end})
	if err != nil {
		return err
	}
	aggs := map[[2]string]*HourlyAggregate{} // (source key, hour) -> row
	var order []*HourlyAggregate
	get := func(row dbq.AggregateRowsRow, h Window) *HourlyAggregate {
		key := sourceKey(row.ConnectionID, row.DeviceID, row.OriginID)
		k := [2]string{key, h.Key}
		a, ok := aggs[k]
		if !ok {
			a = &HourlyAggregate{UserID: userID, MetricID: metricID, SourceKey: key, HourStart: h.Start, LocalDate: h.Date.Format(dateLayout),
				ConnectionID: row.ConnectionID, DeviceID: row.DeviceID, OriginID: row.OriginID,
				MinValue: row.Value, MaxValue: row.Value, FirstAt: h.End, LastAt: h.Start, means: map[int64][2]float64{}}
			aggs[k] = a
			order = append(order, a)
		}
		a.Samples++
		a.MinValue, a.MaxValue = min(a.MinValue, row.Value), max(a.MaxValue, row.Value)
		return a
	}
	hourAt := func(t time.Time) int { // index of the hour holding t, or len(hours)
		i := sort.Search(len(hours), func(i int) bool { return hours[i].End.After(t) })
		if i < len(hours) && hours[i].Start.After(t) {
			return len(hours)
		}
		return i
	}
	for _, row := range rows {
		if row.EndAt == nil || !row.EndAt.After(row.StartAt) { // a sample counts in the hour it starts
			i := hourAt(row.StartAt)
			if i == len(hours) {
				continue
			}
			a := get(row, hours[i])
			b := row.StartAt.Truncate(aggregateBucket).UnixNano()
			m := a.means[b]
			a.means[b] = [2]float64{m[0] + row.Value, m[1] + 1}
			a.FirstAt, a.LastAt = timeMin(a.FirstAt, row.StartAt), timeMax(a.LastAt, row.StartAt)
			continue
		}
		dur := float64(row.EndAt.Sub(row.StartAt))
		for i := hourAt(timeMax(row.StartAt, start)); i < len(hours) && hours[i].Start.Before(*row.EndAt); i++ {
			h := hours[i]
			lo, hi := timeMax(row.StartAt, h.Start), timeMin(*row.EndAt, h.End)
			a := get(row, h)
			a.IntervalSum += row.Value * float64(hi.Sub(lo)) / dur
			for b := lo.Truncate(aggregateBucket); b.Before(hi); b = b.Add(aggregateBucket) {
				if _, ok := a.means[b.UnixNano()]; !ok {
					a.means[b.UnixNano()] = [2]float64{}
				}
			}
			a.FirstAt, a.LastAt = timeMin(a.FirstAt, lo), timeMax(a.LastAt, hi)
		}
	}
	if len(order) == 0 {
		return nil
	}
	for _, a := range order {
		a.Buckets = len(a.means)
		for _, b := range sortedKeys(a.means) { // in order, so the float sum is reproducible
			if m := a.means[b]; m[1] > 0 {
				a.BucketMeanSum += m[0] / m[1]
			}
		}
	}
	batch, err := json.Marshal(order)
	if err != nil {
		return err
	}
	_, err = q.InsertHourlyAggregates(ctx, batch)
	return err
}

// sourceKey is source_hourly_aggregates.source_key: connection/device/origin, - when missing.
func sourceKey(conn uuid.UUID, device, origin *uuid.UUID) string {
	id := func(u *uuid.UUID) string {
		if u == nil {
			return "-"
		}
		return u.String()
	}
	return conn.String() + "/" + id(device) + "/" + id(origin)
}

// HourlyAggregates returns the stored hourly aggregates of metric whose hour starts in
// [from, to), for coverage and range views. Dirty marks the rebuild job has not consumed yet are
// not reflected. Explained results never read them: they list the rows behind a value.
func HourlyAggregates(ctx context.Context, d *db.DB, userID uuid.UUID, metric string, from, to time.Time) ([]HourlyAggregate, error) {
	rows, err := d.Q().ListHourlyAggregates(ctx, dbq.ListHourlyAggregatesParams{UserID: userID, Metric: metric, FromAt: from, ToAt: to})
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := make([]HourlyAggregate, len(rows))
	for i, r := range rows {
		out[i] = HourlyAggregate{UserID: r.UserID, MetricID: r.MetricID, SourceKey: r.SourceKey, HourStart: r.HourStart,
			LocalDate: r.LocalDate.Format(dateLayout), ConnectionID: r.ConnectionID, DeviceID: r.DeviceID, OriginID: r.OriginID,
			Samples: int(r.Samples), Buckets: int(r.Buckets), BucketMeanSum: r.BucketMeanSum, MinValue: r.MinValue,
			MaxValue: r.MaxValue, IntervalSum: r.IntervalSum, FirstAt: r.FirstAt, LastAt: r.LastAt}
	}
	return out, nil
}
