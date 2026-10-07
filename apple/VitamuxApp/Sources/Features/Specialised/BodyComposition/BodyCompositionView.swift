import SwiftUI
import VitamuxKit

/// Body composition (`vitamux://explore/body-composition`): the latest weight and its change in
/// the range, weight per source, fat-free and fat mass stacked, and every weigh-in with all its
/// components, source and provenance. Values are shown as measured: no classes, no thresholds.
struct BodyCompositionView: View {
    @Environment(AppState.self) private var state
    @State private var model = BodyCompositionModel()
    @State private var provenance: ProvenanceRequest?

    var body: some View {
        @Bindable var model = model
        List {
            Section {
                ViewIntro(code: "body_mass", text: "Weight and the parts a scale reports with it, shown as measured. Nothing is graded or flagged.")
                RangePicker(range: $model.span.range, end: $model.span.end, latest: model.span.latest)
                    .buttonStyle(.borderless) // one row, two buttons: each takes only its own taps
                    .accessibilityIdentifier("rangePicker")
            }
            switch model.groups {
            case .loading:
                ProgressView("Loading weigh-ins").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let groups) where groups.isEmpty:
                EmptyRangeView(title: "No weigh-ins in this range", text: "Weigh-ins from a connected scale or a manual entry appear here.")
            case .loaded:
                WeightSection(model: model)
                let stacked = model.stacked
                if !stacked.isEmpty {
                    Section {
                        CompositionBars(entries: stacked).accessibilityIdentifier("compositionChart")
                    } header: {
                        Text("Fat-free and fat mass")
                    } footer: {
                        Text("Per weigh-in that reports both, stacked.")
                    }
                }
                WeighInsSection(model: model, provenance: $provenance)
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Body composition")
        .task(id: model.span.key) { await model.load(state.client) }
        .sheet(item: $provenance) { ProvenanceSheet(request: $0) }
    }
}

private struct WeightSection: View {
    let model: BodyCompositionModel

    var body: some View {
        let series = model.weightSeries
        if !series.isEmpty {
            Section {
                if let latest = model.latestWeight {
                    HStack(alignment: .top) {
                        StatTile(label: "Latest · \(Format.day(latest.date, weekday: false))", value: Format.number(latest.value), unit: model.weightUnit)
                        if let change = latest.change {
                            StatTile(label: "Change in range", value: Format.signed(change), unit: model.weightUnit)
                        }
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("weightStats")
                }
                TimeSeries(title: "Weight per source", series: series, unit: model.weightUnit, hue: .weight, height: 200)
                    .accessibilityIdentifier("weightChart")
            } header: {
                Text("Weight")
            } footer: {
                Text("One line per source, so a scale and an app that copies it stay apart.")
            }
        }
    }
}

/// Every weigh-in newest first, with every component as measured, a page at a time.
private struct WeighInsSection: View {
    let model: BodyCompositionModel
    @Binding var provenance: ProvenanceRequest?
    @State private var shown = ListPage.size

    var body: some View {
        let groups = Array(model.weighIns.reversed())
        let codes = model.codes
        Section("Weigh-ins") {
            ForEach(groups.prefix(shown), id: \.id) { group in
                VStack(alignment: .leading, spacing: 4) {
                    Text(instantText(group.measuredAt, in: TimeZone(offsetMinutes: group.tzOffsetMin)))
                        .accessibilityIdentifier("weighIn-\(group.id)")
                    Text(parts(group, codes: codes)).font(.footnote).monospacedDigit()
                    HStack {
                        SourceChip(provider: group.source.provider, name: recordName(group.source)).font(.footnote)
                        Spacer()
                        ProvenanceButton(entity: .group, id: group.id, request: $provenance)
                    }
                }
            }
            ShowMoreButton(remaining: groups.count - shown) { shown += ListPage.size }
        }
    }

    /// "Weight 79.8 kg · Fat mass 15.1 kg · …" in display order.
    private func parts(_ group: BodyGroup, codes: [String]) -> String {
        codes.compactMap { code in
            group.component(code).map { "\(BodyCompositionModel.name(code)) \(Format.value($0.value, unit: $0.unit))" }
        }
        .joined(separator: " · ")
    }
}
