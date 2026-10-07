import Foundation
import Observation
import VitamuxKit

typealias Backfill = Components.Schemas.Backfill

/// Sources (the panel's `/connections`): every connection with its runs of the last 14 days and
/// its backfills, and the providers (for names and the app-credential state after a failed return).
@Observable
final class SourcesModel {
    private(set) var connections: Loadable<[Connection]> = .loading
    private(set) var runs: [String: Loadable<[Run]>] = [:]
    private(set) var backfills: [String: [Backfill]] = [:]
    private(set) var providers: [Provider] = []

    /// How many latest runs the list shows.
    static let latest = 8

    func load(_ client: Client?) async {
        guard let client else { return }
        async let providers = try? client.listProviders().ok.body.json.providers
        connections = await Loadable { try await client.listConnections().ok.body.json.connections }
        let list = connections.value ?? []
        await withTaskGroup(of: (String, Loadable<[Run]>?, [Backfill]?).self) { group in
            for c in list {
                group.addTask { await (c.id, Loadable { try await Runs.load(client, id: c.id) }, nil) }
                if c.mode != .push {
                    group.addTask { (c.id, nil, try? await client.listBackfills(path: .init(id: c.id)).ok.body.json.backfills) }
                }
            }
            for await (id, runs, backfills) in group {
                if let runs { self.runs[id] = runs }
                if let backfills { self.backfills[id] = backfills }
            }
        }
        self.providers = await providers ?? self.providers
    }

    /// After an action on one card: the connection as it is now, and its runs again.
    func replace(_ connection: Connection, client: Client?) async {
        guard case .loaded(var list) = connections, let index = list.firstIndex(where: { $0.id == connection.id }) else { return }
        list[index] = connection
        connections = .loaded(list)
        guard let client else { return }
        runs[connection.id] = await Loadable { try await Runs.load(client, id: connection.id) }
    }

    /// "3 sources · 2 healthy · 1 needs attention".
    var summary: String {
        let list = connections.value ?? []
        guard !list.isEmpty else { return "Sources send Vitamux your health data. Connect your first one below." }
        let attention = list.filter(\.health.needsAttention).count
        let healthy = list.filter { $0.health == .ok }.count
        var out = "\(Format.plural(list.count, "source")) · \(healthy) healthy"
        if attention > 0 { out += " · \(attention) need\(attention == 1 ? "s" : "") attention" }
        return out
    }

    struct Running: Identifiable {
        var connection: Connection
        var backfill: Backfill
        var id: String { backfill.id }
    }

    var running: [Running] {
        (connections.value ?? []).flatMap { c in
            (backfills[c.id] ?? []).filter { $0.status == .running }.map { Running(connection: c, backfill: $0) }
        }
    }

    struct Recent: Identifiable {
        var connection: Connection
        var run: Run
        var id: String { "\(connection.id)/\(run.id)" }
    }

    var recent: [Recent] {
        let all = (connections.value ?? []).flatMap { c in (runs[c.id]?.value ?? []).map { Recent(connection: c, run: $0) } }
        return Array(all.sorted { $0.run.startedAt > $1.run.startedAt }.prefix(Self.latest))
    }
}

extension Backfill {
    var total: Int { unitCounts.pending + unitCounts.running + unitCounts.done + unitCounts.failed }

    /// "6 of 10 units done, 1 failed".
    var progressText: String {
        var out = "\(unitCounts.done) of \(total) units done"
        if unitCounts.failed > 0 { out += ", \(unitCounts.failed) failed" }
        return out
    }
}
