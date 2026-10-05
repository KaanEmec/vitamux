import SwiftUI
import VitamuxKit

/// Sync and backfill runs of a connection, newest first, 50 at a time.
struct HistoryTab: View {
    @Environment(AppState.self) private var state
    @State private var runs: Loadable<[Run]> = .loading
    @State private var next: String?
    @State private var busy = false
    let connection: Connection

    private static let pageSize = 50

    var body: some View {
        Section("Runs") {
            switch runs {
            case .loading:
                ProgressView("Loading history")
            case .failed(let problem):
                ProblemRow(problem: problem)
            case .loaded(let list):
                if list.isEmpty { Text("No runs yet.").foregroundStyle(.secondary) }
                ForEach(list, id: \.id) { RunRow(run: $0) }
                if let next {
                    Button("Load older runs") { Task { await load(cursor: next) } }
                        .disabled(busy)
                        .accessibilityIdentifier("loadOlderRuns")
                }
            }
        }
        .task { await load(cursor: nil) }
    }

    private func load(cursor: String?) async {
        guard let client = state.client else { return }
        busy = true
        defer { busy = false }
        do {
            let page = try await client.listConnectionRuns(path: .init(id: connection.id), query: .init(limit: Self.pageSize, cursor: cursor)).ok.body.json
            runs = .loaded((cursor == nil ? [] : runs.value ?? []) + page.value2.runs)
            next = page.value1.hasMore ? page.value1.nextCursor : nil
        } catch {
            if runs.value == nil { runs = .failed(Problem(error)) }
        }
    }
}
