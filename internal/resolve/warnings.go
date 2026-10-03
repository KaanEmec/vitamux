package resolve

// Warning is a result warning code. Results repeat a warning every time it applies; an
// acknowledgement in a rule only allows the rule to be saved.
type Warning string

// The warning catalogue. A new code needs a line here and in docs/architecture/resolution.md.
const (
	// WarnCrossSourceSum: values from several groups (or sub-sources) were added and may double count.
	// A rule using sum_across_sources or intra_group: sum must acknowledge it.
	WarnCrossSourceSum Warning = "cross_source_sum_duplicate_risk"
	// WarnCompositeExceeds: an hour-composed day (E9) is larger than every single source's day.
	WarnCompositeExceeds Warning = "composite_exceeds_any_source"
	// WarnInsufficientSources: fewer valid groups than min_sources; the value uses what was available.
	WarnInsufficientSources Warning = "insufficient_sources"
	// WarnPreferredUnavailable: a group ahead of the selected one had no valid value in this window.
	WarnPreferredUnavailable Warning = "preferred_source_unavailable"
	// WarnDefinitionChanged: a selection-only metric took a different group than the previous window.
	WarnDefinitionChanged Warning = "definition_changed"
	// WarnFollowUnavailable: the follow (E5) leader's group had no value; the rule's own ladder was used.
	WarnFollowUnavailable Warning = "follow_unavailable"
)

var warnings = map[Warning]bool{
	WarnCrossSourceSum: true, WarnCompositeExceeds: true, WarnInsufficientSources: true,
	WarnPreferredUnavailable: true, WarnDefinitionChanged: true, WarnFollowUnavailable: true,
}

// Known reports whether w is in the catalogue.
func (w Warning) Known() bool { return warnings[w] }
