#if DEBUG
import Foundation
import Synchronization

// The Apple Health source filter (J22.25) for the fake server, mirroring internal/sourcefilter and
// the device handlers: GET and PUT /devices/{id}/source-filter for an app session, the filter in
// GET /api/ingest/v1/devices/self, the phone's report (PUT /api/ingest/v1/devices/self/sources)
// and the records held raw that GET /inventory?include_ignored=true lists. FakeServer+AppleHealth
// routes here. Every value is synthetic.
//
// Scenario for the paired iPhone (`AppleHealth.deviceID`): a synthetic band app relays a provider
// the owner also connects directly, so it is ignored by default ("Synthetic Band Cloud"); the
// Apple Watch is native and taken; a synthetic scale app is taken. The phone has reported all three.
extension FakeServer {
    /// The synthetic apps of the scenario, shared with the app's `-uitest` HealthStore.
    public enum SourceFilterScenario {
        public static let band = "com.example.synthetic.band"
        public static let bandName = "Synthetic Band"
        public static let bandProvider = "synthetic_band"
        public static let bandProviderName = "Synthetic Band Cloud"
        public static let watch = "com.apple.health.00000000-0000-4000-8000-0000000000aa"
        public static let watchName = "Apple Watch"
        public static let scale = "com.example.synthetic.scale"
        public static let scaleName = "Synthetic Scale"
        /// Records of the band held raw while it is ignored.
        public static let bandIgnoredRecords: Int64 = 42
    }

    /// The source filter requests the fake has accepted (PUT), for assertions.
    public var sourceFilterVersion: Int {
        SourceFilterFixture.update(profile.baseURL.host() ?? "", AppleHealth.deviceID) { $0.version }
    }
}

/// Per host and device: the explicit choices, the phone's last report and the apps seen in data.
struct SourceFilterFixture {
    typealias Scenario = FakeServer.SourceFilterScenario
    static let states = Mutex<[String: [String: Device]]>([:])

    static let heartRate = "HKQuantityTypeIdentifierHeartRate"
    static let hrv = "HKQuantityTypeIdentifierHeartRateVariabilitySDNN"
    static let steps = "HKQuantityTypeIdentifierStepCount"
    static let bodyMass = "HKQuantityTypeIdentifierBodyMass"

    /// The default-ignore patterns: the band's provider is connected directly.
    static let defaults: [[String: String]] = [[
        "origin_pattern": Scenario.band + "%", "provider": Scenario.bandProvider, "provider_name": Scenario.bandProviderName,
    ]]

    struct Choice {
        var bundleID: String
        var name: String?
        var mode: String
        var types: [String]

        var json: [String: Any] {
            var out: [String: Any] = ["bundle_id": bundleID, "mode": mode]
            if let name { out["name"] = name }
            if mode == "per_type" { out["types"] = types }
            return out
        }
    }

    struct Reported {
        var bundleID: String
        var name: String?
        var writes: [(type: String, last: Date?)]
    }

    struct Device {
        var version = 1
        var choices: [Choice] = []
        var reported: [Reported] = []
        var reportedAt: Date?
        /// Apps the server has seen data from: bundle id → (name, classification, relayed provider).
        var seen: [String: (name: String, classification: String, relayed: String?)] = [:]

        init(paired: Bool) {
            guard paired else { return }
            let now = Date()
            reported = [
                Reported(bundleID: Scenario.watch, name: Scenario.watchName,
                         writes: [(heartRate, now - 2 * 3600), (steps, now - 3600)]),
                Reported(bundleID: Scenario.band, name: Scenario.bandName, writes: [(heartRate, now - 3 * 3600), (hrv, now - 8 * 3600)]),
                Reported(bundleID: Scenario.scale, name: Scenario.scaleName, writes: [(bodyMass, now - 26 * 3600)]),
            ]
            reportedAt = now - 600
            seen = [
                Scenario.watch: (Scenario.watchName, "native", nil),
                Scenario.band: (Scenario.bandName, "relayed", Scenario.bandProvider),
                Scenario.scale: (Scenario.scaleName, "direct", nil),
            ]
        }

        /// The decision rules of internal/sourcefilter; patterns here are prefixes ending in `%`.
        func decide(_ bundle: String) -> (mode: String, types: [String], explicit: Bool, defaultMode: String, reason: String?) {
            let candidates = [bundle] + (SourceFilterFixture.parent(bundle).map { [$0] } ?? [])
            var defaultMode = "take", reason: String?
            if bundle.hasPrefix("com.apple.") {
                reason = "native"
            } else if candidates.contains(where: { id in SourceFilterFixture.defaults.contains { id.hasPrefix($0["origin_pattern"]!.dropLast()) } }) {
                defaultMode = "ignore"
                reason = "direct_connection"
            }
            for id in candidates {
                if let choice = choices.first(where: { $0.bundleID == id }) {
                    return (choice.mode, choice.mode == "per_type" ? choice.types : [], true, defaultMode, reason)
                }
            }
            return (defaultMode, [], false, defaultMode, reason)
        }

        func takes(_ bundle: String, _ type: String) -> Bool {
            let d = decide(bundle)
            return d.mode == "take" || (d.mode == "per_type" && d.types.contains(type))
        }

        func origin(_ bundle: String) -> [String: Any] {
            let d = decide(bundle)
            let report = reported.first { $0.bundleID == bundle }
            let seen = seen[bundle]
            let direct = d.reason == "direct_connection"
            let name = report?.name ?? choices.first { $0.bundleID == bundle }?.name ?? seen?.name
            let null = NSNull()
            return [
                "bundle_id": bundle, "name": name ?? null, "mode": d.mode, "types": d.types, "explicit": d.explicit,
                "default_mode": d.defaultMode, "default_reason": d.reason ?? null,
                "reason_provider": direct ? Scenario.bandProvider : null, "reason_provider_name": direct ? Scenario.bandProviderName : null,
                "origin_id": seen == nil ? null : SourceFilterFixture.originID(bundle),
                "classification": seen?.classification ?? (bundle.hasPrefix("com.apple.") ? "native" : "direct"),
                "relayed_provider": seen?.relayed ?? null,
                "writes": (report?.writes ?? []).map { write -> [String: Any] in
                    var out: [String: Any] = ["type": write.type]
                    if let last = write.last { out["last_sample_at"] = AppleHealthFixture.iso(last) }
                    return out
                },
                "ignored_records": bundle == Scenario.band && d.mode == "ignore" ? Scenario.bandIgnoredRecords : 0,
            ]
        }

        func view(_ deviceID: String) -> [String: Any] {
            var bundles = Set(reported.map(\.bundleID))
            bundles.formUnion(choices.map(\.bundleID))
            bundles.formUnion(seen.keys)
            let origins = bundles.map(origin).sorted {
                (($0["name"] as? String) ?? ($0["bundle_id"] as! String)).localizedLowercase
                    < (($1["name"] as? String) ?? ($1["bundle_id"] as! String)).localizedLowercase
            }
            return [
                "device_id": deviceID, "version": version, "origins": origins, "default_ignore": SourceFilterFixture.defaults,
                "sources_reported_at": reportedAt.map(AppleHealthFixture.iso) ?? NSNull(),
            ]
        }

        /// What `GET /api/ingest/v1/devices/self` carries.
        var ingestBody: [String: Any] {
            ["version": version, "origins": choices.map(\.json), "default_ignore": SourceFilterFixture.defaults]
        }

        /// `PUT /devices/{id}/source-filter`: replaces the explicit choices. Returns a problem, or
        /// the types to pull again (`*` for all) because an app went from ignored to taken.
        mutating func replace(_ body: Data) -> (problem: Reply?, pulled: [String]) {
            guard let input = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any],
                  let origins = input["origins"] as? [[String: Any]]
            else { return (Reply.problem(422, "validation_failed", "origins is required"), []) }
            if let expected = input["version"] as? Int, expected != version {
                return (Reply.problem(409, "conflict", "the source filter changed; reload it"), [])
            }
            var next: [Choice] = []
            for (i, origin) in origins.enumerated() {
                guard let bundle = origin["bundle_id"] as? String, !bundle.isEmpty, !next.contains(where: { $0.bundleID == bundle }),
                      let mode = origin["mode"] as? String, ["take", "ignore", "per_type"].contains(mode)
                else { return (Reply.problem(422, "validation_failed", "/origins/\(i) is invalid"), []) }
                let types = (origin["types"] as? [String] ?? []).sorted()
                if mode == "per_type", types.isEmpty {
                    return (Reply.problem(422, "validation_failed", "/origins/\(i)/types: 1 to 64 HealthKit types"), [])
                }
                next.append(Choice(bundleID: bundle, name: origin["name"] as? String, mode: mode, types: mode == "per_type" ? types : []))
            }
            let before = self
            var bundles = Set(reported.map(\.bundleID)).union(seen.keys)
            bundles.formUnion(choices.map(\.bundleID))
            bundles.formUnion(next.map(\.bundleID))
            choices = next
            version += 1
            var pulled: Set<String> = []
            for bundle in bundles {
                let writes = reported.first { $0.bundleID == bundle }?.writes.map(\.type) ?? []
                let newly = writes.filter { !before.takes(bundle, $0) && takes(bundle, $0) }
                if writes.isEmpty, before.decide(bundle).mode != "take", decide(bundle).mode == "take" { pulled.insert("*") }
                pulled.formUnion(newly)
            }
            return (nil, pulled.sorted())
        }

        /// `PUT /api/ingest/v1/devices/self/sources`: replaces the report.
        mutating func report(_ body: Data) -> Reply {
            guard let input = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any],
                  let sources = input["sources"] as? [[String: Any]], sources.count <= 500
            else { return Reply.problem(422, "validation_failed", "sources: at most 500") }
            var next: [Reported] = []
            for source in sources {
                guard let bundle = source["bundle_id"] as? String, !bundle.isEmpty, let types = source["types"] as? [[String: Any]] else {
                    return Reply.problem(422, "validation_failed", "bundle_id and types are required")
                }
                let writes = types.compactMap { type -> (String, Date?)? in
                    guard let id = type["type"] as? String else { return nil }
                    return (id, (type["last_sample_at"] as? String).flatMap { try? Date($0, strategy: .iso8601) })
                }
                next.append(Reported(bundleID: bundle, name: source["name"] as? String, writes: writes))
            }
            reported = next
            reportedAt = Date()
            return Reply(status: 204)
        }
    }

    static func parent(_ bundle: String) -> String? {
        for suffix in [".watchkitapp.watchkitextension", ".watchkitextension", ".watchkitapp", ".watchextension", ".watchapp"]
        where bundle.hasSuffix(suffix) && bundle.count > suffix.count {
            return String(bundle.dropLast(suffix.count))
        }
        return nil
    }

    /// A stable synthetic data-origin id per bundle id.
    static func originID(_ bundle: String) -> String {
        let n = bundle.unicodeScalars.reduce(0) { ($0 * 31 + Int($1.value)) % 0xFFFF_FFFF }
        return String(format: "00000000-0000-4000-8000-%012x", n)
    }

    static func update<T>(_ host: String, _ deviceID: String, _ body: (inout Device) -> T) -> T {
        states.withLock { states in
            var device = states[host]?[deviceID] ?? Device(paired: deviceID == FakeServer.AppleHealth.deviceID)
            defer { states[host, default: [:]][deviceID] = device }
            return body(&device)
        }
    }

    // MARK: Routes (called from FakeServer+AppleHealth.swift)

    static func ingestBody(_ host: String, deviceID: String) -> [String: Any] {
        update(host, deviceID) { $0.ingestBody }
    }

    static func report(_ host: String, deviceID: String, body: Data) -> Reply {
        update(host, deviceID) { $0.report(body) }
    }

    static func view(_ host: String, deviceID: String) -> Reply {
        Reply.json(200, update(host, deviceID) { $0.view(deviceID) })
    }

    /// The reply, and the anchor-reset types an ignore-to-take change asks of the device.
    static func replace(_ host: String, deviceID: String, body: Data) -> (reply: Reply, pulled: [String]) {
        update(host, deviceID) { device in
            let (problem, pulled) = device.replace(body)
            if let problem { return (problem, []) }
            return (Reply.json(200, device.view(deviceID)), pulled)
        }
    }

    /// `GET /inventory?include_ignored=true`: the records held raw because the paired iPhone's
    /// filter ignores their origin. Counts only, never values.
    static func ignoredItems(_ host: String) -> [[String: Any]] {
        update(host, FakeServer.AppleHealth.deviceID) { device in
            guard !device.takes(Scenario.band, heartRate) else { return [] }
            let now = Date()
            return [[
                "kind": "metric", "code": "heart_rate", "origin": ["key": Scenario.band, "name": Scenario.bandName],
                "records": Scenario.bandIgnoredRecords, "first_at": AppleHealthFixture.iso(now - 30 * 86400),
                "last_at": AppleHealthFixture.iso(now - 3 * 3600),
            ]]
        }
    }
}
#endif
