package resolve

import (
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// The rule extensions E1, E2, E3, E5 and E9 (docs/architecture/resolution.md#extensions). Each
// leaves its inputs in WindowResult and GroupValue as plain fields; J09.8 renders them.

// Bases of the E2 window statistics.
const (
	BasisMinRollingMean Basis = "min_rolling_mean" // lowest mean of span/base consecutive bucket means
	BasisMin            Basis = "min"              // lowest bucket mean
)

// Reasons the extensions add.
const (
	ReasonNotWorn     = "not_worn"     // E3: the group's rows fall only where its device was not worn
	ReasonNotReported = "not_reported" // E3: the group's worn devices do not report the metric
	ReasonNoFullSpan  = "no_full_span" // E2: no run of covered buckets as long as within_source.span
)

// opFollow is the internal op of a follower that takes the leader's group (E5).
const opFollow Op = "follow"

// ---- E1 contexts

// ContextEvents are the aligned events that put a window in an E1 context: the episodes of the
// sleep-family alignment (SleepAlignment.Episodes) and the workout clusters (ClusterWorkouts).
// Callers load them for the span they resolve.
type ContextEvents struct {
	Sleep    []Episode
	Workouts []WorkoutCluster
}

// ContextAt returns the E1 context of w and, inside a workout, the group of the workout the rule
// picks from the cluster (Rule.PickWorkout; -1 when none of the cluster is in a group), which
// stands for ContextWorkoutSource. local_night and sleep_episode windows are sleep windows.
// Any other window is in a context when one event covers at least half of it; workouts first.
// latest and reading windows have none. Contexts only reorder the ladder (Rule.Ladder).
func (r *Rule) ContextAt(w Window, ev ContextEvents) (Context, int) {
	switch w.Kind {
	case catalog.WindowLocalNight, catalog.WindowSleepEpisode:
		return ContextSleep, -1
	case catalog.WindowLatest, catalog.WindowReading:
		return "", -1
	case catalog.WindowBucket, catalog.WindowHour, catalog.WindowLocalDay:
	}
	span := w.End.Sub(w.Start)
	covers := func(start, end time.Time) bool {
		return span > 0 && 2*timeMin(end, w.End).Sub(timeMax(start, w.Start)) >= span
	}
	for _, c := range ev.Workouts {
		if covers(c.Start, c.End) {
			if p, ok := r.PickWorkout(c); ok {
				return ContextWorkout, p.Group
			}
			return ContextWorkout, -1
		}
	}
	for _, e := range ev.Sleep {
		if covers(e.Start, e.End) {
			return ContextSleep, -1
		}
	}
	return "", -1
}

// windowContext is Options.Context when set, else ContextAt over Options.Events.
func (r *Rule) windowContext(w Window, opt Options) (Context, int) {
	ctx, wg := r.ContextAt(w, opt.Events)
	if opt.Context != "" && opt.Context != ctx {
		return opt.Context, -1
	}
	return ctx, wg
}

// ---- E2 window statistics and derived codes

// derivedSpec resolves a derived code from its source metric's series: Series holds the source
// code (heart_rate for resting_heart_rate_nocturnal), whose rows and plausible range apply.
// The rule needs a min statistic, which is what defines the code.
func (r *Rule) derivedSpec(m catalog.Metric) (spec, error) {
	src, ok := catalog.Lookup(m.DerivedFrom)
	if !ok {
		return spec{}, fmt.Errorf("resolve: %s derives from unknown metric %q", m.Code, m.DerivedFrom)
	}
	if st := r.withinSource().Statistic; st != StatMinRollingMean && st != StatMin {
		return spec{}, fmt.Errorf("resolve: derived code %s needs within_source.statistic min_rolling_mean or min", m.Code)
	}
	return spec{agg: src.Agg, codes: []string{src.Code}, byCode: map[string]catalog.Metric{src.Code: src}, named: m}, nil
}

// statisticApplies reports whether an E2 statistic applies to a window kind. Other kinds the
// metric allows keep the aggregation's plain value (ADR-0008).
func statisticApplies(st Statistic, k catalog.Window) bool {
	night := k == catalog.WindowLocalDay || k == catalog.WindowLocalNight || k == catalog.WindowSleepEpisode
	switch st {
	case StatMinRollingMean:
		return night
	case StatMin:
		return night || k == catalog.WindowHour
	case StatLatest, StatMean:
	}
	return false
}

// windowStatistic replaces an intensive group's mean with the E2 statistic over its covered
// bucket means (keys are bucket starts in order): min is the lowest bucket, min_rolling_mean the
// lowest mean of span/base consecutive covered buckets (a gap breaks a span, so sparse data
// cannot fake a minimum). Ties go to the earliest span. Coverage stays covered / elapsed
// buckets for the min_coverage gate. It reports false when no span is fully covered.
func (gv *GroupValue) windowStatistic(ws WithinSource, keys []int64, means []float64, base time.Duration) bool {
	n := 1
	gv.Basis = BasisMin
	if ws.Statistic == StatMinRollingMean {
		n = max(1, int(ws.Span.Std()/base))
		gv.Basis = BasisMinRollingMean
	}
	best, at := 0.0, -1
	for i := 0; i+n <= len(keys); i++ {
		if keys[i+n-1]-keys[i] != int64(n-1)*int64(base) {
			continue
		}
		sum := 0.0
		for _, m := range means[i : i+n] {
			sum += m
		}
		// Ties within float noise keep the earliest span, so the choice does not depend on the
		// summation order of equal bucket means (which differs between platforms and PG versions).
		if v := sum / float64(n); at < 0 || v < best-1e-9 {
			best, at = v, i
		}
	}
	if at < 0 {
		return false
	}
	gv.Value = best
	gv.SpanStart = time.Unix(0, keys[at]).UTC()
	gv.SpanEnd = time.Unix(0, keys[at+n-1]).UTC().Add(base)
	return true
}

// ---- E3 wear gate

// WearLookback is how far before a window callers load the wear metric and Series[Reporting].
// A device with no wear rows in the supplied series counts as one that never reports it and is
// exempt, so a watch left off for a whole day still needs its earlier wear rows in the series
// to be gated.
const WearLookback = 30 * 24 * time.Hour

// Reporting is the Series entry of the sources that report the rule's metric, under
// quality.require_wear: one Input (Source and Start) per source and day with rows of the metric,
// loaded like the wear series. A worn device stands for its group only when it reports the
// metric there or has rows in the series, so a band that never counts steps cannot make a
// group's steps a measured 0.
const Reporting = "@reporting"

// wearKey identifies a device for the wear gate: the device row when known, else the source.
type wearKey struct {
	device uuid.UUID
	source Source
}

func wearKeyOf(src Source) wearKey {
	if src.DeviceID != uuid.Nil {
		return wearKey{device: src.DeviceID}
	}
	return wearKey{source: src}
}

// wearIndex is the wear series of quality.require_wear: per device the instants of its wear
// rows in order, per rule group the devices whose wear rows the rule assigns to it and that
// report the metric, and the groups with a worn device that does not.
type wearIndex struct {
	at     map[wearKey][]int64
	groups []map[wearKey]bool
	silent []bool
}

// newWear indexes s[quality.require_wear]; nil without the gate. Manual and implausible rows
// are not wear. A row counts at its start. A device reports the metric when it has a row of
// sp's codes or an entry in s[Reporting].
func (r *Rule) newWear(sp spec, s Series) *wearIndex {
	if r.Quality == nil || r.Quality.RequireWear == "" {
		return nil
	}
	reports := map[wearKey]bool{}
	for _, code := range append([]string{Reporting}, sp.codes...) {
		for _, x := range s[code] {
			reports[wearKeyOf(x.Source)] = true
		}
	}
	wi := &wearIndex{at: map[wearKey][]int64{}, groups: make([]map[wearKey]bool, len(r.Groups)), silent: make([]bool, len(r.Groups))}
	assigned := map[Source]int{}
	for _, x := range s[r.Quality.RequireWear] {
		if x.Source.Manual || x.Flags&(normalize.FlagManualEntry|normalize.FlagImplausible) != 0 {
			continue
		}
		k := wearKeyOf(x.Source)
		wi.at[k] = append(wi.at[k], x.Start.UnixNano())
		g, ok := assigned[x.Source]
		if !ok {
			g = r.Assign(x.Source).Group
			assigned[x.Source] = g
		}
		switch {
		case g < 0:
		case !reports[k]:
			wi.silent[g] = true
		default:
			if wi.groups[g] == nil {
				wi.groups[g] = map[wearKey]bool{}
			}
			wi.groups[g][k] = true
		}
	}
	for _, ts := range wi.at {
		slices.Sort(ts)
	}
	return wi
}

// without returns wi with the groups in skip left without devices (exempt), or wi itself.
func (wi *wearIndex) without(skip []bool) *wearIndex {
	if wi == nil || !slices.Contains(skip, true) {
		return wi
	}
	out := *wi
	out.groups = slices.Clone(wi.groups)
	for g, ok := range skip {
		if ok {
			out.groups[g] = nil
		}
	}
	return &out
}

// counts reports whether a row of src in the bucket [b, b+base) counts: always without the
// gate or for an exempt device, else only when the device has a wear row in the bucket.
func (wi *wearIndex) counts(src Source, b time.Time, base time.Duration) bool {
	if wi == nil {
		return true
	}
	ts, ok := wi.at[wearKeyOf(src)]
	return !ok || wornIn(ts, b, base)
}

func wornIn(ts []int64, b time.Time, base time.Duration) bool {
	lo := b.UnixNano()
	i, _ := slices.BinarySearch(ts, lo)
	return i < len(ts) && ts[i] < lo+int64(base)
}

// applyWear finishes a gated group value. The group's devices are those of its rows plus those
// whose wear rows the rule assigns to it and that report the metric; without one that reports
// wear the group is exempt (and skips the additive coverage gate), and without rows it is
// no_data, not_reported when a worn device of the group does not report the metric. Otherwise
// WornBuckets counts the elapsed base buckets in which one of them was worn. For additive
// metrics coverage becomes worn / elapsed buckets, a group worn without rows is a valid 0
// (worn, no steps) instead of missing, and a daily value without any worn bucket is not_worn.
// Rows only in unworn buckets leave the group not_worn.
func (gv *GroupValue) applyWear(wi *wearIndex, sp spec, w Window, in []Input, base time.Duration, now time.Time) {
	var devs [][]int64
	seen := map[wearKey]bool{}
	add := func(k wearKey) {
		if ts, ok := wi.at[k]; ok && !seen[k] {
			seen[k] = true
			devs = append(devs, ts)
		}
	}
	for _, x := range in {
		add(wearKeyOf(x.Source))
	}
	if gv.Group >= 0 && gv.Group < len(wi.groups) {
		for k := range wi.groups[gv.Group] { // order only changes which device is found first
			add(k)
		}
	}
	if len(devs) == 0 {
		gv.WearExempt = true
		if gv.Count == 0 && gv.Reason == "" && gv.Group >= 0 && gv.Group < len(wi.silent) && wi.silent[gv.Group] {
			gv.Reason = ReasonNotReported
		}
		return
	}
	first, end, n := elapsedSpan(w, base, now)
	var firstWorn, lastWorn time.Time
	for b := first; b.Before(end); b = b.Add(base) {
		for _, ts := range devs {
			if wornIn(ts, b, base) {
				gv.WornBuckets++
				if firstWorn.IsZero() {
					firstWorn = b
				}
				lastWorn = b
				break
			}
		}
	}
	notWorn := func() { gv.Status, gv.Reason = StatusBelowQuality, ReasonNotWorn }
	if sp.agg != catalog.Additive {
		if gv.Count == 0 && gv.Gated > 0 {
			notWorn()
		}
		return
	}
	cov := 0.0
	if n > 0 {
		cov = min(1, float64(gv.WornBuckets)/float64(n))
	}
	switch {
	case gv.WornBuckets == 0:
		if gv.Count > 0 || gv.Gated > 0 {
			notWorn()
		}
	case gv.Count > 0:
		gv.Coverage = cov
	case gv.Reason == "": // worn, no rows: a measured zero
		gv.Status, gv.Basis, gv.Value, gv.Coverage = StatusValid, BasisIntervals, 0, cov
		gv.First, gv.At = firstWorn, lastWorn.Add(base)
	}
}

// coverageGated reports whether min_coverage applies to gv: intensive bucket values (E2
// statistics included), and additive values under the wear gate unless the group is exempt.
func (r *Rule) coverageGated(sp spec, gv *GroupValue) bool {
	switch gv.Basis {
	case BasisBucketMeans, BasisMinRollingMean, BasisMin:
		return true
	case BasisIntervals, BasisDailyValue, BasisLatest, BasisMean, BasisSessions:
	}
	return sp.agg == catalog.Additive && r.Quality != nil && r.Quality.RequireWear != "" && !gv.WearExempt
}

// ---- E5 follow

// followPos returns the position of the valid group with the leader's id, or -1.
func followPos(leader string, groups []GroupValue) int {
	if leader == "" {
		return -1
	}
	for pos, g := range groups {
		if g.ID == leader && g.Status == StatusValid {
			return pos
		}
	}
	return -1
}

// ---- E9 hour composition

// composeDay resolves a local_day from its local hours (walked from the day's start, so DST
// days have 23 or 25). Each hour resolves on its own, with its context, wear gate and coverage
// gate, compose.op as the hourly pick, and no daily values; the day is the sum of the hour
// values and Hours keeps every hour. A group with only daily values that day is skipped in
// every hour (no_data, only_daily_total), never a worn 0. The day's groups add up their hours:
// the best status of any hour (selected, then fallback_unused, below_quality, stale, no_data),
// the summed value (that group's own day) and coverage weighted by elapsed time. A day picked
// from more than one group warns composite_exceeds_any_source when it is larger than every
// group's own day. A daily total is never added to hours: when a daily-only group with a valid
// value ranks above every group that supplied an hour, the day is that value (dailyDay).
func (r *Rule) composeDay(sp spec, w Window, s Series, opt Options, wear *wearIndex) (WindowResult, error) {
	h := *r
	h.Compose, h.Strategy = nil, Strategy{Op: OpFirstAvailable}
	if r.Compose.Op == ComposeMax {
		h.Strategy = Strategy{Op: OpMax}
	}
	hopt := opt
	hopt.Previous = ""
	res := WindowResult{Window: w, Op: h.Strategy.Op, Status: ResultNoData}
	day := make([]GroupValue, len(r.Groups))
	cov := make([]float64, len(r.Groups))
	for i := range day {
		day[i] = GroupValue{Group: i, ID: r.Groups[i].ID, Status: StatusNoData, WearExempt: true}
	}
	rows, excluded, unmatched, counts := r.windowRows(sp, w, s)
	dailyOnly := make([]bool, len(r.Groups))
	for g := range rows {
		dailyOnly[g] = onlyDaily(rows[g].kept)
	}
	hwear := wear.without(dailyOnly)
	supplied := make([]bool, len(r.Groups))
	var elapsed time.Duration
	picked := map[string]bool{}
	seen := map[WindowWarning]bool{}
	valued, direct := 0, true
	for t := w.Start; t.Before(w.End); t = t.Add(time.Hour) {
		hw := Window{Kind: catalog.WindowHour, Start: t, End: timeMin(t.Add(time.Hour), w.End), Date: w.Date, Key: t.UTC().Format(time.RFC3339)}
		hr, err := h.resolveWindow(sp, hw, s, hopt, hwear)
		if err != nil {
			return WindowResult{}, err
		}
		if r.Compose.Op == ComposeMax {
			pickMax(&hr)
		}
		span := hw.End.Sub(hw.Start)
		if !opt.Now.IsZero() {
			span = max(0, timeMin(hw.End, opt.Now).Sub(hw.Start))
		}
		elapsed += span
		for _, g := range hr.Groups {
			if g.Group >= 0 {
				mergeHour(&day[g.Group], g)
				cov[g.Group] += g.Coverage * float64(span)
				supplied[g.Group] = supplied[g.Group] || g.Status == StatusSelected
			}
		}
		if hr.Status != ResultNoData {
			valued++
			res.Value += hr.Value
			picked[hr.Selected] = true
			direct = direct && hr.Status == ResultDirect
		}
		for _, x := range hr.Warnings {
			if !seen[x] {
				seen[x] = true
				res.Warnings = append(res.Warnings, x)
			}
		}
		res.Hours = append(res.Hours, hr)
	}

	most := 0.0
	for i := range day {
		if elapsed > 0 {
			day[i].Coverage = cov[i] / float64(elapsed)
		}
		slices.Sort(day[i].Refs)
		day[i].Refs = slices.Compact(day[i].Refs)
		most = max(most, day[i].Value)
		if dailyOnly[i] && day[i].Status == StatusNoData {
			day[i].Reason = ReasonOnlyDailyTotal
		}
	}
	switch {
	case valued == 0:
	case len(picked) == 1 && !picked[""]:
		for id := range picked {
			res.Selected = id
		}
		res.Status = ResultFallback
		if direct {
			res.Status = ResultDirect
		}
	default:
		res.Status = ResultCalculated
	}
	if len(picked) > 1 && res.Value > most+1e-9 {
		res.Warnings = append(res.Warnings, WindowWarning{Code: WarnCompositeExceeds})
	}
	if err := r.dailyDay(sp, w, rows, dailyOnly, supplied, day, &res, opt.Now, wear); err != nil {
		return WindowResult{}, err
	}
	if res.Selected != "" && opt.Previous != "" && res.Selected != opt.Previous && r.selectionOnly() {
		res.Warnings = append(res.Warnings, WindowWarning{Code: WarnDefinitionChanged, Group: res.Selected})
	}
	for _, i := range r.Ladder("", -1) {
		res.Groups = append(res.Groups, day[i])
	}
	res.Groups = append(res.Groups, sourceEntries(excluded, StatusExcluded)...)
	res.Groups = append(res.Groups, sourceEntries(unmatched, StatusNotInRule)...)
	res.Inputs = counts
	res.Partial = !opt.Now.IsZero() && w.Partial(opt.Now)
	return res, nil
}

// onlyDaily reports whether a group's rows are all daily values (and there is one).
func onlyDaily(s Series) bool {
	n := 0
	for _, in := range s {
		for _, x := range in {
			if x.Kind != catalog.DailyValue {
				return false
			}
			n++
		}
	}
	return n > 0
}

// dailyDay makes a composed day the daily value of the first daily-only group in ladder order
// with a valid value, when it ranks above every group that supplied an hour: direct when it is
// the first group, else fallback. The hours are dropped (the day is not their sum) and the
// groups that supplied them become fallback_unused.
func (r *Rule) dailyDay(sp spec, w Window, rows []groupRows, dailyOnly, supplied []bool, day []GroupValue, res *WindowResult, now time.Time, wear *wearIndex) error {
	ladder := r.Ladder("", -1)
	for pos, g := range ladder {
		if supplied[g] {
			return nil
		}
		if !dailyOnly[g] {
			continue
		}
		gv, err := r.aggregate(sp, w, g, rows[g].kept, now, wear)
		if err != nil {
			return err
		}
		r.gateGroup(sp, w, &gv, rows[g], now)
		if gv.Status != StatusValid {
			continue
		}
		for i := range day {
			if day[i].Status == StatusSelected {
				day[i].Status = StatusUnused
			}
		}
		gv.Status = StatusSelected
		day[g] = gv
		res.Value, res.Selected, res.Status, res.Hours, res.Warnings = gv.Value, gv.ID, ResultDirect, nil, nil
		if pos > 0 {
			res.Status = ResultFallback
		}
		for _, a := range ladder[:pos] {
			res.Warnings = append(res.Warnings, WindowWarning{Code: WarnPreferredUnavailable, Group: r.Groups[a].ID})
		}
		return nil
	}
	return nil
}

// pickMax names the group behind an hourly max (compose op max): the first group in ladder
// order with the maximum stays selected, the other valid groups become fallback_unused.
func pickMax(hr *WindowResult) {
	if hr.Status != ResultCalculated {
		return
	}
	for i := range hr.Groups {
		g := &hr.Groups[i]
		if g.Status != StatusSelected {
			continue
		}
		if hr.Selected == "" && g.Value == hr.Value {
			hr.Selected = g.ID
			continue
		}
		g.Status = StatusUnused
	}
}

var hourRank = map[GroupStatus]int{StatusSelected: 5, StatusUnused: 4, StatusBelowQuality: 3, StatusStale: 2, StatusNoData: 1}

// mergeHour adds one hour's group value to the group's day.
func mergeHour(d *GroupValue, g GroupValue) {
	if hourRank[g.Status] > hourRank[d.Status] || g.Status == d.Status && d.Reason == "" {
		d.Status, d.Reason = g.Status, g.Reason
	}
	if d.Basis == "" {
		d.Basis = g.Basis
	}
	d.Value += g.Value
	d.Count += g.Count
	d.Buckets += g.Buckets
	d.WornBuckets += g.WornBuckets
	d.Gated += g.Gated
	d.Prorated = d.Prorated || g.Prorated
	d.WearExempt = d.WearExempt && g.WearExempt
	d.Refs = append(d.Refs, g.Refs...)
	for _, src := range g.Sources {
		if !slices.Contains(d.Sources, src) {
			d.Sources = append(d.Sources, src)
		}
	}
	for _, c := range g.Warnings {
		if !slices.Contains(d.Warnings, c) {
			d.Warnings = append(d.Warnings, c)
		}
	}
	if !g.First.IsZero() && (d.First.IsZero() || g.First.Before(d.First)) {
		d.First = g.First
	}
	if g.At.After(d.At) {
		d.At = g.At
	}
}
