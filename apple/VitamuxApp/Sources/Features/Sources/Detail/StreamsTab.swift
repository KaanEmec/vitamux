import SwiftUI
import VitamuxKit

typealias ConnectionStream = Components.Schemas.Stream

/// Streams: health, newest source time seen, cursor and schedules. Resetting a cursor makes the
/// next sync start over from the connector's initial window; stored data stays and records
/// fetched again deduplicate.
struct StreamsTab: View {
    @Environment(AppState.self) private var state
    @State private var streams: Loadable<[ConnectionStream]> = .loading
    @State private var problem: Problem?
    let connection: Connection

    var body: some View {
        Section("Streams") {
            if let problem { ProblemRow(problem: problem) }
            switch streams {
            case .loading:
                ProgressView("Loading streams")
            case .failed(let problem):
                ProblemRow(problem: problem)
            case .loaded(let list):
                if list.isEmpty {
                    Text("No streams yet; they appear after the first sync.").foregroundStyle(.secondary)
                }
                ForEach(list, id: \.name) { stream in
                    StreamRow(stream: stream, canReset: stream.hasCursor && connection.mode != .push) { await reset(stream.name) }
                }
            }
        }
        .task { await load() }
    }

    private func load() async {
        guard let client = state.client else { return }
        streams = await Loadable { try await client.listConnectionStreams(path: .init(id: connection.id)).ok.body.json.streams }
    }

    private func reset(_ name: String) async {
        guard let client = state.client else { return }
        problem = nil
        do {
            _ = try await client.resetStreamCursor(path: .init(id: connection.id, stream: name)).ok
        } catch {
            problem = Problem(error)
        }
        await load()
    }
}

private struct StreamRow: View {
    let stream: ConnectionStream
    let canReset: Bool
    let reset: () async -> Void
    @State private var confirming = false

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(stream.name).font(.subheadline.monospaced().weight(.semibold))
            HealthBadge(health: stream.health).font(.subheadline)
            if let reason = stream.statusReason ?? stream.healthReason {
                Text(reason).font(.footnote).foregroundStyle(.secondary)
            }
            LabeledContent("Newest data", value: SourcesCopy.when(stream.highWatermark)).font(.footnote)
            LabeledContent("Cursor", value: stream.hasCursor ? "Saved, \(SourcesCopy.ago(stream.updatedAt))" : "None (starts from the initial window)")
                .font(.footnote)
                .accessibilityIdentifier("cursor-\(stream.name)")
            ForEach(stream.schedules, id: \.id) { schedule in
                Text("\(schedule.mode.rawValue) \(SourcesCopy.every(schedule.intervalSeconds))\(schedule.enabled ? "" : " (off)")")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            if canReset {
                Button("Reset cursor") { confirming = true }
                    .buttonStyle(.bordered)
                    .accessibilityLabel("Reset cursor of \(stream.name)")
                    .accessibilityIdentifier("resetCursor-\(stream.name)")
                    .confirmationDialog("Reset the cursor of \(stream.name)?", isPresented: $confirming, titleVisibility: .visible) {
                        Button("Reset cursor") { Task { await reset() } }
                            .accessibilityIdentifier("confirmResetCursor")
                    } message: {
                        Text("The next sync fetches this stream again from the connector's initial window. Stored data stays, and records fetched again are deduplicated.")
                    }
            }
        }
        .padding(.vertical, 2)
    }
}
