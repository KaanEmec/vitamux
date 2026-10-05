import Foundation
import HealthBridgeCore
import HealthKit
import VitamuxKit

/// This iPhone as an Apple Health source: the device token and anchors HealthBridgeKit keeps, in
/// Bridge's Keychain service and defaults, so an installed Bridge upgrades in place. The device
/// token is not the app session: signing out leaves sync running unless the owner unpairs too.
/// J22.14 adds the sync itself.
struct ThisDevice {
    let tokens: TokenStore
    let defaults: UserDefaults
    /// Whether unpairing also turns off HealthKit background delivery (not in UI tests).
    let stopsHealthKit: Bool

    /// The pairing, or nil when this iPhone is not paired.
    var credentials: Credentials? {
        (try? tokens.load()) ?? nil
    }

    /// Revokes the device token on the server it was paired with (when that is the signed-in
    /// server) and forgets the pairing here.
    func unpair(using client: Client, on profile: ServerProfile) async throws {
        guard let credentials else { return }
        if (try? ServerProfile(credentials.baseURL.absoluteString))?.baseURL == profile.baseURL {
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
        if stopsHealthKit, HKHealthStore.isHealthDataAvailable() {
            HKHealthStore().disableAllBackgroundDelivery { _, _ in }
        }
        try? tokens.delete()
        for key in defaults.dictionaryRepresentation().keys where key.hasPrefix("anchor.") {
            defaults.removeObject(forKey: key)
        }
        for key in ["enabledGroups", "typeStatus", "lastAppliedReset"] {
            defaults.removeObject(forKey: key)
        }
    }
}

#if DEBUG
extension ThisDevice {
    /// The stores Bridge's `-uitest` build uses, so the upgrade check reads what Bridge wrote.
    static func uiTest(paired: Bool, keep: Bool) -> ThisDevice {
        let device = ThisDevice(
            tokens: TokenStore(service: "org.vitamux.healthbridge.uitest"),
            defaults: UserDefaults(suiteName: "org.vitamux.healthbridge.uitest")!,
            stopsHealthKit: false
        )
        if !keep { device.forget() }
        if paired {
            try? device.tokens.save(Credentials(
                baseURL: FakeServer.uiTest.profile.baseURL, deviceID: "00000000-0000-4000-8000-000000000020",
                connectionID: "conn_" + String(repeating: "0", count: 31) + "1", token: "synthetic-device-token"
            ))
        }
        return device
    }
}
#endif
