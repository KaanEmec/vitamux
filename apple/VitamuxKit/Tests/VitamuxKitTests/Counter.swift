import Synchronization

/// A count shared with escaping and `@Sendable` closures.
final class Counter: Sendable {
    private let count = Mutex(0)

    var value: Int { count.withLock { $0 } }

    /// Adds one and answers the new count.
    @discardableResult
    func increment() -> Int {
        count.withLock { $0 += 1; return $0 }
    }
}
