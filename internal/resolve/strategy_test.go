package resolve

import (
	"errors"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// stHR is a heart_rate hour rule over garmin, whoop and apple groups.
func stHR(op Op) *Rule {
	return agRule("heart_rate", catalog.WindowHour, op,
		agGroup("garmin", Selector{Provider: "garmin"}),
		agGroup("whoop", Selector{Provider: "whoop"}),
		agGroup("apple", Selector{Provider: "apple_health"}))
}

func stResolve(t *testing.T, r *Rule, w Window, opt Options, in ...Input) WindowResult {
	t.Helper()
	res, err := r.ResolveWindow(w, Series{r.Metric: in}, opt)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func stStatuses(res WindowResult) map[string]GroupStatus {
	out := map[string]GroupStatus{}
	for _, g := range res.Groups {
		if g.ID != "" {
			out[g.ID] = g.Status
		}
	}
	return out
}

func stHasWarning(res WindowResult, c Warning, group string) bool {
	for _, w := range res.Warnings {
		if w.Code == c && w.Group == group {
			return true
		}
	}
	return false
}

func TestStrategies(t *testing.T) {
	w := agHourWindow("2026-06-15T10:00:00Z")
	at := func(m int) time.Time { return w.Start.Add(time.Duration(m) * time.Minute) }
	in := []Input{
		agSample(agWhoop, at(1), 60), agSample(agWhoop, at(40), 64), // whoop: 62, last at :40
		agSample(agWatch, at(5), 70), // apple: 70, only at :05
	}
	cases := []struct {
		op     Op
		value  float64
		status ResultStatus
		sel    string
	}{
		{OpFirstAvailable, 62, ResultFallback, "whoop"},
		{OpMean, 66, ResultCalculated, ""},
		{OpMin, 62, ResultCalculated, ""},
		{OpMax, 70, ResultCalculated, ""},
		{OpLatest, 62, ResultDirect, "whoop"},
		{OpEarliest, 62, ResultDirect, "whoop"}, // whoop's first sample (:01) is earliest
	}
	for _, tc := range cases {
		res := stResolve(t, stHR(tc.op), w, Options{}, in...)
		if !agNear(res.Value, tc.value) || res.Status != tc.status || res.Selected != tc.sel {
			t.Errorf("%s: %v %s %q, want %v %s %q", tc.op, res.Value, res.Status, res.Selected, tc.value, tc.status, tc.sel)
		}
		if st := stStatuses(res); st["garmin"] != StatusNoData {
			t.Errorf("%s: garmin %s, want no_data", tc.op, st["garmin"])
		}
	}
	// first_available: garmin had nothing, so a warning names it; apple is valid but unused.
	res := stResolve(t, stHR(OpFirstAvailable), w, Options{}, in...)
	if !stHasWarning(res, WarnPreferredUnavailable, "garmin") || len(res.Warnings) != 1 || stStatuses(res)["apple"] != StatusUnused {
		t.Errorf("first_available: warnings %v statuses %v", res.Warnings, stStatuses(res))
	}
	// Ties on latest go by group order.
	tie := []Input{agSample(agWhoop, at(10), 60), agSample(agWatch, at(10), 70)}
	if res := stResolve(t, stHR(OpLatest), w, Options{}, tie...); res.Selected != "whoop" {
		t.Errorf("latest tie: %s, want whoop", res.Selected)
	}
	// single_source: the one group, or no_data.
	r := agRule("heart_rate", catalog.WindowHour, OpSingleSource, agGroup("garmin", Selector{Provider: "garmin"}))
	if res := stResolve(t, r, w, Options{}, in...); res.Status != ResultNoData || res.Groups[0].Reason != ReasonNoInputs {
		t.Errorf("single_source without data: %+v", res)
	}
}

// The documented example: dense source A (62.1) and one sample of B (66) in one 5-min bucket.
func TestMeanAcrossSourcesIsNotPooledSamples(t *testing.T) {
	s := instant("2026-06-15T10:00:00Z")
	w := Window{Kind: catalog.WindowBucket, Start: s, End: s.Add(5 * time.Minute)}
	var in []Input
	for i := range 50 {
		v := 62.0
		if i%5 == 0 {
			v = 62.5 // (40*62 + 10*62.5) / 50 = 62.1
		}
		in = append(in, agSample(agGarmin, s.Add(time.Duration(i)*5*time.Second), v))
	}
	in = append(in, agSample(agWhoop, s.Add(time.Minute), 66))
	r := stHR(OpMean)
	r.Window = RuleWindow{Kind: catalog.WindowBucket, Size: "5m"}
	if res := stResolve(t, r, w, Options{}, in...); !agNear(res.Value, (62.1+66)/2) {
		t.Errorf("mean across sources %v, want %v", res.Value, (62.1+66)/2)
	}
}

func TestSumAcrossSources(t *testing.T) {
	w := agHourWindow("2026-06-15T10:00:00Z")
	in := []Input{
		agInterval(agGarmin, w.Start, w.Start.Add(10*time.Minute), 1000),
		agInterval(agWatch, w.Start.Add(20*time.Minute), w.Start.Add(30*time.Minute), 500),
	}
	r := agRule("steps", catalog.WindowHour, OpSum, agGroup("garmin", Selector{Provider: "garmin"}), agGroup("apple", Selector{Provider: "apple_health"}))
	if _, err := r.ResolveWindow(w, Series{"steps": in}, Options{}); !errors.Is(err, ErrUnacknowledgedSum) {
		t.Fatalf("unacknowledged sum: %v", err)
	}
	r.AcknowledgedWarnings = []Warning{WarnCrossSourceSum}
	res := stResolve(t, r, w, Options{}, in...)
	if res.Value != 1500 || !stHasWarning(res, WarnCrossSourceSum, "") {
		t.Errorf("acknowledged sum: %v %v", res.Value, res.Warnings)
	}
}

func TestMinSources(t *testing.T) {
	w := agHourWindow("2026-06-15T10:00:00Z")
	in := []Input{agSample(agGarmin, w.Start, 60)}
	r := stHR(OpMean)
	r.Strategy.MinSources = 2
	res := stResolve(t, r, w, Options{}, in...)
	if res.Value != 60 || res.Status != ResultCalculated || !stHasWarning(res, WarnInsufficientSources, "") {
		t.Errorf("use_available: %+v", res)
	}
	r.Strategy.OnInsufficient = NoValue
	res = stResolve(t, r, w, Options{}, in...)
	if res.Status != ResultNoData || !stHasWarning(res, WarnInsufficientSources, "") || res.Groups[0].Status != StatusUnused || res.Groups[0].Reason != ReasonInsufficient {
		t.Errorf("no_value: %+v", res)
	}
}

func TestQualityGates(t *testing.T) {
	w := agHourWindow("2026-06-15T10:00:00Z")
	at := func(m int) time.Time { return w.Start.Add(time.Duration(m) * time.Minute) }

	// Plausible range: implausible rows are dropped; a group with nothing left falls through.
	r := stHR(OpFirstAvailable)
	res := stResolve(t, r, w, Options{}, agSample(agGarmin, at(1), 400), agSample(agWhoop, at(1), 61), agSample(agWhoop, at(2), 300))
	if g := res.Groups[0]; g.Status != StatusBelowQuality || g.Reason != ReasonImplausible || res.Value != 61 || res.Inputs.Implausible != 2 {
		t.Errorf("catalogue range: %+v / %+v", g, res)
	}
	r.Quality = &Quality{PlausibleRange: []float64{25, 450}} // the rule's range replaces the catalogue's
	if res := stResolve(t, r, w, Options{}, agSample(agGarmin, at(1), 400)); res.Value != 400 || res.Selected != "garmin" {
		t.Errorf("rule range: %+v", res)
	}

	// Flags: manual entries excluded, so apple falls back to nothing and whoop is used.
	r = stHR(OpFirstAvailable)
	r.Quality = &Quality{ExcludeFlags: []string{"manual_entry"}}
	manual := agSample(agGarmin, at(1), 70)
	manual.Flags = normalize.FlagManualEntry
	res = stResolve(t, r, w, Options{}, manual, agSample(agWhoop, at(3), 58))
	if g := res.Groups[0]; g.Status != StatusBelowQuality || g.Reason != ReasonFlagged || res.Selected != "whoop" || res.Inputs.Flagged != 1 {
		t.Errorf("flags: %+v / %+v", g, res)
	}

	// Coverage: garmin covers 2 of 12 buckets, below 0.5, so whoop (12 of 12) is selected.
	r = stHR(OpFirstAvailable)
	r.Quality = &Quality{MinCoverage: new(0.5)}
	in := []Input{agSample(agGarmin, at(0), 60), agSample(agGarmin, at(5), 60)}
	for m := 0; m < 60; m += 5 {
		in = append(in, agSample(agWhoop, at(m), 62))
	}
	res = stResolve(t, r, w, Options{}, in...)
	if g := res.Groups[0]; g.Status != StatusBelowQuality || g.Reason != ReasonCoverage || res.Selected != "whoop" || res.Status != ResultFallback {
		t.Errorf("coverage: %+v / %+v", g, res)
	}

	// Staleness: on a latest window, a weigh-in older than max_staleness falls back.
	wr := agRule("weight", catalog.WindowLatest, OpFirstAvailable, agGroup("withings", Selector{Provider: "withings"}), agGroup("apple", Selector{Provider: "apple_health"}))
	wr.Quality = &Quality{MaxStaleness: "30d"}
	asOf := instant("2026-06-15T12:00:00Z")
	res = stResolve(t, wr, LatestWindow(asOf), Options{}, agSample(agCuff, asOf.AddDate(0, 0, -40), 80), agSample(agWatch, asOf.AddDate(0, 0, -2), 81))
	if g := res.Groups[0]; g.Status != StatusStale || res.Selected != "apple" || res.Value != 81 {
		t.Errorf("staleness: %+v / %+v", g, res)
	}
}

func TestAdditiveRangeAndCoverageGates(t *testing.T) {
	day, _ := LocalDay(date("2026-06-15"), berlin)
	r := agRule("steps", catalog.WindowLocalDay, OpFirstAvailable, agGroup("garmin", Selector{Provider: "garmin"}), agGroup("apple", Selector{Provider: "apple_health"}))
	r.Quality = &Quality{MinCoverage: new(0.9), PlausibleRange: []float64{0, 50000}}
	in := []Input{
		// Two 30k intervals: each row is plausible, the day sum is not.
		agInterval(agGarmin, instant("2026-06-15T08:00:00Z"), instant("2026-06-15T09:00:00Z"), 30000),
		agInterval(agGarmin, instant("2026-06-15T10:00:00Z"), instant("2026-06-15T11:00:00Z"), 30000),
		// One short walk: low bucket coverage, but additive coverage is not gated without require_wear.
		agInterval(agWatch, instant("2026-06-15T08:00:00Z"), instant("2026-06-15T08:10:00Z"), 900),
	}
	res := stResolve(t, r, day, Options{}, in...)
	if g := res.Groups[0]; g.Status != StatusBelowQuality || g.Reason != ReasonImplausible || res.Selected != "apple" || res.Value != 900 {
		t.Errorf("additive gates: %+v / %+v", g, res)
	}
}

// A week of local days: the preferred source is missing on Tuesday and Thursday, and below
// coverage on Saturday. Fallback happens on those days only.
func TestPerWindowFallbackAcrossWeek(t *testing.T) {
	r := agRule("heart_rate", catalog.WindowLocalDay, OpFirstAvailable,
		agGroup("garmin", Selector{Provider: "garmin"}), agGroup("apple", Selector{Provider: "apple_health"}))
	r.Quality = &Quality{MinCoverage: new(0.5)}
	var windows []Window
	var in []Input
	fill := func(src Source, w Window, every time.Duration, v float64) {
		for x := w.Start; x.Before(w.End); x = x.Add(every) {
			in = append(in, agSample(src, x, v))
		}
	}
	for d := range 7 {
		w, err := LocalDay(date("2026-06-15").AddDate(0, 0, d), berlin) // Monday..Sunday
		if err != nil {
			t.Fatal(err)
		}
		windows = append(windows, w)
		fill(agWatch, w, 5*time.Minute, 70)
		switch d {
		case 1, 3: // missing
		case 5: // Saturday: only the first six hours, coverage 0.25
			fill(agGarmin, Window{Start: w.Start, End: w.Start.Add(6 * time.Hour)}, 5*time.Minute, 60)
		default:
			fill(agGarmin, w, 5*time.Minute, 60)
		}
	}
	// Samples carry the UTC date by default; fix local_date to the Berlin date.
	for i := range in {
		in[i].LocalDate = midnightUTC(in[i].Start.In(mustLoc(t, "Europe/Berlin")))
	}
	results, err := r.ResolveWindows(windows, Series{"heart_rate": in}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for d, res := range results {
		fallback := d == 1 || d == 3 || d == 5
		want, sel, st := 60.0, "garmin", ResultDirect
		if fallback {
			want, sel, st = 70, "apple", ResultFallback
		}
		if res.Value != want || res.Selected != sel || res.Status != st || stHasWarning(res, WarnPreferredUnavailable, "garmin") != fallback {
			t.Errorf("day %d: %v %s %s warnings %v", d, res.Value, res.Selected, res.Status, res.Warnings)
		}
		if res.Groups[0].ID != "garmin" || (fallback && res.Groups[0].Status == StatusValid) || (!fallback && res.Groups[1].Status != StatusUnused) {
			t.Errorf("day %d statuses: %v", d, stStatuses(res))
		}
	}
	if g := results[5].Groups[0]; g.Status != StatusBelowQuality || g.Reason != ReasonCoverage {
		t.Errorf("saturday garmin: %s %s, want below_quality coverage", g.Status, g.Reason)
	}
}

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestExcludedAndUnmatchedSourcesListed(t *testing.T) {
	w := agHourWindow("2026-06-15T10:00:00Z")
	r := stHR(OpFirstAvailable)
	r.Groups = r.Groups[:2] // garmin, whoop
	relay := Source{Provider: "apple_health", OriginKey: "com.garmin.connect", Relayed: true}
	r.Exclude = []Selector{{Relayed: new(true)}}
	res := stResolve(t, r, w, Options{}, agSample(agGarmin, w.Start, 60), agSample(relay, w.Start, 61), agSample(agWatch, w.Start, 62), agSample(agWatch, w.Start.Add(time.Minute), 63))
	if len(res.Groups) != 4 || res.Groups[2].Status != StatusExcluded || res.Groups[3].Status != StatusNotInRule || res.Groups[3].Count != 2 || res.Groups[2].Group != -1 {
		t.Fatalf("groups: %+v", res.Groups)
	}
	if res.Inputs != (InputCounts{Grouped: 1, Excluded: 1, NotInRule: 2}) {
		t.Errorf("counts: %+v", res.Inputs)
	}
}

func TestPartialAndDefinitionChanged(t *testing.T) {
	day, _ := LocalDay(date("2026-06-15"), berlin)
	r := agRule("heart_rate", catalog.WindowLocalDay, OpFirstAvailable, agGroup("garmin", Selector{Provider: "garmin"}))
	in := []Input{agSample(agGarmin, instant("2026-06-15T06:00:00Z"), 60)}
	in[0].LocalDate = date("2026-06-15")
	res := stResolve(t, r, day, Options{Now: instant("2026-06-15T09:00:00Z")}, in...)
	if !res.Partial {
		t.Error("today must be partial")
	}
	// definition_changed needs a selection-only metric; select directly with a provider-scoped stand-in.
	sel := agRule("resting_heart_rate", catalog.WindowLocalDay, OpFirstAvailable, agGroup("garmin", Selector{Provider: "garmin"}), agGroup("whoop", Selector{Provider: "whoop"}))
	gvs := []GroupValue{{Group: 0, ID: "garmin", Status: StatusNoData}, {Group: 1, ID: "whoop", Status: StatusValid, Value: 52}}
	res, err := sel.Select(day, gvs, Options{Previous: "garmin"})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := catalog.Lookup("resting_heart_rate")
	if stHasWarning(res, WarnDefinitionChanged, "whoop") != !m.Poolable() {
		t.Errorf("definition_changed: %v (poolable %v)", res.Warnings, m.Poolable())
	}
}
