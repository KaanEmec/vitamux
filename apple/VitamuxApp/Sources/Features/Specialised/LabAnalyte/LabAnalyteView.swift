import SwiftUI
import VitamuxKit

/// One analyte over time (`vitamux://lab/analytes/{code}`, also `label:<printed label>`): the
/// latest result and the count, the results in one printed unit as points with the printed
/// reference range as a band, and every result as printed with its laboratory and document.
struct LabAnalyteView: View {
    @Environment(AppState.self) private var state
    let code: String
    @State private var model: LabAnalyteModel

    init(code: String) {
        self.code = code
        _model = State(initialValue: LabAnalyteModel(code: code))
    }

    var body: some View {
        List {
            switch model.all {
            case .loading:
                ProgressView("Loading results").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded:
                if let latest = model.latest {
                    AnalyteHeader(model: model, latest: latest)
                    AnalyteChart(model: model)
                    ResultsSection(results: model.results)
                } else {
                    EmptyRangeView(title: "No confirmed results for this analyte", text: "Confirm a lab report that includes it, and its results appear here.")
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Analyte history")
        .task(id: code) {
            // A link to another analyte can land on this screen: start over for its code.
            if model.code != code { model = LabAnalyteModel(code: code) }
            await model.load(state.client)
        }
    }
}

private struct AnalyteHeader: View {
    let model: LabAnalyteModel
    let latest: LabResult

    var body: some View {
        Section {
            HStack(spacing: 12) {
                MetricTile(code: "lab_\(latest.analyte ?? "label")", size: 44)
                VStack(alignment: .leading, spacing: 2) {
                    Text(latest.originalLabel).font(.title3.weight(.semibold))
                    Text(latest.analyte ?? "Analyte not in the catalogue, by printed label").font(.footnote.monospaced()).foregroundStyle(.secondary)
                }
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("analyteHeader")
            HStack(alignment: .top) {
                StatTile(label: "Latest result · \(Format.day(latest.collectedDate, weekday: false))", value: latest.printedValue, unit: latest.unitText)
                    .accessibilityIdentifier("latestResult")
                StatTile(label: "Results", value: model.results.count.formatted())
            }
        }
    }
}

private struct AnalyteChart: View {
    @Bindable var model: LabAnalyteModel

    var body: some View {
        let units = model.units
        Section {
            if units.count > 1 {
                Picker("Printed unit", selection: Binding { model.shownUnit } set: { model.unit = $0 }) {
                    ForEach(units, id: \.self) { Text(unitLabel($0)).tag($0) }
                }
                .pickerStyle(.segmented)
                .accessibilityIdentifier("unitPicker")
            }
            if model.plotted.isEmpty {
                Text("No numeric results in this unit.").foregroundStyle(.secondary)
            } else {
                TimeSeries(
                    title: "\(model.latest?.originalLabel ?? model.code) results over time", series: model.series, unit: model.shownUnit,
                    hue: .lab, band: model.band, timeZone: .gmt, withTime: false
                )
                .accessibilityIdentifier("analyteChart")
            }
        } header: {
            Text("Over time")
        } footer: {
            Text(note(units: units))
        }
    }

    private func note(units: [String]) -> String {
        var parts = ["Plotted in \(unitLabel(model.shownUnit)), as printed."]
        if units.count > 1 { parts.append("Results printed in another unit are in the list.") }
        if model.band != nil { parts.append("The band is the reference range printed on each report.") }
        return parts.joined(separator: " ")
    }
}

/// Every result as printed, newest first, a page at a time.
private struct ResultsSection: View {
    let results: [LabResult]
    @State private var shown = ListPage.size

    var body: some View {
        Section("Results as printed") {
            ForEach(results.prefix(shown), id: \.id) { result in
                ResultRow(result: result)
            }
            ShowMoreButton(remaining: results.count - shown) { shown += ListPage.size }
        }
    }
}

/// A result as printed; it opens its document.
private struct ResultRow: View {
    let result: LabResult

    var body: some View {
        NavigationLink(value: Route.labDocument(id: result.provenance.documentId)) {
            VStack(alignment: .leading, spacing: 4) {
                HStack(alignment: .firstTextBaseline) {
                    Text(Format.day(result.collectedDate, weekday: false))
                    Spacer()
                    Text(result.printedValue).font(.body.weight(.semibold)).monospacedDigit()
                    Text(unitLabel(result.unitText ?? "")).font(.caption).foregroundStyle(.secondary)
                }
                Grid(alignment: .leading, horizontalSpacing: 8, verticalSpacing: 2) {
                    GridRow { Text("Label as printed").foregroundStyle(.secondary); Text(result.originalLabel) }
                    GridRow { Text("Range as printed").foregroundStyle(.secondary); Text(result.referenceRangeText ?? "–") }
                    GridRow { Text("Flag as printed").foregroundStyle(.secondary); Text(result.printedFlag ?? "–") }
                    GridRow { Text("Laboratory").foregroundStyle(.secondary); Text(result.provenance.laboratory ?? "–") }
                }
                .font(.footnote)
            }
        }
        .accessibilityHint("Opens the document")
        .accessibilityIdentifier("result-\(result.id)")
    }
}
