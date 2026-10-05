import Foundation

/// The chart a metric gets: the twin of `web/src/lib/charts/grammar.ts`
/// (docs/architecture/frontend.md#chart-grammar). The raw values are the web's, so both sides
/// read the same expectations from `fixtures/chart-grammar.json`.
public enum ChartView: String, Sendable, CaseIterable {
    /// `TimeSeries` with a min–max band (intensive).
    case lineBand = "line-band"
    /// `Bars` per window (additive).
    case bars
    /// `TimeSeries` step line with readings (latest).
    case step
    /// `TimeSeries` with a baseline (daily summaries).
    case lineBaseline = "line-baseline"
    /// `Bars` stacked by stage, `Hypnogram` per night (sleep-derived).
    case sleep
    /// `RangeDumbbell` (blood-pressure groups).
    case dumbbell
}

/// What a client draws for a metric: its view and, when the catalogue has `intraday`, the Day
/// view's default and finest bucket (nil: measured once a day or night, so no Day view).
public struct ChartSpec: Hashable, Sendable {
    public var view: ChartView
    public var day: Components.Schemas.Intraday?
}

/// The view for a catalogue metric (GET /metrics): blood-pressure groups are dumbbells, everything
/// else follows its aggregation. No metric has its own chart code.
public func chartFor(_ metric: Components.Schemas.Metric) -> ChartSpec {
    ChartSpec(view: chartView(aggregation: metric.aggregation, group: metric.group), day: metric.intraday)
}

func chartView(aggregation: Components.Schemas.Metric.AggregationPayload, group: String?) -> ChartView {
    if group?.hasPrefix("bp_") == true { return .dumbbell }
    return switch aggregation {
    case .intensive: .lineBand
    case .additive: .bars
    case .latest: .step
    case .dailySummary: .lineBaseline
    case .sleepDerived: .sleep
    }
}
