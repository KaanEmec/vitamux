import Foundation
import Observation
import SwiftUI
import VitamuxKit

/// Settings › Sources (the panel's `/settings/sources`): the source order (`sources.priority`),
/// the owner's applications at providers (`/providers/{provider}/app-credentials`, write-only
/// secrets, verified after saving) and the sidecars (`/sidecars`: added with a secret shown once,
/// or removed). Removing app credentials or a sidecar still in use needs a second confirmation.
@Observable
final class SourcesSettingsModel {
    typealias Provider = Components.Schemas.Provider
    typealias Sidecar = Components.Schemas.Sidecar

    private(set) var order: Loadable<[String]> = .loading
    private(set) var providers: Loadable<[Provider]> = .loading
    private(set) var sidecars: Loadable<[Sidecar]> = .loading
    private(set) var orderSaved = false
    private(set) var orderProblem: Problem?
    private(set) var notice: String?
    private(set) var problem: Problem?
    private(set) var isBusy = false

    func load(_ client: Client?) async {
        guard let client else { return }
        // The saved order, then connected providers not in it; manual entries are always last.
        order = await Loadable {
            var list = try await client.getSettings().ok.body.json.sources_priority ?? []
            for connection in try await client.listConnections().ok.body.json.connections
            where connection.provider != "manual" && !list.contains(connection.provider) {
                list.append(connection.provider)
            }
            return list
        }
        await loadProviders(client)
        await loadSidecars(client)
    }

    private func loadProviders(_ client: Client) async {
        providers = await Loadable { try await client.listProviders().ok.body.json.providers.filter { $0.appCredentials != nil } }
    }

    private func loadSidecars(_ client: Client) async {
        sidecars = await Loadable { try await client.listSidecars().ok.body.json.sidecars }
    }

    func move(_ index: Int, by step: Int) {
        guard var list = order.value, list.indices.contains(index + step) else { return }
        list.swapAt(index, index + step)
        order = .loaded(list)
        orderSaved = false
    }

    func saveOrder(_ client: Client?) async {
        guard let client, let list = order.value else { return }
        isBusy = true
        defer { isBusy = false }
        orderProblem = nil
        do {
            order = .loaded(try await client.patch(.init(sources_priority: list)).sources_priority ?? list)
            orderSaved = true
        } catch {
            orderProblem = Problem(error)
        }
    }

    /// Saves the app credentials, then checks them with the provider; nil once saved.
    func saveCredentials(_ provider: SourcesSettingsModel.Provider, clientID: String, secret: String, client: Client?) async -> Problem? {
        guard let client else { return nil }
        isBusy = true
        defer { isBusy = false }
        notice = nil
        problem = nil
        let path = Operations.PutProviderAppCredentials.Input.Path(provider: provider.code)
        do {
            let body = Components.Schemas.AppCredentialsInput(clientId: clientID.trimmingCharacters(in: .whitespaces), clientSecret: secret.trimmingCharacters(in: .whitespaces))
            _ = try await client.putProviderAppCredentials(path: path, body: .json(body)).ok
        } catch {
            let failure = Problem(error)
            return failure.status == 409 ? Problem(title: failure.title, detail: "The environment sets them, so they cannot be changed here.", status: 409) : failure
        }
        do {
            let check = try await client.verifyProviderAppCredentials(path: .init(provider: provider.code)).ok.body.json
            notice = "Saved the \(provider.name) app credentials. \(check.message)"
        } catch {
            notice = "Saved the \(provider.name) app credentials, but they could not be checked now (\(Problem(error).detail ?? "no answer"))."
        }
        await loadProviders(client)
        return nil
    }

    /// Removes the app credentials; true when connections still use them and `force` is needed.
    func removeCredentials(_ provider: SourcesSettingsModel.Provider, force: Bool, client: Client?) async -> Bool {
        guard let client else { return false }
        isBusy = true
        defer { isBusy = false }
        notice = nil
        problem = nil
        do {
            _ = try await client.deleteProviderAppCredentials(path: .init(provider: provider.code), query: .init(confirm: force ? true : nil)).noContent
            notice = "Removed the \(provider.name) app credentials."
        } catch {
            let failure = Problem(error)
            if failure.status == 409, !force { return true }
            problem = failure
        }
        await loadProviders(client)
        return false
    }

    /// The shared secret for the sheet to show once.
    func addSidecar(name: String, url: String, client: Client?) async -> Result<String, Problem> {
        guard let client else { return .failure(Problem(title: "Signed out")) }
        isBusy = true
        defer { isBusy = false }
        do {
            let body = Operations.CreateSidecar.Input.Body.JsonPayload(name: name.trimmingCharacters(in: .whitespaces), url: url.trimmingCharacters(in: .whitespaces))
            let created = try await client.createSidecar(body: .json(body)).created.body.json
            await loadSidecars(client)
            return .success(created.secret)
        } catch {
            return .failure(Problem(error))
        }
    }

    /// Removes the sidecar; true when its connections still exist and `force` is needed.
    func removeSidecar(_ sidecar: SourcesSettingsModel.Sidecar, force: Bool, client: Client?) async -> Bool {
        guard let client else { return false }
        isBusy = true
        defer { isBusy = false }
        notice = nil
        problem = nil
        do {
            _ = try await client.deleteSidecar(path: .init(name: sidecar.name), query: .init(confirm: force ? true : nil)).noContent
            notice = "Removed the sidecar “\(sidecar.name)”."
        } catch {
            let failure = Problem(error)
            if failure.status == 409, !force { return true }
            problem = failure
        }
        await loadSidecars(client)
        return false
    }
}

/// What a remove confirmation is about; `force` after the server answered "still in use".
private enum Removal: Identifiable {
    case credentials(SourcesSettingsModel.Provider, force: Bool)
    case sidecar(SourcesSettingsModel.Sidecar, force: Bool)

    var id: String {
        switch self {
        case .credentials(let provider, let force): "app-\(provider.code)-\(force)"
        case .sidecar(let sidecar, let force): "sidecar-\(sidecar.name)-\(force)"
        }
    }

    var force: Bool {
        switch self {
        case .credentials(_, let force), .sidecar(_, let force): force
        }
    }

    /// The dialog's button.
    var action: String {
        switch self {
        case .credentials(_, false): "Remove app credentials"
        case .sidecar(_, false): "Remove sidecar"
        case .credentials(_, true), .sidecar(_, true): "Remove anyway"
        }
    }

    var title: String {
        switch self {
        case .credentials(let provider, false): "Remove the \(provider.name) app credentials?"
        case .credentials(let provider, true): "Connections still use the \(provider.name) app credentials"
        case .sidecar(let sidecar, false): "Remove the sidecar “\(sidecar.name)”?"
        case .sidecar(let sidecar, true): "Connections of \(sidecar.name) still exist"
        }
    }

    var message: String {
        switch self {
        case .credentials(_, false): "Connections that use them stop at their next token refresh until new ones are set."
        case .credentials(_, true): "Removing them anyway stops those connections at their next token refresh, until new ones are set."
        case .sidecar(_, false): "Vitamux stops talking to it. Its secret stops working."
        case .sidecar(_, true): "Removing the sidecar anyway stops them syncing."
        }
    }
}

struct SourcesSettingsView: View {
    @Environment(AppState.self) private var state
    @State private var model = SourcesSettingsModel()
    @State private var editing: SettingsItem<SourcesSettingsModel.Provider>?
    @State private var isAddingSidecar = false
    @State private var removal: Removal?

    var body: some View {
        List {
            Section {
                Text("What Vitamux needs to reach your sources. Secrets are write-only: Vitamux stores them encrypted and never shows them again.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                NavigationLink("Connect an account under Sources", value: Route.connections())
            }
            OrderSection(model: model)
            if let notice = model.notice { Section { NoticeRow(text: notice) } }
            if let problem = model.problem { Section { ProblemRow(problem: problem) } }
            credentialsSection
            sidecarsSection
        }
        .navigationTitle("Sources")
        .task { await model.load(state.client) }
        .refreshable { await model.load(state.client) }
        .sheet(item: $editing) { item in
            AppCredentialsSheet(model: model, provider: item.value)
        }
        .sheet(isPresented: $isAddingSidecar) { AddSidecarSheet(model: model) }
        .confirmationDialog(
            removal?.title ?? "",
            isPresented: Binding { removal != nil } set: { if !$0 { removal = nil } },
            titleVisibility: .visible,
            presenting: removal
        ) { removal in
            Button(removal.action, role: .destructive) {
                Task { await remove(removal) }
            }
            .accessibilityIdentifier("confirmRemoval")
        } message: { removal in
            Text(removal.message)
        }
    }

    /// A first remove that the server refuses as in use asks again, with "Remove anyway".
    private func remove(_ removal: Removal) async {
        switch removal {
        case .credentials(let provider, let force):
            if await model.removeCredentials(provider, force: force, client: state.client) {
                self.removal = .credentials(provider, force: true)
            }
        case .sidecar(let sidecar, let force):
            if await model.removeSidecar(sidecar, force: force, client: state.client) {
                self.removal = .sidecar(sidecar, force: true)
            }
        }
    }

    @ViewBuilder private var credentialsSection: some View {
        Section {
            switch model.providers {
            case .loading: ProgressView()
            case .failed(let problem): ProblemRow(problem: problem)
            case .loaded(let providers) where providers.isEmpty:
                Text("No connector here runs on an application of your own.").foregroundStyle(.secondary)
            case .loaded(let providers):
                ForEach(providers, id: \.code) { provider in
                    CredentialsRow(
                        provider: provider,
                        edit: { editing = SettingsItem(id: provider.code, value: provider) },
                        remove: { removal = .credentials(provider, force: false) }
                    )
                }
            }
        } header: {
            Text("App credentials")
        } footer: {
            Text("Your own application at a provider, such as the Withings developer app. A value set by the environment wins and is shown read-only.")
        }
    }

    @ViewBuilder private var sidecarsSection: some View {
        Section {
            switch model.sidecars {
            case .loading: ProgressView()
            case .failed(let problem): ProblemRow(problem: problem)
            case .loaded(let sidecars) where sidecars.isEmpty:
                Text("No sidecar is registered.").foregroundStyle(.secondary)
            case .loaded(let sidecars):
                ForEach(sidecars, id: \.name) { sidecar in
                    SidecarRow(sidecar: sidecar) { removal = .sidecar(sidecar, force: false) }
                }
            }
            Button("Add a sidecar", systemImage: "plus") { isAddingSidecar = true }
                .accessibilityIdentifier("addSidecar")
        } header: {
            Text("Sidecars")
        } footer: {
            Text("Connectors that run in their own container and talk to Vitamux over the private network.")
        }
    }
}

/// The owner's providers, first preferred: metrics without a built-in rule take the first that has
/// a value.
private struct OrderSection: View {
    @Environment(AppState.self) private var state
    let model: SourcesSettingsModel

    var body: some View {
        Section {
            if model.orderSaved { NoticeRow(text: "Saved.") }
            if let problem = model.orderProblem { ProblemRow(problem: problem) }
            switch model.order {
            case .loading: ProgressView()
            case .failed(let problem): ProblemRow(problem: problem)
            case .loaded(let order) where order.isEmpty:
                Text("No sources connected yet.").foregroundStyle(.secondary)
            case .loaded(let order):
                ForEach(Array(order.enumerated()), id: \.element) { index, code in
                    HStack {
                        Text("\(index + 1)").monospacedDigit().foregroundStyle(.secondary)
                        Text(SettingsFormat.provider(code))
                        Spacer()
                        Button("Move \(SettingsFormat.provider(code)) up", systemImage: "arrow.up") { model.move(index, by: -1) }
                            .disabled(index == 0)
                            .accessibilityIdentifier("orderUp-\(code)")
                        Button("Move \(SettingsFormat.provider(code)) down", systemImage: "arrow.down") { model.move(index, by: 1) }
                            .disabled(index == order.count - 1)
                            .accessibilityIdentifier("orderDown-\(code)")
                    }
                    .labelStyle(.iconOnly)
                    .buttonStyle(.borderless)
                }
                Button("Save order") { Task { await model.saveOrder(state.client) } }
                    .disabled(model.isBusy)
                    .accessibilityIdentifier("saveOrder")
            }
        } header: {
            Text("Source order")
        } footer: {
            Text("Metrics without a built-in rule take the first of these sources that has a value, then a watch, band, ring, chest strap, arm band, phone, other devices and manual entries. Built-in and your own rules are not affected.")
        }
    }
}

private struct CredentialsRow: View {
    let provider: SourcesSettingsModel.Provider
    let edit: () -> Void
    let remove: () -> Void

    var body: some View {
        let app = provider.appCredentials
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(provider.name).font(.headline)
                Spacer()
                if app?.managedByEnvironment == true {
                    Label("Managed by the environment", systemImage: "info.circle").foregroundStyle(.secondary)
                } else if app?.set == true {
                    Label("Set", systemImage: "checkmark.circle").foregroundStyle(Color.feedbackOK)
                } else {
                    Label("Not set", systemImage: "circle.dashed").foregroundStyle(.secondary)
                }
            }
            .font(.subheadline)
            if let id = app?.clientId {
                Text(id).font(.caption.monospaced()).foregroundStyle(.secondary)
            }
            if let updated = app?.updatedAt {
                Text("Last changed \(SettingsFormat.when(updated))").font(.caption).foregroundStyle(.secondary)
            }
            if app?.managedByEnvironment != true {
                HStack {
                    Button(app?.set == true ? "Replace" : "Set", action: edit)
                        .accessibilityIdentifier("editCredentials-\(provider.code)")
                    if app?.set == true {
                        Button("Remove", role: .destructive, action: remove)
                            .accessibilityIdentifier("removeCredentials-\(provider.code)")
                    }
                }
                .buttonStyle(.bordered)
            }
        }
    }
}

private struct SidecarRow: View {
    let sidecar: SourcesSettingsModel.Sidecar
    let remove: () -> Void

    var body: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text(sidecar.name).font(.body.monospaced())
                Text(sidecar.url).font(.caption.monospaced()).foregroundStyle(.secondary)
                Label(sidecar.available ? "Answering" : "Not answering", systemImage: sidecar.available ? "checkmark.circle" : "circle.slash")
                    .font(.caption)
                    .foregroundStyle(sidecar.available ? Color.feedbackOK : .secondary)
                Text(sidecar.source == .panel ? sidecar.createdAt.map { "Added \(SettingsFormat.when($0))" } ?? "Added here" : sidecar.bundled ? "Bundled" : "Environment")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if sidecar.source == .environment {
                Text("Read-only").font(.caption).foregroundStyle(.secondary)
            } else {
                Button("Remove", role: .destructive, action: remove)
                    .buttonStyle(.borderless)
                    .accessibilityIdentifier("removeSidecar-\(sidecar.name)")
            }
        }
    }
}

/// Set or replace a provider app: the client id and a write-only secret, cleared once sent, then
/// checked with the provider. The callback URL to register is shown with a copy button.
private struct AppCredentialsSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let model: SourcesSettingsModel
    let provider: SourcesSettingsModel.Provider
    @State private var clientID: String
    @State private var secret = ""
    @State private var problem: Problem?

    init(model: SourcesSettingsModel, provider: SourcesSettingsModel.Provider) {
        self.model = model
        self.provider = provider
        _clientID = State(initialValue: provider.appCredentials?.clientId ?? "")
    }

    var body: some View {
        NavigationStack {
            Form {
                if let callback = provider.callbackUrl {
                    Section {
                        Text(callback).font(.caption.monospaced()).textSelection(.enabled)
                        Button("Copy callback URL", systemImage: "doc.on.doc") { UIPasteboard.general.string = callback }
                    } header: {
                        Text("Callback URL at \(provider.name)")
                    }
                }
                Section {
                    TextField("Client id", text: $clientID)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .accessibilityIdentifier("clientID")
                    FieldMessage(text: problem?.detail(for: "/client_id"))
                    SecureField("Client secret", text: $secret)
                        .textContentType(.newPassword)
                        .accessibilityIdentifier("clientSecret")
                    FieldMessage(text: problem?.detail(for: "/client_secret"))
                } footer: {
                    Text("Stored encrypted. Vitamux never shows the secret again.")
                }
                if let problem, problem.fieldErrors.isEmpty { Section { ProblemRow(problem: problem) } }
            }
            .navigationTitle("\(provider.appCredentials?.set == true ? "Replace" : "Set") \(provider.name) app")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save and check") {
                        let sent = secret
                        secret = ""
                        Task {
                            problem = await model.saveCredentials(provider, clientID: clientID, secret: sent, client: state.client)
                            if problem == nil { dismiss() }
                        }
                    }
                    .disabled(model.isBusy || clientID.trimmingCharacters(in: .whitespaces).isEmpty || secret.isEmpty)
                    .accessibilityIdentifier("saveCredentials")
                }
            }
        }
        .sheetBackground()
    }
}

/// Register a sidecar by name and private address; its shared secret is shown here once.
private struct AddSidecarSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let model: SourcesSettingsModel
    @State private var name = ""
    @State private var url = ""
    @State private var problem: Problem?
    @State private var secret: String?

    var body: some View {
        NavigationStack {
            Form {
                if let secret {
                    Section {
                        SecretValue(label: "Shared secret", value: secret)
                    } header: {
                        Text("Sidecar “\(name)” added")
                    } footer: {
                        Text("Give this secret to the sidecar as its VITAMUX_SIDECAR_SECRET_FILE. It is shown once and cannot be retrieved later. Once the sidecar runs, check it again under Sources.")
                    }
                } else {
                    Section {
                        TextField("Name, such as ultrahuman", text: $name)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                            .accessibilityIdentifier("sidecarName")
                        FieldMessage(text: problem?.detail(for: "/name"))
                        TextField("http://my-sidecar:8080", text: $url)
                            .keyboardType(.URL)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                            .accessibilityIdentifier("sidecarURL")
                        FieldMessage(text: problem?.detail(for: "/url"))
                    } footer: {
                        Text("The provider code the sidecar describes, and its base URL on the private network. Public addresses are refused.")
                    }
                    if let problem, problem.fieldErrors.isEmpty { Section { ProblemRow(problem: problem) } }
                }
            }
            .navigationTitle(secret == nil ? "Add a sidecar" : "Save the secret")
            .navigationBarTitleDisplayMode(.inline)
            .interactiveDismissDisabled(secret != nil)
            .toolbar {
                if secret == nil {
                    ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                    ToolbarItem(placement: .confirmationAction) {
                        Button("Add") {
                            Task {
                                switch await model.addSidecar(name: name, url: url, client: state.client) {
                                case .success(let value): secret = value
                                case .failure(let failure): problem = failure
                                }
                            }
                        }
                        .disabled(model.isBusy || name.isEmpty || url.isEmpty)
                        .accessibilityIdentifier("saveSidecar")
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
        .sheetBackground()
    }
}
