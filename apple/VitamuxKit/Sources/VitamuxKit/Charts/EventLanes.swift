import Charts
import SwiftUI

/// Events on a shared time axis, one lane per source or event type (workouts, sessions,
/// notifications), labelled on the left (the twin of the web's `EventLanes`). Each event is a bar
/// in its lane's source colour, or the metric hue; selecting moves from event to event in time
/// order, and the events are also a table.
public struct EventLanes: View {
    public struct Event: Hashable, Sendable {
        public var start: Date
        public var end: Date
        public var label: String
        public init(start: Date, end: Date, label: String) {
            self.start = start
            self.end = end
            self.label = label
        }
    }

    public struct Lane: Hashable, Sendable, Identifiable {
        public var id: String { label }
        public var label: String
        public var source: String?
        public var events: [Event]
        public init(label: String, source: String? = nil, events: [Event]) {
            self.label = label
            self.source = source
            self.events = events
        }
    }

    /// An event with its lane, in time order.
    private struct Placed: Identifiable {
        var id: String { "\(lane)-\(event.start.timeIntervalSince1970)-\(event.label)" }
        var lane: Int
        var event: Event
        var mid: Date { event.start.addingTimeInterval(event.end.timeIntervalSince(event.start) / 2) }
    }

    let title: String
    let lanes: [Lane]
    let from: Date
    let to: Date
    let hue: MetricHue
    let timeZone: TimeZone
    private let placed: [Placed]

    @State private var selection: Date?
    @ScaledMetric(relativeTo: .caption) private var laneHeight: CGFloat = 30

    public init(title: String, lanes: [Lane], from: Date, to: Date, hue: MetricHue = .other, timeZone: TimeZone = .current) {
        self.title = title
        self.lanes = lanes
        self.from = from
        self.to = to
        self.hue = hue
        self.timeZone = timeZone
        placed = lanes.enumerated()
            .flatMap { lane, l in l.events.map { Placed(lane: lane, event: $0) } }
            .sorted { ($0.event.start, $0.lane) < ($1.event.start, $1.lane) }
    }

    public var body: some View {
        let domain = from ... max(to, from.addingTimeInterval(60))
        // An instant still shows: at least 1/200 of the axis wide.
        let minimum = domain.upperBound.timeIntervalSince(domain.lowerBound) / 200
        let selected = selection.flatMap(pick)
        let selectedID = selected.map { placed[$0].id }
        ChartFrame(title: title, table: table) {
            Chart {
                ForEach(placed) { p in
                    let lane = lanes[p.lane]
                    RectangleMark(
                        xStart: .value("Start", p.event.start), xEnd: .value("End", max(p.event.end, p.event.start.addingTimeInterval(minimum))),
                        y: .value("Lane", lane.label), height: .ratio(0.6)
                    )
                    .foregroundStyle(lane.source.map(SourceStyle.color) ?? hue.color)
                    .cornerRadius(3)
                    .opacity(selectedID == nil || selectedID == p.id ? 1 : 0.6)
                }
                if let i = selected {
                    RuleMark(x: .value("Selected", placed[i].mid))
                        .foregroundStyle(.clear)
                        .annotation(position: .top, spacing: 0, overflowResolution: .init(x: .fit(to: .chart), y: .fit(to: .chart))) {
                            ChartCallout(tip: tip(i))
                        }
                }
            }
            .chartXScale(domain: domain)
            .chartYScale(domain: lanes.map(\.label))
            .chartYAxis { AxisMarks(position: .leading) { AxisGridLine(); AxisValueLabel() } }
            .chartXSelection(value: $selection)
            .modifier(TimeAxis(domain: domain, timeZone: timeZone, height: CGFloat(lanes.count) * laneHeight + 32, grows: false))
            .accessibilityLabel(title)
            .accessibilityChartDescriptor(descriptor(domain))
        }
    }

    private func pick(_ t: Date) -> Int? {
        if let i = placed.firstIndex(where: { $0.event.start <= t && t <= $0.event.end }) { return i }
        return nearest(placed.lazy.map(\.mid), to: t)
    }

    private func span(_ e: Event) -> String {
        e.end > e.start ? "\(ChartFormat.instant(e.start, in: timeZone)) – \(ChartFormat.instant(e.end, in: timeZone))" : ChartFormat.instant(e.start, in: timeZone)
    }

    private func tip(_ i: Int) -> ChartTip {
        let p = placed[i]
        let lane = lanes[p.lane]
        return ChartTip(title: span(p.event), value: p.event.label, rows: [.init(label: "Lane", value: lane.label, source: lane.source)])
    }

    private func table() -> ChartTable {
        ChartTable(columns: ["Lane", "Event", "Start", "End"], rows: placed.reversed().map { p in
            .init(id: p.id, cells: [lanes[p.lane].label, p.event.label, ChartFormat.instant(p.event.start, in: timeZone), ChartFormat.instant(p.event.end, in: timeZone)])
        })
    }

    private func descriptor(_ domain: ClosedRange<Date>) -> ChartDescriptor {
        let minutes = { (e: Event) in e.end.timeIntervalSince(e.start) / 60 }
        return ChartDescriptor(
            title: title, summary: "\(placed.count) events in \(lanes.count) lanes", x: .time(domain, timeZone, withTime: true),
            yTitle: "Minutes", yRange: 0 ... max(lanes.flatMap(\.events).map(minutes).max() ?? 1, 1), unit: "min",
            series: lanes.map { lane in
                .init(name: lane.label, continuous: false, points: lane.events.map { .init(x: $0.start.timeIntervalSince1970, y: minutes($0), label: $0.label) })
            }
        )
    }
}
