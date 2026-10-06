import Foundation
import OpenAPIRuntime
import OpenAPIURLSession

extension Client {
    /// The generated client for `profile`, with the `SessionMiddleware`, the offline
    /// `ResponseCache` when given, and RFC 3339 dates.
    /// Screens call its operations directly: `try await client.listRules().ok.body.json`.
    public init(
        profile: ServerProfile,
        sessions: SessionStore,
        cache: ResponseCache? = nil,
        urlSession: URLSession = .shared,
        onExpired: @escaping @Sendable () -> Void = {}
    ) {
        self.init(
            profile: profile,
            sessions: sessions,
            cache: cache,
            transport: URLSessionTransport(configuration: .init(session: urlSession)),
            onExpired: onExpired
        )
    }

    init(
        profile: ServerProfile,
        sessions: SessionStore,
        cache: ResponseCache?,
        transport: URLSessionTransport,
        onExpired: @escaping @Sendable () -> Void
    ) {
        // The cache sits inside the session middleware: it sees the token and the raw status.
        let session: any ClientMiddleware = SessionMiddleware(sessions: sessions, onExpired: onExpired)
        self.init(
            serverURL: profile.baseURL,
            configuration: Configuration(dateTranscoder: RFC3339DateTranscoder()),
            transport: transport,
            middlewares: [session] + (cache.map { [$0] } ?? [])
        )
    }
}
