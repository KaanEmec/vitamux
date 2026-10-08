package resolve

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// ErrNotImplemented marks rule parts whose engine lives elsewhere: the sleep family and sleep
// codes resolve through aligned episodes (J09.6). The wrapped error names the part.
var ErrNotImplemented = errors.New("resolve: not implemented")

// ErrUnacknowledgedSum rejects sum_across_sources or intra_group: sum at resolve time when the
// rule does not acknowledge cross_source_sum_duplicate_risk (validation rejects it on save too).
var ErrUnacknowledgedSum = errors.New("resolve: sum without acknowledged " + string(WarnCrossSourceSum))

// Series holds candidate inputs by catalogue code: the rule's metric for a plain rule, one
// entry per component for a family (blood_pressure), the source metric for a derived code
// (E2), plus the wear metric under quality.require_wear (E3). Input has no code of its own.
type Series map[string][]Input

// Basis says what a group value was computed from.
type Basis string

const (
	BasisBucketMeans Basis = "bucket_means" // intensive: mean of equally weighted bucket means
	BasisIntervals   Basis = "intervals"    // additive: sum of (pro-rated) intervals
	BasisDailyValue  Basis = "daily_value"  // the provider's value for the local day
	BasisLatest      Basis = "latest"       // the latest reading or sample
	BasisMean        Basis = "mean"         // mean of the day's readings, per component
)

// GroupValue is one group's within-source value in one window. Aggregate fills it, the quality
// gates may lower Status, and the strategy marks it selected or unused (ResolveWindow). The
// same type lists excluded and unmatched sources in a WindowResult (Group -1).
type GroupValue struct {
	Group      int    // index into Rule.Groups; -1 for excluded and not-in-rule entries
	ID         string // group id; "" for excluded and not-in-rule entries
	Status     GroupStatus
	Reason     string             // Reason* constant when Status is not valid, selected or unused
	Value      float64            // canonical unit; family rules use Components
	Components map[string]float64 // family rules: component code -> value, all from one reading (or a per-component mean)
	Basis      Basis
	Coverage   float64     // covered / elapsed base buckets; 1 for unbucketed values
	Count      int         // contributing inputs
	Readings   int         // latest-type metrics: contributing readings (one row, or one measurement group)
	Buckets    int         // covered base buckets (intensive and additive interval values)
	Min, Max   float64     // intensive: the lowest and highest contributing sample
	First, At  time.Time   // earliest and latest contributing instants (Input.At)
	Prorated   bool        // an interval was cut at the window bounds
	Refs       []int64     // contributing Input.ID values, for record_refs
	Sessions   []uuid.UUID // sleep: the sleep_sessions.id values behind the value
	Sources    []Source    // distinct contributing sources, in input order
	Warnings   []Warning   // per-group warnings, e.g. intra_group sum
	// E2: the bucket span [SpanStart, SpanEnd) a min or min_rolling_mean statistic picked.
	SpanStart, SpanEnd time.Time
	// E3 (quality.require_wear): no device of the group reports the wear metric, so it is not
	// gated; otherwise the elapsed base buckets in which one was worn, and the rows that had a
	// part dropped in buckets where their device was not worn.
	WearExempt  bool
	WornBuckets int
	Gated       int
}

// spec is what a rule resolves: the aggregation and the codes (one, or a family's members).
type spec struct {
	agg    catalog.Aggregation
	codes  []string
	byCode map[string]catalog.Metric
	family bool
	named  catalog.Metric // a derived code (E2); its codes are the source metric's
}

func (s spec) primary() catalog.Metric { return s.byCode[s.codes[0]] }

func (s spec) allowsWindow(k catalog.Window) bool {
	if s.named.Code != "" {
		return s.named.AllowsWindow(k)
	}
	return s.primary().AllowsWindow(k)
}

func (r *Rule) spec() (spec, error) {
	switch r.Metric {
	case FamilySleep:
		return spec{}, fmt.Errorf("%w: the sleep family resolves through aligned episodes (J09.6)", ErrNotImplemented)
	case FamilyBloodPressure:
		sp := spec{agg: catalog.Latest, family: true, byCode: map[string]catalog.Metric{}}
		for _, m := range catalog.Metrics() {
			if m.Group == "bp_reading" {
				sp.codes = append(sp.codes, m.Code)
				sp.byCode[m.Code] = m
			}
		}
		if len(sp.codes) == 0 {
			return spec{}, errors.New("resolve: blood_pressure family has no catalogue codes")
		}
		return sp, nil
	}
	m, ok := catalog.Lookup(r.Metric)
	switch {
	case !ok:
		return spec{}, fmt.Errorf("resolve: unknown metric %q", r.Metric)
	case m.Agg == catalog.SleepDerived:
		return spec{}, fmt.Errorf("%w: sleep codes resolve through the sleep family (J09.6)", ErrNotImplemented)
	case m.DerivedFrom != "":
		return r.derivedSpec(m)
	}
	return spec{agg: m.Agg, codes: []string{m.Code}, byCode: map[string]catalog.Metric{m.Code: m}}, nil
}

func (r *Rule) acknowledged(w Warning) bool { return slices.Contains(r.AcknowledgedWarnings, w) }

func (r *Rule) withinSource() WithinSource {
	if r.WithinSource == nil {
		return WithinSource{}
	}
	return *r.WithinSource
}

// Aggregate computes one group's value in w (docs/architecture/resolution.md#within-source-aggregation).
// s holds that group's inputs that belong to w and passed the row gates; ResolveWindow does
// both. With quality.require_wear, s also holds the whole wear series (see WearLookback). now
// caps the elapsed buckets of an open window (zero: no cap). Without inputs the status is
// no_data; otherwise valid, unless an extension gate lowers it (not_worn, no_full_span).
func (r *Rule) Aggregate(w Window, group int, s Series, now time.Time) (GroupValue, error) {
	sp, err := r.spec()
	if err != nil {
		return GroupValue{}, err
	}
	return r.aggregate(sp, w, group, s, now, r.newWear(sp, s))
}

func (r *Rule) aggregate(sp spec, w Window, group int, s Series, now time.Time, wear *wearIndex) (GroupValue, error) {
	gv := GroupValue{Group: group, Status: StatusNoData}
	if group >= 0 && group < len(r.Groups) {
		gv.ID = r.Groups[group].ID
	}
	ws := r.withinSource()
	switch {
	case ws.IntraGroup == IntraSum && !r.acknowledged(WarnCrossSourceSum):
		return gv, fmt.Errorf("%w (within_source.intra_group)", ErrUnacknowledgedSum)
	}
	in := s[sp.codes[0]]
	base := baseBucket(sp.primary(), w)
	noSpan := false
	switch {
	case sp.agg == catalog.Latest:
		gv.aggReadings(sp, s, w, ws.Statistic)
	case w.Kind == catalog.WindowLatest:
		if x, ok := LatestInput(in, w.End); ok {
			gv.use(x)
			gv.Value, gv.Basis, gv.Coverage = x.Value, BasisLatest, 1
			if x.Kind == catalog.DailyValue {
				gv.Basis = BasisDailyValue
			}
		}
	case sp.agg == catalog.Intensive:
		keys, means := gv.aggIntensive(in, w, base, ws.IntraGroup, now, wear)
		noSpan = len(keys) > 0 && statisticApplies(ws.Statistic, w.Kind) && !gv.windowStatistic(ws, keys, means, base)
	case sp.agg == catalog.Additive:
		gv.aggAdditive(in, w, base, ws, now, wear)
		if gv.Count > 0 && ws.IntraGroup == IntraSum {
			gv.Warnings = append(gv.Warnings, WarnCrossSourceSum)
		}
	case sp.agg == catalog.DailySummary:
		gv.aggDailySummary(in)
	default:
		return gv, fmt.Errorf("resolve: aggregation %s has no within-source step here", sp.agg)
	}
	if gv.Count > 0 {
		gv.Status, gv.Reason = StatusValid, ""
	}
	if wear != nil && base > 0 {
		gv.applyWear(wear, sp, w, in, base, now)
	}
	if noSpan && gv.Status == StatusValid {
		gv.Status, gv.Reason = StatusBelowQuality, ReasonNoFullSpan
	}
	return gv, nil
}

// use records x as a contributing input.
func (gv *GroupValue) use(x Input) {
	gv.Count++
	gv.Refs = append(gv.Refs, x.ID)
	if !slices.Contains(gv.Sources, x.Source) {
		gv.Sources = append(gv.Sources, x.Source)
	}
	if at := x.At(); gv.At.IsZero() || at.After(gv.At) {
		gv.At = at
	}
	if at := x.At(); gv.First.IsZero() || at.Before(gv.First) {
		gv.First = at
	}
}

// baseBucket is min(window length, catalogue base bucket).
func baseBucket(m catalog.Metric, w Window) time.Duration {
	base := m.BaseBucket()
	if d := w.End.Sub(w.Start); d > 0 && d < base {
		return d
	}
	return base
}

// elapsedSpan is the window part that has happened: [first bucket start, min(End, now)).
// Truncate aligns to UTC multiples of base, which are local boundaries for every real offset.
func elapsedSpan(w Window, base time.Duration, now time.Time) (first, end time.Time, n int) {
	first, end = w.Start.Truncate(base), w.End
	if !now.IsZero() && now.Before(end) {
		end = now
	}
	if !end.After(w.Start) {
		return first, end, 0
	}
	return first, end, int((end.Sub(first) + base - 1) / base)
}

// bucketCoverage counts the covered buckets inside the elapsed span and divides by its length.
func bucketCoverage(buckets []int64, w Window, base time.Duration, now time.Time) float64 {
	first, end, n := elapsedSpan(w, base, now)
	if n == 0 {
		return 0
	}
	covered := 0
	for _, b := range buckets {
		if b >= first.UnixNano() && b < end.UnixNano() {
			covered++
		}
	}
	return min(1, float64(covered)/float64(n))
}

// subSources gives each source of a group a stable index in input order, so float sums do not
// depend on map order.
type subSources map[Source]int

func (s subSources) index(src Source) int {
	i, ok := s[src]
	if !ok {
		i = len(s)
		s[src] = i
	}
	return i
}

// combineIntra merges the sub-source values of one bucket (or one day for daily values).
func combineIntra(p IntraGroup, intensive bool, vals []float64) float64 {
	if p == "" || p == IntraAuto {
		p = IntraMax
		if intensive {
			p = IntraMean
		}
	}
	out := vals[0]
	switch p {
	case IntraMax:
		for _, v := range vals[1:] {
			out = max(out, v)
		}
	default: // mean, sum
		for _, v := range vals[1:] {
			out += v
		}
		if p == IntraMean {
			out /= float64(len(vals))
		}
	}
	return out
}

// aggIntensive: per sub-source bucket means, combined per bucket by intra_group, then the mean
// of the covered buckets, each weighted equally. Repeating samples in a bucket changes nothing.
// Samples in buckets where their device was not worn (E3) are dropped. It returns the covered
// bucket starts in order with their combined means, for the E2 statistics.
func (gv *GroupValue) aggIntensive(in []Input, w Window, base time.Duration, intra IntraGroup, now time.Time, wear *wearIndex) ([]int64, []float64) {
	type acc struct {
		sum float64
		n   int
	}
	idx := subSources{}
	buckets := map[int64][]acc{} // bucket start -> per sub-source accumulator
	for _, x := range in {
		if x.Kind == catalog.DailyValue || x.Kind == catalog.Cumulative {
			continue
		}
		if !wear.counts(x.Source, x.Start.Truncate(base), base) {
			gv.Gated++
			continue
		}
		b := x.Start.Truncate(base).UnixNano()
		i := idx.index(x.Source)
		row := buckets[b]
		for len(row) <= i {
			row = append(row, acc{})
		}
		row[i].sum += x.Value
		row[i].n++
		buckets[b] = row
		if gv.Count == 0 || x.Value < gv.Min {
			gv.Min = x.Value
		}
		if gv.Count == 0 || x.Value > gv.Max {
			gv.Max = x.Value
		}
		gv.use(x)
	}
	if gv.Count == 0 {
		return nil, nil
	}
	keys := sortedKeys(buckets)
	combined := make([]float64, len(keys))
	total := 0.0
	for k, b := range keys {
		var means []float64
		for _, a := range buckets[b] {
			if a.n > 0 {
				means = append(means, a.sum/float64(a.n))
			}
		}
		combined[k] = combineIntra(intra, true, means)
		total += combined[k]
	}
	gv.Value = total / float64(len(keys))
	gv.Basis, gv.Buckets = BasisBucketMeans, len(keys)
	gv.Coverage = bucketCoverage(keys, w, base, now)
	return keys, combined
}

// aggAdditive: on local_day with prefer_reported, a provider daily value wins over intervals;
// otherwise intervals are summed per base bucket (pro-rated at the bounds of instant windows),
// sub-sources combine per bucket by intra_group (auto = max), and the buckets are summed. A
// daily value and intervals are never added. Interval parts in buckets where their device was
// not worn (E3) are dropped; a daily value cannot be split and is gated in applyWear.
func (gv *GroupValue) aggAdditive(in []Input, w Window, base time.Duration, ws WithinSource, now time.Time, wear *wearIndex) {
	day := w.Kind == catalog.WindowLocalDay
	var daily, intervals []Input
	for _, x := range in {
		switch x.Kind {
		case catalog.DailyValue:
			daily = append(daily, x)
		case catalog.Interval, catalog.Sample:
			intervals = append(intervals, x)
		case catalog.Cumulative: // counter readings are not converted here; normalizers emit intervals where possible
		}
	}
	if day && len(daily) > 0 && ws.DailyValuePolicy != IntervalsOnly {
		gv.aggDaily(daily, ws.IntraGroup)
		return
	}
	if len(intervals) == 0 {
		if len(daily) > 0 {
			gv.Reason = ReasonOnlyDailyTotal
		}
		return
	}
	idx := subSources{}
	buckets := map[int64][]float64{}
	add := func(b time.Time, i int, v float64) {
		k := b.UnixNano()
		row := buckets[k]
		for len(row) <= i {
			row = append(row, 0)
		}
		row[i] += v
		buckets[k] = row
	}
	for _, x := range intervals {
		i := idx.index(x.Source)
		kept, dropped := false, false
		put := func(b time.Time, v float64) {
			if !wear.counts(x.Source, b, base) {
				dropped = true
				return
			}
			kept = true
			add(b, i, v)
		}
		if !x.End.After(x.Start) {
			put(x.Start.Truncate(base), x.Value)
		} else {
			lo, hi := x.Start, x.End
			if !day { // local_day takes whole rows by local_date, so a travel day is never split
				lo, hi = timeMax(lo, w.Start), timeMin(hi, w.End)
				if hi.Sub(lo) < x.End.Sub(x.Start) {
					gv.Prorated = true
				}
			}
			dur := float64(x.End.Sub(x.Start))
			for b := lo.Truncate(base); b.Before(hi); b = b.Add(base) {
				part := timeMin(hi, b.Add(base)).Sub(timeMax(lo, b))
				put(b, x.Value*float64(part)/dur)
			}
		}
		if kept {
			gv.use(x)
		}
		if dropped {
			gv.Gated++
		}
	}
	keys := sortedKeys(buckets)
	total := 0.0
	for _, b := range keys {
		total += combineIntra(ws.IntraGroup, false, buckets[b])
	}
	gv.Value, gv.Basis, gv.Buckets = total, BasisIntervals, len(keys)
	gv.Coverage = bucketCoverage(keys, w, base, now)
}

// aggDaily takes each sub-source's latest daily value and combines them by intra_group.
func (gv *GroupValue) aggDaily(daily []Input, intra IntraGroup) {
	idx := subSources{}
	var pick []Input
	for _, x := range daily {
		i := idx.index(x.Source)
		if i == len(pick) {
			pick = append(pick, x)
		} else if newerInput(x, pick[i]) {
			pick[i] = x
		}
	}
	vals := make([]float64, len(pick))
	for i, x := range pick {
		vals[i] = x.Value
		gv.use(x)
	}
	gv.Value, gv.Basis, gv.Coverage = combineIntra(intra, false, vals), BasisDailyValue, 1
}

// aggDailySummary: the latest daily value of the date, else the latest sample.
func (gv *GroupValue) aggDailySummary(in []Input) {
	var best Input
	found, bestDaily := false, false
	for _, x := range in {
		d := x.Kind == catalog.DailyValue
		if !found || (d && !bestDaily) || (d == bestDaily && newerInput(x, best)) {
			best, found, bestDaily = x, true, d
		}
	}
	if !found {
		return
	}
	gv.use(best)
	gv.Value, gv.Basis, gv.Coverage = best.Value, BasisLatest, 1
	if bestDaily {
		gv.Basis = BasisDailyValue
	}
}

// aggReadings handles latest-type metrics. Rows of one reading (measurement group) stay
// together: the latest reading gives every component, and statistic mean on local_day averages
// each component over the day's readings, so one reading never mixes sources.
func (gv *GroupValue) aggReadings(sp spec, s Series, w Window, stat Statistic) {
	type reading struct {
		at   time.Time
		id   int64
		rows map[string]Input
	}
	byKey := map[string]*reading{}
	var all []*reading
	for _, code := range sp.codes {
		for _, x := range s[code] {
			if w.Kind == catalog.WindowLatest && x.At().After(w.End) {
				continue
			}
			k := readingKey(x)
			rd := byKey[k]
			if rd == nil {
				rd = &reading{rows: map[string]Input{}}
				byKey[k] = rd
				all = append(all, rd)
			}
			if old, dup := rd.rows[code]; !dup || newerInput(x, old) {
				rd.rows[code] = x
			}
			if x.At().After(rd.at) || (x.At().Equal(rd.at) && x.ID > rd.id) {
				rd.at, rd.id = x.At(), x.ID
			}
		}
	}
	if len(all) == 0 {
		return
	}
	slices.SortStableFunc(all, func(a, b *reading) int {
		if c := a.at.Compare(b.at); c != 0 {
			return c
		}
		return cmp.Compare(a.id, b.id)
	})
	comps := map[string]float64{}
	if w.Kind == catalog.WindowLocalDay && stat == StatMean {
		n := map[string]int{}
		for _, rd := range all {
			for _, code := range sp.codes {
				if x, ok := rd.rows[code]; ok {
					comps[code] += x.Value
					n[code]++
					gv.use(x)
				}
			}
		}
		for code := range comps {
			comps[code] /= float64(n[code])
		}
		gv.Basis, gv.Readings = BasisMean, len(all)
	} else {
		last := all[len(all)-1]
		for _, code := range sp.codes {
			if x, ok := last.rows[code]; ok {
				comps[code] = x.Value
				gv.use(x)
			}
		}
		gv.Basis, gv.Readings = BasisLatest, 1
	}
	gv.Coverage = 1
	if sp.family {
		gv.Components = comps
	} else {
		gv.Value = comps[sp.codes[0]]
	}
}

// newerInput orders inputs by Input.At, then by ID (a later row wins a tie).
func newerInput(a, b Input) bool {
	return a.At().After(b.At()) || (a.At().Equal(b.At()) && a.ID > b.ID)
}

func sortedKeys[V any](m map[int64]V) []int64 {
	return slices.Sorted(maps.Keys(m))
}

func timeMax(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func timeMin(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}
