package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/normalize"
	"github.com/KaanEmec/vitamux/internal/resolve"
)

// Catalogue and resolved endpoints (J10.3; docs/architecture/api.md, resolution.md#result-shape).
// Every value comes from internal/resolve: daily values read and fill resolved_cache, the
// drilldown, sleep members and previews resolve live, and a preview never writes anything (its
// draft rule and the rule in effect both run with Request.Live).
func (rt *router) resolvedRoutes() {
	read := scope(auth.ReadHealth)
	rt.handle("GET /api/v1/metrics", read, rt.ops.ListMetrics)
	rt.handle("GET /api/v1/metrics/{code}", read, rt.ops.GetMetric)
	rt.handle("GET /api/v1/resolved/daily", read, joinRepeated("metrics", rt.ops.GetResolvedDaily))
	rt.handle("GET /api/v1/resolved/series", read, rt.ops.GetResolvedSeries)
	rt.handle("GET /api/v1/resolved/sleep", read, rt.ops.GetResolvedSleep)
	rt.handle("GET /api/v1/resolved/workouts", read, rt.ops.GetResolvedWorkouts)
	rt.handle("GET /api/v1/resolved/{metric}/{window_key}/sources", read, rt.ops.GetResolvedSources)
	rt.handle("POST /api/v1/resolution/preview", read, rt.ops.PreviewResolution)
}

const (
	maxResolvedDays = 366 // dates per daily, sleep, workouts, preview and series request
	maxRecordRefs   = 100 // inputs list their rows up to this many; the drilldown links the rest
	// workoutRule is the rule whose groups pick a workout from each cluster: workouts have no
	// rule family yet (ADR-0008), and E1 workout contexts already rank recording devices by it.
	workoutRule = "heart_rate"
)

// joinRepeated folds a repeated query parameter into the comma-separated form the spec declares
// (explode: false), so metrics=a&metrics=b works like metrics=a,b.
func joinRepeated(name string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); len(q[name]) > 1 {
			q.Set(name, strings.Join(q[name], ","))
			r = r.Clone(r.Context())
			r.URL.RawQuery = q.Encode()
		}
		h(w, r)
	}
}

// ---- catalogue

// opOf maps catalogue strategies to the rule ops a spec names (resolve validates the same pairs).
var opOf = map[catalog.Strategy]resolve.Op{
	catalog.SingleSource: resolve.OpSingleSource, catalog.FirstAvailable: resolve.OpFirstAvailable,
	catalog.Mean: resolve.OpMean, catalog.Min: resolve.OpMin, catalog.Max: resolve.OpMax, catalog.Sum: resolve.OpSum,
	catalog.LatestOf: resolve.OpLatest, catalog.Earliest: resolve.OpEarliest, catalog.EventPriority: resolve.OpEventPriority,
}

func metricBody(m catalog.Metric) oapi.Metric {
	out := oapi.Metric{Code: m.Code, Section: m.Section, Unit: m.Unit, Aggregation: oapi.MetricAggregation(m.Agg),
		Kinds: []oapi.MetricKinds{}, PlausibleRange: []float64{m.Min, m.Max}, ProviderScoped: m.ProviderScoped,
		SelectionOnly: m.SelectionOnly, Group: optString(m.Group), DerivedFrom: optString(m.DerivedFrom)}
	for _, k := range m.Kinds {
		out.Kinds = append(out.Kinds, oapi.MetricKinds(k))
	}
	for _, w := range m.Windows() {
		out.Windows = append(out.Windows, oapi.MetricWindows(w))
	}
	for _, s := range m.Strategies() {
		out.Strategies = append(out.Strategies, oapi.MetricStrategies(opOf[s]))
	}
	if f := resolve.RuleMetric(m.Code); f != m.Code {
		out.Family = &f
	}
	return out
}

func (o *owner) ListMetrics(context.Context, oapi.ListMetricsRequestObject) (oapi.ListMetricsResponseObject, error) {
	ms := catalog.Metrics()
	out := oapi.ListMetrics200JSONResponse{Metrics: make([]oapi.Metric, len(ms))}
	for i, m := range ms {
		out.Metrics[i] = metricBody(m)
	}
	return out, nil
}

func (o *owner) GetMetric(_ context.Context, req oapi.GetMetricRequestObject) (oapi.GetMetricResponseObject, error) {
	m, ok := catalog.Lookup(req.Code)
	if !ok {
		return nil, problemErr(CodeNotFound, "no such metric")
	}
	return oapi.GetMetric200JSONResponse(metricBody(m)), nil
}

// ---- shared

// resolvable reports whether metric is a catalogue code or a rule family.
func resolvable(metric string) bool {
	_, ok := catalog.Lookup(metric)
	return ok || metric == resolve.FamilySleep || metric == resolve.FamilyBloodPressure
}

// allowsWindow reports whether metric (a code or family) may resolve windows of kind.
func allowsWindow(metric string, kind catalog.Window) bool {
	switch metric {
	case resolve.FamilySleep:
		return kind == catalog.WindowLocalNight || kind == catalog.WindowSleepEpisode
	case resolve.FamilyBloodPressure:
		metric = "bp_systolic"
	}
	m, ok := catalog.Lookup(metric)
	return ok && m.AllowsWindow(kind)
}

// dailyKind is the one-window-per-date kind daily values and previews use: the rule's local_day
// or local_night window, else local_night for night-only metrics and local_day otherwise.
func dailyKind(metric string, r *resolve.Rule) catalog.Window {
	if k := r.Window.Kind; k == catalog.WindowLocalDay || k == catalog.WindowLocalNight {
		return k
	}
	if !allowsWindow(metric, catalog.WindowLocalDay) {
		return catalog.WindowLocalNight
	}
	return catalog.WindowLocalDay
}

// drillKind is the drilldown's default kind for a window key: dailyKind for a date, else the
// rule's bucket or sleep_episode window, sleep_episode for the sleep family, and hour.
func drillKind(metric string, r *resolve.Rule, dateKey bool) catalog.Window {
	switch {
	case dateKey:
		return dailyKind(metric, r)
	case r.Window.Kind == catalog.WindowBucket || r.Window.Kind == catalog.WindowSleepEpisode:
		return r.Window.Kind
	case !allowsWindow(metric, catalog.WindowHour):
		return catalog.WindowSleepEpisode
	}
	return catalog.WindowHour
}

var drillKinds = []catalog.Window{catalog.WindowLocalDay, catalog.WindowLocalNight, catalog.WindowHour, catalog.WindowBucket, catalog.WindowSleepEpisode}

// sourcesLink is the drilldown URL of a window, with ?window= when the key alone would pick
// another kind; "" for kinds the drilldown does not serve (latest, reading).
func sourcesLink(metric string, r *resolve.Rule, w resolve.Window) string {
	if !slices.Contains(drillKinds, w.Kind) {
		return ""
	}
	dateKey := w.Kind == catalog.WindowLocalDay || w.Kind == catalog.WindowLocalNight
	link := "/api/v1/resolved/" + url.PathEscape(metric) + "/" + url.PathEscape(w.Key) + "/sources"
	if drillKind(metric, r, dateKey) != w.Kind {
		link += "?window=" + string(w.Kind)
	}
	return link
}

// ruleFor returns the rule in effect for metric's rule (its family for family codes), with
// ok false when the metric has none.
func (o *owner) ruleFor(ctx context.Context, metric string) (resolve.Version, bool, error) {
	v, err := resolve.NewStore(o.opts.DB).Active(ctx, auth.PrincipalFrom(ctx).UserID, resolve.RuleMetric(metric))
	if errors.Is(err, db.ErrNotFound) {
		return v, false, nil
	}
	return v, err == nil, err
}

// noRule is the value of a metric without a rule in effect.
func noRule(metric string) oapi.ResolvedValue {
	return oapi.ResolvedValue{Status: oapi.ResolvedValueStatus(resolve.ResultNoData),
		Explanation: "No rule is in effect for " + metric + "; only the all-sources view shows its data until a source is picked."}
}

// dateRange checks an inclusive range of local dates.
func dateRange(start, end openapi_types.Date) (time.Time, time.Time, error) {
	from, to := start.Time, end.Time
	switch {
	case to.Before(from):
		return from, to, problemErr(CodeValidationFailed, "invalid range", FieldError{Pointer: "/end_date", Detail: "must not be before start_date"})
	case int(to.Sub(from).Hours()/24) >= maxResolvedDays:
		return from, to, problemErr(CodeValidationFailed, "invalid range", FieldError{Pointer: "/end_date", Detail: "at most 366 dates"})
	}
	return from, to, nil
}

// zones renders instants in the owner's timezone in effect at each.
type zones struct {
	tl   normalize.Timeline
	locs map[string]*time.Location
}

func (o *owner) zones(ctx context.Context) (*zones, error) {
	tl, err := normalize.NewPeriods(o.opts.DB).Timeline(ctx, auth.PrincipalFrom(ctx).UserID)
	if err != nil {
		return nil, err
	}
	if len(tl) == 0 {
		return nil, errNoTimezone
	}
	return &zones{tl: tl, locs: map[string]*time.Location{}}, nil
}

var errNoTimezone = problemErr(CodeValidationFailed, "no timezone period is configured; add one in the settings")

func (z *zones) name(t time.Time) string {
	n, _ := z.tl.At(t)
	return n
}

func (z *zones) at(t time.Time) time.Time {
	n := z.name(t)
	loc, ok := z.locs[n]
	if !ok {
		var err error
		if loc, err = time.LoadLocation(n); err != nil {
			loc = time.UTC
		}
		z.locs[n] = loc
	}
	return t.In(loc)
}

func (z *zones) ptr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	l := z.at(t)
	return &l
}

// localDate is the owner's calendar date of t.
func (z *zones) localDate(t time.Time) (time.Time, error) {
	l, err := normalize.LocalDate(t, normalize.Zone{}, z.tl)
	return l.Date, err
}

// resolveErr maps resolution errors a request can cause.
func resolveErr(err error) error {
	if errors.Is(err, normalize.ErrNoTimezone) {
		return errNoTimezone
	}
	return err
}

func ruleRef(v resolve.Version, op resolve.Op) oapi.RuleRef {
	out := oapi.RuleRef{Ref: v.Ref, Version: v.Version}
	if op != "" {
		s := string(op)
		out.Strategy = &s
	}
	return out
}

// valueOf is a value or a family's components, without floating-point noise (num).
func valueOf(v *float64, components map[string]float64) any {
	switch {
	case components != nil:
		return nums(components)
	case v != nil:
		return num(*v)
	}
	return nil
}

// num rounds away the floating-point noise of pro-rating and averaging (6114.000000000002) at a
// millionth, far below any unit's resolution.
func num(f float64) float64 { return math.Round(f*1e6) / 1e6 }

func nums(m map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = num(v)
	}
	return out
}

func round3(f float64) *float64 {
	r := math.Round(f*1000) / 1000
	return &r
}

func windowBody(z *zones, w resolve.Window) *oapi.Window {
	out := &oapi.Window{Kind: string(w.Kind), Start: z.ptr(w.Start), End: z.ptr(w.End), Key: optString(w.Key)}
	if !w.Date.IsZero() {
		d := apiDate(w.Date)
		out.LocalDate = &d
	}
	return out
}

func sourceBody(s resolve.Source) oapi.ResolvedSource {
	out := oapi.ResolvedSource{Provider: s.Provider, Device: deviceRef(s), Origin: originRef(s, "")}
	if s.ConnectionID != uuid.Nil {
		c := ingest.FormatConnectionID(s.ConnectionID)
		out.ConnectionID = &c
	}
	if s.Manual {
		out.Manual = &s.Manual
	}
	return out
}

func deviceRef(s resolve.Source) *oapi.DeviceRef {
	if s.DeviceID == uuid.Nil && s.DeviceType == "" {
		return nil
	}
	d := &oapi.DeviceRef{Type: optString(s.DeviceType), Model: optString(s.DeviceModel)}
	if s.DeviceID != uuid.Nil {
		id := formatDeviceID(s.DeviceID)
		d.ID = &id
	}
	return d
}

func originRef(s resolve.Source, relayedProvider string) *oapi.OriginRef {
	if s.OriginKey == "" && s.OriginName == "" {
		return nil
	}
	o := &oapi.OriginRef{Key: optString(s.OriginKey), Name: optString(s.OriginName), RelayedProvider: optString(relayedProvider)}
	if s.Relayed {
		o.Relayed = &s.Relayed
	}
	return o
}

func inputBody(z *zones, in resolve.ResultInput) oapi.ResolvedInput {
	out := oapi.ResolvedInput{Group: optString(in.Group), Status: string(in.Status), Basis: optString(string(in.Basis)),
		Reason: optString(in.Reason), At: z.ptr(in.At)}
	if in.Status == resolve.StatusUsed {
		out.Selected = &in.Selected
	}
	// Excluded and unmatched sources are listed, never aggregated: they have no value.
	if in.Status != resolve.StatusExcluded && in.Status != resolve.StatusNotInRule {
		if out.Value = valueOf(in.Value, in.Components); out.Value != nil {
			out.Coverage = round3(in.Coverage)
		}
	}
	if in.Count > 0 {
		out.Count = &in.Count
	}
	if in.Readings > 0 {
		out.Readings = &in.Readings
	}
	if in.Prorated {
		out.Prorated = &in.Prorated
	}
	if !in.SpanStart.IsZero() {
		out.Span = &oapi.Span{Start: z.at(in.SpanStart), End: z.at(in.SpanEnd)}
	}
	if in.WearExempt {
		out.WearExempt = &in.WearExempt
	}
	if in.WornBuckets > 0 {
		out.WornBuckets = &in.WornBuckets
	}
	if len(in.Sources) > 0 {
		srcs := make([]oapi.ResolvedSource, len(in.Sources))
		for i, s := range in.Sources {
			srcs[i] = sourceBody(s)
		}
		out.Sources = &srcs
	}
	if n := len(in.RecordRefs); n > 0 && n <= maxRecordRefs {
		refs := make([]string, n)
		for i, id := range in.RecordRefs {
			refs[i] = strconv.FormatInt(id, 10)
		}
		out.RecordRefs = &refs
	}
	if len(in.Sessions) > 0 {
		out.SessionRefs = &in.Sessions
	}
	return out
}

// resolvedValue renders one result in the documented shape; r is the rule behind it, for the
// drilldown link.
func resolvedValue(z *zones, res resolve.Result, r *resolve.Rule) oapi.ResolvedValue {
	out := oapi.ResolvedValue{Status: oapi.ResolvedValueStatus(res.Status), Explanation: res.Explanation,
		Value: valueOf(res.Value, res.Components), Unit: optString(res.Unit), Window: windowBody(z, res.Window),
		Selected: optString(res.Selected), Context: optString(string(res.Context)), ComputedAt: z.ptr(res.ComputedAt)}
	rule := ruleRef(resolve.Version{Ref: res.Rule.Ref, Version: res.Rule.Version}, res.Rule.Strategy)
	out.Rule = &rule
	if res.Partial {
		out.Partial = &res.Partial
	}
	if res.Status != resolve.ResultNoData {
		out.Coverage = round3(res.Coverage)
	}
	if len(res.Missing) > 0 {
		m := make(map[string]string, len(res.Missing))
		for code, s := range res.Missing {
			m[code] = string(s)
		}
		out.Missing = &m
	}
	inputs := make([]oapi.ResolvedInput, len(res.Inputs))
	for i, in := range res.Inputs {
		inputs[i] = inputBody(z, in)
	}
	out.Inputs = &inputs
	if len(res.Warnings) > 0 {
		ws := make([]oapi.ResolvedWarning, len(res.Warnings))
		for i, w := range res.Warnings {
			ws[i] = oapi.ResolvedWarning{Code: string(w.Code), Group: optString(w.Group)}
		}
		out.Warnings = &ws
	}
	if res.Follow != "" {
		out.Follow = &oapi.ResolvedFollow{Metric: res.Follow, Group: optString(res.FollowGroup)}
	}
	if len(res.Hours) > 0 {
		hs := make([]oapi.HourPick, len(res.Hours))
		for i, h := range res.Hours {
			hs[i] = oapi.HourPick{Start: z.at(h.Start), Status: string(h.Status), Group: optString(h.Group)}
			if h.Status != resolve.ResultNoData {
				hs[i].Value = &h.Value
			}
		}
		out.Hours = &hs
	}
	if len(res.Overrides)+len(res.Ignored) > 0 {
		ov := &oapi.ResolvedOverrides{Applied: []openapi_types.UUID{}, Ignored: []openapi_types.UUID{}}
		for _, x := range res.Overrides {
			ov.Applied = append(ov.Applied, x.ID)
		}
		for _, x := range res.Ignored {
			ov.Ignored = append(ov.Ignored, x.ID)
		}
		if c := res.Computed; c != nil {
			ov.Computed = &oapi.ComputedValue{Status: oapi.ComputedValueStatus(c.Status), Value: valueOf(c.Value, c.Components), Selected: optString(c.Selected)}
		}
		out.Overrides = ov
	}
	if link := sourcesLink(res.Metric, r, res.Window); link != "" {
		out.Links = &oapi.ResolvedLinks{Sources: &link}
	}
	return out
}

// ---- daily

func (o *owner) GetResolvedDaily(ctx context.Context, req oapi.GetResolvedDailyRequestObject) (oapi.GetResolvedDailyResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	from, to, err := dateRange(req.Params.StartDate, req.Params.EndDate)
	if err != nil {
		return nil, err
	}
	var metrics []string
	for _, m := range ptrVal(req.Params.Metrics) {
		if m = strings.TrimSpace(m); m != "" && !slices.Contains(metrics, m) {
			metrics = append(metrics, m)
		}
	}
	user := auth.PrincipalFrom(ctx).UserID
	if len(metrics) == 0 {
		set, err := resolve.NewStore(o.opts.DB).ActiveSet(ctx, user)
		if err != nil {
			return nil, err
		}
		for _, v := range set {
			metrics = append(metrics, v.Metric)
		}
	}
	for _, m := range metrics {
		if !resolvable(m) {
			return nil, problemErr(CodeValidationFailed, "unknown metric", FieldError{Pointer: "/metrics", Detail: "no such metric: " + strconv.Quote(m)})
		}
	}
	var out oapi.GetResolvedDaily200JSONResponse
	idx := map[time.Time]int{}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		idx[d] = len(out.Days)
		out.Days = append(out.Days, oapi.ResolvedDay{LocalDate: apiDate(d), Metrics: map[string]oapi.ResolvedValue{}})
	}
	z, err := o.zones(ctx)
	if errors.Is(err, errNoTimezone) {
		// A fresh install: nothing can have a local date yet, so every value is no_data (timezone "").
		for i := range out.Days {
			for _, m := range metrics {
				out.Days[i].Metrics[m] = oapi.ResolvedValue{Status: oapi.ResolvedValueStatus(resolve.ResultNoData),
					Explanation: "No timezone period is configured, so there are no local days yet; add one in the settings."}
			}
		}
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	first, err := resolve.LocalDay(from, z.tl)
	if err != nil {
		return nil, resolveErr(err)
	}
	out.Timezone = z.name(first.Start)
	now := time.Now()
	for _, m := range metrics {
		v, ok, err := o.ruleFor(ctx, m)
		if err != nil {
			return nil, err
		}
		if !ok {
			for i := range out.Days {
				out.Days[i].Metrics[m] = noRule(m)
			}
			continue
		}
		rs, err := resolve.Run(ctx, o.opts.DB, resolve.Request{UserID: user, Metric: m, Kind: dailyKind(m, v.Rule), From: from, To: to, Now: now})
		if err != nil {
			return nil, resolveErr(err)
		}
		for _, r := range rs {
			if i, ok := idx[r.Window.Date]; ok {
				out.Days[i].Metrics[m] = resolvedValue(z, r, v.Rule)
			}
		}
	}
	return out, nil
}

// ---- series

var bucketSizes = map[string]bool{"1m": true, "5m": true, "15m": true, "30m": true}

// seriesKey is a point's position: its window start (the as-of instant for latest windows).
func seriesKey(w resolve.Window) time.Time {
	if w.Kind == catalog.WindowLatest {
		return w.End
	}
	return w.Start
}

func (o *owner) GetResolvedSeries(ctx context.Context, req oapi.GetResolvedSeriesRequestObject) (oapi.GetResolvedSeriesResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	prm := req.Params
	if !resolvable(prm.Metric) {
		return nil, problemErr(CodeValidationFailed, "unknown metric", FieldError{Pointer: "/metric", Detail: "no such metric"})
	}
	if !prm.End.After(prm.Start) {
		return nil, problemErr(CodeValidationFailed, "invalid range", FieldError{Pointer: "/end", Detail: "must be after start"})
	}
	bound := prm
	bound.Limit, bound.Cursor = nil, nil
	p, err := o.newPage("resolved-series", bound, prm.Limit, prm.Cursor)
	if err != nil {
		return nil, err
	}
	var after time.Time
	if p.after != nil {
		if after, err = time.Parse(time.RFC3339Nano, p.after.Key); err != nil {
			return nil, errInvalidCursor
		}
	}
	v, ok, err := o.ruleFor(ctx, prm.Metric)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, problemErr(CodeNotFound, "no rule is in effect for "+prm.Metric)
	}
	kind, size, draft, err := seriesWindow(prm.Metric, v, ptrVal(prm.Window))
	if err != nil {
		return nil, err
	}

	z, err := o.zones(ctx)
	if err != nil {
		return nil, err
	}
	fromDate, err := z.localDate(prm.Start)
	if err != nil {
		return nil, resolveErr(err)
	}
	toDate, err := z.localDate(prm.End)
	if err != nil {
		return nil, resolveErr(err)
	}
	// Night windows start the evening before their date, latest windows end at the next midnight.
	fromDate, toDate = fromDate.AddDate(0, 0, -1), toDate.AddDate(0, 0, 1)
	if int(toDate.Sub(fromDate).Hours()/24) > maxResolvedDays+2 {
		return nil, problemErr(CodeValidationFailed, "invalid range", FieldError{Pointer: "/end", Detail: "at most 366 local dates"})
	}
	if !after.IsZero() {
		d, err := z.localDate(after)
		if err != nil {
			return nil, resolveErr(err)
		}
		fromDate = later(fromDate, d.AddDate(0, 0, -1))
	}
	perDay := 1
	switch kind {
	case catalog.WindowHour:
		perDay = 24
	case catalog.WindowBucket:
		perDay = int(24 * time.Hour / resolve.Duration(size).Std())
	default:
	}

	inRange := func(w resolve.Window) bool {
		t := seriesKey(w)
		if w.Kind == catalog.WindowLatest {
			return t.After(prm.Start) && !t.After(prm.End)
		}
		return !t.Before(prm.Start) && t.Before(prm.End)
	}
	afterCursor := func(w resolve.Window) bool {
		if p.after == nil {
			return true
		}
		t := seriesKey(w)
		return t.After(after) || t.Equal(after) && w.Key > p.after.ID
	}
	user, now := auth.PrincipalFrom(ctx).UserID, time.Now()
	var results []resolve.Result
	for d := fromDate; !d.After(toDate) && len(results) <= p.limit; {
		n := min(max((p.limit+1-len(results)+perDay-1)/perDay, 1), 31)
		e := d.AddDate(0, 0, n-1)
		if e.After(toDate) {
			e = toDate
		}
		rs, err := resolve.Run(ctx, o.opts.DB, resolve.Request{UserID: user, Metric: prm.Metric, Kind: kind, From: d, To: e, Rule: draft, Now: now})
		if err != nil {
			return nil, resolveErr(err)
		}
		for _, r := range rs {
			if inRange(r.Window) && afterCursor(r.Window) {
				results = append(results, r)
			}
		}
		d = e.AddDate(0, 0, 1)
	}
	slices.SortStableFunc(results, func(a, b resolve.Result) int {
		return cmp.Or(seriesKey(a.Window).Compare(seriesKey(b.Window)), strings.Compare(a.Window.Key, b.Window.Key))
	})
	results, more, next := trim(o, p, results, func(r resolve.Result) (time.Time, string) { return seriesKey(r.Window), r.Window.Key })

	rule := v.Rule
	if draft != nil {
		rule = draft.Rule
	}
	out := oapi.GetResolvedSeries200JSONResponse{Metric: prm.Metric, Rule: ruleRef(v, v.Rule.Strategy.Op), Timezone: z.name(prm.Start),
		Window: oapi.SeriesWindow{Kind: string(kind), Size: optString(size)}, Points: make([]oapi.ResolvedPoint, len(results)),
		SourcesUsed: resolve.SourcesUsed(results), HasMore: more, NextCursor: next}
	if out.SourcesUsed == nil {
		out.SourcesUsed = []string{}
	}
	if m, ok := catalog.Lookup(prm.Metric); ok {
		out.Unit = &m.Unit
	}
	for i, r := range results {
		out.Points[i] = resolvedPoint(z, prm.Metric, rule, r)
	}
	return out, nil
}

// seriesWindow picks a series' window: the rule's, a window kind, or a bucket size. A size other
// than the rule's resolves a copy of the rule with that bucket (draft), which skips the cache like
// every bucket window.
func seriesWindow(metric string, v resolve.Version, requested string) (catalog.Window, string, *resolve.Version, error) {
	kind, size := v.Rule.Window.Kind, string(v.Rule.Window.Size)
	if requested != "" {
		switch {
		case bucketSizes[requested]:
			kind, size = catalog.WindowBucket, requested
		case slices.Contains([]catalog.Window{catalog.WindowBucket, catalog.WindowHour, catalog.WindowLocalDay, catalog.WindowLocalNight,
			catalog.WindowSleepEpisode, catalog.WindowLatest, catalog.WindowReading}, catalog.Window(requested)):
			kind = catalog.Window(requested)
			if kind != v.Rule.Window.Kind {
				size = ""
			}
		default:
			return "", "", nil, problemErr(CodeValidationFailed, "invalid window", FieldError{Pointer: "/window", Detail: "must be a window kind or 1m, 5m, 15m, 30m"})
		}
	}
	if !allowsWindow(metric, kind) {
		return "", "", nil, problemErr(CodeUnsupportedWindow, fmt.Sprintf("window %s is not allowed for %s", kind, metric))
	}
	if kind != catalog.WindowBucket {
		return kind, size, nil, nil
	}
	if size == "" {
		size = "5m"
	}
	if string(v.Rule.Window.Size) == size && v.Rule.Window.Kind == catalog.WindowBucket {
		return kind, size, nil, nil
	}
	r := *v.Rule
	r.Window = resolve.RuleWindow{Kind: catalog.WindowBucket, Size: resolve.Duration(size)}
	cp := v
	cp.Rule = &r
	return kind, size, &cp, nil
}

// resolvedPoint is the API form of one resolved window of metric under rule.
func resolvedPoint(z *zones, metric string, rule *resolve.Rule, r resolve.Result) oapi.ResolvedPoint {
	pt := oapi.ResolvedPoint{Key: r.Window.Key, Start: z.ptr(r.Window.Start), End: z.at(r.Window.End),
		Status: oapi.ResolvedPointStatus(r.Status), Value: valueOf(r.Value, r.Components), Sources: []string{}}
	if !r.Window.Date.IsZero() {
		d := apiDate(r.Window.Date)
		pt.LocalDate = &d
	}
	if r.Partial {
		pt.Partial = &r.Partial
	}
	if r.Status != resolve.ResultNoData {
		pt.Coverage = round3(r.Coverage)
	}
	for _, in := range r.Inputs {
		if in.Selected && in.Group != "" {
			pt.Sources = append(pt.Sources, in.Group)
		}
	}
	if len(r.Warnings) > 0 {
		ws := make([]string, len(r.Warnings))
		for j, w := range r.Warnings {
			ws[j] = string(w.Code)
		}
		pt.Warnings = &ws
	}
	if link := sourcesLink(metric, rule, r.Window); link != "" {
		pt.Links = &oapi.ResolvedLinks{Sources: &link}
	}
	return pt
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// ---- sleep

func (o *owner) GetResolvedSleep(ctx context.Context, req oapi.GetResolvedSleepRequestObject) (oapi.GetResolvedSleepResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	from, to, err := dateRange(req.Params.StartDate, req.Params.EndDate)
	if err != nil {
		return nil, err
	}
	v, ok, err := o.ruleFor(ctx, resolve.FamilySleep)
	if err != nil || !ok {
		return nil, cmp.Or(err, problemErr(CodeNotFound, "no sleep rule is in effect"))
	}
	z, err := o.zones(ctx)
	if err != nil {
		return nil, err
	}
	first, err := resolve.LocalDay(from, z.tl)
	if err != nil {
		return nil, resolveErr(err)
	}
	rs, err := resolve.Run(ctx, o.opts.DB, resolve.Request{UserID: auth.PrincipalFrom(ctx).UserID, Metric: resolve.FamilySleep,
		Kind: catalog.WindowLocalNight, From: from, To: to, Sources: true, Now: time.Now()})
	if err != nil {
		return nil, resolveErr(err)
	}
	out := oapi.GetResolvedSleep200JSONResponse{Timezone: z.name(first.Start), Nights: make([]oapi.ResolvedNight, len(rs))}
	for i, r := range rs {
		n := oapi.ResolvedNight{LocalDate: apiDate(r.Window.Date), Result: resolvedValue(z, r, v.Rule), Members: make([]oapi.SleepMember, len(r.Sources))}
		for j, s := range r.Sources {
			n.Members[j] = oapi.SleepMember{Group: optString(s.Group), RuleStatus: oapi.SleepMemberRuleStatus(s.RuleStatus),
				Reason: optString(s.Reason), Selected: s.Group != "" && s.Group == r.Selected, Provider: s.Source.Provider,
				Device: deviceRef(s.Source), Origin: originRef(s.Source, ""), SessionRefs: s.Sessions}
			vals := nums(s.Values)
			n.Members[j].Values = &vals
			if s.Sessions == nil {
				n.Members[j].SessionRefs = []uuid.UUID{}
			}
			if s.Source.ConnectionID != uuid.Nil {
				c := ingest.FormatConnectionID(s.Source.ConnectionID)
				n.Members[j].ConnectionID = &c
			}
		}
		out.Nights[i] = n
	}
	return out, nil
}

// ---- workouts

func (o *owner) GetResolvedWorkouts(ctx context.Context, req oapi.GetResolvedWorkoutsRequestObject) (oapi.GetResolvedWorkoutsResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	from, to, err := dateRange(req.Params.StartDate, req.Params.EndDate)
	if err != nil {
		return nil, err
	}
	v, ok, err := o.ruleFor(ctx, workoutRule)
	if err != nil || !ok {
		return nil, cmp.Or(err, problemErr(CodeNotFound, "no "+workoutRule+" rule is in effect"))
	}
	z, err := o.zones(ctx)
	if err != nil {
		return nil, err
	}
	first, err := resolve.LocalDay(from, z.tl)
	if err != nil {
		return nil, resolveErr(err)
	}
	last, err := resolve.LocalDay(to, z.tl)
	if err != nil {
		return nil, resolveErr(err)
	}
	rows, err := o.opts.DB.Q().ResolveWorkouts(ctx, dbq.ResolveWorkoutsParams{UserID: auth.PrincipalFrom(ctx).UserID,
		StartsFrom: first.Start.Add(-24 * time.Hour), ToAt: last.End, FromAt: first.Start})
	if err != nil {
		return nil, db.MapErr(err)
	}
	ws := make([]resolve.WorkoutInput, len(rows))
	for i, row := range rows {
		src := resolve.Source{Provider: row.Provider, ConnectionID: row.ConnectionID, DeviceType: row.DeviceType, DeviceModel: row.DeviceModel,
			OriginKey: row.OriginKey, OriginName: row.OriginName, Relayed: row.Relayed, Manual: row.Provider == "manual"}
		if row.DeviceID != nil {
			src.DeviceID = *row.DeviceID
		}
		ws[i] = resolve.WorkoutInput{ID: row.ID, Source: src, Start: row.StartAt, End: row.EndAt, Sport: row.Sport,
			DistanceM: row.DistanceM, EnergyKcal: row.EnergyKcal, AvgHRBpm: row.AvgHrBpm, MaxHRBpm: row.MaxHrBpm}
	}
	out := oapi.GetResolvedWorkouts200JSONResponse{Timezone: z.name(first.Start), Rule: ruleRef(v, resolve.OpEventPriority), Workouts: []oapi.ResolvedWorkout{}}
	for _, c := range resolve.ClusterWorkouts(ws) {
		date, err := z.localDate(c.Start)
		if err != nil {
			return nil, resolveErr(err)
		}
		if date.Before(from) || date.After(to) {
			continue
		}
		out.Workouts = append(out.Workouts, workoutCluster(z, v, c, date))
	}
	return out, nil
}

func workoutCluster(z *zones, v resolve.Version, c resolve.WorkoutCluster, date time.Time) oapi.ResolvedWorkout {
	r := v.Rule
	out := oapi.ResolvedWorkout{LocalDate: apiDate(date), Start: z.at(c.Start), End: z.at(c.End), Sport: resolve.SportOther,
		Members: make([]oapi.WorkoutMember, len(c.Workouts))}
	pick, picked := r.PickWorkout(c)
	for i, w := range c.Workouts {
		if w.Sport != resolve.SportOther {
			out.Sport = w.Sport
		}
		m := oapi.WorkoutMember{ID: w.ID, RuleStatus: oapi.WorkoutMemberRuleStatus(resolve.StatusNotInRule), StartAt: z.at(w.Start), EndAt: z.at(w.End),
			Sport: w.Sport, DistanceM: w.DistanceM, EnergyKcal: w.EnergyKcal, AvgHrBpm: w.AvgHRBpm, MaxHrBpm: w.MaxHRBpm,
			Provider: w.Source.Provider, Device: deviceRef(w.Source), Origin: originRef(w.Source, ""), Selected: picked && w.ID == pick.Workout.ID}
		c := ingest.FormatConnectionID(w.Source.ConnectionID)
		m.ConnectionID = &c
		switch a := r.Assign(w.Source); a.Membership() {
		case resolve.Excluded:
			sel, _ := json.Marshal(r.Exclude[a.Exclude])
			m.RuleStatus, m.Reason = oapi.WorkoutMemberRuleStatus(resolve.StatusExcluded), optString("exclude: "+string(sel))
		case resolve.Grouped:
			m.RuleStatus, m.Group = oapi.WorkoutMemberRuleStatus(resolve.StatusUsed), &r.Groups[a.Group].ID
		case resolve.NotInRule:
		}
		out.Members[i] = m
	}
	n := len(c.Workouts)
	if !picked {
		out.Explanation = fmt.Sprintf("None of the %d recordings is from a source in the %s rule.", n, v.Metric)
		return out
	}
	id, g := pick.Workout.ID, r.Groups[pick.Group].ID
	out.Selected, out.Group = &id, &g
	if n == 1 {
		out.Explanation = fmt.Sprintf("One recording, from %s.", g)
	} else {
		out.Explanation = fmt.Sprintf("%d recordings of one activity; %s's is shown, the first group of the %s rule that recorded it.", n, g, v.Metric)
	}
	return out
}

// ---- drilldown

func (o *owner) GetResolvedSources(ctx context.Context, req oapi.GetResolvedSourcesRequestObject) (oapi.GetResolvedSourcesResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	metric := req.Metric
	if !resolvable(metric) {
		return nil, problemErr(CodeNotFound, "no such metric")
	}
	v, ok, err := o.ruleFor(ctx, metric)
	if err != nil {
		return nil, err
	}
	var draft *resolve.Version
	if !ok {
		// No rule yet: an empty rule lists every source as not_in_rule.
		r := &resolve.Rule{Schema: resolve.SchemaV1, Metric: resolve.RuleMetric(metric), Window: resolve.RuleWindow{Kind: catalog.WindowLocalDay},
			Strategy: resolve.Strategy{Op: resolve.OpFirstAvailable}}
		v = resolve.Version{Ref: "none:" + r.Metric, Metric: r.Metric, Rule: r}
		draft = &v
	}
	z, err := o.zones(ctx)
	if err != nil {
		return nil, err
	}
	var date, at time.Time
	key := req.WindowKey
	if d, err := time.Parse(time.DateOnly, key); err == nil {
		date = d
	} else if t, err := time.Parse(time.RFC3339, key); err == nil {
		at, key = t, t.UTC().Format(time.RFC3339)
		if date, err = z.localDate(t); err != nil {
			return nil, resolveErr(err)
		}
	} else {
		return nil, problemErr(CodeValidationFailed, "invalid window key", FieldError{Pointer: "/window_key", Detail: "must be a local date or an RFC 3339 instant"})
	}
	kind := drillKind(metric, v.Rule, at.IsZero())
	if w := ptrVal(req.Params.Window); w != "" {
		kind = catalog.Window(w)
	}
	dateKind := kind == catalog.WindowLocalDay || kind == catalog.WindowLocalNight
	switch {
	case !slices.Contains(drillKinds, kind):
		return nil, problemErr(CodeUnsupportedWindow, "the drilldown serves local_day, local_night, hour, bucket and sleep_episode windows")
	case !allowsWindow(metric, kind):
		return nil, problemErr(CodeUnsupportedWindow, fmt.Sprintf("window %s is not allowed for %s", kind, metric))
	case dateKind != at.IsZero():
		return nil, problemErr(CodeValidationFailed, "invalid window key", FieldError{Pointer: "/window_key", Detail: "a date for local_day and local_night, else a window start"})
	}
	to := date
	if kind == catalog.WindowSleepEpisode { // an episode belongs to the night after its start's date
		to = date.AddDate(0, 0, 1)
	}
	rs, err := resolve.Run(ctx, o.opts.DB, resolve.Request{UserID: auth.PrincipalFrom(ctx).UserID, Metric: metric, Kind: kind,
		From: date, To: to, Rule: draft, Sources: true, Now: time.Now()})
	if err != nil {
		return nil, resolveErr(err)
	}
	i := slices.IndexFunc(rs, func(r resolve.Result) bool { return r.Window.Key == key })
	if i < 0 {
		return nil, problemErr(CodeNotFound, "no such window")
	}
	res := rs[i]
	out := oapi.GetResolvedSources200JSONResponse{Metric: metric, Window: *windowBody(z, res.Window),
		Rule: ruleRef(resolve.Version{Ref: res.Rule.Ref, Version: res.Rule.Version}, res.Rule.Strategy), Sources: make([]oapi.DrilldownSource, len(res.Sources))}
	prov, err := o.drillProvenance(ctx, res.Sources)
	if err != nil {
		return nil, err
	}
	codes := rowCodes(metric)
	for i, s := range res.Sources {
		ds := oapi.DrilldownSource{Group: optString(s.Group), RuleStatus: oapi.DrilldownSourceRuleStatus(s.RuleStatus), Reason: optString(s.Reason),
			Provider: s.Source.Provider, Device: deviceRef(s.Source), Origin: originRef(s.Source, prov[i].relayed)}
		vals := nums(s.Values)
		ds.Values = &vals
		if s.Source.ConnectionID != uuid.Nil {
			c := ingest.FormatConnectionID(s.Source.ConnectionID)
			ds.ConnectionID = &c
		}
		if s.Count > 0 {
			ds.Count = &s.Count
		}
		if len(s.Sessions) > 0 {
			ds.SessionRefs = &s.Sessions
		}
		if len(codes) > 0 {
			ds.Records = &oapi.RecordsLink{Href: recordsHref(codes, s.Source, res.Window)}
		}
		if p := prov[i]; p.body != nil {
			ds.Provenance = p.body
		}
		out.Sources[i] = ds
	}
	return out, nil
}

// rowCodes are the catalogue codes whose rows a metric's drilldown lists; none for sleep, whose
// sources are sessions.
func rowCodes(metric string) []string {
	switch rm := resolve.RuleMetric(metric); rm {
	case resolve.FamilySleep:
		return nil
	case resolve.FamilyBloodPressure:
		var out []string
		for _, m := range catalog.Metrics() {
			if m.Group == "bp_reading" {
				out = append(out, m.Code)
			}
		}
		return out
	}
	if m, ok := catalog.Lookup(metric); ok && m.DerivedFrom != "" {
		return []string{m.DerivedFrom}
	}
	return []string{metric}
}

// recordsHref is the source's rows in w on GET /measurements: local_day windows by stored local
// date, the others by start instant.
func recordsHref(codes []string, s resolve.Source, w resolve.Window) string {
	q := url.Values{"metric": codes, "connection": {ingest.FormatConnectionID(s.ConnectionID)}, "include": {"provenance"}}
	if s.DeviceID != uuid.Nil {
		q.Set("device", formatDeviceID(s.DeviceID))
	}
	if s.OriginKey != "" {
		q.Set("origin", s.OriginKey)
	}
	if w.Kind == catalog.WindowLocalDay {
		q.Set("start_date", w.Date.Format(time.DateOnly))
		q.Set("end_date", w.Date.Format(time.DateOnly))
	} else {
		q.Set("start", w.Start.UTC().Format(time.RFC3339))
		q.Set("end", w.End.UTC().Format(time.RFC3339))
	}
	return "/api/v1/measurements?" + q.Encode()
}

type drillProv struct {
	body    *oapi.DrilldownProvenance
	relayed string
}

// drillProvenance loads, per source, the raw payloads and normalizers behind its rows.
func (o *owner) drillProvenance(ctx context.Context, srcs []resolve.SourceView) ([]drillProv, error) {
	out := make([]drillProv, len(srcs))
	var p dbq.ResolvedSourceProvenanceParams
	for i, s := range srcs {
		for _, id := range s.RecordRefs {
			p.Ids, p.Srcs = append(p.Ids, id), append(p.Srcs, int32(i))
		}
	}
	if len(p.Ids) == 0 {
		return out, nil
	}
	q := o.opts.DB.Q()
	p.UserID = auth.PrincipalFrom(ctx).UserID
	rows, err := q.ResolvedSourceProvenance(ctx, p)
	if err != nil {
		return nil, db.MapErr(err)
	}
	var rawIDs []int64
	for _, r := range rows {
		rawIDs = append(rawIDs, r.RawPayloadIds...)
	}
	fetched := map[int64]time.Time{}
	if len(rawIDs) > 0 {
		refs, err := q.ReadRawRefs(ctx, dbq.ReadRawRefsParams{UserID: p.UserID, Ids: rawIDs})
		if err != nil {
			return nil, db.MapErr(err)
		}
		for _, r := range refs {
			fetched[r.ID] = r.FetchedAt
		}
	}
	for _, r := range rows {
		b := &oapi.DrilldownProvenance{Normalizer: r.Normalizer, RawPayloadIds: make([]string, len(r.RawPayloadIds))}
		var last time.Time
		for j, id := range r.RawPayloadIds {
			b.RawPayloadIds[j] = strconv.FormatInt(id, 10)
			if t := fetched[id]; t.After(last) {
				last = t
			}
		}
		if !last.IsZero() {
			last = last.UTC()
			b.FetchedAt = &last
		}
		out[r.Src] = drillProv{body: b, relayed: r.RelayedProvider}
	}
	return out, nil
}

// ---- preview

func (o *owner) PreviewResolution(ctx context.Context, req oapi.PreviewResolutionRequestObject) (oapi.PreviewResolutionResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	r, err := resolve.ParseRule(req.Body.Spec)
	if err != nil {
		return nil, ruleProblem(err, "/spec")
	}
	from, to, err := dateRange(req.Body.StartDate, req.Body.EndDate)
	if err != nil {
		return nil, err
	}
	z, err := o.zones(ctx)
	if err != nil {
		return nil, err
	}
	first, err := resolve.LocalDay(from, z.tl)
	if err != nil {
		return nil, resolveErr(err)
	}
	metric, kind := r.Metric, dailyKind(r.Metric, r)
	user, now := auth.PrincipalFrom(ctx).UserID, time.Now()
	// Both runs are Live: no cache reads or writes (a follow leader inherits it), so the preview
	// stores nothing.
	draft := resolve.Version{Ref: "draft:" + metric, Metric: metric, Rule: r}
	drafts, err := resolve.Run(ctx, o.opts.DB, resolve.Request{UserID: user, Metric: metric, Kind: kind, From: from, To: to, Rule: &draft, Live: true, Now: now})
	if err != nil {
		return nil, resolveErr(err)
	}
	out := oapi.PreviewResolution200JSONResponse{Metric: metric, Timezone: z.name(first.Start), Window: string(kind),
		DraftRule: ruleRef(draft, r.Strategy.Op), Days: []oapi.PreviewDay{}}
	active := map[time.Time]oapi.ResolvedValue{}
	v, ok, err := o.ruleFor(ctx, metric)
	if err != nil {
		return nil, err
	}
	if ok {
		ref := ruleRef(v, v.Rule.Strategy.Op)
		out.ActiveRule = &ref
		rs, err := resolve.Run(ctx, o.opts.DB, resolve.Request{UserID: user, Metric: metric, Kind: kind, From: from, To: to, Live: true, Now: now})
		if err != nil {
			return nil, resolveErr(err)
		}
		for _, x := range rs {
			active[x.Window.Date] = resolvedValue(z, x, v.Rule)
		}
	}
	for _, x := range drafts {
		a, ok := active[x.Window.Date]
		if !ok {
			a = noRule(metric)
		}
		d := resolvedValue(z, x, r)
		d.Links = nil // the drilldown shows the rule in effect, not the draft
		out.Days = append(out.Days, oapi.PreviewDay{LocalDate: apiDate(x.Window.Date), Draft: d, Active: a})
	}
	return out, nil
}
