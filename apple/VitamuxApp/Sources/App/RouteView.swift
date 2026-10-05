import SwiftUI

/// The screen for each `Route`. A screen a later job builds shows a `PlaceholderView` until then.
struct RouteView: View {
    let route: Route

    var body: some View {
        switch route {
        case .dashboard(let date): DashboardView(date: date)
        case .explore: ExploreView()
        case .connections(let back): SourcesView(back: back)
        case .lab: LabView()
        case .more: MoreView()

        case .metric(let code, let range, let end): MetricDetailView(code: code, range: range, end: end)
        case .metricDay(let code, let date): AllSourcesDayRoute(code: code, date: date)
        case .exploreView(.sleep): SleepView()
        case .exploreView(.bloodPressure): BloodPressureView()
        case .exploreView(.bodyComposition): BodyCompositionView()
        case .exploreView(.workouts): WorkoutsView()
        case .events(let code): EventsView(code: code)

        case .connection(let id, let tab):
            PlaceholderView(title: "Connection", detail: [id, tab], job: "J22.11")

        case .labDocument(let id):
            PlaceholderView(title: "Review", detail: [id], job: "J22.12")
        case .labResults:
            PlaceholderView(title: "Results", detail: [], job: "J22.12")
        case .analyte(let code): LabAnalyteView(code: code)

        case .rules:
            PlaceholderView(title: "Rules", detail: [], job: "J22.10")
        case .rule(let metric):
            PlaceholderView(title: "Rule", detail: [metric], job: "J22.10")
        case .ruleNew:
            PlaceholderView(title: "New rule", detail: [], job: "J22.10")
        case .settings(.system): SystemStatusView()
        case .settings(.app): AppSettingsView()
        case .settings(let page):
            PlaceholderView(title: page.title, detail: [], job: "J22.13")
        case .appleHealth:
            PlaceholderView(title: "Apple Health", detail: [], job: "J22.14")
        }
    }
}

extension Route.ExploreKind {
    var title: String {
        switch self {
        case .sleep: "Sleep"
        case .bloodPressure: "Blood pressure"
        case .bodyComposition: "Body composition"
        case .workouts: "Workouts"
        }
    }
}

extension Route.SettingsPage {
    /// The panel's Settings page names (web/src/lib/nav.ts).
    var title: String {
        switch self {
        case .profile: "Profile"
        case .sources: "Sources"
        case .devices: "Devices"
        case .ai: "AI providers"
        case .apiKeys: "API keys"
        case .security: "Security"
        case .retention: "Retention"
        case .backups: "Backups and export"
        case .system: "System status"
        case .app: "This app"
        }
    }
}

/// `heart_rate_resting` → "Heart rate resting", as the panel's `metricLabel`.
func metricLabel(_ code: String) -> String {
    let words = code.replacingOccurrences(of: "_", with: " ")
    return words.prefix(1).uppercased() + words.dropFirst()
}
