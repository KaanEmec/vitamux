import Charts
import SwiftUI

// What the seven views share: the frame (legend, the "Show as table" fallback, Reduce Motion),
// the selection callout, the status glyph and pinch zoom.

/// One legend key: a line sample (with its dash), a swatch or a status glyph with its word.
struct LegendItem: Identifiable {
    enum Mark {
        case line(Color, dash: [CGFloat])
        case swatch(Color)
        case ring(Color)
        case status(DataStatus)
    }

    var id: String { label }
    var label: String
    var mark: Mark
}

extension ChartOverlay {
    /// Legend keys, one per label.
    static func legend(_ overlays: [ChartOverlay], hue: MetricHue) -> [LegendItem] {
        var seen = Set<String>()
        return overlays.filter { seen.insert($0.label).inserted }.map { o in
            switch o.style {
            case .shade: LegendItem(label: o.label, mark: .swatch(Color.chartMuted.opacity(0.8)))
            case .tint: LegendItem(label: o.label, mark: .swatch(hue.color.opacity(0.3)))
            case .line: LegendItem(label: o.label, mark: .line(.primary, dash: [2, 3]))
            }
        }
    }
}

/// The overlays as chart content, under the other marks.
struct OverlayMarks: ChartContent {
    let overlays: [ChartOverlay]
    let hue: MetricHue

    var body: some ChartContent {
        ForEach(overlays) { o in
            switch o.style {
            case .shade:
                RectangleMark(xStart: .value("From", o.start), xEnd: .value("To", o.end))
                    .foregroundStyle(Color.chartMuted.opacity(0.45))
            case .tint:
                RectangleMark(xStart: .value("From", o.start), xEnd: .value("To", o.end))
                    .foregroundStyle(hue.color.opacity(0.16))
            case .line:
                RuleMark(x: .value("At", o.start))
                    .foregroundStyle(Color.primary.opacity(0.7))
                    .lineStyle(StrokeStyle(lineWidth: 1.5, dash: [2, 3]))
            }
        }
    }
}

/// Frames a chart: its legend above, and a toggle that swaps it for its table.
struct ChartFrame<Content: View>: View {
    let title: String
    var legend: [LegendItem] = []
    let table: () -> ChartTable
    @ViewBuilder let content: Content

    @State private var showsTable = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if !legend.isEmpty { LegendRow(items: legend) }
            if showsTable {
                ChartTableView(title: title, table: table())
            } else {
                content.transaction { if reduceMotion { $0.animation = nil } }
            }
            Button {
                withAnimation(reduceMotion ? nil : .default) { showsTable.toggle() }
            } label: {
                Text(showsTable ? "Show as chart" : "Show as table").tapTarget()
            }
            .font(.footnote)
        }
    }
}

struct LegendRow: View {
    let items: [LegendItem]

    var body: some View {
        FlowLayout {
            ForEach(items) { item in
                HStack(spacing: 4) {
                    LegendMark(mark: item.mark)
                    Text(item.label)
                }
                .accessibilityElement(children: .combine)
            }
        }
        .font(.caption)
        .foregroundStyle(.secondary)
    }
}

private struct LegendMark: View {
    let mark: LegendItem.Mark

    var body: some View {
        switch mark {
        case let .line(color, dash):
            Path { $0.move(to: CGPoint(x: 0, y: 4)); $0.addLine(to: CGPoint(x: 16, y: 4)) }
                .stroke(color, style: StrokeStyle(lineWidth: 2, dash: dash))
                .frame(width: 16, height: 8)
        case let .swatch(color):
            RoundedRectangle(cornerRadius: 2).fill(color).frame(width: 10, height: 10)
        case let .ring(color):
            Circle().inset(by: 1).stroke(color, lineWidth: 2).frame(width: 10, height: 10)
        case let .status(status):
            StatusGlyph(status: status).frame(width: 12, height: 12)
        }
    }
}

/// Lays its subviews out in rows, wrapping to the next row when one is full (legends at any
/// Dynamic Type size).
struct FlowLayout: Layout {
    var spacing: CGFloat = 12
    var lineSpacing: CGFloat = 4

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        arrange(width: proposal.width ?? .infinity, subviews).size
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let placed = arrange(width: bounds.width, subviews)
        for (subview, frame) in zip(subviews, placed.frames) {
            subview.place(at: CGPoint(x: bounds.minX + frame.minX, y: bounds.minY + frame.minY), proposal: ProposedViewSize(frame.size))
        }
    }

    private func arrange(width: CGFloat, _ subviews: Subviews) -> (size: CGSize, frames: [CGRect]) {
        var frames: [CGRect] = []
        var x: CGFloat = 0
        var y: CGFloat = 0
        var row: CGFloat = 0
        var widest: CGFloat = 0
        for subview in subviews {
            var size = subview.sizeThatFits(.unspecified)
            if size.width > width { size = subview.sizeThatFits(ProposedViewSize(width: width, height: nil)) }
            if x > 0, x + size.width > width {
                x = 0
                y += row + lineSpacing
                row = 0
            }
            frames.append(CGRect(origin: CGPoint(x: x, y: y), size: size))
            x += size.width + spacing
            row = max(row, size.height)
            widest = max(widest, x - spacing)
        }
        return (CGSize(width: widest, height: y + row), frames)
    }
}

/// The time axis and plot height of an x/y chart: labels on local clock or calendar boundaries in
/// the person's timezone (clock times for a day or two, day and month up to about a year, month
/// and year beyond), fewer labels and a taller plot at accessibility sizes.
struct TimeAxis: ViewModifier {
    let domain: ClosedRange<Date>
    let timeZone: TimeZone
    let height: CGFloat
    /// Fixed-height plots grow at accessibility sizes; row-based ones already scale their rows.
    var grows = true
    @Environment(\.dynamicTypeSize) private var typeSize

    func body(content: Content) -> some View {
        let large = typeSize.isAccessibilitySize
        let span = domain.upperBound.timeIntervalSince(domain.lowerBound)
        var style: Date.FormatStyle = span <= 2 * 86_400 ? .dateTime.hour().minute() : span <= 400 * 86_400 ? .dateTime.day().month(.abbreviated) : .dateTime.month(.abbreviated).year(.twoDigits)
        style.timeZone = timeZone
        return content
            .chartXAxis {
                AxisMarks(values: .automatic(desiredCount: large ? 2 : 4)) { _ in
                    AxisGridLine()
                    AxisTick()
                    AxisValueLabel(format: style)
                }
            }
            .frame(height: grows && large ? height * 1.6 : height)
            .environment(\.timeZone, timeZone)
    }
}

/// The table fallback: lazy rows, newest first; at accessibility sizes each row stacks its cells
/// with their column names.
struct ChartTableView: View {
    let title: String
    let table: ChartTable
    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 6) {
                if !typeSize.isAccessibilitySize {
                    cells(table.columns).font(.caption.weight(.semibold)).accessibilityHidden(true)
                }
                ForEach(table.rows) { row in
                    Group {
                        if typeSize.isAccessibilitySize {
                            VStack(alignment: .leading, spacing: 2) {
                                ForEach(Array(zip(table.columns, row.cells)), id: \.0) { column, cell in
                                    Text("\(column): \(cell)")
                                }
                            }
                        } else {
                            cells(row.cells)
                        }
                    }
                    .font(.footnote.monospacedDigit())
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel(zip(table.columns, row.cells).map { "\($0) \($1)" }.joined(separator: ", "))
                    Divider()
                }
            }
        }
        .frame(maxHeight: 320)
        .accessibilityLabel("\(title), table")
    }

    private func cells(_ texts: [String]) -> some View {
        HStack(alignment: .firstTextBaseline) {
            // A cell is identified by its column, which is unique per table.
            ForEach(Array(zip(table.columns, texts)), id: \.0) { _, text in
                Text(text).frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }
}

/// A status shape on a 12×12 grid: circle (direct), diamond (fallback), triangle (calculated),
/// square (overridden), a half disc in a ring (partial), a ring (no data).
struct StatusShape: Shape {
    var status: DataStatus

    nonisolated func path(in rect: CGRect) -> Path {
        let s = min(rect.width, rect.height) / 12
        func p(_ x: CGFloat, _ y: CGFloat) -> CGPoint { CGPoint(x: rect.minX + x * s, y: rect.minY + y * s) }
        var path = Path()
        switch status {
        case .direct, .noData:
            path.addEllipse(in: CGRect(origin: p(1.5, 1.5), size: CGSize(width: 9 * s, height: 9 * s)))
        case .fallback:
            path.addLines([p(6, 1), p(11, 6), p(6, 11), p(1, 6)])
            path.closeSubpath()
        case .calculated:
            path.addLines([p(6, 1.5), p(10.8, 10.5), p(1.2, 10.5)])
            path.closeSubpath()
        case .overridden:
            path.addRect(CGRect(origin: p(2, 2), size: CGSize(width: 8 * s, height: 8 * s)))
        case .partial:
            path.move(to: p(6, 1.5))
            path.addArc(center: p(6, 6), radius: 4.5 * s, startAngle: .degrees(-90), endAngle: .degrees(90), clockwise: true)
            path.closeSubpath()
        }
        return path
    }
}

/// The marker of a status in its colour; partial and no data carry a ring.
public struct StatusGlyph: View {
    let status: DataStatus

    public init(status: DataStatus) { self.status = status }

    public var body: some View {
        ZStack {
            if status == .partial || status == .noData {
                Circle().inset(by: 1.5).fill(.background).stroke(status.color, lineWidth: 1.4)
            }
            if status != .noData { StatusShape(status: status).fill(status.color) }
        }
        .accessibilityHidden(true)
    }
}

/// The selection callout: the date, the value large with its unit, status (glyph and word) and
/// sources, then the other series.
struct ChartCallout: View {
    let tip: ChartTip

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(tip.title).font(.caption).foregroundStyle(.secondary)
            HStack(alignment: .firstTextBaseline, spacing: 4) {
                Text(tip.value).font(.title3.weight(.semibold).monospacedDigit())
                if let unit = tip.unit, !unit.isEmpty { Text(unit).font(.caption).foregroundStyle(.secondary) }
            }
            if let status = tip.status, status != .direct {
                HStack(spacing: 4) {
                    StatusGlyph(status: status).frame(width: 10, height: 10)
                    Text(status.label)
                }
                .font(.caption)
            }
            if !tip.providers.isEmpty {
                Text(tip.providers.map(SourceStyle.label).joined(separator: ", ")).font(.caption).foregroundStyle(.secondary)
            }
            ForEach(tip.rows, id: \.label) { row in
                HStack(spacing: 4) {
                    if let source = row.source { Circle().fill(SourceStyle.color(source)).frame(width: 6, height: 6) }
                    Text(row.label).foregroundStyle(.secondary)
                    Text(row.value).monospacedDigit()
                    if let status = row.status, status != .direct { Text("(\(status.label))").foregroundStyle(.secondary) }
                }
                .font(.caption)
            }
            if let note = tip.note { Text(note).font(.caption2).foregroundStyle(.secondary) }
        }
        .padding(8)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(tip.spoken)
    }
}

/// Pinch to zoom the time axis; when zoomed, dragging pans and "Reset zoom" returns to the whole
/// range. `zoom` (whole span / visible span) follows the visible length, for decimation. The
/// visible window lives here, or in `window` when the owner reads or sets it (nil: the whole
/// domain), e.g. to pick a finer bucket or to step through zoom levels.
struct ChartZoom: ViewModifier {
    let domain: ClosedRange<Date>
    @Binding var zoom: CGFloat
    var enabled = true
    var maxZoom: CGFloat = 96
    var window: Binding<ClosedRange<Date>?>?

    @State private var own: ClosedRange<Date>?
    @GestureState private var pinch: CGFloat = 1
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var span: TimeInterval { max(domain.upperBound.timeIntervalSince(domain.lowerBound), 1) }

    private var current: ClosedRange<Date>? {
        if let window { window.wrappedValue } else { own }
    }

    private func set(_ new: ClosedRange<Date>?) {
        if let window { window.wrappedValue = new } else { own = new }
    }

    private var visible: TimeInterval {
        current.map { min(span, max($0.upperBound.timeIntervalSince($0.lowerBound), 1)) } ?? span
    }

    func body(content: Content) -> some View {
        let visible = visible
        let length = min(span, max(span / Double(maxZoom), visible / Double(pinch)))
        let zoomed = length < span * 0.999
        content
            .chartScrollableAxes(zoomed ? .horizontal : [])
            .chartXVisibleDomain(length: length)
            .chartScrollPosition(x: Binding(get: { current?.lowerBound ?? domain.lowerBound }, set: { start in
                guard zoomed, current != nil else { return }
                let from = max(domain.lowerBound, min(start, domain.upperBound.addingTimeInterval(-visible)))
                set(from ... from.addingTimeInterval(visible))
            }))
            .simultaneousGesture(
                MagnifyGesture()
                    .updating($pinch) { value, state, _ in state = value.magnification }
                    .onEnded { value in
                        let new = min(span, max(span / Double(maxZoom), visible / Double(value.magnification)))
                        let center = (current?.lowerBound ?? domain.lowerBound).addingTimeInterval(visible / 2)
                        let from = max(domain.lowerBound, min(center.addingTimeInterval(-new / 2), domain.upperBound.addingTimeInterval(-new)))
                        set(new >= span * 0.999 ? nil : from ... from.addingTimeInterval(new))
                    },
                including: enabled ? .all : .subviews
            )
            .onChange(of: visible, initial: true) { zoom = CGFloat(span / visible) }
            .overlay(alignment: .topTrailing) {
                if zoomed {
                    Button("Reset zoom") {
                        withAnimation(reduceMotion ? nil : .default) { set(nil) }
                    }
                    .font(.caption)
                    .buttonStyle(.bordered)
                }
            }
    }
}

/// Taps on the plot: the instant under the finger, for a chart's `onSelect`.
struct ChartTap: ViewModifier {
    let action: ((Date) -> Void)?

    func body(content: Content) -> some View {
        if let action {
            content.chartOverlay { proxy in
                GeometryReader { geometry in
                    Rectangle()
                        .fill(.clear)
                        .contentShape(Rectangle())
                        .onTapGesture { location in
                            guard let plot = proxy.plotFrame else { return }
                            let x = location.x - geometry[plot].origin.x
                            if let date: Date = proxy.value(atX: x) { action(date) }
                        }
                }
            }
        } else {
            content
        }
    }
}
