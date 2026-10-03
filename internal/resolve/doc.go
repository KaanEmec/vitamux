// Package resolve implements the per-metric resolution engine, overrides and cache (docs/architecture/resolution.md). It must not import connectors or api.
//
// Rules (ADR-0008, schemas/resolution-rule.v1.json):
//   - Rule and its parts are the typed rule; ParseRule decodes strictly and validates.
//   - Validate checks one rule against the catalogue; ValidateSet checks rules active together
//     (one per metric, follow cycles, leader windows). Errors are *ValidationError with
//     RFC 6901 pointers. Selection-only metrics are rejected for pooling through
//     catalog.Metric.Poolable, so a catalogue flag needs no change here.
//   - Warning and the Warn* constants are the warning catalogue.
//
// Grouping (pure, no database):
//   - Source is the selector identity of a row; Selector.Matches tests one selector.
//   - Rule.Assign places a source (exclusions win, then the first matching group);
//     Rule.Partition splits Input rows into groups, excluded and not_in_rule;
//     Rule.Ladder orders groups for a context (E1).
//
// Windows (pure; DST and travel through normalize.Timeline, ADR-0009 for nights):
//   - DayWindows, Buckets, LocalDay and LocalNight build windows of one local date;
//     EpisodeWindow, LatestWindow (with LatestInput) and ReadingWindows build the others.
//   - NightOf gives a session's night date; Episode is what the J09.6 episode builder returns.
//   - Window.Includes decides membership; Window.Partial detects open windows.
//
// Sleep and workout alignment (pure; callers load SleepInput and WorkoutInput rows):
//   - Rule.AlignSleep builds a night's episodes (fragment merge within FragmentGap, overlap
//     links, main episode); SleepAlignment.Main and NightEpisodes give what local_night reads.
//   - SleepAlignment.Select applies the sleep-family rule to one episode: SleepGroup coverage,
//     the min_episode_coverage gate (SleepPartialEpisode) and the selecting ops. Every sleep_*
//     code reads from that selection through SleepSelection.Value (or SleepGroup.Value for
//     pooling ops); a code the selected source lacks is SleepNoStageData or SleepNoData.
//   - ClusterWorkouts groups overlapping workouts; Rule.PickWorkout picks one per cluster and
//     keeps its segments, listing the others as alternates.
//
// Within-source values and strategies (pure; inputs by code in a Series):
//   - Rule.Aggregate computes one group's GroupValue in a window: intensive bucket means,
//     additive pro-rating with daily_value_policy (a daily total and intervals are never
//     added), intra_group, latest readings (family components from one reading) and
//     daily_summary.
//   - Rule.ResolveWindow runs partition, row gates (exclude_flags, plausible range), Aggregate,
//     group gates (coverage, staleness) and Rule.Select, the strategy step, into a WindowResult
//     with every group's GroupStatus and the warnings. Rule.ResolveWindows does a series, so
//     fallback stays per window. J09.10 parts return ErrNotImplemented.
package resolve
