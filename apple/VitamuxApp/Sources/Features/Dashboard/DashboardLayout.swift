import Foundation
import VitamuxKit

// The dashboard layout (GET/PUT /settings/dashboard), shared with the panel: ordered cards of a
// metric, a size and hidden. Pure helpers, the twin of web/src/lib/dashboard/layout.ts.

typealias DashboardCard = Components.Schemas.DashboardCard
typealias CardSize = Components.Schemas.DashboardCard.SizePayload

extension CardSize {
    var label: String { rawValue }
    /// S takes half the width; M and L the whole width, as on the panel's two-column grid.
    var isHalf: Bool { self == .s }
}

enum DashboardLayout {
    /// The curated default the server serves until a layout is saved (internal/api/dashboard.go).
    static let defaultCards: [DashboardCard] = [
        ("sleep", CardSize.l), ("resting_heart_rate", .m), ("hrv_rmssd_nightly", .m), ("hrv_sdnn", .m), ("steps", .m),
        ("vo2max", .s), ("weight", .s), ("blood_pressure", .s), ("spo2", .s), ("respiratory_rate", .s),
        ("active_energy", .s), ("total_energy", .s),
    ].map { DashboardCard(metric: $0.0, size: $0.1, hidden: false) }

    private static let labels = [
        "sleep": "Sleep", "blood_pressure": "Blood pressure", "hrv_rmssd_nightly": "HRV · nightly RMSSD",
        "hrv_sdnn": "HRV · SDNN", "vo2max": "VO₂ max", "spo2": "SpO₂", "total_energy": "Total energy",
    ]

    /// A card's title: the curated name, else the code made readable.
    static func label(_ code: String) -> String {
        labels[code] ?? metricLabel(code)
    }

    /// The card a catalogue code is pinned as: sleep stages and blood-pressure parts share one.
    static func card(of metric: Components.Schemas.Metric) -> String {
        if metric.aggregation == .sleepDerived { return "sleep" }
        return metric.group == "bp_reading" ? "blood_pressure" : metric.code
    }

    /// Where a card opens in Explore: the specialised views for the two families.
    static func route(of code: String) -> Route {
        switch code {
        case "sleep": .exploreView(.sleep)
        case "blood_pressure": .exploreView(.bloodPressure)
        default: .metric(code: code)
        }
    }

    /// One row of the grid: a whole-width card, or one or two half-width cards.
    struct Row: Identifiable {
        var cards: [DashboardCard]
        var id: String { cards[0].metric }
        var isHalf: Bool { cards[0].size.isHalf }
    }

    /// Cards in rows: a whole-width card alone, half-width cards in pairs. A half card before a
    /// whole one keeps its row to itself, as the panel's grid leaves the cell empty.
    static func rows(_ cards: [DashboardCard]) -> [Row] {
        var rows: [Row] = []
        for card in cards {
            if card.size.isHalf, let last = rows.last, last.isHalf, last.cards.count == 1 {
                rows[rows.count - 1].cards.append(card)
            } else {
                rows.append(Row(cards: [card]))
            }
        }
        return rows
    }

    /// Reorders the visible cards only: hidden ones keep their places in the saved order.
    static func move(_ cards: [DashboardCard], fromOffsets source: IndexSet, toOffset destination: Int) -> [DashboardCard] {
        let slots = cards.indices.filter { !cards[$0].hidden }
        var visible = slots.map { cards[$0] }
        visible.move(fromOffsets: source, toOffset: destination)
        var out = cards
        for (slot, card) in zip(slots, visible) { out[slot] = card }
        return out
    }

    /// Moves a card one visible place up (-1) or down (+1).
    static func move(_ cards: [DashboardCard], _ metric: String, by step: Int) -> [DashboardCard] {
        let visible = cards.filter { !$0.hidden }
        guard let from = visible.firstIndex(where: { $0.metric == metric }), visible.indices.contains(from + step) else { return cards }
        return move(cards, fromOffsets: [from], toOffset: step > 0 ? from + 2 : from - 1)
    }
}
