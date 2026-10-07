import Accessibility
import SwiftUI

/// The audio-graph and VoiceOver description of a chart: every view builds one from the data it
/// draws (decimated series stay decimated, so Audio Graphs stay quick on a 14,400-point day).
struct ChartDescriptor: AXChartDescriptorRepresentable {
    enum XAxis {
        case time(ClosedRange<Date>, TimeZone, withTime: Bool)
        case category(title: String, [String])
    }

    struct Point {
        var x: Double = 0
        var category: String?
        var y: Double?
        var label: String?
    }

    struct Series {
        var name: String
        var continuous: Bool
        var points: [Point]
    }

    var title: String
    var summary: String?
    var x: XAxis
    var yTitle: String
    var yRange: ClosedRange<Double>
    var unit: String
    var series: [Series]

    func makeChartDescriptor() -> AXChartDescriptor {
        let xAxis: any AXDataAxisDescriptor = switch x {
        case let .time(range, timeZone, withTime):
            AXNumericDataAxisDescriptor(
                title: withTime ? "Time" : "Date",
                range: range.lowerBound.timeIntervalSince1970 ... range.upperBound.timeIntervalSince1970,
                gridlinePositions: []
            ) { ChartFormat.instant(Date(timeIntervalSince1970: $0), in: timeZone, withTime: withTime) }
        case let .category(title, order):
            AXCategoricalDataAxisDescriptor(title: title, categoryOrder: order)
        }
        let unit = unit
        let yAxis = AXNumericDataAxisDescriptor(title: yTitle, range: yRange, gridlinePositions: []) { ChartFormat.value($0, unit: unit) }
        return AXChartDescriptor(
            title: title,
            summary: summary,
            xAxis: xAxis,
            yAxis: yAxis,
            additionalAxes: [],
            series: series.map { s in
                AXDataSeriesDescriptor(name: s.name, isContinuous: s.continuous, dataPoints: s.points.map { p in
                    if let category = p.category { AXDataPoint(x: category, y: p.y, additionalValues: [], label: p.label) }
                    else { AXDataPoint(x: p.x, y: p.y, additionalValues: [], label: p.label) }
                })
            }
        )
    }

    func updateChartDescriptor(_ descriptor: AXChartDescriptor) {}
}
