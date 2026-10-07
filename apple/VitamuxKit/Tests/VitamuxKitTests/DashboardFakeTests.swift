import Foundation
import Testing
@testable import VitamuxKit

/// The fake's dashboard endpoints (J22.7) decode through the generated client, so UI tests on
/// them exercise the real shapes.
struct DashboardFakeTests {
    let fake = FakeServer()
    let sessions = SessionStore.inMemory()

    var client: Client { fake.client(sessions: sessions) }

    func login() async throws {
        let body = Components.Schemas.LoginRequest(
            username: FakeServer.Owner.username, password: FakeServer.Owner.password, client: .app, deviceName: "Test iPhone"
        )
        guard case .AppSession(let session) = try await client.login(body: .json(body)).ok.body.json else {
            Issue.record("expected an app session")
            return
        }
        try sessions.save(session, for: fake.profile)
    }

    @Test func `the default layout saves and reads back`() async throws {
        try await login()
        let layout = try await client.getDashboardLayout().ok.body.json
        #expect(layout.isDefault)
        #expect(layout.cards.map(\.metric) == FakeServer.defaultDashboard.map(\.metric))
        let cards = [Components.Schemas.DashboardCard(metric: "steps", size: .l, hidden: false)]
        let saved = try await client.putDashboardLayout(body: .json(.init(version: ._1, cards: cards, hero: layout.hero, dismissed: [])))
            .ok.body.json
        #expect(!saved.isDefault)
        #expect(try await client.getDashboardLayout().ok.body.json.cards == cards)
        #expect(fake.storedDashboard == [.init("steps", "L")])
    }

    @Test func `summaries cover families, gaps and a past day`() async throws {
        try await login()
        let metrics = ["sleep", "blood_pressure", "steps", "hrv_sdnn"]
        let today = try await client.getResolvedSummary(query: .init(metrics: metrics)).ok.body.json
        #expect(today.timezone == "Europe/Berlin")
        let summaries = today.metrics.additionalProperties
        #expect(summaries.count == 4)
        #expect(summaries["steps"]?.value.partial == true)
        #expect(summaries["hrv_sdnn"]?.value.status == .noData)
        #expect(summaries["sleep"]?.stats[1].components?.additionalProperties["sleep_total"]?.mean != nil)
        #expect(summaries["blood_pressure"]?.sparkline.count == 30)

        let past = LocalDate(today.date)!.adding(days: -3).description
        let earlier = try await client.getResolvedSummary(query: .init(metrics: ["steps"], date: past)).ok.body.json
        #expect(earlier.date == past)
        #expect(earlier.metrics.additionalProperties["steps"]?.value.partial == nil)
        #expect(fake.summaryDates == [nil, past])
    }

    @Test func `the alert sources and an empty install`() async throws {
        try await login()
        let jobs = try await client.listJobs(query: .init(status: "dead")).ok.body.json
        #expect(jobs.value2.jobs.count == 2)
        #expect(try await client.getSystemStatus().ok.body.json.lastBackupAt != nil)
        #expect(try await client.listConnections().ok.body.json.connections.count == 2)
        let catalogue = try await client.listMetrics().ok.body.json.metrics
        #expect(catalogue.contains { $0.code == "bp_systolic" && $0.family == "blood_pressure" })

        fake.dashboardInstall = .empty
        #expect(try await client.listJobs(query: .init(status: "dead")).ok.body.json.value2.jobs.isEmpty)
        #expect(try await client.getSystemStatus().ok.body.json.lastBackupAt == nil)
        #expect(try await client.listConnections().ok.body.json.connections.isEmpty)
        let steps = try await client.getResolvedSummary(query: .init(metrics: ["steps"])).ok.body.json
        #expect(steps.metrics.additionalProperties["steps"]?.value.status == .noData)
    }

    @Test func `a layout the panel saved keeps its dismissed alerts`() async throws {
        fake.dashboardInstall = .panelLayout
        try await login()
        let layout = try await client.getDashboardLayout().ok.body.json
        #expect(!layout.isDefault)
        #expect(layout.cards.map(\.metric) == FakeServer.panelLayout.map(\.metric))
        #expect(layout.dismissed == ["backup:" + FakeServer.backupAt()])
    }
}
