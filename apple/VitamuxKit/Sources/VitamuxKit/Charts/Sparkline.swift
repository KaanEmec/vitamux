import Charts
import SwiftUI

/// A small trend for cards (the twin of the web's `Sparkline`): a line (or bars) with an optional
/// range band and mean line, and an optional dashed `ghost` over the same windows (a draft beside
/// the rule in effect). No axes or interaction; with a `label` it is one accessible image with a
/// chart descriptor (its card links to the full chart and its table), otherwise it is decorative.
public struct Sparkline: View {
    let values: [Double?]
    let bars: Bool
    let band: ClosedRange<Double>?
    let mean: Double?
    let ghost: [Double?]?
    let label: String
    let hue: MetricHue

    public init(values: [Double?], bars: Bool = false, band: ClosedRange<Double>? = nil, mean: Double? = nil, ghost: [Double?]? = nil, label: String = "", hue: MetricHue = .other) {
        self.values = values
        self.bars = bars
        self.band = band
        self.mean = mean
        self.ghost = ghost
        self.label = label
        self.hue = hue
    }

    private struct Point: Identifiable {
        var id: Int { index }
        var index: Int
        var y: Double
        var segment: Int
    }

    /// Segments of different lines must not share a value: Swift Charts joins equal series.
    private static func points(_ values: [Double?], firstSegment: Int = 0) -> [Point] {
        var out: [Point] = []
        var segment = firstSegment
        for (i, v) in values.enumerated() {
            if let v { out.append(Point(index: i, y: v, segment: segment)) } else { segment += 1 }
        }
        return out
    }

    public var body: some View {
        let line = Self.points(values)
        let draft = ghost.map { Self.points($0, firstSegment: values.count + 1) } ?? []
        let y = extent(values + (ghost ?? []) + [band?.lowerBound, band?.upperBound, mean] + (bars ? [0] : []), pad: bars ? 0 : 0.1)
        Chart {
            if let band {
                RectangleMark(yStart: .value("Low", band.lowerBound), yEnd: .value("High", band.upperBound)).foregroundStyle(hue.color.opacity(0.12))
            }
            if bars {
                BarPlot(line, x: .value("Window", \.index), y: .value("Value", \.y)).foregroundStyle(hue.color)
            } else {
                LinePlot(line, x: .value("Window", \.index), y: .value("Value", \.y), series: .value("Segment", \.segment))
                    .foregroundStyle(hue.color)
                    .lineStyle(StrokeStyle(lineWidth: 1.75, lineCap: .round, lineJoin: .round))
            }
            if !draft.isEmpty {
                LinePlot(draft, x: .value("Window", \.index), y: .value("Draft", \.y), series: .value("Draft segment", \.segment))
                    .foregroundStyle(Color.primary)
                    .lineStyle(StrokeStyle(lineWidth: 1.5, dash: [4, 3]))
            }
            if let mean {
                RuleMark(y: .value("Mean", mean)).foregroundStyle(Color.secondary).lineStyle(StrokeStyle(lineWidth: 1, dash: [3, 3]))
            }
        }
        .chartXScale(domain: -0.5 ... Double(max(values.count, 1)) - 0.5)
        .chartYScale(domain: y)
        .chartXAxis(.hidden)
        .chartYAxis(.hidden)
        .chartLegend(.hidden)
        .frame(height: 48)
        .accessibilityElement(children: .ignore)
        .accessibilityHidden(label.isEmpty)
        .accessibilityLabel(label)
        .accessibilityChartDescriptor(descriptor(y))
    }

    private func descriptor(_ y: ClosedRange<Double>) -> ChartDescriptor {
        let windows = values.indices.map { "\($0 + 1)" }
        var series = [ChartDescriptor.Series(name: label.isEmpty ? "Values" : label, continuous: !bars, points: values.enumerated().map { .init(category: windows[$0], y: $1) })]
        if let ghost { series.append(.init(name: "Draft", continuous: true, points: ghost.enumerated().map { .init(category: windows[$0], y: $1) })) }
        return ChartDescriptor(title: label, summary: mean.map { "Mean \(ChartFormat.number($0))" }, x: .category(title: "Window", windows), yTitle: "Value", yRange: y, unit: "", series: series)
    }
}
