import Foundation
import HealthBridgeCore
import LocalAuthentication
import Observation
import SwiftUI
import VitamuxKit

/// The one shared object, in the environment: the server profile, the session and its client,
/// the router, and the app's own preferences (theme, app lock). Screens keep their own state.
@Observable
final class AppState {
    /// The handshake's `api_version` this app needs; an older server is refused.
    static let requiredAPIVersion = 1

    enum SignInReason: Equatable {
        case expired, signedOut
    }

    // MARK: Server and session

    /// The server last signed in to; kept when the session expires or the owner signs out.
    private(set) var profile: ServerProfile?
    /// The generated client for `profile` with the session; nil while signed out.
    private(set) var client: Client?
    private(set) var username: String?
    /// Why sign-in is showing: nil on first run.
    private(set) var signInReason: SignInReason?

    var isSignedIn: Bool { client != nil }

    /// The offline read cache's state, for the shell's banner.
    private(set) var cacheStatus: ResponseCache.Status = .online

    // MARK: Router

    var tab: AppTab = .dashboard
    var paths: [AppTab: [Route]] = [:]
    var isSearching = false
    /// The root route each tab last opened with, so a root gets its link's query.
    private(set) var roots: [AppTab: Route] = [:]
    /// A link that arrived while signed out or locked; it opens after sign-in or unlock.
    private var pending: Route?

    // MARK: Preferences

    var theme: Theme {
        didSet { defaults.set(theme.rawValue, forKey: Key.theme) }
    }
    private(set) var appLock: Bool {
        didSet { defaults.set(appLock, forKey: Key.appLock) }
    }
    var lockAfter: LockTimeout {
        didSet { defaults.set(lockAfter.rawValue, forKey: Key.lockAfter) }
    }
    private(set) var isLocked = false
    private var backgroundedAt: Date?

    // MARK: Environment

    let sessions: SessionStore
    /// The offline read cache (`ResponseCache`): files in the app group, 100 MB.
    let cache: ResponseCache
    let device: ThisDevice
    let isUITest: Bool
    private let urlSession: URLSession
    private let defaults: UserDefaults

    private enum Key {
        static let server = "server", username = "username", theme = "theme"
        static let appLock = "appLock", lockAfter = "appLockAfter"
    }

    init(
        sessions: SessionStore, cache: ResponseCache, urlSession: URLSession, defaults: UserDefaults, device: ThisDevice,
        isUITest: Bool = false
    ) {
        self.sessions = sessions
        self.cache = cache
        self.urlSession = urlSession
        self.defaults = defaults
        self.device = device
        self.isUITest = isUITest
        theme = defaults.string(forKey: Key.theme).flatMap(Theme.init(rawValue:)) ?? .system
        appLock = defaults.bool(forKey: Key.appLock)
        lockAfter = LockTimeout(rawValue: defaults.integer(forKey: Key.lockAfter)) ?? .immediately
        username = defaults.string(forKey: Key.username)
        profile = defaults.data(forKey: Key.server).flatMap { try? JSONDecoder().decode(ServerProfile.self, from: $0) }
        if let profile, let entry = try? sessions.load(), entry.server == profile.baseURL {
            client = makeClient(for: profile)
            isLocked = appLock
        }
        // AppState lives as long as the app; the cache calls from any thread.
        cache.onStatusChange { [self] _ in
            Task { @MainActor in self.cacheStatus = self.cache.status }
        }
    }

    /// The running app: the Keychain, the network, and Bridge's stores, so an installed Bridge
    /// upgrades in place with its device token and anchors.
    static func live() -> AppState {
        AppState(
            sessions: .keychain(),
            cache: ResponseCache(appGroup: "group.org.vitamux.healthbridge"),
            urlSession: .shared,
            defaults: UserDefaults(suiteName: "group.org.vitamux.healthbridge") ?? .standard,
            device: .live()
        )
    }

    // MARK: Sign-in and sign-out

    /// A client for `profile`; requests carry the session only once it is saved for that server.
    func makeClient(for profile: ServerProfile) -> Client {
        // AppState lives as long as the app, so the client may hold it.
        Client(profile: profile, sessions: sessions, cache: cache, urlSession: urlSession) { [self] in
            Task { @MainActor in self.sessionExpired() }
        }
    }

    func didSignIn(_ session: Components.Schemas.AppSession, to profile: ServerProfile) throws {
        try sessions.save(session, for: profile)
        // A new session (or server) never reads an old one's answers.
        cache.clear()
        defaults.set(try JSONEncoder().encode(profile), forKey: Key.server)
        defaults.set(session.user.username, forKey: Key.username)
        self.profile = profile
        username = session.user.username
        client = makeClient(for: profile)
        signInReason = nil
        isLocked = false
        openPending()
    }

    /// Any `401` outside sign-in: back to sign-in with "session expired", keeping the server.
    func sessionExpired() {
        guard client != nil else { return }
        endSession(.expired)
    }

    /// Revokes the app session (and, if asked, this iPhone's device token) on the server, then
    /// signs out here. Apple Health sync keeps running unless the iPhone is unpaired too.
    func signOut(unpairing: Bool) async throws {
        guard let client, let profile else { return }
        if unpairing { try await device.unpair(using: client, on: profile) }
        do {
            _ = try await client.logout().noContent
        } catch {
            // Already ended on the server: nothing left to revoke.
            guard Problem(error).status == 401 else { throw error }
        }
        endSession(.signedOut)
    }

    private func endSession(_ reason: SignInReason) {
        try? sessions.clear()
        cache.clear()
        client = nil
        signInReason = reason
        isLocked = false
        isSearching = false
        tab = .dashboard
        paths = [:]
        roots = [:]
    }

    // MARK: Router

    /// A `vitamux://` link: its route, or its tab's root for a path without one.
    func open(_ url: URL) {
        #if DEBUG
        // UI tests end the fake server's sessions to test expiry mid-use.
        if isUITest, Route.segments(of: url) == ["uitest", "expire-sessions"] {
            FakeServer.uiTest.expireSessions()
            return
        }
        // ... and take the fake server off and on the network for the offline cache.
        if isUITest, let segments = Route.segments(of: url), segments == ["uitest", "offline"] || segments == ["uitest", "online"] {
            FakeServer.uiTest.isOffline = segments[1] == "offline"
            return
        }
        #endif
        guard let route = Route(url: url) ?? AppTab(url: url)?.root else { return }
        open(route)
    }

    /// Shows `route` on its tab; while signed out or locked it waits.
    func open(_ route: Route) {
        guard isSignedIn, !isLocked else {
            pending = route
            return
        }
        isSearching = false
        tab = route.tab
        if route.isTabRoot {
            roots[route.tab] = route
            paths[route.tab] = []
        } else {
            paths[route.tab] = [route]
        }
    }

    func root(of tab: AppTab) -> Route {
        roots[tab] ?? tab.root
    }

    private func openPending() {
        guard let route = pending else { return }
        pending = nil
        open(route)
    }

    // MARK: App lock

    /// Locks on leaving the app (after `lockAfter`) and covers what the app-switcher snapshots.
    func scenePhaseChanged(to phase: ScenePhase) {
        switch phase {
        case .background:
            backgroundedAt = .now
            if appLock, isSignedIn, lockAfter == .immediately { isLocked = true }
        case .active:
            if appLock, isSignedIn, let since = backgroundedAt,
               Date.now.timeIntervalSince(since) >= TimeInterval(lockAfter.rawValue) {
                isLocked = true
            }
            backgroundedAt = nil
        default:
            break
        }
    }

    func unlock() async {
        guard isLocked, await authenticate(reason: "Unlock Vitamux") else { return }
        isLocked = false
        openPending()
    }

    /// Turning the lock on asks for Face ID or the passcode once, so it is known to work.
    func setAppLock(_ on: Bool) async -> Problem? {
        guard on else {
            appLock = false
            return nil
        }
        guard isUITest || LAContext().canEvaluatePolicy(.deviceOwnerAuthentication, error: nil) else {
            return Problem(
                title: "Set a passcode first",
                detail: "App lock uses this iPhone's Face ID, Touch ID or passcode. Set a passcode in the Settings app."
            )
        }
        if await authenticate(reason: "Turn on app lock") { appLock = true }
        return nil
    }

    /// What unlocks the app on this iPhone.
    var lockMethod: String {
        let context = LAContext()
        _ = context.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: nil)
        switch context.biometryType {
        case .faceID: return "Face ID"
        case .touchID: return "Touch ID"
        case .opticID: return "Optic ID"
        default: return "Passcode"
        }
    }

    /// UI tests have no Face ID or passcode: the fake unlocks on the tap.
    private func authenticate(reason: String) async -> Bool {
        if isUITest { return true }
        return (try? await LAContext().evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason)) ?? false
    }
}

enum Theme: String, CaseIterable {
    case system, light, dark

    var label: String {
        switch self {
        case .system: "System"
        case .light: "Light"
        case .dark: "Dark"
        }
    }

    var colorScheme: ColorScheme? {
        switch self {
        case .system: nil
        case .light: .light
        case .dark: .dark
        }
    }
}

/// How long the app may be away before app lock asks again.
enum LockTimeout: Int, CaseIterable {
    case immediately = 0, oneMinute = 60, fiveMinutes = 300, fifteenMinutes = 900

    var label: String {
        switch self {
        case .immediately: "Immediately"
        case .oneMinute: "After 1 minute"
        case .fiveMinutes: "After 5 minutes"
        case .fifteenMinutes: "After 15 minutes"
        }
    }
}

#if DEBUG
extension AppState {
    /// Under `-uitest`: the fake server (`FakeServer.uiTestServers`), an in-memory session, and
    /// cleared preferences. `-uitest-totp` turns two-factor on; `-uitest-paired`,
    /// `-uitest-keep-device`, `-uitest-revoked` and `-uitest-anchor-reset` set up this iPhone's
    /// pairing (`ThisDevice.uiTest`); `-uitest-empty-install` and `-uitest-panel-layout` pick the
    /// dashboard's start.
    static func uiTest(arguments: [String]) -> AppState {
        let servers = FakeServer.uiTestServers
        FakeServer.uiTest.totpEnabled = arguments.contains("-uitest-totp")
        FakeServer.uiTest.dashboardInstall = .init(arguments: arguments)
        let defaults = UserDefaults(suiteName: "org.vitamux.app.uitest")!
        defaults.removePersistentDomain(forName: "org.vitamux.app.uitest")
        let device = ThisDevice.uiTest(arguments: arguments)
        let cache = ResponseCache(directory: URL.temporaryDirectory.appending(path: "uitest-cache", directoryHint: .isDirectory))
        cache.clear()
        return AppState(
            sessions: .inMemory(), cache: cache, urlSession: servers[0].urlSession, defaults: defaults, device: device,
            isUITest: true
        )
    }
}
#endif
