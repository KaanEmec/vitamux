import AuthenticationServices
import SwiftUI
import VitamuxKit

/// One source on the list (the panel's ConnectionCard.svelte): health, last sync, the 14-day run
/// strip, and its fix-it action beside Sync now: Reauthorize instead when it needs it, See streams
/// for a degraded or failing one.
struct ConnectionCard: View {
    @Environment(AppState.self) private var state
    @State private var actions = ConnectionActions()
    let connection: Connection
    let runs: Loadable<[Run]>
    let onChange: (Connection) async -> Void

    var body: some View {
        Section {
            NavigationLink(value: Route.connection(id: connection.id)) {
                VStack(alignment: .leading, spacing: 10) {
                    HStack(spacing: 12) {
                        SourceMonogram(provider: connection.provider)
                        VStack(alignment: .leading, spacing: 2) {
                            HStack(spacing: 6) {
                                Text(providerLabel(connection.provider)).font(.headline)
                                if connection.official == false { UnofficialBadge() }
                            }
                            Text(SourcesCopy.mode(connection.mode)).font(.caption).foregroundStyle(.secondary)
                        }
                    }
                    HStack(spacing: 8) {
                        HealthBadge(health: connection.health).font(.subheadline.weight(.semibold))
                        Text(SourcesCopy.lastSync(connection)).font(.subheadline).foregroundStyle(.secondary)
                    }
                    if let reason = connection.healthReason {
                        Text(reason).font(.footnote).foregroundStyle(.secondary)
                    }
                    if connection.mode != .push { RunStrip(runs: runs) }
                }
                .padding(.vertical, 4)
            }
            .accessibilityIdentifier("connection-\(connection.provider)")
            if connection.mode != .push {
                ActionsRow(connection: connection, actions: actions, compact: true, onChange: onChange)
            }
        }
        .listSectionSpacing(.compact)
    }
}

/// Sync now and Reauthorize, with what they answered. `compact` (list cards) shows only the fix-it
/// action a connection needs beside Sync now; the detail header shows both.
struct ActionsRow: View {
    @Environment(AppState.self) private var state
    @Environment(\.webAuthenticationSession) private var session
    let connection: Connection
    let actions: ConnectionActions
    var compact = false
    let onChange: (Connection) async -> Void

    private var reauth: Bool { connection.health == .needsReauth }
    private var drifting: Bool { connection.health == .degraded || connection.health == .failing }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 12) {
                if !(compact && reauth) {
                    Button("Sync now", systemImage: "arrow.triangle.2.circlepath") {
                        Task {
                            if let changed = await actions.sync(connection, client: state.client) { await onChange(changed) }
                        }
                    }
                    .disabled(actions.busy || connection.status == .paused)
                    .accessibilityIdentifier("syncNow-\(connection.provider)")
                }
                if !compact || reauth {
                    Button("Reauthorize") {
                        Task { await actions.reauthorize(connection, state: state, browser: AuthBrowser(state: state, session: session)) }
                    }
                    .disabled(actions.busy)
                    .accessibilityIdentifier("reauthorize-\(connection.provider)")
                }
                if compact && drifting {
                    Button("See streams") { state.open(.connection(id: connection.id, tab: "streams")) }
                        .accessibilityIdentifier("seeStreams-\(connection.provider)")
                }
                if actions.busy { ProgressView() }
            }
            .buttonStyle(.bordered)
            if reauth && compact {
                Text("Sign in again to resume syncing. Stored data is unaffected.").font(.footnote).foregroundStyle(.secondary)
            }
            if !compact && connection.status == .paused {
                Text("Paused: resume it in Settings to sync.").font(.footnote).foregroundStyle(.secondary)
            }
            if let problem = actions.problem { ProblemRow(problem: problem, lead: actions.lead) }
            if let queued = actions.queuedText { NoticeRow(text: queued, identifier: "syncQueued") }
        }
        // On this one row, not on a Section, so the list presents it once.
        .sheet(item: Bindable(actions).prompt) { prompt in
            PromptSheetView(prompt: prompt, title: "Reauthorize \(providerLabel(prompt.provider))") {
                actions.prompt = nil
            } onDone: { _ in
                actions.prompt = nil
                if let refreshed = try? await state.client?.getConnection(path: .init(id: connection.id)).ok.body.json {
                    await onChange(refreshed)
                }
            }
        }
    }
}

/// A prompt step in its own sheet (reauthorizing a connection).
struct PromptSheetView: View {
    let prompt: PromptSheet
    let title: String
    let onClose: () -> Void
    let onDone: (String) async -> Void

    var body: some View {
        NavigationStack {
            AuthPromptView(provider: prompt.provider, step: prompt.step, onRestart: onClose) { id in
                Task { await onDone(id) }
            }
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel", action: onClose) }
            }
        }
    }
}
