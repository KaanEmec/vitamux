import SwiftUI
import VitamuxKit

/// The metric's chart from the grammar (`chartFor`): bars for additive days, a step line for
/// latest readings, a line with its 7-day range otherwise; rollups for All. Source series are
/// overlaid when toggled; when nothing resolved but sources have values, those are drawn with a
/// note why. Local dates plot at UTC midnight and format in UTC, so a date never shifts.
struct MetricChart: View {
    let model: MetricDetailModel
    /// A tapped day of the daily chart.
    var onDay: ((LocalDate) -> Void)?

    var body: some View {
        Group {
            switch model.values {
            case .loading:
                ProgressView("Loading values").frame(maxWidth: .infinity, minHeight: 220)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded:
                chart
            }
            if let problem = model.sourcesProblem { ProblemView(problem: problem) }
            if model.showCoverage { CoverageRows(model: model) }
        }
    }

    private var title: String { metricLabel(model.code) }
    private var hue: MetricHue { MetricHue.of(code: model.code, section: model.meta.value?.section) }
    private var view: ChartView { model.spec?.view ?? .lineBaseline }

    @ViewBuilder private var chart: some View {
        if let trend = model.trendValue, let rollup = TrendRollup.chart(trend, timeZone: .gmt) {
            TimeSeries(
                title: "\(title), \(rollup.series.label.lowercased())", series: [rollup.series], unit: model.unit, hue: hue,
                band: model.showBaseline ? rollup.band : nil, baseline: baseline, timeZone: .gmt, withTime: false
            )
            .accessibilityIdentifier("metricChart")
        } else if !model.plotted.isEmpty {
            let resolved = resolvedSeries
            let overlays = model.sourceSeries(only: model.shownSources)
            if (view == .bars || view == .sleep) && overlays.isEmpty {
                Bars(
                    title: "\(title), resolved per day", xs: resolved.points.map(\.x), values: resolved.points.map(\.y), unit: model.unit,
                    hue: hue, baseline: baseline, status: resolved.points.map(\.status), providers: resolved.points.map(\.providers), timeZone: .gmt,
                    onSelect: selectDay
                )
                .accessibilityIdentifier("metricChart")
            } else {
                TimeSeries(
                    title: "\(title), resolved per day", series: [resolved] + overlays, unit: model.unit, hue: hue,
                    kind: view == .step || view == .dumbbell ? .step : .line, area: view != .step && view != .dumbbell,
                    band: model.showBaseline ? band(resolved) : nil, baseline: baseline, timeZone: .gmt, withTime: false, onSelect: selectDay
                )
                .accessibilityIdentifier("metricChart")
            }
        } else if case let fallback = model.sourceSeries().filter({ $0.points.contains { $0.y != nil } }), !fallback.isEmpty, model.range != .all {
            Text("Nothing resolved in this range, so each source’s own values are shown. Check the rule’s sources under “How it’s calculated”.")
                .font(.footnote)
                .foregroundStyle(.secondary)
            TimeSeries(title: "\(title), each source per day", series: fallback, unit: model.unit, hue: hue, timeZone: .gmt, withTime: false)
                .accessibilityIdentifier("metricChart")
        } else {
            ContentUnavailableView("No values in this range", systemImage: "chart.xyaxis.line", description: Text("Choose a longer range, or check the sources on the Sources tab."))
                .accessibilityIdentifier("noValues")
        }
    }

    /// The day at an index of the daily chart, for `onDay`.
    private var selectDay: ((Int) -> Void)? {
        onDay.map { open in { i in if model.dates.indices.contains(i) { open(model.dates[i]) } } }
    }

    /// One resolved value per day, with its status and the providers behind it.
    private var resolvedSeries: ChartSeries {
        ChartSeries(label: "Resolved", points: model.dates.map { date in
            let value = model.value(on: date)
            return ChartPoint(x: date.start(in: .gmt), y: value?.value?.number(for: model.code), status: DataStatus(value), providers: dayProviders(value))
        })
    }

    /// The period mean as a labelled line.
    private var baseline: ChartBaseline? {
        guard model.showBaseline else { return nil }
        if let trend = model.trendValue {
            let n = trend.buckets.reduce(0) { $0 + ($1.mean == nil ? 0 : $1.n) }
            guard n > 0 else { return nil }
            return ChartBaseline(value: trend.buckets.reduce(0) { $0 + ($1.mean ?? 0) * Double($1.n) } / Double(n), label: "Mean")
        }
        let values = model.plotted
        guard let mean = model.comparison?.current.mean ?? (values.isEmpty ? nil : values.reduce(0, +) / Double(values.count)) else { return nil }
        return ChartBaseline(value: mean, label: model.range.days.map { "\($0)-day mean" } ?? "Mean")
    }

    /// A 7-day range around the line; gaps stay gaps.
    private func band(_ series: ChartSeries) -> ChartBand {
        let ys = series.points.map(\.y)
        return ChartBand(label: "7-day range", points: series.points.enumerated().map { i, point in
            let window = ys[max(0, i - 6)...i].compactMap(\.self)
            guard point.y != nil, !window.isEmpty else { return .init(x: point.x, low: nil, high: nil) }
            return .init(x: point.x, low: window.min(), high: window.max())
        })
    }
}

/// Coverage per source (GET /coverage) and the source behind each day's value.
private struct CoverageRows: View {
    let model: MetricDetailModel

    var body: some View {
        if let start = model.dates.first {
            let rows = (model.coverage?.rows ?? []).map {
                CoverageStrip.Row(label: providerLabel($0.source), source: $0.source, cells: $0.days)
            }
            let picks = model.dates.map { dayProviders(model.value(on: $0)).first }
            let perDay = CoverageStrip.Row(label: "Source per day", cells: picks.map { $0 == nil ? 0 : 1 }, picks: picks)
            CoverageStrip(caption: "\(metricLabel(model.code)): coverage and the source behind each day", rows: rows + [perDay], start: start)
                .accessibilityIdentifier("coverageStrip")
        } else {
            Text("Coverage is shown for day ranges.").font(.footnote).foregroundStyle(.secondary)
        }
    }
}

/// Baseline, each source's own series, and coverage.
struct SeriesToggles: View {
    @Bindable var model: MetricDetailModel

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                Toggle(model.range == .day ? "Min–max band" : "Baseline", isOn: $model.showBaseline).accessibilityIdentifier("toggleBaseline")
                ForEach(model.providers, id: \.self) { provider in
                    Toggle(isOn: Binding {
                        model.shownSources.contains(provider)
                    } set: { on in
                        if on { model.shownSources.insert(provider) } else { model.shownSources.remove(provider) }
                    }) {
                        HStack(spacing: 6) {
                            SourceDot(provider: provider)
                            Text(providerLabel(provider))
                        }
                    }
                    .accessibilityIdentifier("toggleSource-\(provider)")
                }
                if model.range != .all {
                    Toggle("Coverage", isOn: $model.showCoverage).accessibilityIdentifier("toggleCoverage")
                }
            }
            .toggleStyle(.button)
            .buttonBorderShape(.capsule)
            .font(.subheadline)
        }
    }
}
