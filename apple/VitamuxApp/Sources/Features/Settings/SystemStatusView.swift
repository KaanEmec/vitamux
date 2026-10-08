import SwiftUI
import VitamuxKit

/// The worked example's screen (`Route.settings(.system)`, `vitamux://settings/system`). It owns its
/// model as `@State`, loads in `.task`, and switches on the model's `Loadable`s: versions (this app,
/// the server and its API), storage, degraded connections and failing jobs.
struct SystemStatusView: View {
    @Environment(AppState.self) private var state
    @State private var model = SystemStatusModel()

    var body: some View {
        List {
            switch model.versions {
            case .loading: ProgressView().accessibilityLabel("Loading")
            case .loaded(let versions): VersionsSection(versions: versions, server: model.server, status: model.status.value)
            case .failed(let problem): ProblemView(problem: problem)
            }
            switch model.status {
            case .loading:
                ProgressView().accessibilityLabel("Loading")
            case .failed:
                Label("System status is not available from this server yet.", systemImage: "info.circle")
                    .foregroundStyle(.secondary)
            case .loaded(let status):
                StorageSection(status: status)
                DegradedSection(connections: status.degradedConnections)
                FailingSection(jobs: status.failingJobs)
            }
        }
        .navigationTitle("System status")
        .task { await model.load(state.client) }
        .refreshable { await model.load(state.client) }
    }
}

private struct VersionsSection: View {
    let versions: SystemStatusModel.Versions
    let server: Loadable<Components.Schemas.SystemVersion>
    let status: Components.Schemas.SystemStatus?

    var body: some View {
        Section("Versions") {
            LabeledContent("App", value: "\(versions.app) (\(versions.build))")
                .accessibilityIdentifier("appVersion")
            switch server {
            case .loading:
                EmptyView()
            case .failed(let problem):
                ProblemRow(problem: problem)
            case .loaded(let server):
                LabeledContent("Vitamux server", value: server.version ?? "–")
                    .accessibilityIdentifier("serverVersion")
                if let commit = server.commit { LabeledContent("Commit", value: commit) }
                LabeledContent("API version", value: String(server.apiVersion))
                    .accessibilityIdentifier("apiVersion")
            }
            if let versions = status?.versions {
                LabeledContent("Schema", value: versions.schema)
                LabeledContent("PostgreSQL", value: versions.postgres)
            }
        }
    }
}

private struct StorageSection: View {
    let status: Components.Schemas.SystemStatus

    var body: some View {
        Section("Storage") {
            LabeledContent("Database", value: SettingsFormat.bytes(status.databaseSizeBytes))
                .accessibilityIdentifier("databaseSize")
            LabeledContent("Blobs (PDFs, raw files, exports)", value: SettingsFormat.bytes(status.blobSizeBytes))
            LabeledContent("Last backup", value: status.lastBackupAt.map(Format.instant) ?? "None recorded")
        }
    }
}

private struct DegradedSection: View {
    let connections: [Components.Schemas.StatusConnection]

    var body: some View {
        Section("Degraded connections") {
            if connections.isEmpty {
                Label("No connection is degraded.", systemImage: "checkmark.circle")
                    .accessibilityIdentifier("noDegraded")
            }
            ForEach(connections, id: \.id) { connection in
                NavigationLink(value: Route.connection(id: connection.id)) {
                    VStack(alignment: .leading, spacing: 2) {
                        Label(providerLabel(connection.provider), systemImage: "exclamationmark.triangle")
                        Text([connection.health.rawValue, connection.stream, connection.healthReason].compactMap(\.self).joined(separator: " · "))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
    }
}

private struct FailingSection: View {
    let jobs: [Components.Schemas.StatusJob]

    var body: some View {
        Section("Failing jobs") {
            if jobs.isEmpty {
                Label("No job is failing.", systemImage: "checkmark.circle")
                    .accessibilityIdentifier("noFailing")
            }
            ForEach(jobs, id: \.id) { job in
                VStack(alignment: .leading, spacing: 2) {
                    Label(job.kind, systemImage: "xmark.octagon").foregroundStyle(Color.feedbackError)
                    Text(["\(job.attempts) attempts", job.errorClass, job.finishedAt.map(Format.instant)].compactMap(\.self).joined(separator: " · "))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }
}
