import Foundation
import Observation
import SwiftUI
import VitamuxKit

/// Settings › Retention (the panel's `/settings/retention`): how long lab PDFs, raw provider
/// payloads (per provider), superseded rows and ingest replay records are kept. Typed controls for
/// every key the API defines; the save sends only what changed, after a confirmation, because
/// pruned data cannot be recovered.
@Observable
final class RetentionModel {
    struct RawRow: Identifiable, Equatable {
        var provider: String
        var days: String
        var id: String { provider }
    }

    private(set) var saved: Loadable<ServerSettings> = .loading
    var documentDays = ""
    var deleteOriginal = false
    var rawRows: [RawRow] = []
    var newProvider = ""
    var supersededDays = ""
    var idempotencyDays = ""
    /// Local and server messages by JSON pointer.
    private(set) var errors: [String: String] = [:]
    private(set) var problem: Problem?
    private(set) var done = false
    private(set) var isBusy = false

    func load(_ client: Client?) async {
        guard let client else { return }
        saved = await Loadable { try await client.getSettings().ok.body.json }
        if let settings = saved.value { adopt(settings) }
    }

    private func adopt(_ settings: ServerSettings) {
        saved = .loaded(settings)
        documentDays = settings.documents_retentionDays.map(String.init) ?? ""
        deleteOriginal = settings.documents_deleteOriginalAfterConfirmation ?? false
        rawRows = (settings.retention_rawDays?.additionalProperties ?? [:])
            .sorted { $0.key < $1.key }
            .map { RawRow(provider: $0.key, days: String($0.value)) }
        supersededDays = settings.retention_supersededAfterDays.map(String.init) ?? ""
        idempotencyDays = settings.retention_idempotencyKeyDays.map(String.init) ?? ""
    }

    func addProvider() {
        let code = newProvider.trimmingCharacters(in: .whitespaces).lowercased()
        if !code.isEmpty, !rawRows.contains(where: { $0.provider == code }) {
            rawRows.append(RawRow(provider: code, days: "0"))
        }
        newProvider = ""
        done = false
    }

    func keepAll(_ provider: String) {
        rawRows.removeAll { $0.provider == provider }
        done = false
    }

    /// The patch of changed keys, or nil when a field is invalid (`errors` says which).
    func patch() -> ServerSettings? {
        guard let saved = saved.value else { return nil }
        errors = [:]
        var patch = ServerSettings()
        let documents = documentDays.trimmingCharacters(in: .whitespaces)
        if documents.isEmpty {
            if saved.documents_retentionDays != nil {
                errors["/documents.retention_days"] = "The app cannot clear this yet; set a number of days, or clear it in the web panel to keep PDFs again."
            }
        } else if let days = whole("/documents.retention_days", documents, 1...36_500), days != saved.documents_retentionDays {
            patch.documents_retentionDays = days
        }
        if deleteOriginal != (saved.documents_deleteOriginalAfterConfirmation ?? false) {
            patch.documents_deleteOriginalAfterConfirmation = deleteOriginal
        }
        // The server merges per provider; a provider removed from the list is reset to 0 (keep).
        let before = saved.retention_rawDays?.additionalProperties ?? [:]
        var raw: [String: Int] = [:]
        for row in rawRows {
            if let days = whole("/retention.raw_days.\(row.provider)", row.days.isEmpty ? "0" : row.days, 0...36_500), days != before[row.provider] {
                raw[row.provider] = days
            }
        }
        for (provider, days) in before where days != 0 && !rawRows.contains(where: { $0.provider == provider }) {
            raw[provider] = 0
        }
        if !raw.isEmpty { patch.retention_rawDays = .init(additionalProperties: raw) }
        if let days = whole("/retention.superseded_after_days", supersededDays.isEmpty ? "0" : supersededDays, 0...36_500),
           days != (saved.retention_supersededAfterDays ?? 0) {
            patch.retention_supersededAfterDays = days
        }
        if !idempotencyDays.isEmpty, let days = whole("/retention.idempotency_key_days", idempotencyDays, 7...36_500),
           days != saved.retention_idempotencyKeyDays {
            patch.retention_idempotencyKeyDays = days
        }
        return errors.isEmpty ? patch : nil
    }

    func save(_ patch: ServerSettings, client: Client?) async {
        guard let client else { return }
        problem = nil
        done = false
        guard patch != ServerSettings() else {
            done = true
            return
        }
        isBusy = true
        defer { isBusy = false }
        do {
            adopt(try await client.patch(patch))
            done = true
        } catch {
            let failure = Problem(error)
            problem = failure
            for field in failure.fieldErrors { errors[field.pointer] = field.detail }
        }
    }

    func error(_ pointer: String) -> String? { errors[pointer] }

    private func whole(_ pointer: String, _ text: String, _ range: ClosedRange<Int>) -> Int? {
        guard let value = Int(text.trimmingCharacters(in: .whitespaces)), range.contains(value) else {
            errors[pointer] = "Enter a whole number from \(range.lowerBound) to \(range.upperBound)."
            return nil
        }
        return value
    }
}

struct RetentionView: View {
    @Environment(AppState.self) private var state
    @State private var model = RetentionModel()
    @State private var pending: ServerSettings?
    @FocusState private var focus: String?

    var body: some View {
        @Bindable var model = model
        Form {
            Section {
                Text("Pruned data cannot be recovered. Leave a field empty or at 0 to keep that data. Deletions run in the background after you save.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                if model.done { NoticeRow(text: "Saved.") }
                if let problem = model.problem, problem.fieldErrors.isEmpty { ProblemRow(problem: problem) }
            }
            switch model.saved {
            case .loading:
                ProgressView()
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded:
                Section {
                    DaysField(title: "Delete originals after (days)", text: $model.documentDays, id: "documentDays",
                              error: model.error("/documents.retention_days"), focus: $focus)
                    Toggle("Delete the PDF once its extraction is confirmed", isOn: $model.deleteOriginal)
                        .accessibilityIdentifier("deleteOriginal")
                    FieldMessage(text: model.error("/documents.delete_original_after_confirmation"))
                } header: {
                    Text("Lab PDFs")
                } footer: {
                    Text("Counted from upload, for every stored document. Empty keeps them. Confirmed results are kept either way.")
                }
                rawSection
                Section {
                    DaysField(title: "Keep superseded rows (days)", text: $model.supersededDays, id: "supersededDays",
                              error: model.error("/retention.superseded_after_days"), focus: $focus)
                    DaysField(title: "Keep ingest replay records (days)", text: $model.idempotencyDays, id: "idempotencyDays",
                              error: model.error("/retention.idempotency_key_days"), focus: $focus)
                } header: {
                    Text("Replaced and replayed records")
                } footer: {
                    Text("Superseded rows are older versions of corrected records; 0 or empty keeps them. Replay records are stored responses for repeated uploads (Idempotency-Key): at least 7 days, 30 by default.")
                }
                Section {
                    Button("Save retention") {
                        focus = nil
                        if let patch = model.patch() { pending = patch }
                    }
                    .disabled(model.isBusy)
                    .accessibilityIdentifier("saveRetention")
                }
            }
        }
        .navigationTitle("Retention")
        .task { await model.load(state.client) }
        .confirmationDialog(
            "Save retention?",
            isPresented: Binding { pending != nil } set: { if !$0 { pending = nil } },
            titleVisibility: .visible,
            presenting: pending
        ) { patch in
            Button("Save and prune", role: .destructive) {
                Task { await model.save(patch, client: state.client) }
            }
            .accessibilityIdentifier("confirmSaveRetention")
        } message: { _ in
            Text("Data older than the new limits is deleted in the background and cannot be recovered.")
        }
    }

    @ViewBuilder private var rawSection: some View {
        @Bindable var model = model
        Section {
            Label("Pruned raw payloads can no longer be reprocessed: normalization fixes and new rules cannot be re-run on them. The server still keeps raw that reprocessing needs.", systemImage: "exclamationmark.triangle")
                .font(.footnote)
                .foregroundStyle(Color.feedbackWarn)
            ForEach($model.rawRows) { $row in
                VStack(alignment: .leading) {
                    HStack {
                        Text(SettingsFormat.provider(row.provider))
                        Spacer()
                        TextField("Days", text: $row.days)
                            .keyboardType(.numberPad)
                            .multilineTextAlignment(.trailing)
                            .frame(maxWidth: 80)
                            .focused($focus, equals: "rawDays-\(row.provider)")
                            .accessibilityLabel("Days to keep raw payloads for \(row.provider)")
                            .accessibilityIdentifier("rawDays-\(row.provider)")
                        Button("Keep all") { model.keepAll(row.provider) }
                            .buttonStyle(.borderless)
                            .accessibilityIdentifier("keepAll-\(row.provider)")
                    }
                    FieldMessage(text: model.error("/retention.raw_days.\(row.provider)"))
                }
            }
            FieldMessage(text: model.error("/retention.raw_days"))
            HStack {
                TextField("Add a provider, such as withings", text: $model.newProvider)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .accessibilityIdentifier("newRawProvider")
                Button("Add") { model.addProvider() }
                    .disabled(model.newProvider.trimmingCharacters(in: .whitespaces).isEmpty)
                    .accessibilityIdentifier("addRawProvider")
            }
        } header: {
            Text("Raw provider payloads")
        } footer: {
            Text("Days to keep the original responses per provider. 0 or no row keeps them.")
        }
    }
}

/// A number of days, with its message in the same row so a failed check adds no rows.
private struct DaysField: View {
    let title: String
    @Binding var text: String
    let id: String
    let error: String?
    var focus: FocusState<String?>.Binding

    var body: some View {
        VStack(alignment: .leading) {
            LabeledContent(title) {
                TextField("Keep", text: $text)
                    .keyboardType(.numberPad)
                    .multilineTextAlignment(.trailing)
                    .frame(maxWidth: 90)
                    .focused(focus, equals: id)
                    .accessibilityLabel(title)
                    .accessibilityIdentifier(id)
            }
            FieldMessage(text: error)
        }
    }
}
