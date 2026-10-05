import Foundation

/// Every screen a link can open. Deep links, widgets, notifications and the OAuth return all parse
/// into a `Route`. A link is `vitamux://` plus the panel path: docs/architecture/ios-app-screens.md.
enum Route: Hashable {
    case systemStatus

    init?(url: URL) {
        switch Self.segments(of: url) {
        case ["settings", "system"]: self = .systemStatus
        default: return nil
        }
    }

    var tab: AppTab {
        switch self {
        case .systemStatus: .more
        }
    }

    /// `vitamux://settings/system` → `["settings", "system"]`; nil for another scheme.
    static func segments(of url: URL) -> [String]? {
        guard url.scheme == "vitamux", let host = url.host() else { return nil }
        return [host] + url.pathComponents.filter { $0 != "/" }
    }
}

enum AppTab: Hashable {
    case dashboard, explore, sources, lab, more

    /// The tab a link's first segment belongs to (`vitamux://connections` → Sources).
    init?(url: URL) {
        switch Route.segments(of: url)?.first {
        case "dashboard": self = .dashboard
        case "explore": self = .explore
        case "connections": self = .sources
        case "lab": self = .lab
        case "rules", "settings", "apple-health": self = .more
        default: return nil
        }
    }
}
