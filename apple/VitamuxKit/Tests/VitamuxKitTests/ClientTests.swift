import Foundation
import Testing
@testable import VitamuxKit

/// The generated client with the real middleware against the fake server.
struct ClientTests {
    let fake = FakeServer()
    let sessions = SessionStore.inMemory()
    let expired = Counter()

    var client: Client {
        fake.client(sessions: sessions) { [expired] in expired.increment() }
    }

    func login(password: String = FakeServer.Owner.password, totp: String? = nil, username: String = FakeServer.Owner.username) async throws {
        let body = Components.Schemas.LoginRequest(
            username: username, password: password, totpCode: totp, client: .app, deviceName: "Test iPhone"
        )
        let answer = try await client.login(body: .json(body)).ok.body.json
        guard case .AppSession(let session) = answer else {
            Issue.record("expected an app session, got \(answer)")
            return
        }
        try sessions.save(session, for: fake.profile)
    }

    func failure(_ work: () async throws -> Void) async -> Problem? {
        do {
            try await work()
            return nil
        } catch {
            return Problem(error)
        }
    }

    @Test func `sign-in with TOTP takes the code on the second step`() async throws {
        fake.totpEnabled = true
        let first = await failure { try await login() }
        #expect(first?.status == 401)
        #expect(first?.code == "totp_required")
        #expect(try sessions.load() == nil)

        try await login(totp: FakeServer.Owner.totp)
        let entry = try #require(try sessions.load())
        #expect(entry.server == fake.profile.baseURL)
        #expect(entry.token.hasPrefix("vmx_ses_"))

        let me = try await client.getSession().ok.body.json
        #expect(me.user.username == FakeServer.Owner.username)
        #expect(me.user.totpEnabled)
        #expect(fake.requests.last?.authorization == "Bearer \(entry.token)")
        #expect(fake.requests.first?.authorization == nil, "sign-in never carries a bearer token")
        #expect(expired.value == 0, "a 401 on sign-in is not an expired session")
    }

    @Test func `a problem keeps its field pointers`() async {
        let problem = await failure { try await login(username: "bad name") }
        #expect(problem?.status == 422)
        #expect(problem?.code == "validation_failed")
        #expect(problem?.title == "Validation failed")
        #expect(problem?.requestID == "req-fake")
        #expect(problem?.detail(for: "/username") == "must not contain spaces")
        #expect(problem?.detail(for: "/password") == nil)
    }

    @Test func `a rate limit carries Retry-After`() async {
        for _ in 0..<FakeServer.lockoutAfter {
            let wrong = await failure { try await login(password: "wrong-synthetic") }
            #expect(wrong?.code == "unauthenticated")
            #expect(wrong?.retryAfter == nil)
        }
        let limited = await failure { try await login() }
        #expect(limited?.status == 429)
        #expect(limited?.code == "rate_limited")
        #expect(limited?.retryAfter == .seconds(FakeServer.lockoutSeconds))
    }

    @Test func `an expired session clears the store and reports it`() async throws {
        try await login()
        _ = try await client.getSession().ok
        fake.expireSessions()

        let problem = await failure { _ = try await client.getSession().ok }
        #expect(problem?.status == 401)
        #expect(try sessions.load() == nil)
        #expect(expired.value == 1)

        let again = await failure { _ = try await client.getSession().ok }
        #expect(again?.status == 401)
        #expect(expired.value == 1, "without a session there is nothing left to expire")
    }

    @Test func `cursor paging walks every page once`() async throws {
        try await login()
        var cursor: String?
        var ids: [String] = []
        var pages = 0
        repeat {
            let page = try await client.listMeasurements(query: .init(limit: 2, cursor: cursor)).ok.body.json
            ids += page.value2.measurements.map(\.id)
            cursor = page.value1.hasMore ? page.value1.nextCursor : nil
            pages += 1
        } while cursor != nil
        #expect(pages == 3)
        #expect(ids == (0..<FakeServer.measurementCount).map { "msr_fake_\($0)" })

        let tampered = await failure { _ = try await client.listMeasurements(query: .init(cursor: "forged")).ok }
        #expect(tampered?.detail(for: "/cursor") != nil)
    }

    @Test func `instants decode with and without fractional seconds`() async throws {
        try await login()
        let rows = try await client.listMeasurements(query: .init(limit: 2)).ok.body.json.value2.measurements
        #expect(rows[0].startAt == Date(timeIntervalSince1970: 1_767_250_800))   // 2026-01-01T07:00:00Z
        #expect(rows[1].startAt == Date(timeIntervalSince1970: 1_767_333_600.5)) // 2026-01-02T07:00:00.5+01:00
    }

    @Test func `the token goes only to the server that issued it`() async throws {
        try await login()
        let other = FakeServer()
        _ = await failure { _ = try await other.client(sessions: sessions).getSystemVersion().ok }
        #expect(other.requests.first?.authorization == nil)
        #expect(try sessions.load() != nil, "another server's 401 does not end this session")
    }

    @Test func `sign-out ends the session on the server`() async throws {
        try await login()
        _ = try await client.logout().noContent
        let after = await failure { _ = try await client.getSession().ok }
        #expect(after?.status == 401)
    }

    @Test func `the version handshake answers before sign-in`() async throws {
        let anonymous = try await client.getSystemVersion().ok.body.json
        #expect(anonymous.product == .vitamux)
        #expect(anonymous.apiVersion >= 1)
        #expect(anonymous.version == nil, "the build is for signed-in callers only")
        try await login()
        #expect(try await client.getSystemVersion().ok.body.json.version != nil)

        let old = FakeServer(handshake: .beforeHandshake)
        let before = await failure { _ = try await old.client(sessions: sessions).getSystemVersion().ok }
        #expect(before?.status == 401)
        #expect(before?.code == "unauthenticated")
    }

    @Test func `unstubbed endpoints answer not found`() async throws {
        try await login()
        let problem = await failure { _ = try await client.listRules().ok }
        #expect(problem?.status == 404)
        #expect(problem?.code == "not_found")
    }
}
