import Charts
import SwiftUI

/// One stack of `Bars`: a value per window (nil: no bar, never a zero bar).
public struct BarStack: Hashable, Sendable, Identifiable {
    public var id: String { label }
    public var label: String
    public var values: [Double?]
    /// A stage stack takes the stage colour; otherwise the metric hue.
    public var stage: SleepStageKind?

    public init(label: String, values: [Double?], stage: SleepStageKind? = nil) {
        self.label = label
        self.values = values
        self.stage = stage
    }
}

/// Bars per window (additive metrics: steps, energy) in the metric hue, or bars stacked by stage
/// (sleep stages per night, deep at the bottom): the twin of the web's `Bars`. `xs` are window
/// starts; a missing window has no bar. An optional labelled baseline is drawn over them, and
/// non-direct windows carry their status glyph above the bar. Windows shorter than a calendar
/// unit (a Day view's 30-minute buckets) take `binWidth` seconds; `domain`, `overlays`, `window`
/// and `onSelect` work as on `TimeSeries`.
public struct Bars: View {
    let title: String
    let xs: [Date]
    let stacks: [BarStack]
    let unit: String
    let hue: MetricHue
    let bin: Calendar.Component
    let baseline: ChartBaseline?
    let status: [DataStatus?]?
    let providers: [[String]?]?
    let timeZone: TimeZone
    let zoomable: Bool
    let height: CGFloat
    let binWidth: TimeInterval?
    let fixedDomain: ClosedRange<Date>?
    let overlays: [ChartOverlay]
    let window: Binding<ClosedRange<Date>?>?
    let onSelect: ((Int) -> Void)?

    @State private var selection: Date?
    @State private var zoom: CGFloat = 1
    /// At accessibility sizes the baseline is named in the legend only.
    @Environment(\.dynamicTypeSize) private var typeSize

    /// Plain bars, one value per window.
    public init(
        title: String, xs: [Date], values: [Double?], unit: String = "", hue: MetricHue = .other, bin: Calendar.Component = .day,
        baseline: ChartBaseline? = nil, status: [DataStatus?]? = nil, providers: [[String]?]? = nil,
        timeZone: TimeZone = .current, zoomable: Bool = true, height: CGFloat = 220, binWidth: TimeInterval? = nil,
        domain: ClosedRange<Date>? = nil, overlays: [ChartOverlay] = [], window: Binding<ClosedRange<Date>?>? = nil,
        onSelect: ((Int) -> Void)? = nil
    ) {
        self.init(title: title, xs: xs, stacks: [BarStack(label: title, values: values)], unit: unit, hue: hue, bin: bin,
                  baseline: baseline, status: status, providers: providers, timeZone: timeZone, zoomable: zoomable, height: height,
                  binWidth: binWidth, domain: domain, overlays: overlays, window: window, onSelect: onSelect)
    }

    /// Stacked bars, bottom stack first (deep, light, REM, awake for sleep).
    public init(
        title: String, xs: [Date], stacks: [BarStack], unit: String = "", hue: MetricHue = .other, bin: Calendar.Component = .day,
        baseline: ChartBaseline? = nil, status: [DataStatus?]? = nil, providers: [[String]?]? = nil,
        timeZone: TimeZone = .current, zoomable: Bool = true, height: CGFloat = 220, binWidth: TimeInterval? = nil,
        domain: ClosedRange<Date>? = nil, overlays: [ChartOverlay] = [], window: Binding<ClosedRange<Date>?>? = nil,
        onSelect: ((Int) -> Void)? = nil
    ) {
        self.title = title
        self.xs = xs
        self.stacks = stacks
        self.unit = unit
        self.hue = hue
        self.bin = bin
        self.baseline = baseline
        self.status = status
        self.providers = providers
        self.timeZone = timeZone
        self.zoomable = zoomable
        self.height = height
        self.binWidth = binWidth
        fixedDomain = domain
        self.overlays = overlays
        self.window = window
        self.onSelect = onSelect
    }

    private var stacked: Bool { stacks.count > 1 }
    private var withTime: Bool { binWidth != nil || bin == .hour || bin == .minute }

    public var body: some View {
        let totals = totals
        let domain = domain
        let step = step
        let selected = selection.flatMap { nearest(xs, to: $0.addingTimeInterval(-step / 2)) }
        let overlays = ChartOverlay.clipped(overlays, to: domain)
        ChartFrame(title: title, legend: legend + ChartOverlay.legend(overlays, hue: hue), table: { table(totals) }) {
            Chart {
                OverlayMarks(overlays: overlays, hue: hue)
                ForEach(stacks) { stack in
                    ForEach(cells(stack)) { cell in
                        bar(cell, label: stack.label)
                            .foregroundStyle(stack.stage?.color ?? hue.color)
                            .opacity(selected == nil || selected == cell.index ? 1 : 0.55)
                            .cornerRadius(stacked ? 1 : binWidth == nil ? 3 : 1.5)
                    }
                }
                ForEach(markers(totals)) { marker in
                    if let binWidth {
                        PointMark(x: .value("Window", marker.x.addingTimeInterval(binWidth / 2)), y: .value("Total", marker.y))
                            .symbol { StatusGlyph(status: marker.status).frame(width: 10, height: 10) }
                            .offset(y: -8)
                    } else {
                        PointMark(x: .value("Window", marker.x, unit: bin), y: .value("Total", marker.y))
                            .symbol { StatusGlyph(status: marker.status).frame(width: 10, height: 10) }
                            .offset(y: -8)
                    }
                }
                if let value = baseline?.value {
                    RuleMark(y: .value("Baseline", value))
                        .foregroundStyle(Color.secondary)
                        .lineStyle(StrokeStyle(lineWidth: 1.5, dash: [4, 4]))
                        .annotation(position: .top, alignment: .trailing) {
                            if !typeSize.isAccessibilitySize { Text(baseline?.label ?? "").font(.caption2).foregroundStyle(.secondary) }
                        }
                }
                if let selected, let binWidth {
                    RuleMark(x: .value("Selected", xs[selected].addingTimeInterval(binWidth / 2)))
                        .foregroundStyle(.clear)
                        .annotation(position: .top, spacing: 0, overflowResolution: .init(x: .fit(to: .chart), y: .fit(to: .chart))) {
                            ChartCallout(tip: tip(selected, totals: totals))
                        }
                } else if let selected {
                    RuleMark(x: .value("Selected", xs[selected], unit: bin))
                        .foregroundStyle(.clear)
                        .annotation(position: .top, spacing: 0, overflowResolution: .init(x: .fit(to: .chart), y: .fit(to: .chart))) {
                            ChartCallout(tip: tip(selected, totals: totals))
                        }
                }
            }
            .chartXScale(domain: domain)
            .chartYScale(domain: 0 ... max(extent(totals + [baseline?.value]).upperBound * 1.08, 1))
            .modifier(ChartTap(action: onSelect.map { select in { date in if let i = nearest(xs, to: date.addingTimeInterval(-step / 2)) { select(i) } } }))
            .chartXSelection(value: $selection)
            .modifier(ChartZoom(domain: domain, zoom: $zoom, enabled: zoomable, window: window))
            .modifier(TimeAxis(domain: domain, timeZone: timeZone, height: height))
            .accessibilityLabel(title)
            .accessibilityChartDescriptor(descriptor(totals))
        }
    }

    private struct Cell: Identifiable {
        var id: Int { index }
        var index: Int
        var x: Date
        var value: Double
    }

    private struct Marker: Identifiable {
        var id: Int
        var x: Date
        var y: Double
        var status: DataStatus
    }

    /// The window length: `binWidth`, else the closest pair of windows (a day when there is one).
    private var step: TimeInterval {
        binWidth ?? zip(xs.dropFirst(), xs).map { $0.timeIntervalSince($1) }.min() ?? 86_400
    }

    /// One bar: a calendar unit wide, or `binWidth` seconds with a hairline gap.
    private func bar(_ cell: Cell, label: String) -> BarMark {
        guard let binWidth else { return BarMark(x: .value("Window", cell.x, unit: bin), y: .value(label, cell.value)) }
        let inset = min(binWidth * 0.08, 60)
        return BarMark(xStart: .value("From", cell.x.addingTimeInterval(inset)), xEnd: .value("To", cell.x.addingTimeInterval(binWidth - inset)), y: .value(label, cell.value))
    }

    private var domain: ClosedRange<Date> {
        if let fixedDomain { return fixedDomain }
        guard let first = xs.first, let last = xs.last else { return timeDomain([]) }
        return first ... last.addingTimeInterval(step)
    }

    private var totals: [Double?] {
        xs.indices.map { i in
            stacks.contains { i < $0.values.count && $0.values[i] != nil } ? stacks.reduce(0) { $0 + (i < $1.values.count ? $1.values[i] ?? 0 : 0) } : nil
        }
    }

    private func cells(_ stack: BarStack) -> [Cell] {
        zip(xs, stack.values).enumerated().compactMap { i, pair in pair.1.flatMap { $0 > 0 ? Cell(index: i, x: pair.0, value: $0) : nil } }
    }

    private func markers(_ totals: [Double?]) -> [Marker] {
        guard let status, xs.count <= 400 else { return [] }
        return xs.indices.compactMap { i in
            guard i < status.count, let s = status[i], s != .direct else { return nil }
            return Marker(id: i, x: xs[i], y: totals[i] ?? 0, status: s)
        }
    }

    private var legend: [LegendItem] {
        var items = stacked ? stacks.map { LegendItem(label: $0.label, mark: .swatch($0.stage?.color ?? hue.color)) } : []
        if let baseline { items.append(LegendItem(label: baseline.label, mark: .line(.secondary, dash: [4, 4]))) }
        let present = Set((status ?? []).compactMap(\.self)).subtracting([.direct])
        return items + DataStatus.allCases.filter(present.contains).map { LegendItem(label: $0.label, mark: .status($0)) }
    }

    private func format(_ v: Double?) -> String { ChartFormat.value(v, unit: unit) }

    private func tip(_ i: Int, totals: [Double?]) -> ChartTip {
        ChartTip(
            title: ChartFormat.instant(xs[i], in: timeZone, withTime: withTime),
            value: totals[i].map { ChartFormat.number($0) } ?? "No data",
            unit: totals[i] == nil ? nil : unit,
            status: status.flatMap { i < $0.count ? $0[i] : nil },
            providers: providers.flatMap { i < $0.count ? $0[i] : nil } ?? [],
            rows: stacked ? stacks.map { .init(label: $0.label, value: format(i < $0.values.count ? $0.values[i] : nil)) } : []
        )
    }

    private func table(_ totals: [Double?]) -> ChartTable {
        ChartTable(
            columns: ["Window"] + stacks.map(\.label) + (stacked ? ["Total"] : []),
            rows: xs.indices.reversed().map { i in
                let cells = stacks.map { format(i < $0.values.count ? $0.values[i] : nil) } + (stacked ? [format(totals[i])] : [])
                return ChartTable.Row(id: "\(xs[i].timeIntervalSince1970)", cells: [ChartFormat.instant(xs[i], in: timeZone, withTime: withTime)] + cells)
            }
        )
    }

    private func descriptor(_ totals: [Double?]) -> ChartDescriptor {
        ChartDescriptor(
            title: title, summary: nil,
            x: .time(domain, timeZone, withTime: withTime),
            yTitle: unit.isEmpty ? "Value" : unit, yRange: 0 ... max(extent(totals).upperBound, 1), unit: unit,
            series: stacks.map { stack in
                .init(name: stack.label, continuous: false, points: zip(xs, stack.values).map { .init(x: $0.timeIntervalSince1970, y: $1) })
            }
        )
    }
}
