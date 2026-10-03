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
package resolve
