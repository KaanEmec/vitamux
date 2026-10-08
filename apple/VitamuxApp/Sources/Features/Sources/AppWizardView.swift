import SwiftUI
import VitamuxKit

/// The app wizard for a provider that runs on the owner's own application (Withings, the panel's
/// AppWizard.svelte): 1. create the app at the provider with the values shown, callback URL first;
/// 2. paste the client id and secret (PUT …/app-credentials, then POST …/verify); 3. connect the
/// account. The secret is write-only: the field is cleared once sent and nothing returns it.
/// Credentials set by the environment are shown read-only.
struct AppWizardView: View {
    @Environment(AppState.self) private var state
    let provider: Provider
    let busy: Bool
    let problem: Problem?
    let lead: String
    let onBack: () -> Void
    let onUpdate: (Provider) -> Void
    let onConnect: () -> Void

    @State private var step = 1
    @State private var clientID = ""
    @State private var secret = ""
    @State private var saving = false
    @State private var saveProblem: Problem?
    @State private var checked: (kind: StatusKind, text: String)?

    private static let titles = ["Create the app", "Paste the credentials", "Connect your account"]
    private var environment: Bool { provider.appCredentials?.managedByEnvironment ?? false }

    var body: some View {
        Form {
            Section {
                Text("Step \(step) of 3 · \(Self.titles[step - 1])")
                    .font(.headline)
                    .accessibilityIdentifier("wizardStep")
            }
            switch step {
            case 1: create
            case 2: credentials
            default: account
            }
        }
    }

    @ViewBuilder private var create: some View {
        Section {
            ForEach(provider.problems, id: \.code) { NoticeRow(text: $0.message, kind: .warn, identifier: "setupProblem") }
            if let dashboard = SourcesCopy.appDashboard(provider.code) {
                Link("Open the \(provider.name) developer dashboard", destination: dashboard)
            }
            Text("Sign in there, create an application and enter these values:").font(.subheadline)
            if let callback = provider.callbackUrl {
                CopyValue(label: "Callback URL", value: callback)
            } else {
                NoticeRow(text: "No public address is set, so there is no callback URL yet. Set VITAMUX_PUBLIC_URL to the https address of Vitamux.", kind: .warn)
            }
            CopyValue(label: "Application name", value: "Vitamux")
            CopyValue(label: "Description", value: "Self-hosted personal health data")
        } footer: {
            Text("The callback URL must match exactly. \(provider.name) may keep the application in restricted mode, limited to 10 users: that is fine for one owner.")
        }
        buttons(back: onBack) { step = 2 }
    }

    @ViewBuilder private var credentials: some View {
        if environment {
            Section {
                Text("The client id and secret are set by the environment" + (provider.appCredentials?.clientId.map { " (client id \($0))" } ?? "") + ", so they cannot be changed here.")
                    .accessibilityIdentifier("appEnvironment")
            }
            buttons(back: { step = 1 }) { step = 3 }
        } else {
            Section {
                TextField("Client id", text: $clientID)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .accessibilityIdentifier("clientIdField")
                SecureField("Client secret", text: $secret)
                    .submitLabel(.done)
                    .onSubmit { if canSave { Task { await save() } } }
                    .accessibilityIdentifier("clientSecretField")
                if let error = saveProblem?.detail(for: "/client_id") { Text(error).font(.footnote).foregroundStyle(Color.feedbackError) }
                if let error = saveProblem?.detail(for: "/client_secret") { Text(error).font(.footnote).foregroundStyle(Color.feedbackError) }
            } header: {
                Text("Copy them from the application you just created.").textCase(nil)
            } footer: {
                Text("Stored encrypted. Vitamux never shows the secret again.")
            }
            if let checked { Section { NoticeRow(text: checked.text, kind: checked.kind, identifier: "appCheck") } }
            if let saveProblem, saveProblem.fieldErrors.isEmpty {
                Section { ProblemRow(problem: saveProblem, lead: saveProblem.status == 409 ? "The environment sets them, so they cannot be changed here." : "") }
            }
            Section {
                HStack {
                    Button("Back") { step = 1 }
                    Spacer()
                    Button("Save and check") { Task { await save() } }
                        .disabled(!canSave)
                        .accessibilityIdentifier("saveAppCredentials")
                    if saving { ProgressView().accessibilityLabel("Loading") }
                }
                .buttonStyle(.borderless)
            }
        }
    }

    @ViewBuilder private var account: some View {
        Section {
            if let checked { NoticeRow(text: checked.text, kind: checked.kind, identifier: "appCheck") }
            Text("You will sign in at \(provider.name) and allow access, then come back to the app. Vitamux stores the access it is given encrypted and never shows it.")
        } footer: {
            Text("If \(provider.name) reports a redirect URI error, the callback URL registered there differs from the one in step 1.")
        }
        if let problem { Section { ProblemRow(problem: problem, lead: lead) } }
        Section {
            HStack {
                Button("Back") { step = 2 }
                Spacer()
                Button("Continue to \(provider.name)", action: onConnect)
                    .disabled(busy)
                    .accessibilityIdentifier("wizardConnect")
                if busy { ProgressView().accessibilityLabel("Loading") }
            }
            .buttonStyle(.borderless)
        }
    }

    private func buttons(back: @escaping () -> Void, next: @escaping () -> Void) -> some View {
        Section {
            HStack {
                Button("Back", action: back)
                Spacer()
                Button("Next", action: next).accessibilityIdentifier("wizardNext")
            }
            .buttonStyle(.borderless)
        }
    }

    private var canSave: Bool {
        !saving && !clientID.trimmingCharacters(in: .whitespaces).isEmpty && !secret.isEmpty
    }

    private func save() async {
        guard let client = state.client else { return }
        saving = true
        saveProblem = nil
        checked = nil
        defer { saving = false }
        let body = Components.Schemas.AppCredentialsInput(clientId: clientID.trimmingCharacters(in: .whitespaces),
                                                          clientSecret: secret.trimmingCharacters(in: .whitespaces))
        // Write-only: the secret leaves the view state the moment it is sent.
        secret = ""
        do {
            onUpdate(try await client.putProviderAppCredentials(path: .init(provider: provider.code), body: .json(body)).ok.body.json)
        } catch {
            saveProblem = Problem(error)
            return
        }
        do {
            let verification = try await client.verifyProviderAppCredentials(path: .init(provider: provider.code)).ok.body.json
            switch verification.result {
            case .invalid:
                checked = (.error, verification.message)
                return
            case .valid: checked = (.ok, verification.message)
            case .unverifiable: checked = (.info, verification.message)
            }
        } catch {
            checked = (.warn, "They were saved but could not be checked now (\(Problem(error).detail ?? "no answer")). Connecting will check them.")
        }
        step = 3
    }
}
