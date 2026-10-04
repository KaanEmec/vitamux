package api

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/resolve"
)

// Display rollups (J21.6, docs/architecture/resolution.md#display-rollups): the dashboard
// summary and long-range trends are plain statistics of resolved daily values. They read the
// same windows as /resolved/daily through resolve.Run and resolved_cache, never write anything
// back, and are not a resolution strategy.

const (
	maxSummaryMetrics = 20
	maxTrendDays      = 3660 // about ten years
	sparklineDays     = 30
)

// statPeriods are the summary's rollups: the 7, 30 and 90 local dates ending at its date.
var statPeriods = []int{7, 30, 90}

// metricList trims and de-duplicates requested metrics and checks each is resolvable.
func metricList(requested []string) ([]string, error) {
	var out []string
	for _, m := range requested {
		if m = strings.TrimSpace(m); m != "" && !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	for _, m := range out {
		if !resolvable(m) {
			return nil, problemErr(CodeValidationFailed, "unknown metric", FieldError{Pointer: "/metrics", Detail: "no such metric: " + strconv.Quote(m)})
		}
	}
	return out, nil
}

// dayValue is a resolved daily value as rollups read it: a number or a family's components.
type dayValue struct {
	value      *float64
	components map[string]float64
}

// rollupValue is r's value for rollups; false without one and while its window is still open.
func rollupValue(r resolve.Result) (dayValue, bool) {
	if r.Status == resolve.ResultNoData || r.Partial || (r.Value == nil && len(r.Components) == 0) {
		return dayValue{}, false
	}
	return dayValue{r.Value, r.Components}, true
}

// eachDaily resolves metric's daily windows (the kind /resolved/daily uses) on the dates from
// through to, in requests of at most maxResolvedDays, and passes every result to fn.
func (o *owner) eachDaily(ctx context.Context, metric string, v resolve.Version, from, to time.Time, fn func(resolve.Result)) error {
	user, now := auth.PrincipalFrom(ctx).UserID, time.Now()
	for d := from; !d.After(to); d = d.AddDate(0, 0, maxResolvedDays) {
		e := d.AddDate(0, 0, maxResolvedDays-1)
		if e.After(to) {
			e = to
		}
		rs, err := resolve.Run(ctx, o.opts.DB, resolve.Request{UserID: user, Metric: metric, Kind: dailyKind(metric, v.Rule), From: d, To: e, Now: now})
		if err != nil {
			return resolveErr(err)
		}
		for _, r := range rs {
			fn(r)
		}
	}
	return nil
}

// stat accumulates n, sum, min and max.
type stat struct {
	n             int
	sum, min, max float64
}

func (s *stat) add(x float64) {
	if s.n == 0 || x < s.min {
		s.min = x
	}
	if s.n == 0 || x > s.max {
		s.max = x
	}
	s.sum += x
	s.n++
}

// rollup is the statistics of the values of the dates start through end; sum only for additive
// metrics.
func rollup(start, end time.Time, values map[time.Time]dayValue, additive bool) oapi.Rollup {
	out := oapi.Rollup{StartDate: apiDate(start), EndDate: apiDate(end)}
	var all stat
	comps := map[string]*stat{}
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		out.Days++
		v, ok := values[d]
		if !ok {
			continue
		}
		out.N++
		if v.value != nil {
			all.add(*v.value)
		}
		for code, x := range v.components {
			if comps[code] == nil {
				comps[code] = &stat{}
			}
			comps[code].add(x)
		}
	}
	if out.Days > 0 {
		out.Coverage = *round3(float64(out.N) / float64(out.Days))
	}
	if all.n > 0 {
		out.Mean, out.Min, out.Max = optNum(all.sum/float64(all.n)), optNum(all.min), optNum(all.max)
		if additive {
			out.Sum = optNum(all.sum)
		}
	}
	if len(comps) > 0 {
		cs := make(map[string]oapi.RollupValues, len(comps))
		for code, s := range comps {
			cs[code] = oapi.RollupValues{N: s.n, Mean: optNum(s.sum / float64(s.n)), Min: optNum(s.min), Max: optNum(s.max)}
		}
		out.Components = &cs
	}
	return out
}

func additive(metric string) bool {
	m, ok := catalog.Lookup(metric)
	return ok && m.Agg == catalog.Additive
}

func unitOf(metric string) *string {
	if m, ok := catalog.Lookup(metric); ok {
		return &m.Unit
	}
	return nil
}

// ---- summary

// GetResolvedSummary answers each metric's value of date, its last 30 daily values and the 7-,
// 30- and 90-day rollups ending at date.
func (o *owner) GetResolvedSummary(ctx context.Context, req oapi.GetResolvedSummaryRequestObject) (oapi.GetResolvedSummaryResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	metrics, err := metricList(req.Params.Metrics)
	switch {
	case err != nil:
		return nil, err
	case len(metrics) == 0 || len(metrics) > maxSummaryMetrics:
		return nil, problemErr(CodeValidationFailed, "invalid metrics", FieldError{Pointer: "/metrics", Detail: "one to 20 metrics"})
	}
	z, err := o.zones(ctx)
	noTimezone := errors.Is(err, errNoTimezone)
	if err != nil && !noTimezone {
		return nil, err
	}
	var date time.Time
	switch {
	case req.Params.Date != nil:
		date = req.Params.Date.Time
	case noTimezone:
		y, m, d := time.Now().UTC().Date()
		date = time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	default:
		if date, err = z.localDate(time.Now()); err != nil {
			return nil, resolveErr(err)
		}
	}
	out := oapi.GetResolvedSummary200JSONResponse{Date: apiDate(date), Metrics: map[string]oapi.MetricSummary{}}
	if !noTimezone {
		day, err := resolve.LocalDay(date, z.tl)
		if err != nil {
			return nil, resolveErr(err)
		}
		out.Timezone = z.name(day.Start)
	}
	from, sparkFrom := date.AddDate(0, 0, 1-statPeriods[len(statPeriods)-1]), date.AddDate(0, 0, 1-sparklineDays)
	for _, m := range metrics {
		s := oapi.MetricSummary{Metric: m, Unit: unitOf(m), Value: noRule(m), Sparkline: make([]oapi.SummaryPoint, 0, sparklineDays)}
		points := map[time.Time]oapi.SummaryPoint{}
		values := map[time.Time]dayValue{}
		v, ok, err := o.ruleFor(ctx, m)
		switch {
		case err != nil:
			return nil, err
		case noTimezone:
			s.Value = oapi.ResolvedValue{Status: oapi.ResolvedValueStatus(resolve.ResultNoData),
				Explanation: "No timezone period is configured, so there are no local days yet; add one in the settings."}
		case ok:
			ref := ruleRef(v, v.Rule.Strategy.Op)
			s.Rule = &ref
			err := o.eachDaily(ctx, m, v, from, date, func(r resolve.Result) {
				d := r.Window.Date
				if d.Equal(date) {
					s.Value = resolvedValue(z, r, v.Rule)
				}
				if !d.Before(sparkFrom) {
					p := oapi.SummaryPoint{LocalDate: apiDate(d), Status: oapi.SummaryPointStatus(r.Status), Value: valueOf(r.Value, r.Components)}
					if r.Partial {
						p.Partial = &r.Partial
					}
					points[d] = p
				}
				if dv, ok := rollupValue(r); ok {
					values[d] = dv
				}
			})
			if err != nil {
				return nil, err
			}
		}
		for d := sparkFrom; !d.After(date); d = d.AddDate(0, 0, 1) {
			p, ok := points[d]
			if !ok {
				p = oapi.SummaryPoint{LocalDate: apiDate(d), Status: oapi.SummaryPointStatus(resolve.ResultNoData)}
			}
			s.Sparkline = append(s.Sparkline, p)
		}
		for _, n := range statPeriods {
			s.Stats = append(s.Stats, rollup(date.AddDate(0, 0, 1-n), date, values, additive(m)))
		}
		out.Metrics[m] = s
	}
	return out, nil
}

// ---- trend

// GetResolvedTrend answers weekly or monthly rollups of one metric's resolved daily values over
// up to maxTrendDays dates.
func (o *owner) GetResolvedTrend(ctx context.Context, req oapi.GetResolvedTrendRequestObject) (oapi.GetResolvedTrendResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	prm := req.Params
	from, to := prm.StartDate.Time, prm.EndDate.Time
	grain := ptrVal(prm.Grain)
	if grain == "" {
		grain = oapi.GetResolvedTrendParamsGrainWeek
	}
	switch {
	case !resolvable(prm.Metric):
		return nil, problemErr(CodeValidationFailed, "unknown metric", FieldError{Pointer: "/metric", Detail: "no such metric"})
	case grain != oapi.GetResolvedTrendParamsGrainWeek && grain != oapi.GetResolvedTrendParamsGrainMonth:
		return nil, problemErr(CodeValidationFailed, "invalid grain", FieldError{Pointer: "/grain", Detail: "must be week or month"})
	case to.Before(from):
		return nil, problemErr(CodeValidationFailed, "invalid range", FieldError{Pointer: "/end_date", Detail: "must not be before start_date"})
	case int(to.Sub(from).Hours()/24) >= maxTrendDays:
		return nil, problemErr(CodeValidationFailed, "invalid range", FieldError{Pointer: "/end_date", Detail: "at most 3,660 dates"})
	}
	z, err := o.zones(ctx)
	if err != nil {
		return nil, err
	}
	first, err := resolve.LocalDay(from, z.tl)
	if err != nil {
		return nil, resolveErr(err)
	}
	out := oapi.GetResolvedTrend200JSONResponse{Metric: prm.Metric, Unit: unitOf(prm.Metric), Grain: oapi.ResolvedTrendGrain(grain),
		Timezone: z.name(first.Start), StartDate: prm.StartDate, EndDate: prm.EndDate, Buckets: []oapi.Rollup{}}
	values := map[time.Time]dayValue{}
	v, ok, err := o.ruleFor(ctx, prm.Metric)
	if err != nil {
		return nil, err
	}
	if ok {
		ref := ruleRef(v, v.Rule.Strategy.Op)
		out.Rule = &ref
		err := o.eachDaily(ctx, prm.Metric, v, from, to, func(r resolve.Result) {
			if dv, ok := rollupValue(r); ok {
				values[r.Window.Date] = dv
			}
		})
		if err != nil {
			return nil, err
		}
	}
	for d := from; !d.After(to); {
		next := nextBucket(d, grain)
		end := next.AddDate(0, 0, -1)
		if end.After(to) {
			end = to
		}
		out.Buckets = append(out.Buckets, rollup(d, end, values, additive(prm.Metric)))
		d = next
	}
	return out, nil
}

// nextBucket is the first date of the ISO week (Monday) or calendar month after d's.
func nextBucket(d time.Time, grain oapi.GetResolvedTrendParamsGrain) time.Time {
	if grain == oapi.GetResolvedTrendParamsGrainMonth {
		return time.Date(d.Year(), d.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	}
	return d.AddDate(0, 0, 7-(int(d.Weekday())+6)%7)
}
