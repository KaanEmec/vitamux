import AuthenticationServices
import Observation
import SwiftUI
import VitamuxKit

/// What the Connect sheet opens on: a preselected provider and, after a refused exchange with the
/// owner's own app, its app wizard.
struct ConnectStart: Identifiable {
    var provider: String?
    var app = false
    let id = UUID()
}

/// Connect a source (docs/adr/0021-source-setup.md, the panel's SourceSetup.svelte): every
/// provider of `GET /providers`, re-fetched each time, with its setup state and the one next
/// action for the chosen one: the app wizard (`needs_app_credentials`), else auth/begin, whose
/// redirect runs in the auth browser and whose prompts are asked here. A provider whose install
/// must change first cannot be chosen; its row says what to change.
@Observable
final class ConnectModel {
    private(set) var providers: Loadable<[Provider]> = .loading
    var chosen = ""
    var showsApp = false
    private(set) var prompt: AuthPromptStep?
    private(set) var problem: Problem?
    private(set) var lead = ""
    private(set) var busy = false

    func load(_ client: Client?, start: ConnectStart) async {
        guard let client else { return }
        providers = await Loadable { try await client.listProviders().ok.body.json.providers.filter(Self.connectable) }
        let list = providers.value ?? []
        chosen = start.provider.flatMap { code in list.first { $0.code == code }?.code } ?? list.first(where: Self.choosable)?.code ?? ""
        showsApp = start.app && current?.appCredentials != nil
    }

    /// Providers a phone can authorize (not file imports or device pairing).
    static func connectable(_ p: Provider) -> Bool {
        p.authKind == nil || p.authKind == .oauth2 || p.authKind == .interactiveMfa
    }

    /// A provider can be chosen unless the install itself must change first.
    static func choosable(_ p: Provider) -> Bool {
        p.available && p.setupState != .needsSidecar && p.setupState != .needsPublicUrl
    }

    var current: Provider? { providers.value?.first { $0.code == chosen } }

    func replace(_ p: Provider) {
        guard case .loaded(var list) = providers, let index = list.firstIndex(where: { $0.code == p.code }) else { return }
        list[index] = p
        providers = .loaded(list)
        if chosen.isEmpty, Self.choosable(p) { chosen = p.code }
    }

    /// The one next action: the app wizard, or the sign-in.
    func next(state: AppState, browser: AuthBrowser) async -> Bool {
        if current?.setupState == .needsAppCredentials {
            showsApp = true
            return false
        }
        return await begin(state: state, browser: browser)
    }

    /// auth/begin with the app return: answers true once the auth browser has come back.
    func begin(state: AppState, browser: AuthBrowser) async -> Bool {
        guard let client = state.client, let provider = current else { return false }
        busy = true
        problem = nil
        lead = ""
        defer { busy = false }
        do {
            switch try await client.beginProviderAuth(path: .init(provider: provider.code), body: .json(.init(_return: .app))).ok.body.json {
            case .AuthPromptStep(let step):
                prompt = step
            case .AuthRedirect(let redirect):
                try await AuthFlow.follow(redirect.redirectUrl, state: state, browser: browser)
                return true
            }
        } catch {
            let problem = Problem(error)
            lead = await AuthFlow.explain(problem, provider: provider.code, client: client, codeStep: false)
            self.problem = problem
        }
        return false
    }

    func restart() {
        prompt = nil
        problem = nil
        showsApp = false
    }
}

struct ConnectSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    @Environment(\.webAuthenticationSession) private var session
    @State private var model = ConnectModel()
    let start: ConnectStart

    var body: some View {
        NavigationStack {
            content
                .navigationTitle("Connect a source")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                }
        }
        .task { await model.load(state.client, start: start) }
    }

    @ViewBuilder private var content: some View {
        if let step = model.prompt, let provider = model.current {
            AuthPromptView(provider: provider.code, step: step, onRestart: model.restart) { id in
                dismiss()
                state.open(.connection(id: id))
            }
        } else if model.showsApp, let provider = model.current {
            AppWizardView(provider: provider, busy: model.busy, problem: model.problem, lead: model.lead) {
                model.restart()
            } onUpdate: {
                model.replace($0)
            } onConnect: {
                Task { await connect(model.begin) }
            }
        } else {
            switch model.providers {
            case .loading:
                ProgressView("Loading sources").frame(maxWidth: .infinity, maxHeight: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let list):
                ProviderChoice(model: model, providers: list) {
                    Task { await connect(model.next) }
                }
            }
        }
    }

    /// Runs a step; once the auth browser has come back the router shows its result.
    private func connect(_ step: (AppState, AuthBrowser) async -> Bool) async {
        if await step(state, AuthBrowser(state: state, session: session)) { dismiss() }
    }
}

private struct ProviderChoice: View {
    @Bindable var model: ConnectModel
    let providers: [Provider]
    let onNext: () -> Void

    var body: some View {
        Form {
            if providers.isEmpty {
                Text("No source can be connected from here yet.").foregroundStyle(.secondary)
            }
            ForEach(providers, id: \.code) { provider in
                Section {
                    ProviderRow(provider: provider, isChosen: model.chosen == provider.code) { model.chosen = provider.code }
                    if provider.setupState == .needsSidecar {
                        SidecarCard(provider: provider) { model.replace($0) }
                    }
                }
            }
            if let chosen = model.current, ConnectModel.choosable(chosen) {
                Section {
                    Text(explanation(chosen)).font(.subheadline)
                    if let problem = model.problem { ProblemRow(problem: problem, lead: model.lead) }
                    Button(action: onNext) {
                        HStack {
                            Text(chosen.setupState == .needsAppCredentials ? "Set up \(chosen.name)" : "Continue to \(chosen.name)")
                            if model.busy { Spacer(); ProgressView() }
                        }
                    }
                    .disabled(model.busy)
                    .accessibilityIdentifier("connectNext")
                } footer: {
                    Text("Apple Health connects from this iPhone (More, Apple Health); file imports use the command line.")
                }
            }
        }
    }

    private func explanation(_ p: Provider) -> String {
        if p.setupState == .needsAppCredentials {
            return "\(p.name) needs an application of your own at \(p.name): create it, paste its client id and secret here, then connect."
        }
        if p.authKind == .interactiveMfa {
            return "Vitamux asks for your \(p.name) email and password here, then a verification code if \(p.name) sends one. They pass to the sidecar once and are never stored; Vitamux keeps only the access it is given, encrypted."
        }
        var out = "You will sign in at \(p.name) and allow access, then come back to the app. Vitamux stores the access it is given encrypted and never shows it."
        if p.appCredentials?.managedByEnvironment == true {
            out += " It uses the app credentials set by the environment."
        } else if p.appCredentials?.set == true {
            out += " It uses the app credentials in Settings, Sources."
        }
        return out
    }
}

/// A provider as a radio choice: its setup state, what it brings, and the unofficial warning.
private struct ProviderRow: View {
    let provider: Provider
    let isChosen: Bool
    let choose: () -> Void

    var body: some View {
        let open = ConnectModel.choosable(provider)
        Button(action: choose) {
            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 10) {
                    Image(systemName: isChosen ? "largecircle.fill.circle" : "circle")
                        .foregroundStyle(open ? Color.accentColor : .secondary)
                        .accessibilityHidden(true)
                    SourceMonogram(provider: provider.code, size: 32)
                    Text(provider.name).font(.headline)
                    if !provider.official && provider.available { UnofficialBadge() }
                }
                Label {
                    HStack(spacing: 4) {
                        Text(state.label)
                        if let upstream = provider.upstream {
                            Text("· \(upstream.package) \(upstream.version)").foregroundStyle(.secondary)
                        }
                    }
                } icon: {
                    StatusIcon(kind: state.kind)
                }
                .font(.subheadline)
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("setupState-\(provider.code)")
                if let about = SourcesCopy.about(provider.code) {
                    Text(about).font(.footnote).foregroundStyle(.secondary)
                }
                if !provider.official && provider.available {
                    Text("It uses an unofficial API that can change without notice. The connection starts paused until you enable it on its page. Only connect your own account.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
                if provider.setupState == .needsPublicUrl {
                    ForEach(provider.problems, id: \.code) { Text($0.message).font(.footnote) }
                }
            }
            .foregroundStyle(.primary)
        }
        .buttonStyle(.plain)
        .disabled(!open)
        .accessibilityAddTraits(isChosen ? .isSelected : [])
        .accessibilityIdentifier("provider-\(provider.code)")
    }

    /// How each setup state is shown (setup.ts `states`).
    private var state: (label: String, kind: StatusKind) {
        switch provider.setupState {
        case .ready: ("Ready to connect", .info)
        case .connected: ("Connected", .ok)
        case .needsAppCredentials: ("Needs its app credentials", .pending)
        case .needsPublicUrl: ("Needs an https public address", .warn)
        case .needsSidecar: ("Not available: its sidecar is not running", .off)
        }
    }
}

/// A sidecar provider that does not answer (`needs_sidecar`): what is wrong, the exact line that
/// turns a bundled sidecar on for this install, and Check again. Vitamux never starts containers.
private struct SidecarCard: View {
    @Environment(AppState.self) private var state
    let provider: Provider
    let onUpdate: (Provider) -> Void
    @State private var busy = false
    @State private var checked = ""
    @State private var problem: Problem?

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            ForEach(provider.problems, id: \.code) { Text($0.message).font(.footnote) }
            ForEach(provider.sidecar?.enable ?? [], id: \.line) { enable in
                VStack(alignment: .leading, spacing: 4) {
                    Text(enable.install == .compose ? "Docker Compose" : "Coolify").font(.subheadline.weight(.semibold))
                    CopyValue(label: enable.install == .compose ? "Add this line to .env" : "Add this environment variable", value: enable.line)
                    Text(enable.install == .compose ? "Then run \(enable.apply)." : "Then \(enable.apply.prefix(1).lowercased() + enable.apply.dropFirst()).")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
            HStack {
                Button("Check again") { Task { await probe() } }
                    .buttonStyle(.bordered)
                    .disabled(busy)
                    .accessibilityIdentifier("checkAgain-\(provider.code)")
                if busy { ProgressView() }
                Text(checked).font(.footnote).foregroundStyle(.secondary).accessibilityIdentifier("checked-\(provider.code)")
            }
            if let problem { ProblemRow(problem: problem) }
        }
    }

    private func probe() async {
        guard let client = state.client else { return }
        busy = true
        checked = ""
        problem = nil
        defer { busy = false }
        do {
            let updated = try await client.probeProvider(path: .init(provider: provider.code)).ok.body.json
            checked = updated.setupState == .needsSidecar ? "Checked: the \(provider.name) sidecar still does not answer." : ""
            onUpdate(updated)
        } catch {
            problem = Problem(error)
        }
    }
}

/// A value to paste elsewhere, with a copy button.
struct CopyValue: View {
    let label: String
    let value: String
    @State private var copied = false

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(label).font(.caption).foregroundStyle(.secondary)
            HStack {
                Text(value).font(.footnote.monospaced()).textSelection(.enabled).accessibilityIdentifier("value-\(label)")
                Spacer()
                Button(copied ? "Copied" : "Copy", systemImage: copied ? "checkmark" : "doc.on.doc") {
                    UIPasteboard.general.string = value
                    copied = true
                }
                .labelStyle(.iconOnly)
                .buttonStyle(.borderless)
                .accessibilityLabel(copied ? "Copied \(label)" : "Copy \(label)")
                .accessibilityIdentifier("copy-\(label)")
            }
        }
    }
}
