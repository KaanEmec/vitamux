package resolve

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
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
	// Live skips resolved_cache (verify, benchmarks). A draft Rule and Sources always do.
	Live bool
}

// Run resolves req.Metric for every window of req.Kind ("" for the rule's) on the local dates
// req.From through req.To, with the rule in effect and the active overrides. It only reads:
// rule, timezone periods, overrides, the metric's rows (and its wear series), sleep sessions and workouts for
// contexts and night windows, and the follow leader's results; closed dates come from and go to
// resolved_cache (cache.go).
func Run(ctx context.Context, d *db.DB, req Request) ([]Result, error) {
	days, err := run(ctx, d, req)
	if err != nil {
		return nil, err
	}
	var out []Result
	for _, day := range days {
		out = append(out, day...)
	}
	return out, nil
}

// compute resolves req (normalized by run) live, one slice of results per local date.
func compute(ctx context.Context, d *db.DB, req Request, v Version, ovs []Override) ([][]Result, error) {
	l := &loader{d: d, q: d.Q(), req: req, rule: v.Rule}
	var err error
	if l.tl, err = timeline(ctx, d, req.UserID); err != nil {
		return nil, err
	}
	if RuleMetric(req.Metric) == FamilySleep {
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
	srcs      map[srcIDs]dbq.ResolveSourceIdentitiesRow // identities loadSeries looked up
	ids       map[string]int16                          // metric_catalog ids by code
	zone      zoneSpan                                  // loader.local's last period
}

// srcIDs is the (provider, device, origin) of a row; uuid.Nil stands for none.
type srcIDs struct {
	provider       int16
	device, origin uuid.UUID
}

// results resolves a non-sleep metric, one local date at a time. Each date sees exactly the
// rows a request for that date alone loads (the date padded by a day on both sides, a year back
// for latest windows, WearLookback more for the wear series), so its results do not depend on
// the requested range and the cache (cache.go) can store them per date. A selection-only metric
// also resolves the day before the range, whose last window is the first one's previous window
// for definition_changed.
func (l *loader) results(ctx context.Context, v Version, ovs []Override) ([][]Result, error) {
	r, req := v.Rule, l.req
	sp, err := r.spec()
	if err != nil {
		return nil, err
	}
	if r.selectionOnly() {
		l.req.From = req.From.AddDate(0, 0, -1)
	}
	needSleep := req.Kind == catalog.WindowLocalNight || req.Kind == catalog.WindowSleepEpisode || len(r.Contexts[ContextSleep]) > 0
	if needSleep {
		if err := l.loadSleep(ctx); err != nil {
			return nil, err
		}
	}
	first, err := LocalDay(l.req.From, l.tl)
	if err != nil {
		return nil, err
	}
	last, err := LocalDay(req.To, l.tl)
	if err != nil {
		return nil, err
	}
	back := 24 * time.Hour // nights start the evening before
	if req.Kind == catalog.WindowLatest {
		back = LatestLookback
	}
	end := last.End.Add(24 * time.Hour)
	rows := &slider{end: end, load: func(ctx context.Context, from, to time.Time) (Series, error) {
		return l.loadSeries(ctx, sp.codes, from, to)
	}}
	wearCode, wear := "", (*slider)(nil)
	if q := r.Quality; q != nil && q.RequireWear != "" && !slices.Contains(sp.codes, q.RequireWear) {
		wearCode = q.RequireWear
		wear = &slider{end: end, load: func(ctx context.Context, from, to time.Time) (Series, error) {
			in, err := l.loadWear(ctx, r, wearCode, from, to)
			return Series{wearCode: in}, err
		}}
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
		lead := l.req
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

	var out [][]Result
	for d := l.req.From; !d.After(req.To); d = d.AddDate(0, 0, 1) {
		day, err := LocalDay(d, l.tl)
		if err != nil {
			return nil, err
		}
		lo, hi := day.Start.Add(-back), day.End.Add(24*time.Hour)
		sub, err := rows.advance(ctx, lo, hi)
		if err != nil {
			return nil, err
		}
		if wearCode != "" {
			w, err := wear.advance(ctx, lo.Add(-WearLookback), hi)
			if err != nil {
				return nil, err
			}
			sub[wearCode] = w[wearCode]
		}
		ws, err := l.windows(r, sub, sp, d)
		if err != nil {
			return nil, err
		}
		resolved, err := r.ResolveWindowsOverridden(ws, sub, opt, ovs)
		if err != nil {
			return nil, err
		}
		opt.Previous = ""
		if n := len(resolved); n > 0 {
			opt.Previous = resolved[n-1].Selected
		}
		if d.Before(req.From) {
			continue
		}
		rs := make([]Result, len(resolved))
		for i, res := range resolved {
			rs[i] = BuildResult(req.Metric, v, res, req.Now)
			if req.Sources {
				if rs[i].Sources, err = BuildSources(r, res.Window, sub, req.Now); err != nil {
					return nil, err
				}
			}
		}
		out = append(out, rs)
	}
	return out, nil
}

// slider holds the rows of a series while a request walks its dates forward: it loads ahead in
// spans sized to about sliderRows rows and drops the rows before the current date's, so a request
// holds a few days of dense rows (a year back for latest windows) instead of its whole range.
// Consecutive loads concatenate to what one load of the union returns, so results do not change.
type slider struct {
	load  func(ctx context.Context, from, to time.Time) (Series, error)
	end   time.Time // never load at or past end: the last date's hi
	s     Series
	hi    time.Time     // rows starting before hi are loaded
	ahead time.Duration // how far past the asked hi a load reaches; adapts to the row density
}

const sliderRows = 50_000 // rows per load the slider aims at (about 25 MiB as Input)

// advance returns the rows that start in [lo, hi); lo and hi never decrease between calls.
func (w *slider) advance(ctx context.Context, lo, hi time.Time) (Series, error) {
	if w.s == nil || lo.After(w.hi) {
		w.hi = lo
	}
	if hi.After(w.hi) {
		to := hi.Add(w.ahead)
		if to.After(w.end) {
			to = timeMax(hi, w.end)
		}
		more, err := w.load(ctx, w.hi, to)
		if err != nil {
			return nil, err
		}
		if w.s == nil {
			w.s = Series{}
		}
		n := 0
		for code, in := range more {
			w.s[code] = append(w.s[code], in...)
			n += len(in)
		}
		w.hi = to
		switch {
		case n < sliderRows/2:
			w.ahead = max(2*w.ahead, 24*time.Hour)
		case n > 2*sliderRows:
			w.ahead /= 2
		}
	}
	for code, in := range w.s { // the dropped head is freed when append next moves the slice
		i, _ := slices.BinarySearchFunc(in, lo, func(x Input, t time.Time) int { return x.Start.Compare(t) })
		w.s[code] = in[i:]
	}
	return w.s.between(lo, hi), nil
}

// between returns the rows of s that start in [lo, hi); s holds rows in start order.
func (s Series) between(lo, hi time.Time) Series {
	out := make(Series, len(s))
	for code, in := range s {
		out[code] = between(in, lo, hi)
	}
	return out
}

func between(in []Input, lo, hi time.Time) []Input {
	i, _ := slices.BinarySearchFunc(in, lo, func(x Input, t time.Time) int { return x.Start.Compare(t) })
	j, _ := slices.BinarySearchFunc(in, hi, func(x Input, t time.Time) int { return x.Start.Compare(t) })
	return in[i:j:j]
}

// windows builds the windows of the requested kind on local date d; s holds that date's rows.
func (l *loader) windows(r *Rule, s Series, sp spec, d time.Time) ([]Window, error) {
	req := l.req
	switch req.Kind {
	case catalog.WindowReading:
		var in []Input
		for _, code := range sp.codes {
			for _, x := range s[code] {
				if x.LocalDate.Equal(d) {
					in = append(in, x)
				}
			}
		}
		return ReadingWindows(in), nil
	case catalog.WindowLatest:
		day, err := LocalDay(d, l.tl)
		if err != nil {
			return nil, err
		}
		return []Window{LatestWindow(timeMin(day.End, req.Now))}, nil
	case catalog.WindowSleepEpisode:
		var out []Window
		for _, e := range l.nights[d].Episodes {
			out = append(out, EpisodeWindow(e))
		}
		return out, nil
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
		return []Window{w}, nil
	case catalog.WindowBucket, catalog.WindowHour, catalog.WindowLocalDay:
	}
	rw := RuleWindow{Kind: req.Kind}
	if req.Kind == catalog.WindowBucket {
		rw.Size = r.Window.Size
		if rw.Size == "" {
			rw.Size = "5m"
		}
	}
	return DayWindows(rw, d, l.tl, r.NightAnchor())
}

// sleepResults resolves the sleep family or one sleep code: one result per night (local_night,
// the main episode) or per episode (sleep_episode). Overrides of the sleep family can force a
// group; other actions are listed as ignored.
func (l *loader) sleepResults(ctx context.Context, v Version, ovs []Override) ([][]Result, error) {
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
	var out [][]Result
	for d := req.From; !d.After(req.To); d = d.AddDate(0, 0, 1) {
		a := l.nights[d]
		var day []Result
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
			day = append(day, r)
		}
		out = append(out, day)
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
		bySession[st.SessionID] = append(bySession[st.SessionID], normalize.SleepStage{Stage: st.Stage, Start: l.local(st.StartAt), End: l.local(st.EndAt)})
	}
	l.sleepIn = make([]SleepInput, len(rows))
	for i, row := range rows {
		l.sleepIn[i] = SleepInput{ID: row.ID, Start: l.local(row.StartAt), End: l.local(row.EndAt), Zone: normalize.Zone{OffsetMin: row.TzOffsetMin},
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
	ids, err := l.metricIDs(ctx, codes)
	if err != nil {
		return nil, err
	}
	rows, err := l.q.ResolveMeasurements(ctx, dbq.ResolveMeasurementsParams{UserID: l.req.UserID, MetricIds: ids, FromAt: from, ToAt: to})
	if err != nil {
		return nil, db.MapErr(err)
	}
	// Rows carry source ids; their identities are looked up once per distinct triple and Run.
	if l.srcs == nil {
		l.srcs = map[srcIDs]dbq.ResolveSourceIdentitiesRow{}
	}
	keys := make([]srcIDs, len(rows))
	var p dbq.ResolveSourceIdentitiesParams
	var missing []srcIDs
	for i, row := range rows {
		k := srcIDs{provider: row.ProviderID}
		if row.DeviceID != nil {
			k.device = *row.DeviceID
		}
		if row.OriginID != nil {
			k.origin = *row.OriginID
		}
		if _, ok := l.srcs[k]; !ok {
			l.srcs[k] = dbq.ResolveSourceIdentitiesRow{}
			missing = append(missing, k)
			p.ProviderIds, p.DeviceIds, p.OriginIds = append(p.ProviderIds, k.provider), append(p.DeviceIds, k.device), append(p.OriginIds, k.origin)
		}
		keys[i] = k
	}
	if len(missing) > 0 {
		found, err := l.q.ResolveSourceIdentities(ctx, p)
		if err != nil {
			return nil, db.MapErr(err)
		}
		for _, f := range found {
			l.srcs[missing[f.I-1]] = f
		}
	}
	s := Series{}
	for _, code := range codes {
		s[code] = nil
	}
	for i, row := range rows {
		f := l.srcs[keys[i]]
		flags := normalize.Flags(row.QualityFlags)
		in := Input{ID: row.ID, Kind: catalog.Kind(row.Kind), Start: l.local(row.StartAt), LocalDate: row.LocalDate,
			Value: row.Value, Flags: flags, GroupID: row.GroupID,
			Source: sourceOf(f.Provider, row.ConnectionID, row.DeviceID, f.DeviceType, f.DeviceModel, f.OriginKey, f.OriginName, f.Relayed, flags)}
		if row.EndAt != nil {
			in.End = l.local(*row.EndAt)
		}
		code := codes[slices.Index(ids, row.MetricID)]
		s[code] = append(s[code], in)
	}
	return s, nil
}

// metricIDs returns the metric_catalog ids of codes, in order; an unknown code gets -1, which
// matches no row.
func (l *loader) metricIDs(ctx context.Context, codes []string) ([]int16, error) {
	if l.ids == nil {
		l.ids = map[string]int16{}
	}
	var missing []string
	for _, c := range codes {
		if _, ok := l.ids[c]; !ok {
			missing = append(missing, c)
			l.ids[c] = -1
		}
	}
	if len(missing) > 0 {
		rows, err := l.q.ResolveMetricIDs(ctx, missing)
		if err != nil {
			return nil, db.MapErr(err)
		}
		for _, r := range rows {
			l.ids[r.Code] = r.ID
		}
	}
	out := make([]int16, len(codes))
	for i, c := range codes {
		out[i] = l.ids[c]
	}
	return out, nil
}

// loadWear loads the wear series of quality.require_wear from from to to. Where every base bucket
// is a UTC-aligned 5 minutes, the series is one row per source and 5-minute bucket
// (ResolveWearBuckets), which gates exactly like the rows it stands for and is far smaller for
// dense heart rate; shorter windows (small buckets, short episodes) read the rows.
func (l *loader) loadWear(ctx context.Context, r *Rule, code string, from, to time.Time) ([]Input, error) {
	short := l.req.Kind == catalog.WindowSleepEpisode || l.req.Kind == catalog.WindowLocalNight ||
		(l.req.Kind == catalog.WindowBucket && r.Window.Size != "" && r.Window.Size.Std() < 5*time.Minute)
	if short {
		s, err := l.loadSeries(ctx, []string{code}, from, to)
		return s[code], err
	}
	rows, err := l.q.ResolveWearBuckets(ctx, dbq.ResolveWearBucketsParams{UserID: l.req.UserID, Metric: code, FromAt: from, ToAt: to,
		NotWear: int32(normalize.FlagManualEntry | normalize.FlagImplausible)})
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := make([]Input, len(rows))
	for i, row := range rows {
		out[i] = Input{Kind: catalog.Sample, Start: l.local(row.Bucket),
			Source: sourceOf(row.Provider, row.ConnectionID, row.DeviceID, row.DeviceType, row.DeviceModel, row.OriginKey, row.OriginName, row.Relayed, 0)}
	}
	return out, nil
}

// loadWorkouts loads the active workouts overlapping [from, to), without segments.
func (l *loader) loadWorkouts(ctx context.Context, from, to time.Time) ([]WorkoutInput, error) {
	rows, err := l.q.ResolveWorkouts(ctx, dbq.ResolveWorkoutsParams{UserID: l.req.UserID, StartsFrom: from.Add(-24 * time.Hour), ToAt: to, FromAt: from})
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := make([]WorkoutInput, len(rows))
	for i, row := range rows {
		out[i] = WorkoutInput{ID: row.ID, Start: l.local(row.StartAt), End: l.local(row.EndAt), Sport: row.Sport, DistanceM: row.DistanceM,
			EnergyKcal: row.EnergyKcal, AvgHRBpm: row.AvgHrBpm, MaxHRBpm: row.MaxHrBpm,
			Source: sourceOf(row.Provider, row.ConnectionID, row.DeviceID, row.DeviceType, row.DeviceModel, row.OriginKey, row.OriginName, row.Relayed, 0)}
	}
	return out, nil
}

// local returns the scanned instant t in the owner's zone at t (UTC without a timeline): pgx
// scans timestamptz in the process zone, and windows and explanations built from loaded times
// must not depend on it.
func (l *loader) local(t time.Time) time.Time {
	z := &l.zone
	if z.loc == nil || t.Before(z.from) || !t.Before(z.to) {
		*z = zoneSpan{loc: time.UTC, from: minTime, to: maxTime}
		i := sort.Search(len(l.tl), func(i int) bool { return l.tl[i].ValidFrom.After(t) })
		if i > 0 { // the first period also covers everything before it
			z.from = l.tl[i-1].ValidFrom
		}
		if i < len(l.tl) {
			z.to = l.tl[i].ValidFrom
		}
		if name, ok := l.tl.At(t); ok {
			if loc, err := loadLocation(name); err == nil {
				z.loc = loc
			}
		}
	}
	return t.In(z.loc)
}

// zoneSpan is the zone of the timeline period last used by loader.local, from through to.
type zoneSpan struct {
	loc      *time.Location
	from, to time.Time
}

var minTime, maxTime = time.Unix(-1<<62, 0), time.Unix(1<<62, 0)

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

// timeline returns the owner's timezone periods.
func timeline(ctx context.Context, d *db.DB, userID uuid.UUID) (normalize.Timeline, error) {
	return normalize.NewPeriods(d).Timeline(ctx, userID)
}
