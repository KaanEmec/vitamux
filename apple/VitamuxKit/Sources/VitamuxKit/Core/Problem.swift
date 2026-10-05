import Foundation
import HTTPTypes
import OpenAPIRuntime

/// What a failed call tells the owner: the server's `application/problem+json` (RFC 9457) with
/// its status, code and field pointers, or a plain message for any other error. `SessionMiddleware`
/// throws one for every response of 400 or more, so screens read failures only from here.
public struct Problem: Error, Equatable, Sendable {
    /// One invalid input of a `validation_failed` problem; `pointer` is a JSON pointer (`/username`).
    public struct FieldError: Equatable, Sendable {
        public var pointer: String
        public var detail: String

        public init(pointer: String, detail: String) {
            self.pointer = pointer
            self.detail = detail
        }
    }

    public var title: String
    public var detail: String?
    /// HTTP status; nil when no response arrived.
    public var status: Int?
    /// Stable code from the server's registry (`validation_failed`, `totp_required`, …).
    public var code: String?
    public var requestID: String?
    public var fieldErrors: [FieldError]
    /// From `Retry-After` on a 429: how long to wait before trying again. The kit never retries by
    /// itself (a sign-in lockout lasts minutes and a POST is not safe to repeat); the screen shows it.
    public var retryAfter: Duration?

    public init(
        title: String,
        detail: String? = nil,
        status: Int? = nil,
        code: String? = nil,
        requestID: String? = nil,
        fieldErrors: [FieldError] = [],
        retryAfter: Duration? = nil
    ) {
        self.title = title
        self.detail = detail
        self.status = status
        self.code = code
        self.requestID = requestID
        self.fieldErrors = fieldErrors
        self.retryAfter = retryAfter
    }

    /// One failure type for every screen: a `Problem` passes through (also from inside the generated
    /// client's `ClientError`), anything else is wrapped.
    public init(_ error: any Error) {
        switch error {
        case let problem as Problem:
            self = problem
        case let error as ClientError:
            self.init(error.underlyingError)
        case let error as URLError:
            self.init(title: "Can't reach the server", detail: error.localizedDescription)
        case is DecodingError:
            self.init(title: "Unexpected response", detail: "The server answered in a shape this app does not know.")
        default:
            self.init(title: "Something went wrong", detail: error.localizedDescription)
        }
    }

    /// The message for one field, matched by JSON pointer, to show beside that input.
    public func detail(for pointer: String) -> String? {
        fieldErrors.first { $0.pointer == pointer }?.detail
    }
}

extension Problem {
    /// Reads a failed response: problem+json when the server sent one, the status text otherwise.
    init(response: HTTPResponse, body: HTTPBody?) async {
        let status = response.status.code
        let fallback = HTTPURLResponse.localizedString(forStatusCode: status)
        self.init(title: fallback.prefix(1).uppercased() + fallback.dropFirst(), status: status)
        if let body, let data = try? await Data(collecting: body, upTo: 64 * 1024),
           let decoded = try? JSONDecoder().decode(Components.Schemas.Problem.self, from: data) {
            title = decoded.title
            detail = decoded.detail
            code = decoded.code
            requestID = decoded.requestId
            fieldErrors = (decoded.errors ?? []).map { FieldError(pointer: $0.pointer, detail: $0.detail) }
        }
        if status == 429, let value = response.headerFields[.retryAfter], let seconds = Int(value), seconds >= 0 {
            retryAfter = .seconds(seconds)
        }
    }
}
