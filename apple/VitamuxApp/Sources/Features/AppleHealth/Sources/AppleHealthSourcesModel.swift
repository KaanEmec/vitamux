import Foundation
import HealthBridgeCore
import HealthBridgeHealthKit
import Observation
import VitamuxKit

/// Apple Health › Sources (J22.25): which apps' Apple Health data this iPhone takes, per app and
/// optionally per type (`GET` and `PUT /devices/{id}/source-filter`). The server evaluates the
/// defaults; a change replaces the full list of explicit choices, guarded by the filter's version.
@Observable
final class AppleHealthSourcesModel {
    typealias Filter = Components.Schemas.SourceFilterView
    typealias Origin = Components.Schemas.SourceFilterOrigin
    typealias Choice = Components.Schemas.SourceFilterChoice

    /// What the owner picks for one app.
    enum Pick: Hashable {
        case take, ignore
        case types(Set<String>)
        /// Back to the default.
        case useDefault
    }

    private(set) var filter: Loadable<Filter> = .loading
    private(set) var saving: String?
    /// A failed change; the list shows the server's current state.
    private(set) var problem: Problem?

    func load(_ client: Client?, deviceID: String?) async {
        guard let client, let deviceID else { return }
        filter = await Loadable { try await client.getDeviceSourceFilter(path: .init(id: deviceID)).ok.body.json }
    }

    /// Saves `pick` for `origin` and returns whether it was stored. A stale version (409) reloads
    /// the list and shows the problem.
    @discardableResult
    func set(_ pick: Pick, for origin: Origin, client: Client?, deviceID: String?) async -> Bool {
        guard let client, let deviceID, let current = filter.value else { return false }
        saving = origin.bundleId
        defer { saving = nil }
        let body = Components.Schemas.SourceFilterUpdate(version: current.version, origins: Self.choices(current, setting: pick, for: origin))
        do {
            filter = .loaded(try await client.setDeviceSourceFilter(path: .init(id: deviceID), body: .json(body)).ok.body.json)
            problem = nil
            return true
        } catch {
            problem = Problem(error)
            if problem?.status == 409 { await load(client, deviceID: deviceID) }
            return false
        }
    }

    /// The full explicit list with `origin` changed. A Watch extension that only follows its
    /// parent's choice is not listed on its own.
    static func choices(_ filter: Filter, setting pick: Pick, for origin: Origin) -> [Choice] {
        let byBundle = Dictionary(filter.origins.map { ($0.bundleId, $0) }) { first, _ in first }
        var out: [Choice] = filter.origins.filter { candidate in
            guard candidate.explicit, candidate.bundleId != origin.bundleId else { return false }
            if let parent = SourceFilter.parent(of: candidate.bundleId), let p = byBundle[parent], p.explicit,
               p.mode == candidate.mode, p.types == candidate.types {
                return false
            }
            return true
        }
        .map { Choice(bundleId: $0.bundleId, name: $0.name, mode: $0.mode, types: $0.mode == .perType ? $0.types : nil) }
        let written = Set(origin.writes.map(\._type))
        let choice: Choice? = switch pick {
        case .take: Choice(bundleId: origin.bundleId, name: origin.name, mode: .take)
        case .ignore: Choice(bundleId: origin.bundleId, name: origin.name, mode: .ignore)
        case .types(let types) where types.isEmpty: Choice(bundleId: origin.bundleId, name: origin.name, mode: .ignore)
        case .types(let types) where !written.isEmpty && written.isSubset(of: types):
            Choice(bundleId: origin.bundleId, name: origin.name, mode: .take)
        case .types(let types): Choice(bundleId: origin.bundleId, name: origin.name, mode: .perType, types: types.sorted())
        case .useDefault: nil
        }
        if let choice { out.append(choice) }
        return out
    }

    /// "1 ignored", "1 ignored · 1 per type" or "All apps taken" for the Apple Health screen.
    var summary: String? {
        guard let origins = filter.value?.origins else { return nil }
        let ignored = origins.filter { $0.mode == .ignore }.count, perType = origins.filter { $0.mode == .perType }.count
        let parts = [ignored > 0 ? "\(ignored) ignored" : nil, perType > 0 ? "\(perType) per type" : nil].compactMap(\.self)
        return parts.isEmpty ? "All apps taken" : parts.joined(separator: " · ")
    }
}

extension Components.Schemas.SourceFilterOrigin {
    var title: String {
        guard let name, !name.isEmpty else { return bundleId }
        return name
    }

    var isNative: Bool { classification == .native || SourceFilter.isNative(bundleId) }

    /// The types written plus any taken per type that the phone has not reported.
    var choosableTypes: [String] {
        let written = writes.map(\._type)
        return written + types.filter { !written.contains($0) }
    }

    /// The types currently taken among `choosableTypes`.
    var takenTypes: Set<String> {
        switch mode {
        case .take: Set(choosableTypes)
        case .ignore: []
        case .perType: Set(types)
        }
    }

    /// "Heart rate, Steps · last sample 2 h ago", or "Not reported yet".
    var writesText: String {
        guard !writes.isEmpty else { return "Not reported yet" }
        let names = writes.map { healthTypeLabel($0._type) }.joined(separator: ", ")
        guard let last = writes.compactMap(\.lastSampleAt).max() else { return names }
        return "\(names) · last sample \(last.formatted(.relative(presentation: .numeric, unitsStyle: .abbreviated)))"
    }

    /// Native, "Relayed from X" or Direct (Settings › Devices' classification).
    var classificationText: String {
        switch classification {
        case .native: "Native"
        case .relayed: relayedProvider.map { "Relayed from \(providerLabel($0))" } ?? "Relayed"
        case .direct: "Direct"
        }
    }
}

/// "HKQuantityTypeIdentifierHeartRate" → "Heart rate": the registry's name in sentence case,
/// acronyms kept ("Heart rate variability SDNN").
func healthTypeLabel(_ id: String) -> String {
    let name = (Registry.v2.first { $0.id == id } ?? Registry.v1.first { $0.id == id })?.displayName ?? id
    let words = name.split(separator: " ").enumerated().map { index, word in
        index == 0 || word.allSatisfy(\.isUppercase) ? String(word) : word.lowercased()
    }
    return words.joined(separator: " ")
}
