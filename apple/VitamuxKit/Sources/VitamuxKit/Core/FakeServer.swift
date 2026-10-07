#if DEBUG
import Foundation
import OpenAPIURLSession
import Synchronization

/// A stateful stand-in for a Vitamux server, the counterpart of `web/e2e/fake-api.ts`: a
/// `URLProtocol` stub answering synthetic JSON per endpoint (`FakeServer+Fixtures.swift`). Unit
/// tests use one per test; the app's debug build switches to `FakeServer.uiTest` under `-uitest`.
/// Each instance has its own host, so tests running in parallel never share state. Unstubbed
/// `/api` paths answer 404 problem+json. Debug builds only.
public final class FakeServer: Sendable {
    /// The synthetic owner: the only credentials the fake accepts.
    public enum Owner {
        public static let username = "owner"
        public static let password = "synthetic-password"
        public static let totp = "123456"
        public static let recovery = "synthetic-recovery-1"
    }

    /// One request the fake answered, for assertions.
    public struct Request: Sendable, Equatable {
        public var method: String
        public var path: String
        public var authorization: String?
    }

    /// How the fake answers the version handshake (`GET /system/version`) the app reads before
    /// sign-in, so the server step's errors can be tested.
    public enum Handshake: Sendable {
        /// The current server: anonymous handshake, then the build for a signed-in caller.
        case current
        /// A Vitamux server from before the handshake: the version route needs a session.
        case beforeHandshake
        /// Something else at the address: every path answers a plain HTML 404.
        case notVitamux
    }

    struct State {
        var handshake = Handshake.current
        var totpEnabled = false
        /// Live app-session tokens and their device names.
        var sessions: [String: String] = [:]
        var failedLogins = 0
        /// Every request fails as if the phone had no network.
        var isOffline = false
        var requests: [Request] = []
    }

    /// Failed sign-ins before the fake answers 429, and the wait it asks for.
    public static let lockoutAfter = 3
    public static let lockoutSeconds = 60

    public let profile: ServerProfile
    public let urlSession: URLSession
    let state = Mutex(State())

    /// `host` defaults to a fresh `*.fake.vitamux.test` name.
    public init(
        host: String = "s\(UInt64.random(in: 0...UInt64.max)).fake.vitamux.test",
        totpEnabled: Bool = false,
        handshake: Handshake = .current
    ) {
        profile = try! ServerProfile("https://\(host)", allowsLocalHTTP: false)
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [FakeURLProtocol.self]
        urlSession = URLSession(configuration: configuration)
        state.withLock {
            $0.totpEnabled = totpEnabled
            $0.handshake = handshake
        }
        FakeURLProtocol.servers.withLock { $0[host] = self }
    }

    /// The server UI tests run against (`-uitest` in the launch arguments).
    public static let uiTest = FakeServer(host: "fake.vitamux.test")

    /// Every address a UI test can type, registered together. Another `*.vitamux.test` host
    /// fails as an unreachable server. All share `uiTest.urlSession`.
    public static let uiTestServers = [
        uiTest,
        FakeServer(host: "old.vitamux.test", handshake: .beforeHandshake),
        FakeServer(host: "other.vitamux.test", handshake: .notVitamux),
    ]

    /// The owner's timezone in every fixture: their local dates, and "today", are this zone's.
    public static let timeZone = TimeZone(identifier: "Europe/Berlin")!

    public static var isUITestRun: Bool {
        ProcessInfo.processInfo.arguments.contains("-uitest")
    }

    /// A generated client for this fake, with the real middleware.
    public func client(sessions: SessionStore, cache: ResponseCache? = nil, onExpired: @escaping @Sendable () -> Void = {}) -> Client {
        Client(
            profile: profile,
            sessions: sessions,
            cache: cache,
            transport: URLSessionTransport(configuration: .init(session: urlSession, httpBodyProcessingMode: .buffered)),
            onExpired: onExpired
        )
    }

    public var totpEnabled: Bool {
        get { state.withLock { $0.totpEnabled } }
        set { state.withLock { $0.totpEnabled = newValue } }
    }

    /// The failure mode: while true, every request fails with "not connected to the internet",
    /// as on a phone without a network (the offline cache's tests).
    public var isOffline: Bool {
        get { state.withLock { $0.isOffline } }
        set { state.withLock { $0.isOffline = newValue } }
    }

    /// Ends every app session, as the server does after the idle or absolute lifetime.
    public func expireSessions() {
        state.withLock { $0.sessions.removeAll() }
    }

    public var requests: [Request] {
        state.withLock { $0.requests }
    }

    /// Answers one request: status, content type and body.
    func respond(to request: URLRequest, body: Data) -> Reply {
        let url = request.url!
        let method = request.httpMethod ?? "GET"
        let authorization = request.value(forHTTPHeaderField: "Authorization")
        return state.withLock { state in
            state.requests.append(Request(method: method, path: url.path, authorization: authorization))
            return Self.route(method: method, url: url, authorization: authorization, body: body, state: &state)
        }
    }
}

/// What the fake answers.
struct Reply {
    var status: Int
    var headers: [String: String] = [:]
    var body: Data?

    static func json(_ status: Int, _ object: [String: Any]) -> Reply {
        Reply(status: status, body: try! JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]))
    }

    /// The server's problem+json, with its registry's titles.
    static func problem(
        _ status: Int, _ code: String, _ detail: String,
        errors: [(String, String)] = [], headers: [String: String] = [:]
    ) -> Reply {
        let titles = [
            "validation_failed": "Validation failed", "unauthenticated": "Authentication required",
            "totp_required": "TOTP code required", "not_found": "Not found", "rate_limited": "Rate limited",
            "conflict": "Conflict", "forbidden": "Forbidden", "unavailable": "Service unavailable",
            "auth_rejected": "Sign-in refused", "consent_required": "Consent required",
            "payload_too_large": "Payload too large",
        ]
        var object: [String: Any] = [
            "type": "urn:vitamux:problem:\(code)", "title": titles[code] ?? code, "status": status,
            "code": code, "detail": detail, "request_id": "req-fake",
        ]
        if !errors.isEmpty { object["errors"] = errors.map { ["pointer": $0.0, "detail": $0.1] } }
        var reply = Reply.json(status, object)
        reply.headers = headers.merging(["Content-Type": "application/problem+json"]) { $1 }
        return reply
    }
}

/// Routes requests to their `FakeServer` by host.
final class FakeURLProtocol: URLProtocol {
    static let servers = Mutex<[String: FakeServer]>([:])

    /// Registered hosts, and any other `.vitamux.test` host, which fails as unreachable.
    override class func canInit(with request: URLRequest) -> Bool {
        guard let host = request.url?.host() else { return false }
        return host.hasSuffix(".vitamux.test") || servers.withLock { $0[host] != nil }
    }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        guard let url = request.url, let host = url.host(),
              let server = Self.servers.withLock({ $0[host] })
        else {
            client?.urlProtocol(self, didFailWithError: URLError(.cannotFindHost))
            return
        }
        guard !server.isOffline else {
            client?.urlProtocol(self, didFailWithError: URLError(.notConnectedToInternet))
            return
        }
        let reply = server.respond(to: request, body: Self.body(of: request))
        var headers = reply.headers
        if reply.body != nil, headers["Content-Type"] == nil { headers["Content-Type"] = "application/json" }
        let response = HTTPURLResponse(url: url, statusCode: reply.status, httpVersion: "HTTP/1.1", headerFields: headers)!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        if let body = reply.body { client?.urlProtocol(self, didLoad: body) }
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}

    /// The request body: inline, or read from the stream an upload task hands the protocol.
    private static func body(of request: URLRequest) -> Data {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return Data() }
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 16 * 1024)
        // Read to the end: `hasBytesAvailable` can be false before a streamed body arrives.
        while true {
            let count = stream.read(&buffer, maxLength: buffer.count)
            guard count > 0 else { break }
            data.append(buffer, count: count)
        }
        return data
    }
}
#endif
