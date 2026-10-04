import CryptoKit
import Foundation
import Security

/// Per-type HealthKit anchors (opaque archived `HKQueryAnchor` data). Anchors are not secret.
public final class AnchorStore: @unchecked Sendable {
    let defaults: UserDefaults

    public init(defaults: UserDefaults = .standard) { self.defaults = defaults }

    public func anchor(for type: String) -> Data? { defaults.data(forKey: "anchor." + type) }
    public func set(_ anchor: Data?, for type: String) { defaults.set(anchor, forKey: "anchor." + type) }
    /// Next sync of `type` is a full pull; the server dedupes re-sent samples by UUID.
    public func reset(_ type: String) { defaults.removeObject(forKey: "anchor." + type) }

    /// Short stable hash of an anchor for the payload, `none` before the first pull.
    public static func hash(_ anchor: Data?) -> String {
        anchor.map { hex(SHA256.hash(data: $0).prefix(8)) } ?? "none"
    }
}

/// Keeps `Credentials` in the Keychain only, readable after first unlock so background uploads work.
public struct TokenStore: Sendable {
    let service: String

    public init(service: String = "org.vitamux.healthbridge") { self.service = service }

    public func load() throws -> Credentials? {
        var query = base
        query[kSecReturnData] = true
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = item as? Data else { throw KeychainError(status: status) }
        return try JSONDecoder().decode(Credentials.self, from: data)
    }

    public func save(_ credentials: Credentials) throws {
        try delete()
        var item = base
        item[kSecValueData] = try JSONEncoder().encode(credentials)
        item[kSecAttrAccessible] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(item as CFDictionary, nil)
        guard status == errSecSuccess else { throw KeychainError(status: status) }
    }

    public func delete() throws {
        let status = SecItemDelete(base as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else { throw KeychainError(status: status) }
    }

    var base: [CFString: Any] {
        [kSecClass: kSecClassGenericPassword, kSecAttrService: service, kSecAttrAccount: "device"]
    }
}

public struct KeychainError: Error, Equatable {
    public let status: OSStatus
}
