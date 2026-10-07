import SwiftUI
import VitamuxKit

/// The 7-, 30- and 90-day means, lowest and highest, and days with data.
struct MetricStatsSection: View {
    let model: MetricDetailModel

    var body: some View {
        Section("Statistics") {
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 96), alignment: .leading)], alignment: .leading, spacing: 14) {
                Stat(label: "7-day mean", value: model.mean(days: 7).map(Format.number))
                Stat(label: "30-day mean", value: model.mean(days: 30).map(Format.number))
                Stat(label: "90-day mean", value: model.mean(days: 90).map(Format.number))
                Stat(label: "Lowest", value: model.lowest.map(Format.number))
                Stat(label: "Highest", value: model.highest.map(Format.number))
                let count = model.coverageCount
                Stat(label: "Days with data", value: "\(count.with.formatted()) / \(count.of.formatted())")
            }
            .padding(.vertical, 4)
            if !model.unit.isEmpty {
                Text("Values in \(model.unit == "count" ? "steps" : model.unit == "s" ? "seconds" : model.unit), ending \(Format.day(model.end)).")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
    }
}

private struct Stat: View {
    let label: String
    let value: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(label).font(.caption).foregroundStyle(.secondary)
            Text(value ?? "–").font(.title3.weight(.semibold)).monospacedDigit()
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("stat-\(label)")
    }
}

/// The values, newest first, 31 rows a page: each day with its value, status and source (opens
/// the point sheet), or each rollup's mean, min and max (drills into its week or month).
struct ValuesSection: View {
    let model: MetricDetailModel
    @Binding var selected: SelectedDay?
    @State private var rows = Self.pageRows
    private static let pageRows = 31

    var body: some View {
        Section {
            if let trend = model.trendValue {
                ForEach(trend.buckets.reversed().prefix(rows), id: \.startDate) { bucket in
                    RollupRow(bucket: bucket, grain: trend.grain, unit: model.unit) { drill(into: bucket, grain: trend.grain) }
                }
            } else {
                ForEach(model.dates.reversed().prefix(rows), id: \.self) { date in
                    DayRow(code: model.code, date: date, value: model.value(on: date)) { selected = SelectedDay(date: date) }
                }
            }
            if total > rows {
                Button("Show all \(total)") { rows = .max }.accessibilityIdentifier("showAllValues")
            }
        } header: {
            Text("Values")
        } footer: {
            Text(model.trendValue == nil ? "Newest first. A day opens its explanation and overrides." : "Newest first. A row opens its \(model.trendValue?.grain == .week ? "week" : "month").")
        }
        .onChange(of: model.key) { rows = Self.pageRows }
    }

    private var total: Int {
        model.trendValue?.buckets.count ?? model.dates.count
    }

    /// A week opens as 1W, a month as 1M, ending on its last day.
    private func drill(into bucket: Components.Schemas.Rollup, grain: Components.Schemas.ResolvedTrend.GrainPayload) {
        guard let end = LocalDate(bucket.endDate) else { return }
        model.range = grain == .week ? .week : .month
        model.end = min(end, model.latest)
    }
}

private struct DayRow: View {
    let code: String
    let date: LocalDate
    let value: ResolvedValue?
    let open: () -> Void

    var body: some View {
        Button(action: open) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(Format.day(date)).foregroundStyle(.primary)
                    let sources = dayProviders(value).map(groupLabel).joined(separator: ", ")
                    if !sources.isEmpty { Text(sources).font(.caption).foregroundStyle(.secondary) }
                }
                Spacer()
                VStack(alignment: .trailing, spacing: 2) {
                    Text(Format.resolved(value, code: code)).monospacedDigit().foregroundStyle(.primary)
                    StatusLabel(status: DataStatus(value)).font(.caption).foregroundStyle(.secondary)
                }
            }
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("valueRow-\(date)")
    }
}

private struct RollupRow: View {
    let bucket: Components.Schemas.Rollup
    let grain: Components.Schemas.ResolvedTrend.GrainPayload
    let unit: String
    let open: () -> Void

    var body: some View {
        Button(action: open) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("\(grain == .week ? "Week of" : "Month of") \(Format.day(bucket.startDate, weekday: false))").foregroundStyle(.primary)
                    Text("\(bucket.n) / \(bucket.days) days with data").font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                VStack(alignment: .trailing, spacing: 2) {
                    Text(Format.value(bucket.mean, unit: unit)).monospacedDigit().foregroundStyle(.primary)
                    if let low = bucket.min, let high = bucket.max {
                        Text("\(Format.number(low))–\(Format.number(high))").font(.caption).foregroundStyle(.secondary)
                    }
                }
                Image(systemName: "chevron.forward").font(.caption).foregroundStyle(.tertiary)
            }
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("rollupRow-\(bucket.startDate)")
    }
}
