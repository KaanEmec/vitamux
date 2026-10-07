import Charts
import SwiftUI

/// Lines over time (the twin of the web's `TimeSeries`): one or more series, an optional area
/// under the first, a min–max band, a labelled baseline with an optional range, status markers
/// for non-direct points, a ghost (draft) series, dots, and a step variant with readings for
/// "latest" metrics. Gaps stay gaps, shaded as "No data", never zero. Series with a `source`
/// take their source colour and a dash, so colour is never the only cue. Series above about
/// 2,000 points are reduced to min/max per pixel column. The first series drives the selection
/// callout; the others are listed under it. For a Day view, `domain` fixes the x axis, `overlays`
/// shade spans under the marks (night, workouts, now), `window` reads or sets the zoomed window
/// and `onSelect` hands over the first series' point under a tap.
public struct TimeSeries: View {
    public enum Kind: Sendable { case line, step }

    let title: String
    let series: [ChartSeries]
    let unit: String
    let hue: MetricHue
    let kind: Kind
    let area: Bool
    let band: ChartBand?
    let baseline: ChartBaseline?
    let timeZone: TimeZone
    let withTime: Bool
    let zoomable: Bool
    let height: CGFloat
    let domain: ClosedRange<Date>?
    let overlays: [ChartOverlay]
    let window: Binding<ClosedRange<Date>?>?
    let onSelect: ((Int) -> Void)?

    @State private var selection: Date?
    @State private var zoom: CGFloat = 1
    @State private var width: CGFloat = 360
    @State private var memo = Memo<Input, Layout>()
    @Environment(\.displayScale) private var displayScale
    /// At accessibility sizes the baseline is named in the legend only.
    @Environment(\.dynamicTypeSize) private var typeSize

    public init(
        title: String, series: [ChartSeries], unit: String = "", hue: MetricHue = .other, kind: Kind = .line,
        area: Bool = false, band: ChartBand? = nil, baseline: ChartBaseline? = nil, timeZone: TimeZone = .current,
        withTime: Bool = true, zoomable: Bool = true, height: CGFloat = 220, domain: ClosedRange<Date>? = nil,
        overlays: [ChartOverlay] = [], window: Binding<ClosedRange<Date>?>? = nil, onSelect: ((Int) -> Void)? = nil
    ) {
        self.title = title
        self.series = series
        self.unit = unit
        self.hue = hue
        self.kind = kind
        self.area = area
        self.band = band
        self.baseline = baseline
        self.timeZone = timeZone
        self.withTime = withTime
        self.zoomable = zoomable
        self.height = height
        self.domain = domain
        self.overlays = overlays
        self.window = window
        self.onSelect = onSelect
    }

    public var body: some View {
        let layout = memo(Input(
            series: series, band: band, baseline: baseline, kind: kind, area: area, hue: hue, width: width, columns: columns,
            domain: domain, overlays: overlays
        )) { Layout($0) }
        let selected = selection.flatMap { nearest(layout.anchors, to: $0) }
        ChartFrame(title: title, legend: legend(layout), table: table) {
            Chart {
                OverlayMarks(overlays: layout.overlays, hue: hue)
                ForEach(layout.holes) { hole in
                    RectangleMark(xStart: .value("From", hole.from), xEnd: .value("To", hole.to))
                        .foregroundStyle(Color.chartMuted.opacity(0.35))
                        .annotation(position: .overlay, alignment: .topLeading) {
                            if hole.wide { Text("No data").font(.caption2).foregroundStyle(.secondary) }
                        }
                }
                if let range = baseline?.range {
                    RectangleMark(yStart: .value("Low", range.lowerBound), yEnd: .value("High", range.upperBound))
                        .foregroundStyle(hue.color.opacity(0.1))
                }
                if !layout.band.isEmpty {
                    AreaPlot(layout.band, x: .value("Time", \.x), yStart: .value("Low", \.low), yEnd: .value("High", \.high), series: .value("Segment", \.segment))
                        .foregroundStyle(hue.color.opacity(0.14))
                }
                if !layout.area.isEmpty {
                    AreaPlot(layout.area, x: .value("Time", \.x), yStart: .value("Floor", \.low), yEnd: .value("Value", \.high), series: .value("Segment", \.segment))
                        .foregroundStyle(LinearGradient(colors: [layout.lines[0].color.opacity(0.3), layout.lines[0].color.opacity(0)], startPoint: .top, endPoint: .bottom))
                }
                ForEach(layout.lines) { line in
                    if line.drawsLine {
                        LinePlot(line.points, x: .value("Time", \.x), y: .value(line.label, \.y), series: .value("Segment", \.segment))
                            .foregroundStyle(line.color)
                            .lineStyle(line.stroke)
                    }
                    if line.drawsDots {
                        PointPlot(line.points, x: .value("Time", \.x), y: .value(line.label, \.y))
                            .foregroundStyle(line.color)
                            .symbolSize(28)
                    }
                }
                ForEach(layout.markers) { marker in
                    PointMark(x: .value("Time", marker.x), y: .value("Value", marker.y))
                        .symbol { StatusGlyph(status: marker.status).frame(width: 12, height: 12) }
                }
                if let value = baseline?.value {
                    RuleMark(y: .value("Baseline", value))
                        .foregroundStyle(Color.secondary)
                        .lineStyle(StrokeStyle(lineWidth: 1.5, dash: [4, 4]))
                        .annotation(position: .top, alignment: .trailing) {
                            if !typeSize.isAccessibilitySize { Text(baseline?.label ?? "").font(.caption2).foregroundStyle(.secondary) }
                        }
                }
                if let selected {
                    RuleMark(x: .value("Selected", layout.anchors[selected]))
                        .foregroundStyle(Color.secondary.opacity(0.6))
                        .annotation(position: .top, spacing: 0, overflowResolution: .init(x: .fit(to: .chart), y: .fit(to: .chart))) {
                            ChartCallout(tip: tip(selected, layout: layout))
                        }
                }
            }
            .chartXScale(domain: layout.x)
            .chartYScale(domain: layout.y)
            .modifier(ChartTap(action: onSelect.map { select in { date in if let i = nearest(layout.anchors, to: date) { select(i) } } }))
            .chartXSelection(value: $selection)
            .modifier(ChartZoom(domain: layout.x, zoom: $zoom, enabled: zoomable, window: window))
            .modifier(TimeAxis(domain: layout.x, timeZone: timeZone, height: height))
            .onGeometryChange(for: CGFloat.self, of: { $0.size.width }) { width = max($0, 1) }
            .accessibilityLabel(title)
            .accessibilityChartDescriptor(descriptor(layout))
        }
    }

    /// Pixel columns across the whole (zoomed) domain, rounded up to a power of two so small
    /// width changes keep the same decimation.
    private var columns: Int {
        let raw = max(Double(width * displayScale * zoom), 64)
        return 1 << Int(log2(raw).rounded(.up))
    }

    private func legend(_ layout: Layout) -> [LegendItem] {
        var items: [LegendItem] = []
        if series.count > 1 || band != nil || baseline != nil {
            items += layout.lines.map { LegendItem(label: $0.label, mark: $0.drawsLine ? .line($0.color, dash: $0.stroke.dash) : .swatch($0.color)) }
            if let band { items.append(LegendItem(label: band.label, mark: .swatch(hue.color.opacity(0.3)))) }
            if let baseline { items.append(LegendItem(label: baseline.label, mark: baseline.value == nil ? .swatch(hue.color.opacity(0.3)) : .line(.secondary, dash: [4, 4]))) }
        }
        return items + ChartOverlay.legend(layout.overlays, hue: hue) + layout.statuses.map { LegendItem(label: $0.label, mark: .status($0)) }
    }

    private func tip(_ i: Int, layout: Layout) -> ChartTip {
        let first = series[0].points[i]
        let tolerance = layout.x.upperBound.timeIntervalSince(layout.x.lowerBound) / 400
        return ChartTip(
            title: ChartFormat.instant(first.x, in: timeZone, withTime: withTime),
            value: first.y.map { ChartFormat.number($0) } ?? "No data",
            unit: first.y == nil ? nil : unit,
            status: first.status,
            providers: first.providers ?? series[0].source.map { [$0] } ?? [],
            rows: series.dropFirst().map { s in
                let j = nearest(s.points.lazy.map(\.x), to: first.x).flatMap { abs(s.points[$0].x.timeIntervalSince(first.x)) <= tolerance ? $0 : nil }
                return ChartTip.Row(label: s.label, value: ChartFormat.value(j.flatMap { s.points[$0].y }, unit: unit), source: s.source, status: j.flatMap { s.points[$0].status })
            }
        )
    }

    private func table() -> ChartTable {
        var byTime: [Date: [Double?]] = [:]
        for (k, s) in series.enumerated() {
            for p in s.points { byTime[p.x, default: Array(repeating: nil, count: series.count)][k] = p.y }
        }
        let suffix = unit.isEmpty ? "" : " (\(unit))"
        return ChartTable(
            columns: [withTime ? "Time" : "Date"] + series.map { $0.label + suffix },
            rows: byTime.keys.sorted(by: >).map { t in
                ChartTable.Row(id: "\(t.timeIntervalSince1970)", cells: [ChartFormat.instant(t, in: timeZone, withTime: withTime)] + byTime[t]!.map { $0.map { ChartFormat.number($0) } ?? "–" })
            }
        )
    }

    private func descriptor(_ layout: Layout) -> ChartDescriptor {
        ChartDescriptor(
            title: title,
            summary: baseline?.value.map { "\(baseline?.label ?? "Baseline") \(ChartFormat.value($0, unit: unit))" },
            x: .time(layout.x, timeZone, withTime: withTime),
            yTitle: unit.isEmpty ? "Value" : unit,
            yRange: layout.y,
            unit: unit,
            series: layout.lines.map { line in
                ChartDescriptor.Series(name: line.label, continuous: line.drawsLine, points: line.points.map { .init(x: $0.x.timeIntervalSince1970, y: $0.y) })
            }
        )
    }
}

extension TimeSeries {
    struct Input: Equatable {
        var series: [ChartSeries]
        var band: ChartBand?
        var baseline: ChartBaseline?
        var kind: Kind
        var area: Bool
        var hue: MetricHue
        var width: CGFloat
        var columns: Int
        var domain: ClosedRange<Date>?
        var overlays: [ChartOverlay]
    }

    struct Hole: Identifiable {
        var id: Date { from }
        var from: Date
        var to: Date
        var wide: Bool
    }

    struct Line: Identifiable {
        var id: String
        var label: String
        var points: [DrawnPoint]
        var color: Color
        var stroke: StrokeStyle
        var drawsLine: Bool
        var drawsDots: Bool
    }

    struct Marker: Identifiable {
        var id: String
        var x: Date
        var y: Double
        var status: DataStatus
    }

    struct BandPoint {
        var x: Date
        var low: Double
        var high: Double
        var segment: Int
    }

    /// Everything the chart draws, derived once per input.
    struct Layout {
        static let markerLimit = 400

        var x: ClosedRange<Date>
        var y: ClosedRange<Double>
        var anchors: [Date]
        var lines: [Line] = []
        var band: [BandPoint] = []
        var area: [BandPoint] = []
        var markers: [Marker] = []
        var holes: [Hole] = []
        var statuses: [DataStatus] = []
        var overlays: [ChartOverlay] = []

        init(_ input: Input) {
            let series = input.series
            let bandPoints = input.band?.points ?? []
            x = input.domain ?? timeDomain(series.flatMap { [$0.points.first?.x, $0.points.last?.x].compactMap(\.self) } + [bandPoints.first?.x, bandPoints.last?.x].compactMap(\.self))
            overlays = ChartOverlay.clipped(input.overlays, to: x)
            let values = series.lazy.flatMap { $0.points.lazy.map(\.y) }
            let extras: [Double?] = bandPoints.flatMap { [$0.low, $0.high] } + [input.baseline?.value, input.baseline?.range?.lowerBound, input.baseline?.range?.upperBound]
            y = extent(Array(values) + extras, pad: 0.08)
            anchors = series.first?.points.map(\.x) ?? []

            var segmentBase = 0
            for (k, s) in series.enumerated() {
                var points = decimate(s.points, over: x, columns: input.columns)
                if input.kind == .step { points = stepRows(points) }
                for i in points.indices { points[i].segment += segmentBase }
                segmentBase = (points.last?.segment ?? segmentBase) + 1
                let color: Color = s.style == .ghost ? .primary : s.source.map(SourceStyle.color) ?? input.hue.color
                let dashed = k > 0 && s.style != .trend && s.points.count < Int(input.width)
                let dash = s.style == .ghost ? [6, 4] : dashed ? SourceStyle.dashes[k % SourceStyle.dashes.count] : []
                let lineWidth: CGFloat = s.style == .ghost ? 1.75 : k == 0 || s.style == .trend ? 2.25 : 1.5
                lines.append(Line(
                    id: s.id, label: s.label, points: points,
                    color: k > 0 && s.style == .line ? color.opacity(0.8) : color,
                    stroke: StrokeStyle(lineWidth: lineWidth, lineCap: .round, lineJoin: .round, dash: dash),
                    drawsLine: s.style != .dots,
                    drawsDots: s.style == .dots || (input.kind == .step && s.points.count <= Self.markerLimit)
                ))
                guard s.points.count <= Self.markerLimit else { continue }
                for (j, p) in s.points.enumerated() {
                    guard let status = p.status, status != .direct else { continue }
                    markers.append(Marker(id: "\(k)-\(j)", x: p.x, y: status == .noData ? y.lowerBound : p.y ?? y.lowerBound, status: status))
                }
            }
            statuses = DataStatus.allCases.filter { status in markers.contains { $0.status == status } }

            // Band segments count down from -1, so they never join a line's segments.
            var segment = -1
            var open = false
            for p in bandPoints {
                guard let low = p.low, let high = p.high else {
                    if open { segment -= 1; open = false }
                    continue
                }
                band.append(BandPoint(x: p.x, low: low, high: high, segment: segment))
                open = true
            }
            if input.area, let first = lines.first, first.drawsLine {
                area = first.points.map { BandPoint(x: $0.x, low: y.lowerBound, high: $0.y, segment: $0.segment) }
            }
            let span = x.upperBound.timeIntervalSince(x.lowerBound)
            holes = (series.first.map { VitamuxKit.holes(in: $0.points) } ?? []).map {
                Hole(from: $0.lowerBound, to: $0.upperBound, wide: $0.upperBound.timeIntervalSince($0.lowerBound) > span * 0.12)
            }
        }
    }
}

/// Step-after rows: each reading holds until the next one in the same segment.
func stepRows(_ points: [DrawnPoint]) -> [DrawnPoint] {
    var out: [DrawnPoint] = []
    out.reserveCapacity(points.count * 2)
    for (i, p) in points.enumerated() {
        out.append(p)
        if i + 1 < points.count, points[i + 1].segment == p.segment {
            out.append(DrawnPoint(x: points[i + 1].x, y: p.y, segment: p.segment))
        }
    }
    return out
}
