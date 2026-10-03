package resolve

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

// The resolved cache (docs/architecture/resolution.md#cache-and-materialization) holds the
// results of one local date's windows per (metric, window kind, rule version, overrides
// fingerprint). Dates are independent (loader.results), so a request reads its closed dates
// from the cache and computes only the span of the others. Rows are written only for dates whose
// windows have all closed, and never for bucket windows, drafts (previews), Sources drilldowns
// or Live requests.
//
// Invalidation lives in the triggers of the migration that creates resolved_cache, inside the
// writer's transaction: a dirty mark deletes the rows whose deps hold its code and whose
// dep_from..dep_to holds its date, rule activation deletes the rows whose deps hold the rule's
// metric, a workout change the rows of rules with a workout context, and timezone, device or
// origin changes the owner's rows. deps lists everything a date read (cacheDeps), so a dirty
// heart_rate mark also reaches resting_heart_rate_nocturnal, a leader's mark its followers, and
// a sleep_date D mark night D+1 (ADR-0009).

const (
	// depsAhead and depsBack bound the local dates a date's results read: rows from a day before
	// the date (two for local dates of other offsets, three with the previous day that
	// definition_changed resolves) to a day after it.
	depsAhead = 2
	depsBack  = 3
)

// cacheDeps returns what results of metric under rule r (window kind) read, for resolved_cache
// deps: the metric and its rule metric, the codes it loads and its overrides mark, the wear code,
// sleep for night windows and sleep contexts, "workouts" for workout contexts and the follow
// leader's deps; and how many days before a date they reach.
func cacheDeps(ctx context.Context, d *db.DB, userID uuid.UUID, metric string, r *Rule, kind catalog.Window) ([]string, int, error) {
	ruleMetric := RuleMetric(metric)
	deps := append([]string{metric, ruleMetric}, metricCodes(ruleMetric)...)
	back := depsBack
	sleep := ruleMetric == FamilySleep || kind == catalog.WindowLocalNight || kind == catalog.WindowSleepEpisode
	if ruleMetric != FamilySleep {
		sp, err := r.spec()
		if err != nil {
			return nil, 0, err
		}
		deps = append(deps, sp.codes...)
		if q := r.Quality; q != nil && q.RequireWear != "" {
			deps = append(deps, q.RequireWear)
			back += int(WearLookback / (24 * time.Hour))
		}
		if kind == catalog.WindowLatest {
			back += int(LatestLookback / (24 * time.Hour))
		}
		sleep = sleep || len(r.Contexts[ContextSleep]) > 0
		if len(r.Contexts[ContextWorkout]) > 0 {
			deps = append(deps, "workouts")
		}
		if r.Follow != "" {
			lv, err := activeRule(ctx, d, userID, r.Follow, nil)
			if err != nil {
				return nil, 0, err
			}
			ld, lb, err := cacheDeps(ctx, d, userID, r.Follow, lv.Rule, kind)
			if err != nil {
				return nil, 0, err
			}
			deps, back = append(deps, ld...), max(back, lb)
		}
	}
	if sleep {
		deps = append(deps, FamilySleep)
		deps = append(deps, metricCodes(FamilySleep)...)
	}
	slices.Sort(deps)
	return slices.Compact(deps), back, nil
}

// run is Run per local date: days[i] holds the results of From+i.
func run(ctx context.Context, d *db.DB, req Request) ([][]Result, error) {
	if req.Now.IsZero() {
		req.Now = time.Now()
	}
	req.From, req.To = midnightUTC(req.From), midnightUTC(req.To)
	if req.To.Before(req.From) {
		return nil, fmt.Errorf("resolve: range ends before it starts")
	}
	ruleMetric := RuleMetric(req.Metric)
	v, err := activeRule(ctx, d, req.UserID, ruleMetric, req.Rule)
	if err != nil {
		return nil, err
	}
	if req.Kind == "" {
		req.Kind = v.Rule.Window.Kind
	}
	ovs, err := NewOverrides(d).Active(ctx, req.UserID, ruleMetric, req.From.AddDate(0, 0, -1), req.To.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	n := int(req.To.Sub(req.From)/(24*time.Hour)) + 1
	days := make([][]Result, n)
	missing := make([]bool, n)
	for i := range missing {
		missing[i] = true
	}
	// Bucket windows (5 min and finer) are cheap to compute and would make large rows.
	cached := req.Rule == nil && !req.Sources && !req.Live && req.Kind != catalog.WindowBucket
	var fps []string
	if cached {
		fps = make([]string, n)
		dates := make([]time.Time, n)
		for i := range n {
			dates[i] = req.From.AddDate(0, 0, i)
			fps[i] = overridesFingerprint(ovs, dates[i])
		}
		rows, err := d.Q().GetResolvedCache(ctx, dbq.GetResolvedCacheParams{Dates: dates, Fps: fps, UserID: req.UserID,
			Metric: req.Metric, WindowKind: string(req.Kind), RuleRef: v.Ref, Now: req.Now})
		if err != nil {
			return nil, db.MapErr(err)
		}
		for _, row := range rows {
			i := int(midnightUTC(row.LocalDate).Sub(req.From) / (24 * time.Hour))
			if err := json.Unmarshal(row.Results, &days[i]); err != nil {
				return nil, fmt.Errorf("resolve: cached %s %s: %w", req.Metric, row.LocalDate.Format(dateLayout), err)
			}
			missing[i] = false
		}
	}
	lo, hi := slices.Index(missing, true), len(missing)-1
	if lo < 0 {
		return days, nil
	}
	for !missing[hi] {
		hi--
	}
	span := req
	span.From, span.To = req.From.AddDate(0, 0, lo), req.From.AddDate(0, 0, hi)
	live, err := compute(ctx, d, span, v, ovs)
	if err != nil {
		return nil, err
	}
	for i := lo; i <= hi; i++ {
		if missing[i] {
			days[i] = live[i-lo]
		}
	}
	if cached {
		if err := putCache(ctx, d, req, v, days, missing, fps); err != nil {
			return nil, err
		}
	}
	return days, nil
}

// overridesFingerprint hashes the ids of the active overrides on date and its neighbours (the
// dates whose windows a date's results can read); "" without any.
func overridesFingerprint(ovs []Override, date time.Time) string {
	var ids []string
	for _, o := range ovs {
		if o.Active() && !o.LocalDate.Before(date.AddDate(0, 0, -1)) && !o.LocalDate.After(date.AddDate(0, 0, 1)) {
			ids = append(ids, o.ID.String())
		}
	}
	if len(ids) == 0 {
		return ""
	}
	slices.Sort(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, ",")))
	return hex.EncodeToString(sum[:16])
}

// putCache stores the computed dates whose windows have all closed. A date with a pending dirty
// mark is not stored, and a rebuild_aggregates job is queued to consume the mark.
func putCache(ctx context.Context, d *db.DB, req Request, v Version, days [][]Result, missing []bool, fps []string) error {
	deps, back, err := cacheDeps(ctx, d, req.UserID, req.Metric, v.Rule, req.Kind)
	if err != nil {
		return err
	}
	tl, err := timeline(ctx, d, req.UserID)
	if err != nil {
		return err
	}
	p := dbq.PutResolvedCacheParams{UserID: req.UserID, Metric: req.Metric, WindowKind: string(req.Kind), RuleRef: v.Ref,
		Deps: deps, BackDays: int32(back), AheadDays: depsAhead} //nolint:gosec // a few hundred days
	for i, day := range days {
		if !missing[i] {
			continue
		}
		date := req.From.AddDate(0, 0, i)
		lw, err := LocalDay(date, tl)
		if err != nil {
			return err
		}
		complete := lw.End
		for _, r := range day {
			complete = timeMax(complete, r.Window.End)
		}
		if complete.After(req.Now) {
			continue
		}
		raw, err := json.Marshal(day)
		if err != nil {
			continue // a value JSON cannot hold (NaN) is not cached
		}
		p.Dates, p.Fps = append(p.Dates, date), append(p.Fps, fps[i])
		p.Results, p.CompleteAt = append(p.Results, raw), append(p.CompleteAt, complete)
	}
	if len(p.Dates) == 0 {
		return nil
	}
	stored, err := d.Q().PutResolvedCache(ctx, p)
	if err != nil {
		return db.MapErr(err)
	}
	if len(stored) < len(p.Dates) {
		_, _, err = jobs.Enqueue(ctx, d.Q(), jobs.NewJob{Kind: KindRebuildAggregates, Priority: jobs.PriorityLow,
			DedupeKey: KindRebuildAggregates, RunAt: time.Now().Add(MarkSettle)})
	}
	return err
}
