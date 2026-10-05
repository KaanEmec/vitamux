import AuthenticationServices
import Observation
import SwiftUI
import VitamuxKit

typealias AuthPromptStep = Components.Schemas.AuthPromptStep

/// Where an OAuth redirect step runs: `ASWebAuthenticationSession` (through SwiftUI's
/// `webAuthenticationSession`), whose cookie store is not the app's, so auth/begin asks for
/// `{"return": "app"}` and the server's callback ends at `vitamux://connections?…`
/// (docs/architecture/connectors.md#oauth-connection-flow). UI tests use the fake server's browser.
struct AuthBrowser {
    let open: (URL) async throws -> URL

    init(state: AppState, session: WebAuthenticationSession) {
        #if DEBUG
        if state.isUITest {
            open = { FakeServer.uiTest.authorize($0) }
            return
        }
        #endif
        open = { url in
            // Ephemeral: nothing of the provider sign-in stays in Safari, and iOS asks no consent.
            try await session.authenticate(using: url, callback: .customScheme("vitamux"), preferredBrowserSession: .ephemeral,
                                           additionalHeaderFields: [:])
        }
    }
}

/// A prompt step shown in a sheet.
struct PromptSheet: Identifiable {
    let provider: String
    let step: AuthPromptStep
    var id: String { step.state }
}

/// Sync now and Reauthorize for one connection (the panel's ConnectionActions.svelte): manual
/// sync (one job per stream, coalesced with a pending one) and reauthorization through the
/// provider's page or the connector's prompts.
@Observable
final class ConnectionActions {
    private(set) var busy = false
    private(set) var problem: Problem?
    private(set) var lead = ""
    private(set) var queued: [Components.Schemas.Job]?
    var prompt: PromptSheet?

    /// Queues a sync and answers the connection as it is afterwards.
    func sync(_ connection: Connection, client: Client?) async -> Connection? {
        guard let client else { return nil }
        busy = true
        problem = nil
        queued = nil
        do {
            queued = try await client.syncConnection(path: .init(id: connection.id)).accepted.body.json.jobs
        } catch {
            problem = Problem(error)
        }
        busy = false
        return try? await client.getConnection(path: .init(id: connection.id)).ok.body.json
    }

    /// The provider's page in the auth browser (its return reaches the router), or a prompt sheet.
    func reauthorize(_ connection: Connection, state: AppState, browser: AuthBrowser) async {
        guard let client = state.client else { return }
        busy = true
        problem = nil
        lead = ""
        defer { busy = false }
        do {
            let step = try await client.beginConnectionAuth(path: .init(id: connection.id), body: .json(.init(_return: .app))).ok.body.json
            switch step {
            case .AuthPromptStep(let step):
                prompt = PromptSheet(provider: connection.provider, step: step)
            case .AuthRedirect(let redirect):
                try await AuthFlow.follow(redirect.redirectUrl, state: state, browser: browser)
            }
        } catch {
            problem = Problem(error)
            lead = await AuthFlow.explain(problem!, provider: connection.provider, client: client, codeStep: false)
        }
    }

    var queuedText: String? {
        guard let queued else { return nil }
        let streams = queued.map { job in
            "\(job.payload.value["stream"] as? String ?? job.kind) (\(job.status.rawValue))"
        }
        return "Sync queued: " + (streams.isEmpty ? "nothing to sync" : streams.joined(separator: ", ")) + "."
    }
}

/// The steps shared by connecting and reauthorizing.
enum AuthFlow {
    /// Opens a redirect step in the auth browser and hands its `vitamux://` return to the router.
    /// Cancelling the browser is not an error.
    static func follow(_ redirect: String, state: AppState, browser: AuthBrowser) async throws {
        guard let profile = state.profile, let url = URL(string: redirect, relativeTo: profile.baseURL)?.absoluteURL else { return }
        do {
            let back = try await browser.open(url)
            state.open(back)
        } catch let error as ASWebAuthenticationSessionError where error.code == .canceledLogin {
            return
        }
    }

    /// The plain-language lead for a failed begin or continue: on a 503 it checks whether the
    /// provider's sidecar went away.
    static func explain(_ problem: Problem, provider: String, client: Client, codeStep: Bool) async -> String {
        var sidecarDown = false
        if problem.status == 503 {
            let providers = try? await client.listProviders().ok.body.json.providers
            sidecarDown = providers?.first { $0.code == provider }?.setupState == .needsSidecar
        }
        return SourcesCopy.signInError(problem, name: providerLabel(provider), codeStep: codeStep, sidecarDown: sidecarDown)
    }
}

/// One connector prompt after another until the connection exists (the panel's AuthPrompt.svelte):
/// the values go to auth/continue and are cleared the moment they are sent; a state works once,
/// so after an error the owner starts again.
@Observable
final class AuthPromptModel {
    let provider: String
    private(set) var step: AuthPromptStep
    var values: [String: String]
    private(set) var problem: Problem?
    private(set) var lead = ""
    private(set) var busy = false

    init(provider: String, step: AuthPromptStep) {
        self.provider = provider
        self.step = step
        values = Self.blank(step)
    }

    private static func blank(_ step: AuthPromptStep) -> [String: String] {
        Dictionary(uniqueKeysWithValues: step.prompt.fields.map { ($0.name, "") })
    }

    var isComplete: Bool {
        step.prompt.fields.allSatisfy { !(values[$0.name] ?? "").isEmpty }
    }

    /// Sends the values; answers the connection id once authorized.
    func submit(state: AppState, browser: AuthBrowser) async -> String? {
        guard let client = state.client, !busy else { return nil }
        busy = true
        problem = nil
        let sent = values
        values = Self.blank(step)
        let codeStep = step.prompt.fields.contains { $0.kind == .code }
        defer { busy = false }
        do {
            let body = Components.Schemas.AuthContinueInput(state: step.state, values: .init(additionalProperties: sent))
            switch try await client.continueProviderAuth(path: .init(provider: provider), body: .json(body)).ok.body.json {
            case .AuthDone(let done):
                return done.connectionId
            case .AuthRedirect(let redirect):
                try await AuthFlow.follow(redirect.redirectUrl, state: state, browser: browser)
            case .AuthPromptStep(let next):
                step = next
                values = Self.blank(next)
            }
        } catch {
            let problem = Problem(error)
            lead = await AuthFlow.explain(problem, provider: provider, client: client, codeStep: codeStep)
            self.problem = problem
        }
        return nil
    }
}

struct AuthPromptView: View {
    @Environment(AppState.self) private var state
    @Environment(\.webAuthenticationSession) private var session
    @State private var model: AuthPromptModel
    @FocusState private var focused: String?
    let onRestart: () -> Void
    let onDone: (String) -> Void

    init(provider: String, step: AuthPromptStep, onRestart: @escaping () -> Void, onDone: @escaping (String) -> Void) {
        // The first step only seeds the model; later steps come from the server's answers.
        _model = State(initialValue: AuthPromptModel(provider: provider, step: step))
        self.onRestart = onRestart
        self.onDone = onDone
    }

    var body: some View {
        Form {
            if let problem = model.problem {
                Section {
                    ProblemRow(problem: problem, lead: model.lead)
                    Button("Start again", action: onRestart)
                        .accessibilityIdentifier("promptRestart")
                }
            } else {
                Section {
                    ForEach(model.step.prompt.fields, id: \.name) { field in
                        PromptField(field: field, value: Binding { model.values[field.name] ?? "" } set: { model.values[field.name] = $0 })
                            .focused($focused, equals: field.name)
                    }
                } header: {
                    Text(model.step.prompt.message)
                        .textCase(nil)
                        .accessibilityIdentifier("promptMessage")
                } footer: {
                    Text("These pass to \(providerLabel(model.provider)) once and are never stored.")
                }
                Section {
                    Button {
                        Task { await submit() }
                    } label: {
                        if model.busy { ProgressView() } else { Text("Continue") }
                    }
                    .disabled(!model.isComplete || model.busy)
                    .accessibilityIdentifier("promptContinue")
                }
            }
        }
        // Focus the first field of every step, since the previous step's button is gone.
        .onChange(of: model.step.state, initial: true) { _, _ in focused = model.step.prompt.fields.first?.name }
    }

    private func submit() async {
        if let id = await model.submit(state: state, browser: AuthBrowser(state: state, session: session)) {
            onDone(id)
        }
    }
}

private struct PromptField: View {
    let field: AuthPromptStep.PromptPayload.FieldsPayloadPayload
    @Binding var value: String

    var body: some View {
        Group {
            switch field.kind {
            case .password:
                SecureField(field.label, text: $value).textContentType(.password)
            case .code:
                TextField(field.label, text: $value).textContentType(.oneTimeCode).keyboardType(.numberPad)
            case .text:
                TextField(field.label, text: $value).textContentType(.username).keyboardType(.emailAddress)
            }
        }
        .textInputAutocapitalization(.never)
        .autocorrectionDisabled()
        .accessibilityIdentifier("promptField-\(field.name)")
    }
}
