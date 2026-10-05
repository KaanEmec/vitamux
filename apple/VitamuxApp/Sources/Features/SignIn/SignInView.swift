import SwiftUI
import VitamuxKit

/// Server step and sign-in on one screen (artboard SignIn): address or pairing QR, username and
/// password; then the two-factor code when the server asks for it.
struct SignInView: View {
    @Environment(AppState.self) private var state
    @State private var model: SignInModel?

    var body: some View {
        ScrollView {
            if let model {
                VStack(spacing: 18) {
                    Header()
                    if let reason = state.signInReason { ReasonBanner(reason: reason) }
                    CredentialsForm(model: model)
                    PrivacyNote()
                }
                .padding(.horizontal, 24)
                .padding(.vertical, 32)
            }
        }
        .scrollDismissesKeyboard(.interactively)
        .onAppear {
            // Seeded once from the last server and username, so "session expired" keeps them.
            if model == nil { model = SignInModel(profile: state.profile, username: state.username) }
        }
    }
}

private struct Header: View {
    var body: some View {
        VStack(spacing: 12) {
            AppMark()
            Text("Vitamux").font(.largeTitle.bold())
            Text("Sign in to your own server").font(.body).foregroundStyle(.secondary)
        }
        .padding(.bottom, 8)
    }
}

private struct ReasonBanner: View {
    let reason: AppState.SignInReason

    var body: some View {
        Label(
            reason == .expired ? "Your session expired. Sign in again." : "You signed out.",
            systemImage: reason == .expired ? "clock.badge.exclamationmark" : "checkmark.circle"
        )
        .font(.subheadline)
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(12)
        .background(.fill.tertiary, in: .rect(cornerRadius: 12))
        .accessibilityIdentifier(reason == .expired ? "sessionExpired" : "signedOut")
    }
}

private struct CredentialsForm: View {
    @Environment(AppState.self) private var state
    @Bindable var model: SignInModel
    @State private var isScanning = false

    var body: some View {
        VStack(spacing: 18) {
            VStack(alignment: .leading, spacing: 8) {
                Text("Server address").font(.subheadline).foregroundStyle(.secondary)
                HStack(spacing: 8) {
                    FieldBox(systemImage: "server.rack") {
                        TextField("Server address", text: $model.address, prompt: Text(verbatim: "https://vitamux.example.org"))
                            .keyboardType(.URL)
                            .textContentType(.URL)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                            .accessibilityLabel("Server address")
                            .accessibilityIdentifier("serverField")
                    }
                    Button("Scan pairing QR code", systemImage: "qrcode.viewfinder") { isScanning = true }
                        .labelStyle(.iconOnly)
                        .font(.title2)
                        .frame(width: 50, height: 50)
                        .background(.fill.tertiary, in: .rect(cornerRadius: 12))
                }
                if let problem = model.serverProblem { ProblemText(problem: problem, id: "serverProblem") }
            }
            Field("Username", systemImage: "person.fill") {
                TextField("owner", text: $model.username)
                    .textContentType(.username)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .accessibilityIdentifier("usernameField")
            }
            Field("Password", systemImage: "lock.fill") {
                SecureField("At least 12 characters", text: $model.password)
                    .textContentType(.password)
                    .accessibilityIdentifier("passwordField")
                    .onSubmit { Task { await model.submit(to: state) } }
            }
            // The code joins the form instead of replacing it, so the password stays on screen
            // (and iOS does not offer to save it halfway through).
            if model.step == .code {
                CodeFields(model: model)
                SubmitButton(model: model, title: "Verify")
            } else {
                SubmitButton(model: model, title: "Sign in")
                Text("If two-factor is on, the app asks for your code next.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
        }
        .sheet(isPresented: $isScanning) {
            ScannerSheet { payload in
                isScanning = false
                model.scanned(payload)
            }
        }
    }
}

private struct CodeFields: View {
    @Bindable var model: SignInModel

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Field(model.usesRecoveryCode ? "Recovery code" : "Two-factor code", systemImage: "key.fill") {
                TextField(model.usesRecoveryCode ? "Recovery code" : "123456", text: $model.code)
                    .textContentType(.oneTimeCode)
                    .keyboardType(model.usesRecoveryCode ? .asciiCapable : .numberPad)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .accessibilityIdentifier("codeField")
            }
            Text(model.usesRecoveryCode
                 ? "Enter one of your recovery codes. Each code works once."
                 : "Enter the 6-digit code from your authenticator app.")
                .font(.subheadline)
                .foregroundStyle(.secondary)
            Button(model.usesRecoveryCode ? "Use the authenticator code" : "Use a recovery code instead") {
                model.usesRecoveryCode.toggle()
                model.code = ""
            }
            .font(.subheadline)
            .accessibilityIdentifier("recoveryToggle")
        }
    }
}

/// Sign in or Verify; disabled and counting down while sign-in is rate limited.
private struct SubmitButton: View {
    @Environment(AppState.self) private var state
    let model: SignInModel
    let title: String

    var body: some View {
        TimelineView(.periodic(from: .now, by: 1)) { context in
            let wait = model.retryAt.map { Int($0.timeIntervalSince(context.date).rounded(.up)) } ?? 0
            VStack(spacing: 8) {
                Button {
                    Task { await model.submit(to: state) }
                } label: {
                    Group {
                        if model.isBusy { ProgressView() } else { Text(title) }
                    }
                    .frame(maxWidth: .infinity, minHeight: 34)
                }
                .buttonStyle(.borderedProminent)
                .disabled(!model.canSubmit || wait > 0)
                .accessibilityIdentifier("signInButton")
                if wait > 0 {
                    Text("Too many attempts. Try again in \(Duration.seconds(wait).formatted(.time(pattern: .minuteSecond))).")
                        .font(.subheadline)
                        .foregroundStyle(.red)
                        .accessibilityIdentifier("rateLimit")
                } else if let problem = model.problem {
                    ProblemText(problem: problem, id: "signInProblem")
                }
                if let note = model.appUpdateNote {
                    Text(note).font(.footnote).foregroundStyle(.secondary)
                }
            }
        }
    }
}

private struct Field<Content: View>: View {
    let label: String
    let systemImage: String
    @ViewBuilder let content: Content

    init(_ label: String, systemImage: String, @ViewBuilder content: () -> Content) {
        self.label = label
        self.systemImage = systemImage
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(label).font(.subheadline).foregroundStyle(.secondary)
            FieldBox(systemImage: systemImage) { content }
        }
    }
}

/// An input with its icon in a rounded box.
private struct FieldBox<Content: View>: View {
    let systemImage: String
    @ViewBuilder let content: Content

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: systemImage).foregroundStyle(.tint).accessibilityHidden(true)
            content
        }
        .padding(.horizontal, 12)
        .frame(minHeight: 50)
        .background(.fill.tertiary, in: .rect(cornerRadius: 12))
    }
}

private struct ProblemText: View {
    let problem: Problem
    let id: String

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(problem.title).font(.subheadline.weight(.semibold))
            if let detail = problem.detail { Text(detail).font(.subheadline) }
        }
        .foregroundStyle(.red)
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier(id)
    }
}

private struct PrivacyNote: View {
    var body: some View {
        Label("Your data stays on the server you name above. Vitamux sends nothing anywhere else.", systemImage: "checkmark.shield.fill")
            .font(.subheadline)
            .foregroundStyle(.secondary)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(14)
            .background(.fill.tertiary, in: .rect(cornerRadius: 14))
            .padding(.top, 24)
    }
}
