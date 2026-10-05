import Foundation
import Observation
import VitamuxKit

/// Explore (the panel's `/explore`): everything stored (`GET /inventory`), grouped by section, with
/// a 30-day sparkline per metric (`GET /resolved/summary`, 20 metrics a call), filters, and the
/// catalogue metrics without data (`GET /metrics`) when asked for.
@Observable
final class ExploreModel {
    struct Section: Identifiable {
        var name: String
        var items: [InventoryItem]
        var id: String { name }
    }

    private(set) var inventory: Loadable<Components.Schemas.Inventory> = .loading
    private(set) var sparks: [String: [Double?]] = [:]
    private(set) var catalogue: [Components.Schemas.Metric] = []
    var filter = ExploreFilter()
    var showEmpty = false

    /// GET /resolved/summary takes at most 20 metrics.
    private static let summaryBatch = 20

    func load(_ client: Client?) async {
        guard let client else { return }
        inventory = await Loadable { try await client.getInventory().ok.body.json }
        guard let items = inventory.value?.items else { return }
        let codes = items.filter { $0.kind == .metric }.map(\.code)
        await withTaskGroup { group in
            for start in stride(from: 0, to: codes.count, by: Self.summaryBatch) {
                let batch = Array(codes[start..<min(start + Self.summaryBatch, codes.count)])
                group.addTask { try? await client.getResolvedSummary(query: .init(metrics: batch)).ok.body.json }
            }
            for await summary in group {
                for (code, entry) in summary?.metrics.additionalProperties ?? [:] {
                    sparks[code] = entry.sparkline.map { $0.value?.number(for: code) }
                }
            }
        }
    }

    func loadCatalogue(_ client: Client?) async {
        guard showEmpty, catalogue.isEmpty, let client else { return }
        catalogue = (try? await client.listMetrics().ok.body.json.metrics) ?? []
    }

    private var stored: [InventoryItem] { inventory.value?.items ?? [] }

    /// Stored items plus, when asked for, catalogue metrics without data.
    private var all: [InventoryItem] {
        guard showEmpty else { return stored }
        let have = Set(stored.filter { $0.kind == .metric }.map(\.code))
        return stored + catalogue.filter { !have.contains($0.code) }.map(InventoryItem.empty)
    }

    var isLoadingCatalogue: Bool { showEmpty && catalogue.isEmpty }

    /// Matching items by section in the server's order; events and lab analytes last.
    var sections: [Section] {
        let items = all
        let metrics = Dictionary(items.filter { $0.kind == .metric }.map { ($0.code, $0) }) { first, _ in first }
        var order: [String] = []
        var bySection: [String: [InventoryItem]] = [:]
        for item in items where item.matches(filter) {
            let name = item.section(metrics: metrics)
            if bySection[name] == nil { order.append(name) }
            bySection[name, default: []].append(item)
        }
        let last = ["Events", "Lab analytes"]
        let sorted = order.filter { !last.contains($0) } + last.filter(order.contains)
        return sorted.map { Section(name: $0, items: bySection[$0]!) }
    }

    var providers: [String] {
        Array(Set(all.flatMap(\.providers))).sorted()
    }

    var devices: [Components.Schemas.DeviceRef] {
        var seen = Set<String>()
        return all.flatMap(\.devices).filter { seen.insert($0.key).inserted }
    }

    var origins: [Components.Schemas.OriginRef] {
        var seen = Set<String>()
        return all.flatMap(\.origins).filter { seen.insert($0.key ?? "").inserted }
    }

    /// "7 metrics · 1 event type · 1 lab analyte".
    var counts: String {
        let n = { (kind: InventoryItem.KindPayload) in self.stored.filter { $0.kind == kind }.count }
        let parts: [(Int, String, String)] = [(n(.metric), "metric", "metrics"), (n(.event), "event type", "event types"), (n(.analyte), "lab analyte", "lab analytes")]
        return parts.filter { $0.0 > 0 }.map { "\($0.0) \($0.0 == 1 ? $0.1 : $0.2)" }.joined(separator: " · ")
    }

    func clearFilters() {
        filter = ExploreFilter()
    }
}
