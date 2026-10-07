#if DEBUG
import Foundation

// The specialised views (J22.9) for the fake server, mirroring web/e2e/views-fake.ts: sleep
// sessions and resolved nights, blood-pressure readings, body-composition weigh-ins, resolved
// workouts, health events and confirmed lab results. Every value is synthetic and placed relative
// to today, so the range presets always find data and stepping far enough back finds none.
//
// Paged lists answer at most `pageSize` rows a page, whatever `limit` asks, so every client walks
// the cursor. Record ids are digits, as the shared provenance fixture expects.
//
// Scenarios:
// - Sleep: 60 nights ending today, none on the gap night (9 nights ago). Last night has three
//   sources (the selected watch with stages, a band with stages that is in the rule, and an
//   excluded relayed copy without stages); the night before has the band too and a nap.
// - Blood pressure: a morning reading every day for 90 days, a second one ten minutes after
//   today's (one session), an evening reading every fifth day and one manual entry.
// - Body composition: a scale weigh-in every third day for 120 days, and a weight-only reading
//   from a second source every seventh day.
// - Workouts: 70 days; today's run was recorded by three sources.
// - Events: three types over 60 days; lab results for `ldl_c` in two printed units, `creatinine`
//   and an unknown analyte addressed as `label:Synthetic marker`.
extension FakeServer {
    /// Answers a specialised-view endpoint, or nil for a request this file does not handle.
    static func specialised(method: String, url: URL) -> Reply? {
        guard method == "GET" else { return nil }
        let fixture = SpecialisedFixture(query: .init(url))
        switch url.path {
        case "/api/v1/sleep": return fixture.sleep()
        case "/api/v1/resolved/sleep": return fixture.resolvedSleep()
        case "/api/v1/blood-pressure": return fixture.bloodPressure()
        case "/api/v1/groups": return fixture.groups()
        case "/api/v1/resolved/workouts": return fixture.resolvedWorkouts()
        case "/api/v1/events": return fixture.events()
        case "/api/v1/lab-results": return fixture.labResults()
        default: return nil
        }
    }
}

struct SpecialisedFixture {
    typealias JSON = [String: Any]

    static let timezone = FakeServer.timeZone.identifier
    static let zone = FakeServer.timeZone
    static let pageSize = 25
    static let sleepNights = 60
    static let gapNight = 9

    let query: Query
    let today = LocalDate.today(in: SpecialisedFixture.zone)

    init(query: Query) {
        self.query = query
    }

    // MARK: Sleep

    struct Session {
        var id: String
        var sleepDate: LocalDate
        var start: Date
        var end: Date
        var nap = false
        var stages: [(stage: String, start: Date, end: Date)] = []
        var source: JSON

        func seconds(_ stage: String) -> Int {
            stages.filter { $0.stage == stage }.reduce(0) { $0 + Int($1.end.timeIntervalSince($1.start)) }
        }

        var asleep: Int {
            stages.isEmpty ? Int(end.timeIntervalSince(start)) - 1_500 : seconds("deep") + seconds("light") + seconds("rem")
        }
    }

    /// The stage plan of a night, `shift` minutes longer in the first stage so sources differ.
    static func stages(from start: Date, to end: Date, shift: Int) -> [(stage: String, start: Date, end: Date)] {
        let plan = [("light", 70 + shift), ("deep", 80), ("rem", 55), ("awake", 10), ("light", 120), ("rem", 50), ("light", 600)]
        var out: [(stage: String, start: Date, end: Date)] = []
        var t = start
        for (stage, minutes) in plan where t < end {
            let to = min(end, t.addingTimeInterval(TimeInterval(minutes * 60)))
            out.append((stage, t, to))
            t = to
        }
        return out
    }

    /// Sessions whose wake date is `date`: none on the gap night.
    func sessions(on date: LocalDate) -> [Session] {
        let back = today.number - date.number
        guard (0..<Self.sleepNights).contains(back), back != Self.gapNight else { return [] }
        let n = date.number
        let start = Self.at(date.adding(days: -1), minutes: 23 * 60 + (back * 7) % 30)
        let end = Self.at(date, minutes: 6 * 60 + 50 + (back * 13) % 60)
        var out = [Session(id: "71\(n)", sleepDate: date, start: start, end: end, stages: Self.stages(from: start, to: end, shift: 0),
                           source: Self.source("apple_health", key: 1, device: "watch"))]
        if back <= 1 {
            let bandStart = Self.at(date.adding(days: -1), minutes: 23 * 60 + 25), bandEnd = Self.at(date, minutes: 7 * 60 + 5)
            out.append(Session(id: "72\(n)", sleepDate: date, start: bandStart, end: bandEnd, stages: Self.stages(from: bandStart, to: bandEnd, shift: 15),
                               source: Self.source("whoop", key: 2, device: "band")))
        }
        if back == 0 {
            out.append(Session(id: "73\(n)", sleepDate: date, start: Self.at(date.adding(days: -1), minutes: 23 * 60 + 5), end: Self.at(date, minutes: 6 * 60 + 55),
                               source: Self.source("apple_health", key: 3, origin: "com.garmin.connect.mobile")))
        }
        if back == 1 {
            out.append(Session(id: "74\(n)", sleepDate: date, start: Self.at(date, minutes: 14 * 60 + 10), end: Self.at(date, minutes: 14 * 60 + 50), nap: true,
                               source: Self.source("apple_health", key: 4, device: "watch")))
        }
        return out
    }

    func sleep() -> Reply {
        guard let dates = query.dates() else { return Self.problem(422, "start_date and end_date are required here") }
        let staged = query.list("include").contains("stages")
        let rows = dates.flatMap(sessions(on:)).map { session -> JSON in
            let has = !session.stages.isEmpty
            let part = { (stage: String) -> Any in has ? session.seconds(stage) : NSNull() }
            var row: JSON = [
                "id": session.id, "start_at": Self.iso(session.start), "end_at": Self.iso(session.end),
                "tz_offset_min": Self.offset(session.start), "sleep_date": session.sleepDate.description, "is_nap": session.nap,
                "has_stages": has, "totals_basis": has ? "stages" : "provider", "asleep_s": session.asleep,
                "deep_s": part("deep"), "light_s": part("light"), "rem_s": part("rem"), "awake_s": part("awake"),
                "latency_s": session.nap ? NSNull() : 600, "source": session.source, "provenance": Self.provenance,
            ]
            if staged {
                row["stages"] = session.stages.map { ["stage": $0.stage, "start_at": Self.iso($0.start), "end_at": Self.iso($0.end)] }
            }
            return row
        }
        return Self.page(rows, key: "sleep", query: query)
    }

    func resolvedSleep() -> Reply {
        guard let dates = query.dates(), dates.count <= 366 else {
            return Self.problem(422, "start_date and end_date must span at most 366 dates")
        }
        let nights = dates.compactMap { date -> JSON? in
            let back = today.number - date.number
            guard (0..<Self.sleepNights).contains(back) else { return nil }
            let window: JSON = ["kind": "local_night", "local_date": date.description]
            let sessions = sessions(on: date).filter { !$0.nap }
            guard let main = sessions.first else {
                return ["local_date": date.description, "members": [JSON](),
                        "result": ["status": "no_data", "window": window, "explanation": "No source recorded this night."]]
            }
            let member = { (session: Session, group: Any, extra: JSON) -> JSON in
                [
                    "group": group, "rule_status": "used", "selected": false, "provider": session.source["provider"]!,
                    "values": ["sessions": 1, "sleep_in_bed": session.end.timeIntervalSince(session.start), "sleep_total": Double(session.asleep)],
                    "session_refs": [session.id],
                ].merging(extra) { $1 }
            }
            var members = [member(main, "apple_watch", ["selected": true, "device": ["id": Self.deviceID("a"), "type": "watch", "model": "Synthetic Watch"]])]
            for session in sessions.dropFirst() {
                if session.source["provider"] as? String == "whoop" {
                    members.append(member(session, "whoop", ["device": ["id": Self.deviceID("c"), "type": "band", "model": "Synthetic Band"]]))
                } else {
                    members.append(member(session, NSNull(), [
                        "rule_status": "excluded", "reason": "exclude: relayed=true",
                        "origin": ["key": "com.garmin.connect.mobile", "name": "Garmin Connect", "relayed": true, "relayed_provider": "garmin"],
                    ]))
                }
            }
            let value: JSON = [
                "sleep_total": main.asleep, "sleep_in_bed": Int(main.end.timeIntervalSince(main.start)), "sleep_deep": main.seconds("deep"),
                "sleep_light": main.seconds("light"), "sleep_rem": main.seconds("rem"), "sleep_awake": main.seconds("awake"), "sleep_latency": 600,
            ]
            return [
                "local_date": date.description, "episode": ["start": Self.iso(main.start), "end": Self.iso(main.end)], "members": members,
                "result": [
                    "status": "direct", "value": value, "window": window, "selected": "apple_watch",
                    "rule": ["ref": "builtin:sleep:1", "version": 1, "strategy": "event_priority"],
                    "explanation": "Used apple_watch, the first source in the rule with an episode on this night.",
                ],
            ]
        }
        return Self.json(200, ["timezone": Self.timezone, "nights": nights])
    }

    // MARK: Blood pressure

    func bloodPressure() -> Reply {
        let rows = (query.dates() ?? allDates(back: 90)).flatMap { date -> [JSON] in
            let back = today.number - date.number
            guard (0..<90).contains(back) else { return [] }
            let n = date.number
            let manual = back == 3
            let source = manual ? Self.source("manual", key: 5) : Self.source("withings", key: 1, device: "bp_monitor")
            let context: JSON = manual ? [:] : ["position": "seated", "arm": "left"]
            let reading = { (id: String, minutes: Int, sys: Int, dia: Int, pulse: Int) -> JSON in
                let at = Self.at(date, minutes: minutes)
                return [
                    "id": id, "measured_at": Self.iso(at), "tz_offset_min": Self.offset(at), "local_date": date.description,
                    "systolic": sys, "diastolic": dia, "pulse": pulse, "context": context, "source": source, "provenance": Self.provenance,
                ]
            }
            var out = [reading("81\(n)", 7 * 60 + 10, 118 + back % 7, 74 + back % 5, 58 + back % 6)]
            if back == 0 { out.append(reading("82\(n)", 7 * 60 + 20, 124, 80, 61)) }
            if back % 5 == 4 { out.append(reading("83\(n)", 19 * 60 + 30, 126, 82, 66)) }
            return out
        }
        return Self.page(rows, key: "readings", query: query)
    }

    // MARK: Body composition

    func groups() -> Reply {
        guard query.value("kind") ?? "body_composition" == "body_composition" else { return Self.page([], key: "groups", query: query) }
        let rows = (query.dates() ?? allDates(back: 120)).flatMap { date -> [JSON] in
            let back = today.number - date.number
            guard (0..<120).contains(back) else { return [] }
            let n = date.number
            let group = { (id: String, minutes: Int, source: JSON, parts: [(String, Double)]) -> JSON in
                let at = Self.at(date, minutes: minutes)
                return [
                    "id": id, "kind": "body_composition", "measured_at": Self.iso(at), "tz_offset_min": Self.offset(at),
                    "local_date": date.description, "context": JSON(), "source": source, "provenance": Self.provenance,
                    "components": parts.enumerated().map { i, part -> JSON in
                        ["id": "\(id)\(i)", "metric": part.0, "value": part.1, "unit": "kg", "source_value": NSNull(), "source_unit": NSNull(), "quality_flags": 0]
                    },
                ]
            }
            var out: [JSON] = []
            if back % 3 == 0 {
                let weight = Self.round(78.4 + Double(back) * 0.012 + Double(back % 4) * 0.1)
                let fat = Self.round(15 + Double(back % 5) * 0.1)
                out.append(group("61\(n)", 6 * 60 + 40, Self.source("withings", key: 1, device: "scale"), [
                    ("weight", weight), ("fat_mass", fat), ("fat_free_mass", Self.round(weight - fat)),
                    ("muscle_mass", Self.round(36 + Double(back % 3) * 0.1)), ("bone_mass", 3.1),
                ]))
            }
            if back % 7 == 1 {
                out.append(group("62\(n)", 8 * 60, Self.source("apple_health", key: 3), [("weight", Self.round(78.9 + Double(back % 3) * 0.1))]))
            }
            return out
        }
        return Self.page(rows, key: "groups", query: query)
    }

    // MARK: Workouts

    func resolvedWorkouts() -> Reply {
        guard let dates = query.dates(), dates.count <= 366 else {
            return Self.problem(422, "start_date and end_date must span at most 366 dates")
        }
        let clusters = dates.flatMap { date -> [JSON] in
            let back = today.number - date.number
            guard (0..<70).contains(back) else { return [] }
            let n = date.number
            let member = { (id: String, provider: String, group: Any, from: Int, to: Int, sport: String, extra: JSON) -> JSON in
                [
                    "id": id, "group": group, "rule_status": "used", "selected": false, "provider": provider,
                    "start_at": Self.iso(Self.at(date, minutes: from)), "end_at": Self.iso(Self.at(date, minutes: to)), "sport": sport,
                    "distance_m": 8_100, "energy_kcal": 410, "avg_hr_bpm": 148, "max_hr_bpm": 171,
                    "connection_id": Self.connection(provider == "garmin" ? 2 : provider == "whoop" ? 4 : 3),
                ].merging(extra) { $1 }
            }
            let cluster = { (from: Int, to: Int, sport: String, members: [JSON], explanation: String) -> JSON in
                [
                    "local_date": date.description, "start": Self.iso(Self.at(date, minutes: from)), "end": Self.iso(Self.at(date, minutes: to)),
                    "sport": sport, "selected": members.first { $0["selected"] as? Bool == true }?["id"] ?? NSNull(), "group": "garmin",
                    "explanation": explanation, "members": members,
                ]
            }
            let watch: JSON = ["device": ["id": Self.deviceID("a"), "type": "watch", "model": "Synthetic Watch"]]
            var out: [JSON] = []
            if back == 0 {
                out.append(cluster(7 * 60 + 10, 7 * 60 + 52, "running", [
                    member("911\(n)", "garmin", "garmin", 7 * 60 + 10, 7 * 60 + 52, "running", watch.merging(["selected": true]) { $1 }),
                    member("912\(n)", "whoop", "whoop", 7 * 60 + 9, 7 * 60 + 53, "running", ["energy_kcal": 430, "avg_hr_bpm": 149, "max_hr_bpm": 173, "distance_m": NSNull()]),
                    member("913\(n)", "apple_health", NSNull(), 7 * 60 + 11, 7 * 60 + 51, "running",
                           ["rule_status": "not_in_rule", "distance_m": 8_000, "energy_kcal": 395, "avg_hr_bpm": 147, "max_hr_bpm": 170]),
                ].map { $0.filter { !($0.value is NSNull) || $0.key == "group" } }, "Used garmin: the first source in the rule for this workout."))
                out.append(cluster(18 * 60, 18 * 60 + 45, "cycling", [
                    member("914\(n)", "garmin", "garmin", 18 * 60, 18 * 60 + 45, "cycling", watch.merging(["selected": true, "distance_m": 15_000, "avg_hr_bpm": 132]) { $1 }),
                ], "Only one source recorded this workout."))
            }
            if back % 3 == 2 {
                out.append(cluster(7 * 60, 7 * 60 + 40, "running", [
                    member("915\(n)", "garmin", "garmin", 7 * 60, 7 * 60 + 40, "running", watch.merging(["selected": true, "distance_m": 7_200]) { $1 }),
                ], "Only one source recorded this workout."))
            }
            if back % 5 == 3 {
                out.append(cluster(6 * 60 + 30, 7 * 60 + 15, "walking", [
                    member("916\(n)", "garmin", "garmin", 6 * 60 + 30, 7 * 60 + 15, "walking", watch.merging(["selected": true, "distance_m": 3_800, "avg_hr_bpm": 101, "energy_kcal": 190]) { $1 }),
                ], "Only one source recorded this workout."))
            }
            return out
        }
        return Self.json(200, ["timezone": Self.timezone, "rule": ["ref": "builtin:workouts:1", "version": 1, "strategy": "event_priority"], "workouts": clusters])
    }

    // MARK: Events

    /// Days back, minutes after midnight, duration in minutes, code and level.
    static let eventPlan: [(back: Int, minutes: Int, length: Int?, code: String, level: String?)] = [
        (30, 20 * 60 + 20, nil, "headphone_audio_alert", "seven_day_limit"),
        (20, 2 * 60 + 10, nil, "irregular_rhythm", nil),
        (12, 12 * 60 + 20, nil, "environment_audio_alert", "momentary_limit"),
        (5, 17 * 60 + 20, 30, "environment_audio_alert", "momentary_limit"),
        (0, 3 * 60 + 20, nil, "irregular_rhythm", nil),
    ]

    func events() -> Reply {
        let codes = query.list("code")
        let range = query.dates().map { Set($0.map(\.description)) }
        let rows = Self.eventPlan.enumerated().compactMap { i, plan -> JSON? in
            let date = today.adding(days: -plan.back)
            guard codes.isEmpty || codes.contains(plan.code), range?.contains(date.description) ?? true else { return nil }
            let start = Self.at(date, minutes: plan.minutes)
            return [
                "id": String(format: "00000000-0000-4000-8000-%012d", i + 1), "code": plan.code, "start_at": Self.iso(start),
                "end_at": plan.length.map { Self.iso(start.addingTimeInterval(TimeInterval($0 * 60))) } ?? NSNull(),
                "tz_offset_min": Self.offset(start), "local_date": date.description, "value": NSNull(), "level": plan.level ?? NSNull(),
                "context": JSON(), "quality_flags": 0, "source": Self.source("apple_health", key: 3, origin: "com.example.health"),
                "provenance": Self.provenance,
            ]
        }
        return Self.page(rows, key: "events", query: query)
    }

    // MARK: Lab results

    /// Days back, analyte, label, value text, number, comparator, unit, range text, low, high, flag.
    static let labPlan: [(back: Int, analyte: String?, label: String, text: String, value: Double?, comparator: String?, unit: String?, range: String?, low: Double?, high: Double?, flag: String?)] = [
        (720, "ldl_c", "LDL cholesterol", "3.4", 3.4, nil, "mmol/L", "1.0 - 3.0", 1.0, 3.0, "H"),
        (500, "ldl_c", "LDL cholesterol", "131", 131, nil, "mg/dL", "< 116", nil, 116, nil),
        (365, "ldl_c", "LDL cholesterol", "< 2.6", 2.6, "<", "mmol/L", "1.0 - 3.0", 1.0, 3.0, nil),
        (200, nil, "Synthetic marker", "12", 12, nil, "U/L", "5 - 40", 5, 40, nil),
        (0, "ldl_c", "LDL cholesterol", "2.9", 2.9, nil, "mmol/L", "1.0 - 3.0", 1.0, 3.0, nil),
        (0, "creatinine", "Creatinine", "88", 88, nil, "umol/L", "60 - 110", 60, 110, nil),
        (0, nil, "Synthetic marker", "14", 14, nil, "U/L", "5 - 40", 5, 40, nil),
    ]

    func labResults() -> Reply {
        let range = query.dates().map { Set($0.map(\.description)) }
        let rows = Self.labPlan.enumerated().compactMap { i, r -> JSON? in
            let date = today.adding(days: -r.back)
            guard range?.contains(date.description) ?? true else { return nil }
            let hex = { (prefix: String) in prefix + String(format: "%032x", i + 1) }
            let at = Self.iso(Self.at(date, minutes: 8 * 60))
            return [
                "id": hex("lab_"), "revision": 1, "analyte": r.analyte ?? NSNull(), "original_label": r.label, "value_text": r.text,
                "value_numeric": r.value ?? NSNull(), "comparator": r.comparator ?? NSNull(), "unit_text": r.unit ?? NSNull(),
                "reference_range_text": r.range ?? NSNull(), "ref_low": r.low ?? NSNull(), "ref_high": r.high ?? NSNull(),
                "printed_flag": r.flag ?? NSNull(), "specimen_type": "Serum", "canonical_value": NSNull(), "canonical_unit": NSNull(),
                "conversion_factor": NSNull(), "conversion_offset": NSNull(), "catalog_version": 1, "collected_at": NSNull(),
                "collected_date": date.description, "page": 1, "evidence_text": NSNull(), "created_at": at, "updated_at": at,
                "provenance": [
                    "report_id": hex("rpt_"), "document_id": hex("doc_"), "extraction_id": NSNull(), "row_index": 0,
                    "laboratory": "Synthetic Lab", "reported_at": at, "provider": "fake", "model": NSNull(), "schema_version": "1",
                    "prompt_version": "1", "confirmed_by": FakeServer.Owner.username, "confirmed_at": at,
                ],
            ]
        }
        return Self.page(rows, key: "lab_results", query: query)
    }

    // MARK: Helpers

    struct Query {
        let items: [URLQueryItem]

        init(_ url: URL) {
            items = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        }

        func value(_ name: String) -> String? {
            items.first { $0.name == name }?.value
        }

        /// A repeated or comma-separated parameter.
        func list(_ name: String) -> [String] {
            items.filter { $0.name == name }.flatMap { ($0.value ?? "").split(separator: ",").map(String.init) }
        }

        /// start_date through end_date (a missing start reads from 2000), or nil without an end.
        func dates() -> [LocalDate]? {
            guard let end = value("end_date").flatMap(LocalDate.init) else { return nil }
            let start = value("start_date").flatMap(LocalDate.init) ?? LocalDate("2000-01-01")!
            guard start <= end else { return [] }
            return (0...(end.number - start.number)).map { start.adding(days: $0) }
        }
    }

    /// Every date with data, oldest first, when a request has no range.
    func allDates(back: Int) -> [LocalDate] {
        (0..<back).map { today.adding(days: -$0) }.reversed()
    }

    /// One page of `rows` at most `pageSize` long, after the opaque cursor.
    static func page(_ rows: [JSON], key: String, query: Query) -> Reply {
        let limit = min(max(query.value("limit").flatMap(Int.init) ?? 100, 1), pageSize)
        var offset = 0
        if let cursor = query.value("cursor") {
            guard cursor.hasPrefix("sp-"), let parsed = Int(cursor.dropFirst(3)), (0..<rows.count).contains(parsed) else {
                return problem(422, "invalid or expired cursor")
            }
            offset = parsed
        }
        let end = min(offset + limit, rows.count)
        var body: JSON = [key: Array(rows[offset..<end]), "has_more": end < rows.count]
        if end < rows.count { body["next_cursor"] = "sp-\(end)" }
        return json(200, body)
    }

    static func source(_ provider: String, key: Int, device: String? = nil, origin: String? = nil) -> JSON {
        [
            "provider": provider, "connection_id": connection(key), "device": device.map { deviceID($0 == "watch" ? "a" : $0 == "band" ? "c" : "b") } ?? NSNull(),
            "device_type": device ?? NSNull(), "origin": origin ?? NSNull(), "external_id": NSNull(), "dedupe_key": String(format: "%032x", key),
        ]
    }

    static func connection(_ index: Int) -> String {
        "conn_" + String(format: "%032x", index)
    }

    static func deviceID(_ letter: String) -> String {
        "dev_" + String(repeating: letter, count: 32)
    }

    static var provenance: JSON {
        [
            "raw_payload_id": "5", "normalizer": "synthetic@1", "ingested_at": "2026-01-01T06:00:00Z", "normalized_at": "2026-01-01T06:00:01Z",
            "superseded_at": NSNull(), "superseded_by": NSNull(), "deleted_at": NSNull(), "deleted_by_raw_id": NSNull(),
        ]
    }

    /// The instant of a local wall-clock time, `minutes` after the date's midnight.
    static func at(_ date: LocalDate, minutes: Int) -> Date {
        date.start(in: zone).addingTimeInterval(TimeInterval(minutes * 60))
    }

    static func offset(_ instant: Date) -> Int {
        zone.secondsFromGMT(for: instant) / 60
    }

    static func iso(_ date: Date) -> String {
        date.formatted(Date.ISO8601FormatStyle())
    }

    static func round(_ value: Double) -> Double {
        (value * 10).rounded() / 10
    }

    static func json(_ status: Int, _ object: JSON) -> Reply {
        Reply(status: status, body: try! JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]))
    }

    static func problem(_ status: Int, _ detail: String) -> Reply {
        let object: JSON = [
            "type": "urn:vitamux:problem:validation_failed", "title": "Validation failed", "status": status,
            "code": "validation_failed", "detail": detail, "request_id": "req-fake",
        ]
        var reply = json(status, object)
        reply.headers = ["Content-Type": "application/problem+json"]
        return reply
    }
}

private extension LocalDate {
    /// Days since 1970: the seed of the synthetic values and the digits of record ids.
    var number: Int {
        Int(start(in: .gmt).timeIntervalSince1970 / 86_400)
    }
}
#endif
