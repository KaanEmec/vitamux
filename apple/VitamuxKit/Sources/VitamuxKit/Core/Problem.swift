import Foundation

/// What a failed call tells the owner: the server's `application/problem+json` title and detail,
/// or a plain message for any other error. J22.4 adds the status and field pointers.
public struct Problem: Error, Equatable, Sendable {
    public var title: String
    public var detail: String?

    public init(title: String, detail: String? = nil) {
        self.title = title
        self.detail = detail
    }

    /// One failure type for every screen: a `Problem` passes through, anything else is wrapped.
    public init(_ error: any Error) {
        if let problem = error as? Problem {
            self = problem
        } else {
            self.init(title: "Something went wrong", detail: error.localizedDescription)
        }
    }
}
