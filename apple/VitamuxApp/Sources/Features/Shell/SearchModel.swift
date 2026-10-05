import Foundation
import Observation
import VitamuxKit

/// Search (the panel's ⌘K palette, web/src/lib/shell/CommandPalette.svelte): sections, settings
/// pages, connections, metrics and each metric's rule. Metrics load from `GET /metrics` when the
/// sheet opens; without a query only sections and settings show.
@Observable
final class SearchModel {
    struct Item: Identifiable, Equatable {
        var group: String
        var label: String
        var detail: String?
        var route: Route

        var id: String { "\(group)/\(label)/\(detail ?? "")" }
    }

    private(set) var metrics: Loadable<[Components.Schemas.Metric]> = .loading

    func load(_ client: Client?) async {
        guard let client else { return }
        metrics = await Loadable { try await client.listMetrics().ok.body.json.metrics }
    }

    static let groups = ["Go to", "Settings", "Connections", "Metrics", "Rules"]

    static let sections: [Item] = [
        Item(group: "Go to", label: "Dashboard", route: .dashboard()),
        Item(group: "Go to", label: "Explore", route: .explore),
        Item(group: "Go to", label: "Sources", route: .connections()),
        Item(group: "Go to", label: "Lab results", route: .lab),
        Item(group: "Go to", label: "Rules", route: .rules),
        Item(group: "Go to", label: "Apple Health", route: .appleHealth),
    ]

    static let settings: [Item] = Route.SettingsPage.allCases.map {
        Item(group: "Settings", label: $0.title, route: .settings($0))
    }

    /// At most 50 matches, grouped in `groups` order.
    func items(matching query: String, connections: [Components.Schemas.Connection]) -> [Item] {
        let metrics = metrics.value ?? []
        let all = Self.sections + Self.settings
            + connections.map { Item(group: "Connections", label: providerLabel($0.provider), detail: $0.provider, route: .connection(id: $0.id)) }
            + metrics.map { Item(group: "Metrics", label: metricLabel($0.code), detail: $0.code, route: .metric(code: $0.code)) }
            + metrics.map { Item(group: "Rules", label: "Rule for \(metricLabel($0.code).lowercased())", detail: $0.code, route: .rule(metric: $0.code)) }
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        guard !q.isEmpty else { return Self.sections + Self.settings }
        return Array(all.filter { "\($0.label) \($0.detail ?? "") \($0.group)".lowercased().contains(q) }.prefix(50))
    }
}

/// The provider's name, else the code made readable (as the panel's `providerLabel`).
func providerLabel(_ code: String) -> String {
    let known = ["withings": "Withings", "apple_health": "Apple Health", "manual": "Manual entries",
                 "garmin": "Garmin Connect", "whoop": "WHOOP"]
    return known[code] ?? metricLabel(code)
}
