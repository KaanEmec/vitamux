/// A screen's loading state: every model keeps what it shows in one `Loadable`.
public enum Loadable<Value> {
    case loading
    case loaded(Value)
    case failed(Problem)

    /// Runs `work` on the caller's actor and captures its value or its failure.
    ///
    ///     status = await Loadable { try await client.systemStatus() }
    public init(_ work: () async throws -> Value) async {
        do {
            self = .loaded(try await work())
        } catch {
            self = .failed(Problem(error))
        }
    }

    public var value: Value? {
        if case .loaded(let value) = self { value } else { nil }
    }
}

extension Loadable: Sendable where Value: Sendable {}
extension Loadable: Equatable where Value: Equatable {}
