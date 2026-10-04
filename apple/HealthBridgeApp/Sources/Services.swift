import Foundation
import HealthBridgeCore
import HealthBridgeHealthKit
import HealthKit

/// Everything the model talks to outside the app. `live` uses HealthKit and the network;
/// `fake` (launch argument `-uitest`) uses neither, so UI tests need no prompts and no server.
struct Services {
    var store: HealthStore
    var transport: Transport
    var tokens: TokenStore
    var defaults: UserDefaults
    var disableBackgroundDelivery: @Sendable () -> Void

    static func live() -> Services {
        let hk = HKHealthStore()
        return Services(store: hk, transport: IngestClient.urlSession, tokens: TokenStore(), defaults: .standard,
                        disableBackgroundDelivery: { hk.disableAllBackgroundDelivery { _, _ in } })
    }

    /// Fake store and transport. `paired` starts with a stored token, as after a successful pairing.
    static func fake(paired: Bool) -> Services {
        let defaults = UserDefaults(suiteName: "org.vitamux.healthbridge.uitest")!
        defaults.removePersistentDomain(forName: "org.vitamux.healthbridge.uitest")
        let tokens = TokenStore(service: "org.vitamux.healthbridge.uitest")
        try? tokens.delete()
        if paired {
            try? tokens.save(Credentials(baseURL: URL(string: "https://vitamux.example.test")!, deviceID: "device-synthetic",
                                         connectionID: "conn-synthetic", token: "synthetic-token-not-secret"))
        }
        let transport: Transport = { request in
            let path = request.url!.path
            let (status, body) = path.hasSuffix("/devices/pair")
                ? (201, #"{"device_id":"device-synthetic","connection_id":"conn-synthetic","token":"synthetic-token-not-secret"}"#)
                : path.hasSuffix("/devices/self")
                ? (200, #"{"device_id":"device-synthetic","connection_id":"conn-synthetic","name":"Synthetic iPhone","anchor_resets":[]}"#)
                : path.hasSuffix("/heartbeat") ? (204, "") : (202, "")
            return (Data(body.utf8), HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: nil)!)
        }
        return Services(store: FakeHealthStore(), transport: transport, tokens: tokens, defaults: defaults, disableBackgroundDelivery: {})
    }
}

/// Serves empty pages and accepts every authorization request.
struct FakeHealthStore: HealthStore {
    func requestReadAuthorization(_ types: Set<HKObjectType>) async throws {}
    func earliestPermittedSampleDate() -> Date { .distantPast }
    func anchoredPage(of type: HKSampleType, from start: Date, anchor: Data?, limit: Int) async throws -> AnchoredPage {
        AnchoredPage(samples: [], deleted: [], anchor: anchor)
    }
    func observe(_ type: HKSampleType, onUpdate: @escaping @Sendable (@escaping @Sendable () -> Void) -> Void) {}
    func enableBackgroundDelivery(for type: HKSampleType) async throws {}
}

/// The types that may currently sync. HealthKit observer queries cannot be removed through `HealthStore`,
/// so a disabled type or an unpaired device must make its observer callbacks do nothing.
final class Gate: @unchecked Sendable {
    private let lock = NSLock()
    private var ids: Set<String> = []
    func set(_ ids: Set<String>) { lock.withLock { self.ids = ids } }
    func allows(_ id: String) -> Bool { lock.withLock { ids.contains(id) } }
}

/// Wraps a store so observer callbacks of disabled types complete at once without syncing.
struct GatedStore: HealthStore {
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
