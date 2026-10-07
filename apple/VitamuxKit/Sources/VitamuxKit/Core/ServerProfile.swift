import Foundation

/// The Vitamux server the owner typed or scanned: a base URL, `https` only. A debug build also
/// accepts `http` for `localhost`, `*.local` and private addresses, matching
/// `VITAMUX_ENV=development` (docs/architecture/ios-app.md#server-and-sign-in).
public struct ServerProfile: Codable, Hashable, Sendable {
    /// Scheme, host, optional port and path prefix; no trailing slash, query or credentials.
    public let baseURL: URL

    /// Whether this build takes `http` for local addresses: debug builds only.
    public static var allowsLocalHTTPByDefault: Bool {
        #if DEBUG
        true
        #else
        false
        #endif
    }

    /// Validates what the owner typed. A bare host (`vitamux.example.org`) means `https`.
    public init(_ text: String, allowsLocalHTTP: Bool = allowsLocalHTTPByDefault) throws(Problem) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { throw Self.invalid("Enter the address of your Vitamux server.") }
        let withScheme = trimmed.contains("://") ? trimmed : "https://" + trimmed
        guard var parts = URLComponents(string: withScheme),
              let scheme = parts.scheme?.lowercased(),
              let host = parts.host?.lowercased(), !host.isEmpty
        else { throw Self.invalid("This is not a web address.") }
        guard parts.user == nil, parts.password == nil else {
            throw Self.invalid("Leave the user name and password out of the address.")
        }
        guard parts.query == nil, parts.fragment == nil else {
            throw Self.invalid("Use only the server address, without ? or #.")
        }
        guard scheme == "https" || (scheme == "http" && allowsLocalHTTP && Self.isLocal(host)) else {
            throw Self.invalid("The address must start with https://.")
        }
        parts.scheme = scheme
        parts.host = host
        while parts.path.hasSuffix("/") { parts.path.removeLast() }
        guard let url = parts.url else { throw Self.invalid("This is not a web address.") }
        baseURL = url
    }

    /// `localhost`, `*.local`, loopback, private and link-local addresses (IPv4 and IPv6).
    static func isLocal(_ host: String) -> Bool {
        if host == "localhost" || host.hasSuffix(".localhost") || host.hasSuffix(".local") { return true }
        let octets = host.split(separator: ".", omittingEmptySubsequences: false).compactMap { UInt8($0) }
        if octets.count == 4 {
            switch (octets[0], octets[1]) {
            case (127, _), (10, _), (192, 168), (169, 254): return true
            case (172, 16...31): return true
            default: return false
            }
        }
        let v6 = host.trimmingCharacters(in: CharacterSet(charactersIn: "[]"))
        guard v6.contains(":") else { return false }
        return v6 == "::1" || v6.hasPrefix("fc") || v6.hasPrefix("fd") || v6.hasPrefix("fe80:")
    }

    private static func invalid(_ detail: String) -> Problem {
        Problem(title: "Check the server address", detail: detail)
    }
}
