#if DEBUG
import Foundation
import Synchronization

// The Settings pages (J22.13) for the fake server, mirroring web/e2e/settings-fake.ts and the
// server's handlers (internal/api/auth.go, settings.go, devices.go, origins.go, exports.go,
// setup.go): timezone periods, the settings map (a merge patch; retention.raw_days merges per
// provider), origins (also the rule builder's), API keys, password, TOTP and sessions, sidecars and
// exports. Not here, one owner each: /devices and pairing codes are FakeServer+AppleHealth.swift,
// /providers (with app credentials) and /source-devices are FakeServer+Sources.swift, and GET
// /system/status is FakeServer+Dashboard.swift. Every value and secret is synthetic, and the state
// is kept per fake host so parallel unit tests never share it.
extension FakeServer {
    /// Answers a Settings endpoint, or nil for a request this file does not handle. `core` is the
    /// fake's sign-in state: TOTP and the live app sessions.
    static func settings(method: String, url: URL, body: Data, token: String, core: inout State) -> Reply? {
        let parts = url.path.split(separator: "/").map(String.init)
        guard parts.count >= 3, parts[0] == "api", parts[1] == "v1" else { return nil }
        let path = Array(parts.dropFirst(2))
        let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        let request = SettingsFixture.Request(method: method, path: path, query: query, body: body)
        return SettingsFixture.states.withLock { states in
            let host = url.host() ?? ""
            var state = states[host] ?? SettingsFixture.State()
            defer { states[host] = state }
            return state.answer(request, token: token, core: &core)
        }
    }
}

/// The synthetic Settings data and what the pages' writes change.
struct SettingsFixture {
    static let states = Mutex<[String: State]>([:])

    struct Request {
        var method: String
        var path: [String]
        var query: [URLQueryItem]
        var body: Data

        var object: [String: Any]? {
            (try? JSONSerialization.jsonObject(with: body)) as? [String: Any]
        }

        func value(_ name: String) -> String? {
            query.first { $0.name == name }?.value
        }
    }

    /// The secret TOTP enrolment answers; synthetic base32.
    static let totpSecret = "JBSWY3DPEHPK3PXP"
    static let recoveryCodes = ["synthetic-recovery-a", "synthetic-recovery-b", "synthetic-recovery-c"]
    static let apiKeyToken = "vmx_key_synthetic_secret_once"
    static let sidecarSecret = String(repeating: "5a", count: 32)
    static let exportToken = "synthetic-one-time"
    /// The bytes of an empty zip, the export's synthetic file.
    static let exportFile = Data([0x50, 0x4B, 0x05, 0x06] + [UInt8](repeating: 0, count: 18))

    struct Period {
        var id: String
        var tz: String
        var from: String
        var to: String?

        var json: [String: Any] { ["id": id, "tz": tz, "valid_from": from, "valid_to": to ?? NSNull()] }
    }

    struct Origin {
        var id: String
        var key: String
        var name: String?
        var native: Bool
        var relays: String?

        var json: [String: Any] {
            [
                "id": id, "provider": "apple_health", "origin_key": key, "name": name ?? NSNull(),
                "is_native": native, "relayed_provider": relays ?? NSNull(), "created_at": "2026-01-01T08:00:00Z",
            ]
        }
    }

    struct Key {
        var id: String
        var name: String
        var scopes: [String]
        var created: String
        var lastUsed: String?
        var expires: String?
        var revoked: String?

        var json: [String: Any] {
            [
                "id": id, "name": name, "scopes": scopes, "created_at": created, "last_used_at": lastUsed ?? NSNull(),
                "expires_at": expires ?? NSNull(), "revoked_at": revoked ?? NSNull(),
            ]
        }
    }

    struct OtherSession {
        var id: String
        var kind: String
        var name: String?
        var created: String
        var lastSeen: String

        var json: [String: Any] {
            [
                "id": id, "kind": kind, "name": name ?? NSNull(), "created_at": created, "last_seen_at": lastSeen,
                "expires_at": "2099-01-01T00:00:00Z", "current": false,
            ]
        }
    }

    struct Sidecar {
        var name: String
        var url: String
        var source: String
        var bundled: Bool
        var available: Bool
        var created: String?
        /// Connections of its provider exist, so removing it needs a second confirmation.
        var inUse: Bool

        var json: [String: Any] {
            ["name": name, "url": url, "source": source, "bundled": bundled, "available": available, "created_at": created ?? NSNull()]
        }
    }

    struct State {
        var periods = [
            Period(id: "00000000-0000-4000-8000-000000000010", tz: "America/New_York", from: "2024-01-01T00:00:00Z", to: "2026-03-01T05:00:00Z"),
            Period(id: "00000000-0000-4000-8000-000000000011", tz: "Europe/Berlin", from: "2026-03-01T05:00:00Z"),
        ]
        var withingsNotifications = false
        var ai = ["gemini": false, "openai": false, "openai_compatible": false]
        var documentDays: Int?
        var deleteOriginal = false
        var rawDays = ["withings": 90]
        var supersededDays = 0
        var idempotencyDays = 30
        var priority = ["whoop"]
        var origins = [
            Origin(id: "00000000-0000-4000-8000-000000000030", key: "com.apple.health", name: "Health", native: true),
            Origin(id: "00000000-0000-4000-8000-000000000031", key: "com.example.connect", name: "Example Connect", native: false, relays: "garmin"),
            Origin(id: "00000000-0000-4000-8000-000000000032", key: "com.example.scale", name: nil, native: false),
        ]
        var keys = [
            Key(id: "00000000-0000-4000-8000-000000000040", name: "synthetic script", scopes: ["read:health"],
                created: "2026-01-01T08:00:00Z", lastUsed: "2026-01-02T08:00:00Z"),
        ]
        var others = [
            OtherSession(id: "00000000-0000-4000-8000-000000000050", kind: "browser", created: "2026-01-01T09:00:00Z", lastSeen: "2026-01-02T09:00:00Z"),
            OtherSession(id: "00000000-0000-4000-8000-000000000051", kind: "app", name: "Synthetic iPad", created: "2025-12-30T08:00:00Z", lastSeen: "2026-01-01T09:00:00Z"),
        ]
        var totpPending = false
        var sidecars = [
            Sidecar(name: "garmin", url: "http://garmin-sidecar:8080", source: "environment", bundled: true, available: true, inUse: true),
            Sidecar(name: "synthetic_ring", url: "http://ring-sidecar:8080", source: "panel", bundled: false, available: false,
                    created: "2026-01-01T08:00:00Z", inUse: true),
        ]
        var exportPolls: [String: Int] = [:]
        var exportFormats: [String: (format: String, raw: Bool)] = [:]
        var next = 100

        mutating func id() -> String {
            next += 1
            return String(format: "00000000-0000-4000-8000-%012d", next)
        }
    }
}

// MARK: - Routing

extension SettingsFixture.State {
    typealias F = SettingsFixture

    mutating func answer(_ request: F.Request, token: String, core: inout FakeServer.State) -> Reply? {
        let path = request.path
        switch (request.method, path.first ?? "", path.count) {
        case (_, "timezone-periods", _): return periods(request)
        case (_, "settings", 1): return settings(request)
        case (_, "origins", _): return origins(request)
        case (_, "api-keys", _): return apiKeys(request)
        case (_, "auth", 2...): return auth(request, token: token, core: &core)
        case (_, "sidecars", _): return sidecars(request)
        case (_, "exports", _): return exports(request)
        default: return nil
        }
    }

    // MARK: Timezone periods (internal/api/timezone.go)

    private mutating func periods(_ request: F.Request) -> Reply? {
        switch (request.method, request.path.count) {
        case ("GET", 1):
            return F.json(200, ["timezone_periods": periods.map(\.json)])
        case ("POST", 1):
            guard let input = periodInput(request) else { return invalidPeriod(request) }
            if periods.contains(where: { $0.from == input.from }) {
                return F.problem(409, "conflict", "a period already starts at this instant")
            }
            var period = F.Period(id: id(), tz: input.tz, from: input.from)
            periods.append(period)
            periods.sort { $0.from < $1.from }
            relink()
            period = periods.first { $0.id == period.id }!
            return F.json(201, period.json)
        case ("PATCH", 2):
            guard let index = periods.firstIndex(where: { $0.id == request.path[1] }) else {
                return F.problem(404, "not_found", "no such timezone period")
            }
            guard let input = periodInput(request) else { return invalidPeriod(request) }
            periods[index].tz = input.tz
            periods[index].from = input.from
            let id = periods[index].id
            periods.sort { $0.from < $1.from }
            relink()
            return F.json(200, periods.first { $0.id == id }!.json)
        case ("DELETE", 2):
            guard let index = periods.firstIndex(where: { $0.id == request.path[1] }) else {
                return F.problem(404, "not_found", "no such timezone period")
            }
            periods.remove(at: index)
            relink()
            return Reply(status: 204)
        default:
            return nil
        }
    }

    private func periodInput(_ request: F.Request) -> (tz: String, from: String)? {
        guard let object = request.object, let tz = object["tz"] as? String, TimeZone(identifier: tz) != nil,
              let from = object["valid_from"] as? String, (try? Date(from, strategy: .iso8601)) != nil
        else { return nil }
        return (tz, from)
    }

    private func invalidPeriod(_ request: F.Request) -> Reply {
        let tz = request.object?["tz"] as? String
        if tz.flatMap(TimeZone.init(identifier:)) == nil {
            return F.problem(422, "validation_failed", "invalid timezone period", errors: [("/tz", "unknown IANA timezone")])
        }
        return F.problem(422, "validation_failed", "invalid timezone period", errors: [("/valid_from", "must be an RFC 3339 instant")])
    }

    /// Each period ends where the next starts; the newest is current.
    private mutating func relink() {
        for index in periods.indices {
            periods[index].to = index + 1 < periods.count ? periods[index + 1].from : nil
        }
    }

    // MARK: Settings (internal/api/settings.go)

    private var settingsJSON: [String: Any] {
        [
            "withings.notifications": withingsNotifications,
            "documents.external_ai.gemini.enabled": ai["gemini"]!,
            "documents.external_ai.openai.enabled": ai["openai"]!,
            "documents.external_ai.openai_compatible.enabled": ai["openai_compatible"]!,
            "documents.retention_days": documentDays ?? NSNull(),
            "documents.delete_original_after_confirmation": deleteOriginal,
            "retention.raw_days": rawDays,
            "retention.superseded_after_days": supersededDays,
            "retention.idempotency_key_days": idempotencyDays,
            "sources.priority": priority,
        ]
    }

    private mutating func settings(_ request: F.Request) -> Reply? {
        switch request.method {
        case "GET":
            return F.json(200, settingsJSON)
        case "PATCH":
            guard let patch = request.object else { return F.problem(422, "validation_failed", "a JSON object is required") }
            var errors: [(String, String)] = []
            let whole = { (key: String, min: Int) -> Int? in
                guard let value = patch[key] else { return nil }
                guard let number = value as? Int, (min...36500).contains(number) else {
                    errors.append(("/\(key)", "must be a whole number from \(min) to 36500"))
                    return nil
                }
                return number
            }
            let idempotency = whole("retention.idempotency_key_days", 7)
            let superseded = whole("retention.superseded_after_days", 0)
            let documents = patch["documents.retention_days"] is NSNull ? nil : whole("documents.retention_days", 1)
            let raw = patch["retention.raw_days"] as? [String: Int]
            if let raw, raw.values.contains(where: { !(0...36500).contains($0) }) {
                errors.append(("/retention.raw_days", "days must be from 0 to 36500"))
            }
            guard errors.isEmpty else { return F.problem(422, "validation_failed", "invalid settings", errors: errors) }
            for (key, name) in [("gemini", "gemini"), ("openai", "openai"), ("openai_compatible", "openai_compatible")] {
                if let on = patch["documents.external_ai.\(name).enabled"] as? Bool { ai[key] = on }
            }
            if let on = patch["withings.notifications"] as? Bool { withingsNotifications = on }
            if let on = patch["documents.delete_original_after_confirmation"] as? Bool { deleteOriginal = on }
            if patch["documents.retention_days"] is NSNull { documentDays = nil } else if let documents { documentDays = documents }
            if let raw { rawDays.merge(raw) { $1 } }
            if let superseded { supersededDays = superseded }
            if let idempotency { idempotencyDays = idempotency }
            if let order = patch["sources.priority"] as? [String] { priority = order }
            return F.json(200, settingsJSON)
        default:
            return nil
        }
    }

    // MARK: Origins (internal/api/origins.go)

    private static let relayTargets = [["code": "garmin", "name": "Garmin"], ["code": "whoop", "name": "WHOOP"], ["code": "withings", "name": "Withings"]]

    private mutating func origins(_ request: F.Request) -> Reply? {
        switch (request.method, request.path.count) {
        case ("GET", 1):
            return F.json(200, ["origins": origins.map(\.json), "relay_targets": Self.relayTargets])
        case ("PATCH", 2):
            guard let index = origins.firstIndex(where: { $0.id == request.path[1] }) else {
                return F.problem(404, "not_found", "no such origin")
            }
            let target = request.object?["relayed_provider"] as? String
            if let target, !Self.relayTargets.contains(where: { $0["code"] == target }) {
                return F.problem(422, "validation_failed", "unknown relay target", errors: [("/relayed_provider", "not one of relay_targets")])
            }
            origins[index].relays = target
            return Reply(status: 204)
        default:
            return nil
        }
    }

    // MARK: API keys (internal/api/auth.go)

    private mutating func apiKeys(_ request: F.Request) -> Reply? {
        switch (request.method, request.path.count) {
        case ("GET", 1):
            return F.json(200, ["api_keys": keys.map(\.json)])
        case ("POST", 1):
            let input = request.object ?? [:]
            let name = (input["name"] as? String) ?? ""
            let scopes = (input["scopes"] as? [String]) ?? []
            var errors: [(String, String)] = []
            if !(1...100).contains(name.count) { errors.append(("/name", "must be 1 to 100 characters")) }
            let expires = input["expires_at"] as? String
            if let expires, ((try? Date(expires, strategy: .iso8601)) ?? .distantPast) <= .now {
                errors.append(("/expires_at", "must be in the future"))
            }
            if scopes.isEmpty { errors.append(("/scopes", "must be a non-empty list of known scopes")) }
            guard errors.isEmpty else { return F.problem(422, "validation_failed", "invalid API key", errors: errors) }
            let key = F.Key(id: id(), name: name, scopes: scopes, created: Date.now.formatted(.iso8601), expires: expires)
            keys.append(key)
            return F.json(201, key.json.merging(["token": F.apiKeyToken]) { $1 })
        case ("DELETE", 2):
            guard let index = keys.firstIndex(where: { $0.id == request.path[1] && $0.revoked == nil }) else {
                return F.problem(404, "not_found", "no active API key with this id")
            }
            keys[index].revoked = Date.now.formatted(.iso8601)
            return Reply(status: 204)
        default:
            return nil
        }
    }

    // MARK: Password, TOTP and sessions (internal/api/auth.go)

    private mutating func auth(_ request: F.Request, token: String, core: inout FakeServer.State) -> Reply? {
        let path = request.path
        let input = request.object ?? [:]
        switch (request.method, path[1], path.count) {
        case ("POST", "password", 2):
            if input["current_password"] as? String != FakeServer.Owner.password {
                return F.problem(422, "validation_failed", "the current password is wrong", errors: [("/current_password", "does not match")])
            }
            guard ((input["new_password"] as? String) ?? "").count >= 12 else {
                return F.problem(422, "validation_failed", "invalid password", errors: [("/new_password", "must be at least 12 characters")])
            }
            // Every other session ends; sign-in keeps the synthetic password, so tests can sign in again.
            others.removeAll()
            core.sessions = core.sessions.filter { $0.key == token }
            return Reply(status: 204)
        case ("POST", "totp", 3) where path[2] == "enroll":
            guard !core.totpEnabled else { return F.problem(409, "conflict", "TOTP is already enabled; disable it first") }
            totpPending = true
            return F.json(200, [
                "secret": F.totpSecret,
                "otpauth_uri": "otpauth://totp/Vitamux:\(FakeServer.Owner.username)?secret=\(F.totpSecret)&issuer=Vitamux",
            ])
        case ("POST", "totp", 3) where path[2] == "confirm":
            guard totpPending, !core.totpEnabled else { return F.problem(409, "conflict", "there is no pending TOTP enrolment") }
            guard input["code"] as? String == FakeServer.Owner.totp else {
                return F.problem(422, "validation_failed", "the code does not match", errors: [("/code", "does not match")])
            }
            totpPending = false
            core.totpEnabled = true
            return F.json(200, ["recovery_codes": F.recoveryCodes])
        case ("POST", "totp", 3) where path[2] == "disable":
            guard core.totpEnabled else { return F.problem(409, "conflict", "TOTP is not enabled") }
            let code = input["totp_code"] as? String, recovery = input["recovery_code"] as? String
            guard input["password"] as? String == FakeServer.Owner.password,
                  code == FakeServer.Owner.totp || recovery == FakeServer.Owner.recovery || recovery.map(F.recoveryCodes.contains) == true
            else { return F.problem(403, "forbidden", "wrong password or code") }
            core.totpEnabled = false
            return Reply(status: 204)
        case ("GET", "sessions", 2):
            let current: [String: Any] = [
                "id": "00000000-0000-4000-8000-000000000002", "kind": "app", "name": core.sessions[token] ?? NSNull(),
                "created_at": "2026-01-01T08:00:00Z", "last_seen_at": "2026-01-02T08:00:00.25Z",
                "expires_at": "2099-01-01T00:00:00Z", "current": true,
            ]
            return F.json(200, ["sessions": [current] + others.map(\.json)])
        case ("DELETE", "sessions", 3):
            if path[2] == "00000000-0000-4000-8000-000000000002" {
                core.sessions[token] = nil
                return Reply(status: 204)
            }
            guard others.contains(where: { $0.id == path[2] }) else { return F.problem(404, "not_found", "no session with this id") }
            others.removeAll { $0.id == path[2] }
            return Reply(status: 204)
        default:
            return nil
        }
    }

    // MARK: Sidecars (internal/api/setup.go)

    private mutating func sidecars(_ request: F.Request) -> Reply? {
        switch (request.method, request.path.count) {
        case ("GET", 1):
            return F.json(200, ["sidecars": sidecars.map(\.json)])
        case ("POST", 1):
            let input = request.object ?? [:]
            let name = (input["name"] as? String) ?? "", url = (input["url"] as? String) ?? ""
            var errors: [(String, String)] = []
            if name.wholeMatch(of: /[a-z][a-z0-9_]{0,62}/) == nil { errors.append(("/name", "a provider code: lowercase letters, digits and _")) }
            let host = URL(string: url)?.host() ?? ""
            if !(host.hasSuffix("-sidecar") || host.hasPrefix("10.") || host.hasPrefix("192.168.") || host == "localhost") {
                errors.append(("/url", "must resolve to a loopback, private or link-local address"))
            }
            guard errors.isEmpty else { return F.problem(422, "validation_failed", "invalid sidecar", errors: errors) }
            guard !sidecars.contains(where: { $0.name == name }) else { return F.problem(409, "conflict", "a sidecar with this name exists") }
            let sidecar = F.Sidecar(name: name, url: url, source: "panel", bundled: false, available: false,
                                    created: Date.now.formatted(.iso8601), inUse: false)
            sidecars.append(sidecar)
            return F.json(201, ["sidecar": sidecar.json, "secret": F.sidecarSecret])
        case ("DELETE", 2):
            guard let index = sidecars.firstIndex(where: { $0.name == request.path[1] }) else {
                return F.problem(404, "not_found", "no such sidecar")
            }
            guard sidecars[index].source == "panel" else { return F.problem(409, "conflict", "set by the environment; read-only here") }
            if sidecars[index].inUse, request.value("confirm") != "true" {
                return F.problem(409, "conflict", "connections of \(sidecars[index].name) exist")
            }
            sidecars.remove(at: index)
            return Reply(status: 204)
        default:
            return nil
        }
    }

    // MARK: Exports (internal/api/exports.go): queued, then running once, then done

    private mutating func exports(_ request: F.Request) -> Reply? {
        let path = request.path
        switch (request.method, path.count) {
        case ("POST", 1):
            let input = request.object ?? [:]
            guard let format = input["format"] as? String, ["ndjson", "csv"].contains(format) else {
                return F.problem(422, "validation_failed", "invalid export", errors: [("/format", "must be ndjson or csv")])
            }
            let id = "exp_\(next)"
            next += 1
            exportPolls[id] = 0
            exportFormats[id] = (format, input["include_raw"] as? Bool ?? false)
            return F.json(202, ["id": id, "status": "queued", "format": format, "include_raw": exportFormats[id]!.raw, "created_at": Date.now.formatted(.iso8601)])
        case ("GET", 2):
            guard let polls = exportPolls[path[1]], let spec = exportFormats[path[1]] else { return F.problem(404, "not_found", "no such export") }
            exportPolls[path[1]] = polls + 1
            var export: [String: Any] = [
                "id": path[1], "status": polls < 1 ? "running" : "done", "format": spec.format, "include_raw": spec.raw,
                "created_at": Date.now.addingTimeInterval(-5).formatted(.iso8601),
            ]
            if polls >= 1 {
                export["finished_at"] = Date.now.formatted(.iso8601)
                export["size_bytes"] = F.exportFile.count
                export["download_url"] = "/api/v1/exports/\(path[1])/download?token=\(F.exportToken)"
            }
            return F.json(200, export)
        case ("GET", 3) where path[2] == "download":
            guard exportPolls[path[1]] != nil, request.value("token") == F.exportToken else {
                return F.problem(404, "not_found", "the download link has expired")
            }
            return Reply(status: 200, headers: ["Content-Type": "application/zip"], body: F.exportFile)
        default:
            return nil
        }
    }
}

// MARK: - Responses

extension SettingsFixture {
    static func json(_ status: Int, _ object: [String: Any]) -> Reply {
        Reply(status: status, body: try! JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]))
    }

    static func problem(_ status: Int, _ code: String, _ detail: String, errors: [(String, String)] = []) -> Reply {
        let titles = [
            "validation_failed": "Validation failed", "not_found": "Not found", "conflict": "Conflict",
            "forbidden": "Forbidden", "rate_limited": "Rate limited",
        ]
        var object: [String: Any] = [
            "type": "urn:vitamux:problem:\(code)", "title": titles[code] ?? code, "status": status,
            "code": code, "detail": detail, "request_id": "req-fake",
        ]
        if !errors.isEmpty { object["errors"] = errors.map { ["pointer": $0.0, "detail": $0.1] } }
        var reply = json(status, object)
        reply.headers = ["Content-Type": "application/problem+json"]
        return reply
    }
}
#endif
