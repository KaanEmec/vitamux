import Foundation
import Testing
@testable import VitamuxKit

/// The fake server's Apple Watch endpoints (FakeServer+Watch.swift) decode through the generated
/// client and page by cursor, so the app's Watch UI tests stand on them.
struct WatchFakeTests {
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

    /// Every page, following the cursor.
    func readAll<Item>(_ page: (String?) async throws -> ([Item], String?)) async throws -> [Item] {
        var items: [Item] = [], cursor: String?
        for _ in 0..<50 {
            let (more, next) = try await page(cursor)
            items += more
            guard let next else { break }
            cursor = next
        }
        return items
    }

    @Test func `ECG recordings list, and a waveform is 30 seconds at 512 Hz or absent`() async throws {
        let client = try await signedIn()
        let start = today.adding(days: -29).description, end = today.description
        let page = try await client.listEvents(query: .init(startDate: start, endDate: end, code: ["ecg_recording"], limit: 500)).ok.body.json
        let events = page.value2.events
        #expect(events.count == 3 && events.allSatisfy { $0.code == "ecg_recording" })
        let latest = try #require(events.last)
        #expect(latest.level == "sinus_rhythm" && latest.fileSha256 != nil)
        let waveform = try await client.getEventWaveform(path: .init(id: latest.id)).ok.body.json
        #expect(waveform.values.count == 15_360 && waveform.samplingFrequencyHz == 512 && waveform.unit == "µV")
        let poor = try #require(events.first { $0.level == "inconclusive_poor_reading" })
        #expect(poor.fileSha256 == nil)
        do {
            _ = try await client.getEventWaveform(path: .init(id: poor.id)).ok
            Issue.record("the poor reading has no waveform")
        } catch {
            #expect(Problem(error).status == 404)
        }
    }

    @Test func `the unfiltered events list is the events fixture's`() async throws {
        let client = try await signedIn()
        let start = today.adding(days: -29).description, end = today.description
        let page = try await client.listEvents(query: .init(startDate: start, endDate: end, limit: 500)).ok.body.json
        #expect(!page.value2.events.contains { WatchFixture.eventCodes.contains($0.code) })
    }

    @Test func `beats page by cursor as two series a day, none on the gap day`() async throws {
        let client = try await signedIn()
        let day = today.adding(days: -1).description
        let beats = try await readAll { cursor in
            let page = try await client.listMeasurements(query: .init(startDate: day, endDate: day, metric: ["rr_interval"], limit: 500, cursor: cursor)).ok.body.json
            return (page.value2.measurements, page.value1.hasMore ? page.value1.nextCursor : nil)
        }
        #expect(beats.count == 2 * (WatchFixture.beatsPerSeries - 1))
        let series = Set(beats.compactMap { $0.source.externalId?.split(separator: "#").first })
        #expect(series.count == 2)
        #expect(beats.allSatisfy { (0.2...3).contains($0.value) && $0.unit == "s" })
        let gap = today.adding(days: -WatchFixture.beatsGap).description
        let none = try await client.listMeasurements(query: .init(startDate: gap, endDate: gap, metric: ["rr_interval"])).ok.body.json
        #expect(none.value2.measurements.isEmpty)
    }

    @Test func `activity summaries carry Apple's goals`() async throws {
        let client = try await signedIn()
        let start = today.adding(days: -6).description, end = today.description
        let rows = try await readAll { cursor in
            let page = try await client.listMeasurements(query: .init(startDate: start, endDate: end, metric: ["active_energy", "exercise_time", "stand_hours"],
                                                                      kind: [.dailyValue], limit: 500, cursor: cursor)).ok.body.json
            return (page.value2.measurements, page.value1.hasMore ? page.value1.nextCursor : nil)
        }
        #expect(rows.count == 21)
        #expect(rows.allSatisfy { $0.context?.value["goal"] != nil })
        let paused = today.adding(days: -WatchFixture.pausedDay).description
        #expect(rows.filter { $0.localDate == paused }.allSatisfy { $0.context?.value["paused"] as? Bool == true })
    }

    @Test func `a workout has its segments and, for the Apple Health run, a route at sea`() async throws {
        let client = try await signedIn()
        let resolved = try await client.getResolvedWorkouts(query: .init(startDate: today.description, endDate: today.description)).ok.body.json
        let members = resolved.workouts.flatMap(\.members)
        let run = try #require(members.first { $0.provider == "apple_health" })
        let workout = try await client.getWorkout(path: .init(id: run.id)).ok.body.json
        let kinds = (workout.segments ?? []).map(\.kind)
        #expect(kinds.contains(.lap) && kinds.contains(.pause) && kinds.contains(.marker))
        let route = try await client.getWorkoutRoute(path: .init(id: run.id)).ok.body.json
        #expect(route.count == WatchFixture.routePoints && route.latitude.count == route.count)
        #expect(route.latitude.allSatisfy { abs($0) < 0.1 } && route.longitude.allSatisfy { abs($0) < 0.1 })

        let ride = try #require(members.first { $0.sport == "cycling" })
        let multisport = try await client.getWorkout(path: .init(id: ride.id)).ok.body.json
        #expect((multisport.segments ?? []).filter { $0.kind == .activity }.count == 2)
        do {
            _ = try await client.getWorkoutRoute(path: .init(id: ride.id)).ok
            Issue.record("the ride has no route")
        } catch {
            #expect(Problem(error).status == 404)
        }
    }

    @Test func `the inventory and catalogue gain the Watch codes`() async throws {
        let client = try await signedIn()
        let inventory = try await client.getInventory().ok.body.json
        #expect(inventory.items.contains { $0.code == "resting_heart_rate" })
        #expect(inventory.items.contains { $0.kind == .event && $0.code == "ecg_recording" })
        let rr = try #require(inventory.items.first { $0.code == "rr_interval" })
        #expect(rr.metric?.unresolved == true && rr.devices.contains { $0._type == "watch" })
        let metric = try await client.getMetric(path: .init(code: "stand_hours")).ok.body.json
        #expect(metric.aggregation == .additive)
        let metrics = try await client.listMetrics().ok.body.json.metrics
        #expect(metrics.contains { $0.code == "rr_interval" } && metrics.contains { $0.code == "heart_rate" })
    }
}
