import SwiftUI
import VitamuxKit

/// Tab root (`vitamux://connections`, artboard Sources): the summary, the result banner of an
/// OAuth return (`?connected=`, `?auth_error=&provider=`) or a removal (`?removed=`), "This
/// iPhone", one card per connection, Connect a source, running backfills and the latest runs.
struct SourcesView: View {
    @Environment(AppState.self) private var state
    @State private var model = SourcesModel()
    @State private var connect: ConnectStart?
    let back: Route.ConnectionsReturn

    var body: some View {
        List {
            Section {
                Text(model.summary)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("sourcesSummary")
                ReturnBanner(back: back, providers: model.providers, connect: $connect)
            }
            Section {
                ThisIPhoneCard()
            }
            switch model.connections {
            case .loading:
                ProgressView("Loading sources").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let list):
                ForEach(list, id: \.id) { connection in
                    ConnectionCard(connection: connection, runs: model.runs[connection.id] ?? .loading) { changed in
                        await model.replace(changed, client: state.client)
                    }
                }
                Section {
                    Button("Connect a source", systemImage: "plus") { connect = ConnectStart() }
                        .accessibilityIdentifier("connectSource")
                } footer: {
                    if list.isEmpty {
                        Text("Pick a source. Each one shows what it needs, and every step happens here on the phone.")
                    } else {
                        Text("Guided setup for Withings, Garmin, WHOOP or any sidecar collector.")
                    }
                }
                if !list.isEmpty {
                    RunningBackfills(running: model.running)
                    RecentRuns(recent: model.recent)
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Sources")
        .refreshable { await model.load(state.client) }
        .task(id: back) { await model.load(state.client) }
        .sheet(item: $connect, onDismiss: { Task { await model.load(state.client) } }) { start in
            ConnectSheet(start: start)
        }
    }
}

/// The outcome of an OAuth round trip or a removal, until dismissed.
private struct ReturnBanner: View {
    @Environment(AppState.self) private var state
    let back: Route.ConnectionsReturn
    let providers: [Provider]
    @Binding var connect: ConnectStart?

    var body: some View {
        if let connected = back.connected {
            banner(.ok, "\(providerLabel(connected)) is connected. A first sync has been queued.", id: "bannerConnected")
        } else if let code = back.authError {
            let name = back.provider.map { " " + providerLabel($0) } ?? ""
            VStack(alignment: .leading, spacing: 8) {
                NoticeRow(text: "Connecting\(name) failed: \(SourcesCopy.authError(code))" + (appSetup ? " Check the client id, the secret and the callback URL of your\(name) app." : ""),
                          kind: .error, identifier: "bannerAuthError")
                HStack {
                    Button(appSetup ? "Review the app setup" : "Try again") {
                        connect = ConnectStart(provider: back.provider, app: appSetup)
                    }
                    .accessibilityIdentifier("bannerRetry")
                    Spacer()
                    dismiss
                }
                .buttonStyle(.borderless)
            }
        } else if let removed = back.removed {
            banner(.info, "The \(providerLabel(removed)) connection was removed.", id: "bannerRemoved")
        }
    }

    /// A refused exchange with the owner's own app usually means a wrong secret or callback URL.
    private var appSetup: Bool {
        guard back.authError == "exchange_failed", let app = providers.first(where: { $0.code == back.provider })?.appCredentials else { return false }
        return app.set && !app.managedByEnvironment
    }

    private func banner(_ kind: StatusKind, _ text: String, id: String) -> some View {
        HStack {
            NoticeRow(text: text, kind: kind, identifier: id)
            Spacer()
            dismiss.buttonStyle(.borderless)
        }
    }

    private var dismiss: some View {
        Button("Dismiss") { state.open(.connections()) }
            .font(.subheadline)
            .accessibilityIdentifier("bannerDismiss")
    }
}

/// Apple Health on this iPhone: the pairing HealthBridgeKit keeps (sync status arrives with J22.14).
private struct ThisIPhoneCard: View {
    @Environment(AppState.self) private var state

    var body: some View {
        Button { state.open(.appleHealth) } label: {
            HStack(spacing: 12) {
                Image(systemName: "iphone")
                    .font(.title2)
                    .foregroundStyle(SourceStyle.color("apple_health"))
                    .frame(width: 40, height: 40)
                    .background(SourceStyle.color("apple_health").opacity(0.16), in: .rect(cornerRadius: 11))
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Apple Health · this iPhone").font(.headline)
                    Text(paired ? "Paired with this server" : "Not paired yet: set up Apple Health sync")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Image(systemName: "chevron.right").font(.footnote.weight(.semibold)).foregroundStyle(.tertiary)
            }
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("thisIPhone")
    }

    private var paired: Bool {
        guard let credentials = state.device.credentials, let profile = state.profile else { return false }
        return (try? ServerProfile(credentials.baseURL.absoluteString))?.baseURL == profile.baseURL
    }
}

private struct RunningBackfills: View {
    @Environment(AppState.self) private var state
    let running: [SourcesModel.Running]

    var body: some View {
        Section("Backfill in progress") {
            if running.isEmpty {
                Text("No backfill is running. A backfill fetches history older than the regular sync window.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            ForEach(running) { item in
                Button { state.open(.connection(id: item.connection.id, tab: "backfills")) } label: {
                    VStack(alignment: .leading, spacing: 6) {
                        HStack {
                            Text("\(providerLabel(item.connection.provider)) · \(item.backfill.stream)").font(.subheadline.weight(.semibold))
                            Spacer()
                            Text(Double(item.backfill.unitCounts.done) / Double(max(item.backfill.total, 1)), format: .percent.precision(.fractionLength(0)))
                                .font(.subheadline)
                                .foregroundStyle(.secondary)
                        }
                        ProgressView(value: Double(item.backfill.unitCounts.done), total: Double(max(item.backfill.total, 1)))
                        Text(item.backfill.progressText + " · resumes after restarts").font(.caption).foregroundStyle(.secondary)
                    }
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("runningBackfill-\(item.backfill.stream)")
            }
        }
    }
}

private struct RecentRuns: View {
    let recent: [SourcesModel.Recent]

    var body: some View {
        Section("Recent sync runs") {
            if recent.isEmpty {
                Text("No runs in the last 14 days.").font(.subheadline).foregroundStyle(.secondary)
            }
            ForEach(recent) { item in
                RunRow(run: item.run, provider: item.connection.provider)
            }
        }
    }
}

/// One run: outcome, kind, when and how long, with its error class.
struct RunRow: View {
    let run: Run
    var provider: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 6) {
                StatusIcon(kind: StatusKind(outcome: run.outcome))
                if let provider {
                    SourceDot(provider: provider)
                    Text(providerLabel(provider)).font(.subheadline.weight(.semibold))
                }
                Text(run.kind).font(.subheadline.monospaced())
                if run.attempt > 1 { Text("attempt \(run.attempt)").font(.caption).foregroundStyle(.secondary) }
                Spacer()
                Text(run.outcome ?? "running").font(.subheadline)
            }
            Text("\(SourcesCopy.when(run.startedAt)) · \(SourcesCopy.elapsed(run.startedAt, run.finishedAt))")
                .font(.caption)
                .foregroundStyle(.secondary)
            if let error = run.errorClass {
                Text([error, run.errorMessage].compactMap(\.self).joined(separator: " · "))
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
            }
        }
        .accessibilityElement(children: .combine)
    }
}
