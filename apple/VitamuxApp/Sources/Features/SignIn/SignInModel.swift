import Foundation
import Observation
import OpenAPIRuntime
import UIKit
import VitamuxKit

/// The server step and sign-in (docs/architecture/ios-app.md#server-and-sign-in): check the
/// address with the version handshake, then `POST /auth/login` with `client: app`; a
/// `totp_required` answer asks for the code, a `429` waits for `Retry-After`. The password and
/// code stay in memory only until sign-in ends.
@Observable
final class SignInModel {
    enum Step {
        case credentials, code
    }

    var address: String
    var username: String
    var password = ""
    var code = ""
    var usesRecoveryCode = false

    private(set) var step = Step.credentials
    private(set) var isBusy = false
    /// What is wrong with the address or the server; shown under the address.
    private(set) var serverProblem: Problem?
    /// Why sign-in failed; shown under the button.
    private(set) var problem: Problem?
    /// Sign-in is rate limited until then.
    private(set) var retryAt: Date?
    /// The server expects a newer app: a warning, not a refusal.
    private(set) var appUpdateNote: String?

    init(profile: ServerProfile?, username: String?) {
        address = profile.map { $0.baseURL.absoluteString } ?? ""
        self.username = username ?? ""
    }

    var canSubmit: Bool {
        guard !isBusy, !username.isEmpty, !password.isEmpty, !address.isEmpty else { return false }
        return step == .credentials || !code.trimmingCharacters(in: .whitespaces).isEmpty
    }

    /// A scanned pairing QR (`{"url","code"}`) fills the address; anything else is ignored.
    func scanned(_ payload: String) {
        struct Pairing: Decodable { let url: String }
        if let pairing = try? JSONDecoder().decode(Pairing.self, from: Data(payload.utf8)) {
            address = pairing.url
            serverProblem = nil
        } else {
            serverProblem = Problem(title: "Not a pairing code", detail: "Scan the QR code shown in Settings › Devices on your server.")
        }
    }

    func submit(to state: AppState) async {
        guard canSubmit else { return }
        isBusy = true
        defer { isBusy = false }
        serverProblem = nil
        problem = nil
        let profile: ServerProfile
        do {
            profile = try ServerProfile(address)
        } catch {
            serverProblem = error
            return
        }
        let client = state.makeClient(for: profile)
        do {
            try await checkServer(client)
        } catch {
            serverProblem = Self.serverProblem(error)
            return
        }
        await signIn(client, profile: profile, state: state)
    }

    /// The handshake: a Vitamux server with an API this app knows.
    private func checkServer(_ client: Client) async throws {
        let version = try await client.getSystemVersion().ok.body.json
        guard version.apiVersion >= AppState.requiredAPIVersion else { throw Self.tooOld }
        let app = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0"
        appUpdateNote = Self.isVersion(app, olderThan: version.minAppVersion)
            ? "This server expects version \(version.minAppVersion) of the app or later. Update the app if a screen does not load."
            : nil
    }

    private func signIn(_ client: Client, profile: ServerProfile, state: AppState) async {
        let trimmed = code.trimmingCharacters(in: .whitespaces)
        let body = Components.Schemas.LoginRequest(
            username: username,
            password: password,
            totpCode: step == .code && !usesRecoveryCode ? trimmed : nil,
            recoveryCode: step == .code && usesRecoveryCode ? trimmed : nil,
            client: .app,
            deviceName: UIDevice.current.name
        )
        do {
            guard case .AppSession(let session) = try await client.login(body: .json(body)).ok.body.json else {
                throw Problem(title: "Unexpected response", detail: "The server answered with a browser session.")
            }
            try state.didSignIn(session, to: profile)
            password = ""
            code = ""
            step = .credentials
        } catch {
            let problem = Problem(error)
            if problem.code == "totp_required" {
                step = .code
            } else {
                if let wait = problem.retryAfter { retryAt = .now.addingTimeInterval(TimeInterval(wait.components.seconds)) }
                self.problem = problem
            }
        }
    }

    // MARK: Server errors

    private static let tooOld = Problem(
        title: "The server needs an update",
        detail: "This Vitamux server is older than the app supports. Update it to version 0.4.0 or later."
    )

    /// Unreachable, not Vitamux, or a Vitamux server from before the handshake.
    static func serverProblem(_ error: any Error) -> Problem {
        let underlying = (error as? ClientError)?.underlyingError ?? error
        switch underlying {
        case let error as URLError:
            return Problem(title: "Can't reach the server", detail: "Check the address and your connection. \(error.localizedDescription)")
        case let problem as Problem where problem.title == tooOld.title:
            return problem
        case let problem as Problem where problem.status == 401 && problem.code != nil:
            // A Vitamux server whose version route still needs a session predates the handshake.
            return tooOld
        default:
            return Problem(
                title: "This is not a Vitamux server",
                detail: "The address answered, but not as Vitamux does. Check it, including any path after the host."
            )
        }
    }

    /// Compares dotted versions numerically: `0.4.0` is older than `0.10.0`.
    static func isVersion(_ version: String, olderThan minimum: String) -> Bool {
        let parts = { (text: String) in text.split(separator: ".").map { Int($0.prefix { $0.isNumber }) ?? 0 } }
        let a = parts(version), b = parts(minimum)
        for index in 0..<max(a.count, b.count) {
            let x = index < a.count ? a[index] : 0, y = index < b.count ? b[index] : 0
            if x != y { return x < y }
        }
        return false
    }
}
