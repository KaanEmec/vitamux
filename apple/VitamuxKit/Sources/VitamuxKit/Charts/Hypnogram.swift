import Charts
import SwiftUI

/// One stage interval of a sleep session (GET sleep sessions `stages`).
public struct StageInterval: Hashable, Sendable, Identifiable {
    public var id: Date { start }
    public var stage: SleepStageKind
    public var start: Date
    public var end: Date
    public init(stage: SleepStageKind, start: Date, end: Date) {
        self.stage = stage
        self.start = start
        self.end = end
    }
}

/// Stage bars of one sleep session on a shared time axis, so several sources line up (the twin
/// of the web's `Hypnogram`). Rows (awake on top) encode the stage, labelled on the left; colour
/// is secondary. Selecting moves from stage to stage; the stages are also a table.
public struct Hypnogram: View {
    let title: String
    let stages: [StageInterval]
    let rows: [SleepStageKind]
    let from: Date
    let to: Date
    let timeZone: TimeZone
    let rowHeight: CGFloat

    @State private var selection: Date?
    @ScaledMetric(relativeTo: .caption) private var scaledRow: CGFloat = 1

    /// `rows` are the stage rows top to bottom, the same for every session compared; `from`–`to`
    /// is the shared axis.
    public init(
        title: String, stages: [StageInterval], rows: [SleepStageKind] = [.awake, .rem, .light, .deep],
        from: Date, to: Date, timeZone: TimeZone = .current, rowHeight: CGFloat = 26
    ) {
        self.title = title
        self.stages = stages.sorted { $0.start < $1.start }
        self.rows = rows
        self.from = from
        self.to = to
        self.timeZone = timeZone
        self.rowHeight = rowHeight
    }

    public var body: some View {
        let selected = selection.flatMap(pick)
        let selectedStart = selected.map { stages[$0].start }
        ChartFrame(title: title, table: table) {
            Chart {
                ForEach(stages) { s in
                    RectangleMark(
                        xStart: .value("Start", s.start), xEnd: .value("End", max(s.end, s.start.addingTimeInterval(30))),
                        y: .value("Stage", s.stage.label), height: .ratio(0.75)
                    )
                    .foregroundStyle(s.stage.color)
                    .cornerRadius(2)
                    .opacity(selectedStart == nil || selectedStart == s.start ? 1 : 0.6)
                }
                if let i = selected {
                    RuleMark(x: .value("Selected", stages[i].start.addingTimeInterval(stages[i].end.timeIntervalSince(stages[i].start) / 2)))
                        .foregroundStyle(.clear)
                        .annotation(position: .top, spacing: 0, overflowResolution: .init(x: .fit(to: .chart), y: .fit(to: .chart))) {
                            ChartCallout(tip: tip(i))
                        }
                }
            }
            .chartXScale(domain: from ... max(to, from.addingTimeInterval(60)))
            .chartYScale(domain: rows.map(\.label))
            .chartYAxis { AxisMarks(position: .leading) { AxisGridLine(); AxisValueLabel() } }
            .chartXSelection(value: $selection)
            .modifier(TimeAxis(domain: from ... max(to, from.addingTimeInterval(60)), timeZone: timeZone, height: CGFloat(rows.count) * rowHeight * max(scaledRow, 1) + 32, grows: false))
            .accessibilityLabel(title)
            .accessibilityChartDescriptor(descriptor)
        }
    }

    /// The stage under `t`, else the one whose middle is nearest.
    private func pick(_ t: Date) -> Int? {
        if let i = stages.firstIndex(where: { $0.start <= t && t < $0.end }) { return i }
        return nearest(stages.lazy.map { $0.start.addingTimeInterval($0.end.timeIntervalSince($0.start) / 2) }, to: t)
    }

    private func minutes(_ s: StageInterval) -> Int { Int((s.end.timeIntervalSince(s.start) / 60).rounded()) }

    private func tip(_ i: Int) -> ChartTip {
        let s = stages[i]
        return ChartTip(title: "\(ChartFormat.clock(s.start, in: timeZone)) – \(ChartFormat.clock(s.end, in: timeZone))", value: s.stage.label, unit: "\(minutes(s)) min")
    }

    private func table() -> ChartTable {
        ChartTable(columns: ["Start", "End", "Stage"], rows: stages.map { s in
            .init(id: "\(s.start.timeIntervalSince1970)", cells: [ChartFormat.instant(s.start, in: timeZone), ChartFormat.instant(s.end, in: timeZone), s.stage.label])
        })
    }

    private var descriptor: ChartDescriptor {
        ChartDescriptor(
            title: title, summary: nil, x: .time(from ... max(to, from.addingTimeInterval(60)), timeZone, withTime: true),
            yTitle: "Minutes", yRange: 0 ... Double(max(stages.map(minutes).max() ?? 1, 1)), unit: "min",
            series: rows.map { row in
                .init(name: row.label, continuous: false, points: stages.filter { $0.stage == row }.map {
                    .init(x: $0.start.timeIntervalSince1970, y: Double(minutes($0)), label: row.label)
                })
            }
        )
    }
}
