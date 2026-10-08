package resolve

import (
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/normalize"
)

// FragmentGap is the largest gap between two sessions of one source that still merges them
// into one session (docs/architecture/resolution.md#sleep-episode-alignment).
const FragmentGap = 60 * time.Minute

// DefaultMatchOverlap is the default of quality.sleep.match_overlap. The minimum episode
// coverage has no default: a gate applies only when the rule sets it.
const DefaultMatchOverlap = 0.5

// SleepInput is one active sleep session as alignment sees it. J09.8 and J09.9 fill it from
// sleep_sessions and sleep_stages; this package does no I/O.
type SleepInput struct {
	ID         uuid.UUID
	Source     Source
	Start, End time.Time
	Zone       normalize.Zone // tz_offset_min of the row; the timeline covers the rest
	IsNap      bool
	HasStages  bool
	Totals     normalize.SleepTotals  // the stored *_s columns; nil means not reported, never 0
	Stages     []normalize.SleepStage // sleep_stages rows; sleep_waso needs them
}

// SleepAlignment is one night's candidate sessions aligned under a rule. Only sessions in a
// rule group form episodes; the others are listed so the all-sources view can show them.
type SleepAlignment struct {
	Night     time.Time
	Episodes  []Episode // start order; exactly one is Main when any exist
	Excluded  []SleepInput
	NotInRule []SleepInput

	rule *Rule
	byID map[uuid.UUID]SleepInput
}

// AlignSleep builds the episodes of night (ADR-0009). in may hold sessions of other nights
// (readers query sleep_date IN (D-1, D)); only those whose NightOf is night are candidates.
// Same-source fragments at most FragmentGap apart merge, then sessions link across sources
// when their overlap is at least match_overlap of the shorter one; connected sessions form
// one episode, and the episode with the largest span is the main one.
func (r *Rule) AlignSleep(night time.Time, in []SleepInput, tl normalize.Timeline) (SleepAlignment, error) {
	night = midnightUTC(night)
	a := SleepAlignment{Night: night, rule: r, byID: map[uuid.UUID]SleepInput{}}
	anchor := r.NightAnchor()
	var cand []SleepInput
	for _, s := range in {
		d, err := NightOf(s.End, s.Zone, tl, anchor)
		if err != nil {
			return SleepAlignment{}, err
		}
		if !d.Equal(night) {
			continue
		}
		switch r.Assign(s.Source).Membership() {
		case Excluded:
			a.Excluded = append(a.Excluded, s)
		case NotInRule:
			a.NotInRule = append(a.NotInRule, s)
		case Grouped:
			cand = append(cand, s)
			a.byID[s.ID] = s
		}
	}
	a.Episodes = buildEpisodes(night, cand, r.sleepQuality().matchOverlap)
	return a, nil
}

// Main returns the night's main episode.
func (a SleepAlignment) Main() (Episode, bool) {
	for _, e := range a.Episodes {
		if e.Main {
			return e, true
		}
	}
	return Episode{}, false
}

// NightEpisodes are the episodes a local_night window resolves: the main episode, plus the
// secondary ones (naps) when the rule sets include_naps. sleep_episode windows resolve every
// episode on its own (EpisodeWindow).
func (a SleepAlignment) NightEpisodes() []Episode {
	naps := a.rule.sleepQuality().includeNaps
	var out []Episode
	for _, e := range a.Episodes {
		if e.Main || naps {
			out = append(out, e)
		}
	}
	return out
}

type sleepParams struct {
	matchOverlap, minCoverage float64
	includeNaps               bool
}

func (r *Rule) sleepQuality() sleepParams {
	p := sleepParams{matchOverlap: DefaultMatchOverlap}
	if r.Quality == nil || r.Quality.Sleep == nil {
		return p
	}
	s := r.Quality.Sleep
	if s.MatchOverlap != nil {
		p.matchOverlap = *s.MatchOverlap
	}
	if s.MinEpisodeCoverage != nil {
		p.minCoverage = *s.MinEpisodeCoverage
	}
	p.includeNaps = s.IncludeNaps
	return p
}

func buildEpisodes(night time.Time, in []SleepInput, minOverlap float64) []Episode {
	ss := mergeFragments(in)
	root := make([]int, len(ss))
	for i := range root {
		root[i] = i
	}
	find := func(i int) int {
		for root[i] != i {
			root[i] = root[root[i]]
			i = root[i]
		}
		return i
	}
	for i := range ss {
		for j := i + 1; j < len(ss); j++ {
			if overlapRatio(ss[i].Start, ss[i].End, ss[j].Start, ss[j].End) >= minOverlap {
				root[find(j)] = find(i)
			}
		}
	}
	idx := map[int]int{}
	var eps []Episode
	for i, s := range ss { // ss is in start order, so episodes are too
		k := find(i)
		n, ok := idx[k]
		if !ok {
			n = len(eps)
			idx[k] = n
			eps = append(eps, Episode{Night: night, Start: s.Start, End: s.End})
		}
		e := &eps[n]
		e.End = timeMax(e.End, s.End)
		e.Sessions = append(e.Sessions, s)
	}
	main := -1
	for i, e := range eps {
		if main < 0 || e.End.Sub(e.Start) > eps[main].End.Sub(eps[main].Start) {
			main = i
		}
	}
	if main >= 0 {
		eps[main].Main = true
	}
	return eps
}

// mergeFragments merges each source's sessions that are at most FragmentGap apart and
// returns all sessions in start order (ties keep the order sources first appear in).
func mergeFragments(in []SleepInput) []EpisodeSession {
	var order []Source
	bySrc := map[Source][]SleepInput{}
	for _, s := range in {
		if _, ok := bySrc[s.Source]; !ok {
			order = append(order, s.Source)
		}
		bySrc[s.Source] = append(bySrc[s.Source], s)
	}
	var out []EpisodeSession
	for _, src := range order {
		frags := bySrc[src]
		slices.SortStableFunc(frags, func(a, b SleepInput) int { return a.Start.Compare(b.Start) })
		var cur *EpisodeSession
		for _, f := range frags {
			if cur != nil && f.Start.Sub(cur.End) <= FragmentGap {
				cur.IDs = append(cur.IDs, f.ID)
				cur.End = timeMax(cur.End, f.End)
				cur.IsNap = cur.IsNap && f.IsNap
				cur.HasStages = cur.HasStages && f.HasStages
				continue
			}
			out = append(out, EpisodeSession{IDs: []uuid.UUID{f.ID}, Source: src, Start: f.Start, End: f.End, IsNap: f.IsNap, HasStages: f.HasStages})
			cur = &out[len(out)-1]
		}
	}
	slices.SortStableFunc(out, func(a, b EpisodeSession) int { return a.Start.Compare(b.Start) })
	return out
}

// overlapRatio is the overlap of two spans divided by the shorter duration.
func overlapRatio(aStart, aEnd, bStart, bEnd time.Time) float64 {
	ov := timeMin(aEnd, bEnd).Sub(timeMax(aStart, bStart))
	shorter := min(aEnd.Sub(aStart), bEnd.Sub(bStart))
	if ov <= 0 || shorter <= 0 {
		return 0
	}
	return float64(ov) / float64(shorter)
}

// SleepPartialEpisode is the below_quality reason when the group covered less than min_episode_coverage of the episode.
const SleepPartialEpisode = "partial_episode"

// SleepGroup is one rule group's capture of an episode. When a group holds several sources
// in the episode, the one covering most of it is used alone, so sub-sources are never added.
type SleepGroup struct {
	Status   GroupStatus // valid, no_data or below_quality
	Reason   string
	Coverage float64          // fraction of the episode span covered by Sessions
	Sessions []EpisodeSession // the used source's sessions in the episode

	frags []SleepInput // the fragments behind Sessions, start order
}

// SleepSelection is the sleep-family rule applied to one episode. Every sleep_* code reads
// from it (docs/architecture/resolution.md#group-coherent-selection).
type SleepSelection struct {
	Episode Episode
	Groups  []SleepGroup // indexed like Rule.Groups
	// Selected is the group the codes come from for the selecting ops (single_source,
	// first_available, event_priority, latest, earliest); -1 when no group is valid or the op
	// pools, in which case callers pool Groups[i].Value over the valid groups.
	Selected int
	Warnings []Warning
}

// Select applies the rule to an episode of the alignment: group coverage, the coverage gate,
// then the strategy. The ladder ignores contexts (they reorder other metrics inside sleep).
func (a SleepAlignment) Select(e Episode) SleepSelection {
	r := a.rule
	sel := SleepSelection{Episode: e, Groups: make([]SleepGroup, len(r.Groups)), Selected: -1}
	type cand struct {
		sess  []EpisodeSession
		frags []SleepInput
	}
	cands := make([][]*cand, len(r.Groups))
	bySrc := map[Source]*cand{}
	for _, es := range e.Sessions {
		c, ok := bySrc[es.Source]
		if !ok {
			g := r.Assign(es.Source).Group
			if g < 0 {
				continue
			}
			c = &cand{}
			bySrc[es.Source] = c
			cands[g] = append(cands[g], c)
		}
		c.sess = append(c.sess, es)
		for _, id := range es.IDs {
			c.frags = append(c.frags, a.byID[id])
		}
	}
	minCov := r.sleepQuality().minCoverage
	for g, cs := range cands {
		grp := SleepGroup{Status: StatusNoData}
		for _, c := range cs {
			slices.SortStableFunc(c.frags, func(a, b SleepInput) int { return a.Start.Compare(b.Start) })
			if cov := coverage(c.frags, e.Start, e.End); grp.Sessions == nil || cov > grp.Coverage {
				grp = SleepGroup{Coverage: cov, Sessions: c.sess, frags: c.frags}
			}
		}
		switch {
		case grp.Sessions == nil:
		case grp.Coverage < minCov:
			grp.Status, grp.Reason = StatusBelowQuality, SleepPartialEpisode
		default:
			grp.Status = StatusValid
		}
		sel.Groups[g] = grp
	}

	order := r.Ladder("", -1)
	switch r.Strategy.Op {
	case OpMean, OpMin, OpMax, OpSum:
		return sel
	case OpLatest, OpEarliest:
		for _, i := range order {
			g := sel.Groups[i]
			if g.Status != StatusValid {
				continue
			}
			if sel.Selected < 0 {
				sel.Selected = i
				continue
			}
			best := sel.Groups[sel.Selected]
			if r.Strategy.Op == OpLatest && g.frags[len(g.frags)-1].End.After(best.frags[len(best.frags)-1].End) ||
				r.Strategy.Op == OpEarliest && g.frags[0].Start.Before(best.frags[0].Start) {
				sel.Selected = i
			}
		}
	default: // single_source, first_available, event_priority
		for _, i := range order {
			if sel.Groups[i].Status == StatusValid {
				sel.Selected = i
				break
			}
		}
		if sel.Selected >= 0 && sel.Selected != order[0] {
			sel.Warnings = []Warning{WarnPreferredUnavailable}
		}
	}
	return sel
}

// coverage is the fraction of [start, end) covered by the union of the fragments, which are
// in start order.
func coverage(frags []SleepInput, start, end time.Time) float64 {
	span := end.Sub(start)
	if span <= 0 {
		return 0
	}
	var covered time.Duration
	cur := start
	for _, f := range frags {
		s, e := timeMax(f.Start, cur), timeMin(f.End, end)
		if e.After(s) {
			covered += e.Sub(s)
			cur = e
		}
	}
	return float64(covered) / float64(span)
}

// SleepValue is one sleep code's value in seconds (percent for sleep_efficiency).
type SleepValue struct {
	Value  float64
	Status GroupStatus // valid, no_data, below_quality or no_stage_data
}

// Value reads code from the selected group. A code the selected source lacks is
// no_stage_data (stage codes) or no_data, never another group's value.
func (s SleepSelection) Value(code string) SleepValue {
	if s.Selected < 0 {
		return SleepValue{Status: StatusNoData}
	}
	return s.Groups[s.Selected].Value(code)
}

// Value derives one sleep code from the group's sessions: the sum over its fragments, except
// sleep_latency (the first fragment's) and sleep_efficiency (100 * sleep_total / sleep_in_bed).
// One fragment lacking the code makes the whole value missing, so a gap never reads as 0.
func (g SleepGroup) Value(code string) SleepValue {
	if g.Status != StatusValid {
		return SleepValue{Status: g.Status}
	}
	switch code {
	case "sleep_efficiency":
		t, bed := g.Value("sleep_total"), g.Value("sleep_in_bed")
		switch {
		case t.Status != StatusValid:
			return t
		case bed.Value <= 0:
			return SleepValue{Status: StatusNoData}
		}
		return SleepValue{Value: 100 * t.Value / bed.Value, Status: StatusValid}
	case "sleep_latency":
		v, st := fragValue(g.frags[0], code)
		return SleepValue{Value: v, Status: st}
	}
	var sum float64
	for _, f := range g.frags {
		v, st := fragValue(f, code)
		if st != StatusValid {
			return SleepValue{Status: st}
		}
		sum += v
	}
	return SleepValue{Value: sum, Status: StatusValid}
}

// stageCodes need stage data; when missing they are no_stage_data rather than no_data.
var stageCodes = map[string]bool{
	"sleep_awake": true, "sleep_light": true, "sleep_deep": true, "sleep_rem": true,
	"sleep_unspecified": true, "sleep_waso": true,
}

// fragValue is one session's value of a sleep code in seconds.
func fragValue(f SleepInput, code string) (float64, GroupStatus) {
	missing := StatusNoData
	if stageCodes[code] {
		missing = StatusNoStageData
	}
	ptr := func(v *int32) (float64, GroupStatus) {
		if v == nil {
			return 0, missing
		}
		return float64(*v), StatusValid
	}
	t := f.Totals
	switch code {
	case "sleep_total":
		return ptr(t.Asleep)
	case "sleep_in_bed":
		return f.End.Sub(f.Start).Seconds(), StatusValid
	case "sleep_awake":
		return ptr(t.Awake)
	case "sleep_light":
		return ptr(t.Light)
	case "sleep_deep":
		return ptr(t.Deep)
	case "sleep_rem":
		return ptr(t.REM)
	case "sleep_latency":
		if t.Latency != nil {
			return ptr(t.Latency)
		}
		if first, _, ok := asleepSpan(f.Stages); ok {
			return max(first.Sub(f.Start), 0).Seconds(), StatusValid
		}
	case "sleep_unspecified":
		if len(f.Stages) > 0 {
			var d time.Duration
			for _, s := range f.Stages {
				if s.Stage == "asleep_unspecified" {
					d += s.End.Sub(s.Start)
				}
			}
			return d.Seconds(), StatusValid
		}
		// Without stage rows the remainder of the totals is unspecified sleep, when the
		// session has stages or reports every staged total.
		staged := t.Deep != nil && t.Light != nil && t.REM != nil
		if t.Asleep != nil && (f.HasStages || staged) {
			rest := *t.Asleep
			for _, v := range []*int32{t.Deep, t.Light, t.REM} {
				if v != nil {
					rest -= *v
				}
			}
			return float64(max(rest, 0)), StatusValid
		}
	case "sleep_waso":
		if first, last, ok := asleepSpan(f.Stages); ok {
			var d time.Duration
			for _, s := range f.Stages {
				if s.Stage == "awake" {
					d += max(timeMin(s.End, last).Sub(timeMax(s.Start, first)), 0)
				}
			}
			return d.Seconds(), StatusValid
		}
	}
	return 0, missing
}

// asleepSpan returns the start of the first and the end of the last asleep stage.
func asleepSpan(stages []normalize.SleepStage) (first, last time.Time, ok bool) {
	for _, s := range stages {
		switch s.Stage {
		case "light", "deep", "rem", "asleep_unspecified":
			if !ok || s.Start.Before(first) {
				first = s.Start
			}
			if !ok || s.End.After(last) {
				last = s.End
			}
			ok = true
		}
	}
	return first, last, ok
}

// BasisSessions marks a sleep group value read from its sessions in the episode.
const BasisSessions Basis = "sessions"

// ResolveEpisode is the window result of the sleep-family rule for episode e (w is its
// local_night or sleep_episode window). The episode groups go through Rule.Select, so sleep
// results carry the same statuses, ladder and warnings as any other metric. codes are the sleep
// codes to read: one fills Value, several fill Components. A selecting op reads every code from
// the selected group; a code it lacks goes to Missing (no_stage_data or no_data) instead of
// being taken from another group, and with a single code the window has no value and the
// group shows the code's status. Pooling ops pool each code over the groups that have it.
// An empty episode (no sessions) gives no_data.
func (a SleepAlignment) ResolveEpisode(w Window, e Episode, codes []string, opt Options) (WindowResult, error) {
	r := a.rule
	sel := a.Select(e)
	gvs := make([]GroupValue, len(r.Groups))
	for i, g := range sel.Groups {
		gv := GroupValue{Group: i, ID: r.Groups[i].ID, Status: g.Status, Reason: g.Reason, Coverage: g.Coverage}
		for _, f := range g.frags {
			gv.Count++
			gv.Sessions = append(gv.Sessions, f.ID)
			if !slices.Contains(gv.Sources, f.Source) {
				gv.Sources = append(gv.Sources, f.Source)
			}
			if gv.First.IsZero() || f.Start.Before(gv.First) {
				gv.First = f.Start
			}
			gv.At = timeMax(gv.At, f.End)
		}
		if gv.Count > 0 {
			gv.Basis = BasisSessions
		}
		gvs[i] = gv
	}
	resolveCodes := r.selectCodes
	if slices.Contains([]Op{OpMean, OpMin, OpMax, OpSum}, r.Strategy.Op) {
		resolveCodes = r.poolCodes
	}
	res, err := resolveCodes(w, sel.Groups, gvs, codes, opt)
	if err != nil {
		return WindowResult{}, err
	}
	res.Groups = append(res.Groups, a.sessionEntries(a.Excluded, e, StatusExcluded)...)
	res.Groups = append(res.Groups, a.sessionEntries(a.NotInRule, e, StatusNotInRule)...)
	for _, g := range res.Groups {
		switch g.Status {
		case StatusExcluded:
			res.Inputs.Excluded += g.Count
		case StatusNotInRule:
			res.Inputs.NotInRule += g.Count
		default:
			res.Inputs.Grouped += g.Count
		}
	}
	res.Partial = !opt.Now.IsZero() && w.Partial(opt.Now)
	return res, nil
}

// selectCodes reads every code from the group the strategy selects; a code that group lacks
// goes to Missing, and with a single code the window has no value.
func (r *Rule) selectCodes(w Window, sgs []SleepGroup, gvs []GroupValue, codes []string, opt Options) (WindowResult, error) {
	for i := range gvs {
		gvs[i].setSleepValues(sgs[i], codes)
	}
	res, err := r.Select(w, gvs, opt)
	if err != nil {
		return WindowResult{}, err
	}
	g := r.groupIndex(res.Selected)
	if g < 0 {
		return res, nil
	}
	for _, code := range codes {
		if v := sgs[g].Value(code); v.Status != StatusValid {
			if res.Missing == nil {
				res.Missing = map[string]GroupStatus{}
			}
			res.Missing[code] = v.Status
		}
	}
	if len(codes) == 1 && len(res.Missing) == 1 {
		pos := slices.IndexFunc(res.Groups, func(x GroupValue) bool { return x.Group == g })
		res.Status, res.Value = ResultNoData, 0
		res.Groups[pos].Status, res.Groups[pos].Reason = res.Missing[codes[0]], ""
	}
	return res, nil
}

// poolCodes pools each code over the groups that have it: groups lacking the code drop out of
// that code's pool. Several codes fill Components, and a code no group has goes to Missing.
func (r *Rule) poolCodes(w Window, sgs []SleepGroup, gvs []GroupValue, codes []string, opt Options) (WindowResult, error) {
	var missing map[string]GroupStatus
	values := map[string]float64{}
	for _, code := range codes {
		per := slices.Clone(gvs)
		for i := range per {
			v := sgs[i].Value(code)
			per[i].Value = v.Value
			if per[i].Status == StatusValid && v.Status != StatusValid {
				per[i].Status = v.Status
			}
		}
		cr, err := r.Select(w, per, opt)
		if err != nil || len(codes) == 1 {
			return cr, err
		}
		if cr.Status != ResultNoData {
			values[code] = cr.Value
			continue
		}
		if missing == nil {
			missing = map[string]GroupStatus{}
		}
		missing[code] = StatusNoStageData
		if !stageCodes[code] {
			missing[code] = StatusNoData
		}
	}
	for i := range gvs {
		gvs[i].setSleepValues(sgs[i], codes)
	}
	res, err := r.Select(w, gvs, opt)
	if err != nil {
		return WindowResult{}, err
	}
	res.Missing = missing
	if res.Status != ResultNoData {
		res.Value, res.Components = 0, values
	}
	return res, nil
}

// setSleepValues fills a group's value (one code) or components (several) from its sessions.
func (gv *GroupValue) setSleepValues(g SleepGroup, codes []string) {
	if len(codes) == 1 {
		v := g.Value(codes[0])
		gv.Value = v.Value
		if g.Status == StatusValid && v.Status != StatusValid {
			gv.Reason = string(v.Status) // the episode capture is valid, the code is missing
		}
		return
	}
	for _, code := range codes {
		if v := g.Value(code); v.Status == StatusValid {
			if gv.Components == nil {
				gv.Components = map[string]float64{}
			}
			gv.Components[code] = v.Value
		}
	}
}

// sessionEntries lists excluded or unmatched sessions that overlap e, once per source.
func (a SleepAlignment) sessionEntries(in []SleepInput, e Episode, st GroupStatus) []GroupValue {
	var out []GroupValue
	idx := map[Source]int{}
	for _, s := range in {
		if !s.End.After(e.Start) || !s.Start.Before(e.End) {
			continue
		}
		i, ok := idx[s.Source]
		if !ok {
			i = len(out)
			idx[s.Source] = i
			out = append(out, GroupValue{Group: -1, Status: st, Basis: BasisSessions, Sources: []Source{s.Source}})
		}
		g := &out[i]
		g.Count++
		g.Sessions = append(g.Sessions, s.ID)
		if g.First.IsZero() || s.Start.Before(g.First) {
			g.First = s.Start
		}
		g.At = timeMax(g.At, s.End)
	}
	return out
}

// ForceGroup applies a force_source override to an episode result made by ResolveEpisode:
// the named group becomes the selected one if it captured the episode (a partial capture
// included), and every code is read from it, so Missing then describes that group. It reports
// false when the group has no sessions in the episode. The status becomes overridden, or
// no_data when the forced group lacks the only code.
func (a SleepAlignment) ForceGroup(res *WindowResult, e Episode, codes []string, id string) bool {
	g := a.rule.groupIndex(id)
	if g < 0 {
		return false
	}
	sg := a.Select(e).Groups[g]
	if sg.Sessions == nil || !forceGroup(res, id) {
		return false
	}
	sg.Status = StatusValid // forced: the coverage gate no longer applies
	pos := slices.IndexFunc(res.Groups, func(x GroupValue) bool { return x.Group == g })
	res.Missing, res.Components, res.Value = nil, nil, 0
	for _, code := range codes {
		v := sg.Value(code)
		switch {
		case v.Status != StatusValid:
			if res.Missing == nil {
				res.Missing = map[string]GroupStatus{}
			}
			res.Missing[code] = v.Status
		case len(codes) == 1:
			res.Value = v.Value
		default:
			if res.Components == nil {
				res.Components = map[string]float64{}
			}
			res.Components[code] = v.Value
		}
	}
	res.Groups[pos].Value, res.Groups[pos].Components = res.Value, res.Components
	res.Status = ResultOverridden
	if len(codes) == 1 && len(res.Missing) == 1 {
		res.Status = ResultNoData
		res.Groups[pos].Status = res.Missing[codes[0]]
	}
	return true
}
