package resolve

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// ResultOverridden is the status of a window an owner override changed (J09.7). A window whose
// override leaves it without a value stays no_data.
const ResultOverridden ResultStatus = "overridden"

// ReasonOverridden is the reason of a group that was selected by the strategy but lost to a
// force_source override.
const ReasonOverridden = "overridden"

// OverrideAction is what an override does in its window (docs/architecture/resolution.md#manual-overrides).
type OverrideAction string

const (
	ExcludeInput OverrideAction = "exclude_input" // drop one input row (a whole reading for a family) from the window
	ForceSource  OverrideAction = "force_source"  // use one rule group's value whatever the strategy chose
	SetValue     OverrideAction = "set_value"     // replace the window value, with a note
)

// Scope is what an override applies to: one window of one metric. Key is Window.Key and
// LocalDate the local date the window belongs to (Window.Date), so a change marks that day
// dirty for the cache.
type Scope struct {
	Metric    string
	Kind      catalog.Window
	Key       string
	LocalDate time.Time
}

// ScopeOf is the scope of window w of the rule metric.
func ScopeOf(metric string, w Window) Scope {
	return Scope{Metric: metric, Kind: w.Kind, Key: w.Key, LocalDate: w.Date}
}

// Override is one stored override. Only the fields of its Action are set: InputID for
// exclude_input, Group for force_source, Value (canonical unit), Unit and Note for set_value.
type Override struct {
	ID uuid.UUID
	Scope
	Action    OverrideAction
	InputID   int64  // measurements.id
	Group     string // rule group id
	Value     float64
	Unit      string
	Note      string
	CreatedBy string
	CreatedAt time.Time
	RevokedAt *time.Time // set by a revoke; the row stays as history
	RevokedBy string
}

// Active reports whether the override has not been revoked.
func (o Override) Active() bool { return o.RevokedAt == nil }

// Resolved is a window result with the overrides applied. WindowResult is the effective
// result; Computed is what the rule alone gave and stays visible for the explanation.
type Resolved struct {
	WindowResult
	Overrides []Override    // applied, oldest first; the result status is overridden when any changed the value
	Ignored   []Override    // matched the window but changed nothing (a row that is not in it, a group without a value, force_source beside set_value)
	Computed  *WindowResult // the result without overrides; nil when none matched the window
}

// ResolveWindowOverridden resolves one window like ResolveWindow, then applies the active
// overrides that match it; the caller loads them for the range (Overrides.Active). The engine
// stays pure and source rows are never touched.
//   - exclude_input removes the row from the inputs (the whole reading when it belongs to
//     one) and resolves again, so the strategy sees what remains.
//   - force_source makes the named group the selected one, if it has a value (valid, stale or
//     below quality, with inputs); the group the strategy chose is marked unused.
//   - set_value replaces the value last and wins over force_source.
//
// An override does not apply to a window of another metric, kind or key, nor once revoked.
func (r *Rule) ResolveWindowOverridden(w Window, s Series, opt Options, ovs []Override) (Resolved, error) {
	computed, err := r.ResolveWindow(w, s, opt)
	if err != nil {
		return Resolved{}, err
	}
	var mine []Override
	for _, o := range ovs {
		if o.Active() && o.Metric == r.Metric && o.Kind == w.Kind && o.Key == w.Key {
			mine = append(mine, o)
		}
	}
	if len(mine) == 0 {
		return Resolved{WindowResult: computed}, nil
	}
	slices.SortStableFunc(mine, func(a, b Override) int { return a.CreatedAt.Compare(b.CreatedAt) })
	out := Resolved{WindowResult: computed, Computed: &computed}

	var drop []int64
	var excludes []Override
	for _, o := range mine {
		if o.Action == ExcludeInput {
			excludes = append(excludes, o)
			drop = append(drop, o.InputID)
		}
	}
	if len(drop) > 0 {
		filtered, found := dropInputs(s, drop)
		for _, o := range excludes {
			if found[o.InputID] {
				out.Overrides = append(out.Overrides, o)
			} else {
				out.Ignored = append(out.Ignored, o)
			}
		}
		if len(found) > 0 {
			if out.WindowResult, err = r.ResolveWindow(w, filtered, opt); err != nil {
				return Resolved{}, err
			}
		}
	}

	var force, set *Override
	for i, o := range mine {
		switch o.Action {
		case ForceSource:
			force = &mine[i] // at most one is active (unique index); the last wins regardless
		case SetValue:
			set = &mine[i]
		case ExcludeInput: // handled above
		}
	}
	switch {
	case set != nil:
		out.Status, out.Value = ResultOverridden, set.Value
		out.Components, out.Selected, out.Warnings = nil, "", nil
		out.Overrides = append(out.Overrides, *set)
		if force != nil {
			out.Ignored = append(out.Ignored, *force)
		}
	case force != nil:
		if !forceGroup(&out.WindowResult, force.Group) {
			out.Ignored = append(out.Ignored, *force)
			break
		}
		out.Overrides = append(out.Overrides, *force)
	}
	// Status: any applied override marks the window, unless nothing is left to show.
	if len(out.Overrides) > 0 && out.Status != ResultNoData {
		out.Status = ResultOverridden
	}
	slices.SortStableFunc(out.Overrides, func(a, b Override) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

// ResolveWindowsOverridden resolves consecutive windows with overrides, as ResolveWindows does
// without them: each window falls back on its own and passes its selection to the next.
func (r *Rule) ResolveWindowsOverridden(ws []Window, s Series, opt Options, ovs []Override) ([]Resolved, error) {
	out := make([]Resolved, 0, len(ws))
	for _, w := range ws {
		res, err := r.ResolveWindowOverridden(w, s, opt, ovs)
		if err != nil {
			return nil, err
		}
		opt.Previous = res.Selected
		out = append(out, res)
	}
	return out, nil
}

// dropInputs returns s without the rows with the given ids and, for rows of a reading, the
// rest of that reading. found holds the ids that were present. s itself is not modified.
func dropInputs(s Series, ids []int64) (Series, map[int64]bool) {
	found := map[int64]bool{}
	groups := map[int64]bool{}
	for _, xs := range s {
		for _, x := range xs {
			if slices.Contains(ids, x.ID) {
				found[x.ID] = true
				if x.GroupID != 0 {
					groups[x.GroupID] = true
				}
			}
		}
	}
	out := make(Series, len(s))
	for code, xs := range s {
		out[code] = slices.DeleteFunc(slices.Clone(xs), func(x Input) bool {
			return found[x.ID] || (x.GroupID != 0 && groups[x.GroupID])
		})
	}
	return out, found
}

// forceGroup makes the group the selected one and reports whether it could: a group with no
// inputs has no value to force. The strategy's pick becomes unused; other groups keep their
// computed status.
func forceGroup(res *WindowResult, id string) bool {
	pos := slices.IndexFunc(res.Groups, func(g GroupValue) bool { return g.ID == id && g.ID != "" })
	if pos < 0 || res.Groups[pos].Count == 0 {
		return false
	}
	switch res.Groups[pos].Status {
	case StatusSelected, StatusUnused, StatusValid, StatusStale, StatusBelowQuality:
	default:
		return false
	}
	res.Groups = slices.Clone(res.Groups)
	for i := range res.Groups {
		if g := &res.Groups[i]; i != pos && g.Status == StatusSelected {
			g.Status, g.Reason = StatusUnused, ReasonOverridden
		}
	}
	g := &res.Groups[pos]
	g.Status, g.Reason = StatusSelected, ""
	res.Value, res.Components, res.Selected = g.Value, maps.Clone(g.Components), g.ID
	res.Warnings = nil
	for _, c := range g.Warnings {
		res.Warnings = append(res.Warnings, WindowWarning{Code: c, Group: g.ID})
	}
	return true
}

// validateOverride checks what Create stores; the database repeats the shape checks.
func validateOverride(n NewOverride) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidOverride, fmt.Sprintf(format, a...))
	}
	m, isMetric := catalog.Lookup(n.Metric)
	switch {
	case !isMetric && n.Metric != FamilyBloodPressure && n.Metric != FamilySleep:
		return bad("unknown metric %q", n.Metric)
	case !slices.Contains([]catalog.Window{catalog.WindowBucket, catalog.WindowHour, catalog.WindowLocalDay,
		catalog.WindowLocalNight, catalog.WindowSleepEpisode, catalog.WindowLatest, catalog.WindowReading}, n.Kind):
		return bad("unknown window kind %q", n.Kind)
	case isMetric && !m.AllowsWindow(n.Kind):
		return bad("window %s is not allowed for %s", n.Kind, n.Metric)
	case n.Key == "":
		return bad("window key is required")
	case n.LocalDate.IsZero():
		return bad("local date is required")
	case (n.Kind == catalog.WindowLocalDay || n.Kind == catalog.WindowLocalNight) && n.Key != midnightUTC(n.LocalDate).Format(dateLayout):
		return bad("window key %q is not the local date", n.Key)
	}
	switch n.Action {
	case ExcludeInput:
		if n.InputID <= 0 {
			return bad("exclude_input needs an input id")
		}
	case ForceSource:
		if n.Group == "" {
			return bad("force_source needs a group id")
		}
	case SetValue:
		switch {
		case !isMetric:
			return bad("set_value is not supported for %s; exclude an input or force a group", n.Metric)
		case math.IsNaN(n.Value) || math.IsInf(n.Value, 0):
			return bad("value is not a number in range")
		case m.Max > m.Min && (n.Value < m.Min || n.Value > m.Max):
			return bad("value is outside the plausible range of %s", n.Metric)
		case n.Unit != m.Unit:
			return bad("unit %q is not the canonical unit %q of %s", n.Unit, m.Unit, n.Metric)
		case n.Note == "":
			return bad("set_value needs a note")
		}
	default:
		return bad("unknown action %q", n.Action)
	}
	return nil
}
