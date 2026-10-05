import Foundation
import Observation
import VitamuxKit

typealias LabResult = Components.Schemas.LabResult

/// One analyte over time (the panel's `/lab/analytes/{code}`): its confirmed results as printed
/// (`GET /lab-results`, every page), plotted in one printed unit with the reference range printed
/// on each report as a band. The code is an analyte code, or `label:<printed label>` for results
/// whose analyte is unknown. Values, units, ranges and flags are shown as the lab printed them;
/// nothing is flagged or rated here.
@Observable
final class LabAnalyteModel {
    let code: String
    /// The printed unit picked; nil follows the latest result's.
    var unit: String?
    private(set) var all: Loadable<[LabResult]> = .loading

    init(code: String) {
        self.code = code
    }

    func load(_ client: Client?) async {
        guard let client else { return }
        all = await Loadable {
            try await readAll { cursor in
                let page = try await client.listLabResults(query: .init(limit: 500, cursor: cursor)).ok.body.json
                return (page.value2.labResults, nextCursor(page.value1))
            }
        }
    }

    /// The analyte's results, newest first.
    var results: [LabResult] {
        let label = code.hasPrefix("label:") ? String(code.dropFirst(6)) : nil
        return (all.value ?? [])
            .filter { result in label.map { result.analyte == nil && result.originalLabel == $0 } ?? (result.analyte == code) }
            .sorted { $0.collectedDate > $1.collectedDate }
    }

    var latest: LabResult? { results.first }

    /// Printed units, the latest's first; "" stands for unitless.
    var units: [String] {
        var seen = Set<String>()
        return results.map { $0.unitText ?? "" }.filter { seen.insert($0).inserted }
    }

    var shownUnit: String {
        unit.flatMap { units.contains($0) ? $0 : nil } ?? latest?.unitText ?? ""
    }

    /// Numeric results printed in the shown unit, oldest first, at midnight UTC of the collection date.
    var plotted: [LabResult] {
        results.filter { $0.valueNumeric != nil && ($0.unitText ?? "") == shownUnit }.reversed()
    }

    var series: [ChartSeries] {
        let label = latest?.originalLabel ?? code
        return [ChartSeries(label: label, points: plotted.compactMap { r in dayX(r.collectedDate).map { ChartPoint(x: $0, y: r.valueNumeric) } }, style: .dots)]
    }

    /// The printed range of each plotted report, when any report printed both bounds.
    var band: ChartBand? {
        guard plotted.contains(where: { $0.refLow != nil && $0.refHigh != nil }) else { return nil }
        return ChartBand(label: "Reference range as printed", points: plotted.compactMap { r in
            dayX(r.collectedDate).map { ChartBand.Point(x: $0, low: r.refLow, high: r.refHigh) }
        })
    }
}

/// "Unitless" for a result confirmed without a unit.
func unitLabel(_ unit: String) -> String {
    unit.isEmpty ? "unitless" : unit
}

extension LabResult {
    /// The value as printed, with its comparator when the text lacks it: "< 0.5".
    var printedValue: String {
        guard let comparator, !valueText.trimmingCharacters(in: .whitespaces).hasPrefix(comparator) else { return valueText }
        return "\(comparator) \(valueText)"
    }
}
