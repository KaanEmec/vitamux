import Charts
import SwiftUI

/// A low–high pair per reading, e.g. diastolic and systolic blood pressure: a filled dot (high),
/// a ring (low) and a stem between them, in the metric hue (the twin of the web's
/// `RangeDumbbell`). Position and shape both tell the two apart. The callout leads with "high/low".
public struct RangeDumbbell: View {
    public struct Reading: Hashable, Sendable, Identifiable {
        public var id: Date { x }
        public var x: Date
        public var low: Double?
        public var high: Double?
        /// Extra callout rows of the reading (pulse, posture, device).
        public var details: [ChartTip.Row]
        public init(x: Date, low: Double?, high: Double?, details: [ChartTip.Row] = []) {
            self.x = x
            self.low = low
            self.high = high
            self.details = details
        }
    }

    let title: String
    let readings: [Reading]
    let lowLabel: String
    let highLabel: String
    let unit: String
    let hue: MetricHue
    let timeZone: TimeZone
    let height: CGFloat

    @State private var selection: Date?

    public init(
        title: String, readings: [Reading], lowLabel: String = "Low", highLabel: String = "High", unit: String = "",
        hue: MetricHue = .bloodPressure, timeZone: TimeZone = .current, height: CGFloat = 220
    ) {
        self.title = title
        self.readings = readings
        self.lowLabel = lowLabel
        self.highLabel = highLabel
        self.unit = unit
        self.hue = hue
        self.timeZone = timeZone
        self.height = height
    }

    public var body: some View {
        let selected = selection.flatMap { nearest(readings.lazy.map(\.x), to: $0) }
        let y = extent(readings.flatMap { [$0.low, $0.high] }, pad: 0.1)
        ChartFrame(title: title, legend: legend, table: table) {
            Chart {
                ForEach(readings) { r in
                    if let low = r.low, let high = r.high {
                        RuleMark(x: .value("Time", r.x), yStart: .value(lowLabel, low), yEnd: .value(highLabel, high))
                            .foregroundStyle(hue.color.opacity(selected.map { readings[$0].x == r.x } == true ? 1 : 0.45))
                            .lineStyle(StrokeStyle(lineWidth: 3, lineCap: .round))
                    }
                    if let high = r.high {
                        PointMark(x: .value("Time", r.x), y: .value(highLabel, high))
                            .symbol { Circle().fill(hue.color).frame(width: 8, height: 8) }
                    }
                    if let low = r.low {
                        PointMark(x: .value("Time", r.x), y: .value(lowLabel, low))
                            .symbol { Circle().inset(by: 1).fill(.background).stroke(hue.color, lineWidth: 2).frame(width: 9, height: 9) }
                    }
                }
                if let i = selected {
                    RuleMark(x: .value("Selected", readings[i].x))
                        .foregroundStyle(.clear)
                        .annotation(position: .top, spacing: 0, overflowResolution: .init(x: .fit(to: .chart), y: .fit(to: .chart))) {
                            ChartCallout(tip: tip(i))
                        }
                }
            }
            .chartXScale(domain: domain)
            .chartYScale(domain: y)
            .chartXSelection(value: $selection)
            .modifier(TimeAxis(domain: domain, timeZone: timeZone, height: height))
            .accessibilityLabel(title)
            .accessibilityChartDescriptor(descriptor(y))
        }
    }

    private var domain: ClosedRange<Date> {
        let d = timeDomain(readings.map(\.x))
        let pad = d.upperBound.timeIntervalSince(d.lowerBound) * 0.02
        return d.lowerBound.addingTimeInterval(-pad) ... d.upperBound.addingTimeInterval(pad)
    }

    private var legend: [LegendItem] {
        [LegendItem(label: highLabel, mark: .swatch(hue.color)), LegendItem(label: lowLabel, mark: .ring(hue.color))]
    }

    private func format(_ v: Double?) -> String { ChartFormat.value(v, unit: unit) }

    private func tip(_ i: Int) -> ChartTip {
        let r = readings[i]
        let none = r.low == nil && r.high == nil
        return ChartTip(
            title: ChartFormat.instant(r.x, in: timeZone),
            value: none ? "No data" : "\(r.high.map { ChartFormat.number($0) } ?? "–")/\(r.low.map { ChartFormat.number($0) } ?? "–")",
            unit: none ? nil : unit,
            rows: [.init(label: highLabel, value: format(r.high)), .init(label: lowLabel, value: format(r.low))] + r.details
        )
    }

    private func table() -> ChartTable {
        ChartTable(columns: ["Time", highLabel, lowLabel], rows: readings.reversed().map { r in
            .init(id: "\(r.x.timeIntervalSince1970)", cells: [ChartFormat.instant(r.x, in: timeZone), format(r.high), format(r.low)])
        })
    }

    private func descriptor(_ y: ClosedRange<Double>) -> ChartDescriptor {
        ChartDescriptor(
            title: title, summary: nil, x: .time(domain, timeZone, withTime: true), yTitle: unit.isEmpty ? "Value" : unit, yRange: y, unit: unit,
            series: [
                .init(name: highLabel, continuous: false, points: readings.map { .init(x: $0.x.timeIntervalSince1970, y: $0.high) }),
                .init(name: lowLabel, continuous: false, points: readings.map { .init(x: $0.x.timeIntervalSince1970, y: $0.low) }),
            ]
        )
    }
}
