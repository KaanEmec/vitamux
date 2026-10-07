import Foundation
import Security
import Synchronization

/// Where the app session lives: one Keychain generic-password item, readable after first unlock on
/// this device only (background refresh and the widget work while locked), in an access group the
/// widget shares. It is separate from HealthBridgeKit's device token (account `device`), so signing
/// out never stops Apple Health sync (ADR-0023).
public struct SessionStore: Sendable {
    /// An app session from `POST /auth/login` with `client: app`, bound to the server that issued it.
    public struct Entry: Codable, Equatable, Sendable {
        public var server: URL
        public var token: String
        public var expiresAt: Date

        public init(server: URL, token: String, expiresAt: Date) {
            self.server = server
            self.token = token
            self.expiresAt = expiresAt
        }
    }

    struct Keychain: Sendable {
        var service: String
        var accessGroup: String?
    }

    // A class, so copies of an in-memory store share one value.
    final class Memory: Sendable {
        let entry: Mutex<Entry?>
        init(_ entry: Entry?) { self.entry = Mutex(entry) }
    }

    let item: Keychain?
    let memory: Memory?
    static let account = "app-session"

    /// The Keychain item. `service` stays Bridge's so an installed Bridge upgrades in place.
    public static func keychain(service: String = "org.vitamux.healthbridge", accessGroup: String? = nil) -> SessionStore {
        SessionStore(item: Keychain(service: service, accessGroup: accessGroup), memory: nil)
    }

    /// Kept in memory only: UI tests against the fake server and previews.
    public static func inMemory(_ entry: Entry? = nil) -> SessionStore {
        SessionStore(item: nil, memory: Memory(entry))
    }

    public func load() throws -> Entry? {
        if let memory { return memory.entry.withLock { $0 } }
        var query = keychainQuery
        query[kSecReturnData] = true
        query[kSecMatchLimit] = kSecMatchLimitOne
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = item as? Data else { throw KeychainError(status: status) }
        return try JSONDecoder().decode(Entry.self, from: data)
    }

    public func save(_ entry: Entry) throws {
        if let memory { return memory.entry.withLock { $0 = entry } }
        try clear()
        var item = keychainQuery
        item[kSecValueData] = try JSONEncoder().encode(entry)
        item[kSecAttrAccessible] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(item as CFDictionary, nil)
        guard status == errSecSuccess else { throw KeychainError(status: status) }
    }

    /// Keeps the token from a successful app sign-in for the server that issued it.
    public func save(_ session: Components.Schemas.AppSession, for profile: ServerProfile) throws {
        try save(Entry(server: profile.baseURL, token: session.token, expiresAt: session.expiresAt))
    }

    public func clear() throws {
        if let memory { return memory.entry.withLock { $0 = nil } }
        let status = SecItemDelete(keychainQuery as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else { throw KeychainError(status: status) }
    }

    /// The bearer token for requests to `server`, only the server that issued it. The server, not
    /// the phone's clock, decides expiry: its 401 ends the session (`SessionMiddleware`).
    func token(for server: URL) -> String? {
        guard let entry = try? load(), entry.server == server else { return nil }
        return entry.token
    }

    private var keychainQuery: [CFString: Any] {
        guard let item else { return [:] }
        var query: [CFString: Any] = [
            kSecClass: kSecClassGenericPassword,
            kSecAttrService: item.service,
            kSecAttrAccount: Self.account,
        ]
        if let accessGroup = item.accessGroup { query[kSecAttrAccessGroup] = accessGroup }
        return query
    }
}

public struct KeychainError: Error, Equatable, Sendable {
    public let status: OSStatus
}
