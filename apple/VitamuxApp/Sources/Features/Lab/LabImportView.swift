import Foundation
import SwiftUI
import VitamuxKit

/// A PDF shared to Vitamux from another app ("Open in Vitamux", `Route.labImport`): it is checked
/// and uploaded like a file from Files, then opens its review. iOS hands the app a copy in its
/// Documents/Inbox folder; that copy is deleted once read, so no PDF stays on the phone.
struct LabImportView: View {
    @Environment(AppState.self) private var state
    let file: URL
    @State private var outcome: Loadable<UploadOutcome> = .loading
    @State private var started = false

    var body: some View {
        List {
            Section {
                Text(file.lastPathComponent).font(.headline)
                switch outcome {
                case .loading:
                    ProgressView("Uploading…").accessibilityIdentifier("uploading")
                case .failed(let problem):
                    ProblemView(problem: problem).accessibilityIdentifier("labProblem")
                case .loaded(let outcome):
                    LabNotice(text: outcome.notice(name: file.lastPathComponent))
                    Button("Open the document") {
                        state.paths[.lab] = [.labDocument(id: outcome.document.id)]
                    }
                    .accessibilityIdentifier("openImported")
                }
            } footer: {
                Text("The PDF is sent to your Vitamux server only. Nothing is extracted until you choose Extract.")
            }
        }
        .navigationTitle("Import")
        .task {
            guard !started, let client = state.client else { return }
            started = true
            outcome = await Loadable { try await LabUpload.send(file: file, client: client) }
            removeInboxCopy()
        }
    }

    /// Deletes the copy iOS placed in this app's Inbox; a file opened elsewhere is left alone.
    private func removeInboxCopy() {
        guard let documents = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first else { return }
        let inbox = documents.appending(path: "Inbox", directoryHint: .isDirectory).standardizedFileURL.path
        if file.standardizedFileURL.path.hasPrefix(inbox + "/") { try? FileManager.default.removeItem(at: file) }
    }
}
