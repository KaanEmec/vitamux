package resolve

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// LatestLookback is how far before a range Run loads rows for latest windows: the oldest value
// a latest window can return.
const LatestLookback = 366 * 24 * time.Hour

// Request is one resolution query: a metric over an inclusive range of local dates.
type Request struct {
	UserID uuid.UUID
	// Metric is a catalogue code (a sleep or blood pressure code resolves through its family's
	// rule) or a family (sleep, blood_pressure).
	Metric   string
	Kind     catalog.Window // "" uses the rule's window
	From, To time.Time      // local dates, inclusive
	Now      time.Time      // zero is time.Now(); open windows are partial and capped at it
	Rule     *Version       // nil uses the rule in effect (Store.Active); a preview passes a draft
	Sources  bool           // also build every window's all-sources drilldown (Result.Sources)
}

// Resolve resolves metric for every window of kind ("" for the rule's) on the local dates from
// through to, with the rule in effect and the active overrides. It only reads: rule, timezone
// periods, overrides, the metric's rows (and its wear series), sleep sessions and workouts for
// contexts and night windows, and the follow leader's results. J09.9 caches what it returns.
func Resolve(ctx context.Context, d *db.DB, userID uuid.UUID, metric string, kind catalog.Window, from, to time.Time) ([]Result, error) {
	return Run(ctx, d, Request{UserID: userID, Metric: metric, Kind: kind, From: from, To: to})
}

// Run is Resolve with every option of Request.
func Run(ctx context.Context, d *db.DB, req Request) ([]Result, error) {
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
	l := &loader{d: d, q: d.Q(), req: req, rule: v.Rule}
	if req.Kind == "" {
		req.Kind = v.Rule.Window.Kind
		l.req.Kind = req.Kind
	}
	if l.tl, err = normalize.NewPeriods(d).Timeline(ctx, req.UserID); err != nil {
		return nil, err
	}
	ovs, err := NewOverrides(d).Active(ctx, req.UserID, ruleMetric, req.From.AddDate(0, 0, -1), req.To.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	if ruleMetric == FamilySleep {
		return l.sleepResults(ctx, v, ovs)
	}
	return l.results(ctx, v, ovs)
}

// RuleMetric is the rule a metric resolves under: its family for sleep and blood pressure codes,
// else the metric itself.
func RuleMetric(metric string) string {
	m, ok := catalog.Lookup(metric)
	switch {
	case !ok:
	case m.Agg == catalog.SleepDerived:
		return FamilySleep
	case m.Group == "bp_reading":
		return FamilyBloodPressure
	}
	return metric
}

func activeRule(ctx context.Context, d *db.DB, userID uuid.UUID, metric string, draft *Version) (Version, error) {
	if draft != nil {
		if draft.Rule == nil || draft.Rule.Metric != metric {
			return Version{}, fmt.Errorf("resolve: draft rule is not a rule for %s", metric)
		}
		return *draft, nil
	}
	v, err := NewStore(d).Active(ctx, userID, metric)
	if errors.Is(err, db.ErrNotFound) {
		return Version{}, fmt.Errorf("resolve: no rule for %s: %w", metric, err)
	}
	return v, err
}

// loader holds what one Run reads.
type loader struct {
	d    *db.DB
	q    *dbq.Queries
	req  Request
	rule *Rule
	tl   normalize.Timeline

	sleepRule *Rule
	sleepIn   []SleepInput
	nights    map[time.Time]SleepAlignment
}

// results resolves a non-sleep metric.
func (l *loader) results(ctx context.Context, v Version, ovs []Override) ([]Result, error) {
	r, req := v.Rule, l.req
	sp, err := r.spec()
	if err != nil {
		return nil, err
	}
	needSleep := req.Kind == catalog.WindowLocalNight || req.Kind == catalog.WindowSleepEpisode || len(r.Contexts[ContextSleep]) > 0
	if needSleep {
		if err := l.loadSleep(ctx); err != nil {
			return nil, err
		}
	}
	first, err := LocalDay(req.From, l.tl)
	if err != nil {
		return nil, err
	}
	last, err := LocalDay(req.To, l.tl)
	if err != nil {
		return nil, err
	}
	from, to := first.Start.Add(-24*time.Hour), last.End.Add(24*time.Hour) // nights start the evening before
	if req.Kind == catalog.WindowLatest {
		from = first.Start.Add(-LatestLookback)
	}
	s, err := l.loadSeries(ctx, sp.codes, from, to)
	if err != nil {
		return nil, err
	}
	if q := r.Quality; q != nil && q.RequireWear != "" {
		wear, err := l.loadSeries(ctx, []string{q.RequireWear}, from.Add(-WearLookback), to)
		if err != nil {
			return nil, err
		}
		if _, dup := s[q.RequireWear]; !dup {
			s[q.RequireWear] = wear[q.RequireWear]
		}
	}
	ws, err := l.windows(r, s, sp)
	if err != nil {
		return nil, err
	}

	opt := Options{Now: req.Now}
	if len(r.Contexts) > 0 {
		if len(r.Contexts[ContextSleep]) > 0 {
			for _, a := range l.nights {
				opt.Events.Sleep = append(opt.Events.Sleep, a.Episodes...)
			}
		}
		if len(r.Contexts[ContextWorkout]) > 0 {
			wo, err := l.loadWorkouts(ctx, first.Start, last.End)
			if err != nil {
				return nil, err
			}
			opt.Events.Workouts = ClusterWorkouts(wo)
		}
	}
	if r.Follow != "" {
		lead := req
		lead.Metric, lead.Rule, lead.Sources = r.Follow, nil, false
		leader, err := Run(ctx, l.d, lead)
		if err != nil {
			return nil, fmt.Errorf("resolve: follow leader %s: %w", r.Follow, err)
		}
		opt.Leader = map[string]string{}
		for _, x := range leader {
			if x.Selected != "" {
				opt.Leader[x.Window.Key] = x.Selected
			}
		}
	}

	resolved, err := r.ResolveWindowsOverridden(ws, s, opt, ovs)
	if err != nil {
		return nil, err
	}
	out := make([]Result, len(resolved))
	for i, res := range resolved {
		out[i] = BuildResult(req.Metric, v, res, req.Now)
		if req.Sources {
			if out[i].Sources, err = BuildSources(r, res.Window, s, req.Now); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// windows builds the windows of the requested kind on every date of the range.
func (l *loader) windows(r *Rule, s Series, sp spec) ([]Window, error) {
	req := l.req
	var out []Window
	if req.Kind == catalog.WindowReading {
		var in []Input
		for _, code := range sp.codes {
			for _, x := range s[code] {
				if !x.LocalDate.Before(req.From) && !x.LocalDate.After(req.To) {
					in = append(in, x)
				}
			}
		}
		return ReadingWindows(in), nil
	}
	for d := req.From; !d.After(req.To); d = d.AddDate(0, 0, 1) {
		switch req.Kind {
		case catalog.WindowLatest:
			day, err := LocalDay(d, l.tl)
			if err != nil {
				return nil, err
			}
			out = append(out, LatestWindow(timeMin(day.End, req.Now)))
		case catalog.WindowSleepEpisode:
			for _, e := range l.nights[d].Episodes {
				out = append(out, EpisodeWindow(e))
			}
		case catalog.WindowLocalNight:
			w, err := LocalNight(d, l.sleepRule.NightAnchor(), l.tl)
			if err != nil {
				return nil, err
			}
			if e, ok := l.nights[d].Main(); ok {
				w = w.WithEpisode(e)
			} else {
				w.Start = w.End // no episode: an empty window, explained as such
			}
			out = append(out, w)
		default:
			rw := RuleWindow{Kind: req.Kind}
			if req.Kind == catalog.WindowBucket {
				rw.Size = r.Window.Size
				if rw.Size == "" {
					rw.Size = "5m"
				}
			}
			ws, err := DayWindows(rw, d, l.tl, r.NightAnchor())
			if err != nil {
				return nil, err
			}
			out = append(out, ws...)
		}
	}
	return out, nil
}

// sleepResults resolves the sleep family or one sleep code: one result per night (local_night,
// the main episode) or per episode (sleep_episode). Overrides of the sleep family can force a
// group; other actions are listed as ignored.
func (l *loader) sleepResults(ctx context.Context, v Version, ovs []Override) ([]Result, error) {
	req := l.req
	l.sleepRule = v.Rule
	if err := l.loadSleep(ctx); err != nil {
		return nil, err
	}
	codes := []string{req.Metric}
	if req.Metric == FamilySleep {
		codes = metricCodes(FamilySleep)
	}
	opt := Options{Now: req.Now}
	var out []Result
	for d := req.From; !d.After(req.To); d = d.AddDate(0, 0, 1) {
		a := l.nights[d]
		var eps []Episode
		var ws []Window
		switch req.Kind {
		case catalog.WindowLocalNight:
			w, err := LocalNight(d, v.Rule.NightAnchor(), l.tl)
			if err != nil {
				return nil, err
			}
			e, ok := a.Main()
			if ok {
				w = w.WithEpisode(e)
			} else {
				w.Start, e = w.End, Episode{Night: d}
			}
			eps, ws = []Episode{e}, []Window{w}
		case catalog.WindowSleepEpisode:
			for _, e := range a.Episodes {
				eps, ws = append(eps, e), append(ws, EpisodeWindow(e))
			}
		default:
			return nil, fmt.Errorf("resolve: window %s is not allowed for %s", req.Kind, req.Metric)
		}
		for i, e := range eps {
			wr, err := a.ResolveEpisode(ws[i], e, codes, opt)
			if err != nil {
				return nil, err
			}
			res := Resolved{WindowResult: wr}
			for _, o := range ovs {
				if !o.Active() || o.Metric != FamilySleep || o.Kind != ws[i].Kind || o.Key != ws[i].Key {
					continue
				}
				if res.Computed == nil {
					computed := wr
					res.Computed = &computed
				}
				if o.Action == ForceSource && a.ForceGroup(&res.WindowResult, e, codes, o.Group) {
					res.Overrides = append(res.Overrides, o)
					continue
				}
				res.Ignored = append(res.Ignored, o)
			}
			r := BuildResult(req.Metric, v, res, req.Now)
			if req.Sources {
				r.Sources = a.Sources(e)
			}
			out = append(out, r)
		}
	}
	return out, nil
}

// loadSleep loads the sessions of the nights around the range and aligns each night under the
// sleep-family rule (the request's own, or the one in effect).
func (l *loader) loadSleep(ctx context.Context) error {
	if l.sleepRule == nil {
		v, err := activeRule(ctx, l.d, l.req.UserID, FamilySleep, nil)
		if err != nil {
			return err
		}
		l.sleepRule = v.Rule
	}
	from, to := l.req.From, l.req.To.AddDate(0, 0, 1) // a nap after the anchor on the last date is the next night's
	rows, err := l.q.ResolveSleepSessions(ctx, dbq.ResolveSleepSessionsParams{UserID: l.req.UserID, FromDate: from.AddDate(0, 0, -1), ToDate: to})
	if err != nil {
		return db.MapErr(err)
	}
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	stages, err := l.q.ResolveSleepStages(ctx, ids)
	if err != nil {
		return db.MapErr(err)
	}
	bySession := map[uuid.UUID][]normalize.SleepStage{}
	for _, st := range stages {
		bySession[st.SessionID] = append(bySession[st.SessionID], normalize.SleepStage{Stage: st.Stage, Start: st.StartAt, End: st.EndAt})
	}
	l.sleepIn = make([]SleepInput, len(rows))
	for i, row := range rows {
		l.sleepIn[i] = SleepInput{ID: row.ID, Start: row.StartAt, End: row.EndAt, Zone: normalize.Zone{OffsetMin: row.TzOffsetMin},
			IsNap: row.IsNap, HasStages: row.HasStages, Stages: bySession[row.ID],
			Source: sourceOf(row.Provider, row.ConnectionID, row.DeviceID, row.DeviceType, row.DeviceModel, row.OriginKey, row.OriginName, row.Relayed, 0),
			Totals: normalize.SleepTotals{Asleep: row.AsleepS, Deep: row.DeepS, Light: row.LightS, REM: row.RemS, Awake: row.AwakeS, Latency: row.LatencyS}}
	}
	l.nights = map[time.Time]SleepAlignment{}
	for d := from.AddDate(0, 0, -1); !d.After(to); d = d.AddDate(0, 0, 1) {
		a, err := l.sleepRule.AlignSleep(d, l.sleepIn, l.tl)
		if err != nil {
			return err
		}
		l.nights[d] = a
	}
	return nil
}

// loadSeries loads the active rows of codes that start in [from, to).
func (l *loader) loadSeries(ctx context.Context, codes []string, from, to time.Time) (Series, error) {
	rows, err := l.q.ResolveMeasurements(ctx, dbq.ResolveMeasurementsParams{UserID: l.req.UserID, Metrics: codes, FromAt: from, ToAt: to})
	if err != nil {
		return nil, db.MapErr(err)
	}
	s := Series{}
	for _, code := range codes {
		s[code] = nil
	}
	for _, row := range rows {
		in := Input{ID: row.ID, Kind: catalog.Kind(row.Kind), Start: row.StartAt, LocalDate: row.LocalDate,
			Value: row.Value, Flags: normalize.Flags(row.QualityFlags), GroupID: row.GroupID,
			Source: sourceOf(row.Provider, row.ConnectionID, row.DeviceID, row.DeviceType, row.DeviceModel, row.OriginKey, row.OriginName, row.Relayed,
				normalize.Flags(row.QualityFlags))}
		if row.EndAt != nil {
			in.End = *row.EndAt
		}
		s[row.Metric] = append(s[row.Metric], in)
	}
	return s, nil
}

// loadWorkouts loads the active workouts overlapping [from, to), without segments.
func (l *loader) loadWorkouts(ctx context.Context, from, to time.Time) ([]WorkoutInput, error) {
	rows, err := l.q.ResolveWorkouts(ctx, dbq.ResolveWorkoutsParams{UserID: l.req.UserID, StartsFrom: from.Add(-24 * time.Hour), ToAt: to, FromAt: from})
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := make([]WorkoutInput, len(rows))
	for i, row := range rows {
		out[i] = WorkoutInput{ID: row.ID, Start: row.StartAt, End: row.EndAt, Sport: row.Sport, DistanceM: row.DistanceM,
			EnergyKcal: row.EnergyKcal, AvgHRBpm: row.AvgHrBpm, MaxHRBpm: row.MaxHrBpm,
			Source: sourceOf(row.Provider, row.ConnectionID, row.DeviceID, row.DeviceType, row.DeviceModel, row.OriginKey, row.OriginName, row.Relayed, 0)}
	}
	return out, nil
}

// sourceOf builds the selector identity of a row; manual means provider manual or the
// manual_entry quality flag.
func sourceOf(provider string, conn uuid.UUID, device *uuid.UUID, deviceType, model, originKey, originName string, relayed bool, flags normalize.Flags) Source {
	s := Source{Provider: provider, ConnectionID: conn, DeviceType: deviceType, DeviceModel: model, OriginKey: originKey,
		OriginName: originName, Relayed: relayed, Manual: provider == "manual" || flags&normalize.FlagManualEntry != 0}
	if device != nil {
		s.DeviceID = *device
	}
	return s
}
