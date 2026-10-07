import Charts
import SwiftUI

/// Coverage per source and day (the twin of the web's `CoverageStrip`): one row per source in its
/// stable colour, one cell per day from `start` (or per `noun`, e.g. the Day view's buckets),
/// shaded by the share of hours with data (0–1). A row with `picks` is a source-per-window strip
/// instead: each cell takes the colour of the source behind that window's value (nil: none).
/// Every row has a text summary, so the value never depends on colour alone.
public struct CoverageStrip: View {
    public struct Row: Hashable, Sendable, Identifiable {
        public var id: String { label }
        public var label: String
        /// The provider whose colour the row takes.
        public var source: String?
        /// Share of each cell with data, 0–1.
        public var cells: [Double]
        /// The source behind each window's value, for a source-per-window row.
        public var picks: [String?]?
        public init(label: String, source: String? = nil, cells: [Double], picks: [String?]? = nil) {
            self.label = label
            self.source = source
            self.cells = cells
            self.picks = picks
        }
    }

    private struct Cell: Identifiable {
        var id: Int
        var color: Color
        var opacity: Double
    }

    let caption: String
    let rows: [Row]
    let start: LocalDate
    let noun: String
    let cellLabel: (Int) -> String

    public init(caption: String, rows: [Row], start: LocalDate, noun: String = "days", cellLabel: ((Int) -> String)? = nil) {
        self.caption = caption
        self.rows = rows
        self.start = start
        self.noun = noun
        self.cellLabel = cellLabel ?? { start.adding(days: $0).description }
    }

    public var body: some View {
        ChartFrame(title: caption, table: table) {
            VStack(alignment: .leading, spacing: 6) {
                ForEach(rows) { row in
                    VStack(alignment: .leading, spacing: 2) {
                        HStack {
                            Text(SourceStyle.label(row.label)).font(.caption.monospaced()).lineLimit(1)
                            Spacer()
                            Text(short(row)).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                        }
                        Chart(cells(row)) { cell in
                            RectangleMark(xStart: .value("Start", Double(cell.id) + 0.08), xEnd: .value("End", Double(cell.id) + 0.92), yStart: .value("Low", 0), yEnd: .value("High", 1))
                                .foregroundStyle(cell.color.opacity(cell.opacity))
                                .cornerRadius(1.5)
                        }
                        .chartXScale(domain: 0 ... Double(max(row.cells.count, 1)))
                        .chartYScale(domain: 0 ... 1)
                        .chartXAxis(.hidden)
                        .chartYAxis(.hidden)
                        .frame(height: 14)
                        if row.picks != nil { key(row) }
                    }
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel(summary(row))
                }
            }
            .accessibilityElement(children: .contain)
            .accessibilityLabel(caption)
            .accessibilityChartDescriptor(descriptor)
        }
    }

    private static func level(_ share: Double) -> Double {
        share <= 0 ? 0 : share < 0.25 ? 0.25 : share < 0.5 ? 0.5 : share < 0.75 ? 0.75 : 1
    }

    private func cells(_ row: Row) -> [Cell] {
        row.cells.indices.map { i in
            if let picks = row.picks {
                let pick = i < picks.count ? picks[i] : nil
                return Cell(id: i, color: pick.map(SourceStyle.color) ?? .chartMuted, opacity: 1)
            }
            let level = Self.level(row.cells[i])
            return Cell(id: i, color: level == 0 ? .chartMuted : row.source.map(SourceStyle.color) ?? MetricHue.other.color, opacity: level == 0 ? 1 : level)
        }
    }

    /// Windows per source of a source-per-window row, most first.
    private func tally(_ row: Row) -> [(String, Int)] {
        var counts: [String: Int] = [:]
        for case let s? in row.picks ?? [] { counts[s, default: 0] += 1 }
        return counts.sorted { ($1.value, $0.key) < ($0.value, $1.key) }
    }

    private func withData(_ row: Row) -> Int { row.cells.filter { $0 > 0 }.count }

    private func short(_ row: Row) -> String { "\(withData(row))/\(row.cells.count)\(noun == "days" ? " d" : "")" }

    private func summary(_ row: Row) -> String {
        let label = SourceStyle.label(row.label)
        guard let picks = row.picks else { return "\(label): data on \(withData(row)) of \(row.cells.count) \(noun)" }
        let parts = tally(row).map { "\(SourceStyle.label($0.0)) \($0.1)" } + ["none \(picks.filter { $0 == nil }.count)"]
        return "\(label): \(parts.joined(separator: ", ")) of \(row.cells.count) \(noun)"
    }

    private func key(_ row: Row) -> some View {
        LegendRow(items: tally(row).map { LegendItem(label: SourceStyle.label($0.0), mark: .swatch(SourceStyle.color($0.0))) } + [LegendItem(label: "None", mark: .swatch(.chartMuted))])
    }

    private func table() -> ChartTable {
        let count = rows.map(\.cells.count).max() ?? 0
        return ChartTable(columns: [noun == "days" ? "Date" : "Window"] + rows.map { SourceStyle.label($0.label) }, rows: (0 ..< count).reversed().map { i in
            .init(id: "\(i)", cells: [cellLabel(i)] + rows.map { row in
                if let picks = row.picks { return (i < picks.count ? picks[i] : nil).map(SourceStyle.label) ?? "no value" }
                return i < row.cells.count ? "\(Int((row.cells[i] * 100).rounded()))%" : "–"
            })
        })
    }

    private var descriptor: ChartDescriptor {
        let count = rows.map(\.cells.count).max() ?? 0
        let labels = (0 ..< count).map(cellLabel)
        return ChartDescriptor(
            title: caption, summary: rows.map(summary).joined(separator: ". "), x: .category(title: noun.capitalized, labels),
            yTitle: "Coverage", yRange: 0 ... 100, unit: "%",
            series: rows.map { row in
                .init(name: SourceStyle.label(row.label), continuous: false, points: row.cells.enumerated().map { .init(category: labels[$0], y: $1 * 100) })
            }
        )
    }
}
