import Foundation
import Observation
import VitamuxKit

typealias BodyGroup = Components.Schemas.Group

/// Body composition (the panel's `/explore/body-composition`): every weigh-in in the range
/// (`GET /groups?kind=body_composition`, every page), weight per source, fat-free and fat mass
/// stacked per weigh-in, and every component of every weigh-in. Values are shown as measured.
@Observable
final class BodyCompositionModel {
    var span = ViewRange(.quarter)
    private(set) var groups: Loadable<[BodyGroup]> = .loading

    /// Column order of the components; others follow alphabetically.
    static let order = ["weight", "body_fat_ratio", "fat_mass", "fat_free_mass", "muscle_mass", "bone_mass", "hydration"]

    func load(_ client: Client?) async {
        guard let client else { return }
        let start = span.start()?.description, end = span.end.description
        groups = await Loadable {
            try await readAll { cursor in
                let page = try await client.listGroups(query: .init(kind: .bodyComposition, startDate: start, endDate: end, limit: 500, cursor: cursor)).ok.body.json
                return (page.value2.groups, nextCursor(page.value1))
            }
        }
    }

    /// Weigh-ins oldest first.
    var weighIns: [BodyGroup] {
        (groups.value ?? []).sorted { $0.measuredAt < $1.measuredAt }
    }

    /// One weight series per source, so a scale and an app that relays it stay apart.
    var weightSeries: [ChartSeries] {
        let weighed = weighIns.filter { $0.component("weight") != nil }
        var order: [String] = []
        var points: [String: [ChartPoint]] = [:]
        for group in weighed {
            let provider = group.source.provider
            if points[provider] == nil { order.append(provider) }
            points[provider, default: []].append(ChartPoint(x: group.measuredAt, y: group.component("weight")?.value))
        }
        return order.map { ChartSeries(label: providerLabel($0), points: points[$0]!, source: $0) }
    }

    var weightUnit: String {
        weighIns.last { $0.component("weight") != nil }?.component("weight")?.unit ?? "kg"
    }

    /// The newest weight and the change since the first in the range, from the same source.
    var latestWeight: (value: Double, date: String, change: Double?)? {
        guard let last = weighIns.last(where: { $0.component("weight") != nil }), let value = last.component("weight")?.value else { return nil }
        let first = weighIns.first { $0.source.provider == last.source.provider && $0.component("weight") != nil }
        let change = first.flatMap { $0.id == last.id ? nil : $0.component("weight").map { value - $0.value } }
        return (value, last.localDate, change)
    }

    /// Fat-free and fat mass of each weigh-in that has both.
    var stacked: [CompositionBars.Entry] {
        weighIns.compactMap { group in
            guard let lean = group.component("fat_free_mass"), let fat = group.component("fat_mass") else { return nil }
            return CompositionBars.Entry(id: group.id, at: group.measuredAt, fatFree: lean.value, fat: fat.value, unit: lean.unit)
        }
    }

    /// Every component code present, in display order.
    var codes: [String] {
        let present = Set(weighIns.flatMap { $0.components.map(\.metric) })
        let rank = { (code: String) in Self.order.firstIndex(of: code) ?? Self.order.count }
        return present.sorted { (rank($0), $0) < (rank($1), $1) }
    }

    static func name(_ code: String) -> String {
        ["fat_free_mass": "Fat-free mass", "body_fat_ratio": "Body fat"][code] ?? metricLabel(code)
    }
}

extension BodyGroup {
    func component(_ code: String) -> Components.Schemas.GroupComponent? {
        components.first { $0.metric == code }
    }
}
