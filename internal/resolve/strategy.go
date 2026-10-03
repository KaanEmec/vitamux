package resolve

import (
	"fmt"
	"maps"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// GroupStatus is a group's status in one window (docs/architecture/resolution.md#strategies).
type GroupStatus string

const (
	StatusValid        GroupStatus = "valid"           // passed the gates; the strategy has not run yet
	StatusSelected     GroupStatus = "selected"        // contributed to the window value
	StatusUnused       GroupStatus = "fallback_unused" // valid, but the strategy did not use it
	StatusNoData       GroupStatus = "no_data"
	StatusBelowQuality GroupStatus = "below_quality" // Reason names the gate
	StatusStale        GroupStatus = "stale"         // latest value older than quality.max_staleness
	StatusExcluded     GroupStatus = "excluded"      // a source matched an exclude selector
	StatusNotInRule    GroupStatus = "not_in_rule"   // a source matched no group
)

// Reasons for no_data, below_quality and fallback_unused.
const (
	ReasonNoInputs       = "no_inputs"
	ReasonOnlyDailyTotal = "only_daily_total" // daily_value_policy intervals_only, and the source sent only a daily total
	ReasonImplausible    = "implausible"      // every row (or the value) is outside the plausible range
	ReasonFlagged        = "flagged"          // every row carries an excluded quality flag
	ReasonCoverage       = "coverage"         // below quality.min_coverage
	ReasonInsufficient   = "insufficient_sources"
)

// ResultStatus is the window result status. J09.7 adds overridden.
type ResultStatus string

const (
	ResultDirect     ResultStatus = "direct"     // the first group of the ladder, or a latest/earliest pick
	ResultFallback   ResultStatus = "fallback"   // a later group, because the ones ahead had no valid value
	ResultCalculated ResultStatus = "calculated" // pooled across groups
	ResultNoData     ResultStatus = "no_data"
)

// WindowWarning is one warning of a window result; Group is the group id it concerns, if any.
type WindowWarning struct {
	Code  Warning
	Group string
}

// InputCounts summarises a window's candidate inputs.
type InputCounts struct {
	Grouped, Excluded, NotInRule int
	Flagged, Implausible         int // grouped rows dropped by the row gates
}

// WindowResult is one resolved window as plain data; J09.8 renders the documented result shape
// and the explanation from it.
type WindowResult struct {
	Window     Window
	Status     ResultStatus
	Value      float64            // canonical unit; unset when Status is no_data
	Components map[string]float64 // family rules (blood_pressure)
	Op         Op
	Selected   string       // the selected group id for selecting ops; "" when pooled or no data
	Groups     []GroupValue // every rule group in ladder order, then each excluded and unmatched source
	Warnings   []WindowWarning
	Inputs     InputCounts
	Partial    bool // the window ends after Options.Now
}

// Options are per-call inputs that are not part of the rule.
type Options struct {
	Now time.Time // caps elapsed buckets and staleness and sets Partial; zero means no cap
	// Previous is the group id the previous window selected, for definition_changed; "" if unknown.
	// ResolveWindows sets it from window to window.
	Previous string
	Context  Context // E1 context of the window; only "" until J09.10
}

// ResolveWindows resolves consecutive windows; each window falls back on its own, and the
// selection carries over for definition_changed.
func (r *Rule) ResolveWindows(ws []Window, s Series, opt Options) ([]WindowResult, error) {
	out := make([]WindowResult, 0, len(ws))
	for _, w := range ws {
		res, err := r.ResolveWindow(w, s, opt)
		if err != nil {
			return nil, err
		}
		if res.Selected != "" {
			opt.Previous = res.Selected
		}
		out = append(out, res)
	}
	return out, nil
}

// ResolveWindow resolves one window: assign inputs to groups, drop rows by flag and plausible
// range, aggregate each group, gate coverage and staleness, then apply the strategy. s may hold
// inputs outside w; Window.Includes filters them.
func (r *Rule) ResolveWindow(w Window, s Series, opt Options) (WindowResult, error) {
	sp, err := r.spec()
	if err != nil {
		return WindowResult{}, err
	}
	if err := r.pendingParts(); err != nil {
		return WindowResult{}, err
	}
	if !sp.primary().AllowsWindow(w.Kind) {
		return WindowResult{}, fmt.Errorf("resolve: window %s is not allowed for %s", w.Kind, r.Metric)
	}
	rows, excluded, unmatched, counts := r.windowRows(sp, w, s)
	gvs := make([]GroupValue, len(r.Groups))
	for i := range r.Groups {
		gv, err := r.aggregate(sp, w, i, rows[i].kept, opt.Now)
		if err != nil {
			return WindowResult{}, err
		}
		r.gateGroup(sp, w, &gv, rows[i], opt.Now)
		gvs[i] = gv
	}
	res, err := r.Select(w, gvs, opt)
	if err != nil {
		return WindowResult{}, err
	}
	res.Groups = append(res.Groups, sourceEntries(excluded, StatusExcluded)...)
	res.Groups = append(res.Groups, sourceEntries(unmatched, StatusNotInRule)...)
	res.Inputs = counts
	res.Partial = !opt.Now.IsZero() && w.Partial(opt.Now)
	return res, nil
}

// pendingParts rejects rule parts that J09.10 implements.
func (r *Rule) pendingParts() error {
	switch {
	case r.Follow != "":
		return fmt.Errorf("%w: follow (E5, J09.10)", ErrNotImplemented)
	case r.Compose != nil:
		return fmt.Errorf("%w: compose (E9, J09.10)", ErrNotImplemented)
	case r.Quality != nil && r.Quality.RequireWear != "":
		return fmt.Errorf("%w: quality.require_wear (E3, J09.10)", ErrNotImplemented)
	}
	return nil
}

// groupRows is one group's rows in a window after the row gates.
type groupRows struct {
	kept                 Series
	flagged, implausible int
}

var flagBits = map[string]normalize.Flags{
	"manual_entry": normalize.FlagManualEntry, "motion_context": normalize.FlagMotionContext,
	"implausible": normalize.FlagImplausible, "relayed": normalize.FlagRelayed,
	"migrated_without_raw": normalize.FlagMigratedWithoutRaw, "prorated_source": normalize.FlagProratedSource,
}

// windowRows partitions the inputs of w and applies the row gates: quality.exclude_flags and the
// plausible range (the rule's, else the catalogue's). A family reading with one bad component
// is dropped whole, so components always come from one reading.
func (r *Rule) windowRows(sp spec, w Window, s Series) ([]groupRows, []Input, []Input, InputCounts) {
	var mask normalize.Flags
	var ruleRange []float64
	if q := r.Quality; q != nil {
		for _, f := range q.ExcludeFlags {
			mask |= flagBits[f]
		}
		if !sp.family && len(q.PlausibleRange) == 2 {
			ruleRange = q.PlausibleRange
		}
	}
	rows := make([]groupRows, len(r.Groups))
	for i := range rows {
		rows[i].kept = Series{}
	}
	var excluded, unmatched []Input
	var counts InputCounts
	bad := make([]map[string]string, len(r.Groups)) // family: reading key -> reason
	for _, code := range sp.codes {
		m := sp.byCode[code]
		lo, hi := m.Min, m.Max
		if ruleRange != nil {
			lo, hi = ruleRange[0], ruleRange[1]
		}
		var in []Input
		for _, x := range s[code] {
			if w.Includes(x) {
				in = append(in, x)
			}
		}
		p := r.Partition(in)
		excluded, unmatched = append(excluded, p.Excluded...), append(unmatched, p.NotInRule...)
		for g, xs := range p.Groups {
			for _, x := range xs {
				counts.Grouped++
				reason := ""
				switch {
				case x.Flags&mask != 0:
					reason = ReasonFlagged
				case hi > lo && (x.Value < lo || x.Value > hi):
					reason = ReasonImplausible
				}
				if reason == "" {
					rows[g].kept[code] = append(rows[g].kept[code], x)
					continue
				}
				rows[g].drop(reason, &counts)
				if sp.family {
					if bad[g] == nil {
						bad[g] = map[string]string{}
					}
					bad[g][readingKey(x)] = reason
				}
			}
		}
	}
	for g := range rows {
		if len(bad[g]) == 0 {
			continue
		}
		for code, xs := range rows[g].kept {
			kept := xs[:0:0]
			for _, x := range xs {
				if reason, ok := bad[g][readingKey(x)]; ok {
					rows[g].drop(reason, &counts)
					continue
				}
				kept = append(kept, x)
			}
			rows[g].kept[code] = kept
		}
	}
	counts.Excluded, counts.NotInRule = len(excluded), len(unmatched)
	return rows, excluded, unmatched, counts
}

func (g *groupRows) drop(reason string, c *InputCounts) {
	if reason == ReasonFlagged {
		g.flagged++
		c.Flagged++
	} else {
		g.implausible++
		c.Implausible++
	}
}

// gateGroup applies the group gates: an empty group is no_data, or below_quality when the row
// gates dropped everything; then the plausible range of an additive sum, min_coverage
// (intensive bucket means only: additive coverage needs the E3 wear gate), and max_staleness
// against the window end, or now for an open window.
func (r *Rule) gateGroup(sp spec, w Window, gv *GroupValue, rows groupRows, now time.Time) {
	if gv.Status != StatusValid {
		switch {
		case gv.Reason != "":
		case rows.implausible > 0:
			gv.Status, gv.Reason = StatusBelowQuality, ReasonImplausible
		case rows.flagged > 0:
			gv.Status, gv.Reason = StatusBelowQuality, ReasonFlagged
		default:
			gv.Reason = ReasonNoInputs
		}
		return
	}
	q := r.Quality
	if q == nil {
		q = &Quality{}
	}
	if sp.agg == catalog.Additive {
		lo, hi := sp.primary().Min, sp.primary().Max
		if len(q.PlausibleRange) == 2 {
			lo, hi = q.PlausibleRange[0], q.PlausibleRange[1]
		}
		if hi > lo && (gv.Value < lo || gv.Value > hi) {
			gv.Status, gv.Reason = StatusBelowQuality, ReasonImplausible
			return
		}
	}
	if q.MinCoverage != nil && gv.Basis == BasisBucketMeans && gv.Coverage < *q.MinCoverage {
		gv.Status, gv.Reason = StatusBelowQuality, ReasonCoverage
		return
	}
	if limit := q.MaxStaleness.Std(); limit > 0 {
		ref := w.End
		if !now.IsZero() && now.Before(ref) {
			ref = now
		}
		if ref.Sub(gv.At) > limit {
			gv.Status = StatusStale
		}
	}
}

// Select applies the rule's strategy to gated group values of one window, indexed like
// Rule.Groups. Only valid groups take part; the others keep their status and reason. A
// selecting op warns preferred_source_unavailable for every group ahead of the selected one.
// J09.6 feeds episode-based group values through the same step (event_priority).
func (r *Rule) Select(w Window, gvs []GroupValue, opt Options) (WindowResult, error) {
	if len(gvs) != len(r.Groups) {
		return WindowResult{}, fmt.Errorf("resolve: %d group values for %d groups", len(gvs), len(r.Groups))
	}
	if opt.Context != "" {
		return WindowResult{}, fmt.Errorf("%w: contexts (E1, J09.10)", ErrNotImplemented)
	}
	res := WindowResult{Window: w, Op: r.Strategy.Op, Status: ResultNoData}
	order := r.Ladder("", -1)
	groups := make([]GroupValue, len(order))
	var valid []int // positions in groups
	for pos, i := range order {
		groups[pos] = gvs[i]
		if gvs[i].Status == StatusValid {
			valid = append(valid, pos)
		}
	}
	warn := func(c Warning, group string) {
		res.Warnings = append(res.Warnings, WindowWarning{Code: c, Group: group})
	}
	selectOne := func(pos int, st ResultStatus) {
		g := &groups[pos]
		g.Status = StatusSelected
		res.Status, res.Value, res.Selected = st, g.Value, g.ID
		res.Components = maps.Clone(g.Components)
	}

	switch op := r.Strategy.Op; op {
	case OpSingleSource, OpFirstAvailable, OpEventPriority:
		if len(valid) == 0 {
			break
		}
		sel := valid[0]
		for pos := 0; pos < sel; pos++ {
			warn(WarnPreferredUnavailable, groups[pos].ID)
		}
		st := ResultDirect
		if sel > 0 {
			st = ResultFallback
		}
		selectOne(sel, st)
	case OpLatest, OpEarliest:
		if len(valid) == 0 {
			break
		}
		sel := valid[0]
		for _, pos := range valid[1:] {
			if (op == OpLatest && groups[pos].At.After(groups[sel].At)) ||
				(op == OpEarliest && groups[pos].First.Before(groups[sel].First)) {
				sel = pos // strict comparison: ties stay with the earlier group in the ladder
			}
		}
		selectOne(sel, ResultDirect)
	case OpMean, OpMin, OpMax, OpSum:
		if op == OpSum && !r.acknowledged(WarnCrossSourceSum) {
			return WindowResult{}, fmt.Errorf("%w (strategy)", ErrUnacknowledgedSum)
		}
		need := max(1, r.Strategy.MinSources)
		if len(valid) > 0 && len(valid) < need {
			warn(WarnInsufficientSources, "")
			if r.Strategy.OnInsufficient == NoValue {
				for _, pos := range valid {
					groups[pos].Status, groups[pos].Reason = StatusUnused, ReasonInsufficient
				}
				valid = nil
			}
		}
		if len(valid) == 0 {
			break
		}
		v := groups[valid[0]].Value
		for _, pos := range valid[1:] {
			switch x := groups[pos].Value; op {
			case OpMin:
				v = min(v, x)
			case OpMax:
				v = max(v, x)
			default:
				v += x
			}
		}
		if op == OpMean {
			v /= float64(len(valid))
		}
		for _, pos := range valid {
			groups[pos].Status = StatusSelected
		}
		res.Status, res.Value = ResultCalculated, v
		if op == OpSum {
			warn(WarnCrossSourceSum, "")
		}
	default:
		return WindowResult{}, fmt.Errorf("resolve: unknown strategy op %q", op)
	}

	for pos := range groups {
		g := &groups[pos]
		switch g.Status {
		case StatusValid:
			g.Status = StatusUnused
		case StatusSelected:
			for _, c := range g.Warnings {
				warn(c, g.ID)
			}
		default: // not valid: keeps its status and reason
		}
	}
	if res.Selected != "" && opt.Previous != "" && res.Selected != opt.Previous && r.selectionOnly() {
		warn(WarnDefinitionChanged, res.Selected)
	}
	res.Groups = groups
	return res, nil
}

// selectionOnly reports whether the rule's metric is never pooled (provider-scoped or
// selection-only), so a change of group changes the definition.
func (r *Rule) selectionOnly() bool {
	m, ok := catalog.Lookup(r.Metric)
	return ok && !m.Poolable()
}

// sourceEntries lists excluded or unmatched inputs once per source, for the all-sources view.
func sourceEntries(in []Input, st GroupStatus) []GroupValue {
	var out []GroupValue
	idx := map[Source]int{}
	for _, x := range in {
		i, ok := idx[x.Source]
		if !ok {
			i = len(out)
			idx[x.Source] = i
			out = append(out, GroupValue{Group: -1, Status: st})
		}
		out[i].use(x)
	}
	return out
}
