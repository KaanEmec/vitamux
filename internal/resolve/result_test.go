package resolve

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// Explanation snapshots live in testdata/explanations (synthetic data only). Regenerate with
//
//	UPDATE_GOLDEN=1 go test ./internal/resolve/ (add -tags integration for the fixturegen scenarios)

// resultText is the snapshot of one result: window, status, value and every input, then the
// explanation on its own line.
func resultText(r Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s: %s", r.Window.Kind, r.Window.Key, r.Metric, r.Status)
	if r.Value != nil {
		fmt.Fprintf(&b, " %s", number(*r.Value))
	}
	for _, k := range slices.Sorted(maps.Keys(r.Components)) {
		fmt.Fprintf(&b, " %s=%s", k, number(r.Components[k]))
	}
	if r.Partial {
		b.WriteString(" partial")
	}
	b.WriteString(" [")
	for i, in := range r.Inputs {
		if i > 0 {
			b.WriteString(" ")
		}
		name := in.Group
		if name == "" && len(in.Sources) > 0 {
			name = strings.ReplaceAll(sourceLabel(in.Sources[0]), " ", "/")
		}
		b.WriteString(name + ":" + string(in.Status))
		if in.Selected {
			b.WriteString("*")
		}
	}
	b.WriteString("]\n  " + r.Explanation + "\n")
	return b.String()
}

// golden compares the results' snapshot with testdata/explanations/<name>.txt.
func golden(t *testing.T, name string, rs ...Result) {
	t.Helper()
	var b strings.Builder
	b.WriteString("# synthetic: true\n")
	for _, r := range rs {
		b.WriteString(resultText(r))
	}
	path := filepath.Join("testdata", "explanations", name+".txt")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1)", err)
	}
	if got := b.String(); got != string(want) {
		t.Errorf("%s changed:\n--- got\n%s--- want\n%s", path, got, want)
	}
}

// testVersion wraps a rule as an owner version for BuildResult.
func testVersion(r *Rule) Version {
	return Version{Ref: RuleRef(r.Metric, 1), Metric: r.Metric, Version: 1, Rule: r}
}

func mustResolve(t *testing.T, r *Rule, w Window, s Series, opt Options, ovs ...Override) Result {
	t.Helper()
	res, err := r.ResolveWindowOverridden(w, s, opt, ovs)
	if err != nil {
		t.Fatal(err)
	}
	return BuildResult(r.Metric, testVersion(r), res, instant("2026-09-15T06:00:03Z"))
}

// near compares values summed from pro-rated bucket parts.
func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func input(r Result, group string) ResultInput {
	for _, in := range r.Inputs {
		if in.Group == group {
			return in
		}
	}
	return ResultInput{}
}

var (
	rsRelay = Source{Provider: "apple_health", OriginKey: "com.garmin.connect.mobile", OriginName: "Garmin Connect", Relayed: true, DeviceType: "watch"}
	relayed = func(b bool) *bool { return &b }
)

// rsSteps spreads total over n intervals of d from local midnight of day.
func rsSteps(src Source, day string, n int, d time.Duration, total int) []Input {
	var out []Input
	per := total / n
	for i := range n {
		v := per
		if i == n-1 {
			v = total - per*(n-1)
		}
		out = append(out, exInterval(src, exT(day, "00:00").Add(time.Duration(i)*d), d, float64(v)))
	}
	return out
}

// The resolved-day example of docs/architecture/api.md: maximum of a Garmin daily total and
// Apple Watch intervals, Withings without data, a relayed copy excluded and the iPhone outside
// the rule. The result and the drilldown carry what the API renders.
func TestResultShapeStepsDay(t *testing.T) {
	const day = "2026-09-14"
	r := &Rule{Schema: SchemaV1, Metric: "steps", Window: RuleWindow{Kind: catalog.WindowLocalDay}, Strategy: Strategy{Op: OpMax},
		Groups: []Group{{ID: "garmin", Match: []Selector{{Provider: "garmin"}}},
			{ID: "apple_watch", Match: []Selector{{Provider: "apple_health", DeviceType: "watch", Relayed: relayed(false)}}},
			{ID: "withings", Match: []Selector{{Provider: "withings"}}}},
		Exclude: []Selector{{Provider: "apple_health", Relayed: relayed(true)}}}
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	s := Series{"steps": slices.Concat(
		[]Input{exDaily(exGarmin, day, 11342)}, rsSteps(exGarmin, day, 96, 15*time.Minute, 11290),
		rsSteps(exWatch, day, 48, 30*time.Minute, 10877), rsSteps(rsRelay, day, 24, time.Hour, 11288),
		rsSteps(exPhone, day, 12, 2*time.Hour, 6034))}
	w, _ := LocalDay(date(day), berlin)
	got := mustResolve(t, r, w, s, Options{})

	if got.Status != ResultCalculated || *got.Value != 11342 || got.Unit != "count" || got.Rule != (RuleInfo{"rule:steps:1", 1, OpMax}) {
		t.Fatalf("result: %+v", got)
	}
	g, a, wi := input(got, "garmin"), input(got, "apple_watch"), input(got, "withings")
	if g.Status != StatusUsed || !g.Selected || g.Basis != BasisDailyValue || *g.Value != 11342 || len(g.RecordRefs) != 1 {
		t.Errorf("garmin: %+v", g)
	}
	if a.Status != StatusUsed || a.Selected || a.Basis != BasisIntervals || !near(*a.Value, 10877) {
		t.Errorf("apple_watch: %+v", a)
	}
	if wi.Status != StatusNoData || wi.Value != nil || wi.Reason != ReasonNoInputs {
		t.Errorf("withings: %+v", wi)
	}
	ex, nr := got.Inputs[3], got.Inputs[4]
	if ex.Status != StatusExcluded || ex.Reason != "exclude: provider=apple_health relayed=true" || ex.Count != 24 {
		t.Errorf("excluded: %+v", ex)
	}
	if nr.Status != StatusNotInRule || nr.Sources[0] != exPhone || nr.Count != 12 {
		t.Errorf("not in rule: %+v", nr)
	}
	if got.Coverage != 1 { // the mean over the used groups: a daily value and a full day of intervals
		t.Errorf("coverage %v", got.Coverage)
	}

	srcs, err := BuildSources(r, w, s, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		group  string
		status GroupStatus
		values map[string]float64
	}{
		{"garmin", StatusUsed, map[string]float64{"daily_value": 11342, "interval_sum": 11290, "intervals": 96}},
		{"apple_watch", StatusUsed, map[string]float64{"interval_sum": 10877, "intervals": 48}},
		{"", StatusExcluded, map[string]float64{"interval_sum": 11288, "intervals": 24}},
		{"", StatusNotInRule, map[string]float64{"interval_sum": 6034, "intervals": 12}},
	}
	if len(srcs) != len(want) {
		t.Fatalf("%d sources: %+v", len(srcs), srcs)
	}
	for i, x := range want {
		if srcs[i].Group != x.group || srcs[i].RuleStatus != x.status || !maps.EqualFunc(srcs[i].Values, x.values, near) {
			t.Errorf("source %d: %+v, want %+v", i, srcs[i], x)
		}
	}
	if srcs[2].Reason != "exclude: provider=apple_health relayed=true" || srcs[2].Source != rsRelay {
		t.Errorf("excluded source: %+v", srcs[2])
	}
	golden(t, "api-steps-day", got)
}

// The blood pressure example: components from the latest reading of one source.
func TestResultShapeBloodPressure(t *testing.T) {
	const day = "2026-09-14"
	r := &Rule{Schema: SchemaV1, Metric: FamilyBloodPressure, Window: RuleWindow{Kind: catalog.WindowLocalDay}, Strategy: Strategy{Op: OpLatest},
		WithinSource: &WithinSource{Statistic: StatLatest}, Groups: []Group{{ID: "withings", Match: []Selector{{Provider: "withings"}}}}}
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	row := func(g int64, hhmm string, v float64) Input {
		x := exSample(agCuff, exT(day, hhmm), v)
		x.GroupID = g
		return x
	}
	s := Series{"bp_systolic": {row(1, "07:10", 118), row(2, "21:05", 121)}, "bp_diastolic": {row(1, "07:10", 77), row(2, "21:05", 79)},
		"bp_pulse": {row(1, "07:10", 61), row(2, "21:05", 64)}}
	w, _ := LocalDay(date(day), berlin)
	got := mustResolve(t, r, w, s, Options{})
	want := map[string]float64{"bp_systolic": 121, "bp_diastolic": 79, "bp_pulse": 64}
	if got.Status != ResultDirect || got.Value != nil || !maps.Equal(got.Components, want) || got.Unit != "" {
		t.Fatalf("result: %+v", got)
	}
	if in := input(got, "withings"); !in.Selected || in.Readings != 1 || !maps.Equal(in.Components, want) {
		t.Errorf("withings: %+v", in)
	}
	srcs, _ := BuildSources(r, w, s, time.Time{})
	if len(srcs) != 1 || srcs[0].Values["readings"] != 2 || srcs[0].Values["bp_systolic"] != 121 {
		t.Errorf("sources: %+v", srcs)
	}
	golden(t, "api-blood-pressure", got)
}

// Every op and status has its template: direct, fallback, the pooling ops, latest, no data,
// stale, coverage and flags, and overrides.
func TestExplanationTemplates(t *testing.T) {
	const day = "2026-09-14"
	hr := func(op Op) *Rule {
		return &Rule{Schema: SchemaV1, Metric: "heart_rate", Window: RuleWindow{Kind: catalog.WindowHour}, Strategy: Strategy{Op: op, MinSources: 2},
			Groups: []Group{{ID: "watch", Match: []Selector{{DeviceType: "watch", Provider: "apple_health"}}}, {ID: "ring", Match: []Selector{{DeviceType: "ring"}}},
				{ID: "garmin", Match: []Selector{{Provider: "garmin"}}}},
			Quality: &Quality{MinCoverage: new(0.5), ExcludeFlags: []string{"manual_entry"}, MaxStaleness: "2h"}}
	}
	hour, _ := Buckets(date(day), time.Hour, berlin)
	h10 := hour[10]
	var s []Input
	exEvery(h10.Start, h10.End, time.Minute, func(m time.Time) { s = append(s, exSample(exWatch, m, 64)) })
	exEvery(h10.Start, h10.End, 5*time.Minute, func(m time.Time) { s = append(s, exSample(exRing, m, 66)) })
	s = append(s, exSample(exGarmin, h10.Start.Add(time.Minute), 70)) // one bucket of twelve: below coverage
	series := Series{"heart_rate": s}

	var out []Result
	for _, op := range []Op{OpFirstAvailable, OpMean, OpMin, OpMax, OpLatest} {
		out = append(out, mustResolve(t, hr(op), h10, series, Options{}))
	}
	// Fallback: the watch has only manual rows this hour.
	var manual []Input
	for _, x := range s {
		if x.Source == exWatch {
			x.Flags = 1 // manual_entry
		}
		manual = append(manual, x)
	}
	out = append(out, mustResolve(t, hr(OpFirstAvailable), h10, Series{"heart_rate": manual}, Options{}))
	// No data at all in another hour; a stale watch on a latest window.
	out = append(out, mustResolve(t, hr(OpFirstAvailable), hour[3], series, Options{}))
	lr := hr(OpFirstAvailable)
	lr.Window = RuleWindow{Kind: catalog.WindowLatest}
	out = append(out, mustResolve(t, lr, LatestWindow(exT(day, "15:00")), Series{"heart_rate": append(slices.Clone(s), exSample(exGarmin, exT(day, "14:30"), 71))}, Options{}))
	// Overrides: set_value keeps the computed value in the explanation; force_source names both.
	ov := func(a OverrideAction, edit func(*Override)) Override {
		o := Override{ID: uuid.New(), Scope: ScopeOf("heart_rate", h10), Action: a, CreatedAt: instant("2026-09-14T12:00:00Z")}
		edit(&o)
		return o
	}
	out = append(out, mustResolve(t, hr(OpFirstAvailable), h10, series, Options{},
		ov(SetValue, func(o *Override) { o.Value, o.Unit, o.Note = 61, "bpm", "synthetic note" })))
	out = append(out, mustResolve(t, hr(OpFirstAvailable), h10, series, Options{},
		ov(ForceSource, func(o *Override) { o.Group = "ring" }), ov(ExcludeInput, func(o *Override) { o.InputID = s[0].ID }),
		ov(ExcludeInput, func(o *Override) { o.InputID = 1 << 40 })))
	for _, r := range out {
		if r.Explanation == "" || len(r.Inputs) < 3 {
			t.Errorf("result without explanation or groups: %+v", r)
		}
	}
	if o := out[len(out)-2]; o.Status != ResultOverridden || *o.Value != 61 || o.Computed == nil || *o.Computed.Value != 64 || o.Selected != "" ||
		input(o, "watch").Selected || strings.Contains(o.Explanation, "synthetic note") {
		t.Errorf("set_value: %+v", o)
	}
	golden(t, "templates", out...)
}

// Edge case 10: semantically different metrics are never pooled. In memory: the generator has
// no heart rate variability. Pooling a selection-only metric is rejected on save, codes of
// different methods never combine, and switching groups between windows is flagged.
func TestScenarioNeverPooled(t *testing.T) {
	r := &Rule{Schema: SchemaV1, Metric: "hrv_rmssd_nightly", Window: RuleWindow{Kind: catalog.WindowLocalDay}, Strategy: Strategy{Op: OpMean},
		Groups: []Group{{ID: "ring", Match: []Selector{{DeviceType: "ring"}}}, {ID: "garmin", Match: []Selector{{Provider: "garmin"}}}}}
	var ve *ValidationError
	if err := Validate(r); !errors.As(err, &ve) || !strings.Contains(err.Error(), "/strategy/op") {
		t.Fatalf("mean of a selection-only metric: %v", err)
	}
	if catalog.Combinable("hrv_sdnn", "hrv_rmssd") || catalog.Combinable("hrv_rmssd", "hrv_rmssd_nightly") {
		t.Error("different heart rate variability methods combine")
	}
	r.Strategy = Strategy{Op: OpFirstAvailable}
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	days := []string{"2026-09-14", "2026-09-15"}
	var ws []Window
	for _, d := range days {
		w, _ := LocalDay(date(d), berlin)
		ws = append(ws, w)
	}
	s := Series{"hrv_rmssd_nightly": {exDaily(exRing, days[0], 48), exDaily(exGarmin, days[0], 61), exDaily(exGarmin, days[1], 59)}}
	res, err := r.ResolveWindowsOverridden(ws, s, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []Result
	for _, x := range res {
		out = append(out, BuildResult(r.Metric, testVersion(r), x, time.Time{}))
	}
	if out[0].Selected != "ring" || out[1].Selected != "garmin" || !slices.ContainsFunc(out[1].Warnings, func(w WindowWarning) bool { return w.Code == WarnDefinitionChanged }) {
		t.Errorf("selection: %+v", out)
	}
	if got := SourcesUsed(out); !slices.Equal(got, []string{"ring", "garmin"}) {
		t.Errorf("sources used: %v", got)
	}
	golden(t, "scenario-10-never-pooled", out...)
}

// The J09.10 extension scenarios (extensions_test.go) explained: E1 contexts, E2 window
// statistics, E3 wear with E9 hour composition, and E5 follow.
func TestExplanationExtensions(t *testing.T) {
	const day = "2026-06-15"
	var out []Result
	add := func(r *Rule, res WindowResult) {
		out = append(out, BuildResult(r.Metric, testVersion(r), Resolved{WindowResult: res}, time.Time{}))
	}

	// E1: a chest strap leads inside the workout recorded by the Garmin watch.
	e1 := exRule(t, "e1-workout-hr-contexts.json")
	var hr []Input
	exEvery(exT(day, "09:55"), exT(day, "11:30"), time.Minute, func(m time.Time) { hr = append(hr, exSample(exWatch, m, 100)) })
	exEvery(exT(day, "10:00"), exT(day, "10:30"), time.Minute, func(m time.Time) { hr = append(hr, exSample(exStrap, m, 130)) })
	exEvery(exT(day, "10:00"), exT(day, "11:00"), time.Minute, func(m time.Time) { hr = append(hr, exSample(exGarmin, m, 125)) })
	run := WorkoutInput{ID: uuid.New(), Source: exGarmin, Start: exT(day, "10:00"), End: exT(day, "11:00"), Sport: "running"}
	buckets, _ := Buckets(date(day), 5*time.Minute, berlin)
	got := exByStart(t, e1, buckets, Series{"heart_rate": hr}, Options{Events: ContextEvents{Workouts: ClusterWorkouts([]WorkoutInput{run})}})
	add(e1, got["10:00"])
	add(e1, got["10:30"])

	// E2: nocturnal resting heart rate, the lowest 30-minute mean of the main episode.
	nr := exNocturnalRule()
	ep, _ := exNight(t).Main()
	night, _ := LocalNight(date(day), DefaultNightAnchor, berlin)
	var ring []Input
	exEvery(ep.Start, ep.End, 5*time.Minute, func(b time.Time) {
		v := 58.0
		if l := b.In(exLoc).Format("15:04"); l >= "03:00" && l < "03:30" {
			v = 49
		}
		ring = append(ring, exSample(exRing, b.Add(time.Minute), v))
	})
	res, err := nr.ResolveWindow(night.WithEpisode(ep), Series{"heart_rate": ring}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	add(nr, res)

	// E3 + E9: hourly steps; the watch is off 12:00-15:00 and the phone is wear-exempt.
	e9 := exRule(t, "e9-steps-compose.json")
	var whr, steps []Input
	off := func(b time.Time) bool { return !b.Before(exT(day, "12:00")) && b.Before(exT(day, "15:00")) }
	exEvery(exT(day, "07:00"), exT(day, "23:00"), 5*time.Minute, func(b time.Time) {
		if !off(b) {
			whr = append(whr, exSample(exWatch, b.Add(2*time.Minute), 70))
		}
	})
	exEvery(exT(day, "08:00"), exT(day, "20:00"), 5*time.Minute, func(b time.Time) {
		if !off(b) {
			steps = append(steps, exInterval(exWatch, b, 5*time.Minute, 50))
		}
	})
	exEvery(exT(day, "10:00"), exT(day, "18:00"), time.Hour, func(h time.Time) { steps = append(steps, exInterval(exPhone, h, time.Hour, 900)) })
	dw, _ := LocalDay(date(day), berlin)
	res, err = e9.ResolveWindow(dw, Series{"steps": steps, "heart_rate": whr}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	add(e9, res)
	add(e9, res.Hours[13])

	// E5: the follower takes the leader's group, else its own ladder.
	follower := &Rule{Schema: SchemaV1, Metric: "distance_walk_run", Window: RuleWindow{Kind: catalog.WindowLocalDay}, Strategy: Strategy{Op: OpFirstAvailable},
		Groups: []Group{{ID: "ring", Match: []Selector{{DeviceType: "ring"}}}, {ID: "watch", Match: []Selector{{DeviceType: "watch"}}}},
		Follow: "active_energy"}
	fs := Series{"distance_walk_run": {exDaily(exRing, day, 1500), exDaily(exWatch, day, 1600)}}
	res, _ = follower.ResolveWindow(dw, fs, Options{Leader: map[string]string{dw.Key: "watch"}})
	add(follower, res)
	res, _ = follower.ResolveWindow(dw, Series{"distance_walk_run": {exDaily(exRing, day, 1500)}}, Options{Leader: map[string]string{dw.Key: "watch"}})
	add(follower, res)

	for _, r := range out {
		if len(r.Inputs) < 2 || r.Explanation == "" {
			t.Errorf("incomplete result: %+v", r)
		}
	}
	golden(t, "extensions", out...)
}
