import BackgroundTasks
import Foundation
import HealthBridgeCore
import HealthBridgeHealthKit
import HealthKit
import Observation
import Synchronization
import UIKit
import VitamuxKit

/// This iPhone as an Apple Health source, moved from Bridge's `AppModel`: the pairing, the
/// enabled metric groups, per-type status and HealthBridgeKit's sync engine. It keeps Bridge's
/// Keychain service and defaults keys, so an installed Bridge upgrades in place without
/// re-pairing or a full re-pull. The device token is not the app session: sync runs while signed
/// out, and only unpairing (or the server revoking the token) stops it.
@Observable
final class ThisDevice {
    static let refreshTaskID = "org.vitamux.healthbridge.refresh"

    struct TypeStatus: Codable, Equatable {
        var lastSync: Date?
        var lastError: String?
    }

    /// The current run: types done out of the types queued.
    struct Progress: Equatable {
        var done: Int
        var total: Int
    }

    private(set) var credentials: Credentials?
    private(set) var enabled: Set<MetricGroup>
    private(set) var status: [String: TypeStatus]
    /// Short hashes of the stored anchors (`none` before a type's first full pull).
    private(set) var anchorHashes: [String: String] = [:]
    private(set) var progress: Progress?
    /// Finished runs since launch, so a screen can reload the server's view after each.
    private(set) var finishedRuns = 0
    private(set) var isPairing = false
    /// The server answered 401 to the device token (revoked, or rotated elsewhere): sync stops
    /// until the owner pairs again.
    private(set) var isRevoked = false
    /// When the newest server-requested anchor reset was applied here.
    private(set) var lastAppliedReset: Date?
    var problem: Problem?

    var isPaired: Bool { credentials != nil }
    var isSyncing: Bool { progress != nil }
    var healthAvailable: Bool { !isLive || HKHealthStore.isHealthDataAvailable() }
    /// Enabled types this OS knows; an identifier it does not know is skipped by the kit too.
    var enabledTypes: [HealthType] { Registry.types(in: enabled).filter { $0.sampleType != nil } }
    var lastUpload: Date? { status.values.compactMap(\.lastSync).max() }
    /// Enabled types whose last run failed; their pages are re-sent on the next run.
    var waiting: [HealthType] { enabledTypes.filter { status[$0.id]?.lastError != nil } }
    /// Enabled types without an anchor yet: their history is still to be pulled.
    var historyPending: [HealthType] { enabledTypes.filter { (anchorHashes[$0.id] ?? "none") == "none" } }

    func types(in group: MetricGroup) -> [HealthType] { Registry.types(in: [group]).filter { $0.sampleType != nil } }

    @ObservationIgnored let tokens: TokenStore
    @ObservationIgnored let defaults: UserDefaults
    @ObservationIgnored private let store: HealthStore
    @ObservationIgnored private let transport: Transport
    @ObservationIgnored private let anchors: AnchorStore
    /// The running app (HealthKit, background tasks); false under UI tests.
    @ObservationIgnored private let isLive: Bool
    /// The types observer callbacks may sync. Observer queries cannot be removed, so a new
    /// pairing gets a new gate and closes the old one.
    @ObservationIgnored private var gate = Gate()
    @ObservationIgnored private var sync: HealthSync?
    @ObservationIgnored private var observed: Set<String> = []
    @ObservationIgnored private var queue: [HealthType] = []
    @ObservationIgnored private var running: Task<Void, Never>?

    init(store: HealthStore, transport: @escaping Transport, tokens: TokenStore, defaults: UserDefaults, isLive: Bool) {
        self.store = store
        self.transport = transport
        self.tokens = tokens
        self.defaults = defaults
        self.isLive = isLive
        anchors = AnchorStore(defaults: defaults)
        credentials = (try? tokens.load()) ?? nil
        enabled = Set((defaults.stringArray(forKey: Key.groups) ?? []).compactMap(MetricGroup.init(rawValue:)))
        status = defaults.data(forKey: Key.status).flatMap { try? JSONDecoder().decode([String: TypeStatus].self, from: $0) } ?? [:]
        lastAppliedReset = defaults.object(forKey: Key.reset) as? Date
        refreshAnchors()
        gate.set(Set(enabledTypes.map(\.id)))
    }

    /// Bridge's stores: its Keychain service and the standard defaults.
    static func live() -> ThisDevice {
        ThisDevice(store: HKHealthStore(), transport: IngestClient.urlSession, tokens: TokenStore(), defaults: .standard, isLive: true)
    }

    private enum Key {
        static let groups = "enabledGroups", status = "typeStatus", reset = "lastAppliedReset"
    }

    // MARK: Pairing

    /// One-tap pairing: the signed-in session creates a code and this iPhone redeems it.
    func pairHere(using client: Client, on profile: ServerProfile) async {
        await pairing {
            let code = try await client.createPairingCode().created.body.json.code
            return try await IngestClient(transport: transport).pair(baseURL: profile.baseURL, code: code, deviceName: Self.deviceName)
        }
    }

    /// Manual pairing with a code from the panel (typed, or the QR's `{"url","code"}`): a phone
    /// that only syncs, or another server.
    func pair(address: String, code: String) async {
        let base: URL
        do {
            base = try ServerProfile(address, allowsLocalHTTP: false).baseURL
        } catch {
            problem = error
            return
        }
        let code = code.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !code.isEmpty else {
            problem = Problem(title: "Enter the pairing code")
            return
        }
        await pairing { try await IngestClient(transport: transport).pair(baseURL: base, code: code, deviceName: Self.deviceName) }
    }

    /// The QR payload is `{"url":"<public base URL>","code":"XXXX-XXXX"}`.
    static func parsePairingPayload(_ payload: String) -> (url: String, code: String)? {
        struct Payload: Decodable { let url, code: String }
        guard let parsed = try? JSONDecoder().decode(Payload.self, from: Data(payload.utf8)) else { return nil }
        return (parsed.url, parsed.code)
    }

    private func pairing(_ exchange: () async throws -> Credentials) async {
        isPairing = true
        defer { isPairing = false }
        do {
            let paired = try await exchange()
            try tokens.save(paired)
            // The same connection keeps its anchors (the server dedupes by UUID); another server
            // or connection gets a full pull.
            if let old = credentials, old.baseURL != paired.baseURL || old.connectionID != paired.connectionID {
                for type in Registry.v1 { anchors.reset(type.id) }
                refreshAnchors()
            }
            replaceEngine()
            credentials = paired
            isRevoked = false
            problem = nil
            await start()
        } catch BridgeError.rejected, BridgeError.unauthorized {
            problem = Problem(title: "The server did not accept this code", detail: "Codes work once and expire after 10 minutes.")
        } catch {
            problem = Self.problem(error)
        }
    }

    /// Revokes the device token on the server it was paired with (when that is the signed-in
    /// server) and forgets the pairing here.
    func unpair(using client: Client?, on profile: ServerProfile?) async throws {
        guard let credentials else { return }
        if let client, let profile, (try? ServerProfile(credentials.baseURL.absoluteString))?.baseURL == profile.baseURL {
            do {
                _ = try await client.revokeDevice(path: .init(id: credentials.deviceID)).noContent
            } catch {
                // Already revoked or removed on the server.
                guard Problem(error).status == 404 else { throw error }
            }
        }
        forget()
    }

    /// What Bridge's unpair clears: the token, every anchor, enabled groups and sync status, so a
    /// new pairing pulls in full.
    func forget() {
        replaceEngine()
        if isLive, HKHealthStore.isHealthDataAvailable() {
            HKHealthStore().disableAllBackgroundDelivery { _, _ in }
        }
        try? tokens.delete()
        for key in defaults.dictionaryRepresentation().keys where key.hasPrefix("anchor.") {
            defaults.removeObject(forKey: key)
        }
        for key in [Key.groups, Key.status, Key.reset] {
            defaults.removeObject(forKey: key)
        }
        credentials = nil
        enabled = []
        status = [:]
        lastAppliedReset = nil
        isRevoked = false
        problem = nil
        refreshAnchors()
    }

    /// Closes the observers of the current engine; the next `start` builds a new one.
    private func replaceEngine() {
        gate.set([])
        gate = Gate()
        sync = nil
        observed = []
        queue = []
    }

    // MARK: Sync

    /// Idempotent: builds the engine, applies server-requested resets, registers observer queries
    /// with background delivery for the enabled types and syncs them. Runs at launch (from the app
    /// delegate, so HealthKit finds the observers when it wakes the app), on becoming active and
    /// after pairing.
    func start() async {
        guard let credentials, !isRevoked else { return }
        let engine = engine(for: credentials)
        await applyServerResets(engine, credentials: credentials)
        guard self.credentials == credentials, !isRevoked else { return }
        await observe(enabledTypes)
        await run(enabledTypes)
    }

    func syncNow() async {
        await start()
    }

    /// Runs from `BGAppRefreshTask`; SwiftUI completes the task when this returns.
    func backgroundRefresh() async {
        await start()
        scheduleRefresh()
    }

    func scheduleRefresh() {
        guard isLive, isPaired else { return }
        let request = BGAppRefreshTaskRequest(identifier: Self.refreshTaskID)
        request.earliestBeginDate = Date(timeIntervalSinceNow: 3600)
        try? BGTaskScheduler.shared.submit(request)
    }

    private func engine(for credentials: Credentials) -> HealthSync {
        if let sync { return sync }
        let client = IngestClient(transport: transport)
        let sender = BatchSender(credentials: credentials, anchors: anchors, client: client, clientVersion: Self.version)
        let engine = HealthSync(store: GatedStore(base: store, gate: gate), sender: sender, anchors: anchors)
        sync = engine
        gate.set(Set(enabledTypes.map(\.id)))
        return engine
    }

    /// Anchor resets the owner requested on the server, newer than the last one applied here.
    private func applyServerResets(_ engine: HealthSync, credentials: Credentials) async {
        do {
            let me = try await IngestClient(transport: transport).deviceSelf(credentials: credentials)
            let applied = lastAppliedReset ?? .distantPast
            let fresh = me.anchorResets.filter { $0.requestedAt > applied }
            for reset in fresh {
                for type in Registry.v1 where reset.type == "*" || reset.type == type.id { await engine.resetAnchor(type) }
            }
            if let newest = fresh.map(\.requestedAt).max() {
                lastAppliedReset = newest
                defaults.set(newest, forKey: Key.reset)
                refreshAnchors()
            }
        } catch BridgeError.unauthorized {
            revoked()
        } catch {
            // Offline or a server error: the next start reads the resets again.
        }
    }

    /// Enabling requests read authorization (HealthKit never says whether it was granted) and
    /// gives the group's types a new anchor, so they are pulled in full. Disabling stops syncing
    /// and keeps what the server already has.
    func setGroup(_ group: MetricGroup, enabled on: Bool) async {
        guard let credentials else { return }
        let engine = engine(for: credentials)
        guard on else {
            enabled.remove(group)
            gate.set(Set(enabledTypes.map(\.id)))
            persist()
            return
        }
        do {
            try await engine.requestAuthorization(for: [group])
        } catch {
            problem = Problem(title: "Authorization request failed", detail: Self.describe(error))
            return
        }
        let types = types(in: group)
        for type in types { await engine.resetAnchor(type) }
        enabled.insert(group)
        gate.set(Set(enabledTypes.map(\.id)))
        persist()
        refreshAnchors()
        await observe(types)
        await run(types)
    }

    /// The next sync of `type` is a full pull; the server dedupes by UUID.
    func resetAnchor(_ type: HealthType) async {
        await resetAnchors([type])
    }

    func resetAllAnchors() async {
        await resetAnchors(enabledTypes)
    }

    private func resetAnchors(_ types: [HealthType]) async {
        guard let credentials else { return }
        let engine = engine(for: credentials)
        for type in types { await engine.resetAnchor(type) }
        refreshAnchors()
        await run(types)
    }

    private func observe(_ types: [HealthType]) async {
        guard let sync else { return }
        let fresh = types.filter { observed.insert($0.id).inserted }
        do {
            try await sync.observe(fresh)
        } catch {
            problem = Problem(title: "Background delivery could not be enabled", detail: Self.describe(error))
        }
    }

    /// Queues `types` and returns when the queue is drained and the heartbeat sent. One run at a
    /// time: a call during a run adds its types to it and waits for the same run.
    private func run(_ types: [HealthType]) async {
        guard sync != nil else { return }
        queue.append(contentsOf: types.filter { !queue.contains($0) })
        if running == nil {
            running = Task { await drain() }
        }
        await running?.value
    }

    private func drain() async {
        var done = 0
        var failures: [any Error] = []
        progress = Progress(done: 0, total: queue.count)
        while let sync, !queue.isEmpty, !isRevoked {
            let type = queue.removeFirst()
            progress = Progress(done: done, total: done + queue.count + 1)
            do {
                try await sync.sync(type)
                status[type.id] = TypeStatus(lastSync: .now, lastError: nil)
            } catch {
                status[type.id, default: TypeStatus()].lastError = Self.describe(error)
                failures.append(error)
                if case BridgeError.unauthorized = error { revoked() }
            }
            done += 1
        }
        refreshAnchors()
        persist()
        await sendHeartbeat(failures: failures)
        progress = nil
        running = nil
        finishedRuns += 1
    }

    private func revoked() {
        isRevoked = true
        gate.set([])
        queue = []
    }

    /// Best effort, at the end of a run: tells the server which anchors this phone holds, so it
    /// lists the types and flags silent ones. Anchor hashes only, never values.
    private func sendHeartbeat(failures: [any Error]) async {
        guard let credentials, !isRevoked else { return }
        let hashes = Dictionary(uniqueKeysWithValues: enabledTypes.map { ($0.id, AnchorStore.hash(anchors.anchor(for: $0.id))) })
        try? await IngestClient(transport: transport).heartbeat(
            credentials: credentials, clientVersion: Self.version, anchorHashes: hashes, lastSuccessAt: lastUpload,
            failedUnits: failures.count, errorClass: failures.first.map(Self.errorClass)
        )
    }

    private func refreshAnchors() {
        anchorHashes = Dictionary(uniqueKeysWithValues: Registry.v1.map { ($0.id, AnchorStore.hash(anchors.anchor(for: $0.id))) })
    }

    private func persist() {
        defaults.set(enabled.map(\.rawValue).sorted(), forKey: Key.groups)
        defaults.set(try? JSONEncoder().encode(status), forKey: Key.status)
    }

    // MARK: Text

    private static var version: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0"
    }

    private static var deviceName: String { UIDevice.current.name }

    static func errorClass(_ error: any Error) -> String {
        switch error {
        case BridgeError.unauthorized: "reauth_required"
        case BridgeError.rejected: "permanent"
        case is URLError: "network"
        default: "transient"
        }
    }

    static func describe(_ error: any Error) -> String {
        switch error {
        case BridgeError.unauthorized: "The server no longer accepts this iPhone's device token."
        case BridgeError.rejected(let status): "The server refused the upload (HTTP \(status))."
        case BridgeError.badResponse: "The server sent an unexpected response."
        case let error as URLError: "Network error: \(error.localizedDescription)"
        default: error.localizedDescription
        }
    }

    private static func problem(_ error: any Error) -> Problem {
        switch error {
        case is BridgeError, is URLError: Problem(title: "Could not pair", detail: describe(error))
        default: Problem(error)
        }
    }
}

/// The types that may currently sync. HealthKit observer queries cannot be removed through
/// `HealthStore`, so a disabled type or an unpaired device makes its observer callbacks do nothing.
nonisolated final class Gate: Sendable {
    private let ids = Mutex<Set<String>>([])

    func set(_ ids: Set<String>) { self.ids.withLock { $0 = ids } }
    func allows(_ id: String) -> Bool { ids.withLock { $0.contains(id) } }
}

/// Wraps a store so observer callbacks of types the gate closes complete at once without syncing.
nonisolated struct GatedStore: HealthStore {
    let base: HealthStore
    let gate: Gate

    func requestReadAuthorization(_ types: Set<HKObjectType>) async throws { try await base.requestReadAuthorization(types) }
    func earliestPermittedSampleDate() -> Date { base.earliestPermittedSampleDate() }
    func anchoredPage(of type: HKSampleType, from start: Date, anchor: Data?, limit: Int) async throws -> AnchoredPage {
        try await base.anchoredPage(of: type, from: start, anchor: anchor, limit: limit)
    }
    func observe(_ type: HKSampleType, onUpdate: @escaping @Sendable (@escaping @Sendable () -> Void) -> Void) {
        let id = type.identifier, gate = gate
        base.observe(type) { done in
            if gate.allows(id) { onUpdate(done) } else { done() }
        }
    }
    func enableBackgroundDelivery(for type: HKSampleType) async throws { try await base.enableBackgroundDelivery(for: type) }
}

#if DEBUG
extension ThisDevice {
    /// Bridge's `-uitest` stores, so the upgrade check reads what a Bridge build wrote, with a
    /// fake HealthStore and the fake server. `-uitest-paired` starts paired as
    /// `FakeServer.AppleHealth.deviceID`; `-uitest-keep-device` keeps what a Bridge build left;
    /// `-uitest-revoked` revokes that device on the server; `-uitest-anchor-reset` has the server
    /// ask it to pull every type again.
    static func uiTest(arguments: [String]) -> ThisDevice {
        let server = FakeServer.uiTest
        let transport: Transport = { request in
            let (data, response) = try await server.urlSession.data(for: request)
            guard let http = response as? HTTPURLResponse else { throw BridgeError.badResponse }
            return (data, http)
        }
        let tokens = TokenStore(service: "org.vitamux.healthbridge.uitest")
        let defaults = UserDefaults(suiteName: "org.vitamux.healthbridge.uitest")!
        if !arguments.contains("-uitest-keep-device") {
            try? tokens.delete()
            defaults.removePersistentDomain(forName: "org.vitamux.healthbridge.uitest")
        }
        if arguments.contains("-uitest-paired") {
            try? tokens.save(Credentials(
                baseURL: server.profile.baseURL, deviceID: FakeServer.AppleHealth.deviceID,
                connectionID: FakeServer.AppleHealth.connectionID, token: FakeServer.AppleHealth.deviceToken
            ))
        }
        if arguments.contains("-uitest-revoked") { server.revokeDevice(FakeServer.AppleHealth.deviceID) }
        if arguments.contains("-uitest-anchor-reset") { server.requestAnchorReset(deviceID: FakeServer.AppleHealth.deviceID) }
        return ThisDevice(store: FakeHealthStore(), transport: transport, tokens: tokens, defaults: defaults, isLive: false)
    }
}

/// Grants every authorization request and serves empty pages with a synthetic anchor, so a
/// synced type shows an anchor and a reset type shows none until its next pull.
nonisolated struct FakeHealthStore: HealthStore {
    func requestReadAuthorization(_ types: Set<HKObjectType>) async throws {}
    func earliestPermittedSampleDate() -> Date { .distantPast }
    func anchoredPage(of type: HKSampleType, from start: Date, anchor: Data?, limit: Int) async throws -> AnchoredPage {
        AnchoredPage(samples: [], deleted: [], anchor: anchor ?? Data("synthetic-anchor-\(type.identifier)".utf8))
    }
    func observe(_ type: HKSampleType, onUpdate: @escaping @Sendable (@escaping @Sendable () -> Void) -> Void) {}
    func enableBackgroundDelivery(for type: HKSampleType) async throws {}
}
#endif
