#if DEBUG
import Foundation
import Synchronization

// Sources (J22.11) for the fake server, mirroring web/e2e/connections-fake.ts and setup-fake.ts:
// connections with health, runs, streams, schedules, backfills and devices; providers with their
// E20 setup states, app credentials and sidecar probe; and the connection auth flows. Every value
// is synthetic. GET /connections is answered here (the dashboard's empty install answers it first).
//
// Seed: Withings (official, healthy, a running backfill, three devices, one of them a duplicate
// scale) and Garmin Connect (an unofficial sidecar that needs reauthorization, a failed backfill
// with a failed unit). Providers: Withings without app credentials (`-uitest-withings-env`: set by
// the environment), Garmin with sign-in prompts, WHOOP whose sidecar is off until one probe.
//
// OAuth (Withings) answers a start ticket only for `{"return": "app"}`; `authorize(_:)` plays the
// auth browser: it consumes the ticket, applies what the callback does and answers the
// `vitamux://connections?…` return. Prompt steps (Garmin, WHOOP) ask for an email and password,
// then a code (`SourcesFixture.Login`); special values make a step fail with 429 or 503.
extension FakeServer {
    /// Answers a Sources endpoint, or nil for a request this file does not handle.
    static func sources(method: String, url: URL, body: Data) -> Reply? {
        let host = url.host() ?? ""
        let parts = url.path.split(separator: "/").map(String.init)
        guard parts.count >= 3, parts[0] == "api", parts[1] == "v1",
              ["connections", "providers", "schedules", "source-devices"].contains(parts[2])
        else { return nil }
        let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        return SourcesFixture.states.withLock { states in
            let input = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any] ?? [:]
            var state = states[host] ?? SourcesFixture.State(host: host)
            defer { states[host] = state }
            return state.route(method: method, parts: Array(parts.dropFirst(2)), query: query, body: input)
        }
    }

    /// The auth browser of UI tests: opens a `redirect_url` from auth/begin and answers where the
    /// server's callback would send the app (`vitamux://connections?connected=…` or `?auth_error=`).
    public func authorize(_ url: URL) -> URL {
        let host = profile.baseURL.host() ?? ""
        let answer = SourcesFixture.states.withLock { states in
            var state = states[host] ?? SourcesFixture.State(host: host)
            defer { states[host] = state }
            return state.authorize(url)
        }
        return URL(string: answer)!
    }

    /// Every request body the Sources endpoints received, as "METHOD /path" and JSON, for checks
    /// that secrets are sent once and nothing else.
    public var sourcesSent: [(route: String, body: [String: String])] {
        let host = profile.baseURL.host() ?? ""
        return SourcesFixture.states.withLock { $0[host]?.sent ?? [] }
    }
}

/// The synthetic Sources dataset and the state the owner's actions change, per fake host.
struct SourcesFixture {
    static let states = Mutex<[String: State]>([:])

    /// The sign-in a sidecar prompt accepts; `limited` answers 429, `gone` (as the code) 503.
    enum Login {
        static let email = "synthetic@example.test"
        static let password = "synthetic-pass"
        static let code = "654321"
        static let limited = "synthetic-limited"
        static let gone = "000503"
    }

    /// The client secret the Withings app check accepts.
    static let appSecret = "synthetic-client-secret"

    static let withingsID = "conn_" + String(format: "%032x", 1)
    static let garminID = "conn_" + String(format: "%032x", 2)

    static let streams: [String: [String]] = [
        "withings": ["withings.measures", "withings.sleep"],
        "garmin": ["garmin.daily_summary", "garmin.intraday_reload"],
        "whoop": ["whoop.cycles"],
    ]

    static let deviceTypes = ["watch", "band", "ring", "phone", "chest_strap", "arm_band", "scale", "bp_monitor",
                              "under_mattress", "sleep_monitor", "cgm", "glucose_meter", "other"]

    static func time(_ date: Date) -> String {
        date.formatted(.iso8601)
    }

    static func ago(days: Double = 0, hours: Double = 0) -> Date {
        Date.now.addingTimeInterval(-(days * 86_400 + hours * 3_600))
    }

    struct Connection {
        var id: String
        var provider: String
        var mode: String
        var status: String
        var official: Bool
        var upstream: [String: Any]?
        var health: String
        var healthReason: String?
        var lastSuccess: Date?
        var lastError: String?
        var failures: Int
        var created: Date

        var json: [String: Any] {
            [
                "id": id, "provider": provider, "mode": mode, "status": status, "official": official,
                "upstream": upstream ?? NSNull(), "health": health, "health_reason": healthReason ?? NSNull(),
                "last_success_at": lastSuccess.map(SourcesFixture.time) ?? NSNull(), "last_error_class": lastError ?? NSNull(),
                "consecutive_failures": failures, "created_at": SourcesFixture.time(created),
                "updated_at": SourcesFixture.time(created.addingTimeInterval(86_400)),
            ]
        }

        mutating func healthy() {
            status = "active"
            health = "ok"
            healthReason = nil
            lastError = nil
            failures = 0
        }
    }

    struct Run {
        var id: String
        var kind = "sync"
        var attempt = 1
        var started: Date
        var outcome: String
        var error: (class: String, message: String)?

        var json: [String: Any] {
            [
                "id": id, "job_id": "55555555-5555-4555-8555-" + String(format: "%012d", Int(id) ?? 0), "kind": kind,
                "attempt": attempt, "started_at": SourcesFixture.time(started),
                "finished_at": SourcesFixture.time(started.addingTimeInterval(65)), "outcome": outcome,
                "error_class": error?.class ?? NSNull(), "error_message": error?.message ?? NSNull(), "stats": ["records": 12],
            ]
        }
    }

    struct Unit {
        var start: Date
        var end: Date
        var status: String
        var attempts: Int
        var error: String?

        var json: [String: Any] {
            [
                "start": SourcesFixture.time(start), "end": SourcesFixture.time(end), "status": status,
                "attempts": attempts, "error_class": error ?? NSNull(), "updated_at": SourcesFixture.time(start),
            ]
        }
    }

    struct Backfill {
        var id: String
        var connection: String
        var stream: String
        var status: String
        var created: Date
        var dailyLimit: Int?
        var units: [Unit]

        var start: Date { units.first?.start ?? created }
        var end: Date { units.last?.end ?? created }

        func json(units withUnits: Bool) -> [String: Any] {
            let count = { (status: String) in units.filter { $0.status == status }.count }
            var out: [String: Any] = [
                "id": id, "connection_id": connection, "stream": stream, "start": SourcesFixture.time(start),
                "end": SourcesFixture.time(end), "status": status, "created_at": SourcesFixture.time(created),
                "finished_at": status == "running" ? NSNull() : SourcesFixture.time(created.addingTimeInterval(600)),
                "daily_limit": dailyLimit ?? NSNull(),
                "unit_counts": ["pending": count("pending"), "running": count("running"), "done": count("done"), "failed": count("failed")],
            ]
            if withUnits { out["units"] = units.map(\.json) }
            return out
        }

        /// The server works through a running backfill: one pending unit finishes per look.
        mutating func advance() {
            guard status == "running" else { return }
            if let next = units.firstIndex(where: { $0.status == "pending" }) {
                units[next].status = "done"
                units[next].attempts += 1
            }
            if !units.contains(where: { $0.status == "pending" }) {
                status = units.contains { $0.status == "failed" } ? "failed" : "done"
            }
        }

        static func units(from start: Date, to end: Date, days: Int = 30, status: (Int) -> String) -> [Unit] {
            var out: [Unit] = []
            var at = start
            while at < end {
                let next = min(at.addingTimeInterval(Double(days) * 86_400), end)
                let st = status(out.count)
                out.append(Unit(start: at, end: next, status: st, attempts: st == "pending" ? 0 : st == "failed" ? 5 : 1,
                                error: st == "failed" ? "upstream_5xx" : nil))
                at = next
            }
            return out
        }
    }

    struct Schedule {
        var id: String
        var connection: String
        var stream: String
        var mode: String
        var interval: Int
        var lookback: Int
        var enabled = true

        var json: [String: Any] {
            [
                "id": id, "connection_id": connection, "stream": stream, "mode": mode, "interval_seconds": interval,
                "lookback_seconds": lookback, "enabled": enabled,
                "next_run_at": SourcesFixture.time(Date.now.addingTimeInterval(Double(interval))),
            ]
        }
    }

    struct Device {
        var id: String
        var provider: String
        var fingerprint: String
        var name: String?
        var type: String?
        var manufacturer: String?
        var model: String?
        var mergedInto: String?
        var connection: String
        var records: [String: Int]

        func json(records withRecords: Bool) -> [String: Any] {
            var out: [String: Any] = [
                "id": id, "provider": provider, "fingerprint": fingerprint, "name": name ?? NSNull(),
                "device_type": type ?? NSNull(), "manufacturer": manufacturer ?? NSNull(), "model": model ?? NSNull(),
                "merged_into": mergedInto ?? NSNull(),
            ]
            if withRecords {
                let all = ["measurements", "groups", "sleep_sessions", "workouts", "events"]
                let counts = Dictionary(uniqueKeysWithValues: all.map { ($0, records[$0] ?? 0) })
                out["connections"] = mergedInto == nil ? [["connection_id": connection, "records": counts]] : []
            }
            return out
        }
    }

    struct Provider {
        var code: String
        var name: String
        var official: Bool
        var authKind: String?
        var remote: Bool
        var available: Bool
        var upstream: [String: Any]?
        var callback: String?
        var problems: [[String: String]] = []
        /// set, managed by the environment, client id; nil when the provider needs no app.
        var app: (set: Bool, environment: Bool, clientID: String?)?
        var sidecar: [String: Any]?
    }

    /// A prompt step waiting for its values, or an app redirect waiting for its ticket.
    struct Pending {
        var provider: String
        var step: String
        var connection: String?
    }

    struct State {
        var connections: [Connection]
        var runs: [String: [Run]]
        var backfills: [Backfill]
        var schedules: [Schedule]
        var cursors: [String: Bool] = [:]
        var devices: [Device]
        var providers: [Provider]
        var pending: [String: Pending] = [:]
        var tickets: [String: Pending] = [:]
        var sent: [(route: String, body: [String: String])] = []
        var probesUntilUp = 1
        var next = 0x100

        init(host: String) {
            let garminUpstream: [String: Any] = ["package": "garminconnect", "version": "0.3.17",
                                                 "source_url": "https://github.com/cyberjunky/python-garminconnect"]
            connections = [
                Connection(id: withingsID, provider: "withings", mode: "in_process", status: "active", official: true,
                           health: "ok", lastSuccess: ago(hours: 2), failures: 0, created: ago(days: 400)),
                Connection(id: garminID, provider: "garmin", mode: "remote", status: "needs_reauth", official: false,
                           upstream: garminUpstream, health: "needs_reauth", healthReason: "Garmin signed this account out; sign in again",
                           lastSuccess: ago(days: 2, hours: 1), lastError: "auth_expired", failures: 3, created: ago(days: 200)),
            ]
            // Withings: a daily sync for 60 days (one failed 3 days ago); Garmin failed the last two days.
            var serial = 1000
            func run(_ back: Int, _ failed: Bool, _ error: (String, String)) -> Run {
                serial -= 1
                return Run(id: String(serial), started: ago(days: Double(back), hours: 1), outcome: failed ? "failed" : "succeeded",
                           error: failed ? error : nil)
            }
            runs = [
                withingsID: (0..<60).map { run($0, $0 == 3, ("upstream_5xx", "the provider answered 503")) },
                garminID: (0..<14).map { run($0, $0 < 2, ("auth_expired", "the sign-in is no longer accepted")) },
            ]
            backfills = [
                Backfill(id: "44444444-4444-4444-8444-000000000001", connection: withingsID, stream: "withings.measures",
                         status: "running", created: ago(hours: 1),
                         units: Backfill.units(from: ago(days: 300), to: ago(days: 60)) { $0 < 6 ? "done" : "pending" }),
                Backfill(id: "44444444-4444-4444-8444-000000000002", connection: garminID, stream: "garmin.daily_summary",
                         status: "failed", created: ago(days: 5),
                         units: Backfill.units(from: ago(days: 180), to: ago(days: 90)) { $0 == 1 ? "failed" : "done" }),
            ]
            var schedules: [Schedule] = []
            for connection in connections {
                for stream in streams[connection.provider] ?? [] {
                    let n = schedules.count
                    schedules.append(Schedule(id: String(format: "66666666-6666-4666-8666-%012d", n + 1), connection: connection.id,
                                              stream: stream, mode: "incremental", interval: 3_600, lookback: 0))
                    schedules.append(Schedule(id: String(format: "66666666-6666-4666-8666-%012d", n + 2), connection: connection.id,
                                              stream: stream, mode: "correction", interval: 86_400, lookback: 7 * 86_400))
                }
            }
            self.schedules = schedules
            for connection in connections where connection.mode == "in_process" {
                for stream in streams[connection.provider] ?? [] { cursors["\(connection.id)/\(stream)"] = true }
            }
            let device = { (n: Int) in "dev_" + String(format: "%032x", n) }
            devices = [
                Device(id: device(1), provider: "withings", fingerprint: "synthetic-scale-1", type: "scale", manufacturer: "Withings",
                       model: "Synthetic Scale", connection: withingsID, records: ["groups": 120, "measurements": 480]),
                Device(id: device(2), provider: "withings", fingerprint: "synthetic-scale-1b", manufacturer: "Withings",
                       connection: withingsID, records: ["groups": 4]),
                Device(id: device(3), provider: "withings", fingerprint: "synthetic-cuff", type: "bp_monitor", manufacturer: "Withings",
                       model: "Synthetic Cuff", connection: withingsID, records: ["groups": 30]),
                Device(id: device(4), provider: "garmin", fingerprint: "synthetic-watch", type: "watch", manufacturer: "Garmin",
                       model: "Synthetic Watch", connection: garminID, records: ["measurements": 1440, "sleep_sessions": 7]),
            ]
            let environment = FakeServer.isUITestRun && ProcessInfo.processInfo.arguments.contains("-uitest-withings-env")
            let enable: [[String: String]] = [
                ["install": "compose", "line": "COMPOSE_PROFILES=whoop", "apply": "docker compose up -d"],
                ["install": "coolify", "line": "WHOOP_SIDECAR=1", "apply": "Redeploy the resource in Coolify"],
            ]
            providers = [
                Provider(code: "withings", name: "Withings", official: true, authKind: "oauth2", remote: false, available: true,
                         callback: "https://\(host)/oauth/withings/callback",
                         app: environment ? (true, true, "synthetic-env-client") : (false, false, nil)),
                Provider(code: "garmin", name: "Garmin Connect", official: false, authKind: "interactive_mfa", remote: true,
                         available: true, upstream: garminUpstream,
                         sidecar: ["source": "environment", "bundled": true, "enable": [[String: String]]()]),
                Provider(code: "whoop", name: "WHOOP", official: false, authKind: nil, remote: true, available: false,
                         problems: [["code": "sidecar_unreachable", "message": "The whoop sidecar is not running. Turn it on as shown, then check again."]],
                         sidecar: ["source": "environment", "bundled": true, "enable": enable]),
            ]
        }

        // MARK: Routing

        mutating func route(method: String, parts: [String], query: [URLQueryItem], body: [String: Any]) -> Reply {
            let value = { (name: String) in query.first { $0.name == name }?.value }
            if method != "GET" {
                let fields = body.compactMapValues { $0 as? String }.merging(
                    (body["values"] as? [String: String]) ?? [:]) { $1 }
                sent.append(("\(method) /" + parts.joined(separator: "/"), fields))
            }
            switch (method, parts.count, parts.first) {
            case ("GET", 1, "connections"):
                return json(200, ["connections": connections.map(\.json)])
            case (_, _, "connections"):
                guard let index = connections.firstIndex(where: { $0.id == parts[1] }) else {
                    return problem(404, "not_found", "no such connection")
                }
                return connection(index, method: method, sub: Array(parts.dropFirst(2)), value: value, body: body)
            case ("GET", 1, "providers"):
                return json(200, ["providers": providers.map(providerJSON)])
            case (_, _, "providers") where parts.count >= 3:
                guard let index = providers.firstIndex(where: { $0.code == parts[1] }) else {
                    return problem(404, "not_found", "no such provider")
                }
                return provider(index, method: method, sub: parts.dropFirst(2).joined(separator: "/"), body: body)
            case ("GET", 1, "schedules"):
                let only = value("connection")
                return json(200, ["schedules": schedules.filter { only == nil || $0.connection == only }.map(\.json)])
            case ("PATCH", 2, "schedules"):
                guard let index = schedules.firstIndex(where: { $0.id == parts[1] }) else {
                    return problem(404, "not_found", "no such schedule")
                }
                if let interval = body["interval_seconds"] as? Int {
                    guard interval >= 60 else { return problem(422, "validation_failed", "invalid schedule", errors: [("/interval_seconds", "must be at least 60")]) }
                    schedules[index].interval = interval
                }
                if let enabled = body["enabled"] as? Bool { schedules[index].enabled = enabled }
                return json(200, schedules[index].json)
            case ("GET", 1, "source-devices"):
                let records = (value("include") ?? "").split(separator: ",").contains("records")
                return json(200, ["devices": devices.map { $0.json(records: records) }, "device_types": deviceTypes])
            case (_, _, "source-devices") where parts.count >= 2:
                return device(method: method, id: parts[1], merge: parts.count == 3 && parts[2] == "merge", body: body)
            default:
                return problem(404, "not_found", "not stubbed in the fake server")
            }
        }

        // MARK: Connections

        private mutating func connection(_ index: Int, method: String, sub: [String], value: (String) -> String?, body: [String: Any]) -> Reply {
            let c = connections[index]
            switch (method, sub.first, sub.count) {
            case ("GET", nil, _):
                return json(200, c.json)
            case ("PATCH", nil, _):
                guard let status = body["status"] as? String, ["active", "paused"].contains(status) else {
                    return problem(422, "validation_failed", "invalid connection update", errors: [("/status", "must be active or paused")])
                }
                guard ["active", "degraded", "paused"].contains(c.status) else { return problem(409, "conflict", "the connection is \(c.status)") }
                connections[index].status = status
                connections[index].health = status == "paused" ? "paused" : "ok"
                return json(200, connections[index].json)
            case ("DELETE", nil, _):
                switch value("data") {
                case "keep":
                    connections[index].status = "disabled"
                    connections[index].health = "disabled"
                case "delete":
                    connections.remove(at: index)
                    runs[c.id] = nil
                    backfills.removeAll { $0.connection == c.id }
                    schedules.removeAll { $0.connection == c.id }
                    devices.removeAll { $0.connection == c.id }
                default:
                    return problem(422, "validation_failed", "invalid request", errors: [("/data", "must be keep or delete")])
                }
                return Reply(status: 204)
            case ("POST", "auth", 2) where sub[1] == "begin":
                return begin(provider: c.provider, connection: c.id, body: body)
            case ("POST", "sync", 1):
                guard ["active", "degraded", "needs_reauth"].contains(c.status) else { return problem(409, "conflict", "the connection is \(c.status)") }
                let jobs = (streams[c.provider] ?? []).map { stream -> [String: Any] in
                    next += 1
                    let now = SourcesFixture.time(.now)
                    return [
                        "id": String(format: "55555555-5555-4555-8555-%012d", next), "kind": "sync", "status": "queued",
                        "connection_id": c.id, "priority": 0, "attempts": 0, "max_attempts": 5,
                        "payload": ["stream": stream, "mode": "manual"], "run_at": now, "created_at": now,
                        "started_at": NSNull(), "finished_at": NSNull(),
                    ]
                }
                if c.status != "needs_reauth" {
                    next += 1
                    runs[c.id, default: []].insert(Run(id: String(next), started: .now, outcome: "succeeded"), at: 0)
                    connections[index].lastSuccess = .now
                }
                return json(202, ["jobs": jobs])
            case ("GET", "runs", 1):
                let all = runs[c.id] ?? []
                let limit = max(1, value("limit").flatMap(Int.init) ?? 50)
                let offset = value("cursor").flatMap { $0.hasPrefix("runs-") ? Int($0.dropFirst(5)) : nil } ?? 0
                guard offset <= all.count else { return problem(422, "validation_failed", "invalid cursor", errors: [("/cursor", "invalid or expired cursor")]) }
                let end = min(offset + limit, all.count)
                var page: [String: Any] = ["runs": all[offset..<end].map(\.json), "has_more": end < all.count]
                if end < all.count { page["next_cursor"] = "runs-\(end)" }
                return json(200, page)
            case ("GET", "streams", 1):
                return json(200, ["streams": (streams[c.provider] ?? []).map { stream(c, $0) }])
            case ("POST", "streams", 3) where sub[2] == "reset-cursor":
                guard c.mode == "in_process" else { return problem(409, "conflict", "only in-process connections have a cursor to reset") }
                guard (streams[c.provider] ?? []).contains(sub[1]) else { return problem(404, "not_found", "no such stream") }
                cursors["\(c.id)/\(sub[1])"] = false
                return json(200, stream(c, sub[1]))
            case ("GET", "backfills", 1):
                for i in backfills.indices where backfills[i].connection == c.id { backfills[i].advance() }
                return json(200, ["backfills": backfills.filter { $0.connection == c.id }.map { $0.json(units: false) }])
            case ("POST", "backfills", 1):
                return createBackfill(c, body: body)
            case (_, "backfills", _) where sub.count >= 2:
                guard let b = backfills.firstIndex(where: { $0.id == sub[1] && $0.connection == c.id }) else {
                    return problem(404, "not_found", "no such backfill")
                }
                return backfill(b, method: method, action: sub.count == 3 ? sub[2] : nil, body: body)
            default:
                return problem(404, "not_found", "not stubbed in the fake server")
            }
        }

        private func stream(_ c: Connection, _ name: String) -> [String: Any] {
            let health = ["needs_reauth", "paused", "disabled"].contains(c.health) ? c.health : "ok"
            let cursor = cursors["\(c.id)/\(name)"] ?? false
            return [
                "name": name, "health": health, "health_reason": (c.health == "needs_reauth" ? c.healthReason : nil) ?? NSNull(),
                "status": "ok", "status_reason": NSNull(), "has_cursor": cursor,
                "high_watermark": c.lastSuccess.map(SourcesFixture.time) ?? NSNull(),
                "updated_at": cursor ? SourcesFixture.time(c.lastSuccess ?? c.created) : NSNull(),
                "schedules": schedules.filter { $0.connection == c.id && $0.stream == name }.map(\.json),
            ]
        }

        private mutating func createBackfill(_ c: Connection, body: [String: Any]) -> Reply {
            guard c.mode != "push" else { return problem(409, "conflict", "push sources upload their own history") }
            guard let stream = body["stream"] as? String, (streams[c.provider] ?? []).contains(stream) else {
                return problem(422, "validation_failed", "invalid backfill", errors: [("/stream", "unknown stream")])
            }
            let parse = { (key: String) in (body[key] as? String).flatMap { try? Date($0, strategy: .iso8601) } }
            guard let start = parse("start") else {
                return problem(422, "validation_failed", "invalid backfill", errors: [("/start", "must be an RFC 3339 time")])
            }
            let end = parse("end") ?? .now
            guard end > start else { return problem(422, "validation_failed", "invalid backfill", errors: [("/end", "must be after the start")]) }
            next += 1
            let days = (body["daily_limit"] as? Int) != nil ? 1 : 30
            let backfill = Backfill(id: String(format: "44444444-4444-4444-8444-%012d", next), connection: c.id, stream: stream,
                                    status: "running", created: .now, dailyLimit: body["daily_limit"] as? Int,
                                    units: Backfill.units(from: start, to: end, days: days) { _ in "pending" })
            backfills.insert(backfill, at: 0)
            return json(202, backfill.json(units: false))
        }

        private mutating func backfill(_ index: Int, method: String, action: String?, body: [String: Any]) -> Reply {
            switch (method, action) {
            case ("GET", nil):
                return json(200, backfills[index].json(units: true))
            case ("POST", "cancel"):
                guard ["running", "failed"].contains(backfills[index].status) else { return problem(409, "conflict", "backfill is not running") }
                backfills[index].status = "cancelled"
                return json(200, backfills[index].json(units: false))
            case ("POST", "retry"):
                let only = (body["unit_start"] as? String).flatMap { try? Date($0, strategy: .iso8601) }
                let failed = backfills[index].units.indices.filter {
                    let unit = backfills[index].units[$0]
                    return unit.status == "failed" && (only == nil || abs(unit.start.timeIntervalSince(only!)) < 1)
                }
                guard !failed.isEmpty, backfills[index].status != "cancelled" else {
                    return problem(409, "conflict", "no failed or unfinished unit to retry")
                }
                for unit in failed {
                    backfills[index].units[unit].status = "pending"
                    backfills[index].units[unit].error = nil
                }
                backfills[index].status = "running"
                return json(200, backfills[index].json(units: false))
            default:
                return problem(404, "not_found", "not stubbed in the fake server")
            }
        }

        // MARK: Devices

        private mutating func device(method: String, id: String, merge: Bool, body: [String: Any]) -> Reply {
            guard let index = devices.firstIndex(where: { $0.id == id }) else { return problem(404, "not_found", "no such device") }
            if let into = devices[index].mergedInto { return problem(409, "conflict", "\(id) is merged into \(into); use that device") }
            switch (method, merge) {
            case ("PATCH", false):
                if let type = body["device_type"] as? String {
                    guard deviceTypes.contains(type) else { return problem(422, "validation_failed", "invalid device", errors: [("/device_type", "unknown device type")]) }
                    devices[index].type = type
                } else if body.keys.contains("device_type") {
                    devices[index].type = nil
                }
                if let name = body["name"] as? String {
                    guard (1...100).contains(name.count) else { return problem(422, "validation_failed", "invalid device", errors: [("/name", "must be 1 to 100 characters")]) }
                    devices[index].name = name
                } else if body.keys.contains("name") {
                    devices[index].name = nil
                }
                return Reply(status: 204)
            case ("POST", true):
                guard let into = body["into"] as? String, let target = devices.firstIndex(where: { $0.id == into }) else {
                    return problem(404, "not_found", "no such device")
                }
                guard target != index, devices[target].provider == devices[index].provider else {
                    return problem(422, "validation_failed", "invalid merge", errors: [("/into", "must be another device of the same provider")])
                }
                guard devices[target].mergedInto == nil else { return problem(409, "conflict", "\(into) is merged") }
                let moved = devices[index].records
                devices[target].records.merge(moved) { $0 + $1 }
                devices[index].mergedInto = into
                devices[index].records = [:]
                let all = ["measurements", "groups", "sleep_sessions", "workouts", "events"]
                return json(200, ["moved": Dictionary(uniqueKeysWithValues: all.map { ($0, moved[$0] ?? 0) })])
            default:
                return problem(404, "not_found", "not stubbed in the fake server")
            }
        }

        // MARK: Providers and setup (internal/api/setup.go)

        private func setupState(_ p: Provider) -> String {
            if !p.available { return "needs_sidecar" }
            if connections.contains(where: { $0.provider == p.code && $0.status != "disabled" }) { return "connected" }
            if let app = p.app, !app.set { return "needs_app_credentials" }
            return "ready"
        }

        private func providerJSON(_ p: Provider) -> [String: Any] {
            var out: [String: Any] = [
                "code": p.code, "name": p.name, "official": p.official, "auth_kind": p.authKind ?? NSNull(), "remote": p.remote,
                "available": p.available, "setup_state": setupState(p), "callback_url": p.callback ?? NSNull(),
                "problems": p.problems, "sidecar": p.sidecar ?? NSNull(),
                "app_credentials": p.app.map { app -> [String: Any] in
                    ["set": app.set, "managed_by_environment": app.environment, "client_id": app.clientID ?? NSNull(),
                     "updated_at": app.set && !app.environment ? SourcesFixture.time(.now) : NSNull()]
                } ?? NSNull(),
                "connections": connections.filter { $0.provider == p.code && $0.status != "disabled" }.count,
            ]
            if let upstream = p.upstream { out["upstream"] = upstream }
            return out
        }

        private mutating func provider(_ index: Int, method: String, sub: String, body: [String: Any]) -> Reply {
            let p = providers[index]
            switch (method, sub) {
            case ("PUT", "app-credentials"):
                guard let app = p.app else { return problem(404, "not_found", "\(p.code) needs no app credentials") }
                guard !app.environment else { return problem(409, "conflict", "set by the environment") }
                let id = (body["client_id"] as? String) ?? "", secret = (body["client_secret"] as? String) ?? ""
                var errors: [(String, String)] = []
                if id.isEmpty { errors.append(("/client_id", "must not be empty")) }
                if secret.isEmpty { errors.append(("/client_secret", "must not be empty")) }
                guard errors.isEmpty else { return problem(422, "validation_failed", "invalid app credentials", errors: errors) }
                providers[index].app = (true, false, id)
                appSecret = secret
                return json(200, providerJSON(providers[index]))
            case ("POST", "app-credentials/verify"):
                guard p.app?.set == true else { return problem(404, "not_found", "no app credentials are set") }
                if p.app?.environment == true || appSecret == SourcesFixture.appSecret {
                    return json(200, ["result": "valid", "message": "Withings accepted the client id and secret."])
                }
                return json(200, ["result": "invalid", "message": "Withings refused the client id and secret. Copy both again from its developer dashboard."])
            case ("POST", "probe"):
                if !p.available, probesUntilUp <= 0 {
                    providers[index].available = true
                    providers[index].authKind = "interactive_mfa"
                    providers[index].problems = []
                    providers[index].upstream = ["package": "@dofek/whoop", "version": "0.1.65", "source_url": "https://github.com/Asherlc/dofek"]
                    providers[index].sidecar?["enable"] = [[String: String]]()
                }
                probesUntilUp -= 1
                return json(200, providerJSON(providers[index]))
            case ("POST", "auth/begin"):
                return begin(provider: p.code, connection: nil, body: body)
            case ("POST", "auth/continue"):
                return continueAuth(p, body: body)
            default:
                return problem(404, "not_found", "not stubbed in the fake server")
            }
        }

        /// The secret last stored for the Withings app (only its check reads it).
        private var appSecret = ""

        // MARK: Auth (internal/connectors/auth.go)

        private mutating func begin(provider code: String, connection: String?, body: [String: Any]) -> Reply {
            guard let p = providers.first(where: { $0.code == code }), p.available else {
                return problem(503, "unavailable", "authorization is not available for this provider")
            }
            if p.authKind == "interactive_mfa" { return json(200, prompt(provider: code, step: "login", connection: connection)) }
            if let app = p.app, !app.set { return problem(503, "unavailable", "the app credentials of \(code) are not set") }
            next += 1
            guard body["return"] as? String == "app" else {
                // A browser return would go to the provider and back to the panel, never to the app.
                return json(200, ["redirect_url": "https://provider.example.test/authorize?state=browser-\(next)"])
            }
            let ticket = "ticket-\(next)"
            tickets[ticket] = Pending(provider: code, step: "redirect", connection: connection)
            return json(200, ["redirect_url": "https://\(p.callback.flatMap { URL(string: $0)?.host() } ?? "fake.vitamux.test")/oauth/\(code)/start?ticket=\(ticket)"])
        }

        private mutating func prompt(provider: String, step: String, connection: String?) -> [String: Any] {
            next += 1
            let state = "state-\(next)"
            pending[state] = Pending(provider: provider, step: step, connection: connection)
            let name = providers.first { $0.code == provider }?.name ?? provider
            let fields: [[String: String]] = step == "login"
                ? [["name": "email", "label": "Email", "kind": "text"], ["name": "password", "label": "Password", "kind": "password"]]
                : [["name": "code", "label": "Verification code", "kind": "code"]]
            let message = step == "login" ? "Sign in to \(name)." : "Enter the code \(name) sent you."
            return ["state": state, "prompt": ["message": message, "fields": fields]]
        }

        private mutating func continueAuth(_ p: Provider, body: [String: Any]) -> Reply {
            guard let state = body["state"] as? String, let values = body["values"] as? [String: String] else {
                return problem(422, "validation_failed", "invalid request", errors: [("/state", "required")])
            }
            guard let step = pending.removeValue(forKey: state), step.provider == p.code else {
                return problem(422, "validation_failed", "invalid authorization step",
                               errors: [("/state", "the authorization step expired or was already used")])
            }
            guard p.available else { return problem(503, "unavailable", "the sidecar is unavailable") }
            if step.step == "login" {
                if values["password"] == Login.limited {
                    var reply = problem(429, "rate_limited", "the provider is limiting sign-ins")
                    reply.headers["Retry-After"] = "120"
                    return reply
                }
                guard values["email"] == Login.email, values["password"] == Login.password else {
                    return problem(422, "auth_rejected", "the connector did not accept the sign-in details")
                }
                return json(200, prompt(provider: p.code, step: "code", connection: step.connection))
            }
            if values["code"] == Login.gone { return problem(503, "unavailable", "the sidecar did not answer") }
            guard values["code"] == Login.code else { return problem(422, "auth_rejected", "the code was not accepted") }
            return json(200, ["connection_id": connect(p.code, connection: step.connection)])
        }

        /// What a finished authorization does: reauthorizes the connection, revives the provider's
        /// connection (the same account again), or creates one; a new unofficial one starts paused.
        private mutating func connect(_ code: String, connection: String?) -> String {
            let p = providers.first { $0.code == code }
            if let index = connections.firstIndex(where: { $0.id == connection || (connection == nil && $0.provider == code) }) {
                connections[index].healthy()
                return connections[index].id
            }
            next += 1
            let id = "conn_" + String(format: "%032x", next)
            let official = p?.official ?? true
            connections.append(Connection(
                id: id, provider: code, mode: p?.remote == true ? "remote" : "in_process", status: official ? "active" : "paused",
                official: official, upstream: p?.upstream, health: official ? "ok" : "paused", failures: 0, created: .now
            ))
            for stream in streams[code] ?? [] {
                for (mode, interval, lookback) in [("incremental", 3_600, 0), ("correction", 86_400, 7 * 86_400)] {
                    next += 1
                    schedules.append(Schedule(id: String(format: "66666666-6666-4666-8666-%012d", next), connection: id,
                                              stream: stream, mode: mode, interval: interval, lookback: lookback))
                }
            }
            return id
        }

        mutating func authorize(_ url: URL) -> String {
            let parts = url.path.split(separator: "/").map(String.init)
            let provider = parts.count == 3 && parts[0] == "oauth" && parts[2] == "start" ? parts[1] : ""
            let ticket = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "ticket" }?.value ?? ""
            guard let pending = tickets.removeValue(forKey: ticket), pending.provider == provider else {
                return "vitamux://connections?auth_error=invalid_state&provider=\(provider.isEmpty ? "unknown" : provider)"
            }
            _ = connect(provider, connection: pending.connection)
            return "vitamux://connections?connected=\(provider)"
        }

        // MARK: Responses

        private func json(_ status: Int, _ object: [String: Any]) -> Reply {
            Reply(status: status, body: try! JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]))
        }

        private func problem(_ status: Int, _ code: String, _ detail: String, errors: [(String, String)] = []) -> Reply {
            let titles = ["validation_failed": "Validation failed", "not_found": "Not found", "conflict": "Conflict",
                          "unavailable": "Service unavailable", "rate_limited": "Rate limited", "auth_rejected": "Sign-in refused"]
            var object: [String: Any] = [
                "type": "urn:vitamux:problem:\(code)", "title": titles[code] ?? code, "status": status, "code": code,
                "detail": detail, "request_id": "req-fake",
            ]
            if !errors.isEmpty { object["errors"] = errors.map { ["pointer": $0.0, "detail": $0.1] } }
            var reply = json(status, object)
            reply.headers = ["Content-Type": "application/problem+json"]
            return reply
        }
    }
}
#endif
