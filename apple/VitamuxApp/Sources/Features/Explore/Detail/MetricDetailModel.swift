import Foundation
import Observation
import VitamuxKit

/// Metric detail (the panel's `/explore/{metric}`): the catalogue entry (`GET /metrics/{code}`)
/// picks the chart (`chartFor`); up to a year of resolved days (`GET /resolved/daily`) or, for All,
/// weekly or monthly rollups (`GET /resolved/trend`); the stats header (`GET /resolved/summary`
/// with comparisons); each source's own values (`GET /sources/series`) and coverage
/// (`GET /coverage`) as toggles. Overrides bump `version`, which loads the values again.
@Observable
final class MetricDetailModel {
    /// What the chart plots: one resolved value per day, or rollups for All.
    enum Values {
        case days([LocalDate], [String: ResolvedValue])
        case trend(Components.Schemas.ResolvedTrend?)
    }

    /// Reloads when any of these change.
    struct Key: Equatable {
        var range: ChartRange
        var end: LocalDate
        var version: Int
    }

    let code: String
    var range: ChartRange
    var end: LocalDate
    let latest: LocalDate
    private(set) var version = 0

    private(set) var meta: Loadable<Components.Schemas.Metric> = .loading
    private(set) var summary: Components.Schemas.MetricSummary?
    private(set) var values: Loadable<Values> = .loading
    private(set) var sources: Components.Schemas.SourceSeries?
    private(set) var sourcesProblem: Problem?
    private(set) var coverage: Components.Schemas.Coverage?

    var showBaseline = true
    var shownSources: Set<String> = []
    var showCoverage = false

    init(code: String, range: String?, end: String?, today: LocalDate = .today(in: .current)) {
        self.code = code
        self.range = range.flatMap(ChartRange.init(rawValue:)) ?? .quarter
        latest = today
        self.end = min(end.flatMap(LocalDate.init) ?? today, today)
    }

    var key: Key { Key(range: range, end: end, version: version) }

    /// After an override or a revoke: load the values again.
    func changed() {
        version += 1
    }

    func loadMeta(_ client: Client?) async {
        guard let client else { return }
        meta = await Loadable { try await client.getMetric(path: .init(code: code)).ok.body.json }
    }

    var notFound: Bool {
        if case .failed(let problem) = meta { problem.status == 404 } else { false }
    }

    var spec: ChartSpec? { meta.value.map(chartFor) }
    var unit: String { meta.value?.unit ?? summary?.unit ?? trendValue?.unit ?? "" }

    func load(_ client: Client?) async {
        guard let client else { return }
        let key = key
        async let summary = try? client.getResolvedSummary(query: .init(metrics: [code], date: key.end.description, compare: true)).ok.body.json
        let values = await Loadable { try await self.values(client, key) }
        let answer = await summary
        guard key == self.key else { return }
        self.values = values
        self.summary = answer?.metrics.additionalProperties[code]
        await loadSources(client)
        if showCoverage { await loadCoverage(client) }
    }

    private func values(_ client: Client, _ key: Key) async throws -> Values {
        if let start = key.range.start(endingOn: key.end) {
            let answer = try await client.getResolvedDaily(query: .init(startDate: start.description, endDate: key.end.description, metrics: [code])).ok.body.json
            var byDate: [String: ResolvedValue] = [:]
            for day in answer.days { byDate[day.localDate] = day.metrics.additionalProperties[code] }
            var dates: [LocalDate] = []
            var d = start
            while d <= key.end {
                dates.append(d)
                d = d.adding(days: 1)
            }
            return .days(dates, byDate)
        }
        // All: monthly rollups over ten years; up to two years of data read better by week.
        let first = TrendRollup.query(endingOn: key.end)
        var trend = try await client.getResolvedTrend(query: .init(metric: code, startDate: first.start.description, endDate: key.end.description, grain: .month)).ok.body.json
        if let weekly = TrendRollup.refinement(of: trend, endingOn: key.end) {
            trend = try await client.getResolvedTrend(query: .init(metric: code, startDate: weekly.start.description, endDate: key.end.description, grain: .week)).ok.body.json
        }
        guard let firstData = trend.buckets.firstIndex(where: { $0.n > 0 }) else { return .trend(nil) }
        trend.buckets = Array(trend.buckets[firstData...])
        return .trend(trend)
    }

    /// Each source's own daily values, with a day of margin either side.
    private func loadSources(_ client: Client) async {
        guard let from else { return }
        let first = max(from, end.adding(days: -3655))
        do {
            sources = try await client.getSourceSeries(query: .init(
                metric: code, start: first.adding(days: -1).start(in: .gmt), end: end.adding(days: 2).start(in: .gmt), grain: .day
            )).ok.body.json
            sourcesProblem = nil
        } catch {
            sources = nil
            // Sleep and derived codes have no source series: nothing to toggle, not a failure.
            let problem = Problem(error)
            sourcesProblem = problem.status == 422 ? nil : problem
        }
    }

    func loadCoverage(_ client: Client?) async {
        guard let client, let start = range.start(endingOn: end) else { return }
        coverage = try? await client.getCoverage(query: .init(startDate: start.description, endDate: end.description, metric: [code])).ok.body.json
    }

    // MARK: Derived

    var dates: [LocalDate] {
        if case .loaded(.days(let dates, _)) = values { dates } else { [] }
    }

    func value(on date: LocalDate) -> ResolvedValue? {
        if case .loaded(.days(_, let byDate)) = values { byDate[date.description] } else { nil }
    }

    var trendValue: Components.Schemas.ResolvedTrend? {
        if case .loaded(.trend(let trend)) = values { trend } else { nil }
    }

    /// First local date on the chart: the range's, or the first rollup with data.
    var from: LocalDate? {
        range.start(endingOn: end) ?? trendValue.flatMap { $0.buckets.first.flatMap { LocalDate($0.startDate) } }
    }

    /// The plotted numbers (days with a value, or rollup means).
    var plotted: [Double] {
        if let trend = trendValue { return trend.buckets.compactMap(\.mean) }
        return dates.compactMap { value(on: $0)?.value?.number(for: code) }
    }

    var providers: [String] {
        var seen = Set<String>()
        return (sources?.sources ?? []).map(\.provider).filter { seen.insert($0).inserted }
    }

    var rule: Components.Schemas.RuleRef? {
        summary?.rule ?? trendValue?.rule ?? dates.lazy.compactMap { self.value(on: $0)?.rule }.first
    }

    /// The newest resolved value in the summary's 30 days.
    var latestPoint: (date: String, value: Double)? {
        summary?.sparkline.reversed().lazy.compactMap { p in p.value?.number(for: self.code).map { (p.localDate, $0) } }.first
    }

    /// The 7-, 30- and 90-day rollups ending on `end`.
    func mean(days: Int) -> Double? {
        summary?.stats.first { $0.days == days }?.mean
    }

    /// The period comparison matching the range, for the neutral delta.
    var comparison: Components.Schemas.PeriodComparison? {
        summary?.comparisons?.first { $0.days.rawValue == range.days }
    }

    var lowest: Double? {
        trendValue.map { $0.buckets.compactMap(\.min).min() } ?? plotted.min()
    }

    var highest: Double? {
        trendValue.map { $0.buckets.compactMap(\.max).max() } ?? plotted.max()
    }

    /// Days with a value, and days in the range.
    var coverageCount: (with: Int, of: Int) {
        if let trend = trendValue { return (trend.buckets.reduce(0) { $0 + $1.n }, trend.buckets.reduce(0) { $0 + $1.days }) }
        return (plotted.count, dates.count)
    }

    /// Each source's own values in the range, as chart series.
    func sourceSeries(only shown: Set<String>? = nil) -> [ChartSeries] {
        guard let sources, let from else { return [] }
        let additive = sources.aggregation == .additive
        return sources.sources.filter { shown?.contains($0.provider) ?? true }.map { source in
            let points = source.points.compactMap { p -> ChartPoint? in
                guard let date = LocalDate(p.localDate), date >= from, date <= end else { return nil }
                return ChartPoint(x: date.start(in: .gmt), y: p.dailyValue ?? (additive ? p.sum : p.mean))
            }
            return ChartSeries(label: sourceLabel(source), points: points, source: source.provider)
        }
    }

    private func sourceLabel(_ source: Components.Schemas.SourceSeriesSource) -> String {
        var parts = [providerLabel(source.provider)]
        if let detail = source.origin?.name ?? source.origin?.key ?? source.device?.model ?? source.device?._type { parts.append(detail) }
        if source.ruleStatus == .excluded { parts.append("excluded") }
        if source.ruleStatus == .notInRule { parts.append("not in rule") }
        return parts.joined(separator: " · ")
    }
}
