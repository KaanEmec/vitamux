#if DEBUG
import Foundation

// The fake's endpoints and synthetic fixtures, mirroring web/e2e/fake-api.ts and the server's
// handlers (internal/api/auth.go, problem.go). Values are synthetic. Add an endpoint here when a
// screen's UI test needs it.
extension FakeServer {
    static func route(method: String, url: URL, authorization: String?, body: Data, state: inout State) -> Reply {
        if state.handshake == .notVitamux {
            return Reply(status: 404, headers: ["Content-Type": "text/html"], body: Data("<html><body>Not found</body></html>".utf8))
        }
        let token = authorization.flatMap { $0.hasPrefix("Bearer ") ? String($0.dropFirst(7)) : nil }
        switch (method, url.path) {
        case ("POST", "/api/v1/auth/login"):
            return login(body, state: &state)
        case ("GET", "/api/v1/system/version") where token == nil && state.handshake == .current:
            // Public: the app checks a server before sign-in (internal/api/system.go).
            return json(200, handshakeBody)
        default:
            break
        }
        guard url.path.hasPrefix("/api/") else { return problem(404, "not_found", "not stubbed in the fake server") }
        guard let token, let device = state.sessions[token] else {
            return problem(401, "unauthenticated", token == nil ? "sign in or send a bearer token" : "invalid, revoked or expired token")
        }
        if let reply = dashboard(method: method, url: url, body: body) { return reply } // FakeServer+Dashboard.swift
        if let reply = explore(method: method, url: url, body: body) { return reply } // FakeServer+Explore.swift
        if let reply = specialised(method: method, url: url) { return reply } // FakeServer+Specialised.swift
        if let reply = sources(method: method, url: url, body: body) { return reply } // FakeServer+Sources.swift
        switch (method, url.path) {
        case ("POST", "/api/v1/auth/logout"):
            state.sessions[token] = nil
            return Reply(status: 204)
        case ("GET", "/api/v1/auth/session"):
            return json(200, ["user": user(state), "csrf_token": ""])
        case ("GET", "/api/v1/auth/sessions"):
            return json(200, ["sessions": [activeSession(name: device)]])
        case ("GET", "/api/v1/system/version"):
            guard state.handshake == .current else { return json(200, ["version": "0.3.1-fake", "commit": "fake"]) }
            return json(200, handshakeBody.merging(["version": "0.0.0-fake", "commit": "fake"]) { $1 })
        case ("GET", "/api/v1/metrics"):
            return json(200, ["metrics": allMetrics])
        case ("POST", _) where url.path.hasPrefix("/api/v1/devices/") && url.path.hasSuffix("/revoke"):
            return Reply(status: 204)
        case ("GET", "/api/v1/timezone-periods"):
            return json(200, ["timezone_periods": timezonePeriods])
        case ("GET", "/api/v1/measurements"):
            return measurements(url)
        default:
            return problem(404, "not_found", "not stubbed in the fake server")
        }
    }

    // MARK: - Sign-in (internal/api/auth.go)

    private static func login(_ body: Data, state: inout State) -> Reply {
        guard let input = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any] else {
            return problem(422, "validation_failed", "request body is not valid JSON")
        }
        let field = { (name: String) in input[name] as? String }
        if state.failedLogins >= lockoutAfter {
            return problem(429, "rate_limited", "too many failed sign-in attempts; try again later",
                           headers: ["Retry-After": String(lockoutSeconds)])
        }
        if field("username") == "bad name" {
            return problem(422, "validation_failed", "invalid sign-in request",
                           errors: [("/username", "must not contain spaces")])
        }
        let app: Bool
        switch field("client") {
        case nil, "browser":
            guard field("device_name") == nil else {
                return problem(422, "validation_failed", "invalid sign-in request", errors: [("/device_name", "only with client app")])
            }
            app = false
        case "app":
            let name = field("device_name")?.trimmingCharacters(in: .whitespaces) ?? ""
            guard (1...100).contains(name.count) else {
                return problem(422, "validation_failed", "invalid sign-in request",
                               errors: [("/device_name", "must be 1 to 100 characters without control characters")])
            }
            app = true
        default:
            return problem(422, "validation_failed", "invalid sign-in request", errors: [("/client", "must be browser or app")])
        }
        let invalid = "invalid username, password or code"
        guard field("username") == Owner.username, field("password") == Owner.password else {
            state.failedLogins += 1
            return problem(401, "unauthenticated", invalid)
        }
        if state.totpEnabled {
            let totp = field("totp_code"), recovery = field("recovery_code")
            if totp == nil, recovery == nil {
                return problem(401, "totp_required", "enter the code from your authenticator app or a recovery code")
            }
            guard totp == Owner.totp || recovery == Owner.recovery else {
                state.failedLogins += 1
                return problem(401, "unauthenticated", invalid)
            }
        }
        state.failedLogins = 0
        guard app else { return json(200, ["user": user(state), "csrf_token": "synthetic-csrf"]) }
        let id = UUID().uuidString.replacingOccurrences(of: "-", with: "").lowercased()
        let token = "vmx_ses_\(id)_synthetic-secret"
        state.sessions[token] = field("device_name")
        return json(200, ["user": user(state), "token": token, "expires_at": "2099-01-01T00:00:00Z"])
    }

    private static func user(_ state: State) -> [String: Any] {
        ["id": "00000000-0000-4000-8000-000000000001", "username": Owner.username, "totp_enabled": state.totpEnabled]
    }

    private static func activeSession(name: String) -> [String: Any] {
        [
            "id": "00000000-0000-4000-8000-000000000002", "kind": "app", "name": name,
            "created_at": "2026-01-01T08:00:00Z", "last_seen_at": "2026-01-02T08:00:00.25Z",
            "expires_at": "2099-01-01T00:00:00Z", "current": true,
        ]
    }

    static var handshakeBody: [String: Any] { ["product": "vitamux", "api_version": 1, "min_app_version": "0.4.0"] }

    // MARK: - Catalogue (the shell's search; connections are in FakeServer+Sources.swift)

    private static func metric(_ code: String, section: String, unit: String, kind: String, aggregation: String) -> [String: Any] {
        [
            "code": code, "section": section, "unit": unit, "kinds": [kind], "aggregation": aggregation,
            "windows": ["bucket", "local_day"], "strategies": ["single_source", "first_available", "mean_across_sources"],
            "plausible_range": [0, 100_000], "provider_scoped": false, "selection_only": false,
        ]
    }

    static var metrics: [[String: Any]] { exploreMetrics + [
        metric("heart_rate", section: "heart", unit: "bpm", kind: "sample", aggregation: "intensive"),
        metric("heart_rate_resting", section: "heart", unit: "bpm", kind: "daily_value", aggregation: "intensive"),
        metric("steps", section: "activity", unit: "count", kind: "cumulative", aggregation: "additive"),
        metric("body_mass", section: "body", unit: "kg", kind: "sample", aggregation: "latest"),
    ] }

    /// `GET /metrics`: the shell's and Explore's entries, then the dashboard's codes not already listed.
    static var allMetrics: [[String: Any]] {
        let base = metrics
        let known = Set(base.compactMap { $0["code"] as? String })
        return base + dashboardMetrics.filter { !known.contains($0["code"] as? String ?? "") }
    }

    // MARK: - Settings

    private static var timezonePeriods: [[String: Any]] { [
        ["id": "00000000-0000-4000-8000-000000000010", "tz": "America/New_York",
         "valid_from": "2024-01-01T00:00:00Z", "valid_to": "2026-03-01T05:00:00Z"],
        ["id": "00000000-0000-4000-8000-000000000011", "tz": "Europe/Berlin",
         "valid_from": "2026-03-01T05:00:00Z", "valid_to": NSNull()],
    ] }

    // MARK: - Source data: five synthetic heart-rate samples, paged by an opaque cursor

    static let measurementCount = 5

    private static func measurements(_ url: URL) -> Reply {
        let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        let value = { (name: String) in query.first { $0.name == name }?.value }
        let limit = value("limit").flatMap(Int.init) ?? 500
        var offset = 0
        if let cursor = value("cursor") {
            guard cursor.hasPrefix("fake-"), let parsed = Int(cursor.dropFirst(5)), (0..<measurementCount).contains(parsed) else {
                return problem(422, "validation_failed", "invalid cursor", errors: [("/cursor", "invalid or expired cursor")])
            }
            offset = parsed
        }
        let end = min(offset + max(limit, 1), measurementCount)
        var page: [String: Any] = ["measurements": (offset..<end).map(measurement), "has_more": end < measurementCount]
        if end < measurementCount { page["next_cursor"] = "fake-\(end)" }
        return json(200, page)
    }

    private static func measurement(_ index: Int) -> [String: Any] {
        let day = String(format: "2026-01-%02d", index + 1)
        return [
            "id": "msr_fake_\(index)", "metric": "heart_rate", "kind": "sample",
            // Alternating whole and fractional seconds, as the server writes them.
            "start_at": index.isMultiple(of: 2) ? "\(day)T07:00:00Z" : "\(day)T07:00:00.5+01:00",
            "end_at": NSNull(), "tz_offset_min": 60, "local_date": day,
            "value": 60 + index, "unit": "bpm", "source_value": NSNull(), "source_unit": NSNull(),
            "quality_flags": 0, "group_id": NSNull(),
            "source": [
                "provider": "apple_health", "connection_id": "conn_" + String(repeating: "0", count: 31) + "1",
                "device": NSNull(), "device_type": NSNull(), "origin": NSNull(), "external_id": NSNull(),
                "dedupe_key": String(format: "%032x", index + 1),
            ],
            "provenance": [
                "raw_payload_id": "raw_fake_\(index)", "normalizer": "fake@1",
                "ingested_at": "\(day)T07:05:00Z", "normalized_at": "\(day)T07:05:01.123Z",
                "superseded_at": NSNull(), "superseded_by": NSNull(), "deleted_at": NSNull(), "deleted_by_raw_id": NSNull(),
            ],
        ]
    }

    // MARK: - Responses

    private static func json(_ status: Int, _ object: [String: Any]) -> Reply {
        Reply(status: status, body: try! JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]))
    }

    /// The server's problem+json, with its registry's titles.
    private static func problem(
        _ status: Int, _ code: String, _ detail: String,
        errors: [(pointer: String, detail: String)] = [], headers: [String: String] = [:]
    ) -> Reply {
        let titles = [
            "validation_failed": "Validation failed", "unauthenticated": "Authentication required",
            "totp_required": "TOTP code required", "not_found": "Not found", "rate_limited": "Rate limited",
        ]
        var object: [String: Any] = [
            "type": "urn:vitamux:problem:\(code)", "title": titles[code] ?? code, "status": status,
            "code": code, "detail": detail, "request_id": "req-fake",
        ]
        if !errors.isEmpty { object["errors"] = errors.map { ["pointer": $0.pointer, "detail": $0.detail] } }
        var reply = json(status, object)
        reply.headers = headers.merging(["Content-Type": "application/problem+json"]) { $1 }
        return reply
    }
}
#endif
