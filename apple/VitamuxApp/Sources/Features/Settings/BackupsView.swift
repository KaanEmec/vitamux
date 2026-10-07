import Foundation
import Observation
import SwiftUI
import VitamuxKit

/// Settings › Backups and export (the panel's `/settings/backups`): the last backup
/// (`GET /system/status`; backups run with `vitamux backup`), and an export as NDJSON or CSV with or
/// without raw (`POST /exports`), polled every second, downloaded with the bearer session to a
/// private temporary file and handed to the share sheet or Files. The file is deleted when the
/// page closes.
@Observable
final class ExportModel {
    typealias Export = Components.Schemas.Export
    typealias Format = Components.Schemas.ExportRequest.FormatPayload

    private(set) var lastBackup: Loadable<Date?> = .loading
    var format = Format.ndjson
    var includeRaw = false
    private(set) var job: Export?
    private(set) var file: URL?
    private(set) var problem: Problem?
    private(set) var isBusy = false

    var isRunning: Bool { job?.status == .queued || job?.status == .running }

    func load(_ client: Client?) async {
        guard let client else { return }
        lastBackup = await Loadable { try await client.getSystemStatus().ok.body.json.lastBackupAt }
    }

    func start(_ client: Client?) async {
        guard let client else { return }
        discardFile()
        isBusy = true
        defer { isBusy = false }
        problem = nil
        do {
            job = try await client.createExport(body: .json(.init(format: format, includeRaw: includeRaw))).accepted.body.json
        } catch {
            problem = Problem(error)
        }
    }

    /// One poll; `Poller.export` repeats it while the export runs.
    func poll(_ client: Client?) async {
        guard let client, let id = job?.id else { return }
        do {
            job = try await client.getExport(path: .init(id: id)).ok.body.json
        } catch {
            problem = Problem(error)
            job = nil
        }
    }

    /// Streams the zip through its one-time link into a file only this app can read.
    func download(_ client: Client?) async {
        guard let client, let job, let link = job.downloadUrl,
              let token = URLComponents(string: link)?.queryItems?.first(where: { $0.name == "token" })?.value
        else { return }
        isBusy = true
        defer { isBusy = false }
        problem = nil
        let name = "vitamux-export-\(job.createdAt.formatted(.iso8601.year().month().day())).zip"
        let target = FileManager.default.temporaryDirectory.appending(path: name)
        do {
            let body = try await client.downloadExport(path: .init(id: job.id), query: .init(token: token)).ok.body.applicationZip
            try? FileManager.default.removeItem(at: target)
            guard FileManager.default.createFile(atPath: target.path(), contents: nil, attributes: [.protectionKey: FileProtectionType.complete]) else {
                throw Problem(title: "Can't save the export", detail: "There is no room for it on this iPhone.")
            }
            let handle = try FileHandle(forWritingTo: target)
            defer { try? handle.close() }
            for try await chunk in body {
                try handle.write(contentsOf: chunk)
            }
            file = target
        } catch {
            try? FileManager.default.removeItem(at: target)
            problem = Problem(error)
        }
    }

    func discardFile() {
        if let file { try? FileManager.default.removeItem(at: file) }
        file = nil
    }
}

struct BackupsView: View {
    @Environment(AppState.self) private var state
    @State private var model = ExportModel()
    @State private var isSavingToFiles = false

    var body: some View {
        @Bindable var model = model
        Form {
            Section {
                switch model.lastBackup {
                case .loading:
                    ProgressView()
                case .loaded(let date?):
                    Label("Last backup: \(Format.instant(date)) (\(Format.ago(date)))", systemImage: "checkmark.circle")
                        .accessibilityIdentifier("lastBackup")
                case .loaded(nil):
                    Label("No backup is recorded.", systemImage: "exclamationmark.triangle")
                        .foregroundStyle(Color.feedbackWarn)
                        .accessibilityIdentifier("lastBackup")
                case .failed(let problem):
                    ProblemRow(problem: problem)
                }
            } header: {
                Text("Backups")
            } footer: {
                Text("Backups restore this server and are made with vitamux backup (see the backup and restore guide). An export below is a portable copy of your data, not a restorable backup.")
            }
            Section {
                Picker("Format", selection: $model.format) {
                    Text("NDJSON, one file per table").tag(ExportModel.Format.ndjson)
                    Text("NDJSON plus measurements.csv").tag(ExportModel.Format.csv)
                }
                .pickerStyle(.inline)
                .labelsHidden()
                .accessibilityIdentifier("exportFormat")
                Toggle(isOn: $model.includeRaw) {
                    VStack(alignment: .leading) {
                        Text("Include raw provider payloads")
                        Text("The original responses as received. Larger, and holds private health data as the providers sent it.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                .accessibilityIdentifier("exportRaw")
                Button("Start export") { Task { await model.start(state.client) } }
                    .disabled(model.isBusy || model.isRunning)
                    .accessibilityIdentifier("startExport")
                if let problem = model.problem { ProblemRow(problem: problem) }
            } header: {
                Text("Export your data")
            } footer: {
                Text("The export is a zip with one file per table and a manifest. It holds all of your health rows, rules and audit history, so keep it private.")
            }
            if let job = model.job {
                JobSection(job: job, file: model.file, isBusy: model.isBusy) {
                    Task { await model.download(state.client) }
                } saveToFiles: {
                    isSavingToFiles = true
                }
            }
        }
        .navigationTitle("Backups and export")
        .task { await model.load(state.client) }
        .task(id: model.job?.id) {
            guard model.isRunning else { return }
            await Poller.export.run {
                await model.poll(state.client)
                return model.isRunning
            }
        }
        .fileMover(isPresented: $isSavingToFiles, file: model.file) { _ in }
        .onDisappear { model.discardFile() }
    }
}

private struct JobSection: View {
    let job: ExportModel.Export
    let file: URL?
    let isBusy: Bool
    let download: () -> Void
    let saveToFiles: () -> Void

    var body: some View {
        Section {
            switch job.status {
            case .queued, .running:
                HStack {
                    ProgressView()
                    Text(job.status == .queued ? "Export queued…" : "Export running…")
                }
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("exportStatus")
            case .failed:
                Label("The export failed. Check the server log, then try again.", systemImage: "xmark.octagon")
                    .foregroundStyle(Color.feedbackError)
                    .accessibilityIdentifier("exportStatus")
            case .done:
                Label("Export ready\(job.sizeBytes.map { " (\(SettingsFormat.bytes(Int64($0))))" } ?? "").", systemImage: "checkmark.circle")
                    .accessibilityIdentifier("exportStatus")
                if let file {
                    ShareLink(item: file) { Label("Share or save the export", systemImage: "square.and.arrow.up") }
                        .accessibilityIdentifier("shareExport")
                    Button("Save to Files", systemImage: "folder", action: saveToFiles)
                        .accessibilityIdentifier("saveExportToFiles")
                } else if job.downloadUrl != nil {
                    Button("Download export", systemImage: "arrow.down.circle", action: download)
                        .disabled(isBusy)
                        .accessibilityIdentifier("downloadExport")
                } else {
                    Text("No download link is available. Start a new export.").foregroundStyle(.secondary)
                }
            }
        } footer: {
            Text(job.status == .done
                 ? "The link works once and expires 10 minutes after it was issued. Start a new export if it has expired. Started \(Format.instant(job.createdAt))."
                 : "Started \(Format.instant(job.createdAt)).")
        }
    }
}
