import SwiftUI

/// The signed-in shell: the tab bar, one `NavigationStack` per tab, search and sync status.
struct ShellView: View {
    @Environment(AppState.self) private var state
    @Environment(\.scenePhase) private var phase
    @State private var sync = SyncStatusModel()

    var body: some View {
        @Bindable var state = state
        TabView(selection: $state.tab) {
            Tab("Dashboard", systemImage: "square.grid.2x2", value: AppTab.dashboard) {
                TabStack(tab: .dashboard, sync: sync)
            }
            Tab("Explore", systemImage: "safari", value: AppTab.explore) {
                TabStack(tab: .explore, sync: sync)
            }
            Tab("Sources", systemImage: "powerplug", value: AppTab.sources) {
                TabStack(tab: .sources, sync: sync)
            }
            Tab("Lab", systemImage: "flask", value: AppTab.lab) {
                TabStack(tab: .lab, sync: sync)
            }
            Tab("More", systemImage: "ellipsis", value: AppTab.more) {
                TabStack(tab: .more, sync: sync)
            }
        }
        .sheet(isPresented: $state.isSearching) {
            SearchView(connections: sync.connections)
        }
        .task(id: phase) {
            if phase == .active { await sync.load(state.client) }
        }
    }
}

/// One tab's stack. Its root carries the search button and the sync status.
private struct TabStack: View {
    @Environment(AppState.self) private var state
    let tab: AppTab
    let sync: SyncStatusModel

    var body: some View {
        NavigationStack(path: Binding { state.paths[tab] ?? [] } set: { state.paths[tab] = $0 }) {
            RouteView(route: state.root(of: tab))
                .toolbar {
                    ToolbarItem(placement: .topBarLeading) {
                        SyncStatusButton(model: sync)
                    }
                    ToolbarItem(placement: .topBarTrailing) {
                        Button("Search", systemImage: "magnifyingglass") { state.isSearching = true }
                            .accessibilityIdentifier("searchButton")
                    }
                }
                .navigationDestination(for: Route.self) { RouteView(route: $0) }
        }
    }
}
