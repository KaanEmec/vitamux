import Foundation
import Observation
import SwiftUI
import VitamuxKit

/// Settings › Security (the panel's `/settings/security`): change the password, TOTP enrolment
/// (the key, an `otpauth://` link, confirm, recovery codes shown once) and turning it off, and the
/// sessions signed in (browser and app) with end one or all others. Passwords and codes are sent
/// once and cleared; the TOTP key and recovery codes live only in their sheet.
@Observable
final class SecurityModel {
    typealias Session = Components.Schemas.ActiveSession

    private(set) var sessions: Loadable<[SecurityModel.Session]> = .loading
    private(set) var totpEnabled: Bool?
    private(set) var notice: String?
    private(set) var problem: Problem?
    private(set) var isBusy = false

    var others: [SecurityModel.Session] { (sessions.value ?? []).filter { !$0.current } }

    func load(_ client: Client?) async {
        guard let client else { return }
        sessions = await Loadable { try await client.listSessions().ok.body.json.sessions }
        await refreshTOTP(client)
    }

    func refreshTOTP(_ client: Client?) async {
        guard let client else { return }
        if let session = try? await client.getSession().ok.body.json { totpEnabled = session.user.totpEnabled }
    }

    /// Nil once changed, else the problem for the form.
    func changePassword(current: String, new: String, client: Client?) async -> Problem? {
        guard let client else { return nil }
        isBusy = true
        defer { isBusy = false }
        notice = nil
        problem = nil
        do {
            _ = try await client.changePassword(body: .json(.init(currentPassword: current, newPassword: new))).noContent
        } catch {
            return Problem(error)
        }
        notice = "Password changed. Every other session was signed out."
        await load(client)
        return nil
    }

    func enroll(_ client: Client?) async -> Components.Schemas.TOTPEnrollment? {
        guard let client else { return nil }
        isBusy = true
        defer { isBusy = false }
        problem = nil
        do {
            return try await client.enrollTOTP().ok.body.json
        } catch {
            problem = Problem(error)
            return nil
        }
    }

    /// The recovery codes, for the sheet to show once.
    func confirm(code: String, client: Client?) async -> Result<[String], Problem> {
        guard let client else { return .failure(Problem(title: "Signed out")) }
        isBusy = true
        defer { isBusy = false }
        do {
            let codes = try await client.confirmTOTP(body: .json(.init(code: code.trimmingCharacters(in: .whitespaces)))).ok.body.json.recoveryCodes
            await refreshTOTP(client)
            return .success(codes)
        } catch {
            return .failure(Problem(error))
        }
    }

    func disable(password: String, code: String, recovery: String, client: Client?) async -> Problem? {
        guard let client else { return nil }
        isBusy = true
        defer { isBusy = false }
        let trim = { (text: String) in text.trimmingCharacters(in: .whitespaces) }
        let body = Operations.DisableTOTP.Input.Body.JsonPayload(
            password: password,
            totpCode: trim(code).isEmpty ? nil : trim(code),
            recoveryCode: trim(recovery).isEmpty ? nil : trim(recovery)
        )
        do {
            _ = try await client.disableTOTP(body: .json(body)).noContent
        } catch {
            return Problem(error)
        }
        notice = "Two-factor authentication is off."
        await refreshTOTP(client)
        return nil
    }

    func end(_ sessions: [SecurityModel.Session], done: String, client: Client?) async {
        guard let client else { return }
        isBusy = true
        defer { isBusy = false }
        notice = nil
        problem = nil
        for session in sessions {
            do {
                _ = try await client.revokeSession(path: .init(id: session.id)).noContent
            } catch {
                problem = Problem(error)
                break
            }
        }
        if problem == nil { notice = done }
        self.sessions = await Loadable { try await client.listSessions().ok.body.json.sessions }
    }

    /// "App · Synthetic iPhone" or "Browser", as the panel lists sessions.
    static func device(_ session: SecurityModel.Session) -> String {
        session.kind == .app ? "App · \(session.name ?? "unnamed device")" : "Browser"
    }
}

struct SecurityView: View {
    @Environment(AppState.self) private var state
    @State private var model = SecurityModel()
    @State private var enrolment: SettingsItem<Components.Schemas.TOTPEnrollment>?
    @State private var isDisabling = false
    @State private var ending: [SecurityModel.Session] = []
    /// Whether `ending` is every other session (the section's button) rather than one row.
    @State private var endingAll = false

    var body: some View {
        List {
            if let notice = model.notice { Section { NoticeRow(text: notice) } }
            if let problem = model.problem { Section { ProblemRow(problem: problem) } }
            PasswordSection(model: model)
            totpSection
            sessionsSection
        }
        .navigationTitle("Security")
        .task { await model.load(state.client) }
        .refreshable { await model.load(state.client) }
        .sheet(item: $enrolment) { item in
            TOTPSheet(model: model, enrolment: item.value)
        }
        .sheet(isPresented: $isDisabling) { DisableTOTPSheet(model: model) }
        .confirmationDialog(
            endingAll ? "Sign out every other session?" : "Sign out \(ending.first.map(SecurityModel.device) ?? "")?",
            isPresented: Binding { !ending.isEmpty } set: { if !$0 { ending = [] } },
            titleVisibility: .visible,
            presenting: ending
        ) { sessions in
            Button(endingAll ? "Sign out all others" : "Sign out session", role: .destructive) {
                let done = endingAll ? "Other sessions signed out." : "Session signed out."
                Task { await model.end(sessions, done: done, client: state.client) }
            }
            .accessibilityIdentifier("confirmEndSessions")
        } message: { _ in
            Text("A browser is signed out at once; an app at its next request.")
        }
    }

    @ViewBuilder private var totpSection: some View {
        Section("Two-factor authentication") {
            switch model.totpEnabled {
            case nil:
                ProgressView().accessibilityLabel("Loading")
            case true?:
                Label("Two-factor authentication is on.", systemImage: "checkmark.shield")
                    .accessibilityIdentifier("totpState")
                Button("Turn off…", role: .destructive) { isDisabling = true }
                    .accessibilityIdentifier("disableTOTP")
            case false?:
                Label("Two-factor authentication is off.", systemImage: "shield.slash")
                    .accessibilityIdentifier("totpState")
                Button("Set up two-factor") {
                    Task { enrolment = await model.enroll(state.client).map { SettingsItem(id: $0.otpauthUri, value: $0) } }
                }
                .disabled(model.isBusy)
                .accessibilityIdentifier("enrollTOTP")
            }
        }
    }

    @ViewBuilder private var sessionsSection: some View {
        Section {
            switch model.sessions {
            case .loading: ProgressView().accessibilityLabel("Loading")
            case .failed(let problem): ProblemRow(problem: problem)
            case .loaded(let sessions):
                ForEach(sessions, id: \.id) { session in
                    SessionRow(session: session) {
                        endingAll = false
                        ending = [session]
                    }
                }
                Button("Sign out other sessions") {
                    endingAll = true
                    ending = model.others
                }
                    .disabled(model.isBusy || model.others.isEmpty)
                    .accessibilityIdentifier("endOtherSessions")
            }
        } header: {
            Text("Sessions")
        } footer: {
            Text("Browsers and the Vitamux app signed in to this server. Signing out an app ends it at its next request.")
        }
    }
}

private struct SessionRow: View {
    let session: SecurityModel.Session
    let end: () -> Void

    var body: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text(SecurityModel.device(session))
                Text("Signed in \(Format.instant(session.createdAt)) · active \(Format.instant(session.lastSeenAt))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if session.current {
                Text("This iPhone").font(.caption).foregroundStyle(.secondary)
            } else {
                Button("Sign out", action: end)
                    .buttonStyle(.borderless)
                    .accessibilityIdentifier("endSession-\(session.id)")
            }
        }
    }
}

/// Current and new password; the change signs out every other session, so it asks first.
private struct PasswordSection: View {
    @Environment(AppState.self) private var state
    let model: SecurityModel
    @State private var current = ""
    @State private var new = ""
    @State private var problem: Problem?
    @State private var isConfirming = false

    var body: some View {
        Section {
            SecureField("Current password", text: $current)
                .textContentType(.password)
                .accessibilityIdentifier("currentPassword")
            FieldMessage(text: problem?.detail(for: "/current_password"))
            SecureField("New password", text: $new)
                .textContentType(.newPassword)
                .accessibilityIdentifier("newPassword")
            FieldMessage(text: problem?.detail(for: "/new_password"))
            if let problem, problem.fieldErrors.isEmpty { ProblemRow(problem: problem) }
            Button("Change password") { isConfirming = true }
                .disabled(model.isBusy || current.isEmpty || new.count < 12)
                .accessibilityIdentifier("changePassword")
        } header: {
            Text("Password")
        } footer: {
            Text("At least 12 characters. Changing the password signs out every other session.")
        }
        .confirmationDialog("Change the password?", isPresented: $isConfirming, titleVisibility: .visible) {
            Button("Change and sign out others", role: .destructive) {
                Task {
                    problem = await model.changePassword(current: current, new: new, client: state.client)
                    if problem == nil {
                        current = ""
                        new = ""
                    }
                }
            }
            .accessibilityIdentifier("confirmChangePassword")
        } message: {
            Text("Every other browser and app session is signed out.")
        }
    }
}

/// Enrolment: the key with copy, a link to an authenticator app on this iPhone, the code, then the
/// recovery codes once.
private struct TOTPSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let model: SecurityModel
    let enrolment: Components.Schemas.TOTPEnrollment
    @State private var code = ""
    @State private var problem: Problem?
    @State private var recovery: [String]?

    var body: some View {
        NavigationStack {
            Form {
                if let recovery {
                    Section {
                        ForEach(recovery, id: \.self) { code in
                            Text(code).font(.body.monospaced()).privacySensitive()
                        }
                        SecretValue(label: "Recovery codes", value: recovery.joined(separator: "\n"))
                    } header: {
                        Text("Recovery codes")
                    } footer: {
                        Text("Save these codes somewhere safe. Each works once if you lose your authenticator, and they are not shown again.")
                    }
                } else {
                    Section {
                        SecretValue(label: "Setup key", value: enrolment.secret)
                        if let link = URL(string: enrolment.otpauthUri) {
                            Link(destination: link) {
                                Label("Open in an authenticator app", systemImage: "arrow.up.forward.app")
                            }
                            .accessibilityIdentifier("otpauthLink")
                        }
                    } header: {
                        Text("Setup key")
                    } footer: {
                        Text("Add this key to your authenticator app: open the link on this iPhone, or enter the key by hand. Then enter the code it shows.")
                    }
                    Section {
                        TextField("Code from the app", text: $code)
                            .keyboardType(.numberPad)
                            .textContentType(.oneTimeCode)
                            .accessibilityIdentifier("totpCode")
                        FieldMessage(text: problem?.detail(for: "/code"))
                        if let problem, problem.fieldErrors.isEmpty { ProblemRow(problem: problem) }
                    }
                }
            }
            .navigationTitle(recovery == nil ? "Set up two-factor" : "Save recovery codes")
            .navigationBarTitleDisplayMode(.inline)
            .interactiveDismissDisabled(recovery != nil)
            .toolbar {
                if recovery == nil {
                    ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                    ToolbarItem(placement: .confirmationAction) {
                        Button("Turn on") {
                            Task {
                                switch await model.confirm(code: code, client: state.client) {
                                case .success(let codes):
                                    code = ""
                                    recovery = codes
                                case .failure(let failure):
                                    problem = failure
                                }
                            }
                        }
                        .disabled(model.isBusy || code.trimmingCharacters(in: .whitespaces).isEmpty)
                        .accessibilityIdentifier("confirmTOTP")
                    }
                } else {
                    ToolbarItem(placement: .confirmationAction) {
                        Button("I have saved them") {
                            recovery = nil
                            dismiss()
                        }
                        .accessibilityIdentifier("savedRecoveryCodes")
                    }
                }
            }
        }
        .sheetBackground()
    }
}

/// Turning two-factor off needs the password and a code from the authenticator, or a recovery code.
private struct DisableTOTPSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let model: SecurityModel
    @State private var password = ""
    @State private var code = ""
    @State private var recovery = ""
    @State private var problem: Problem?

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    SecureField("Password", text: $password)
                        .textContentType(.password)
                        .accessibilityIdentifier("disablePassword")
                    FieldMessage(text: problem?.detail(for: "/password"))
                    TextField("Authenticator code", text: $code)
                        .keyboardType(.numberPad)
                        .textContentType(.oneTimeCode)
                        .accessibilityIdentifier("disableCode")
                    TextField("Or a recovery code", text: $recovery)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .accessibilityIdentifier("disableRecovery")
                    if let problem, problem.fieldErrors.isEmpty { ProblemRow(problem: problem) }
                } footer: {
                    Text("Enter your password and a code from your authenticator, or one recovery code. Sign-in then needs only the password.")
                }
                Section {
                    Button("Turn off two-factor", role: .destructive) {
                        Task {
                            problem = await model.disable(password: password, code: code, recovery: recovery, client: state.client)
                            password = ""
                            if problem == nil { dismiss() }
                        }
                    }
                    .disabled(model.isBusy || password.isEmpty || (code.isEmpty && recovery.isEmpty))
                    .accessibilityIdentifier("confirmDisableTOTP")
                }
            }
            .navigationTitle("Turn off two-factor")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            }
        }
        .sheetBackground()
    }
}
