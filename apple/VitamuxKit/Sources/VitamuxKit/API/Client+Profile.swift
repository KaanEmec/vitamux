import Foundation
import OpenAPIRuntime
import OpenAPIURLSession

extension Client {
    /// The generated client for `profile`, with the one `SessionMiddleware` and RFC 3339 dates.
    /// Screens call its operations directly: `try await client.listRules().ok.body.json`.
    public init(
        profile: ServerProfile,
        sessions: SessionStore,
        urlSession: URLSession = .shared,
        onExpired: @escaping @Sendable () -> Void = {}
    ) {
        self.init(
            profile: profile,
            sessions: sessions,
            transport: URLSessionTransport(configuration: .init(session: urlSession)),
            onExpired: onExpired
        )
    }

    init(
        profile: ServerProfile,
        sessions: SessionStore,
        transport: URLSessionTransport,
        onExpired: @escaping @Sendable () -> Void
    ) {
        self.init(
            serverURL: profile.baseURL,
            configuration: Configuration(dateTranscoder: RFC3339DateTranscoder()),
            transport: transport,
            middlewares: [SessionMiddleware(sessions: sessions, onExpired: onExpired)]
        )
    }
}
