import Foundation
import Observation
import VitamuxKit

/// Pinning to the dashboard from Explore and metric detail (the panel's `Pins`): a card in the
/// stored layout (`GET`/`PUT /settings/dashboard`). Pinning adds a card at the end or shows a
/// hidden one; unpinning removes it.
@Observable
final class Pins {
    typealias Card = Components.Schemas.DashboardCard

    private(set) var layout: Components.Schemas.DashboardLayout?
    private(set) var problem: Problem?

    func load(_ client: Client?) async {
        guard let client else { return }
        do {
            layout = try await client.getDashboardLayout().ok.body.json
        } catch {
            problem = Problem(error)
        }
    }

    func has(_ key: String) -> Bool {
        layout?.cards.contains { $0.metric == key && !$0.hidden } ?? false
    }

    func toggle(_ key: String, client: Client?) async {
        guard let client, let layout else { return }
        let cards: [Card]
        if has(key) {
            cards = layout.cards.filter { $0.metric != key }
        } else if layout.cards.contains(where: { $0.metric == key }) {
            cards = layout.cards.map { $0.metric == key ? Card(metric: key, size: $0.size, hidden: false) : $0 }
        } else {
            cards = layout.cards + [Card(metric: key, size: .m, hidden: false)]
        }
        problem = nil
        // A PUT replaces the whole layout, so the owner's hero tiles and dismissed alerts go with it.
        let input = Components.Schemas.DashboardLayoutInput(
            version: ._1, cards: cards, hero: layout.isDefault ? nil : layout.hero, dismissed: layout.dismissed
        )
        do {
            self.layout = try await client.putDashboardLayout(body: .json(input)).ok.body.json
        } catch {
            problem = Problem(error)
        }
    }
}
