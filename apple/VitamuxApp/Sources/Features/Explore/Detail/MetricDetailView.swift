import SwiftUI
import VitamuxKit

/// Metric detail (`vitamux://explore/{code}?range=&end=`, artboard MetricDetail): the latest
/// value, range picker with end date, the grammar chart with baseline, source series and coverage
/// toggles, stats, the rule and its "Edit rule" entry, and the values table. A day opens the point
/// sheet; a rollup drills into its week or month.
struct MetricDetailView: View {
    @Environment(AppState.self) private var state
    /// Seeded once from the link; the screen owns its range and end date afterwards.
    @State private var model: MetricDetailModel
    @State private var pins = Pins()
    @State private var selected: SelectedDay?

    init(code: String, range: String?, end: String?) {
        _model = State(initialValue: MetricDetailModel(code: code, range: range, end: end))
    }

    var body: some View {
        List {
            if model.notFound {
                ContentUnavailableView {
                    Label("No metric with the code \(model.code)", systemImage: "safari")
                } description: {
                    Text("It is not in the catalogue. Explore lists everything Vitamux has stored.")
                }
            } else {
                if case .failed(let problem) = model.meta { ProblemView(problem: problem) }
                if let problem = pins.problem { ProblemView(problem: problem) }
                MetricHeader(model: model)
                Section {
                    RangePicker(range: $model.range, end: $model.end, latest: model.latest)
                        .accessibilityIdentifier("rangePicker")
                    SpecialisedNote(view: model.spec?.view)
                    MetricChart(model: model)
                    SeriesToggles(model: model)
                }
                MetricStatsSection(model: model)
                RuleSection(model: model)
                ValuesSection(model: model, selected: $selected)
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle(metricLabel(model.code))
        .toolbar {
            if pins.layout != nil, !model.notFound {
                ToolbarItem(placement: .topBarTrailing) {
                    let pinned = pins.has(model.code)
                    Button(pinned ? "Pinned to dashboard" : "Pin to dashboard", systemImage: pinned ? "star.fill" : "star") {
                        Task { await pins.toggle(model.code, client: state.client) }
                    }
                    .accessibilityIdentifier("pinMetric")
                }
            }
        }
        .task {
            async let pinsLoaded: Void = pins.load(state.client)
            await model.loadMeta(state.client)
            await pinsLoaded
        }
        .task(id: model.key) { await model.load(state.client) }
        .task(id: model.showCoverage) { if model.showCoverage { await model.loadCoverage(state.client) } }
        .sheet(item: $selected) { day in
            PointSheet(model: model, date: day.date)
        }
    }
}

/// The day a values row or chart opened, for the point sheet.
struct SelectedDay: Identifiable, Hashable {
    let date: LocalDate
    var id: String { date.description }
}

/// Tile, code and unit, the latest value with its status and source, and the neutral delta.
private struct MetricHeader: View {
    let model: MetricDetailModel

    var body: some View {
        Section {
            HStack(spacing: 12) {
                MetricTile(code: model.code, section: model.meta.value?.section, size: 44)
                VStack(alignment: .leading, spacing: 2) {
                    Text(subtitle).font(.footnote).foregroundStyle(.secondary)
                    if let latest = model.latestPoint { Text(Format.day(latest.date)).font(.subheadline) }
                }
            }
            if let latest = model.latestPoint {
                VStack(alignment: .leading, spacing: 4) {
                    Text(Format.value(latest.value, unit: model.unit))
                        .font(.system(.largeTitle, design: .rounded, weight: .semibold))
                        .monospacedDigit()
                        .accessibilityIdentifier("latestValue")
                    if let value = model.summary?.value, value.status != .noData {
                        HStack(spacing: 6) {
                            StatusLabel(status: DataStatus(value))
                            let sources = dayProviders(value).map(groupLabel)
                            if !sources.isEmpty { Text("· \(sources.joined(separator: ", "))") }
                        }
                        .font(.subheadline)
                    }
                    if let mean = model.mean(days: 30) {
                        Text("\(Format.signed(latest.value - mean)) vs 30-day mean of \(Format.number(mean))")
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                    }
                }
            } else if model.summary != nil {
                Text("Nothing in the last 30 days").foregroundStyle(.secondary)
            }
        }
    }

    private var subtitle: String {
        var parts = [model.code]
        if let meta = model.meta.value {
            parts.append(meta.aggregation.rawValue.replacingOccurrences(of: "_", with: " "))
        }
        if !model.unit.isEmpty { parts.append(model.unit) }
        return parts.joined(separator: " · ")
    }
}

/// Blood-pressure and sleep codes point at their specialised views (J22.9).
private struct SpecialisedNote: View {
    let view: ChartView?

    var body: some View {
        switch view {
        case .dumbbell:
            NavigationLink("Readings are paired with their other values in the blood pressure view.", value: Route.exploreView(.bloodPressure))
                .font(.footnote)
        case .sleep:
            NavigationLink("Stages and nights side by side are in the sleep view.", value: Route.exploreView(.sleep))
                .font(.footnote)
        default:
            EmptyView()
        }
    }
}

/// The rule in effect and the way to change it: the rule lens over the chart (J22.10).
private struct RuleSection: View {
    let model: MetricDetailModel
    @State private var isEditing = false

    var body: some View {
        Section("How it’s calculated") {
            if let rule = model.rule {
                Text(ruleSummary(rule)).accessibilityIdentifier("ruleSummary")
            }
            Button("Edit rule", systemImage: "slider.horizontal.3") { isEditing = true }
                .accessibilityIdentifier("editRule")
                .sheet(isPresented: $isEditing, onDismiss: model.changed) {
                    RuleLensSheet(metric: model.code, start: model.from ?? model.end.adding(days: -29), end: model.end)
                }
        }
    }
}
