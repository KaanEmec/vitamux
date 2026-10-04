package resolve

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// Synthetic sources: a staged watch (A) and a second wearable (B), plus a relay of A.
var (
	slA     = Source{Provider: "garmin", DeviceType: "watch"}
	slB     = Source{Provider: "apple_health", OriginKey: "com.apple.health.watch", DeviceType: "watch"}
	slRelay = Source{Provider: "apple_health", OriginKey: "com.garmin.connect", Relayed: true}
)

func sleepRule(op Op, sq *SleepQuality) *Rule {
	yes := true
	r := &Rule{
		Schema: SchemaV1, Metric: FamilySleep,
		Window:   RuleWindow{Kind: catalog.WindowLocalNight},
		Groups:   []Group{{ID: "a", Match: []Selector{{Provider: "garmin"}}}, {ID: "b", Match: []Selector{{Provider: "apple_health"}}}},
		Exclude:  []Selector{{Relayed: &yes}},
		Strategy: Strategy{Op: op},
	}
	if sq != nil {
		r.Quality = &Quality{Sleep: sq}
	}
	return r
}

var sessSeq byte

// staged is a session with a hypnogram: 10 min awake, light, 20 min awake, deep, rem. Totals
// are summed from the stages the way the writer stores them (no latency column).
func staged(src Source, start, end string) SleepInput {
	s, e := instant(start), instant(end)
	m1, m2 := s.Add(e.Sub(s)/3), s.Add(2*e.Sub(s)/3)
	st := []normalize.SleepStage{
		{Stage: "awake", Start: s, End: s.Add(10 * time.Minute)},
		{Stage: "light", Start: s.Add(10 * time.Minute), End: m1},
		{Stage: "awake", Start: m1, End: m1.Add(20 * time.Minute)},
		{Stage: "deep", Start: m1.Add(20 * time.Minute), End: m2},
		{Stage: "rem", Start: m2, End: e},
	}
	sec := func(d time.Duration) *int32 { v := int32(d / time.Second); return &v }
	light, deep, rem := m1.Sub(s)-10*time.Minute, m2.Sub(m1)-20*time.Minute, e.Sub(m2)
	in := unstaged(src, start, end)
	in.HasStages, in.Stages = true, st
	in.Totals = normalize.SleepTotals{Asleep: sec(light + deep + rem), Light: sec(light), Deep: sec(deep), REM: sec(rem), Awake: sec(30 * time.Minute)}
	return in
}

// unstaged is a session that reports only total sleep (15 min less than the span).
func unstaged(src Source, start, end string) SleepInput {
	sessSeq++
	s, e := instant(start), instant(end)
	asleep := int32((e.Sub(s) - 15*time.Minute) / time.Second)
	return SleepInput{ID: uuid.UUID{sessSeq}, Source: src, Start: s, End: e, Totals: normalize.SleepTotals{Asleep: &asleep}}
}

func align(t *testing.T, r *Rule, night string, in ...SleepInput) SleepAlignment {
	t.Helper()
	a, err := r.AlignSleep(date(night), in, berlin)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func mainSel(t *testing.T, a SleepAlignment) SleepSelection {
	t.Helper()
	e, ok := a.Main()
	if !ok {
		t.Fatal("no main episode")
	}
	return a.Select(e)
}

func want(t *testing.T, sel SleepSelection, code string, status GroupStatus, value float64) {
	t.Helper()
	v := sel.Value(code)
	if v.Status != status || math.Abs(v.Value-value) > 1e-9 {
		t.Errorf("%s = %v %s, want %v %s", code, v.Value, v.Status, value, status)
	}
}

// Berlin is UTC+2 in June: 23:10 local on the 14th is 21:10Z.
func TestMatchedNight(t *testing.T) {
	a0 := staged(slA, "2026-06-14T21:10:00Z", "2026-06-15T04:55:00Z")
	b0 := staged(slB, "2026-06-14T21:40:00Z", "2026-06-15T05:05:00Z")
	relay := staged(slRelay, "2026-06-14T21:10:00Z", "2026-06-15T04:55:00Z")
	a := align(t, sleepRule(OpEventPriority, nil), "2026-06-15", a0, b0, relay)
	if len(a.Episodes) != 1 || len(a.Episodes[0].Sessions) != 2 || !a.Episodes[0].Main {
		t.Fatalf("episodes = %+v, want one main episode of two sessions", a.Episodes)
	}
	if len(a.Excluded) != 1 || a.Excluded[0].ID != relay.ID {
		t.Errorf("excluded = %+v, want the relay", a.Excluded)
	}
	e, _ := a.Main()
	w, err := LocalNight(date("2026-06-15"), DefaultNightAnchor, berlin)
	if err != nil {
		t.Fatal(err)
	}
	if w = w.WithEpisode(e); !w.Start.Equal(a0.Start) || !w.End.Equal(b0.End) || w.Key != "2026-06-15" {
		t.Errorf("night window = %v..%v %s", w.Start, w.End, w.Key)
	}
	sel := a.Select(e)
	if sel.Selected != 0 || len(sel.Warnings) != 0 {
		t.Fatalf("selected %d warnings %v, want group a and none", sel.Selected, sel.Warnings)
	}
	for _, g := range sel.Groups {
		if g.Status != StatusValid || g.Coverage < 0.9 {
			t.Errorf("group %+v, want valid with high coverage", g)
		}
	}
	// Every code comes from A's session.
	want(t, sel, "sleep_deep", StatusValid, float64(*a0.Totals.Deep))
	want(t, sel, "sleep_total", StatusValid, float64(*a0.Totals.Asleep))
	want(t, sel, "sleep_in_bed", StatusValid, a0.End.Sub(a0.Start).Seconds())
	want(t, sel, "sleep_latency", StatusValid, 600)
	want(t, sel, "sleep_waso", StatusValid, 1200)
	want(t, sel, "sleep_unspecified", StatusValid, 0)
	want(t, sel, "sleep_efficiency", StatusValid, 100*float64(*a0.Totals.Asleep)/a0.End.Sub(a0.Start).Seconds())

	// Pooling ops leave the choice to the strategy step; every group stays readable.
	pooled := mainSel(t, align(t, sleepRule(OpMean, nil), "2026-06-15", a0, b0))
	if pooled.Selected != -1 || pooled.Groups[1].Value("sleep_deep").Value != float64(*b0.Totals.Deep) {
		t.Errorf("mean: selected %d, b deep %v", pooled.Selected, pooled.Groups[1].Value("sleep_deep"))
	}
	if latest := mainSel(t, align(t, sleepRule(OpLatest, nil), "2026-06-15", a0, b0)); latest.Selected != 1 {
		t.Errorf("latest selected %d, want b (ends later)", latest.Selected)
	}
}

func TestPartialCaptureExcluded(t *testing.T) {
	a0 := staged(slA, "2026-06-15T01:00:00Z", "2026-06-15T05:00:00Z") // 03:00-07:00 local
	b0 := staged(slB, "2026-06-14T21:40:00Z", "2026-06-15T05:05:00Z")
	a := align(t, sleepRule(OpEventPriority, nil), "2026-06-15", a0, b0)
	if len(a.Episodes) != 1 {
		t.Fatalf("%d episodes, want the two sessions matched", len(a.Episodes))
	}
	sel := mainSel(t, a)
	g := sel.Groups[0]
	if g.Status != StatusBelowQuality || g.Reason != SleepPartialEpisode || math.Abs(g.Coverage-240.0/445) > 1e-9 {
		t.Errorf("group a = %s %s %.3f, want below_quality partial_episode 0.539", g.Status, g.Reason, g.Coverage)
	}
	if sel.Selected != 1 || len(sel.Warnings) != 1 || sel.Warnings[0] != WarnPreferredUnavailable {
		t.Fatalf("selected %d warnings %v, want b with preferred_source_unavailable", sel.Selected, sel.Warnings)
	}
	want(t, sel, "sleep_deep", StatusValid, float64(*b0.Totals.Deep))

	// A lower threshold admits A again.
	low := 0.5
	if s := mainSel(t, align(t, sleepRule(OpEventPriority, &SleepQuality{MinEpisodeCoverage: &low}), "2026-06-15", a0, b0)); s.Selected != 0 {
		t.Errorf("min_episode_coverage 0.5: selected %d, want a", s.Selected)
	}
}

func TestSplitNight(t *testing.T) {
	r := sleepRule(OpEventPriority, nil)

	// 40 min apart: one merged session.
	f1 := staged(slA, "2026-06-14T21:00:00Z", "2026-06-15T00:00:00Z")
	f2 := staged(slA, "2026-06-15T00:40:00Z", "2026-06-15T05:00:00Z")
	a := align(t, r, "2026-06-15", f2, f1)
	if len(a.Episodes) != 1 || len(a.Episodes[0].Sessions) != 1 || len(a.Episodes[0].Sessions[0].IDs) != 2 {
		t.Fatalf("episodes = %+v, want one session of two fragments", a.Episodes)
	}
	sel := mainSel(t, a)
	want(t, sel, "sleep_total", StatusValid, float64(*f1.Totals.Asleep+*f2.Totals.Asleep))
	want(t, sel, "sleep_in_bed", StatusValid, (7*time.Hour + 20*time.Minute).Seconds()) // the gap is not in bed
	want(t, sel, "sleep_latency", StatusValid, 600)                                     // first fragment only

	// 90 min apart and alone: two episodes, the longer one is main.
	g1 := staged(slA, "2026-06-14T21:00:00Z", "2026-06-14T23:30:00Z")
	g2 := staged(slA, "2026-06-15T01:00:00Z", "2026-06-15T05:00:00Z")
	a = align(t, r, "2026-06-15", g1, g2)
	if len(a.Episodes) != 2 || a.Episodes[0].Main || !a.Episodes[1].Main {
		t.Fatalf("episodes = %+v, want two with the second main", a.Episodes)
	}
	want(t, mainSel(t, a), "sleep_total", StatusValid, float64(*g2.Totals.Asleep))

	// The same fragments bridged by another source form one episode; A sums both.
	b0 := staged(slB, "2026-06-14T21:10:00Z", "2026-06-15T04:50:00Z")
	a = align(t, r, "2026-06-15", g1, g2, b0)
	if len(a.Episodes) != 1 || len(a.Episodes[0].Sessions) != 3 {
		t.Fatalf("episodes = %+v, want one bridged episode", a.Episodes)
	}
	sel = mainSel(t, a)
	if sel.Selected != 0 || math.Abs(sel.Groups[0].Coverage-6.5/8) > 1e-9 {
		t.Errorf("selected %d coverage %.3f, want a with 0.8125", sel.Selected, sel.Groups[0].Coverage)
	}
	want(t, sel, "sleep_total", StatusValid, float64(*g1.Totals.Asleep+*g2.Totals.Asleep))
}

func TestNap(t *testing.T) {
	night := []SleepInput{
		staged(slA, "2026-06-14T21:10:00Z", "2026-06-15T04:55:00Z"),
		staged(slB, "2026-06-14T21:40:00Z", "2026-06-15T05:05:00Z"),
	}
	nap := unstaged(slA, "2026-06-15T12:00:00Z", "2026-06-15T12:40:00Z") // 14:00-14:40 local
	nap.IsNap = true
	evening := unstaged(slB, "2026-06-15T16:30:00Z", "2026-06-15T17:10:00Z") // ends 19:10 local: night of the 16th
	in := append(night, nap, evening)

	a := align(t, sleepRule(OpEventPriority, nil), "2026-06-15", in...)
	if len(a.Episodes) != 2 || !a.Episodes[0].Main || a.Episodes[1].Main || !a.Episodes[1].Sessions[0].IsNap {
		t.Fatalf("episodes = %+v, want the main night and a secondary nap", a.Episodes)
	}
	if n := a.NightEpisodes(); len(n) != 1 || !n[0].Main {
		t.Errorf("night episodes = %d, want main only", len(n))
	}
	napEp := a.Episodes[1]
	if w := EpisodeWindow(napEp); w.Kind != catalog.WindowSleepEpisode || !w.Date.Equal(date("2026-06-15")) || !w.Start.Equal(nap.Start) {
		t.Errorf("nap window = %+v", w)
	}
	sel := a.Select(napEp)
	if sel.Selected != 0 || sel.Groups[1].Status != StatusNoData {
		t.Errorf("nap: selected %d, b %s", sel.Selected, sel.Groups[1].Status)
	}
	want(t, sel, "sleep_total", StatusValid, 25*60)

	if n := align(t, sleepRule(OpEventPriority, &SleepQuality{IncludeNaps: true}), "2026-06-15", in...).NightEpisodes(); len(n) != 2 {
		t.Errorf("include_naps: %d night episodes, want 2", len(n))
	}
	next := align(t, sleepRule(OpEventPriority, nil), "2026-06-16", in...)
	if len(next.Episodes) != 1 || next.Episodes[0].Sessions[0].IDs[0] != evening.ID {
		t.Errorf("night of the 16th = %+v, want the evening session", next.Episodes)
	}
}

func TestSourceWithoutStages(t *testing.T) {
	a0 := unstaged(slA, "2026-06-14T21:10:00Z", "2026-06-15T04:55:00Z")
	b0 := staged(slB, "2026-06-14T21:40:00Z", "2026-06-15T05:05:00Z")
	sel := mainSel(t, align(t, sleepRule(OpEventPriority, nil), "2026-06-15", a0, b0))
	if sel.Selected != 0 {
		t.Fatalf("selected %d, want a", sel.Selected)
	}
	for _, code := range []string{"sleep_deep", "sleep_light", "sleep_rem", "sleep_awake", "sleep_unspecified", "sleep_waso"} {
		want(t, sel, code, StatusNoStageData, 0) // never B's stages
	}
	want(t, sel, "sleep_latency", StatusNoData, 0)
	want(t, sel, "sleep_total", StatusValid, float64(*a0.Totals.Asleep))
	want(t, sel, "sleep_efficiency", StatusValid, 100*float64(*a0.Totals.Asleep)/a0.End.Sub(a0.Start).Seconds())
	if v := sel.Groups[1].Value("sleep_deep"); v.Status != StatusValid {
		t.Errorf("b deep = %+v, want valid (only not used)", v)
	}

	// Stages with unspecified sleep only: the remainder is unspecified, the staged codes are not.
	u := unstaged(slA, "2026-06-14T21:10:00Z", "2026-06-15T04:55:00Z")
	u.HasStages = true
	sel = mainSel(t, align(t, sleepRule(OpEventPriority, nil), "2026-06-15", u))
	want(t, sel, "sleep_unspecified", StatusValid, float64(*u.Totals.Asleep))
	want(t, sel, "sleep_deep", StatusNoStageData, 0)
}

func TestAlignSleepNeedsTimezone(t *testing.T) {
	_, err := sleepRule(OpEventPriority, nil).AlignSleep(date("2026-06-15"), []SleepInput{unstaged(slA, "2026-06-14T21:10:00Z", "2026-06-15T04:55:00Z")}, nil)
	if err == nil {
		t.Fatal("want an error without a timeline or record zone")
	}
}

// TestResolveEpisodeCodes pins how ResolveEpisode reads one or several codes: a selecting op
// takes every code from the selected group, a pooling op pools each code over the groups that
// have it.
func TestResolveEpisodeCodes(t *testing.T) {
	a0 := unstaged(slA, "2026-06-14T21:10:00Z", "2026-06-15T04:55:00Z")
	b0 := staged(slB, "2026-06-14T21:40:00Z", "2026-06-15T05:05:00Z")
	relay := staged(slRelay, "2026-06-14T21:10:00Z", "2026-06-15T04:55:00Z")
	night, err := LocalNight(date("2026-06-15"), DefaultNightAnchor, berlin)
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(op Op, in []SleepInput, codes ...string) WindowResult {
		t.Helper()
		a := align(t, sleepRule(op, nil), "2026-06-15", in...)
		e, _ := a.Main()
		res, err := a.ResolveEpisode(night.WithEpisode(e), e, codes, Options{})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	both := []SleepInput{a0, b0, relay}
	totalA, totalB, deepB := float64(*a0.Totals.Asleep), float64(*b0.Totals.Asleep), float64(*b0.Totals.Deep)

	// Selecting: A is preferred and lacks stages; B's stages are never taken.
	res := resolve(OpEventPriority, both, "sleep_deep")
	if res.Status != ResultNoData || res.Selected != "a" || res.Groups[0].Status != StatusNoStageData {
		t.Errorf("priority deep = %s selected %q group %s", res.Status, res.Selected, res.Groups[0].Status)
	}
	if res.Inputs.Grouped != 2 || res.Inputs.Excluded != 1 {
		t.Errorf("inputs = %+v, want 2 grouped and 1 excluded", res.Inputs)
	}
	res = resolve(OpEventPriority, both, "sleep_total", "sleep_deep")
	if res.Status != ResultDirect || res.Components["sleep_total"] != totalA || res.Missing["sleep_deep"] != StatusNoStageData {
		t.Errorf("priority total+deep = %s %v missing %v", res.Status, res.Components, res.Missing)
	}

	// Pooling: each code pools over the groups that have it.
	res = resolve(OpMean, both, "sleep_total")
	if res.Status != ResultCalculated || math.Abs(res.Value-(totalA+totalB)/2) > 1e-9 {
		t.Errorf("mean total = %s %v", res.Status, res.Value)
	}
	res = resolve(OpMean, both, "sleep_deep")
	if res.Status != ResultCalculated || res.Value != deepB || res.Groups[0].Status != StatusNoStageData {
		t.Errorf("mean deep = %s %v group a %s", res.Status, res.Value, res.Groups[0].Status)
	}
	res = resolve(OpMean, both, "sleep_total", "sleep_deep", "sleep_latency")
	if res.Status != ResultCalculated || res.Value != 0 || len(res.Missing) != 0 ||
		math.Abs(res.Components["sleep_total"]-(totalA+totalB)/2) > 1e-9 ||
		res.Components["sleep_deep"] != deepB || res.Components["sleep_latency"] != 600 {
		t.Errorf("mean total+deep+latency = %s %v %v missing %v", res.Status, res.Value, res.Components, res.Missing)
	}

	// Pooling without a staged group: stage codes miss as no_stage_data, others as no_data.
	res = resolve(OpMean, []SleepInput{a0}, "sleep_total", "sleep_deep", "sleep_latency")
	if res.Status != ResultCalculated || res.Components["sleep_total"] != totalA || len(res.Components) != 1 ||
		res.Missing["sleep_deep"] != StatusNoStageData || res.Missing["sleep_latency"] != StatusNoData {
		t.Errorf("mean unstaged = %s %v missing %v", res.Status, res.Components, res.Missing)
	}
}

// unknown, restless and out_of_bed stages are no sleep: unknown counts as neither asleep nor awake,
// restless as in bed.
func TestFragValueIgnoresNonSleepStages(t *testing.T) {
	s := instant("2026-06-14T22:00:00Z")
	at := func(min int) time.Time { return s.Add(time.Duration(min) * time.Minute) }
	asleep, awake := int32(3600), int32(300)
	f := SleepInput{Start: s, End: at(100), HasStages: true, Totals: normalize.SleepTotals{Asleep: &asleep, Awake: &awake},
		Stages: []normalize.SleepStage{
			{Stage: "unknown", Start: s, End: at(10)}, {Stage: "light", Start: at(10), End: at(70)},
			{Stage: "restless", Start: at(70), End: at(80)}, {Stage: "awake", Start: at(80), End: at(85)},
			{Stage: "out_of_bed", Start: at(85), End: at(100)}}}
	for code, want := range map[string]float64{"sleep_total": 3600, "sleep_awake": 300, "sleep_in_bed": 6000,
		"sleep_latency": 600, "sleep_waso": 0, "sleep_unspecified": 0} {
		if got, st := fragValue(f, code); st != StatusValid || got != want {
			t.Errorf("%s = %v (%s), want %v", code, got, st, want)
		}
	}
}
