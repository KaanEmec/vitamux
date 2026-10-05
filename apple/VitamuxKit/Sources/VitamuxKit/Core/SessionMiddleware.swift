import Foundation
import HTTPTypes
import OpenAPIRuntime

/// The client's one middleware (docs/architecture/ios-app.md#api-client):
/// - adds `Authorization: Bearer` from the `SessionStore`, only for the server that issued it;
/// - throws a `Problem` for every response of 400 or more, read from `application/problem+json`
///   with field pointers, and `Retry-After` on a 429;
/// - on a `401` to a request that carried the token (anything but sign-in), clears the session and
///   calls `onExpired`, so the app returns to sign-in with "session expired" and keeps the server URL.
public struct SessionMiddleware: ClientMiddleware {
    let sessions: SessionStore
    let onExpired: @Sendable () -> Void

    public init(sessions: SessionStore, onExpired: @escaping @Sendable () -> Void = {}) {
        self.sessions = sessions
        self.onExpired = onExpired
    }

    public func intercept(
        _ request: HTTPRequest,
        body: HTTPBody?,
        baseURL: URL,
        operationID: String,
        next: @concurrent @Sendable (HTTPRequest, HTTPBody?, URL) async throws -> (HTTPResponse, HTTPBody?)
    ) async throws -> (HTTPResponse, HTTPBody?) {
        let signIn = operationID == Operations.Login.id
        var request = request
        let token = signIn ? nil : sessions.token(for: baseURL)
        if let token { request.headerFields[.authorization] = "Bearer \(token)" }
        let (response, responseBody) = try await next(request, body, baseURL)
        guard response.status.code >= 400 else { return (response, responseBody) }
        let problem = await Problem(response: response, body: responseBody)
        if response.status == .unauthorized, token != nil {
            try? sessions.clear()
            onExpired()
        }
        throw problem
    }
}
