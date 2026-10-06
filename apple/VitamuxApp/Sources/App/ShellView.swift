import SwiftUI
import VitamuxKit

/// The signed-in shell: the tab bar, one `NavigationStack` per tab, search, sync status and the
/// offline banner.
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
        .safeAreaInset(edge: .bottom, spacing: 0) {
            OfflineBanner(status: state.cacheStatus)
        }
    }
}

/// While the server cannot be reached: since when the shown data is (the offline cache's stored
/// time, `ResponseCache`) and that changes need the network.
private struct OfflineBanner: View {
    let status: ResponseCache.Status

    var body: some View {
        if case .offline(let from) = status {
            VStack(spacing: 2) {
                Label(title(from), systemImage: "wifi.slash")
                    .font(.footnote.weight(.semibold))
                Text("Changes need the network.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 8)
            .padding(.horizontal, 16)
            .background(.thinMaterial)
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("offlineBanner")
        }
    }

    private func title(_ from: Date?) -> String {
        guard let from else { return "Offline" }
        let today = Calendar.current.isDateInToday(from)
        let time = from.formatted(today ? .dateTime.hour().minute() : .dateTime.day().month().hour().minute())
        return "Offline, showing data from \(time)"
    }
}
