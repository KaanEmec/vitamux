package resolve

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// FieldError points at one invalid rule field (RFC 6901 pointer). Detail never echoes values
// other than catalogue codes and group ids.
type FieldError struct {
	Pointer string
	Detail  string
}

// ValidationError lists every problem found in a rule or rule set.
type ValidationError struct{ Errors []FieldError }

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Errors))
	for i, f := range e.Errors {
		parts[i] = f.Pointer + ": " + f.Detail
	}
	return "resolve: invalid rule: " + strings.Join(parts, "; ")
}

// lookupFunc finds a catalogue metric; tests swap in a catalogue with extra codes.
type lookupFunc func(code string) (catalog.Metric, bool)

// Validate checks one rule against the schema and the catalogue. Checks that need other rules
// (follow cycles, leader windows) are in ValidateSet. Errors are *ValidationError.
func Validate(r *Rule) error { return validate(r, catalog.Lookup) }

// ValidateSet validates rules that are active together: each rule, one rule per metric, no
// follow cycles, and a follower uses its leader rule's window. Pointers start with the index.
func ValidateSet(rules []*Rule) error { return validateSet(rules, catalog.Lookup) }

var (
	groupIDRe  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	providerRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	bucketSize = map[Duration]bool{"1m": true, "5m": true, "15m": true, "30m": true}
	flagNames  = map[string]bool{"manual_entry": true, "motion_context": true, "implausible": true,
		"relayed": true, "migrated_without_raw": true, "prorated_source": true}
	windowKinds = []catalog.Window{catalog.WindowBucket, catalog.WindowHour, catalog.WindowLocalDay,
		catalog.WindowLocalNight, catalog.WindowSleepEpisode, catalog.WindowLatest, catalog.WindowReading}
	opStrategy = map[Op]catalog.Strategy{
		OpSingleSource: catalog.SingleSource, OpFirstAvailable: catalog.FirstAvailable,
		OpMean: catalog.Mean, OpMin: catalog.Min, OpMax: catalog.Max, OpSum: catalog.Sum,
		OpLatest: catalog.LatestOf, OpEarliest: catalog.Earliest, OpEventPriority: catalog.EventPriority,
	}
)

// pooling reports whether op combines values of several groups.
func (o Op) pooling() bool { return o == OpMean || o == OpMin || o == OpMax || o == OpSum }

type checker struct {
	errs   []FieldError
	lookup lookupFunc
}

func (c *checker) check(ok bool, ptr, detail string) bool {
	if !ok {
		c.errs = append(c.errs, FieldError{Pointer: ptr, Detail: detail})
	}
	return ok
}

func (c *checker) err() error {
	if len(c.errs) == 0 {
		return nil
	}
	return &ValidationError{Errors: c.errs}
}

func validate(r *Rule, lookup lookupFunc) error {
	c := &checker{lookup: lookup}
	c.rule(r)
	return c.err()
}

func (c *checker) rule(r *Rule) {
	c.check(r.Schema == SchemaV1, "/schema", "must be "+SchemaV1)
	c.groups(r)
	for i, s := range r.Exclude {
		c.selector(s, fmt.Sprintf("/exclude/%d", i))
	}
	c.contexts(r)
	acked := map[Warning]bool{}
	for i, w := range r.AcknowledgedWarnings {
		ptr := fmt.Sprintf("/acknowledged_warnings/%d", i)
		c.check(w.Known(), ptr, "unknown warning code")
		c.check(!acked[w], ptr, "duplicate warning code")
		acked[w] = true
	}
	m, family, ok := c.metric(r.Metric)
	if !ok {
		return // the remaining checks depend on the metric's aggregation
	}
	c.window(r, m)
	c.strategy(r, m, family, acked)
	c.withinSource(r, m, acked)
	c.quality(r, m, family)
	c.follow(r, family)
	c.compose(r, m)
}

// metric resolves the rule's metric. A family returns a representative member with the
// family's name as Code, so the catalogue's window and strategy lists apply.
func (c *checker) metric(code string) (catalog.Metric, bool, bool) {
	var rep string
	switch code {
	case "":
		c.check(false, "/metric", "required")
		return catalog.Metric{}, false, false
	case FamilySleep:
		rep = "sleep_total"
	case FamilyBloodPressure:
		rep = "bp_systolic"
	}
	if rep != "" {
		m, ok := c.lookup(rep)
		if !c.check(ok, "/metric", "rule family has no catalogue codes") {
			return m, true, false
		}
		m.Code = code
		return m, true, true
	}
	m, ok := c.lookup(code)
	switch {
	case !c.check(ok, "/metric", "unknown metric code"):
	case !c.check(m.Agg != catalog.SleepDerived, "/metric", "sleep codes are resolved together by the sleep-family rule (metric: sleep)"):
	case !c.check(m.Group != "bp_reading", "/metric", "blood pressure components are resolved together by the blood_pressure rule"):
	default:
		return m, false, true
	}
	return m, false, false
}

func (c *checker) groups(r *Rule) {
	if !c.check(len(r.Groups) > 0, "/groups", "at least one group is required") {
		return
	}
	c.check(len(r.Groups) <= 32, "/groups", "at most 32 groups")
	seen := map[string]bool{}
	for i, g := range r.Groups {
		ptr := fmt.Sprintf("/groups/%d", i)
		if c.check(groupIDRe.MatchString(g.ID), ptr+"/id", "must be 1-32 lowercase letters, digits or '_', starting with a letter") {
			c.check(!seen[g.ID], ptr+"/id", "duplicate group id")
		}
		seen[g.ID] = true
		c.check(len(g.Match) > 0, ptr+"/match", "at least one selector is required")
		for j, s := range g.Match {
			c.selector(s, fmt.Sprintf("%s/match/%d", ptr, j))
		}
	}
}

func (c *checker) selector(s Selector, ptr string) {
	if !c.check(s != (Selector{}), ptr, "must set at least one field") {
		return
	}
	c.check(s.Provider == "" || providerRe.MatchString(s.Provider), ptr+"/provider", "must be a provider code")
	c.check(s.ConnectionID == "" || canonicalUUID(s.ConnectionID), ptr+"/connection_id", "must be a lowercase UUID")
	c.check(s.DeviceID == "" || canonicalUUID(s.DeviceID), ptr+"/device_id", "must be a lowercase UUID")
	c.check(s.OriginKey == "" || s.OriginKeyPrefix == "", ptr+"/origin_key_prefix", "set origin_key or origin_key_prefix, not both")
	for name, v := range map[string]string{"origin_key": s.OriginKey, "origin_key_prefix": s.OriginKeyPrefix,
		"origin_name": s.OriginName, "device_type": s.DeviceType, "device_model": s.DeviceModel} {
		c.check(len(v) <= 256, ptr+"/"+name, "at most 256 characters")
	}
	c.check(s.Entry == "" || s.Entry == EntryDevice || s.Entry == EntryManual, ptr+"/entry", "must be device or manual")
}

func canonicalUUID(s string) bool {
	u, err := uuid.Parse(s)
	return err == nil && u.String() == s
}

func (c *checker) contexts(r *Rule) {
	for ctx, ids := range r.Contexts {
		ptr := "/contexts/" + string(ctx)
		if !c.check(ctx == ContextWorkout || ctx == ContextSleep, ptr, "unknown context; use workout or sleep") {
			continue
		}
		c.check(len(ids) > 0, ptr, "at least one group id is required")
		seen := map[string]bool{}
		for i, id := range ids {
			p := fmt.Sprintf("%s/%d", ptr, i)
			if id == ContextWorkoutSource {
				c.check(ctx == ContextWorkout, p, ContextWorkoutSource+" is only valid in contexts.workout")
			} else {
				c.check(r.groupIndex(id) >= 0, p, "names no group of this rule")
			}
			c.check(!seen[id], p, "duplicate entry")
			seen[id] = true
		}
	}
}

func (c *checker) window(r *Rule, m catalog.Metric) {
	w := r.Window
	if !c.check(slices.Contains(windowKinds, w.Kind), "/window/kind", "must be bucket, hour, local_day, local_night, sleep_episode, latest or reading") {
		return
	}
	c.check(m.AllowsWindow(w.Kind), "/window/kind", fmt.Sprintf("window %s is not allowed for %s (%s)", w.Kind, m.Code, m.Agg))
	if w.Kind == catalog.WindowBucket {
		c.check(bucketSize[w.Size], "/window/size", "must be 1m, 5m, 15m or 30m")
	} else {
		c.check(w.Size == "", "/window/size", "only bucket windows take a size")
	}
}

func (c *checker) strategy(r *Rule, m catalog.Metric, family bool, acked map[Warning]bool) {
	s := r.Strategy
	cs, ok := opStrategy[s.Op]
	if !c.check(ok, "/strategy/op", "must be single_source, first_available, mean_across_sources, minimum_across_sources, maximum_across_sources, sum_across_sources, latest, earliest or event_priority") {
		return
	}
	if !m.AllowsStrategy(cs) {
		detail := fmt.Sprintf("%s is not allowed for %s (%s)", s.Op, m.Code, m.Agg)
		switch {
		case s.Op.pooling() && s.Op != OpSum && !m.Poolable():
			// Provider-scoped scores and selection-only metrics (catalog.Metric.SelectionOnly).
			detail = m.Code + " is never pooled across sources (provider-scoped or selection-only); use first_available or single_source"
		case s.Op == OpSum:
			detail = "sum_across_sources needs an additive metric"
		case s.Op == OpEventPriority:
			detail = "event_priority applies to the sleep family only"
		}
		c.check(false, "/strategy/op", detail)
	}
	if family && m.Group != "" {
		c.check(!s.Op.pooling(), "/strategy/op", "components of one reading never mix sources; use a selecting strategy")
	}
	if s.Op == OpSingleSource {
		c.check(len(r.Groups) <= 1, "/groups", "single_source takes exactly one group")
	}
	if s.MinSources != 0 {
		if c.check(s.Op.pooling(), "/strategy/min_sources", "applies to mean, minimum, maximum and sum only") {
			c.check(s.MinSources >= 1, "/strategy/min_sources", "must be at least 1")
			c.check(s.MinSources <= len(r.Groups), "/strategy/min_sources", "cannot exceed the number of groups")
		}
	}
	if s.OnInsufficient != "" {
		if c.check(s.Op.pooling(), "/strategy/on_insufficient", "applies to mean, minimum, maximum and sum only") {
			c.check(s.OnInsufficient == UseAvailable || s.OnInsufficient == NoValue, "/strategy/on_insufficient", "must be use_available or no_value")
		}
	}
	if s.Op == OpSum {
		c.check(acked[WarnCrossSourceSum], "/acknowledged_warnings", "sum_across_sources needs "+string(WarnCrossSourceSum)+" acknowledged")
	}
}

func (c *checker) withinSource(r *Rule, m catalog.Metric, acked map[Warning]bool) {
	ws := r.WithinSource
	if ws == nil {
		return
	}
	bucketed := m.BaseBucket() > 0
	switch ws.IntraGroup {
	case "", IntraAuto:
	case IntraMean, IntraMax, IntraSum:
		if c.check(bucketed, "/within_source/intra_group", "applies to bucketed (intensive or additive) metrics only") && ws.IntraGroup == IntraSum {
			c.check(m.Agg == catalog.Additive, "/within_source/intra_group", "sum needs an additive metric")
			c.check(acked[WarnCrossSourceSum], "/acknowledged_warnings", "intra_group: sum needs "+string(WarnCrossSourceSum)+" acknowledged")
		}
	default:
		c.check(false, "/within_source/intra_group", "must be auto, mean, max or sum")
	}
	switch ws.DailyValuePolicy {
	case "":
	case PreferReported, IntervalsOnly:
		c.check(m.Agg == catalog.Additive, "/within_source/daily_value_policy", "applies to additive metrics only")
	default:
		c.check(false, "/within_source/daily_value_policy", "must be prefer_reported or intervals_only")
	}
	nightly := []catalog.Window{catalog.WindowLocalDay, catalog.WindowLocalNight, catalog.WindowSleepEpisode}
	k := r.Window.Kind
	switch ws.Statistic {
	case "":
	case StatMinRollingMean:
		c.check(m.Agg == catalog.Intensive, "/within_source/statistic", "min_rolling_mean needs an intensive metric")
		c.check(slices.Contains(nightly, k), "/within_source/statistic", "min_rolling_mean needs a local_day, local_night or sleep_episode window")
		span, base := ws.Span.Std(), m.BaseBucket()
		if c.check(span > 0, "/within_source/span", "required with min_rolling_mean; e.g. 30m") && base > 0 {
			c.check(span >= base && span%base == 0 && span <= 12*time.Hour, "/within_source/span",
				"must be a multiple of the "+base.String()+" base bucket, at most 12h")
		}
	case StatMin:
		c.check(m.Agg == catalog.Intensive, "/within_source/statistic", "min needs an intensive metric")
		c.check(k == catalog.WindowHour || slices.Contains(nightly, k), "/within_source/statistic", "min needs an hour, local_day, local_night or sleep_episode window")
	case StatLatest, StatMean:
		c.check(m.Agg == catalog.Latest, "/within_source/statistic", string(ws.Statistic)+" applies to latest-type metrics only")
		c.check(k == catalog.WindowLocalDay, "/within_source/statistic", string(ws.Statistic)+" needs a local_day window")
	default:
		c.check(false, "/within_source/statistic", "must be min_rolling_mean, min, latest or mean")
	}
	if ws.Statistic != StatMinRollingMean {
		c.check(ws.Span == "", "/within_source/span", "only with statistic min_rolling_mean")
	}
}

func (c *checker) quality(r *Rule, m catalog.Metric, family bool) {
	q := r.Quality
	if q == nil {
		return
	}
	bucketed := m.BaseBucket() > 0
	if q.MinCoverage != nil {
		c.check(*q.MinCoverage > 0 && *q.MinCoverage <= 1, "/quality/min_coverage", "must be in (0, 1]")
		c.check(bucketed, "/quality/min_coverage", "applies to bucketed (intensive or additive) metrics only")
	}
	if q.PlausibleRange != nil {
		if c.check(!family, "/quality/plausible_range", "a rule family has no single unit") &&
			c.check(len(q.PlausibleRange) == 2, "/quality/plausible_range", "must be [low, high]") {
			c.check(q.PlausibleRange[0] < q.PlausibleRange[1], "/quality/plausible_range", "low must be below high")
		}
	}
	seen := map[string]bool{}
	for i, f := range q.ExcludeFlags {
		ptr := fmt.Sprintf("/quality/exclude_flags/%d", i)
		c.check(flagNames[f], ptr, "unknown quality flag")
		c.check(!seen[f], ptr, "duplicate flag")
		seen[f] = true
	}
	c.check(q.MaxStaleness == "" || q.MaxStaleness.Std() > 0, "/quality/max_staleness", "must be a duration such as 36h or 30d")
	if q.RequireWear != "" && c.check(bucketed, "/quality/require_wear", "needs a metric with buckets (intensive or additive)") {
		wm, ok := c.lookup(q.RequireWear)
		c.check(ok && wm.Agg == catalog.Intensive, "/quality/require_wear", "must name an intensive sample metric such as heart_rate")
	}
	if s := q.Sleep; s != nil {
		if s.MatchOverlap != nil {
			c.check(*s.MatchOverlap > 0 && *s.MatchOverlap <= 1, "/quality/sleep/match_overlap", "must be in (0, 1]")
		}
		if s.MinEpisodeCoverage != nil {
			c.check(*s.MinEpisodeCoverage > 0 && *s.MinEpisodeCoverage <= 1, "/quality/sleep/min_episode_coverage", "must be in (0, 1]")
		}
		if s.NightAnchor != "" {
			_, ok := parseAnchor(s.NightAnchor)
			c.check(ok, "/quality/sleep/night_anchor", "must be a local time HH:MM between 12:00 and 23:59")
		}
	}
}

func (c *checker) follow(r *Rule, family bool) {
	if r.Follow == "" {
		return
	}
	if !c.check(!family, "/follow", "a rule family cannot follow another metric") ||
		!c.check(r.Follow != r.Metric, "/follow", "a rule cannot follow itself") {
		return
	}
	lm, ok := c.lookup(r.Follow)
	switch {
	case !c.check(ok, "/follow", "unknown metric code"):
	case !c.check(lm.Agg != catalog.SleepDerived && lm.Group != "bp_reading", "/follow", "cannot follow a member of a rule family"):
	default:
		c.check(lm.AllowsWindow(r.Window.Kind), "/follow", fmt.Sprintf("leader %s cannot use window %s", lm.Code, r.Window.Kind))
	}
}

func (c *checker) compose(r *Rule, m catalog.Metric) {
	cp := r.Compose
	if cp == nil {
		return
	}
	c.check(cp.From == catalog.WindowHour, "/compose/from", "must be hour")
	c.check(cp.Op == ComposeFirstAvailable || cp.Op == ComposeMax, "/compose/op", "must be first_available or max")
	c.check(m.Agg == catalog.Additive, "/compose", "compose needs an additive metric")
	c.check(r.Window.Kind == catalog.WindowLocalDay, "/compose", "compose needs a local_day window")
}

func validateSet(rules []*Rule, lookup lookupFunc) error {
	c := &checker{lookup: lookup}
	byMetric := map[string]int{}
	for i, r := range rules {
		sub := &checker{lookup: lookup}
		sub.rule(r)
		for _, e := range sub.errs {
			c.check(false, fmt.Sprintf("/%d%s", i, e.Pointer), e.Detail)
		}
		if j, dup := byMetric[r.Metric]; dup {
			c.check(false, fmt.Sprintf("/%d/metric", i), fmt.Sprintf("rule %d already covers this metric", j))
			continue
		}
		byMetric[r.Metric] = i
	}
	for i, r := range rules {
		if r.Follow == "" {
			continue
		}
		ptr := fmt.Sprintf("/%d/follow", i)
		if j, ok := byMetric[r.Follow]; ok && rules[j].Window != r.Window {
			c.check(false, ptr, fmt.Sprintf("leader rule uses window %s; a follower must use the same window", windowLabel(rules[j].Window)))
		}
		// Walk the chain; each rule has at most one leader, so a revisit of r is a cycle through it.
		chain, cur := []string{r.Metric}, r
		for steps := 0; cur.Follow != "" && steps <= len(rules); steps++ {
			chain = append(chain, cur.Follow)
			if cur.Follow == r.Metric {
				c.check(false, ptr, "follow cycle: "+strings.Join(chain, " -> "))
				break
			}
			j, ok := byMetric[cur.Follow]
			if !ok {
				break
			}
			cur = rules[j]
		}
	}
	return c.err()
}

func windowLabel(w RuleWindow) string {
	if w.Size != "" {
		return string(w.Kind) + " " + string(w.Size)
	}
	return string(w.Kind)
}
