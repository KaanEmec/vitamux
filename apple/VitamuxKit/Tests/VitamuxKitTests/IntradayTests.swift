import Foundation
import OpenAPIRuntime
import Testing
@testable import VitamuxKit

/// The Day view's ladder (DayLadder, the twin of web/src/lib/explore/intraday.ts).
struct DayLadderTests {
    let heartRate = DayLadder(.init(_default: ._1m, finest: .raw))
    let hrv = DayLadder(.init(_default: ._5m, finest: .raw))
    let steps = DayLadder(.init(_default: ._30m, finest: ._1m))

    @Test func `each default walks down to its finest step`() {
        #expect(heartRate.rungs.map(\.step) == [.m1, .s30, .raw])
        #expect(hrv.rungs.map(\.step) == [.m5, .m1, .raw])
        #expect(steps.rungs.map(\.step) == [.m30, .m15, .m5, .m1])
        #expect(DayLadder(.init(_default: ._1m, finest: ._1m)).rungs.map(\.step) == [.m1])
        #expect(heartRate.finestBucket == .s30)
        #expect(steps.finestBucket == .m1)
    }

    @Test(arguments: [
        (86_400.0, IntradayStep.m1, IntradayStep.m30), (6 * 3_600, .m1, .m15), (3_600, .s30, .m5), (900, .raw, .m1), (600, .raw, .m1),
    ])
    func `the visible span picks the step`(span: TimeInterval, heart: IntradayStep, additive: IntradayStep) {
        #expect(heartRate.step(forSpan: span) == heart)
        #expect(steps.step(forSpan: span) == additive)
    }

    @Test func `a source is never drawn finer than its spacing`() {
        #expect(DayLadder.sourceStep(.m1, spacing: 6) == .m1)
        #expect(DayLadder.sourceStep(.m1, spacing: 120) == .m5)
        #expect(DayLadder.sourceStep(.m1, spacing: 900) == .m15)
        #expect(DayLadder.sourceStep(.m1, spacing: 7_200) == .raw)
        #expect(DayLadder.sourceStep(.raw, spacing: 900) == .raw)
        #expect(DayLadder.sourceStep(.m30, spacing: nil) == .m30)
    }

    @Test func `raw resolves at the finest bucket the densest source allows`() {
        #expect(heartRate.resolvedBucket(for: .raw, spacings: [6, 120]) == .s30)
        #expect(heartRate.resolvedBucket(for: .s30, spacings: [120]) == .m5)
        #expect(steps.resolvedBucket(for: .m1, spacings: []) == .m1)
    }

    @Test func `a zoomed span loads whole buckets with a margin, inside the day`() {
        let day = ChartSamples.dayStart ... ChartSamples.dayStart.addingTimeInterval(86_400)
        let at = { (seconds: TimeInterval) in ChartSamples.dayStart.addingTimeInterval(seconds) }
        #expect(DayLadder.loadSpan(.s30, visible: at(3_630) ... at(3_690), day: day) == at(3_540) ... at(3_780))
        #expect(DayLadder.loadSpan(.m15, visible: at(0) ... at(1_000), day: day) == at(0) ... at(2_700))
        #expect(DayLadder.loadSpan(.raw, visible: at(86_000) ... at(86_400), day: day) == at(85_920) ... at(86_400))
    }

    @Test func `the 1D preset steps one day`() {
        let end = LocalDate("2026-10-04")!
        #expect(ChartRange.day.start(endingOn: end) == end)
        #expect(!ChartRange.periods.contains(.day))
        #expect(ChartRange.allCases.first == .day)
    }

    @Test func `overlays clip to the domain and lines outside it drop`() {
        let at = { (h: Double) in ChartSamples.dayStart.addingTimeInterval(h * 3_600) }
        let clipped = ChartOverlay.clipped([
            ChartOverlay(label: "Night", start: at(-2), end: at(6.5), style: .shade),
            ChartOverlay(label: "Run", start: at(7), end: at(7.7), style: .tint),
            ChartOverlay(label: "Now", start: at(30), style: .line),
            ChartOverlay(label: "Later", start: at(25), end: at(26), style: .tint),
        ], to: at(0) ... at(24))
        #expect(clipped.map(\.label) == ["Night", "Run"])
        #expect(clipped[0].start == at(0))
    }
}

/// The fake server's intraday answers (FakeServer+Intraday.swift) decode through the generated
/// client and keep their promises: a 6-second heart-rate day, steps that sum to the day.
struct IntradayFakeTests {
    let fake = FakeServer()
    let sessions = SessionStore.inMemory()
    let zone = ExploreFixture.zone
    var yesterday: LocalDate { LocalDate.today(in: zone).adding(days: -1) }

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

    var day: (Date, Date) { (yesterday.start(in: zone), yesterday.adding(days: 1).start(in: zone)) }

    @Test func `the catalogue carries intraday for the Day view codes only`() async throws {
        let client = try await signedIn()
        #expect(try await client.getMetric(path: .init(code: "heart_rate")).ok.body.json.intraday == .init(_default: ._1m, finest: .raw))
        #expect(try await client.getMetric(path: .init(code: "steps")).ok.body.json.intraday == .init(_default: ._30m, finest: ._1m))
        #expect(try await client.getMetric(path: .init(code: "heart_rate_resting")).ok.body.json.intraday == nil)
        let listed = try await client.listMetrics().ok.body.json.metrics
        #expect(listed.filter { $0.intraday != nil }.map(\.code).sorted() == ["heart_rate", "rr_interval", "spo2", "stand_hours", "steps"])
    }

    @Test func `a heart-rate day has 14,400 raw rows from the watch, paged`() async throws {
        let client = try await signedIn()
        let (start, end) = day
        var watch = 0
        var cursor: String?
        var pages = 0
        repeat {
            let page = try await client.getSourceSeries(query: .init(metric: "heart_rate", start: start, end: end, grain: .raw, cursor: cursor)).ok.body.json
            #expect(page.sources.reduce(0) { $0 + $1.points.count } <= 2_000)
            watch += page.sources.first { $0.provider == "apple_health" }?.points.count ?? 0
            cursor = page.hasMore == true ? page.nextCursor : nil
            pages += 1
        } while cursor != nil && pages < 20
        // 14,400 rows less the 12:30–13:00 gap (300 rows at 6 s).
        #expect(watch == 14_400 - 300)
        let minute = try await client.getSourceSeries(query: .init(metric: "heart_rate", start: start, end: end, grain: ._1m)).ok.body.json
        #expect(minute.sources.map(\.spacingS) == [6, 120])
        #expect(minute.sources[0].points.count == 1_440 - 30)
    }

    @Test func `resolved one-minute buckets carry the band and their sources`() async throws {
        let client = try await signedIn()
        let (start, end) = day
        let series = try await client.getResolvedSeries(query: .init(metric: "heart_rate", start: start, end: end, window: "1m", limit: 3_000)).ok.body.json
        #expect(series.points.count == 1_440)
        #expect(series.hasMore == false)
        let point = try #require(series.points.first { $0.status == .calculated })
        #expect(point.sources == ["apple_watch", "garmin"])
        let low = try #require(point.min), high = try #require(point.max)
        #expect(low <= high)
        #expect(point.n ?? 0 > 1)
        // The gap leaves the watch out: Garmin alone, every other minute.
        #expect(series.points.contains { $0.sources == ["garmin"] })
        // 30-second buckets span at most a day.
        await #expect(throws: (any Error).self) {
            _ = try await client.getResolvedSeries(query: .init(metric: "heart_rate", start: start, end: end.addingTimeInterval(86_400), window: "30s"))
        }
    }

    @Test func `steps buckets sum to the day's resolved total`() async throws {
        let client = try await signedIn()
        let (start, end) = day
        let series = try await client.getResolvedSeries(query: .init(metric: "steps", start: start, end: end, window: "30m")).ok.body.json
        #expect(series.points.count == 48)
        let sum = series.points.compactMap { number($0.value) }.reduce(0, +)
        let daily = try await client.getResolvedDaily(query: .init(startDate: yesterday.description, endDate: yesterday.description, metrics: ["steps"])).ok.body.json
        let total = try #require(number(daily.days.first?.metrics.additionalProperties["steps"]?.value))
        #expect(abs(sum - total) <= 1)
        let garmin = try await client.getSourceSeries(query: .init(metric: "steps", start: start, end: end, grain: ._5m)).ok.body.json.sources[1]
        #expect(garmin.spacingS == 900)
    }

    @Test func `blood oxygen is read at night only`() async throws {
        let client = try await signedIn()
        let (start, end) = day
        let rows = try await client.getSourceSeries(query: .init(metric: "spo2", start: start, end: end, grain: .raw)).ok.body.json.sources[0].points
        #expect(!rows.isEmpty)
        // Last night's episode until the morning, tonight's from the late evening.
        let daytime = start.addingTimeInterval(10 * 3_600) ..< start.addingTimeInterval(22 * 3_600)
        #expect(rows.allSatisfy { !daytime.contains($0.start ?? daytime.lowerBound) })
    }

    private func number(_ value: OpenAPIValueContainer?) -> Double? {
        switch value?.value {
        case let v as Double: v
        case let v as Int: Double(v)
        default: nil
        }
    }
}
