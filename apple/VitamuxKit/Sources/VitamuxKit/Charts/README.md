The native chart kit ([ios-app#charts](../../../../../docs/architecture/ios-app.md#charts), [J22.6](../../../../../docs/plan/E22-ios-app/J22.6-chart-kit.md)), the twin of `web/src/lib/charts`, on Swift Charts.

- `ChartGrammar.swift`: `chartFor(metric)` from catalogue metadata (view and Day-view bucket). `fixtures/chart-grammar.json` is checked here (`ChartsTests`) and by `web/e2e/chart-grammar.spec.ts`.
- Views: `TimeSeries` (line, band, step, baseline and printed range, dots, ghost, source overlays), `Bars` (plain or stacked by stage), `Hypnogram`, `RangeDumbbell`, `EventLanes`, `Sparkline`, `CoverageStrip`; `RangePicker` and `TrendRollup` (week and month grains of GET /resolved/trend for All).
- Shared: `ChartFrame.swift` (legend, "Show as table", Reduce Motion, callout, status glyphs, pinch zoom, time axis), `ChartDescriptor.swift` (VoiceOver and Audio Graphs), `ChartModel.swift` (values, min/max decimation above 2,000 points), `ChartPalette.swift` (flat colour lookups).
- `ChartSamples.swift`: synthetic data for the `#Preview` gallery and the rendering tests.
