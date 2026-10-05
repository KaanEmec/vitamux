import Foundation
import VitamuxKit

// The Explore inventory's display rules, the twin of web/src/lib/explore/inventory.ts and
// links.ts: names, sections, the latest value, filters, where an item opens and its pin key.

typealias InventoryItem = Components.Schemas.InventoryItem

/// Explore's filters: text, source, device and origin app.
struct ExploreFilter: Equatable {
    var text = ""
    var provider = ""
    var device = ""
    var origin = ""

    var isActive: Bool { !text.isEmpty || !provider.isEmpty || !device.isEmpty || !origin.isEmpty }
}

extension Components.Schemas.DeviceRef {
    var key: String { id ?? _type ?? "" }
    var label: String { model ?? _type ?? id ?? "device" }
}

extension Components.Schemas.OriginRef {
    var label: String { name ?? key ?? "app" }
}

extension InventoryItem {
    /// Unique across kinds (a metric and an event may share a code).
    var key: String { "\(kind.rawValue)/\(code)" }

    private static let groupNames = ["bp_reading": "Blood pressure", "body_composition": "Body composition"]

    var name: String {
        switch kind {
        case .analyte: analyte?.name ?? code
        case .group: Self.groupNames[code] ?? metricLabel(code)
        default: metricLabel(code)
        }
    }

    /// The section the item is listed under: the catalogue section for metrics and groups.
    func section(metrics: [String: InventoryItem]) -> String {
        switch kind {
        case .metric: Self.title(metric?.section ?? "Other")
        case .group: (components ?? []).lazy.compactMap { metrics[$0]?.metric?.section }.first.map(Self.title) ?? Self.groupNames[code] ?? "Other"
        case .sleep: "Sleep"
        case .workouts: "Activity"
        case .event: "Events"
        case .analyte: "Lab analytes"
        }
    }

    private static func title(_ section: String) -> String {
        section.prefix(1).uppercased() + section.dropFirst()
    }

    /// The newest record as printed: value and unit, a group's components, a level or text.
    var latestText: (value: String, unit: String) {
        guard let latest else { return ("–", "") }
        if kind == .sleep, let value = latest.value { return (Format.duration(value), "asleep") }
        if let parts = latest.components?.additionalProperties, !parts.isEmpty {
            if let sys = parts["bp_systolic"], let dia = parts["bp_diastolic"] { return ("\(Format.number(sys))/\(Format.number(dia))", "mmHg") }
            return (parts.sorted { $0.key < $1.key }.map { Format.number($0.value) }.joined(separator: " · "), "")
        }
        if let value = latest.value {
            let unit = latest.unit ?? ""
            return unit == "s" ? (Format.duration(value), "") : (Format.number(value), unit == "count" ? "" : unit)
        }
        return (latest.text ?? latest.level ?? "–", "")
    }

    /// The last record's time today, else its date.
    var lastSeen: String {
        guard let lastAt else { return LocalDate(lastDate).map { Format.day($0, weekday: false) } ?? "–" }
        if lastDate == LocalDate.today(in: .current).description { return lastAt.formatted(date: .omitted, time: .shortened) }
        return lastAt.formatted(.dateTime.day().month(.abbreviated))
    }

    /// Where the item opens: metrics their detail; sleep, workouts, groups and events their
    /// specialised views (J22.9); analytes their lab history (J22.12).
    var route: Route {
        switch kind {
        case .sleep: .exploreView(.sleep)
        case .workouts: .exploreView(.workouts)
        case .event: .events(code: code)
        case .analyte: .analyte(code: code)
        case .group:
            switch code {
            case "bp_reading": .exploreView(.bloodPressure)
            case "body_composition": .exploreView(.bodyComposition)
            default: .metric(code: code)
            }
        case .metric:
            switch code {
            case "sleep": .exploreView(.sleep)
            case "blood_pressure": .exploreView(.bloodPressure)
            default: .metric(code: code)
            }
        }
    }

    /// The dashboard card key (a catalogue code or rule family), or nil when it has no card.
    var pinKey: String? {
        switch kind {
        case .metric: code
        case .sleep: "sleep"
        case .group where code == "bp_reading": "blood_pressure"
        default: nil
        }
    }

    /// Whether the item matches every filter; the text matches its name, code, devices, origins or analyte.
    func matches(_ filter: ExploreFilter) -> Bool {
        if !filter.provider.isEmpty, !providers.contains(filter.provider) { return false }
        if !filter.device.isEmpty, !devices.contains(where: { $0.key == filter.device }) { return false }
        if !filter.origin.isEmpty, !origins.contains(where: { $0.key == filter.origin }) { return false }
        let query = filter.text.trimmingCharacters(in: .whitespaces).lowercased()
        guard !query.isEmpty else { return true }
        let haystack = [name, code, analyte?.code].compactMap(\.self) + devices.map(\.label) + origins.map(\.label)
        return haystack.contains { $0.lowercased().contains(query) }
    }

    /// A catalogue metric without data, listed when "Show metrics without data" is on.
    static func empty(_ metric: Components.Schemas.Metric) -> InventoryItem {
        InventoryItem(kind: .metric, code: metric.code, count: 0, days: 0, firstDate: "", lastDate: "", providers: [], devices: [], origins: [], metric: metric)
    }
}
