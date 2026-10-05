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

    struct State {
        var totpEnabled = false
        /// Live app-session tokens and their device names.
        var sessions: [String: String] = [:]
        var failedLogins = 0
        var requests: [Request] = []
    }

    /// Failed sign-ins before the fake answers 429, and the wait it asks for.
    public static let lockoutAfter = 3
    public static let lockoutSeconds = 60

    public let profile: ServerProfile
    public let urlSession: URLSession
    let state = Mutex(State())

    /// `host` defaults to a fresh `*.fake.vitamux.test` name.
    public init(host: String = "s\(UInt64.random(in: 0...UInt64.max)).fake.vitamux.test", totpEnabled: Bool = false) {
        profile = try! ServerProfile("https://\(host)", allowsLocalHTTP: false)
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [FakeURLProtocol.self]
        urlSession = URLSession(configuration: configuration)
        state.withLock { $0.totpEnabled = totpEnabled }
        FakeURLProtocol.servers.withLock { $0[host] = self }
    }

    /// The server UI tests run against (`-uitest` in the launch arguments).
    public static let uiTest = FakeServer(host: "fake.vitamux.test")

    public static var isUITestRun: Bool {
        ProcessInfo.processInfo.arguments.contains("-uitest")
    }

    /// A generated client for this fake, with the real middleware.
    public func client(sessions: SessionStore, onExpired: @escaping @Sendable () -> Void = {}) -> Client {
        Client(
            profile: profile,
            sessions: sessions,
            transport: URLSessionTransport(configuration: .init(session: urlSession, httpBodyProcessingMode: .buffered)),
            onExpired: onExpired
        )
    }

    public var totpEnabled: Bool {
        get { state.withLock { $0.totpEnabled } }
        set { state.withLock { $0.totpEnabled = newValue } }
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
}

/// Routes requests to their `FakeServer` by host.
final class FakeURLProtocol: URLProtocol {
    static let servers = Mutex<[String: FakeServer]>([:])

    override class func canInit(with request: URLRequest) -> Bool {
        guard let host = request.url?.host() else { return false }
        return servers.withLock { $0[host] != nil }
    }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        guard let url = request.url, let host = url.host(),
              let server = Self.servers.withLock({ $0[host] })
        else {
            client?.urlProtocol(self, didFailWithError: URLError(.cannotFindHost))
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
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            guard count > 0 else { break }
            data.append(buffer, count: count)
        }
        return data
    }
}
#endif
