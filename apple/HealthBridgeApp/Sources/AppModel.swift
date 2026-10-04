import BackgroundTasks
import Foundation
import HealthBridgeCore
import HealthBridgeHealthKit
import HealthKit
import Observation
import UIKit

struct TypeStatus: Codable, Equatable {
    var lastSync: Date?
    var lastError: String?
}

/// The single observable model: pairing state, enabled groups, per-type status, and the sync engine.
@MainActor @Observable
final class AppModel {
    static let refreshTaskID = "org.vitamux.healthbridge.refresh"

    private(set) var credentials: Credentials?
    private(set) var enabled: Set<MetricGroup>
    private(set) var status: [String: TypeStatus]
    private(set) var isSyncing = false
    private(set) var isBusy = false
    /// The server rejected the stored token: the owner must pair again.
    private(set) var needsRepair = false
    var message: String?

    private let services: Services
    private let anchors: AnchorStore
    private let gate = Gate()
    private var sync: HealthSync?
    private var observed: Set<String> = []

    init(services: Services) {
        self.services = services
        anchors = AnchorStore(defaults: services.defaults)
        credentials = (try? services.tokens.load()) ?? nil
        enabled = Set((services.defaults.stringArray(forKey: "enabledGroups") ?? []).compactMap(MetricGroup.init(rawValue:)))
        status = (services.defaults.data(forKey: "typeStatus")).flatMap { try? JSONDecoder().decode([String: TypeStatus].self, from: $0) } ?? [:]
        gate.set(Set(enabledTypes.map(\.id)))
    }

    var isPaired: Bool { credentials != nil }
    var healthAvailable: Bool { HKHealthStore.isHealthDataAvailable() || ProcessInfo.processInfo.arguments.contains("-uitest") }
    /// Enabled types this OS knows; an identifier it does not know is skipped by the kit too.
    var enabledTypes: [HealthType] { Registry.types(in: enabled).filter { $0.sampleType != nil } }

    func types(in group: MetricGroup) -> [HealthType] { Registry.types(in: [group]).filter { $0.sampleType != nil } }

    // MARK: Pairing

    /// The QR payload is `{"url":"<public base URL>","code":"XXXX-XXXX"}`.
    static func parsePairingPayload(_ payload: String) -> (url: String, code: String)? {
        struct Payload: Decodable { let url, code: String }
        guard let p = try? JSONDecoder().decode(Payload.self, from: Data(payload.utf8)) else { return nil }
        return (p.url, p.code)
    }

    func pair(url: String, code: String) async {
        let trimmed = url.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let base = URL(string: trimmed), base.scheme == "https", base.host() != nil else {
            message = "Enter the server address as an https:// URL."
            return
        }
        let code = code.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !code.isEmpty else {
            message = "Enter the pairing code."
            return
        }
        isBusy = true
        defer { isBusy = false }
        do {
            let client = IngestClient(transport: services.transport)
            let paired = try await client.pair(baseURL: base, code: code, deviceName: UIDevice.current.name)
            try services.tokens.save(paired)
            credentials = paired
            needsRepair = false
            message = nil
            await start()
        } catch BridgeError.rejected, BridgeError.unauthorized {
            message = "The server did not accept this code. Codes work once and expire after 10 minutes."
        } catch {
            message = "Could not pair: \(Self.describe(error))"
        }
    }

    /// Forgets this device locally. Revoke it on the server too (Settings, Devices).
    func unpair() {
        gate.set([])
        services.disableBackgroundDelivery()
        try? services.tokens.delete()
        for type in Registry.v1 { anchors.reset(type.id) }
        credentials = nil
        sync = nil
        observed = []
        enabled = []
        status = [:]
        services.defaults.removeObject(forKey: "lastAppliedReset")
        needsRepair = false
        message = nil
        persist()
    }

    // MARK: Sync

    /// Idempotent. Builds the engine, starts observers for enabled types and syncs them.
    func start() async {
        guard let credentials else { return }
        if sync == nil {
            let client = IngestClient(transport: services.transport)
            let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0"
            let sender = BatchSender(credentials: credentials, anchors: anchors, client: client, clientVersion: version)
            sync = HealthSync(store: GatedStore(base: services.store, gate: gate), sender: sender, anchors: anchors)
        }
        await applyServerResets()
        await observe(enabledTypes)
        await run(enabledTypes)
    }

    /// Anchor resets the owner requested on the server, newer than the last one applied here.
    private func applyServerResets() async {
        guard let credentials, let sync else { return }
        let defaults = services.defaults
        do {
            let me = try await IngestClient(transport: services.transport).deviceSelf(credentials: credentials)
            let applied = defaults.object(forKey: "lastAppliedReset") as? Date ?? .distantPast
            let fresh = me.anchorResets.filter { $0.requestedAt > applied }
            for reset in fresh {
                for type in Registry.v1 where reset.type == "*" || reset.type == type.id { await sync.resetAnchor(type) }
            }
            if let newest = fresh.map(\.requestedAt).max() { defaults.set(newest, forKey: "lastAppliedReset") }
        } catch BridgeError.unauthorized {
            needsRepair = true
        } catch {}
    }

    func syncNow() async {
        guard !isSyncing else { return }
        isSyncing = true
        defer { isSyncing = false }
        await start()
    }

    /// Runs from `BGAppRefreshTask`.
    func backgroundRefresh() async {
        await syncNow()
        scheduleRefresh()
    }

    func scheduleRefresh() {
        guard isPaired else { return }
        let request = BGAppRefreshTaskRequest(identifier: Self.refreshTaskID)
        request.earliestBeginDate = Date(timeIntervalSinceNow: 3600)
        try? BGTaskScheduler.shared.submit(request)
    }

    // MARK: Metric groups

    /// Enabling requests read authorization and gives the group's types a new anchor, so they are
    /// pulled in full. Disabling stops syncing and keeps what the server already has.
    func setGroup(_ group: MetricGroup, enabled on: Bool) async {
        guard let sync else { return }
        if on {
            do { try await sync.requestAuthorization(for: [group]) } catch {
                message = "Authorization request failed: \(Self.describe(error))"
                return
            }
            let types = types(in: group)
            for type in types { await sync.resetAnchor(type) }
            enabled.insert(group)
            gate.set(Set(enabledTypes.map(\.id)))
            persist()
            await observe(types)
            await run(types)
        } else {
            enabled.remove(group)
            gate.set(Set(enabledTypes.map(\.id)))
            persist()
        }
    }

    /// The next sync of `type` is a full pull; the server dedupes by UUID.
    func resetAnchor(_ type: HealthType) async {
        guard let sync else { return }
        await sync.resetAnchor(type)
        await run([type])
    }

    func resetAllAnchors() async {
        guard let sync else { return }
        for type in enabledTypes { await sync.resetAnchor(type) }
        await run(enabledTypes)
    }

    // MARK: Internals

    private func observe(_ types: [HealthType]) async {
        guard let sync else { return }
        let fresh = types.filter { observed.insert($0.id).inserted }
        do { try await sync.observe(fresh) } catch {
            message = "Background delivery could not be enabled: \(Self.describe(error))"
        }
    }

    private func run(_ types: [HealthType]) async {
        guard let sync, !types.isEmpty else { return }
        let failures = await sync.syncAll(types)
        let now = Date()
        for type in types {
            if let error = failures[type.id] {
                status[type.id, default: TypeStatus()].lastError = Self.describe(error)
                if case BridgeError.unauthorized = error { needsRepair = true }
            } else {
                status[type.id] = TypeStatus(lastSync: now, lastError: nil)
            }
        }
        persist()
        sendHeartbeat(failures: Array(failures.values))
    }

    /// Best effort: tells the server which anchors this phone holds, so it can list types and flag silent ones.
    private func sendHeartbeat(failures: [any Error]) {
        guard let credentials else { return }
        let hashes = Dictionary(uniqueKeysWithValues: enabledTypes.map { ($0.id, AnchorStore.hash(anchors.anchor(for: $0.id))) })
        let lastSuccess = status.values.compactMap(\.lastSync).max()
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0"
        let client = IngestClient(transport: services.transport)
        let errorClass = failures.first.map(Self.errorClass)
        let count = failures.count
        Task {
            try? await client.heartbeat(credentials: credentials, clientVersion: version, anchorHashes: hashes,
                                        lastSuccessAt: lastSuccess, failedUnits: count, errorClass: errorClass)
        }
    }

    static func errorClass(_ error: any Error) -> String {
        switch error {
        case BridgeError.unauthorized: "reauth_required"
        case BridgeError.rejected: "permanent"
        case is URLError: "network"
        default: "transient"
        }
    }

    private func persist() {
        services.defaults.set(enabled.map(\.rawValue).sorted(), forKey: "enabledGroups")
        services.defaults.set(try? JSONEncoder().encode(status), forKey: "typeStatus")
    }

    static func describe(_ error: any Error) -> String {
        switch error {
        case BridgeError.unauthorized: "The server rejected this device. Unpair and pair again."
        case BridgeError.rejected(let status): "The server refused the upload (HTTP \(status))."
        case BridgeError.badResponse: "The server sent an unexpected response."
        case let e as URLError: "Network error: \(e.localizedDescription)"
        default: error.localizedDescription
        }
    }
}

extension MetricGroup {
    var title: String { rawValue.capitalized }
}

extension HealthType {
    /// "HKQuantityTypeIdentifierHeartRateVariabilitySDNN" -> "Heart Rate Variability SDNN"
    var displayName: String {
        var name = id
        for prefix in ["HKQuantityTypeIdentifier", "HKCategoryTypeIdentifier", "HKCorrelationTypeIdentifier", "HKWorkoutType"] where name.hasPrefix(prefix) {
            name.removeFirst(prefix.count)
            break
        }
        if name.isEmpty { return "Workouts" }
        var out = ""
        for (i, ch) in name.enumerated() {
            let prev = i > 0 ? name[name.index(name.startIndex, offsetBy: i - 1)] : nil
            if let prev, ch.isUppercase, prev.isLowercase { out.append(" ") }
            out.append(ch)
        }
        return out
    }
}
