import SwiftUI
import VitamuxKit

/// One connection (`vitamux://connections/{id}?tab=`): the header with health, Sync now and
/// Reauthorize, then the panel's tabs. Each tab loads its own data.
struct ConnectionDetailView: View {
    enum Tab: String, CaseIterable, Identifiable {
        case overview, streams, devices, backfills, history, settings
        var id: String { rawValue }
        var label: String { rawValue.prefix(1).uppercased() + rawValue.dropFirst() }
    }

    @Environment(AppState.self) private var state
    @State private var connection: Loadable<Connection> = .loading
    @State private var actions = ConnectionActions()
    /// Seeded once from the link's `?tab=`; a later link with another tab updates it.
    @State private var tab: Tab
    let id: String
    let linkedTab: String?

    init(id: String, tab: String?) {
        self.id = id
        linkedTab = tab
        _tab = State(initialValue: tab.flatMap(Tab.init(rawValue:)) ?? .overview)
    }

    var body: some View {
        List {
            switch connection {
            case .loading:
                ProgressView("Loading the connection").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let c):
                DetailHeader(connection: c, actions: actions) { connection = .loaded($0) }
                    .id(c.id)
                Section {
                    TabChips(tab: $tab)
                }
                switch tab {
                case .overview: OverviewTab(connection: c) { tab = .history }
                case .streams: StreamsTab(connection: c)
                case .devices: DevicesTab(connection: c)
                case .backfills: BackfillsTab(connection: c)
                case .history: HistoryTab(connection: c)
                case .settings: SettingsTab(connection: c) { connection = .loaded($0) }
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle(connection.value.map { providerLabel($0.provider) } ?? "Connection")
        .navigationBarTitleDisplayMode(.inline)
        .task(id: id) { await load() }
        .onChange(of: linkedTab) { _, new in tab = new.flatMap(Tab.init(rawValue:)) ?? .overview }
    }

    private func load() async {
        guard let client = state.client else { return }
        let loaded = await Loadable { try await client.getConnection(path: .init(id: id)).ok.body.json }
        if loaded.value != nil || connection.value == nil { connection = loaded }
    }
}

private struct DetailHeader: View {
    let connection: Connection
    let actions: ConnectionActions
    let onChange: (Connection) -> Void

    var body: some View {
        Section {
            HStack(spacing: 12) {
                SourceMonogram(provider: connection.provider, size: 48)
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 6) {
                        Text(providerLabel(connection.provider)).font(.title3.bold())
                        switch connection.official {
                        case false?: UnofficialBadge()
                        case true?: Text("Official").font(.caption2.weight(.semibold)).foregroundStyle(.secondary)
                        case nil: Text("Push").font(.caption2.weight(.semibold)).foregroundStyle(.secondary)
                        }
                    }
                    HealthBadge(health: connection.health).font(.subheadline.weight(.semibold)).accessibilityIdentifier("detailHealth")
                    Text("\(SourcesCopy.mode(connection.mode)) · \(SourcesCopy.lastSync(connection))")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
            if connection.mode == .push {
                Text("This source uploads its data to Vitamux; there is nothing to sync from here.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            } else {
                ActionsRow(connection: connection, actions: actions) { onChange($0) }
            }
        }
    }
}

/// The six tabs as chips that scroll sideways, so every label stays readable on a phone.
private struct TabChips: View {
    @Binding var tab: ConnectionDetailView.Tab

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                ForEach(ConnectionDetailView.Tab.allCases) { item in
                    Button { tab = item } label: {
                        Text(item.label)
                            .font(.subheadline.weight(.semibold))
                            .padding(.horizontal, 14)
                            .padding(.vertical, 8)
                            .foregroundStyle(tab == item ? Color(.systemBackground) : .primary)
                            .background(tab == item ? Color.primary : Color(.tertiarySystemFill), in: .capsule)
                    }
                    .buttonStyle(.plain)
                    .accessibilityAddTraits(tab == item ? .isSelected : [])
                    .accessibilityIdentifier("tab-\(item.rawValue)")
                }
            }
            .padding(.vertical, 2)
        }
        .listRowInsets(EdgeInsets(top: 8, leading: 16, bottom: 8, trailing: 16))
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Connection sections")
    }
}
