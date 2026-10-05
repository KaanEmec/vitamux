import Observation
import SwiftUI
import VitamuxKit

/// The panel's sync pill (web/src/lib/shell/SyncStatus.svelte), from `GET /connections`: how long
/// ago the latest successful sync finished, or how many sources need attention. Search reuses
/// the connections. Hidden until they load; a failed load keeps the last answer.
@Observable
final class SyncStatusModel {
    private(set) var connections: [Components.Schemas.Connection] = []

    /// The health values the panel flags (`alerting` in web/src/lib/connections/connections.ts).
    static let alerting: Set<Components.Schemas.Health> = [.degraded, .failing, .needsReauth, .stale]

    var attention: Int {
        connections.filter { Self.alerting.contains($0.health) }.count
    }

    var lastSync: Date? {
        connections.compactMap(\.lastSuccessAt).max()
    }

    func load(_ client: Client?) async {
        guard let client else { return }
        if let answer = try? await client.listConnections().ok.body.json {
            connections = answer.connections
        }
    }
}

/// Opens Sources. Attention is a triangle as well as a colour.
struct SyncStatusButton: View {
    @Environment(AppState.self) private var state
    let model: SyncStatusModel

    var body: some View {
        if !model.connections.isEmpty {
            Button { state.open(.connections()) } label: {
                // An HStack, not a Label: toolbars show a Label's icon only.
                HStack(spacing: 6) {
                    Image(systemName: model.attention > 0 ? "exclamationmark.triangle.fill" : "circle.fill")
                        .foregroundStyle(model.attention > 0 ? .orange : .green)
                        .imageScale(.small)
                    Text(text).font(.footnote)
                }
                .fixedSize()
            }
            .accessibilityLabel(text)
            .accessibilityIdentifier("syncStatus")
        }
    }

    private var text: String {
        let attention = model.attention
        if attention > 0 { return attention == 1 ? "1 source needs attention" : "\(attention) sources need attention" }
        guard let last = model.lastSync else { return "Not synced yet" }
        return "Synced " + last.formatted(.relative(presentation: .named))
    }
}
