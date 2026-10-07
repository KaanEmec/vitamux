import Foundation
import Testing
@testable import VitamuxKit

/// The fake server's specialised-view endpoints (FakeServer+Specialised.swift) decode through the
/// generated client and page by cursor, so the app's UI tests stand on them.
struct SpecialisedFakeTests {
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

    var today: LocalDate { LocalDate.today(in: SpecialisedFixture.zone) }

    @Test func `last night has three sources and the night before a nap`() async throws {
        let client = try await signedIn()
        let start = today.adding(days: -29).description, end = today.description
        let resolved = try await client.getResolvedSleep(query: .init(startDate: start, endDate: end)).ok.body.json
        let last = try #require(resolved.nights.last)
        #expect(last.localDate == end)
        #expect(last.members.map(\.ruleStatus) == [.used, .used, .excluded])
        #expect(last.members.filter(\.selected).count == 1)
        let gap = try #require(resolved.nights.first { $0.localDate == today.adding(days: -SpecialisedFixture.gapNight).description })
        #expect(gap.result.status == .noData && gap.members.isEmpty)

        let yesterday = today.adding(days: -1).description
        let page = try await client.listSleep(query: .init(startDate: yesterday, endDate: yesterday, include: [.stages], limit: 500)).ok.body.json
        #expect(page.value2.sleep.contains { $0.isNap })
        #expect(page.value2.sleep.filter { !$0.isNap }.allSatisfy { !($0.stages ?? []).isEmpty })
    }

    @Test func `paged lists walk the cursor to the end`() async throws {
        let client = try await signedIn()
        var query = Operations.ListBloodPressure.Input.Query(limit: 500)
        var readings: [Components.Schemas.BloodPressureReading] = []
        var pages = 0
        repeat {
            let page = try await client.listBloodPressure(query: query).ok.body.json
            readings += page.value2.readings
            pages += 1
            query.cursor = page.value1.hasMore ? page.value1.nextCursor : nil
        } while query.cursor != nil
        #expect(pages > 1, "the fake pages at \(SpecialisedFixture.pageSize) rows")
        #expect(readings.count == 90 + 1 + 18)
        #expect(readings.map(\.measuredAt) == readings.map(\.measuredAt).sorted())
        #expect(Set(readings.map(\.id)).count == readings.count)
    }

    @Test func `weigh-ins, workouts, events and lab results decode`() async throws {
        let client = try await signedIn()
        let start = today.adding(days: -29).description, end = today.description
        let groups = try await client.listGroups(query: .init(kind: .bodyComposition, startDate: start, endDate: end, limit: 500)).ok.body.json
        #expect(groups.value2.groups.contains { $0.source.provider == "apple_health" })
        let workouts = try await client.getResolvedWorkouts(query: .init(startDate: end, endDate: end)).ok.body.json
        #expect(workouts.workouts.first?.members.count == 3)
        let events = try await client.listEvents(query: .init(startDate: start, endDate: end, code: ["irregular_rhythm"], limit: 500)).ok.body.json
        #expect(!events.value2.events.isEmpty && events.value2.events.allSatisfy { $0.code == "irregular_rhythm" })
        let lab = try await client.listLabResults(query: .init(limit: 500)).ok.body.json
        #expect(lab.value2.labResults.contains { $0.analyte == nil && $0.originalLabel == "Synthetic marker" })
        let empty = try await client.listGroups(query: .init(kind: .bodyComposition, startDate: "2001-01-01", endDate: "2001-01-31")).ok.body.json
        #expect(empty.value2.groups.isEmpty && !empty.value1.hasMore)
    }
}
