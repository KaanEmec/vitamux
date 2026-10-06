import Foundation
import VitamuxKit
import WidgetKit

/// What one widget shows at one time: the app's snapshot (nil while signed out), the metrics the
/// owner chose, and whether values are hidden while locked.
struct WidgetEntry: TimelineEntry {
    /// After this long without a dashboard load the "as of" line says how old the values are.
    static let staleAfter: TimeInterval = 6 * 3600

    var date: Date
    /// nil: signed out, the session ended, or the app has not shown a dashboard yet.
    var snapshot: WidgetSnapshot?
    /// The chosen metric codes; empty means the dashboard's first cards.
    var metrics: [String] = []
    var redacts = true

    var isSignedOut: Bool { snapshot == nil }

    var isStale: Bool {
        guard let snapshot else { return false }
        return date.timeIntervalSince(snapshot.asOf) >= Self.staleAfter
    }

    /// The chosen cards that are on the dashboard, else its first `limit` cards.
    func cards(limit: Int) -> [WidgetSnapshot.Card] {
        guard let snapshot else { return [] }
        let chosen = metrics.compactMap { code in snapshot.cards.first { $0.metric == code } }
        return Array((chosen.isEmpty ? snapshot.cards : chosen).prefix(limit))
    }

    /// "As of 08:12", or with the weekday once stale.
    var asOfText: String {
        guard let snapshot else { return "" }
        let style: Date.FormatStyle = isStale ? .dateTime.weekday(.abbreviated).hour().minute() : .dateTime.hour().minute()
        return "As of " + snapshot.asOf.formatted(style)
    }
}

/// Reads what the widgets show, on the device: the snapshot file, the redaction preference and
/// whether the app session still exists (the shared Keychain item). Nothing here talks to the
/// server; the app refreshes the snapshot.
struct WidgetStore: Sendable {
    /// The app's session as far as this iPhone knows.
    enum Session: Sendable {
        case active(until: Date)
        case none
        /// The Keychain could not be read (before the first unlock): the snapshot decides.
        case unknown
    }

    /// The idle limit of an app session (`VITAMUX_APP_SESSION_IDLE`'s default): a snapshot older
    /// than this belongs to a session that has ended without use.
    static let sessionIdle: TimeInterval = 30 * 86_400

    var snapshotURL: URL?
    var redacts: @Sendable () -> Bool
    var session: @Sendable () -> Session

    /// The installed extension: the app group's file and defaults, and the shared Keychain item.
    static let live = WidgetStore(
        snapshotURL: WidgetSnapshot.groupURL,
        redacts: {
            UserDefaults(suiteName: WidgetSnapshot.appGroup)?.object(forKey: WidgetSnapshot.redactKey) as? Bool ?? true
        },
        session: {
            do {
                guard let entry = try SessionStore.keychain().load() else { return .none }
                return .active(until: entry.expiresAt)
            } catch {
                return .unknown
            }
        }
    )

    /// The entry at `date`: signed out once the session has ended.
    func entry(at date: Date, metrics: [String]) -> WidgetEntry {
        let snapshot = WidgetSnapshot.read(from: snapshotURL)
        return WidgetEntry(date: date, snapshot: Self.usable(snapshot, at: date, session: session()), metrics: metrics, redacts: redacts())
    }

    /// Now, when the values turn stale, and when the session ends. The app reloads the timelines
    /// after each write; the four-hour refresh catches a session ended elsewhere.
    func timeline(metrics: [String], now: Date = .now) -> Timeline<WidgetEntry> {
        let first = entry(at: now, metrics: metrics)
        var entries = [first]
        if let snapshot = first.snapshot {
            var later = [snapshot.asOf.addingTimeInterval(WidgetEntry.staleAfter), snapshot.asOf.addingTimeInterval(Self.sessionIdle)]
            if case .active(let until) = session() { later.append(until) }
            for date in later.filter({ $0 > now }).sorted() {
                let next = entry(at: date, metrics: metrics)
                entries.append(next)
                if next.isSignedOut { break }
            }
        }
        return Timeline(entries: entries, policy: .after(now.addingTimeInterval(4 * 3600)))
    }

    /// The snapshot, unless the session it was made with has ended.
    static func usable(_ snapshot: WidgetSnapshot?, at date: Date, session: Session) -> WidgetSnapshot? {
        guard let snapshot, date.timeIntervalSince(snapshot.asOf) < sessionIdle else { return nil }
        switch session {
        case .none: return nil
        case .active(let until) where until <= date: return nil
        default: return snapshot
        }
    }
}

extension WidgetSnapshot.Card {
    /// The card's Explore route: the specialised views for the two families, else the metric.
    var link: URL {
        switch metric {
        case "sleep": URL(string: "vitamux://explore/sleep")!
        case "blood_pressure": URL(string: "vitamux://explore/blood-pressure")!
        default:
            URL(string: "vitamux://explore/" + (metric.addingPercentEncoding(withAllowedCharacters: CharacterSet.urlPathAllowed.subtracting(CharacterSet(charactersIn: "/"))) ?? metric))
                ?? URL(string: "vitamux://explore")!
        }
    }

    var metricHue: MetricHue { MetricHue(rawValue: hue) ?? .other }
    var dataStatus: DataStatus { DataStatus(rawValue: status) ?? .noData }
    var hasValues: Bool { values.contains { $0 != nil } }
}

extension URL {
    /// Where a signed-out or empty widget opens the app.
    static let vitamuxDashboard = URL(string: "vitamux://dashboard")!
}
