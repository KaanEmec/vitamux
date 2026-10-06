import Foundation

/// Every screen a link can open. Deep links, widgets, notifications and the OAuth return all parse
/// into a `Route`. A link is `vitamux://` plus the panel path and query
/// (docs/architecture/ios-app-screens.md); an unknown path opens its tab's root.
enum Route: Hashable {
    // Tab roots: opening one resets its tab's stack and hands the root its query.
    case dashboard(date: String? = nil)
    /// `origin`: Explore filtered to one origin app (`vitamux://explore?origin=<bundle id>`).
    case explore(origin: String? = nil)
    case connections(ConnectionsReturn = .init())
    case lab
    case more

    // Explore
    case metric(code: String, range: String? = nil, end: String? = nil)
    case metricDay(code: String, date: String)
    case exploreView(ExploreKind)
    case events(code: String? = nil)
    // Apple Watch (J22.18): an ECG recording, a day's beat-to-beat series, a workout's route and segments.
    case ecgRecording(id: String)
    case beats(date: String? = nil)
    case workout(id: String)

    // Sources
    case connection(id: String, tab: String? = nil)

    // Lab
    case labDocument(id: String)
    case labResults
    case analyte(code: String)
    /// A PDF another app opened in Vitamux (a `file://` URL, not a link).
    case labImport(file: URL)

    // More
    case rules
    case rule(metric: String, saved: Int? = nil)
    /// The builder: from the rule in effect, version `from`, or an empty rule (`blank`).
    case ruleNew(metric: String? = nil, from: Int? = nil, blank: Bool = false)
    case settings(SettingsPage)
    case appleHealth
    case appleHealthSources

    /// The specialised Explore views (`/explore/sleep` and siblings).
    enum ExploreKind: String, CaseIterable {
        case sleep, bloodPressure = "blood-pressure", bodyComposition = "body-composition", workouts
        // Apple Watch (J22.18)
        case ecg, activityRings = "activity-rings", stateOfMind = "state-of-mind"
    }

    /// The query the OAuth return and the panel's links put on `/connections`.
    struct ConnectionsReturn: Hashable {
        var connected: String?
        var authError: String?
        var provider: String?
        var removed: String?
    }

    /// `vitamux://settings/{page}`; `profile` is the bare `/settings`, `app` is app-only.
    enum SettingsPage: String, CaseIterable {
        case profile, sources, devices, ai, apiKeys = "api-keys", security, retention, backups, system, app
    }

    init?(url: URL) {
        if url.isFileURL {
            self = .labImport(file: url)
            return
        }
        guard let segments = Self.segments(of: url) else { return nil }
        let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        let value = { (name: String) in query.first { $0.name == name }?.value.flatMap { $0.isEmpty ? nil : $0 } }
        guard segments.count <= 4 else { return nil }
        let part = { (index: Int) in index < segments.count ? segments[index] : nil }
        switch (part(0), part(1), part(2), part(3)) {
        case ("dashboard", nil, nil, nil): self = .dashboard(date: value("date"))
        case ("explore", nil, nil, nil): self = .explore(origin: value("origin"))
        case ("explore", "events", nil, nil): self = .events(code: value("code"))
        case ("explore", "beats", nil, nil): self = .beats(date: value("date"))
        case ("explore", "ecg", let id?, nil): self = .ecgRecording(id: id)
        case ("explore", "workouts", let id?, nil): self = .workout(id: id)
        case ("explore", let name?, nil, nil) where ExploreKind(rawValue: name) != nil:
            self = .exploreView(ExploreKind(rawValue: name)!)
        case ("explore", let code?, nil, nil): self = .metric(code: code, range: value("range"), end: value("end"))
        case ("explore", let code?, "day", let date?): self = .metricDay(code: code, date: date)
        case ("connections", nil, nil, nil):
            self = .connections(ConnectionsReturn(
                connected: value("connected"), authError: value("auth_error"),
                provider: value("provider"), removed: value("removed")
            ))
        case ("connections", let id?, nil, nil): self = .connection(id: id, tab: value("tab"))
        case ("lab", nil, nil, nil): self = .lab
        case ("lab", "documents", let id?, nil): self = .labDocument(id: id)
        case ("lab", "results", nil, nil): self = .labResults
        case ("lab", "analytes", let code?, nil): self = .analyte(code: code)
        case ("rules", nil, nil, nil): self = .rules
        case ("rules", "new", nil, nil):
            self = .ruleNew(metric: value("metric"), from: value("from").flatMap(Int.init), blank: value("blank") != nil)
        case ("rules", let metric?, nil, nil): self = .rule(metric: metric, saved: value("saved").flatMap(Int.init))
        case ("settings", nil, nil, nil): self = .settings(.profile)
        case ("settings", let page?, nil, nil) where page != "profile" && SettingsPage(rawValue: page) != nil:
            self = .settings(SettingsPage(rawValue: page)!)
        case ("apple-health", nil, nil, nil): self = .appleHealth
        case ("apple-health", "sources", nil, nil): self = .appleHealthSources
        default: return nil
        }
    }

    var tab: AppTab {
        switch self {
        case .dashboard: .dashboard
        case .explore, .metric, .metricDay, .exploreView, .events, .ecgRecording, .beats, .workout: .explore
        case .connections, .connection: .sources
        case .lab, .labDocument, .labResults, .analyte, .labImport: .lab
        case .more, .rules, .rule, .ruleNew, .settings, .appleHealth, .appleHealthSources: .more
        }
    }

    /// A tab root is shown as the tab itself, not pushed onto its stack.
    var isTabRoot: Bool {
        switch self {
        case .dashboard, .explore, .connections, .lab, .more: true
        default: false
        }
    }

    /// `vitamux://settings/system` → `["settings", "system"]`; nil for another scheme. Segments are
    /// percent-decoded, so `vitamux://explore/a%20b` is `["explore", "a b"]`.
    static func segments(of url: URL) -> [String]? {
        guard url.scheme == "vitamux", let host = url.host(percentEncoded: false), !host.isEmpty else { return nil }
        return [host] + url.pathComponents.filter { $0 != "/" }
    }
}

enum AppTab: Hashable, CaseIterable {
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

    /// The route of the tab's root, without a query.
    var root: Route {
        switch self {
        case .dashboard: .dashboard()
        case .explore: .explore()
        case .sources: .connections()
        case .lab: .lab
        case .more: .more
        }
    }
}
