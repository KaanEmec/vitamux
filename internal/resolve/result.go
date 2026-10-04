package resolve

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// StatusUsed is how a result presents a group the strategy used (StatusSelected): its value
// took part in the window value. ResultInput.Selected says whether that value is the result
// (every pooled group for mean and sum, the extreme one for minimum and maximum). In the
// all-sources view it marks a source inside a rule group.
const StatusUsed GroupStatus = "used"

// Result is one resolved window in the documented result shape
// (docs/architecture/resolution.md#result-shape) as plain data; the API (J10.3) serializes it.
// BuildResult makes it from a Resolved window.
type Result struct {
	Metric     string // what was asked for: a catalogue code (a sleep code included) or a family
	Status     ResultStatus
	Value      *float64               // canonical unit; nil without a value and for family results
	Components map[string]float64     // family results: code -> value
	Missing    map[string]GroupStatus // family codes without a value (sleep: no_stage_data)
	Unit       string                 // canonical unit of Metric; "" for a family
	Partial    bool
	Window     Window
	Rule       RuleInfo
	Selected   string // the group the value came from for selecting ops; "" when pooled or none
	// Coverage is the coverage of the groups the value came from: the selected group's, the mean
	// of the pooled ones, or the elapsed hours' mean for a composed day. 0 without a value.
	Coverage float64
	Inputs   []ResultInput // every rule group in ladder order, then excluded and unmatched sources
	Counts   InputCounts
	Warnings []WindowWarning
	// Extensions: the E1 context and the group @workout_source stood for, the E5 leader metric
	// and the group it selected, and the E9 hourly picks.
	Context      Context
	WorkoutGroup string
	Follow       string
	FollowGroup  string
	Hours        []HourPick
	// Overrides applied to the window and those that matched but changed nothing; Computed is
	// what the rule alone gave when an override was applied.
	Overrides   []Override
	Ignored     []Override
	Computed    *ComputedValue
	Explanation string
	ComputedAt  time.Time
	Sources     []SourceView // the all-sources drilldown, when requested (Request.Sources)
}

// RuleInfo identifies the rule version a result came from.
type RuleInfo struct {
	Ref      string // builtin:<metric>:<n> or rule:<metric>:<n>
	Version  int
	Strategy Op
}

// ResultInput is one rule group (or one excluded or unmatched source) of a result.
type ResultInput struct {
	Group      string      // rule group id; "" for excluded and not_in_rule sources
	Status     GroupStatus // used, fallback_unused, no_data, below_quality, stale, no_stage_data, excluded, not_in_rule
	Selected   bool        // the group's value is (part of) the window value
	Reason     string      // Reason* constant, or "exclude: <selector>" for an excluded source
	Value      *float64    // nil without a value; family groups use Components
	Components map[string]float64
	Basis      Basis
	Coverage   float64
	Count      int // contributing rows (sessions for sleep)
	Readings   int // latest-type metrics: contributing readings
	Prorated   bool
	At         time.Time // latest contributing instant
	// E2: the span a min or min_rolling_mean statistic picked. E3: the group is exempt from the
	// wear gate, or the number of elapsed base buckets in which it was worn.
	SpanStart, SpanEnd time.Time
	WearExempt         bool
	WornBuckets        int
	Sources            []Source
	RecordRefs         []int64     // measurements.id
	Sessions           []uuid.UUID // sleep_sessions.id
}

// HourPick is one hour of a composed day (E9).
type HourPick struct {
	Start  time.Time
	Status ResultStatus
	Group  string // the group picked for the hour; "" without a value
	Value  float64
}

// ComputedValue is the rule's own result for a window an override changed.
type ComputedValue struct {
	Status     ResultStatus
	Value      *float64
	Components map[string]float64
	Selected   string
}

// BuildResult renders one resolved window of metric under rule version v: the value, every
// group with its status, coverage, warnings, extension inputs, overrides and the explanation
// (explain.go). metric is what was asked for; it differs from the rule's for a sleep code.
func BuildResult(metric string, v Version, res Resolved, computedAt time.Time) Result {
	r := v.Rule
	out := Result{Metric: metric, Status: res.Status, Partial: res.Partial, Window: res.Window,
		Rule:     RuleInfo{Ref: v.Ref, Version: v.Version, Strategy: r.Strategy.Op},
		Selected: res.Selected, Counts: res.Inputs, Warnings: res.Warnings, Context: res.Context,
		WorkoutGroup: res.WorkoutGroup, Follow: r.Follow, FollowGroup: res.FollowGroup,
		Overrides: res.Overrides, Ignored: res.Ignored, Missing: res.Missing, ComputedAt: computedAt}
	if m, ok := catalog.Lookup(metric); ok {
		out.Unit = m.Unit
	}
	if res.Status != ResultNoData {
		if res.Components != nil {
			out.Components = res.Components
		} else {
			out.Value = new(res.Value)
		}
	}
	if c := res.Computed; c != nil && len(res.Overrides) > 0 {
		out.Computed = &ComputedValue{Status: c.Status, Components: c.Components, Selected: c.Selected}
		if c.Status != ResultNoData && c.Components == nil {
			out.Computed.Value = new(c.Value)
		}
	}
	for _, h := range res.Hours {
		out.Hours = append(out.Hours, HourPick{Start: h.Window.Start, Status: h.Status, Group: h.Selected, Value: h.Value})
	}
	setValue := slices.ContainsFunc(res.Overrides, func(o Override) bool { return o.Action == SetValue })
	for _, g := range res.Groups {
		in := ResultInput{Group: g.ID, Status: g.Status, Reason: g.Reason, Basis: g.Basis, Coverage: g.Coverage,
			Count: g.Count, Readings: g.Readings, Prorated: g.Prorated, At: g.At, SpanStart: g.SpanStart, SpanEnd: g.SpanEnd,
			WearExempt: g.WearExempt, WornBuckets: g.WornBuckets, Sources: g.Sources, RecordRefs: g.Refs, Sessions: g.Sessions}
		if g.Status == StatusSelected {
			in.Status = StatusUsed
			in.Selected = !setValue && selectedInput(r, res.WindowResult, g)
		}
		if g.Status == StatusExcluded && len(g.Sources) > 0 {
			if i := r.Assign(g.Sources[0]).Exclude; i >= 0 {
				in.Reason = "exclude: " + selectorText(r.Exclude[i])
			}
		}
		if hasValue(g) {
			if g.Components != nil {
				in.Components = g.Components
			} else {
				in.Value = new(g.Value)
			}
		}
		out.Inputs = append(out.Inputs, in)
	}
	out.Coverage = resultCoverage(res.WindowResult, computedAt)
	out.Explanation = explain(r, res, out)
	return out
}

// hasValue reports whether a group has a value to show: it has rows (or is a measured zero
// under the wear gate) and its code is not missing (sleep groups without stage data).
func hasValue(g GroupValue) bool {
	switch {
	case g.Status == StatusNoData || g.Status == StatusNoStageData,
		g.Reason == string(StatusNoStageData) || g.Reason == string(StatusNoData):
		return false
	}
	return g.Count > 0 || g.Status == StatusSelected || g.Status == StatusUnused
}

// selectedInput reports whether a used group's value is the window value: the selected group
// for selecting ops, every pooled group for mean and sum, the extreme ones for min and max, and
// every hour pick for a composed day.
func selectedInput(r *Rule, res WindowResult, g GroupValue) bool {
	switch {
	case res.Selected != "":
		return g.ID == res.Selected
	case len(res.Hours) > 0:
		return true
	}
	switch r.Strategy.Op {
	case OpMin, OpMax:
		return g.Value == res.Value
	case OpMean, OpSum:
		return true
	default:
		return false
	}
}

// resultCoverage is the coverage of the groups behind the value (see Result.Coverage).
func resultCoverage(res WindowResult, now time.Time) float64 {
	if res.Status == ResultNoData {
		return 0
	}
	if len(res.Hours) > 0 {
		sum, n := 0.0, 0
		for _, h := range res.Hours {
			if !now.IsZero() && !h.Window.Start.Before(now) {
				continue
			}
			sum += resultCoverage(h, now)
			n++
		}
		if n == 0 {
			return 0
		}
		return sum / float64(n)
	}
	sum, n := 0.0, 0
	for _, g := range res.Groups {
		if g.Status == StatusSelected {
			sum += g.Coverage
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// SourcesUsed lists the groups the results took their values from, in first-use order: the
// sources_used of a series summary (edge case 9).
func SourcesUsed(rs []Result) []string {
	var out []string
	for _, r := range rs {
		for _, in := range r.Inputs {
			if in.Selected && in.Group != "" && !slices.Contains(out, in.Group) {
				out = append(out, in.Group)
			}
		}
	}
	return out
}

// SourceView is one source of the all-sources drilldown (docs/architecture/api.md#example-all-sources-drilldown):
// every source with rows in the window, inside the rule or not, with its own values.
type SourceView struct {
	Group      string      // rule group id; "" when excluded or outside the rule
	RuleStatus GroupStatus // used (in a rule group), excluded or not_in_rule
	Reason     string      // "exclude: <selector>" for an excluded source
	Source     Source
	// Values are the source's own values in the window by basis: daily_value, interval_sum and
	// intervals (additive); samples and bucket_means (intensive); latest and readings (latest
	// metrics, per component code for a family); sessions, sleep_in_bed and sleep_total (sleep).
	Values     map[string]float64
	Count      int
	RecordRefs []int64
	Sessions   []uuid.UUID
}

// BuildSources lists every source with rows of the rule's metric in w, in input order, with
// its group under r and its own values. It reads the same Series ResolveWindow does.
func BuildSources(r *Rule, w Window, s Series, now time.Time) ([]SourceView, error) {
	sp, err := r.spec()
	if err != nil {
		return nil, err
	}
	var order []Source
	bySrc := map[Source]Series{}
	for _, code := range sp.codes {
		for _, x := range s[code] {
			if !w.Includes(x) {
				continue
			}
			if _, ok := bySrc[x.Source]; !ok {
				order = append(order, x.Source)
				bySrc[x.Source] = Series{}
			}
			bySrc[x.Source][code] = append(bySrc[x.Source][code], x)
		}
	}
	base := baseBucket(sp.primary(), w)
	out := make([]SourceView, 0, len(order))
	for _, src := range order {
		v := r.sourceView(src)
		rows := bySrc[src]
		in := rows[sp.codes[0]]
		v.Values = map[string]float64{}
		switch sp.agg {
		case catalog.Latest:
			var gv GroupValue
			gv.aggReadings(sp, rows, w, StatLatest)
			v.Values["readings"] = float64(countReadings(sp, rows))
			if sp.family {
				maps.Copy(v.Values, gv.Components)
			} else if gv.Count > 0 {
				v.Values["latest"] = gv.Value
			}
		case catalog.Additive:
			var daily, iv GroupValue
			var dailyRows []Input
			for _, x := range in {
				if x.Kind == catalog.DailyValue {
					dailyRows = append(dailyRows, x)
				}
			}
			if len(dailyRows) > 0 {
				daily.aggDaily(dailyRows, IntraAuto)
				v.Values["daily_value"] = daily.Value
			}
			iv.aggAdditive(in, w, base, WithinSource{DailyValuePolicy: IntervalsOnly}, now, nil)
			if iv.Count > 0 {
				v.Values["interval_sum"], v.Values["intervals"] = iv.Value, float64(iv.Count)
			}
		case catalog.Intensive:
			var gv GroupValue
			gv.aggIntensive(in, w, base, IntraAuto, now, nil)
			if gv.Count > 0 {
				v.Values["samples"], v.Values["bucket_means"] = float64(gv.Count), gv.Value
			}
		case catalog.DailySummary:
			var gv GroupValue
			gv.aggDailySummary(in)
			if gv.Count > 0 {
				v.Values[string(gv.Basis)] = gv.Value
			}
		default: // sleep codes have no rows; see SleepAlignment.Sources
		}
		for _, code := range sp.codes {
			for _, x := range rows[code] {
				v.Count++
				v.RecordRefs = append(v.RecordRefs, x.ID)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// Sources is the all-sources drilldown of a sleep episode: every source with a session of the
// night overlapping e, with its session count and summed in-bed and asleep time.
func (a SleepAlignment) Sources(e Episode) []SourceView {
	var all []SleepInput
	for _, s := range a.byID {
		all = append(all, s)
	}
	all = append(append(all, a.Excluded...), a.NotInRule...)
	slices.SortStableFunc(all, func(x, y SleepInput) int {
		if c := x.Start.Compare(y.Start); c != 0 {
			return c
		}
		return strings.Compare(x.ID.String(), y.ID.String())
	})
	var out []SourceView
	idx := map[Source]int{}
	for _, s := range all {
		if !s.End.After(e.Start) || !s.Start.Before(e.End) {
			continue
		}
		i, ok := idx[s.Source]
		if !ok {
			i = len(out)
			idx[s.Source] = i
			out = append(out, a.rule.sourceView(s.Source))
			out[i].Values = map[string]float64{}
		}
		v := &out[i]
		v.Count++
		v.Sessions = append(v.Sessions, s.ID)
		v.Values["sessions"]++
		v.Values["sleep_in_bed"] += s.End.Sub(s.Start).Seconds()
		if s.Totals.Asleep != nil {
			v.Values["sleep_total"] += float64(*s.Totals.Asleep)
		}
	}
	return out
}

// sourceView places a source under the rule.
func (r *Rule) sourceView(src Source) SourceView {
	v := SourceView{Source: src, RuleStatus: StatusNotInRule}
	switch a := r.Assign(src); a.Membership() {
	case Excluded:
		v.RuleStatus, v.Reason = StatusExcluded, "exclude: "+selectorText(r.Exclude[a.Exclude])
	case Grouped:
		v.RuleStatus, v.Group = StatusUsed, r.Groups[a.Group].ID
	case NotInRule:
	}
	return v
}

// countReadings counts the distinct readings (measurement groups, or single rows) in s.
func countReadings(sp spec, s Series) int {
	seen := map[string]bool{}
	for _, code := range sp.codes {
		for _, x := range s[code] {
			seen[readingKey(x)] = true
		}
	}
	return len(seen)
}

// selectorText renders the set fields of a selector, e.g. "provider=apple_health relayed=true".
func selectorText(s Selector) string {
	var parts []string
	add := func(k, v string) {
		if v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	add("provider", s.Provider)
	add("connection_id", s.ConnectionID)
	add("origin_key", s.OriginKey)
	add("origin_key_prefix", s.OriginKeyPrefix)
	add("origin_name", s.OriginName)
	if s.Relayed != nil {
		add("relayed", fmt.Sprint(*s.Relayed))
	}
	add("device_type", s.DeviceType)
	add("device_model", s.DeviceModel)
	add("device_id", s.DeviceID)
	add("entry", string(s.Entry))
	return strings.Join(parts, " ")
}
