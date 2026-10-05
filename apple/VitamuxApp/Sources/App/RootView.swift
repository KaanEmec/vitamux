import SwiftUI

/// The tab bar, one `NavigationStack` per tab, and the deep-link entry point.
struct RootView: View {
    @Environment(AppState.self) private var state

    var body: some View {
        @Bindable var state = state
        TabView(selection: $state.tab) {
            Tab("Dashboard", systemImage: "square.grid.2x2", value: AppTab.dashboard) {
                TabStack(tab: .dashboard) { DashboardView() }
            }
            Tab("Explore", systemImage: "chart.xyaxis.line", value: AppTab.explore) {
                TabStack(tab: .explore) { ExploreView() }
            }
            Tab("Sources", systemImage: "antenna.radiowaves.left.and.right", value: AppTab.sources) {
                TabStack(tab: .sources) { SourcesView() }
            }
            Tab("Lab", systemImage: "doc.text", value: AppTab.lab) {
                TabStack(tab: .lab) { LabView() }
            }
            Tab("More", systemImage: "ellipsis", value: AppTab.more) {
                TabStack(tab: .more) { MoreView() }
            }
        }
        .onOpenURL { state.open($0) }
    }
}

private struct TabStack<Root: View>: View {
    @Environment(AppState.self) private var state
    let tab: AppTab
    @ViewBuilder let root: Root

    var body: some View {
        NavigationStack(path: Binding { state.paths[tab] ?? [] } set: { state.paths[tab] = $0 }) {
            root.navigationDestination(for: Route.self) { RouteView(route: $0) }
        }
    }
}

/// The screen for each `Route`. A new route adds one case here.
private struct RouteView: View {
    let route: Route

    var body: some View {
        switch route {
        case .systemStatus: SystemStatusView()
        }
    }
}
