#if DEBUG
import Foundation

// The Apple Watch views (J22.18) for the fake server, on the J22.17 endpoints and codes
// (docs/adr/0024-watch-data.md): ECG recordings and their waveforms, beat-to-beat (rr_interval)
// series, activity-summary daily values with Apple's goals, State of Mind and mindful sessions,
// and one workout's segments and route. Every value is synthetic and placed relative to today in
// the specialised fixture's timezone; the route sits at sea near 0°, 0°.
//
// Scenarios:
// - ECG: three recordings. Today's and the one 20 days back have a waveform (30 s at 512 Hz);
//   the one 4 days back has none, so its strip shows the empty state.
// - Beats: two series a day for 30 days (07:00 and 14:30), none 3 days back.
// - Activity summaries: 60 days of move (active energy), exercise and stand with Apple's goals;
//   2 days back is a paused day.
// - Mind: three State of Mind entries and two mindful sessions in the last week.
// - Workouts: `GET /workouts/{id}` answers the specialised fixture's workout members; today's
//   Apple Health run (`913…`) has laps, a pause, a marker and a route, the evening ride (`914…`) two
//   activities, the others laps only and no route.
// - Inventory and catalogue: the Watch codes are added to `GET /inventory` and `GET /metrics`.
//   `GET /events` answers here only when every requested code is a Watch code, so the events
//   fixture's unfiltered list stays as it is.
extension FakeServer {
    /// Answers an Apple Watch endpoint, or nil for a request this file does not handle.
    static func watch(method: String, url: URL) -> Reply? {
        guard method == "GET" else { return nil }
        let query = SpecialisedFixture.Query(url)
        let fixture = WatchFixture(query: query)
        let parts = url.path.split(separator: "/").map(String.init)
        switch url.path {
        case "/api/v1/inventory":
            return fixture.inventory(base: explore(method: method, url: url, body: Data()))
        case "/api/v1/metrics":
            return SpecialisedFixture.json(200, ["metrics": allMetrics + WatchFixture.metrics])
        case "/api/v1/events":
            let codes = query.list("code")
            guard !codes.isEmpty, codes.allSatisfy(WatchFixture.eventCodes.contains) else { return nil }
            return fixture.events(codes: Set(codes))
        case "/api/v1/measurements":
            let metrics = query.list("metric")
            guard !metrics.isEmpty, metrics.allSatisfy(WatchFixture.measurementCodes.contains) else { return nil }
            return fixture.measurements(codes: Set(metrics))
        default:
            break
        }
        switch parts.count {
        case 4 where parts[2] == "metrics":
            return WatchFixture.metrics.first { $0["code"] as? String == parts[3] }.map { SpecialisedFixture.json(200, $0) }
        case 4 where parts[2] == "workouts":
            return fixture.workout(parts[3])
        case 5 where parts[2] == "workouts" && parts[4] == "route":
            return fixture.route(parts[3])
        case 5 where parts[2] == "events" && parts[4] == "waveform":
            return fixture.waveform(parts[3])
        default:
            return nil
        }
    }
}

struct WatchFixture {
    typealias JSON = [String: Any]

    static let eventCodes: Set<String> = ["ecg_recording", "state_of_mind", "mindful_session"]
    static let measurementCodes: Set<String> = ["rr_interval", "active_energy", "move_time", "exercise_time", "stand_hours"]
    /// Beats per series and the day without beats.
    static let beatsPerSeries = 90
    static let beatsGap = 3
    static let summaryDays = 60
    static let pausedDay = 2
    /// The ECG strip: 30 s at 512 Hz.
    static let ecgHz = 512.0
    static let ecgSeconds = 30.0
    /// The route: 40 min at 1 Hz.
    static let routePoints = 2_400

    let query: SpecialisedFixture.Query
    let today = LocalDate.today(in: SpecialisedFixture.zone)

    init(query: SpecialisedFixture.Query) {
        self.query = query
    }

    // MARK: Catalogue and inventory

    /// GET /metrics entries of the Watch codes Explore lists (J22.17's catalogue rows).
    static var metrics: [JSON] { [
        [
            "code": "rr_interval", "section": "Heart and circulation", "unit": "s", "kinds": ["sample"], "aggregation": "intensive",
            "windows": [String](), "strategies": [String](), "plausible_range": [0.2, 3], "provider_scoped": false,
            "selection_only": false, "unresolved": true, "intraday": ["default": "5m", "finest": "raw"],
        ],
        [
            "code": "stand_hours", "section": "Activity", "unit": "count", "kinds": ["interval", "daily_value"], "aggregation": "additive",
            "windows": ["bucket", "hour", "local_day"], "strategies": ["single_source", "first_available", "latest"],
            "plausible_range": [0, 24], "provider_scoped": false, "selection_only": false, "intraday": ["default": "30m", "finest": "1m"],
        ],
    ] }

    /// The explore fixture's inventory plus the Watch items.
    func inventory(base: Reply?) -> Reply? {
        guard let base, base.status == 200, let body = base.body,
              var object = (try? JSONSerialization.jsonObject(with: body)) as? JSON,
              let items = object["items"] as? [JSON]
        else { return base }
        let watch = [ExploreFixture.watch]
        let at = { (date: LocalDate, minutes: Int) in Self.iso(Self.at(date, minutes: minutes)) }
        let item = { (kind: String, code: String, days: Int, last: LocalDate, minutes: Int, extra: JSON) -> JSON in
            [
                "kind": kind, "code": code, "count": days * 3, "days": days, "first_date": today.adding(days: -40).description,
                "last_date": last.description, "first_at": at(today.adding(days: -40), minutes), "last_at": at(last, minutes),
                "providers": ["apple_health"], "devices": watch, "origins": [JSON](),
            ].merging(extra) { $1 }
        }
        object["items"] = items + [
            item("metric", "rr_interval", 27, today, 14 * 60 + 30, ["metric": Self.metrics[0], "count": 27 * 2 * Self.beatsPerSeries,
                 "latest": ["local_date": today.description, "value": 0.94, "unit": "s"]]),
            item("metric", "stand_hours", Self.summaryDays, today, 23 * 60, ["metric": Self.metrics[1],
                 "latest": ["local_date": today.description, "value": 7, "unit": "count"]]),
            item("event", "ecg_recording", 3, today, 8 * 60 + 5, ["event": ["code": "ecg_recording", "levels": [String]()],
                 "latest": ["local_date": today.description, "level": "sinus_rhythm"]]),
            item("event", "state_of_mind", 3, today, 9 * 60, ["latest": ["local_date": today.description, "level": "momentary_emotion"]]),
        ]
        return SpecialisedFixture.json(200, object)
    }

    // MARK: Events

    struct Plan {
        var back: Int
        var minutes: Int
        var length: Int?
        var code: String
        var level: String?
        var value: Double?
        var context: JSON
    }

    /// The Watch events, oldest first. ECG ids end in 1xx, the others in 2xx.
    static var eventPlan: [(id: Int, plan: Plan)] { [
        (101, Plan(back: 20, minutes: 12 * 60 + 15, code: "ecg_recording", level: "atrial_fibrillation", value: 88,
                   context: ["symptoms_status": "present", "voltage_count": 15_360, "sampling_frequency_hz": 512, "lead": "apple_watch_similar_to_lead_i", "algorithm_version": 2])),
        (201, Plan(back: 5, minutes: 20 * 60, code: "state_of_mind", level: "daily_mood", value: 0, context: ["valence_classification": "neutral"])),
        (102, Plan(back: 4, minutes: 21 * 60 + 40, code: "ecg_recording", level: "inconclusive_poor_reading", value: nil,
                   context: ["symptoms_status": "not_set", "voltage_count": 0])),
        (202, Plan(back: 2, minutes: 22 * 60, length: 5, code: "mindful_session", context: [:])),
        (203, Plan(back: 1, minutes: 21 * 60, code: "state_of_mind", level: "daily_mood", value: -0.2,
                   context: ["valence_classification": "slightly_unpleasant", "labels": ["drained"], "associations": ["tasks", "weather"]])),
        (204, Plan(back: 0, minutes: 7 * 60, length: 10, code: "mindful_session", context: [:])),
        (103, Plan(back: 0, minutes: 8 * 60 + 5, code: "ecg_recording", level: "sinus_rhythm", value: 64,
                   context: ["symptoms_status": "none", "voltage_count": 15_360, "sampling_frequency_hz": 512, "lead": "apple_watch_similar_to_lead_i", "algorithm_version": 2])),
        (205, Plan(back: 0, minutes: 9 * 60, code: "state_of_mind", level: "momentary_emotion", value: 0.4,
                   context: ["valence_classification": "slightly_pleasant", "labels": ["calm", "content"], "associations": ["work"]])),
    ] }

    static func eventID(_ n: Int) -> String {
        String(format: "00000000-0000-4000-8000-000000000%03d", n)
    }

    func events(codes: Set<String>) -> Reply {
        let range = query.dates().map { Set($0.map(\.description)) }
        let rows = Self.eventPlan.compactMap { id, plan -> JSON? in
            let date = today.adding(days: -plan.back)
            guard codes.contains(plan.code), range?.contains(date.description) ?? true else { return nil }
            let start = Self.at(date, minutes: plan.minutes)
            var row: JSON = [
                "id": Self.eventID(id), "code": plan.code, "start_at": Self.iso(start),
                "end_at": plan.code == "ecg_recording" ? Self.iso(start.addingTimeInterval(Self.ecgSeconds))
                    : plan.length.map { Self.iso(start.addingTimeInterval(TimeInterval($0 * 60))) } ?? NSNull(),
                "tz_offset_min": SpecialisedFixture.offset(start), "local_date": date.description,
                "value": plan.value ?? NSNull(), "level": plan.level ?? NSNull(), "context": plan.context, "quality_flags": 0,
                "source": SpecialisedFixture.source("apple_health", key: 3, device: "watch"), "provenance": SpecialisedFixture.provenance,
            ]
            if Self.hasWaveform(id) { row["file_sha256"] = String(format: "%064x", id) }
            return row
        }
        return SpecialisedFixture.page(rows, key: "events", query: query)
    }

    static func hasWaveform(_ id: Int) -> Bool {
        id == 101 || id == 103
    }

    /// vitamux.waveform/1 of an ECG with one: a synthetic beat (P, QRS, T) every 60/hr seconds.
    func waveform(_ id: String) -> Reply {
        guard let entry = Self.eventPlan.first(where: { Self.eventID($0.id) == id }), Self.hasWaveform(entry.id) else {
            return SpecialisedFixture.notFound("no waveform for this event")
        }
        let start = Self.at(today.adding(days: -entry.plan.back), minutes: entry.plan.minutes)
        let period = 60 / (entry.plan.value ?? 60)
        let count = Int(Self.ecgHz * Self.ecgSeconds)
        let values = (0..<count).map { i -> Double in
            let t = Double(i) / Self.ecgHz
            let phase = (t + 0.4).truncatingRemainder(dividingBy: period) - 0.4
            let bump = { (center: Double, width: Double, height: Double) in height * exp(-pow((phase - center) / width, 2)) }
            let microvolts = bump(-0.2, 0.025, 120) + bump(-0.03, 0.008, -90) + bump(0, 0.01, 950) + bump(0.03, 0.009, -230) + bump(0.28, 0.05, 280)
            return (microvolts + 12 * sin(t * 1.7)).rounded()
        }
        return SpecialisedFixture.json(200, [
            "format": "vitamux.waveform/1", "start": Self.iso(start), "sampling_frequency_hz": Self.ecgHz, "unit": "µV",
            "lead": "apple_watch_similar_to_lead_i", "values": values,
        ])
    }

    // MARK: Measurements

    func measurements(codes: Set<String>) -> Reply {
        let dates = query.dates() ?? (0..<Self.summaryDays).map { today.adding(days: -$0) }.reversed()
        var rows: [JSON] = []
        for date in dates where date <= today {
            let back = Self.days(from: date, to: today)
            if codes.contains("rr_interval") { rows += beats(on: date, back: back) }
            rows += summary(on: date, back: back, codes: codes)
        }
        return SpecialisedFixture.page(rows, key: "measurements", query: query)
    }

    /// Two heartbeat series on the day, one row per beat after the first, keyed `<series>#<beat>`.
    func beats(on date: LocalDate, back: Int) -> [JSON] {
        guard (0..<30).contains(back), back != Self.beatsGap else { return [] }
        return [(1, 7 * 60), (2, 14 * 60 + 30)].flatMap { series, minutes -> [JSON] in
            let uuid = String(format: "00000000-0000-4000-9000-%07d%05d", series, Self.number(date))
            var t = Self.at(date, minutes: minutes)
            return (1..<Self.beatsPerSeries).map { i -> JSON in
                let rr = (0.92 + 0.06 * sin(Double(i) / 4) + 0.01 * Double((i + back) % 3) + 0.02 * Double(series - 1)) * 1_000
                let seconds = rr.rounded() / 1_000
                t = t.addingTimeInterval(seconds)
                return Self.measurement(id: "\(series)\(Self.number(date))\(String(format: "%03d", i))", metric: "rr_interval", kind: "sample",
                                        start: t, end: nil, date: date, value: seconds, unit: "s", externalID: "\(uuid)#\(i)", context: nil)
            }
        }
    }

    /// The day's activity summary as daily values with Apple's goals in context.
    func summary(on date: LocalDate, back: Int, codes: Set<String>) -> [JSON] {
        guard (0..<Self.summaryDays).contains(back) else { return [] }
        let n = Double(Self.number(date))
        let paused = back == Self.pausedDay
        let part = back == 0 ? 0.55 : 1.0
        let plan: [(code: String, unit: String, value: Double, goal: Double)] = [
            ("active_energy", "kcal", ((420 + 140 * sin(n / 3)) * part).rounded(), 500),
            ("exercise_time", "s", ((26 + 14 * sin(n / 2)) * part).rounded() * 60, 1_800),
            ("stand_hours", "count", ((9 + 3 * sin(n / 2.5)) * part).rounded(), 12),
        ]
        let start = date.start(in: SpecialisedFixture.zone), end = date.adding(days: 1).start(in: SpecialisedFixture.zone)
        let uuid = String(format: "00000000-0000-5000-8000-%012d", Self.number(date))
        return plan.filter { codes.contains($0.code) }.enumerated().map { i, row in
            Self.measurement(id: "5\(i)\(Self.number(date))", metric: row.code, kind: "daily_value", start: start, end: end, date: date,
                             value: paused ? 0 : row.value, unit: row.unit, externalID: uuid,
                             context: ["goal": row.goal, "move_mode": "active_energy", "paused": paused],
                             origin: "vitamux.activity-summary", device: nil)
        }
    }

    static func measurement(id: String, metric: String, kind: String, start: Date, end: Date?, date: LocalDate, value: Double,
                            unit: String, externalID: String, context: JSON?, origin: String? = nil, device: String? = "watch") -> JSON {
        var source = SpecialisedFixture.source("apple_health", key: 3, device: device, origin: origin)
        source["external_id"] = externalID
        var row: JSON = [
            "id": id, "metric": metric, "kind": kind, "start_at": iso(start), "end_at": end.map(iso) ?? NSNull(),
            "tz_offset_min": SpecialisedFixture.offset(start), "local_date": date.description, "value": value, "unit": unit,
            "source_value": NSNull(), "source_unit": NSNull(), "quality_flags": 0, "group_id": NSNull(),
            "source": source, "provenance": SpecialisedFixture.provenance,
        ]
        if let context { row["context"] = context }
        return row
    }

    // MARK: Workouts

    /// The specialised fixture's workout members by id prefix: provider, sport and local times.
    static let workoutPlan: [String: (provider: String, sport: String, from: Int, to: Int, device: String?)] = [
        "911": ("garmin", "running", 7 * 60 + 10, 7 * 60 + 52, "watch"),
        "912": ("whoop", "running", 7 * 60 + 9, 7 * 60 + 53, "band"),
        "913": ("apple_health", "running", 7 * 60 + 11, 7 * 60 + 51, "watch"),
        "914": ("garmin", "cycling", 18 * 60, 18 * 60 + 45, "watch"),
        "915": ("garmin", "running", 7 * 60, 7 * 60 + 40, "watch"),
        "916": ("garmin", "walking", 6 * 60 + 30, 7 * 60 + 15, "watch"),
    ]

    /// A member id (`913` + days since 1970) as its plan and local date.
    func member(_ id: String) -> (plan: (provider: String, sport: String, from: Int, to: Int, device: String?), date: LocalDate)? {
        guard id.count > 3, let plan = Self.workoutPlan[String(id.prefix(3))], let n = Int(id.dropFirst(3)) else { return nil }
        let date = LocalDate("1970-01-01")!.adding(days: n)
        guard date <= today, Self.days(from: date, to: today) < 70 else { return nil }
        return (plan, date)
    }

    func workout(_ id: String) -> Reply {
        guard let (plan, date) = member(id) else { return SpecialisedFixture.notFound("no workout with this id") }
        let start = Self.at(date, minutes: plan.from), end = Self.at(date, minutes: plan.to)
        let minute = { (m: Int) in Self.iso(Self.at(date, minutes: plan.from + m)) }
        var segments: [JSON] = []
        switch id.prefix(3) {
        case "913":
            for lap in 0..<5 {
                segments.append(["seq": 0, "kind": "lap", "start_at": minute(lap * 8), "end_at": minute(lap * 8 + 8), "data": JSON()])
            }
            segments.append(["seq": 0, "kind": "pause", "start_at": minute(19), "end_at": minute(20), "data": ["type": "pause"]])
            segments.append(["seq": 0, "kind": "marker", "start_at": minute(30), "end_at": NSNull(), "data": ["type": "marker"]])
        case "914":
            segments.append(["seq": 0, "kind": "activity", "start_at": minute(0), "end_at": minute(30),
                             "data": ["sport": "cycling", "provider_sport": "cycling", "duration_s": 1_800, "location": "outdoor",
                                      "totals": ["distance_m": 11_200, "energy_kcal": 290]]])
            segments.append(["seq": 0, "kind": "activity", "start_at": minute(32), "end_at": minute(45),
                             "data": ["sport": "running", "provider_sport": "running", "duration_s": 780, "location": "outdoor",
                                      "totals": ["distance_m": 2_400, "energy_kcal": 150]]])
        default:
            for lap in 0..<2 {
                segments.append(["seq": 0, "kind": "lap", "start_at": minute(lap * 15), "end_at": minute(lap * 15 + 15), "data": JSON()])
            }
        }
        segments.sort { ($0["start_at"] as! String) < ($1["start_at"] as! String) }
        for i in segments.indices { segments[i]["seq"] = i }
        return SpecialisedFixture.json(200, [
            "id": id, "start_at": Self.iso(start), "end_at": Self.iso(end), "tz_offset_min": SpecialisedFixture.offset(start),
            "local_date": date.description, "sport": plan.sport, "provider_sport": plan.sport, "distance_m": 8_000,
            "energy_kcal": 395, "avg_hr_bpm": 147, "max_hr_bpm": 170, "file_sha256": NSNull(), "segments": segments,
            "source": SpecialisedFixture.source(plan.provider, key: 3, device: plan.device), "provenance": SpecialisedFixture.provenance,
        ])
    }

    /// vitamux.route/1 of today's Apple Health run: a loop at sea near 0°, 0° at 1 Hz.
    func route(_ id: String) -> Reply {
        guard id.hasPrefix("913"), let (plan, date) = member(id) else { return SpecialisedFixture.notFound("no route for this workout") }
        let count = Self.routePoints
        let turn = { (i: Int) in 2 * Double.pi * Double(i) / Double(count) }
        let course = (0..<count).map { (turn($0) * 180 / .pi + 90).truncatingRemainder(dividingBy: 360) }
        return SpecialisedFixture.json(200, [
            "format": "vitamux.route/1", "start": Self.iso(Self.at(date, minutes: plan.from)), "count": count,
            "offsets_s": (0..<count).map(Double.init),
            "latitude": (0..<count).map { 0.01 + 0.004 * sin(turn($0)) },
            "longitude": (0..<count).map { 0.01 + 0.006 * (1 - cos(turn($0))) + 0.0004 * sin(5 * turn($0)) },
            "altitude_m": [Double](repeating: 0, count: count),
            "horizontal_accuracy_m": [Double](repeating: 5, count: count),
            "speed_mps": (0..<count).map { 3.2 + 0.3 * sin(Double($0) / 60) },
            "course_deg": course,
        ])
    }

    // MARK: Helpers

    static func at(_ date: LocalDate, minutes: Int) -> Date {
        SpecialisedFixture.at(date, minutes: minutes)
    }

    static func iso(_ date: Date) -> String {
        SpecialisedFixture.iso(date)
    }

    /// Days since 1970: part of synthetic ids and values.
    static func number(_ date: LocalDate) -> Int {
        Int(date.start(in: .gmt).timeIntervalSince1970 / 86_400)
    }

    static func days(from: LocalDate, to: LocalDate) -> Int {
        number(to) - number(from)
    }
}

extension SpecialisedFixture {
    static func notFound(_ detail: String) -> Reply {
        let object: JSON = [
            "type": "urn:vitamux:problem:not_found", "title": "Not found", "status": 404,
            "code": "not_found", "detail": detail, "request_id": "req-fake",
        ]
        var reply = json(404, object)
        reply.headers = ["Content-Type": "application/problem+json"]
        return reply
    }
}
#endif
