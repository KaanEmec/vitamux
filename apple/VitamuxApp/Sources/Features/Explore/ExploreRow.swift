import SwiftUI
import VitamuxKit

/// One Explore item: tile, name, sources and days with data, a 30-day sparkline, the latest value
/// and the last record. It opens the item's view; a swipe or long press pins it to the dashboard.
struct ExploreRow: View {
    @Environment(AppState.self) private var state
    let item: InventoryItem
    let spark: [Double?]?
    let pins: Pins

    var body: some View {
        NavigationLink(value: item.route) {
            HStack(spacing: 12) {
                MetricTile(code: item.code, section: item.metric?.section)
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 4) {
                        Text(item.name).font(.body.weight(.medium)).foregroundStyle(item.days == 0 ? .secondary : .primary)
                        if let key = item.pinKey, pins.has(key) {
                            Image(systemName: "star.fill").font(.caption2).foregroundStyle(.tint).accessibilityLabel("Pinned")
                        }
                    }
                    Text(detail).font(.footnote).foregroundStyle(.secondary).lineLimit(2)
                }
                Spacer(minLength: 8)
                if let spark, spark.contains(where: { $0 != nil }) {
                    Sparkline(values: spark, bars: item.metric?.aggregation == .additive, hue: MetricHue.of(code: item.code, section: item.metric?.section))
                        .frame(width: 56, height: 26)
                        .accessibilityHidden(true)
                }
                VStack(alignment: .trailing, spacing: 2) {
                    let latest = item.latestText
                    HStack(alignment: .firstTextBaseline, spacing: 3) {
                        Text(latest.value).font(.body.weight(.semibold)).monospacedDigit()
                        if !latest.unit.isEmpty { Text(latest.unit).font(.caption).foregroundStyle(.secondary) }
                    }
                    if item.days > 0 { Text(item.lastSeen).font(.caption).foregroundStyle(.secondary) }
                }
            }
        }
        .accessibilityIdentifier("exploreRow-\(item.code)")
        .swipeActions(edge: .trailing) { pinButton }
        .contextMenu { pinButton }
    }

    /// Sources, then days with data.
    private var detail: String {
        let sources = item.providers.map(providerLabel).joined(separator: ", ")
        guard item.days > 0 else { return "No data yet" }
        let days = item.days == 1 ? "1 day" : "\(item.days.formatted()) days"
        return sources.isEmpty ? days : "\(sources) · \(days)"
    }

    @ViewBuilder private var pinButton: some View {
        if let key = item.pinKey, pins.layout != nil {
            let pinned = pins.has(key)
            Button(pinned ? "Unpin" : "Pin", systemImage: pinned ? "star.slash" : "star") {
                Task { await pins.toggle(key, client: state.client) }
            }
            .tint(.yellow)
            .accessibilityIdentifier("pin-\(item.code)")
        }
    }
}
