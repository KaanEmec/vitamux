import HealthBridgeHealthKit
import Observation
import SwiftUI
import VitamuxKit

/// The types Apple Watch contributed, from the server's inventory (`GET /inventory`): items that
/// Apple Health sent with a device of type watch, by name.
@Observable
final class WatchCardModel {
    private(set) var items: Loadable<[InventoryItem]> = .loading

    func load(_ client: Client?) async {
        guard let client else { return }
        items = await Loadable {
            try await client.getInventory().ok.body.json.items
                .filter(Self.fromWatch)
                .sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
        }
    }

    static func fromWatch(_ item: InventoryItem) -> Bool {
        item.providers.contains("apple_health") && item.devices.contains { $0._type == "watch" }
    }
}

/// Apple Watch on the Apple Health screen: the groups turned on here and what the Watch
/// contributed per type, with the time of the newest record of that type.
struct WatchCard: View {
    @Environment(AppState.self) private var state
    let device: ThisDevice
    @State private var model = WatchCardModel()

    var body: some View {
        Section {
            LabeledContent("Groups on") {
                Text(groups).accessibilityIdentifier("watchGroups")
            }
            switch model.items {
            case .loading:
                ProgressView().accessibilityLabel("Loading").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let items) where items.isEmpty:
                Text("The server has nothing from Apple Watch yet.")
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("watchEmpty")
            case .loaded(let items):
                ForEach(items, id: \.key) { item in
                    LabeledContent(item.name, value: item.lastSeen)
                        .accessibilityIdentifier("watchType-\(item.code)")
                }
            }
        } header: {
            Label("Apple Watch", systemImage: "applewatch")
                .accessibilityIdentifier("watchCard")
                .task(id: device.finishedRuns) { await model.load(state.client) }
        } footer: {
            Text("What your server has from Apple Watch, by type, with the time of the newest record of that type. There is no Watch app: Apple Watch writes to this iPhone's Health app, which Vitamux reads.")
        }
    }

    private var groups: String {
        let on = MetricGroup.allCases.filter(device.enabled.contains).map(\.title)
        return on.isEmpty ? "None" : on.joined(separator: ", ")
    }
}
