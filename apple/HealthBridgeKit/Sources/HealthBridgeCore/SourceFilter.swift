import Foundation

/// Which apps' Apple Health data this device takes (J22.25, `source_filter` of
/// `GET /api/ingest/v1/devices/self`). The rules match the server's `internal/sourcefilter`
/// exactly: an explicit choice for the bundle id, then for its parent app (a Watch extension);
/// Apple's own sources (`com.apple.*`) are taken by default; a `default_ignore` pattern matching
/// the bundle id or its parent ignores it; anything else, including an app never seen before, is taken.
public struct SourceFilter: Codable, Equatable, Sendable {
    public enum Mode: String, Codable, Sendable {
        case take, ignore
        /// Only `Choice.types` are taken.
        case perType = "per_type"
    }

    /// Why a default is what it is.
    public enum Reason: String, Codable, Sendable {
        /// Apple's own source: always taken by default.
        case native
        /// The provider it relays is connected directly, so its copy would count twice.
        case directConnection = "direct_connection"
    }

    /// The owner's explicit choice for one app.
    public struct Choice: Codable, Equatable, Sendable {
        public var bundleID: String
        public var name: String?
        public var mode: Mode
        /// `perType`: the HealthKit type identifiers taken.
        public var types: [String]?

        public init(bundleID: String, name: String? = nil, mode: Mode, types: [String]? = nil) {
            self.bundleID = bundleID
            self.name = name
            self.mode = mode
            self.types = types
        }

        enum CodingKeys: String, CodingKey { case bundleID = "bundle_id", name, mode, types }
    }

    /// Origins ignored by default because the provider they relay is connected directly.
    public struct Default: Codable, Equatable, Sendable {
        /// A SQL LIKE pattern over bundle ids: `%` any run, `_` one character, backslash escapes.
        public var originPattern: String
        public var provider: String
        public var providerName: String

        public init(originPattern: String, provider: String, providerName: String) {
            self.originPattern = originPattern
            self.provider = provider
            self.providerName = providerName
        }

        enum CodingKeys: String, CodingKey { case originPattern = "origin_pattern", provider, providerName = "provider_name" }
    }

    public var version: Int
    public var origins: [Choice]
    public var defaultIgnore: [Default]

    public init(version: Int, origins: [Choice] = [], defaultIgnore: [Default] = []) {
        self.version = version
        self.origins = origins
        self.defaultIgnore = defaultIgnore
    }

    enum CodingKeys: String, CodingKey { case version, origins, defaultIgnore = "default_ignore" }

    /// What the filter does with the app `bundleID`.
    public func decision(for bundleID: String) -> SourceDecision {
        var decision = defaultDecision(for: bundleID)
        for id in Self.candidates(bundleID) {
            if let choice = origins.first(where: { $0.bundleID == id }) {
                decision.mode = choice.mode
                decision.types = choice.mode == .perType ? choice.types ?? [] : []
                decision.explicit = true
                return decision
            }
        }
        decision.mode = decision.defaultMode
        return decision
    }

    /// Whether data of the HealthKit type `type` written by `bundleID` is taken.
    public func takes(_ bundleID: String, type: String) -> Bool {
        decision(for: bundleID).takes(type)
    }

    func defaultDecision(for bundleID: String) -> SourceDecision {
        if Self.isNative(bundleID) { return SourceDecision(defaultMode: .take, reason: .native) }
        for id in Self.candidates(bundleID) {
            if let match = defaultIgnore.first(where: { Self.like($0.originPattern, id) }) {
                return SourceDecision(defaultMode: .ignore, reason: .directConnection, provider: match.provider,
                                      providerName: match.providerName)
            }
        }
        return SourceDecision(defaultMode: .take)
    }

    static func candidates(_ bundleID: String) -> [String] {
        if let parent = parent(of: bundleID) { return [bundleID, parent] }
        return [bundleID]
    }

    /// Bundle id suffixes of Watch apps and extensions, longest first.
    static let watchSuffixes = [".watchkitapp.watchkitextension", ".watchkitextension", ".watchkitapp", ".watchextension", ".watchapp"]

    /// The parent app's bundle id of a Watch-side extension, or nil for any other id. HealthKit
    /// reports no parent, so this follows Apple's naming convention for embedded Watch apps.
    public static func parent(of bundleID: String) -> String? {
        for suffix in watchSuffixes where bundleID.hasSuffix(suffix) && bundleID.count > suffix.count {
            return String(bundleID.dropLast(suffix.count))
        }
        return nil
    }

    /// Apple's own sources: the per-device `com.apple.health.<UUID>` sources and Apple's apps.
    public static func isNative(_ bundleID: String) -> Bool {
        bundleID.hasPrefix("com.apple.")
    }

    /// Matches `string` against a SQL LIKE pattern: `%` any run, `_` any one character, backslash escapes.
    public static func like(_ pattern: String, _ string: String) -> Bool {
        let p = Array(pattern.unicodeScalars), s = Array(string.unicodeScalars)
        func match(_ start: Int, _ from: Int) -> Bool {
            var i = start, j = from
            while i < p.count {
                switch p[i] {
                case "%":
                    while i < p.count, p[i] == "%" { i += 1 }
                    if i == p.count { return true }
                    return (j...s.count).contains { match(i, $0) }
                case "_":
                    guard j < s.count else { return false }
                default:
                    if p[i] == "\\", i + 1 < p.count { i += 1 }
                    guard j < s.count, s[j] == p[i] else { return false }
                }
                i += 1
                j += 1
            }
            return j == s.count
        }
        return match(0, 0)
    }
}

/// What a filter does with one app, and its default with the reason.
public struct SourceDecision: Equatable, Sendable {
    public var mode: SourceFilter.Mode
    /// `perType`: the types taken.
    public var types: [String]
    /// The owner chose it; defaults never change it.
    public var explicit: Bool
    public var defaultMode: SourceFilter.Mode
    public var reason: SourceFilter.Reason?
    /// `directConnection`: the provider connected directly.
    public var provider: String?
    public var providerName: String?

    public init(mode: SourceFilter.Mode? = nil, types: [String] = [], explicit: Bool = false, defaultMode: SourceFilter.Mode,
                reason: SourceFilter.Reason? = nil, provider: String? = nil, providerName: String? = nil) {
        self.mode = mode ?? defaultMode
        self.types = types
        self.explicit = explicit
        self.defaultMode = defaultMode
        self.reason = reason
        self.provider = provider
        self.providerName = providerName
    }

    public func takes(_ type: String) -> Bool {
        switch mode {
        case .take: true
        case .ignore: false
        case .perType: types.contains(type)
        }
    }
}

/// One app the device found in Apple Health (`HKSourceQuery`), with the types it wrote and the end
/// of each type's newest sample. `PUT /api/ingest/v1/devices/self/sources`; no health values.
public struct DiscoveredSource: Encodable, Equatable, Sendable {
    public struct TypeSeen: Encodable, Equatable, Sendable {
        /// The HealthKit type identifier.
        public var type: String
        public var lastSampleAt: Date?

        public init(type: String, lastSampleAt: Date? = nil) {
            self.type = type
            self.lastSampleAt = lastSampleAt
        }

        enum CodingKeys: String, CodingKey { case type, lastSampleAt = "last_sample_at" }

        public func encode(to encoder: Encoder) throws {
            var c = encoder.container(keyedBy: CodingKeys.self)
            try c.encode(type, forKey: .type)
            try c.encodeIfPresent(lastSampleAt.map { rfc3339($0, in: TimeZone(identifier: "UTC")!) }, forKey: .lastSampleAt)
        }
    }

    public var bundleID: String
    public var name: String?
    public var types: [TypeSeen]

    public init(bundleID: String, name: String?, types: [TypeSeen]) {
        self.bundleID = bundleID
        self.name = name
        self.types = types
    }

    enum CodingKeys: String, CodingKey { case bundleID = "bundle_id", name, types }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(bundleID, forKey: .bundleID)
        try c.encodeIfPresent(name.flatMap { $0.isEmpty ? nil : String($0.prefix(200)) }, forKey: .name)
        try c.encode(types, forKey: .types)
    }
}
