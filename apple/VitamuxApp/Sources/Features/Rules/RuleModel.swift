import Foundation
import Observation
import VitamuxKit

/// One metric's rule (the panel's `/rules/{metric}`): the versions newest first
/// (`GET /rules/{metric}/versions`), the 90-day coverage filtered by origin app (`GET /coverage`,
/// `GET /origins`), activation of any version (`POST /rules/{metric}/activate`), and the two
/// versions being compared.
@Observable
final class RuleModel {
    let metric: String
    let saved: Int?
    let range = lastDays(90)

    private(set) var versions: Loadable<[RuleVersion]> = .loading
    private(set) var coverage: Components.Schemas.Coverage?
    private(set) var origins: [Components.Schemas.DataOrigin] = []
    private(set) var activated: Int?
    private(set) var problem: Problem?
    private(set) var busy = false
    var origin = ""
    var left = 0
    var right = 0

    init(metric: String, saved: Int?) {
        self.metric = metric
        self.saved = saved
    }

    func load(_ client: Client?) async {
        guard let client else { return }
        versions = await Loadable { try await client.listRuleVersions(path: .init(metric: metric)).ok.body.json.versions }
        let list = versions.value ?? []
        let active = list.first(where: \.active) ?? list.first
        left = active?.version ?? 0
        right = list.first { $0.version != left }?.version ?? left
    }

    func loadOrigins(_ client: Client?) async {
        guard let client else { return }
        origins = (try? await client.listOrigins().ok.body.json.origins) ?? []
    }

    func loadCoverage(_ client: Client?) async {
        guard let client else { return }
        coverage = try? await client.getCoverage(query: .init(
            startDate: range.start.description, endDate: range.end.description, metric: [metric], origin: origin.isEmpty ? nil : [origin]
        )).ok.body.json
    }

    func activate(_ version: RuleVersion, client: Client?) async {
        guard let client else { return }
        busy = true
        defer { busy = false }
        problem = nil
        do {
            activated = try await client.activateRule(path: .init(metric: metric), body: .json(.init(version: version.version))).ok.body.json.version
            await load(client)
        } catch {
            problem = Problem(error)
        }
    }

    var current: RuleVersion? {
        versions.value?.first(where: \.active)
    }

    func version(_ n: Int) -> RuleVersion? {
        versions.value?.first { $0.version == n }
    }

    var rows: [CoverageStrip.Row] {
        (coverage?.rows ?? []).filter { $0.metric == metric }.map { CoverageStrip.Row(label: $0.source, source: $0.source, cells: $0.days) }
    }
}
