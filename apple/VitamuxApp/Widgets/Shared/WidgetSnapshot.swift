import Foundation

/// The one handoff from the app to the widgets (docs/architecture/ios-app.md#offline-cache-widgets-and-notifications):
/// today's dashboard cards as the app last showed them, one small JSON file in the app group. The
/// app writes it after a dashboard load and removes it on sign-out or an ended session; the
/// extension only reads it and has no networking of its own. Compiled into both targets.
nonisolated struct WidgetSnapshot: Codable, Equatable, Sendable {
    /// One dashboard card, already formatted as the card shows it (`CardContent`).
    struct Card: Codable, Equatable, Sendable, Identifiable {
        var metric: String
        var label: String
        /// `MetricHue` raw value.
        var hue: String
        var value: String
        var unit: String
        var sub: String = ""
        var delta: String = ""
        /// `DataStatus` raw value.
        var status: String
        var values: [Double?] = []
        var bars = false
        var band: ClosedRange<Double>?
        var mean: Double?
        /// Sleep: seconds per stage (`SleepStageKind` raw value) in display order.
        var stages: [Stage]?

        var id: String { metric }
    }

    struct Stage: Codable, Equatable, Sendable {
        var kind: String
        var seconds: Double
    }

    /// Bumped when the shape changes; a widget ignores a file of another version.
    static let version = 1

    var version = Self.version
    /// When the values were fetched from the server.
    var asOf: Date
    /// The owner's local day the values are for (`YYYY-MM-DD`) and their timezone.
    var day: String
    var timeZone: String
    /// The dashboard's shown cards, in its order.
    var cards: [Card]

    // MARK: Where it lives

    static let appGroup = "group.org.vitamux.healthbridge"
    /// The owner's preference (Settings › This app): values hidden while locked, on by default.
    static let redactKey = "widgets.redactWhileLocked"

    /// `Library/Application Support/Widgets/snapshot.json` in the app group's container.
    static var groupURL: URL? {
        FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: appGroup)?
            .appending(path: "Library/Application Support/Widgets/snapshot.json", directoryHint: .notDirectory)
    }

    /// The file at `url`; nil when there is none, it cannot be read, or it has another version.
    static func read(from url: URL?) -> WidgetSnapshot? {
        guard let url, let data = try? Data(contentsOf: url),
              let snapshot = try? JSONDecoder().decode(WidgetSnapshot.self, from: data),
              snapshot.version == version
        else { return nil }
        return snapshot
    }

    /// Writes atomically, readable after the first unlock (the widget renders while locked), and
    /// outside iCloud backup, like the response cache.
    func write(to url: URL) throws {
        let folder = url.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        var options: Data.WritingOptions = [.atomic]
        #if os(iOS)
        options.insert(.completeFileProtectionUntilFirstUserAuthentication)
        #endif
        try JSONEncoder().encode(self).write(to: url, options: options)
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        var marked = url
        try? marked.setResourceValues(values)
    }

    static func remove(at url: URL?) {
        guard let url else { return }
        try? FileManager.default.removeItem(at: url)
    }
}
