#if DEBUG
import Foundation

// The Day view (J22.26) for the fake server, mirroring the server's intraday answers
// (internal/api/explore.go and resolved.go): GET /resolved/series with a bucket size as `window`
// (30s, 1m, 5m, 15m, 30m) and GET /sources/series with an intraday or raw grain, plus `intraday`
// on the catalogue entries below. Every value is synthetic and derived from the instant, so any
// day works; today stops at now.
//
// Scenarios (per local day, Europe/Berlin):
// - heart_rate: the watch through Apple Health every 6 s (14,400 rows a day) with a 12:30–13:00
//   gap, and Garmin every 2 minutes; mean across sources. Lower inside the night's sleep episode
//   (FakeServer+Specialised.swift) and higher in its workouts.
// - steps: the watch's one-minute intervals (origin Example Health) summing to the day's resolved
//   total (FakeServer+Explore.swift), and Garmin's 15-minute intervals; first available.
// - spo2: the watch every 5 minutes inside the sleep episodes only, so the Day view opens on the
//   night.
extension FakeServer {
    /// Answers an intraday endpoint, or nil for a request this file does not handle.
    static func intraday(method: String, url: URL) -> Reply? {
        guard method == "GET" else { return nil }
        let query = ExploreFixture.Query(url)
        let parts = url.path.split(separator: "/").map(String.init)
        switch url.path {
        case "/api/v1/metrics":
            return Reply.json(200, ["metrics": allMetrics.map(IntradayFixture.withIntraday)])
        case "/api/v1/resolved/series":
            return IntradayFixture(query: query)?.resolvedSeries()
        case "/api/v1/sources/series":
            return IntradayFixture(query: query)?.sourceSeries()
        default:
            guard parts.count == 4, parts[2] == "metrics", IntradayFixture.ladders[parts[3]] != nil,
                  let entry = ExploreFixture.catalogue(parts[3]) else { return nil }
            return Reply.json(200, IntradayFixture.withIntraday(entry))
        }
    }
}

struct IntradayFixture {
    typealias JSON = [String: Any]
    typealias F = ExploreFixture

    /// The catalogue's `intraday` of the codes with a Day view here.
    static let ladders: [String: (default: String, finest: String)] = [
        "heart_rate": ("1m", "raw"), "spo2": ("5m", "raw"), "steps": ("30m", "1m"),
    ]

    static func withIntraday(_ entry: JSON) -> JSON {
        guard let code = entry["code"] as? String, let ladder = ladders[code] else { return entry }
        return entry.merging(["intraday": ["default": ladder.default, "finest": ladder.finest]]) { $1 }
    }

    struct Source {
        var group: String
        var provider: String
        var device: String
        var origin: (key: String, name: String)?
        /// Seconds between rows (interval rows last this long).
        var spacing: TimeInterval
        /// Local minutes without rows.
        var gap: Range<Int>?
    }

    struct Series {
        var code: String
        var unit: String
        var strategy: String
        var additive: Bool
        var sources: [Source]
    }

    static let series: [String: Series] = [
        "heart_rate": Series(code: "heart_rate", unit: "bpm", strategy: "mean_across_sources", additive: false, sources: [
            Source(group: "apple_watch", provider: "apple_health", device: "watch", spacing: 6, gap: 750 ..< 780),
            Source(group: "garmin", provider: "garmin", device: "watch", spacing: 120),
        ]),
        "steps": Series(code: "steps", unit: "count", strategy: "first_available", additive: true, sources: [
            Source(group: "apple_watch", provider: "apple_health", device: "watch", origin: F.phoneApp, spacing: 60),
            Source(group: "garmin", provider: "garmin", device: "watch", spacing: 900),
        ]),
        "spo2": Series(code: "spo2", unit: "%", strategy: "first_available", additive: false, sources: [
            Source(group: "apple_watch", provider: "apple_health", device: "watch", spacing: 300),
        ]),
    ]

    static let zone = F.zone
    static let rawPage = 2_000

    let spec: Series
    let query: ExploreFixture.Query
    let start: Date
    let end: Date
    let now = Date()

    init?(query: ExploreFixture.Query) {
        guard let code = query.value("metric"), let spec = Self.series[code],
              let start = query.value("start").flatMap(Self.instant), let end = query.value("end").flatMap(Self.instant) else { return nil }
        self.spec = spec
        self.query = query
        self.start = start
        self.end = end
    }

    static func instant(_ text: String) -> Date? {
        try? RFC3339DateTranscoder().decode(text)
    }

    // MARK: Rows

    /// One stored row: a sample at `t`, or an interval from `t` lasting the source's spacing.
    struct Row {
        var t: Date
        var value: Double
    }

    /// The local day's sleep episodes and workouts, as the specialised fixtures place them.
    struct Plan {
        var nights: [ClosedRange<Date>] = []
        var workouts: [ClosedRange<Date>] = []

        init(from: Date, to: Date) {
            let specialised = SpecialisedFixture(query: .init(URL(string: "https://fake.vitamux.test/")!))
            let today = LocalDate.today(in: zone)
            var date = LocalDate(from, in: zone)
            let last = LocalDate(to, in: zone).adding(days: 1)
            while date <= last {
                if let main = specialised.sessions(on: date).first(where: { !$0.nap }) { nights.append(main.start ... main.end) }
                // The workout schedule of SpecialisedFixture.resolvedWorkouts, by days back from today.
                let back = Int(today.start(in: .gmt).timeIntervalSince(date.start(in: .gmt)) / 86_400)
                let at = { (minutes: Int) in date.start(in: zone).addingTimeInterval(TimeInterval(minutes * 60)) }
                if (0 ..< 70).contains(back) {
                    if back == 0 { workouts += [at(430) ... at(472), at(1_080) ... at(1_125)] }
                    if back % 3 == 2 { workouts.append(at(420) ... at(460)) }
                    if back % 5 == 3 { workouts.append(at(390) ... at(435)) }
                }
                date = date.adding(days: 1)
            }
        }

        func asleep(_ t: Date) -> Bool { nights.contains { $0.contains(t) } }
        func training(_ t: Date) -> Bool { workouts.contains { $0.contains(t) } }
    }

    /// Minutes since the local midnight of `t`.
    static func minute(_ t: Date) -> Int {
        Int(t.timeIntervalSince(LocalDate(t, in: zone).start(in: zone)) / 60)
    }

    /// A source's rows in [from, to), never after now.
    func rows(_ index: Int, from: Date, to: Date, plan: Plan) -> [Row] {
        let source = spec.sources[index]
        let spacing = source.spacing
        let first = (from.timeIntervalSince1970 / spacing).rounded(.up) * spacing
        let stop = min(to, now).timeIntervalSince1970
        guard first < stop else { return [] }
        var out: [Row] = []
        out.reserveCapacity(Int((stop - first) / spacing) + 1)
        var steps = StepDay(spec: spec, source: index, plan: plan)
        var midnight = LocalDate(Date(timeIntervalSince1970: first), in: Self.zone).start(in: Self.zone)
        var nextMidnight = midnight.addingTimeInterval(86_400)
        for s in stride(from: first, to: stop, by: spacing) {
            let t = Date(timeIntervalSince1970: s)
            if t >= nextMidnight {
                midnight = LocalDate(t, in: Self.zone).start(in: Self.zone)
                nextMidnight = LocalDate(t, in: Self.zone).adding(days: 1).start(in: Self.zone)
            }
            if let gap = source.gap, gap.contains(Int(t.timeIntervalSince(midnight) / 60)) { continue }
            let value: Double?
            switch spec.code {
            case "heart_rate":
                value = Self.heartRate(t, plan: plan) + (index == 0 ? 0 : 1)
            case "spo2":
                value = plan.asleep(t) ? (95 + 1.5 * sin(s / 1_700)).rounded() : nil
            default:
                value = steps.count(at: t)
            }
            if let value { out.append(Row(t: t, value: value)) }
        }
        return out
    }

    static func heartRate(_ t: Date, plan: Plan) -> Double {
        let s = t.timeIntervalSince1970
        if plan.asleep(t) { return (53 + 3 * sin(s / 1_900) + 1.5 * sin(s / 47)).rounded() }
        if plan.training(t) { return (142 + 10 * sin(s / 300) + 2 * sin(s / 23)).rounded() }
        return (70 + 8 * sin(s / 5_400) + 3 * sin(s / 37)).rounded()
    }

    /// Steps per interval, shaped over the day and scaled so a source's day sums to its daily
    /// value in FakeServer+Explore.swift (cumulative rounding keeps whole steps).
    struct StepDay {
        let spec: Series
        let source: Int
        let plan: Plan
        private var date: LocalDate?
        private var counts: [Int: Double] = [:]

        init(spec: Series, source: Int, plan: Plan) {
            self.spec = spec
            self.source = source
            self.plan = plan
        }

        mutating func count(at t: Date) -> Double? {
            let day = LocalDate(t, in: zone)
            if day != date { fill(day) }
            return counts[minute(t)]
        }

        private mutating func fill(_ day: LocalDate) {
            date = day
            counts = [:]
            guard let daily = F.spec("steps"), let total = F(state: .init()).value(daily, source: source, on: day) else { return }
            let midnight = day.start(in: zone)
            let weights = (0 ..< 1_440).map { m -> Double in
                let t = midnight.addingTimeInterval(TimeInterval(m * 60))
                if plan.asleep(t) { return 0 }
                if plan.training(t) { return 150 }
                let s = t.timeIntervalSince1970
                return max(0, 18 + 16 * sin(s / 2_800) + 9 * sin(s / 410))
            }
            let sum = max(weights.reduce(0, +), 1)
            let length = Int(spec.sources[source].spacing / 60)
            var carried = 0.0
            var given = 0.0
            for m in stride(from: 0, to: 1_440, by: length) {
                carried += weights[m ..< min(m + length, 1_440)].reduce(0, +) * total / sum
                let whole = carried.rounded() - given
                given += whole
                counts[m] = whole
            }
        }
    }

    // MARK: GET /sources/series

    /// Intraday and raw grains; hour and day are FakeServer+Explore.swift's.
    func sourceSeries() -> Reply? {
        guard let grain = query.value("grain"), let step = IntradayStep(rawValue: grain) else { return nil }
        guard end > start, end.timeIntervalSince(start) <= 25 * 3_600 else {
            return Reply.problem(422, "validation_failed", "invalid range", errors: [("/end", "at most a day for \(grain)")])
        }
        let plan = Plan(from: start, to: end)
        var sources: [JSON] = []
        var hasMore = false
        var next: String?
        if step == .raw {
            // All sources' rows in time order, one page at a time.
            var all: [(source: Int, row: Row)] = []
            for i in spec.sources.indices { all += rows(i, from: start, to: end, plan: plan).map { (i, $0) } }
            all.sort { ($0.row.t, $0.source) < ($1.row.t, $1.source) }
            let limit = min(max(query.value("limit").flatMap(Int.init) ?? Self.rawPage, 1), Self.rawPage)
            var offset = 0
            if let cursor = query.value("cursor") {
                guard cursor.hasPrefix("ir-"), let parsed = Int(cursor.dropFirst(3)), (0 ..< all.count).contains(parsed) else {
                    return Reply.problem(422, "validation_failed", "invalid or expired cursor")
                }
                offset = parsed
            }
            let stop = min(offset + limit, all.count)
            let page = all[offset ..< stop]
            hasMore = stop < all.count
            if hasMore { next = "ir-\(stop)" }
            sources = spec.sources.indices.map { i in
                entry(i, points: page.filter { $0.source == i }.map { rawPoint($0.row, source: i) })
            }
        } else {
            sources = spec.sources.indices.map { i in
                entry(i, points: buckets(rows(i, from: start, to: end, plan: plan), source: i, size: step.seconds).map { bucket in
                    var point: JSON = ["start": F.iso(bucket.start), "local_date": LocalDate(bucket.start, in: Self.zone).description, "n": bucket.n]
                    if spec.additive { point["sum"] = bucket.sum } else {
                        point["mean"] = (bucket.sum / Double(bucket.n) * 10).rounded() / 10
                        point["min"] = bucket.low
                        point["max"] = bucket.high
                    }
                    return point
                })
            }
        }
        var body: JSON = [
            "metric": spec.code, "unit": spec.unit, "aggregation": spec.additive ? "additive" : "intensive", "grain": grain,
            "timezone": F.timezone, "behind": false, "sources": sources, "has_more": hasMore,
            "rule": ["ref": "builtin:\(spec.code):1", "version": 1, "strategy": spec.strategy],
        ]
        if let next { body["next_cursor"] = next }
        return Reply.json(200, body)
    }

    private func entry(_ index: Int, points: [JSON]) -> JSON {
        let source = spec.sources[index]
        var out: JSON = [
            "provider": source.provider, "connection_id": F.connection(source.provider), "group": source.group,
            "rule_status": "used", "device": F.device(source.device), "spacing_s": source.spacing, "points": points,
        ]
        if let origin = source.origin { out["origin"] = ["key": origin.key, "name": origin.name] }
        return out
    }

    private func rawPoint(_ row: Row, source: Int) -> JSON {
        var point: JSON = ["start": F.iso(row.t), "local_date": LocalDate(row.t, in: Self.zone).description, "n": 1, "value": row.value]
        if spec.additive { point["end"] = F.iso(row.t.addingTimeInterval(spec.sources[source].spacing)) }
        return point
    }

    struct Bucket {
        var start: Date
        var n = 0
        var sum = 0.0
        var low = Double.infinity
        var high = -Double.infinity
    }

    /// Rows per bucket of `size` seconds (aligned to local midnight: Berlin's offsets are whole
    /// hours), intervals pro-rated by overlap.
    func buckets(_ rows: [Row], source: Int, size: TimeInterval) -> [Bucket] {
        let length = spec.additive ? spec.sources[source].spacing : 0
        var out: [Bucket] = []
        var index: [TimeInterval: Int] = [:]
        func add(_ key: TimeInterval, _ value: Double, whole: Double) {
            let i = index[key] ?? {
                out.append(Bucket(start: Date(timeIntervalSince1970: key)))
                index[key] = out.count - 1
                return out.count - 1
            }()
            out[i].n += 1
            out[i].sum += value
            out[i].low = min(out[i].low, whole)
            out[i].high = max(out[i].high, whole)
        }
        for row in rows {
            let s = row.t.timeIntervalSince1970
            guard length > size else {
                add((s / size).rounded(.down) * size, row.value, whole: row.value)
                continue
            }
            // An interval longer than the bucket: its share of each bucket it covers.
            var b = (s / size).rounded(.down) * size
            while b < s + length {
                let overlap = min(b + size, s + length) - max(b, s)
                if overlap > 0 { add(b, (row.value * overlap / length * 10).rounded() / 10, whole: row.value) }
                b += size
            }
        }
        return out.filter { $0.start >= start && $0.start < end }
    }

    // MARK: GET /resolved/series

    func resolvedSeries() -> Reply? {
        guard let window = query.value("window"), let step = IntradayStep(rawValue: window), step != .raw else { return nil }
        let most: TimeInterval = step.seconds <= 60 ? 25 * 3_600 : (7 * 24 + 1) * 3_600
        guard end > start, end.timeIntervalSince(start) <= most else {
            return Reply.problem(422, "validation_failed", "invalid range", errors: [("/end", "at most \(step.seconds <= 60 ? "a day" : "7 days") for \(window) buckets")])
        }
        let size = step.seconds
        let plan = Plan(from: start, to: end)
        let perSource: [[TimeInterval: Bucket]] = spec.sources.indices.map { i in
            Dictionary(uniqueKeysWithValues: buckets(rows(i, from: start, to: end, plan: plan), source: i, size: size).map { ($0.start.timeIntervalSince1970, $0) })
        }
        var points: [JSON] = []
        var used: [String] = []
        var b = (start.timeIntervalSince1970 / size).rounded(.up) * size
        while b < end.timeIntervalSince1970 {
            let from = Date(timeIntervalSince1970: b), to = from.addingTimeInterval(size)
            var point: JSON = ["key": F.iso(from), "start": F.iso(from), "end": F.iso(to), "local_date": LocalDate(from, in: Self.zone).description]
            if to > now { point["partial"] = true }
            let found = spec.sources.indices.compactMap { i in perSource[i][b].map { (i, $0) } }
            let picks = spec.strategy == "mean_across_sources" ? found : Array(found.prefix(1))
            if picks.isEmpty {
                point["status"] = "no_data"
                point["sources"] = [String]()
            } else {
                let values = picks.map { spec.additive ? $0.1.sum : $0.1.sum / Double($0.1.n) }
                let value = spec.additive ? values[0] : values.reduce(0, +) / Double(values.count)
                point["value"] = (value * 10).rounded() / 10
                point["status"] = picks.count > 1 ? "calculated" : picks[0].0 == 0 ? "direct" : "fallback"
                point["sources"] = picks.map { spec.sources[$0.0].group }
                point["providers"] = picks.map { spec.sources[$0.0].provider }
                point["n"] = picks.reduce(0) { $0 + $1.1.n }
                point["coverage"] = 1
                if !spec.additive {
                    point["min"] = picks.map(\.1.low).min()!
                    point["max"] = picks.map(\.1.high).max()!
                }
                if picks[0].0 != 0 { point["warnings"] = ["preferred_source_unavailable"] }
                for p in picks where !used.contains(spec.sources[p.0].group) { used.append(spec.sources[p.0].group) }
            }
            points.append(point)
            b += size
        }
        let limit = min(max(query.value("limit").flatMap(Int.init) ?? 500, 1), 10_000)
        var offset = 0
        if let cursor = query.value("cursor") {
            guard cursor.hasPrefix("rp-"), let parsed = Int(cursor.dropFirst(3)), (0 ..< points.count).contains(parsed) else {
                return Reply.problem(422, "validation_failed", "invalid or expired cursor")
            }
            offset = parsed
        }
        let stop = min(offset + limit, points.count)
        var body: JSON = [
            "metric": spec.code, "unit": spec.unit, "window": ["kind": "bucket", "size": window], "timezone": F.timezone,
            "rule": ["ref": "builtin:\(spec.code):1", "version": 1, "strategy": spec.strategy],
            "points": Array(points[offset ..< stop]), "sources_used": used, "has_more": stop < points.count,
        ]
        if stop < points.count { body["next_cursor"] = "rp-\(stop)" }
        return Reply.json(200, body)
    }
}
#endif
