import Foundation
import Testing
@testable import VitamuxKit

struct ServerProfileTests {
    @Test(arguments: [
        ("vitamux.example.org", "https://vitamux.example.org"),
        ("  https://Vitamux.Example.org/  ", "https://vitamux.example.org"),
        ("https://example.org:8443/vitamux/", "https://example.org:8443/vitamux"),
    ])
    func `accepts https addresses`(input: String, expected: String) throws {
        #expect(try ServerProfile(input, allowsLocalHTTP: false).baseURL.absoluteString == expected)
    }

    @Test(arguments: ["http://localhost:8080", "http://vitamux.local", "http://192.168.1.20", "http://10.0.0.5", "http://172.20.0.2", "http://127.0.0.1:8080"])
    func `a debug build takes http on local addresses`(input: String) throws {
        #expect(try ServerProfile(input, allowsLocalHTTP: true).baseURL.scheme == "http")
        #expect(throws: Problem.self) { try ServerProfile(input, allowsLocalHTTP: false) }
    }

    @Test(arguments: ["", "http://vitamux.example.org", "http://172.32.0.1", "ftp://example.org", "https://owner:pw@example.org", "https://example.org/?x=1", "https://"])
    func `refuses anything else`(input: String) {
        #expect(throws: Problem.self) { try ServerProfile(input, allowsLocalHTTP: true) }
    }

    @Test func `the refusal says why`() {
        #expect {
            try ServerProfile("http://vitamux.example.org", allowsLocalHTTP: true)
        } throws: { error in
            (error as? Problem)?.detail == "The address must start with https://."
        }
    }
}

struct SessionStoreTests {
    let entry = SessionStore.Entry(
        server: URL(string: "https://a.example.org")!, token: "vmx_ses_synthetic", expiresAt: .distantFuture
    )

    @Test func `copies of an in-memory store share the session`() throws {
        let store = SessionStore.inMemory()
        let copy = store
        try store.save(entry)
        #expect(try copy.load() == entry)
        #expect(copy.token(for: entry.server) == "vmx_ses_synthetic")
        #expect(copy.token(for: URL(string: "https://b.example.org")!) == nil)
        try copy.clear()
        #expect(try store.load() == nil)
    }

    /// `swift test` may run without a usable login keychain (CI); the round trip then is skipped.
    static let keychainAvailable: Bool = {
        let probe = SessionStore.keychain(service: "org.vitamux.tests.probe.\(UUID().uuidString)")
        defer { try? probe.clear() }
        return (try? probe.save(.init(server: URL(string: "https://probe.example.org")!, token: "probe", expiresAt: .now))) != nil
    }()

    @Test(.enabled(if: keychainAvailable, "needs a usable keychain"))
    func `the keychain round-trips and clears`() throws {
        let store = SessionStore.keychain(service: "org.vitamux.tests.\(UUID().uuidString)")
        defer { try? store.clear() }
        #expect(try store.load() == nil)
        try store.save(entry)
        #expect(try store.load() == entry)
        try store.clear()
        #expect(try store.load() == nil)
    }
}

struct PollerTests {
    @Test func `stops when the work is done`() async {
        let ticks = Counter()
        await Poller(interval: .milliseconds(1)).run {
            ticks.increment() < 3
        }
        #expect(ticks.value == 3)
    }

    @Test func `stops when the screen goes away`() async {
        let ticks = Counter()
        let screen = Task {
            await Poller(interval: .milliseconds(1)).run {
                ticks.increment()
                return true
            }
        }
        while ticks.value < 2 { await Task.yield() }
        screen.cancel()
        await screen.value
        let stopped = ticks.value
        try? await Task.sleep(for: .milliseconds(20))
        #expect(ticks.value == stopped)
    }

    @Test func `uses the panel's intervals`() {
        #expect(Poller.backfills.interval == .seconds(5))
        #expect(Poller.extraction.interval == .seconds(2))
        #expect(Poller.export.interval == .seconds(1))
    }
}

struct LocalDateTests {
    let berlin = TimeZone(identifier: "Europe/Berlin")!

    @Test func `parses and prints the API's dates`() throws {
        let date = try #require(LocalDate("2026-02-28"))
        #expect(date.description == "2026-02-28")
        #expect(date.adding(days: 1).description == "2026-03-01")
        #expect(date.adding(days: -59).description == "2025-12-31")
        #expect(LocalDate("2026-02-30") == nil)
        #expect(LocalDate("2026-2-3") == nil)
        #expect(LocalDate("2026-02-28T00:00:00Z") == nil)
        #expect(LocalDate("2026-01-31")! < date)
    }

    @Test func `a local date belongs to the owner's timezone, not UTC`() {
        let lateEvening = Date(timeIntervalSince1970: 1_767_310_200) // 2026-01-01T23:30:00Z
        #expect(LocalDate(lateEvening, in: .gmt).description == "2026-01-01")
        #expect(LocalDate(lateEvening, in: berlin).description == "2026-01-02")
        #expect(LocalDate("2026-01-02")!.start(in: berlin) == Date(timeIntervalSince1970: 1_767_308_400)) // 2026-01-01T23:00:00Z
    }

    @Test func `the timezone comes from the period that contains the instant`() async throws {
        let fake = FakeServer()
        let sessions = SessionStore.inMemory()
        let client = fake.client(sessions: sessions)
        let answer = try await client.login(body: .json(.init(
            username: FakeServer.Owner.username, password: FakeServer.Owner.password, client: .app, deviceName: "Test"
        ))).ok.body.json
        guard case .AppSession(let session) = answer else { Issue.record("expected an app session"); return }
        try sessions.save(session, for: fake.profile)

        let periods = try await client.listTimezonePeriods().ok.body.json.timezonePeriods
        #expect(periods.timeZone(at: Date(timeIntervalSince1970: 1_767_222_000))?.identifier == "America/New_York")
        #expect(periods.timeZone(at: Date(timeIntervalSince1970: 1_790_000_000))?.identifier == "Europe/Berlin")
        #expect(periods.timeZone(at: Date(timeIntervalSince1970: 0)) == nil)
    }

    @Test func `RFC 3339 instants with an offset, with or without a fraction`() throws {
        let transcoder = RFC3339DateTranscoder()
        #expect(try transcoder.decode("2026-01-01T08:00:00+01:00") == Date(timeIntervalSince1970: 1_767_250_800))
        #expect(try transcoder.decode("2026-01-01T07:00:00.250Z") == Date(timeIntervalSince1970: 1_767_250_800.25))
        #expect(throws: (any Error).self) { try transcoder.decode("2026-01-01") }
        #expect(try transcoder.decode(transcoder.encode(Date(timeIntervalSince1970: 1_767_250_800.5))) == Date(timeIntervalSince1970: 1_767_250_800.5))
    }
}

struct ProblemTests {
    @Test func `a client error unwraps to the middleware's problem`() async throws {
        let fake = FakeServer()
        do {
            _ = try await fake.client(sessions: .inMemory()).getSession().ok
            Issue.record("expected a failure")
        } catch {
            #expect(!(error is Problem), "the generated client wraps it")
            #expect(Problem(error).code == "unauthenticated")
            let state: Loadable<Int> = await Loadable { throw error }
            #expect(state == .failed(Problem(error)))
        }
    }

    @Test func `no response is a reachability problem`() {
        #expect(Problem(URLError(.notConnectedToInternet)).title == "Can't reach the server")
    }
}
