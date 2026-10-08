#if DEBUG
import Foundation
import Synchronization

// Apple Health pairing and this iPhone's device token (J22.14) for the fake server, mirroring
// internal/api's device handlers and the ingest routes (docs/architecture/apple-health.md#pairing-and-security):
// pairing codes, the code exchange, the device list with its possibly-denied hints, anchor-reset
// requests, revocation, and the device-token routes HealthBridgeKit calls (devices/self, batches,
// heartbeat). The ingest routes take the device token, not an app session, so the fixture router
// asks this file before its session check. Every value is synthetic.
//
// This file owns every /devices route, Settings › Devices included (it lists an older, idle
// iPhone beside the paired one).
//
// Scenario: one iPhone is already paired (`AppleHealth.deviceID`, the device `-uitest-paired`
// starts with) and the server lists `AppleHealth.possiblyDenied` as possibly denied for it.
extension FakeServer {
    /// The synthetic device and codes the app's `-uitest` build and UI tests share.
    public enum AppleHealth {
        public static let deviceID = "00000000-0000-4000-8000-000000000020"
        public static let connectionID = "conn_" + String(repeating: "0", count: 31) + "1"
        public static let deviceToken = "synthetic-device-token"
        public static let deviceName = "Synthetic iPhone"
        /// A code the owner made in the panel; it pairs any number of times (manual pairing).
        public static let manualCode = "SYNT-H234"
        /// The type `GET /devices` lists as possibly denied.
        public static let possiblyDenied = "HKQuantityTypeIdentifierRestingHeartRate"
    }

    /// Revokes a device, as the owner does in the panel: its next request answers 401.
    public func revokeDevice(_ id: String) {
        AppleHealthFixture.update(profile.baseURL.host() ?? "") { $0.devices[id]?.revokedAt = Date() }
    }

    /// Asks a device to pull `types` again (every type when empty), as Settings › Devices does.
    public func requestAnchorReset(deviceID: String, types: [String] = []) {
        AppleHealthFixture.update(profile.baseURL.host() ?? "") { $0.devices[deviceID]?.request(types, at: Date()) }
    }

    /// Batches the fake accepted from devices.
    public var acceptedBatches: Int {
        AppleHealthFixture.update(profile.baseURL.host() ?? "") { $0.batches }
    }

    /// Answers an Apple Health endpoint, or nil for a request this file does not handle. App-session
    /// routes answer only with a live session, so without one the router's 401 answers.
    static func appleHealth(method: String, url: URL, token: String?, signedIn: Bool, body: Data) -> Reply? {
        let parts = url.path.split(separator: "/").map(String.init)
        let host = url.host() ?? ""
        return AppleHealthFixture.update(host) { state in
            switch (method, url.path) {
            case ("POST", "/api/ingest/v1/devices/pair"):
                return state.pair(body)
            case ("GET", "/api/ingest/v1/devices/self"):
                return state.withDevice(token) { device in
                    // The source filter (J22.25): FakeServer+SourceFilter.swift.
                    let filter = SourceFilterFixture.ingestBody(host, deviceID: device.id)
                    return Reply.json(200, device.selfBody.merging(["source_filter": filter]) { $1 })
                }
            case ("PUT", "/api/ingest/v1/devices/self/sources"):
                return state.withDevice(token) { SourceFilterFixture.report(host, deviceID: $0.id, body: body) }
            case ("POST", "/api/ingest/v1/batches"):
                let reply = state.withDevice(token) { device in
                    device.lastSyncAt = Date()
                    return Reply(status: 202)
                }
                if reply.status == 202 { state.batches += 1 }
                return reply
            case ("POST", "/api/ingest/v1/heartbeat"):
                return state.withDevice(token) { device in
                    device.types = AppleHealthFixture.checkpointTypes(body)
                    return Reply(status: 204)
                }
            default:
                break
            }
            guard signedIn, parts.count >= 3, parts[0] == "api", parts[1] == "v1", parts[2] == "devices" else { return nil }
            switch (method, parts.count) {
            case ("GET", 3):
                let devices = state.devices.values.sorted { $0.createdAt > $1.createdAt }
                return Reply.json(200, ["devices": devices.map(\.listBody)])
            case ("POST", 4) where parts[3] == "pairing-codes":
                return state.createCode()
            case ("POST", 5) where parts[4] == "request-anchor-reset":
                guard state.devices[parts[3]] != nil else { return Reply.problem(404, "not_found", "device not found") }
                let input = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any]
                state.devices[parts[3]]?.request(input?["types"] as? [String] ?? [], at: Date())
                return Reply(status: 204)
            case ("GET", 5) where parts[4] == "source-filter":
                guard state.devices[parts[3]] != nil else { return Reply.problem(404, "not_found", "device not found") }
                return SourceFilterFixture.view(host, deviceID: parts[3])
            case ("PUT", 5) where parts[4] == "source-filter":
                guard state.devices[parts[3]] != nil else { return Reply.problem(404, "not_found", "device not found") }
                let (reply, pulled) = SourceFilterFixture.replace(host, deviceID: parts[3], body: body)
                if !pulled.isEmpty { state.devices[parts[3]]?.request(pulled.contains("*") ? [] : pulled, at: Date()) }
                return reply
            case ("POST", 5) where parts[4] == "revoke":
                guard let device = state.devices[parts[3]] else { return Reply.problem(404, "not_found", "device not found") }
                state.devices[parts[3]]?.revokedAt = device.revokedAt ?? Date()
                return Reply(status: 204)
            default:
                return nil
            }
        }
    }
}

/// Paired devices and issued codes, kept per fake host so parallel unit tests never share them.
struct AppleHealthFixture {
    static let states = Mutex<[String: State]>([:])
    /// Crockford base32, as the server's codes.
    static let alphabet = Array("0123456789ABCDEFGHJKMNPQRSTVWXYZ")

    struct Device {
        var id: String
        var name: String
        var token: String
        var createdAt: Date
        var lastSeenAt: Date?
        var lastSyncAt: Date?
        var revokedAt: Date?
        var types: [String] = []
        var possiblyDenied: [String] = []
        /// The latest request per type (`*` for every type).
        var resets: [String: Date] = [:]

        mutating func request(_ types: [String], at date: Date) {
            for type in types.isEmpty ? ["*"] : types { resets[type] = date }
        }

        var resetsBody: [[String: Any]] {
            resets.sorted { $0.key < $1.key }.map { ["type": $0.key, "requested_at": AppleHealthFixture.iso($0.value)] }
        }

        var selfBody: [String: Any] {
            ["device_id": id, "connection_id": FakeServer.AppleHealth.connectionID, "name": name, "anchor_resets": resetsBody]
        }

        var listBody: [String: Any] {
            let optional = { (date: Date?) -> Any in date.map(AppleHealthFixture.iso) ?? NSNull() }
            return [
                "id": id, "name": name, "connection_id": FakeServer.AppleHealth.connectionID,
                "created_at": AppleHealthFixture.iso(createdAt), "last_seen_at": optional(lastSeenAt),
                "last_sync_at": optional(lastSyncAt), "revoked_at": optional(revokedAt), "types": types,
                "possibly_denied": revokedAt == nil ? possiblyDenied : [], "anchor_resets": resetsBody,
            ]
        }
    }

    struct State {
        var devices: [String: Device]
        var codes: Set<String> = []
        var batches = 0
        var nextDevice = 0x100

        init() {
            let paired = Device(
                id: FakeServer.AppleHealth.deviceID, name: FakeServer.AppleHealth.deviceName,
                token: FakeServer.AppleHealth.deviceToken, createdAt: Date(timeIntervalSince1970: 1_767_254_400),
                possiblyDenied: [FakeServer.AppleHealth.possiblyDenied]
            )
            // Settings › Devices lists an older, idle iPhone beside this one (it is the one to revoke).
            let old = Device(
                id: "00000000-0000-4000-8000-000000000021", name: "Synthetic old iPhone", token: "synthetic-old-device-token",
                createdAt: Date(timeIntervalSince1970: 1_748_764_800), lastSeenAt: Date(timeIntervalSince1970: 1_764_576_000),
                lastSyncAt: Date(timeIntervalSince1970: 1_764_576_000), types: ["HKQuantityTypeIdentifierHeartRate"]
            )
            devices = [paired.id: paired, old.id: old]
        }

        /// The device a live token belongs to; 401 for a missing, unknown or revoked one.
        mutating func withDevice(_ token: String?, _ answer: (inout Device) -> Reply) -> Reply {
            guard let token, let id = devices.first(where: { $0.value.token == token })?.key, devices[id]?.revokedAt == nil else {
                return Reply.problem(401, "unauthenticated", "invalid, revoked or expired token")
            }
            devices[id]?.lastSeenAt = Date()
            return answer(&devices[id]!)
        }

        /// `POST /devices/pairing-codes` for an app session: the code without `url` or `qr_payload`.
        /// Codes start with SYN so screens and tests can tell them from the manual code.
        mutating func createCode() -> Reply {
            let symbols = "SYN" + (0..<5).map { _ in String(AppleHealthFixture.alphabet.randomElement()!) }.joined()
            let code = symbols.prefix(4) + "-" + symbols.suffix(4)
            codes.insert(String(symbols))
            return Reply.json(201, ["code": code, "expires_at": AppleHealthFixture.iso(Date().addingTimeInterval(600))])
        }

        /// `POST /api/ingest/v1/devices/pair`: an issued code works once, the manual code always.
        mutating func pair(_ body: Data) -> Reply {
            guard let input = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any],
                  let code = input["code"] as? String, let name = input["name"] as? String, !name.isEmpty
            else { return Reply.problem(422, "validation_failed", "code and name are required") }
            let normalized = code.uppercased().filter { $0 != "-" && $0 != " " }
            let manual = FakeServer.AppleHealth.manualCode.filter { $0 != "-" }
            guard normalized == manual || codes.remove(normalized) != nil else {
                return Reply.problem(422, "validation_failed", "invalid or expired pairing code")
            }
            let id = String(format: "00000000-0000-4000-8000-%012x", nextDevice)
            nextDevice += 1
            let token = "vmx_cli_" + String(format: "%032x", nextDevice) + "_" + String(repeating: "s", count: 43)
            devices[id] = Device(id: id, name: name, token: token, createdAt: Date())
            return Reply.json(201, ["device_id": id, "connection_id": FakeServer.AppleHealth.connectionID, "token": token])
        }
    }

    static func update<T>(_ host: String, _ body: (inout State) -> T) -> T {
        states.withLock { states in
            var state = states[host] ?? State()
            defer { states[host] = state }
            return body(&state)
        }
    }

    /// The types a heartbeat's `healthkit.samples.v1` checkpoint lists anchors for.
    static func checkpointTypes(_ body: Data) -> [String] {
        let input = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any]
        let streams = input?["streams"] as? [[String: Any]] ?? []
        let anchors = streams.compactMap { ($0["checkpoint"] as? [String: Any])?["anchors"] as? [String: Any] }
        return anchors.flatMap(\.keys).sorted()
    }

    static func iso(_ date: Date) -> String { ExploreFixture.iso(date) }
}
#endif
