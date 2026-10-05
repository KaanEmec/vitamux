import Foundation
import Testing
@testable import VitamuxKit

/// The fake server's Explore endpoints (FakeServer+Explore.swift) decode through the generated
/// client and apply overrides the way the engine does, so the app's UI tests stand on them.
struct ExploreFakeTests {
    let fake = FakeServer()
    let sessions = SessionStore.inMemory()

    func signedIn() async throws -> Client {
        let client = fake.client(sessions: sessions)
        let body = Components.Schemas.LoginRequest(username: FakeServer.Owner.username, password: FakeServer.Owner.password, client: .app, deviceName: "Test iPhone")
        guard case .AppSession(let session) = try await client.login(body: .json(body)).ok.body.json else {
            Issue.record("expected an app session")
            return client
        }
        try sessions.save(session, for: fake.profile)
        return client
    }

    var today: LocalDate { LocalDate.today(in: ExploreFixture.zone) }

    func day(_ client: Client, _ metric: String, _ date: LocalDate) async throws -> Components.Schemas.ResolvedValue? {
        let days = try await client.getResolvedDaily(query: .init(startDate: date.description, endDate: date.description, metrics: [metric])).ok.body.json
        return days.days.first?.metrics.additionalProperties[metric]
    }

    @Test func `the inventory and catalogue decode`() async throws {
        let client = try await signedIn()
        let inventory = try await client.getInventory().ok.body.json
        #expect(inventory.items.contains { $0.kind == .metric && $0.code == "heart_rate_resting" })
        #expect(inventory.items.contains { $0.kind == .sleep })
        let metrics = try await client.listMetrics().ok.body.json.metrics.map(\.code)
        #expect(metrics.contains("heart_rate_resting") && metrics.contains("vo2max"))
        for code in metrics {
            _ = try await client.getMetric(path: .init(code: code)).ok.body.json
        }
    }

    @Test func `overrides change the day and revoke restores it`() async throws {
        let client = try await signedIn()
        let date = today.adding(days: -1)
        let before = try #require(try await day(client, "heart_rate_resting", date))
        #expect(before.status == .fallback)
        #expect(before.selected == "garmin")
        let garmin = try #require(before.inputs?.first { $0.group == "garmin" }?.recordRefs?.first)

        let window = Components.Schemas.OverrideWindow(kind: .localDay, key: date.description, localDate: date.description)
        let created = try await client.createOverride(body: .json(.init(metric: "heart_rate_resting", window: window, action: .excludeInput, inputId: garmin))).created.body.json
        #expect(try await day(client, "heart_rate_resting", date)?.selected == "apple_watch")

        _ = try await client.createOverride(body: .json(.init(metric: "heart_rate_resting", window: window, action: .setValue, value: 49, unit: "bpm", note: "synthetic note"))).created
        let set = try #require(try await day(client, "heart_rate_resting", date))
        #expect(set.status == .overridden)

        _ = try await client.revokeOverride(path: .init(id: created.id)).ok
        let listed = try await client.listOverrides(query: .init(metric: ["heart_rate_resting"])).ok.body.json.value2.overrides
        #expect(listed.count == 2)
        #expect(listed.first { $0.id == created.id }?.active == false)
    }

    @Test func `summary, trend, source series, coverage and drilldown decode`() async throws {
        let client = try await signedIn()
        let summary = try await client.getResolvedSummary(query: .init(metrics: ["heart_rate_resting", "steps"], compare: true)).ok.body.json
        #expect(summary.metrics.additionalProperties["steps"]?.stats.count == 3)
        #expect(summary.metrics.additionalProperties["steps"]?.comparisons?.count == 4)
        let trend = try await client.getResolvedTrend(query: .init(metric: "steps", startDate: today.adding(days: -3659).description, endDate: today.description, grain: .month)).ok.body.json
        #expect(trend.buckets.contains { $0.n > 0 })
        let start = today.adding(days: -30).start(in: .gmt), end = today.adding(days: 1).start(in: .gmt)
        let series = try await client.getSourceSeries(query: .init(metric: "heart_rate_resting", start: start, end: end)).ok.body.json
        #expect(series.sources.map(\.provider) == ["garmin", "apple_health"])
        let coverage = try await client.getCoverage(query: .init(startDate: today.adding(days: -6).description, endDate: today.description, metric: ["steps"])).ok.body.json
        #expect(coverage.rows.first?.days.count == 7)
        let drill = try await client.getResolvedSources(path: .init(metric: "heart_rate", windowKey: today.description)).ok.body.json
        #expect(drill.sources.contains { $0.ruleStatus == .excluded })
        let page = try await client.listMeasurements(query: .init(startDate: today.description, endDate: today.description, metric: ["heart_rate"], provider: ["garmin"], limit: 100)).ok.body.json
        #expect(page.value1.hasMore)
        #expect(page.value2.measurements.count == 100)
        let trace = try await client.getProvenance(path: .init(entity: .measurement, id: "1220000")).ok.body.json
        #expect(trace.earlier.count == 1)
    }

    @Test func `pins write the dashboard layout`() async throws {
        let client = try await signedIn()
        let layout = try await client.getDashboardLayout().ok.body.json
        #expect(layout.isDefault)
        let cards = layout.cards + [.init(metric: "body_mass", size: .m, hidden: false)]
        let saved = try await client.putDashboardLayout(body: .json(.init(version: ._1, cards: cards))).ok.body.json
        #expect(saved.cards.map(\.metric) == layout.cards.map(\.metric) + ["body_mass"])
        #expect(!saved.isDefault)
    }
}
