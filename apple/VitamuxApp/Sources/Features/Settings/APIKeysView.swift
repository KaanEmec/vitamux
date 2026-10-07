import Foundation
import Observation
import SwiftUI
import VitamuxKit

/// Settings › API keys (the panel's `/settings/api-keys`): the keys with their state, a new key
/// with name, scopes and expiry whose secret is shown once (in the sheet only, never kept), and
/// revoke.
@Observable
final class APIKeysModel {
    typealias Key = Components.Schemas.APIKey
    typealias Scope = Components.Schemas.Scope

    private(set) var keys: Loadable<[APIKeysModel.Key]> = .loading
    private(set) var notice: String?
    private(set) var problem: Problem?
    private(set) var isBusy = false

    func load(_ client: Client?) async {
        guard let client else { return }
        keys = await Loadable { try await client.listAPIKeys().ok.body.json.apiKeys }
    }

    /// Creates the key and answers its secret for the sheet to show once.
    func create(name: String, scopes: [APIKeysModel.Scope], expires: Date?, client: Client?) async -> Result<String, Problem> {
        guard let client else { return .failure(Problem(title: "Signed out")) }
        isBusy = true
        defer { isBusy = false }
        let body = Operations.CreateAPIKey.Input.Body.JsonPayload(
            name: name.trimmingCharacters(in: .whitespaces), scopes: scopes, expiresAt: expires
        )
        do {
            let created = try await client.createAPIKey(body: .json(body)).created.body.json
            notice = nil
            await load(client)
            return .success(created.value2.token)
        } catch {
            return .failure(Problem(error))
        }
    }

    func revoke(_ key: APIKeysModel.Key, client: Client?) async {
        guard let client else { return }
        isBusy = true
        defer { isBusy = false }
        problem = nil
        notice = nil
        do {
            _ = try await client.revokeAPIKey(path: .init(id: key.id)).noContent
            notice = "Revoked key \"\(key.name)\"."
        } catch {
            problem = Problem(error)
        }
        await load(client)
    }

    enum KeyState { case active, expired, revoked }

    static func state(of key: Key, now: Date = .now) -> KeyState {
        if key.revokedAt != nil { return .revoked }
        if let expires = key.expiresAt, expires <= now { return .expired }
        return .active
    }

    static let scopes: [(scope: Scope, hint: String)] = [
        (.read_colon_health, "Read measurements, resolved values, sleep and workouts."),
        (.read_colon_config, "Read rules, settings, connections and status."),
        (.write_colon_config, "Change rules, settings, connections and overrides."),
        (.write_colon_documents, "Upload and review lab documents."),
        (.admin, "Everything, including API keys and full exports."),
    ]
}

struct APIKeysView: View {
    @Environment(AppState.self) private var state
    @State private var model = APIKeysModel()
    @State private var isCreating = false
    @State private var revoking: APIKeysModel.Key?

    var body: some View {
        List {
            Section {
                Text("Keys let scripts and other tools call the API. Each key has only the scopes you give it.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                Button("Create a key", systemImage: "plus") { isCreating = true }
                    .accessibilityIdentifier("createAPIKey")
            }
            Section("Your keys") {
                if let notice = model.notice { NoticeRow(text: notice) }
                if let problem = model.problem { ProblemRow(problem: problem) }
                switch model.keys {
                case .loading: ProgressView()
                case .failed(let problem): ProblemRow(problem: problem)
                case .loaded(let keys) where keys.isEmpty:
                    Text("No API keys yet.").foregroundStyle(.secondary)
                case .loaded(let keys):
                    ForEach(keys, id: \.id) { key in
                        KeyRow(key: key)
                            .swipeActions {
                                if APIKeysModel.state(of: key) != .revoked {
                                    Button("Revoke", role: .destructive) { revoking = key }
                                }
                            }
                            .contextMenu {
                                if APIKeysModel.state(of: key) != .revoked {
                                    Button("Revoke…", systemImage: "xmark.circle", role: .destructive) { revoking = key }
                                }
                            }
                    }
                }
            }
        }
        .navigationTitle("API keys")
        .task { await model.load(state.client) }
        .refreshable { await model.load(state.client) }
        .sheet(isPresented: $isCreating) { NewKeySheet(model: model) }
        .confirmationDialog(
            "Revoke \"\(revoking?.name ?? "")\"?",
            isPresented: Binding { revoking != nil } set: { if !$0 { revoking = nil } },
            titleVisibility: .visible,
            presenting: revoking
        ) { key in
            Button("Revoke key", role: .destructive) {
                Task { await model.revoke(key, client: state.client) }
            }
            .accessibilityIdentifier("confirmRevokeKey")
        } message: { _ in
            Text("Scripts using it stop working at once. This cannot be undone.")
        }
    }
}

private struct KeyRow: View {
    let key: APIKeysModel.Key

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(key.name).font(.headline)
                Spacer()
                switch APIKeysModel.state(of: key) {
                case .active: Label("Active", systemImage: "checkmark.circle").foregroundStyle(Color.feedbackOK)
                case .expired: Label("Expired", systemImage: "exclamationmark.triangle").foregroundStyle(Color.feedbackWarn)
                case .revoked: Label("Revoked", systemImage: "nosign").foregroundStyle(.secondary)
                }
            }
            .font(.subheadline)
            Text(key.scopes.map(\.rawValue).joined(separator: ", ")).font(.caption.monospaced())
            Text("Created \(Format.instant(key.createdAt)) · last used \(key.lastUsedAt.map(Format.instant) ?? "never")")
                .font(.caption)
                .foregroundStyle(.secondary)
            Text(key.expiresAt.map { "Expires \(Format.instant($0))" } ?? "No expiry")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("apiKey-\(key.name)")
    }
}

/// Name, scopes and an optional expiry; once created, the secret is shown here once.
private struct NewKeySheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let model: APIKeysModel
    @State private var name = ""
    @State private var scopes: Set<APIKeysModel.Scope> = [.read_colon_health]
    @State private var expires = false
    @State private var expiry = Calendar.current.date(byAdding: .month, value: 3, to: .now) ?? .now
    @State private var problem: Problem?
    /// The created key's secret: in this view only, gone when the sheet closes.
    @State private var secret: String?

    var body: some View {
        NavigationStack {
            Form {
                if let secret {
                    Section {
                        SecretValue(label: "API key secret", value: secret)
                    } header: {
                        Text("Key \"\(name)\" created")
                    } footer: {
                        Text("Copy the secret now. It is shown once and cannot be retrieved later.")
                    }
                } else {
                    form
                }
            }
            .navigationTitle(secret == nil ? "New API key" : "Save the secret")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { toolbar }
            .interactiveDismissDisabled(secret != nil)
        }
        .sheetBackground()
    }

    @ViewBuilder private var form: some View {
        Section {
            TextField("What the key is for, such as “home script”", text: $name)
                .accessibilityIdentifier("keyName")
            FieldMessage(text: problem?.detail(for: "/name"))
        } header: {
            Text("Name")
        }
        Section {
            ForEach(APIKeysModel.scopes, id: \.scope) { item in
                Toggle(isOn: Binding { scopes.contains(item.scope) } set: { on in
                    if on { scopes.insert(item.scope) } else { scopes.remove(item.scope) }
                }) {
                    VStack(alignment: .leading) {
                        Text(item.scope.rawValue).font(.body.monospaced())
                        Text(item.hint).font(.caption).foregroundStyle(.secondary)
                    }
                }
                .accessibilityIdentifier("scope-\(item.scope.rawValue)")
            }
            FieldMessage(text: problem?.detail(for: "/scopes"))
        } header: {
            Text("Scopes")
        }
        Section {
            Toggle("Expires", isOn: $expires).accessibilityIdentifier("keyExpires")
            if expires {
                DatePicker("Expires on", selection: $expiry, in: Date.now..., displayedComponents: .date)
            }
            FieldMessage(text: problem?.detail(for: "/expires_at"))
        }
        if let problem, problem.fieldErrors.isEmpty {
            Section { ProblemRow(problem: problem) }
        }
    }

    @ToolbarContentBuilder private var toolbar: some ToolbarContent {
        if secret == nil {
            ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            ToolbarItem(placement: .confirmationAction) {
                Button("Create") {
                    Task {
                        // The end of the chosen day, as the panel sends it.
                        let end = Calendar.current.date(bySettingHour: 23, minute: 59, second: 59, of: expiry)
                        let ordered = APIKeysModel.scopes.map(\.scope).filter(scopes.contains)
                        switch await model.create(name: name, scopes: ordered, expires: expires ? end : nil, client: state.client) {
                        case .success(let token): secret = token
                        case .failure(let failure): problem = failure
                        }
                    }
                }
                .disabled(model.isBusy || scopes.isEmpty || name.trimmingCharacters(in: .whitespaces).isEmpty)
                .accessibilityIdentifier("saveAPIKey")
            }
        } else {
            ToolbarItem(placement: .confirmationAction) {
                Button("I have saved it") {
                    secret = nil
                    dismiss()
                }
                .accessibilityIdentifier("savedSecret")
            }
        }
    }
}
