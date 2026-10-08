import SwiftUI
import VitamuxKit

/// The all-sources day (`vitamux://explore/{code}/day/{date}`): the resolved value and why, every
/// source's readings over the day, which sources the rule used, excluded or ignored, the rule's
/// inputs with their records, and the day's overrides with Revoke.
struct AllSourcesDayView: View {
    @Environment(AppState.self) private var state
    /// Seeded once from the link.
    @State private var model: AllSourcesDayModel
    @State private var override: OverrideRequest?
    @State private var provenance: ProvenanceRequest?

    init(code: String, date: LocalDate) {
        _model = State(initialValue: AllSourcesDayModel(code: code, date: date))
    }

    var body: some View {
        List {
            switch model.day {
            case .loading:
                ProgressView().accessibilityLabel("Loading").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let day):
                DayResult(model: model, result: day.result, override: $override)
                DayChart(model: model)
                DaySources(model: model, provenance: $provenance)
                InputsSection(code: model.code, value: day.result, override: $override, provenance: $provenance)
                DayOverrides(model: model)
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("\(metricLabel(model.code)), \(model.date)")
        .navigationBarTitleDisplayMode(.inline)
        .task(id: model.version) { await model.load(state.client) }
        .sheet(item: $override) { request in
            OverrideSheet(request: request, metric: model.code, date: model.date, value: model.day.value?.result) { model.changed() }
        }
        .sheet(item: $provenance) { request in
            ProvenanceSheet(request: request)
        }
    }
}

/// Init from a link's date; a malformed date shows the problem instead.
struct AllSourcesDayRoute: View {
    let code: String
    let date: String

    var body: some View {
        if let day = LocalDate(date) {
            AllSourcesDayView(code: code, date: day)
        } else {
            ProblemView(problem: Problem(title: "Not a date", detail: "\(date) is not a date (YYYY-MM-DD)."))
                .navigationTitle(metricLabel(code))
        }
    }
}

private struct DayResult: View {
    let model: AllSourcesDayModel
    let result: ResolvedValue?
    @Binding var override: OverrideRequest?

    var body: some View {
        ResolvedSection(code: model.code, value: result)
        Section {
            if let result {
                Text(meta(result)).font(.footnote).foregroundStyle(.secondary)
            }
            OverrideButtons(value: result, override: $override)
        }
    }

    private func meta(_ result: ResolvedValue) -> String {
        var parts: [String] = []
        if let rule = result.rule { parts.append("Rule \(rule.ref) v\(rule.version)\(rule.strategy.map { ", \($0)" } ?? "").") }
        if let computed = result.computedAt { parts.append("Computed \(Format.instant(computed)).") }
        if let zone = model.day.value?.timezone, !zone.isEmpty { parts.append("Timezone \(zone).") }
        return parts.joined(separator: " ")
    }
}

/// Every source's readings over the day, overlaid.
private struct DayChart: View {
    let model: AllSourcesDayModel

    var body: some View {
        Section("Every source over the day") {
            switch model.chart {
            case .loading:
                ProgressView("Loading readings").frame(maxWidth: .infinity, minHeight: 160)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let series) where series.isEmpty:
                Text("No measurements from any source on this day.").foregroundStyle(.secondary)
            case .loaded(let series):
                TimeSeries(
                    title: "\(metricLabel(model.code)) on \(model.date): \(series.map(\.label).joined(separator: ", "))",
                    series: series, unit: model.unit, hue: MetricHue.of(code: model.code), timeZone: model.timeZone
                )
                .accessibilityIdentifier("dayChart")
            }
        }
    }
}

/// Every source seen for the window, including excluded ones and those outside the rule.
private struct DaySources: View {
    let model: AllSourcesDayModel
    @Binding var provenance: ProvenanceRequest?

    var body: some View {
        Section {
            switch model.sources {
            case .loading:
                ProgressView().accessibilityLabel("Loading").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let sources):
                ForEach(sources.indices, id: \.self) { i in
                    SourceRow(
                        source: sources[i], label: model.label(sources[i]),
                        record: i < model.firstRecords.count ? model.firstRecords[i] : nil, provenance: $provenance
                    )
                }
            }
        } header: {
            Text("Sources")
        } footer: {
            Text("Every source seen for this window, including excluded ones and those outside the rule.")
        }
    }
}

private struct SourceRow: View {
    let source: Components.Schemas.DrilldownSource
    let label: String
    let record: String?
    @Binding var provenance: ProvenanceRequest?

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                SourceDot(provider: source.provider)
                Text(label).font(.body.weight(.semibold))
                Spacer()
                Label(status.label, systemImage: status.symbol)
                    .font(.footnote)
                    .foregroundStyle(status.color)
                    .accessibilityIdentifier("sourceStatus-\(source.provider)-\(source.ruleStatus.rawValue)")
            }
            if let reason = source.reason { Text(reason).font(.footnote).foregroundStyle(.secondary) }
            let values = (source.values?.additionalProperties ?? [:]).sorted { $0.key < $1.key }
            if !values.isEmpty {
                Text(values.map { "\($0.key.replacingOccurrences(of: "_", with: " ")) \(Format.number($0.value))" }.joined(separator: " · "))
                    .font(.footnote)
                    .monospacedDigit()
            }
            if let p = source.provenance {
                Text([p.normalizer, p.fetchedAt.map { "fetched \(Format.instant($0))" }].compactMap(\.self).joined(separator: " · "))
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if let record {
                Button("Trace a record") { provenance = ProvenanceRequest(recordID: record) }
                    .buttonStyle(.borderless)
                    .font(.footnote)
                    .accessibilityIdentifier("trace-\(source.provider)-\(source.ruleStatus.rawValue)")
            }
        }
        .padding(.vertical, 2)
    }

    /// Used, excluded or not in rule: a shape, a colour and a word.
    private var status: (label: String, symbol: String, color: Color) {
        switch source.ruleStatus {
        case .used: ("Used", "checkmark.circle.fill", .feedbackOK)
        case .excluded: ("Excluded", "xmark.circle.fill", .feedbackError)
        case .notInRule: ("Not in rule", "minus.circle", .secondary)
        }
    }
}

/// The day's overrides, active first; an active one can be revoked.
private struct DayOverrides: View {
    @Environment(AppState.self) private var state
    let model: AllSourcesDayModel

    var body: some View {
        Section("Overrides for this day") {
            if let problem = model.actionProblem { ProblemView(problem: problem) }
            switch model.overrides {
            case .loading:
                ProgressView().accessibilityLabel("Loading").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded:
                let todays = model.todays
                if todays.isEmpty {
                    Text("No overrides on this day.").foregroundStyle(.secondary).accessibilityIdentifier("noOverrides")
                }
                ForEach(todays, id: \.id) { override in
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(override.summary).font(.body.weight(.medium))
                            if let note = override.note { Text(note).font(.footnote).foregroundStyle(.secondary) }
                            Text(override.active ? "Active" : "Revoked \(override.revokedAt.map(Format.instant) ?? "")")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .accessibilityIdentifier("overrideState-\(override.id)")
                        }
                        Spacer()
                        if override.active {
                            Button("Revoke") { Task { await model.revoke(override.id, client: state.client) } }
                                .buttonStyle(.bordered)
                                .accessibilityLabel("Revoke \(override.summary)")
                                .accessibilityIdentifier("revoke-\(override.id)")
                        }
                    }
                }
            }
        }
    }
}
