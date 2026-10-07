import Foundation
import Testing
@testable import VitamuxKit

/// The offline read cache (J22.19) against the fake server's failure mode. Each test has its own
/// fake and its own temporary directory.
struct ResponseCacheTests {
    let fake = FakeServer()
    let sessions = SessionStore.inMemory()
    let directory = URL.temporaryDirectory.appending(path: "ResponseCacheTests-\(UUID().uuidString)", directoryHint: .isDirectory)

    func client(_ cache: ResponseCache) -> Client {
        fake.client(sessions: sessions, cache: cache)
    }

    func login(_ client: Client) async throws {
        let body = Components.Schemas.LoginRequest(
            username: FakeServer.Owner.username, password: FakeServer.Owner.password, client: .app, deviceName: "Test iPhone"
        )
        guard case .AppSession(let session) = try await client.login(body: .json(body)).ok.body.json else {
            Issue.record("expected an app session")
            return
        }
        try sessions.save(session, for: fake.profile)
    }

    var fileCount: Int {
        (try? FileManager.default.contentsOfDirectory(atPath: directory.path).count) ?? 0
    }

    @Test func `an offline launch shows the cached dashboard, marked`() async throws {
        let first = ResponseCache(directory: directory)
        try await login(client(first))
        let layout = try await client(first).getDashboardLayout().ok.body.json
        let summary = try await client(first).getResolvedSummary(query: .init(metrics: ["steps", "sleep"])).ok.body.json
        #expect(first.status == .online)

        // A new launch with no network: the same session and directory.
        fake.isOffline = true
        let relaunched = ResponseCache(directory: directory)
        let changes = Counter()
        relaunched.onStatusChange { _ in changes.increment() }
        #expect(try await client(relaunched).getDashboardLayout().ok.body.json == layout)
        #expect(try await client(relaunched).getResolvedSummary(query: .init(metrics: ["steps", "sleep"])).ok.body.json == summary)
        guard case .offline(let from?) = relaunched.status else {
            Issue.record("expected offline with a time, got \(relaunched.status)")
            return
        }
        #expect(from <= .now)
        #expect(changes.value == 1)

        // Pull to refresh asks the network only, and fails.
        await #expect(throws: (any Error).self) {
            try await ResponseCache.refreshing { try await client(relaunched).getDashboardLayout().ok.body.json }
        }
        // A request never seen fails as before.
        await #expect(throws: (any Error).self) {
            try await client(relaunched).getResolvedSummary(query: .init(metrics: ["weight"])).ok
        }

        fake.isOffline = false
        _ = try await client(relaunched).getDashboardLayout().ok
        #expect(relaunched.status == .online)

        let values = try directory.resourceValues(forKeys: [.isExcludedFromBackupKey])
        #expect(values.isExcludedFromBackup == true)
    }

    @Test func `the cached answer carries its stored time`() async throws {
        let cache = ResponseCache(directory: directory)
        try await login(client(cache))
        _ = try await client(cache).listConnections().ok
        let key = try #require(try FileManager.default.contentsOfDirectory(atPath: directory.path).first)
        let entry = try #require(cache.entry(for: key))
        #expect(entry.contentType == "application/json")
        #expect(abs(entry.storedAt.timeIntervalSinceNow) < 60)
    }

    @Test func `nothing outside the allow-list is cached`() async throws {
        let cache = ResponseCache(directory: directory)
        let client = client(cache)
        try await login(client)
        _ = try await client.getSession().ok
        _ = try await client.listSessions().ok
        _ = try await client.getSystemVersion().ok
        _ = try await client.listTimezonePeriods().ok
        #expect(fileCount == 0)
        _ = try await client.getDashboardLayout().ok
        #expect(fileCount == 1)

        fake.isOffline = true
        await #expect(throws: (any Error).self) { try await client.getSession().ok }
        #expect(cache.status == .offline(showingDataFrom: nil))
    }

    @Test func `a signed-out request is never cached`() async throws {
        let cache = ResponseCache(directory: directory)
        await #expect(throws: (any Error).self) { try await client(cache).getDashboardLayout().ok }
        #expect(fileCount == 0)
    }

    @Test func `eviction keeps the most recently used under the cap`() throws {
        let cache = ResponseCache(directory: directory, capacity: 1_000)
        let body = Data(repeating: 7, count: 300)
        cache.store(body, contentType: "application/json", key: "a")
        cache.store(body, contentType: "application/json", key: "b")
        #expect(cache.entry(for: "a")?.body == body) // a is now the most recent
        cache.store(body, contentType: "application/json", key: "c")
        #expect(cache.entry(for: "b") == nil, "the least recently used went first")
        #expect(cache.entry(for: "a") != nil)
        #expect(cache.entry(for: "c") != nil)
        #expect(cache.size <= 1_000)
        #expect(ResponseCache.capacity == 100 * 1024 * 1024)
    }

    @Test func `an answer over a quarter of the cap is passed on, not kept`() async throws {
        let cache = ResponseCache(directory: directory, capacity: 1_000)
        let client = client(cache)
        try await login(client)
        let layout = try await client.getDashboardLayout().ok.body.json
        #expect(!layout.cards.isEmpty)
        #expect(fileCount == 0)
    }

    @Test func `sign-out empties it`() async throws {
        let cache = ResponseCache(directory: directory)
        let client = client(cache)
        try await login(client)
        _ = try await client.getDashboardLayout().ok
        _ = try await client.listConnections().ok
        #expect(cache.size > 0)
        _ = try await client.logout().noContent
        #expect(cache.size == 0)
        #expect(fileCount == 0)
    }

    @Test func `a 401 empties it`() async throws {
        let cache = ResponseCache(directory: directory)
        let client = client(cache)
        try await login(client)
        _ = try await client.getDashboardLayout().ok
        #expect(cache.size > 0)
        fake.expireSessions()
        await #expect(throws: (any Error).self) { try await client.getDashboardLayout().ok }
        #expect(cache.size == 0)
    }

    @Test func `keys separate servers, sessions and requests`() {
        let server = URL(string: "https://a.example")!
        let key = ResponseCache.key(server: server, authorization: "Bearer one", path: "/api/v1/inventory")
        #expect(key != ResponseCache.key(server: URL(string: "https://b.example")!, authorization: "Bearer one", path: "/api/v1/inventory"))
        #expect(key != ResponseCache.key(server: server, authorization: "Bearer two", path: "/api/v1/inventory"))
        #expect(key != ResponseCache.key(server: server, authorization: "Bearer one", path: "/api/v1/inventory?x=1"))
        #expect(!key.contains("one"))
    }
}
