import Charts
import SwiftUI
import VitamuxKit

/// Fat-free and fat mass stacked per weigh-in, fat-free at the bottom. The kit's stacked `Bars`
/// colour stacks by sleep stage only, so this small chart uses two `ChartPalette` hues, a legend
/// with the words, and a "Show as table" fallback like every kit chart. Colour names the part,
/// never a judgement.
struct CompositionBars: View {
    struct Entry: Identifiable, Hashable {
        var id: String
        var at: Date
        var fatFree: Double
        var fat: Double
        var unit: String
    }

    let entries: [Entry]
    @State private var showsTable = false
    @State private var selection: Date?

    private static let parts = [("Fat-free mass", MetricHue.weight.color), ("Fat mass", MetricHue.other.color)]

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 12) {
                ForEach(Self.parts, id: \.0) { name, color in
                    HStack(spacing: 4) {
                        RoundedRectangle(cornerRadius: 2).fill(color).frame(width: 10, height: 10)
                        Text(name)
                    }
                    .accessibilityElement(children: .combine)
                }
            }
            .font(.caption)
            .foregroundStyle(.secondary)
            if showsTable {
                table
            } else {
                chart
            }
            Button { showsTable.toggle() } label: {
                Text(showsTable ? "Show as chart" : "Show as table").tapTarget()
            }
            .font(.footnote)
            .buttonStyle(.borderless)
        }
    }

    private var unit: String { entries.last?.unit ?? "kg" }

    private var chart: some View {
        Chart {
            ForEach(entries) { entry in
                BarMark(x: .value("Weigh-in", entry.at, unit: .day), y: .value("Mass", entry.fatFree))
                    .foregroundStyle(by: .value("Part", "Fat-free mass"))
                BarMark(x: .value("Weigh-in", entry.at, unit: .day), y: .value("Mass", entry.fat))
                    .foregroundStyle(by: .value("Part", "Fat mass"))
            }
            if let picked = selection.flatMap(nearest) {
                RuleMark(x: .value("Selected", picked.at, unit: .day))
                    .foregroundStyle(.secondary.opacity(0.4))
                    .annotation(position: .top, overflowResolution: .init(x: .fit(to: .chart), y: .fit(to: .chart))) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(picked.at.formatted(date: .abbreviated, time: .omitted)).font(.caption.weight(.semibold))
                            Text("Fat-free \(Format.value(picked.fatFree, unit: unit))")
                            Text("Fat \(Format.value(picked.fat, unit: unit))")
                        }
                        .font(.caption)
                        .padding(6)
                        .background(.regularMaterial, in: .rect(cornerRadius: 6))
                    }
            }
        }
        .chartForegroundStyleScale(["Fat-free mass": Self.parts[0].1, "Fat mass": Self.parts[1].1])
        .chartLegend(.hidden)
        .chartXSelection(value: $selection)
        .chartYAxisLabel(unit)
        .frame(height: 200)
        .accessibilityLabel("Fat-free and fat mass per weigh-in, stacked")
        .accessibilityValue("\(entries.count) weigh-ins")
    }

    private func nearest(_ date: Date) -> Entry? {
        entries.min { abs($0.at.timeIntervalSince(date)) < abs($1.at.timeIntervalSince(date)) }
    }

    private var table: some View {
        Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 6) {
            GridRow {
                Text("Weigh-in")
                Text("Fat-free mass")
                Text("Fat mass")
            }
            .font(.caption.weight(.semibold))
            .foregroundStyle(.secondary)
            ForEach(entries.reversed()) { entry in
                GridRow {
                    Text(entry.at.formatted(date: .abbreviated, time: .omitted))
                    Text(Format.value(entry.fatFree, unit: entry.unit))
                    Text(Format.value(entry.fat, unit: entry.unit))
                }
                .font(.footnote)
                .monospacedDigit()
                .accessibilityElement(children: .combine)
            }
        }
        .accessibilityIdentifier("compositionTable")
    }
}
