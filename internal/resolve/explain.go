package resolve

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// Explanation templates (docs/architecture/resolution.md#result-shape). Every explanation is
// built from these fixed sentences, one per strategy, status and extension, filled with group
// ids, values and counts. They say what was computed from which source and never interpret
// what a value means for health.
const (
	tplNoData       = "No value: no group had a valid value."
	tplNoEpisode    = "No value: no main sleep episode was found for this night."
	tplDirect       = "Used %s: %s."
	tplFallback     = "Fell back to %s: %s."
	tplFollowed     = "Used %s, the group %s selected for this window: %s."
	tplLatest       = "Latest of %s: %s at %s, %s."
	tplEarliest     = "Earliest of %s: %s from %s, %s."
	tplMean         = "Mean of %s: %s = %s."
	tplSum          = "Sum of %s: %s = %s."
	tplExtreme      = "%s of %s: %s."
	tplComposed     = "Sum of %s: %s = %s."
	tplNoStage      = "No value: %s was selected for this episode but has no stage data for %s; stages are never taken from another source."
	tplMissingCodes = "No stage data from the selected source for: %s."
	tplSetValue     = "Set manually to %s; the rule computed %s."
	tplForced       = "Source forced manually to %s; the rule computed %s."
	tplExcludedRows = "%s excluded manually before resolving."
	tplIgnored      = "%s did not change this window."
	tplSkipped      = "%s %s."
	tplNoDataFrom   = "No data from %s."
	tplUnused       = "Not needed: %s."
	tplContext      = "Inside %s, %s came first."
	tplStatMin      = "%s: lowest 5-minute mean, %s-%s."
	tplStatRolling  = "%s: lowest %s rolling mean, %s-%s."
	tplWear         = "Wear gate (%s): %s."
	tplFollowMiss   = "%s selected %s for this window, which had no value here, so this rule's own order applied (follow_unavailable)."
	tplFollowNone   = "%s had no selected group for this window, so this rule's own order applied (follow_unavailable)."
	tplInsufficient = "Fewer sources than the %d required (insufficient_sources)."
	tplSumRisk      = "Adding sources may count the same activity twice (cross_source_sum_duplicate_risk)."
	tplComposite    = "The hour-by-hour total is larger than any single source's day (composite_exceeds_any_source)."
	tplDefinition   = "The selected group differs from the previous window's, so values may not be comparable (definition_changed)."
	tplExcluded     = "Excluded by the rule: %s."
	tplNotInRule    = "Outside the rule: %s."
	tplPartial      = "The window is still open; the value is partial."
)

// explain renders the explanation of one result from the templates above.
func explain(r *Rule, res Resolved, out Result) string {
	e := explainer{r: r, res: res, out: out, loc: windowLocation(res.Window)}
	var s []string
	add := func(format string, a ...any) { s = append(s, fmt.Sprintf(format, a...)) }

	// Overrides first: they decide the value.
	var excludedRows int
	var set, forced *Override
	for i, o := range res.Overrides {
		switch o.Action {
		case ExcludeInput:
			excludedRows++
		case SetValue:
			set = &res.Overrides[i]
		case ForceSource:
			forced = &res.Overrides[i]
		}
	}
	computed := ""
	if res.Computed != nil {
		computed = e.computedText(*res.Computed)
	}
	switch {
	case set != nil:
		add(tplSetValue, e.value(set.Value), computed)
	case forced != nil:
		add(tplForced, forced.Group, computed)
	}
	if excludedRows > 0 {
		add(tplExcludedRows, plural(excludedRows, "input row"))
	}
	if len(res.Ignored) > 0 {
		add(tplIgnored, plural(len(res.Ignored), "override"))
	}

	if set == nil {
		s = append(s, e.main()...)
	}
	s = append(s, e.skipped()...)
	if u := e.unused(); u != "" {
		add(tplUnused, u)
	}
	s = append(s, e.extensions()...)
	for _, w := range res.Warnings {
		switch w.Code {
		case WarnInsufficientSources:
			add(tplInsufficient, max(1, r.Strategy.MinSources))
		case WarnCrossSourceSum:
			if !slices.ContainsFunc(s, func(x string) bool { return x == tplSumRisk }) {
				add(tplSumRisk)
			}
		case WarnCompositeExceeds:
			add(tplComposite)
		case WarnDefinitionChanged:
			add(tplDefinition)
		case WarnPreferredUnavailable, WarnFollowUnavailable: // told by the fallback and follow sentences
		}
	}
	if x := e.sourcesText(StatusExcluded); x != "" {
		add(tplExcluded, x)
	}
	if x := e.sourcesText(StatusNotInRule); x != "" {
		add(tplNotInRule, x)
	}
	if res.Partial {
		add(tplPartial)
	}
	return strings.Join(s, " ")
}

type explainer struct {
	r   *Rule
	res Resolved
	out Result
	loc *time.Location
}

// main is the strategy sentence.
func (e explainer) main() []string {
	res, r := e.res, e.r
	if res.Status == ResultNoData {
		w := res.Window
		switch {
		case w.Kind == catalog.WindowLocalNight && !w.End.After(w.Start):
			return []string{tplNoEpisode}
		case len(e.out.Missing) == 1 && res.Selected != "":
			for code := range e.out.Missing {
				return []string{fmt.Sprintf(tplNoStage, res.Selected, code)}
			}
		}
		return []string{tplNoData}
	}
	used := e.used()
	if len(res.Hours) > 0 {
		return []string{fmt.Sprintf(tplComposed, plural(e.valuedHours(), "hourly value"), e.hoursText(), e.resultValue())}
	}
	var out []string
	switch {
	case res.Selected != "":
		g := e.group(res.Selected)
		v := e.groupValue(g)
		switch {
		case r.Follow != "" && res.FollowGroup == res.Selected && !e.warned(WarnFollowUnavailable):
			out = append(out, fmt.Sprintf(tplFollowed, g.ID, r.Follow, v))
		case r.Strategy.Op == OpLatest:
			out = append(out, fmt.Sprintf(tplLatest, plural(e.eligible(), "eligible group"), g.ID, e.clock(g.At), v))
		case r.Strategy.Op == OpEarliest:
			out = append(out, fmt.Sprintf(tplEarliest, plural(e.eligible(), "eligible group"), g.ID, e.clock(g.First), v))
		case e.statusBeforeOverride() == ResultFallback:
			out = append(out, fmt.Sprintf(tplFallback, g.ID, v))
		default:
			out = append(out, fmt.Sprintf(tplDirect, g.ID, v))
		}
	default:
		var parts []string
		for _, g := range used {
			p := g.ID + " " + e.groupValue(g)
			if (r.Strategy.Op == OpMin || r.Strategy.Op == OpMax) && g.Value == res.Value {
				p += ", selected"
			}
			parts = append(parts, p)
		}
		switch r.Strategy.Op {
		case OpMean:
			out = append(out, fmt.Sprintf(tplMean, plural(len(used), "source"), strings.Join(parts, ", "), e.resultValue()))
		case OpSum:
			out = append(out, fmt.Sprintf(tplSum, plural(len(used), "source"), strings.Join(parts, ", "), e.resultValue()))
		case OpMin:
			out = append(out, fmt.Sprintf(tplExtreme, "Minimum", plural(len(used), "source"), strings.Join(parts, "; ")))
		case OpMax:
			out = append(out, fmt.Sprintf(tplExtreme, "Maximum", plural(len(used), "source"), strings.Join(parts, "; ")))
		default: // selecting ops have Selected set
		}
	}
	if len(e.out.Missing) > 0 {
		codes := make([]string, 0, len(e.out.Missing))
		for c := range e.out.Missing {
			codes = append(codes, c)
		}
		slices.Sort(codes)
		out = append(out, fmt.Sprintf(tplMissingCodes, strings.Join(codes, ", ")))
	}
	return out
}

// statusBeforeOverride is the strategy's own status: overridden hides direct versus fallback.
func (e explainer) statusBeforeOverride() ResultStatus {
	if e.res.Status == ResultOverridden && e.res.Computed != nil {
		if e.res.Computed.Selected == e.res.Selected {
			return e.res.Computed.Status
		}
		return ResultDirect
	}
	return e.res.Status
}

// skipped describes the rule groups without a valid value, in ladder order; groups that
// simply had no rows share one sentence.
func (e explainer) skipped() []string {
	var out, empty []string
	for _, g := range e.res.Groups {
		if g.Group < 0 && g.ID == "" {
			continue
		}
		var why string
		switch g.Status {
		case StatusNoData, StatusBelowQuality, StatusStale, StatusNoStageData:
			if g.Status == StatusNoStageData && e.res.Selected == g.ID {
				continue // told by the main sentence
			}
			why = e.reason(g)
		case StatusUnused:
			if g.Reason == ReasonInsufficient || g.Reason == ReasonOverridden {
				why = e.reason(g)
			}
		default: // used, or a source entry
		}
		switch {
		case why == reasonNoData:
			empty = append(empty, g.ID)
		case why != "":
			out = append(out, fmt.Sprintf(tplSkipped, g.ID, why))
		}
	}
	if len(empty) > 0 {
		out = append([]string{fmt.Sprintf(tplNoDataFrom, strings.Join(empty, ", "))}, out...)
	}
	return out
}

const reasonNoData = "had no data"

// reason is the phrase for a group's status and reason.
func (e explainer) reason(g GroupValue) string {
	q := e.r.Quality
	if q == nil {
		q = &Quality{}
	}
	switch g.Status {
	case StatusStale:
		return fmt.Sprintf("was stale: its latest value is from %s, older than %s", e.stamp(g.At), q.MaxStaleness)
	case StatusNoStageData:
		return "has no stage data"
	default:
	}
	switch g.Reason {
	case ReasonOnlyDailyTotal:
		if e.r.Compose != nil {
			return "sent only a daily total, which cannot fill hours"
		}
		return "sent only a daily total, which this rule does not use"
	case ReasonNotReported:
		return "does not report " + e.r.Metric
	case ReasonImplausible:
		return "had only values outside the plausible range"
	case ReasonFlagged:
		return fmt.Sprintf("had only rows with excluded flags (%s)", strings.Join(q.ExcludeFlags, ", "))
	case ReasonCoverage:
		minCov := 0.0
		if q.MinCoverage != nil {
			minCov = *q.MinCoverage
		}
		return fmt.Sprintf("covered %s of the window, below the minimum of %s", percent(g.Coverage), percent(minCov))
	case ReasonNotWorn:
		return "had rows only while its device was not worn"
	case ReasonNoFullSpan:
		return fmt.Sprintf("had no fully covered %s span", e.r.withinSource().Span)
	case SleepPartialEpisode:
		return fmt.Sprintf("covered %s of the sleep episode, below the minimum of %s", percent(g.Coverage), percent(e.r.sleepQuality().minCoverage))
	case ReasonInsufficient:
		return "was not used: too few sources"
	case ReasonOverridden:
		return "was replaced by a manual override"
	}
	return reasonNoData
}

// unused lists valid groups the strategy did not need.
func (e explainer) unused() string {
	var ids []string
	for _, g := range e.res.Groups {
		if g.Status == StatusUnused && g.Reason != ReasonInsufficient && g.Reason != ReasonOverridden {
			ids = append(ids, g.ID)
		}
	}
	return strings.Join(ids, ", ")
}

// extensions are the sentences of the E1, E2, E3 and E5 inputs.
func (e explainer) extensions() []string {
	res, r := e.res, e.r
	var out []string
	if res.Context != "" {
		var ids []string
		for _, id := range r.Contexts[res.Context] {
			if id == ContextWorkoutSource {
				if res.WorkoutGroup == "" {
					continue
				}
				id = res.WorkoutGroup
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		where := "a workout"
		if res.Context == ContextSleep {
			where = "the sleep episode"
		}
		out = append(out, fmt.Sprintf(tplContext, where, strings.Join(ids, ", ")))
	}
	for _, g := range res.Groups {
		if g.Status != StatusSelected || g.SpanStart.IsZero() {
			continue
		}
		switch g.Basis {
		case BasisMin:
			out = append(out, fmt.Sprintf(tplStatMin, g.ID, e.clock(g.SpanStart), e.clock(g.SpanEnd)))
		case BasisMinRollingMean:
			out = append(out, fmt.Sprintf(tplStatRolling, g.ID, r.withinSource().Span, e.clock(g.SpanStart), e.clock(g.SpanEnd)))
		default:
		}
	}
	if q := r.Quality; q != nil && q.RequireWear != "" {
		var parts []string
		for _, g := range res.Groups {
			if g.ID == "" || (g.Count == 0 && g.Status != StatusSelected && g.Status != StatusUnused && g.Reason != ReasonNotWorn) {
				continue
			}
			if g.WearExempt {
				parts = append(parts, g.ID+" exempt (no "+q.RequireWear+" rows)")
			} else {
				parts = append(parts, fmt.Sprintf("%s worn in %d buckets", g.ID, g.WornBuckets))
			}
		}
		if len(parts) > 0 {
			out = append(out, fmt.Sprintf(tplWear, q.RequireWear, strings.Join(parts, ", ")))
		}
	}
	if r.Follow != "" && e.warned(WarnFollowUnavailable) {
		if res.FollowGroup == "" {
			out = append(out, fmt.Sprintf(tplFollowNone, r.Follow))
		} else {
			out = append(out, fmt.Sprintf(tplFollowMiss, r.Follow, res.FollowGroup))
		}
	}
	return out
}

func (e explainer) warned(c Warning) bool {
	return slices.ContainsFunc(e.res.Warnings, func(w WindowWarning) bool { return w.Code == c })
}

// used are the groups the strategy used, in ladder order.
func (e explainer) used() []GroupValue {
	var out []GroupValue
	for _, g := range e.res.Groups {
		if g.Status == StatusSelected {
			out = append(out, g)
		}
	}
	return out
}

// eligible counts the groups that had a valid value.
func (e explainer) eligible() int {
	n := 0
	for _, g := range e.res.Groups {
		if g.Status == StatusSelected || g.Status == StatusUnused && g.Reason != ReasonInsufficient {
			n++
		}
	}
	return n
}

func (e explainer) group(id string) GroupValue {
	for _, g := range e.res.Groups {
		if g.ID == id {
			return g
		}
	}
	return GroupValue{ID: id}
}

// groupValue is a group's value with its basis, e.g. "64.1 bpm (120 samples in 12 buckets)".
func (e explainer) groupValue(g GroupValue) string {
	v := e.value(g.Value)
	if g.Components != nil {
		v = e.components(g.Components)
	}
	var basis string
	switch g.Basis {
	case BasisBucketMeans:
		basis = plural(g.Count, "sample") + " in " + plural(g.Buckets, "bucket")
	case BasisIntervals:
		basis = plural(g.Count, "interval")
		if g.Count == 0 {
			basis = "worn, no rows"
		}
		if g.Prorated {
			basis += ", pro-rated at the window edges"
		}
	case BasisDailyValue:
		basis = "daily total"
	case BasisLatest:
		basis = "latest reading"
	case BasisMean:
		basis = "mean of " + plural(g.Readings, "reading")
	case BasisMin, BasisMinRollingMean:
		basis = plural(g.Count, "sample")
	case BasisSessions:
		basis = plural(g.Count, "session") + ", " + percent(g.Coverage) + " of the episode"
	}
	if basis == "" {
		return v
	}
	return v + " (" + basis + ")"
}

func (e explainer) resultValue() string {
	if e.res.Components != nil {
		return e.components(e.res.Components)
	}
	return e.value(e.res.Value)
}

// computedText is the rule's own result, e.g. "62 bpm (fallback to garmin)".
func (e explainer) computedText(c WindowResult) string {
	switch {
	case c.Status == ResultNoData:
		return "no value"
	case c.Components != nil:
		return e.components(c.Components) + " (" + string(c.Status) + ")"
	case c.Selected != "":
		return e.value(c.Value) + " (" + string(c.Status) + " from " + c.Selected + ")"
	}
	return e.value(c.Value) + " (" + string(c.Status) + ")"
}

// hoursText summarises a composed day's picks: hours per group, then hours without a value.
func (e explainer) hoursText() string {
	var ids []string
	n := map[string]int{}
	sum := map[string]float64{}
	empty := 0
	for _, h := range e.res.Hours {
		if h.Status == ResultNoData {
			empty++
			continue
		}
		id := h.Selected
		if id == "" {
			id = "several groups"
		}
		if n[id] == 0 {
			ids = append(ids, id)
		}
		n[id]++
		sum[id] += h.Value
	}
	var parts []string
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%s %d h (%s)", id, n[id], e.value(sum[id])))
	}
	if empty > 0 {
		parts = append(parts, fmt.Sprintf("%d h without a value", empty))
	}
	return strings.Join(parts, ", ")
}

func (e explainer) valuedHours() int {
	n := 0
	for _, h := range e.res.Hours {
		if h.Status != ResultNoData {
			n++
		}
	}
	return n
}

// sourcesText lists excluded or unmatched sources with their row counts and reasons.
func (e explainer) sourcesText(st GroupStatus) string {
	var parts []string
	for i, g := range e.res.Groups {
		if g.Status != st {
			continue
		}
		p := ""
		if len(g.Sources) > 0 {
			p = sourceLabel(g.Sources[0])
		}
		unit := "row"
		if g.Basis == BasisSessions {
			unit = "session"
		}
		p += " (" + plural(g.Count, unit)
		if reason := e.out.Inputs[i].Reason; reason != "" {
			p += ", " + reason
		}
		parts = append(parts, p+")")
	}
	return strings.Join(parts, "; ")
}

// sourceLabel names a source by provider, origin and device type.
func sourceLabel(s Source) string {
	parts := []string{s.Provider}
	if s.OriginKey != "" {
		parts = append(parts, s.OriginKey)
	}
	if s.DeviceType != "" {
		parts = append(parts, s.DeviceType)
	}
	if s.Manual && s.Provider != "manual" {
		parts = append(parts, "manual")
	}
	return strings.Join(parts, " ")
}

// value formats a canonical value with the result's unit: seconds as hours and minutes,
// counts without a unit, and other units after the number.
func (e explainer) value(v float64) string {
	switch u := e.out.Unit; u {
	case "s":
		return duration(v)
	case "count", "":
		return number(v)
	case "%":
		return number(v) + "%"
	default:
		return number(v) + " " + u
	}
}

// components formats family values in catalogue order: "bp_systolic 121, bp_diastolic 79".
// More than three are summarised by their number.
func (e explainer) components(c map[string]float64) string {
	if len(c) > 3 {
		return fmt.Sprintf("%d values", len(c))
	}
	var parts []string
	for _, m := range catalog.Metrics() {
		if v, ok := c[m.Code]; ok {
			s := number(v)
			if m.Unit == "s" {
				s = duration(v)
			}
			parts = append(parts, m.Code+" "+s)
		}
	}
	return strings.Join(parts, ", ")
}

func (e explainer) clock(t time.Time) string { return t.In(e.loc).Format("15:04") }

func (e explainer) stamp(t time.Time) string { return t.In(e.loc).Format("2006-01-02 15:04") }

// windowLocation is the zone the window was built in, for local clock times.
func windowLocation(w Window) *time.Location {
	if !w.Start.IsZero() {
		return w.Start.Location()
	}
	return w.End.Location()
}

// number rounds to two decimals, drops trailing zeros and groups thousands: 11342 -> "11,342".
func number(v float64) string {
	s := strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String()
	if frac != "" {
		out += "." + frac
	}
	if neg && out != "0" {
		out = "-" + out
	}
	return out
}

// duration formats seconds as "7h 05m", or "45 s" below a minute.
func duration(sec float64) string {
	if sec < 60 {
		return number(sec) + " s"
	}
	m := int(math.Round(sec / 60))
	if m < 60 {
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%dh %02dm", m/60, m%60)
}

// plural counts a noun: plural(1, "source") = "1 source", plural(2, "source") = "2 sources".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// percent formats a ratio as a whole percentage: 0.42 -> "42%".
func percent(f float64) string { return strconv.Itoa(int(math.Round(f*100))) + "%" }
