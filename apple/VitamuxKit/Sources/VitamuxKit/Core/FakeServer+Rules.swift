#if DEBUG
import Foundation
import Synchronization

// Rules, the rule lens and the builder (J22.10) for the fake server, mirroring
// web/e2e/rules-fake.ts and rule-lens-fake.ts and the server's handlers (internal/api/rules.go):
// built-in and default rules, owner versions (the first save of a metric with a built-in copies
// it as version 1), activation and the draft preview. The builder's one-click selectors read
// /origins (FakeServer+Settings.swift), /source-devices and /providers (FakeServer+Sources.swift),
// so the rule, Settings and Sources screens see the same data. A sum without its acknowledgement
// is 409 rule_warning_unacknowledged, a bad group id or a min_coverage above 0.9 is 422 with a field
// pointer. GET /metrics/{code} and GET /coverage are Explore's (FakeServer+Explore.swift).
// Every value is synthetic.
extension FakeServer {
    /// Answers a rules endpoint, or nil for a request this file does not handle.
    static func rules(method: String, url: URL, body: Data) -> Reply? {
        let host = url.host() ?? ""
        let parts = url.path.split(separator: "/").map(String.init)
        return RulesFixture.states.withLock { states in
            var state = states[host] ?? RulesFixture.State()
            defer { states[host] = state }
            switch (method, url.path) {
            case ("GET", "/api/v1/rules"):
                return RulesFixture.json(200, ["rules": state.activeSet.map(\.json)])
            case ("POST", "/api/v1/resolution/preview"):
                return state.preview(body)
            case ("GET", _) where parts.count == 5 && parts[2] == "rules" && parts[4] == "versions":
                return state.versions(parts[3])
            case ("POST", _) where parts.count == 5 && parts[2] == "rules" && parts[4] == "versions":
                return state.create(parts[3], body)
            case ("POST", _) where parts.count == 5 && parts[2] == "rules" && parts[4] == "activate":
                return state.activate(parts[3], body)
            default:
                return nil
            }
        }
    }
}

/// The synthetic rules and the owner's versions, kept per fake host so parallel unit tests never
/// share them.
struct RulesFixture {
    static let states = Mutex<[String: State]>([:])

    struct Version {
        var ref: String
        var metric: String
        var version: Int
        var builtin: Bool
        var isDefault = false
        var active: Bool
        var spec: [String: Any]
        var basedOn: String?
        var note: String?
        var createdAt: String?
        var reason: String?

        var json: [String: Any] {
            var out: [String: Any] = [
                "ref": ref, "metric": metric, "version": version, "builtin": builtin, "default": isDefault, "active": active,
                "spec": spec, "based_on": basedOn ?? NSNull(), "note": note ?? NSNull(),
                "created_by": builtin ? NSNull() : "session:\(FakeServer.Owner.username)", "created_at": createdAt ?? NSNull(),
            ]
            if let reason { out["reason"] = reason }
            return out
        }
    }

    // MARK: Built-in and default rules (the groups match Explore's sources)

    static func spec(_ metric: String, window: [String: Any], groups: [(String, [[String: Any]])], op: String, extra: [String: Any] = [:]) -> [String: Any] {
        var out: [String: Any] = [
            "schema": "vitamux.rule/1", "metric": metric, "window": window,
            "groups": groups.map { ["id": $0.0, "match": $0.1] }, "strategy": ["op": op],
        ]
        out.merge(extra) { $1 }
        return out
    }

    static var appleWatch: [String: Any] { ["provider": "apple_health", "device_type": "watch", "relayed": false] }

    static var builtins: [Version] { [
        builtin(spec("heart_rate_resting", window: ["kind": "local_day"], groups: [
            ("whoop", [["provider": "whoop"]]), ("garmin", [["provider": "garmin"]]), ("apple_watch", [appleWatch]),
        ], op: "first_available", extra: ["exclude": [["provider": "apple_health", "relayed": true]], "quality": ["max_staleness": "36h"]]),
        reason: "Selection only (definitions differ); a suggested order of the connected sources."),
        builtin(spec("heart_rate", window: ["kind": "bucket", "size": "5m"], groups: [
            ("garmin", [["provider": "garmin"]]), ("apple_watch", [appleWatch]),
        ], op: "mean_across_sources", extra: ["quality": ["plausible_range": [25, 230], "exclude_flags": ["manual_entry"]]]),
        reason: "Both wrist sources sample often; each counts once per bucket."),
        builtin(spec("steps", window: ["kind": "local_day"], groups: [
            ("apple_watch", [["provider": "apple_health", "device_type": "watch"]]), ("garmin", [["provider": "garmin"]]),
        ], op: "first_available", extra: ["compose": ["from": "hour", "op": "first_available"]]),
        reason: "Watches first; hours resolve separately."),
        builtin(spec("sleep", window: ["kind": "local_night"], groups: [
            ("garmin", [["provider": "garmin"]]), ("apple_watch", [appleWatch]),
        ], op: "event_priority"), reason: "Whole nights from one source; stages are never spliced."),
    ] }

    static func builtin(_ spec: [String: Any], reason: String) -> Version {
        let metric = spec["metric"] as! String
        return Version(ref: "builtin:\(metric):1", metric: metric, version: 1, builtin: true, active: true, spec: spec, reason: reason)
    }

    /// The default rule of a code without a built-in: the source order, then the device ladder.
    static var defaults: [Version] { [
        Version(ref: "default:spo2:0a1b2c3d", metric: "spo2", version: 1, builtin: true, isDefault: true, active: true,
                spec: spec("spo2", window: ["kind": "local_day"], groups: [
                    ("apple_watch", [appleWatch]), ("manual", [["entry": "manual"]]),
                ], op: "first_available"),
                reason: "No neutral order for this metric: it uses the default rule."),
    ] }

    struct State {
        /// Owner versions per metric, oldest first.
        var owned: [String: [Version]] = [:]
        var nextDay = 1

        var activeSet: [Version] {
            let shipped = RulesFixture.builtins + RulesFixture.defaults
            let others = owned.keys.sorted().filter { m in !shipped.contains { $0.metric == m } }
            return shipped.map { b in owned[b.metric]?.first(where: \.active) ?? b } + others.compactMap { owned[$0]?.first(where: \.active) }
        }

        func versions(_ metric: String) -> Reply {
            if let own = owned[metric], !own.isEmpty { return RulesFixture.json(200, ["versions": own.reversed().map(\.json)]) }
            if let v = (RulesFixture.builtins + RulesFixture.defaults).first(where: { $0.metric == metric }) {
                return RulesFixture.json(200, ["versions": [v.json]])
            }
            guard RulesFixture.known(metric) else { return RulesFixture.problem(404, "not_found", "no such metric") }
            return RulesFixture.json(200, ["versions": [Any]()])
        }

        mutating func create(_ metric: String, _ body: Data) -> Reply {
            guard let input = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any], let spec = input["spec"] as? [String: Any] else {
                return RulesFixture.problem(422, "validation_failed", "request body is not valid JSON")
            }
            if spec["metric"] as? String != metric {
                return RulesFixture.problem(422, "validation_failed", "invalid rule", errors: [("/spec/metric", "must be the metric in the path")])
            }
            if let problem = RulesFixture.validate(spec) { return problem }
            var own = owned[metric] ?? []
            if own.isEmpty, let b = (RulesFixture.builtins + RulesFixture.defaults).first(where: { $0.metric == metric }) {
                // The rule in effect is copied as version 1 and stays in effect.
                var copy = owner(metric, 1, b.spec, basedOn: b.ref, note: nil)
                copy.active = true
                own.append(copy)
            }
            var v = owner(metric, own.count + 1, spec, basedOn: nil, note: (input["note"] as? String).flatMap { $0.isEmpty ? nil : $0 })
            if input["activate"] as? Bool == true {
                for i in own.indices { own[i].active = false }
                v.active = true
            }
            own.append(v)
            owned[metric] = own
            return RulesFixture.json(201, v.json)
        }

        mutating func activate(_ metric: String, _ body: Data) -> Reply {
            let version = ((try? JSONSerialization.jsonObject(with: body)) as? [String: Any])?["version"] as? Int
            guard var own = owned[metric], let i = own.firstIndex(where: { $0.version == version }) else {
                return RulesFixture.problem(404, "not_found", "no such rule version")
            }
            for k in own.indices { own[k].active = k == i }
            owned[metric] = own
            return RulesFixture.json(200, own[i].json)
        }

        private mutating func owner(_ metric: String, _ n: Int, _ spec: [String: Any], basedOn: String?, note: String?) -> Version {
            defer { nextDay += 1 }
            return Version(ref: "rule:\(metric):\(n)", metric: metric, version: n, builtin: false, active: false, spec: spec,
                           basedOn: basedOn, note: note, createdAt: String(format: "2026-01-%02dT09:00:00Z", min(nextDay, 28)))
        }

        /// The draft beside the rule in effect, one day per local date: the same values when the draft
        /// equals the rule, otherwise the 3rd, 6th and 10th days change source and value.
        func preview(_ body: Data) -> Reply {
            guard let input = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any],
                  let draft = input["spec"] as? [String: Any], let metric = draft["metric"] as? String,
                  let start = (input["start_date"] as? String).flatMap(LocalDate.init),
                  let end = (input["end_date"] as? String).flatMap(LocalDate.init), start <= end
            else { return RulesFixture.problem(422, "validation_failed", "spec, start_date and end_date are required") }
            if let problem = RulesFixture.validate(draft) { return problem }
            let active = activeSet.first { $0.metric == metric }
            let same = active.map { NSDictionary(dictionary: $0.spec).isEqual(to: draft) } ?? false
            let unit = ExploreFixture.catalogue(metric)?["unit"] as? String ?? ""
            let base = ExploreFixture.spec(metric)?.base ?? 60
            let firstGroup = { (spec: [String: Any]?) in ((spec?["groups"] as? [[String: Any]])?.first?["id"] as? String) ?? "" }
            let activeGroup = firstGroup(active?.spec), draftGroup = firstGroup(draft)
            var days: [[String: Any]] = []
            var d = start
            var i = 0
            while d <= end, i < 366 {
                let changed = !same && [2, 5, 9].contains(i)
                let value = { (draft: Bool) -> [String: Any] in
                    let moved = draft && changed
                    let group = moved ? draftGroup : activeGroup
                    let number = moved ? base + 1.5 : base
                    return [
                        "status": moved ? "calculated" : "direct", "value": number, "unit": unit,
                        "inputs": [["group": group, "status": "used", "selected": true]],
                        "explanation": "\(RulesFixture.label(group)): \(ExploreFixture.text(number, unit: unit)).",
                    ]
                }
                days.append(["local_date": d.description, "active": value(false), "draft": value(true)])
                d = d.adding(days: 1)
                i += 1
            }
            return RulesFixture.json(200, [
                "metric": metric, "timezone": ExploreFixture.timezone, "window": "local_day",
                "draft_rule": ["ref": "draft:\(metric)", "version": 0, "strategy": (draft["strategy"] as? [String: Any])?["op"] ?? ""],
                "active_rule": active.map { ["ref": $0.ref, "version": $0.version] as [String: Any] } ?? NSNull(),
                "days": days,
            ])
        }
    }

    /// The server's checks this fake needs: the sum acknowledgement, group ids, and a ceiling on
    /// min_coverage to test that field errors reach their control.
    static func validate(_ spec: [String: Any]) -> Reply? {
        let strategy = spec["strategy"] as? [String: Any]
        let within = spec["within_source"] as? [String: Any]
        let sums = strategy?["op"] as? String == "sum_across_sources" || within?["intra_group"] as? String == "sum"
        if sums, !((spec["acknowledged_warnings"] as? [String]) ?? []).contains("cross_source_sum_duplicate_risk") {
            return problem(409, "rule_warning_unacknowledged",
                           "the rule sums across sources: acknowledge cross_source_sum_duplicate_risk in acknowledged_warnings",
                           errors: [("/spec/acknowledged_warnings", "sum_across_sources must be acknowledged")])
        }
        let groups = (spec["groups"] as? [[String: Any]]) ?? []
        let bad = groups.enumerated().compactMap { i, g -> (String, String)? in
            let id = g["id"] as? String ?? ""
            return id.wholeMatch(of: /[a-z][a-z0-9_]{0,31}/) == nil
                ? ("/spec/groups/\(i)/id", "must start with a lowercase letter and use only a-z, 0-9 and _") : nil
        }
        if !bad.isEmpty { return problem(422, "validation_failed", "invalid rule", errors: bad) }
        if let coverage = (spec["quality"] as? [String: Any])?["min_coverage"] as? Double, coverage > 0.9 {
            return problem(422, "validation_failed", "invalid rule", errors: [("/spec/quality/min_coverage", "must be at most 0.9")])
        }
        return nil
    }

    static func known(_ metric: String) -> Bool {
        ["sleep", "blood_pressure"].contains(metric) || ExploreFixture.catalogue(metric) != nil
    }

    static func label(_ group: String) -> String {
        ["apple_watch": "Apple Watch", "garmin": "Garmin", "whoop": "WHOOP", "withings": "Withings"][group] ?? group
    }

    static func json(_ status: Int, _ object: [String: Any]) -> Reply {
        ExploreFixture.json(status, object)
    }

    static func problem(_ status: Int, _ code: String, _ detail: String, errors: [(String, String)] = []) -> Reply {
        ExploreFixture.problem(status, code, detail, errors: errors)
    }
}
#endif
