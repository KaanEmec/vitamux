import SwiftUI
import VitamuxKit

/// Overview: the sync timeline (14-day run strip, latest runs) and the connection's facts.
struct OverviewTab: View {
    @Environment(AppState.self) private var state
    @State private var runs: Loadable<[Run]> = .loading
    let connection: Connection
    let showHistory: () -> Void

    var body: some View {
        if connection.health == .needsReauth {
            Section {
                NoticeRow(text: "\(providerLabel(connection.provider)) no longer accepts the stored authorization. Sign in again to resume syncing; no data is lost.",
                          kind: .error, identifier: "reauthNotice")
            }
        }
        Section("Sync timeline") {
            RunStrip(runs: runs, large: true)
            if let list = runs.value {
                if list.isEmpty {
                    Text("No runs in the last 14 days.").font(.subheadline).foregroundStyle(.secondary)
                }
                ForEach(list.prefix(5), id: \.id) { RunRow(run: $0) }
                if !list.isEmpty {
                    Button("All runs", action: showHistory).accessibilityIdentifier("allRuns")
                }
            }
        }
        // Reload when the connection changes (a manual sync updates last_success_at).
        .task(id: connection.lastSuccessAt) {
            guard let client = state.client else { return }
            runs = await Loadable { try await Runs.load(client, id: connection.id) }
        }
        Section("Details") {
            LabeledContent("Health") {
                Text(connection.health.label + (connection.healthReason.map { " · \($0)" } ?? ""))
            }
            LabeledContent("Status") { Text(connection.status.rawValue).font(.body.monospaced()) }
            LabeledContent("API", value: api)
            if let upstream = connection.upstream {
                LabeledContent("Upstream") {
                    if let url = URL(string: upstream.sourceUrl), ["http", "https"].contains(url.scheme?.lowercased()) {
                        Link("\(upstream.package) \(upstream.version)", destination: url)
                    } else {
                        Text("\(upstream.package) \(upstream.version)")
                    }
                }
            }
            LabeledContent("Last success") {
                Text(SourcesCopy.ago(connection.lastSuccessAt) + (connection.lastSuccessAt.map { " · " + SourcesCopy.when($0) } ?? ""))
            }
            LabeledContent("Last error") {
                Text(connection.lastErrorClass.map { "\($0) · \(SourcesCopy.plural(connection.consecutiveFailures, "consecutive failure")) " } ?? "None")
            }
            LabeledContent("Connected", value: SourcesCopy.when(connection.createdAt))
            LabeledContent("ID") { Text(connection.id).font(.caption.monospaced()).textSelection(.enabled) }
        }
    }

    private var api: String {
        switch connection.official {
        case false?: "Unofficial (may change without notice)"
        case true?: "Official"
        case nil: "Push uploads"
        }
    }
}
