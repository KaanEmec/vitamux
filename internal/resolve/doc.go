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
// Built-ins and storage (J09.2):
//   - Builtins and LookupBuiltin are the builtin:<metric>:<n> defaults (builtin.go, generating
//     docs/resolution-defaults.md); NoBuiltin lists the codes deliberately without one.
//   - Store keeps immutable owner versions (rule:<metric>:<n>): Create (the first edit copies
//     the built-in as version 1), Activate (any version; ValidateSet on the active set),
//     Active, ActiveSet, History and Diff. Every mutation is audited with actor and diff.
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
//     fallback stays per window.
//
// Extensions (extensions.go, J09.10; results carry their inputs as plain fields):
//   - E1: Options.Events (ContextEvents: aligned episodes, workout clusters) or Options.Context
//     give each window a context (Rule.ContextAt); Select then uses Rule.Ladder for it.
//   - E2: within_source.statistic min / min_rolling_mean (GroupValue.SpanStart/SpanEnd); derived
//     catalogue codes (catalog.Metric.DerivedFrom) read the source metric's series.
//   - E3: quality.require_wear gates rows per base bucket by the wear series in the same Series
//     (load WearLookback of it); GroupValue.WearExempt, WornBuckets, Gated; reason not_worn.
//   - E5: Options.Leader (LeaderSelections of the leader's results) by window key.
//   - E9: a compose rule's local_day sums its hours; WindowResult.Hours keeps them.
//
// Results and loading (J09.8):
//   - BuildResult renders a Resolved window as Result, the documented result shape: value,
//     every group as a ResultInput (selected groups present as StatusUsed with Selected),
//     coverage, warnings, extension inputs, overrides and the explanation from the fixed
//     templates in explain.go. BuildSources and SleepAlignment.Sources make the all-sources
//     drilldown; SourcesUsed summarises a series.
//   - SleepAlignment.ResolveEpisode feeds an episode's groups through Rule.Select, so the sleep
//     family (and each sleep code) yields a WindowResult like any metric; Missing lists codes
//     the selected source lacks. SleepAlignment.ForceGroup applies force_source to it.
//   - Resolve and Run (load.go) are the only reads: rule (Store or Request.Rule), timezone
//     periods, overrides, rows and wear series (queries/resolve.sql), sleep and workouts for
//     night windows and contexts, and the follow leader's results. Each local date resolves from
//     exactly the rows a request for that date alone loads, so results never depend on the range.
//
// Cache and aggregates (J09.9, docs/architecture/resolution.md#cache-and-materialization):
//   - Run reads closed dates from resolved_cache and computes the span of the others
//     (cache.go); Request.Live, a draft Rule, Sources and bucket windows skip it. cacheDeps lists
//     what a date read; the triggers of the resolution_cache migration delete rows on dirty
//     marks, rule activation, workouts and timezone, device or origin changes.
//   - RebuildAggregates (the rebuild_aggregates job: Register, RebuildJob) consumes
//     resolution_dirty into source_hourly_aggregates; HourlyAggregates reads them.
//   - Verify compares cached and live results (`vitamux resolve verify`).
//
// Overrides (J09.7):
//   - Overrides stores manual overrides (Create, Revoke, Active, History); each change is audited
//     and marks resolution_dirty in its transaction. Rows are never deleted.
//   - Rule.ResolveWindowOverridden and ResolveWindowsOverridden take the loaded overrides and
//     return Resolved: the effective result, the applied and ignored overrides, and the
//     computed result without them.
package resolve
