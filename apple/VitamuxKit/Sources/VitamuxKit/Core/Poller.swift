/// Repeats a refresh at the panel's intervals while a screen shows work in progress. Run it from
/// the screen's `.task`, so it stops when the screen goes away (SwiftUI cancels the task) and
/// never polls in the background:
///
///     .task { await Poller.backfills.run { await model.load(); return model.isRunning } }
public struct Poller: Sendable {
    public var interval: Duration

    public init(interval: Duration) {
        self.interval = interval
    }

    public static let backfills = Poller(interval: .seconds(5))
    public static let extraction = Poller(interval: .seconds(2))
    public static let export = Poller(interval: .seconds(1))

    /// Waits `interval`, calls `tick`, and repeats while `tick` returns true and the task is not
    /// cancelled. Runs on the caller's actor.
    public func run(_ tick: () async -> Bool) async {
        while !Task.isCancelled {
            do {
                try await Task.sleep(for: interval)
            } catch {
                return
            }
            guard await tick() else { return }
        }
    }
}
