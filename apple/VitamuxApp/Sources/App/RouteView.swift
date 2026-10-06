import SwiftUI

/// The screen for each `Route`. A screen a later job builds shows a `PlaceholderView` until then.
struct RouteView: View {
    let route: Route

    var body: some View {
        switch route {
        case .dashboard(let date): DashboardView(date: date)
        case .explore(let origin): ExploreView(origin: origin)
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
        case .exploreView(.ecg): ECGListView()
        case .exploreView(.activityRings): ActivityRingsView()
        case .exploreView(.stateOfMind): StateOfMindView()
        // Keyed by the link's value, so a new link at the same place in the stack gets a new model.
        case .ecgRecording(let id): ECGRecordingView(id: id).id(id)
        case .beats(let date): BeatsView(date: date).id(date)
        case .workout(let id): WorkoutDetailView(id: id).id(id)

        case .connection(let id, let tab): ConnectionDetailView(id: id, tab: tab)

        case .labDocument(let id): ReviewView(id: id)
        case .labResults: ResultsView()
        case .analyte(let code): LabAnalyteView(code: code)
        case .labImport(let file): LabImportView(file: file)

        case .rules: RulesView()
        case .rule(let metric, let saved): RuleView(metric: metric, saved: saved)
        case .ruleNew(let metric, let from, let blank): RuleBuilderView(metric: metric, from: from, blank: blank)
        case .settings(let page): SettingsPageView(page: page)
        case .appleHealth: AppleHealthView()
        case .appleHealthSources: AppleHealthSourcesView()
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
        case .ecg: "ECG"
        case .activityRings: "Activity rings"
        case .stateOfMind: "State of Mind"
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
