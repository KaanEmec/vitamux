import Foundation
import VitamuxKit

/// This app's own preferences that later jobs read: notification categories (J22.21) and widget
/// redaction (J22.20). They live in the app group, beside `AppState`'s theme and app lock, so the
/// widget sees them; UI tests use the cleared test suite. Read them with `@AppStorage(key, store:)`.
enum AppPreferences {
    static var store: UserDefaults {
        #if DEBUG
        if FakeServer.isUITestRun { return UserDefaults(suiteName: "org.vitamux.app.uitest")! }
        #endif
        return UserDefaults(suiteName: "group.org.vitamux.healthbridge") ?? .standard
    }

    /// Widgets hide values while the iPhone is locked (on by default).
    static let redactWidgets = "widgets.redactWhileLocked"

    /// The local notification categories (docs/architecture/ios-app.md#offline-cache-widgets-and-notifications),
    /// each on unless turned off. Notifications never contain health values.
    enum Notification: String, CaseIterable {
        case connectionAttention = "notifications.connectionAttention"
        case failedJob = "notifications.failedJob"
        case labReady = "notifications.labReady"
        case staleBackup = "notifications.staleBackup"
        case uploadStalled = "notifications.uploadStalled"

        var title: String {
            switch self {
            case .connectionAttention: "A connection needs attention"
            case .failedJob: "A job failed for good"
            case .labReady: "A lab document is ready for review"
            case .staleBackup: "The last backup is old"
            case .uploadStalled: "Apple Health upload stalled"
            }
        }

        var isOn: Bool { AppPreferences.store.object(forKey: rawValue) as? Bool ?? true }
    }

    static var notificationsOn: Int { Notification.allCases.filter(\.isOn).count }

    /// What the offline cache holds on disk: today the URL cache of the app's requests; the
    /// file cache (J22.19) adds its folder here.
    static var cacheBytes: Int64 { Int64(URLCache.shared.currentDiskUsage) }

    static func clearCache() {
        URLCache.shared.removeAllCachedResponses()
    }
}
