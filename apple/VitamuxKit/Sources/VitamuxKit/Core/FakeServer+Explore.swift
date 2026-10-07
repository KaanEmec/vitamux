#if DEBUG
import Foundation
import Synchronization

// Explore, metric detail, overrides and provenance (J22.8) for the fake server, mirroring
// web/e2e/explore-fake.ts and data-fake.ts: the inventory, catalogue entries, resolved days,
// summaries and rollups, per-source series, coverage, the all-sources drilldown with its records,
// overrides (applied the way the engine applies them) and provenance chains. The dashboard layout
// (pins) and GET /resolved/summary belong to FakeServer+Dashboard.swift. Every value is synthetic and derived from the date, so any range works.
//
// Scenario `resting_heart_rate`: the preferred source (whoop) has a degraded stream every day, so
// each day falls back to garmin; excluding garmin's record falls back again to apple_watch.
extension FakeServer {
    /// The extra catalogue entries Explore needs beside the shell's four (`metrics`).
    static var exploreMetrics: [[String: Any]] {
        ExploreFixture.extraMetrics.map(\.json)
    }

    /// Answers an Explore endpoint, or nil for a request this file does not handle. GET
    /// /measurements is answered only with a date filter (the drilldown's records); without one
    /// the shell's paging fixture answers.
    static func explore(method: String, url: URL, body: Data) -> Reply? {
        let host = url.host() ?? ""
        let query = ExploreFixture.Query(url)
        let path = url.path
        let parts = path.split(separator: "/").map(String.init)
        return ExploreFixture.states.withLock { states in
            var state = states[host] ?? ExploreFixture.State()
            defer { states[host] = state }
            let fixture = ExploreFixture(state: state)
            switch (method, path) {
            case ("GET", "/api/v1/inventory"):
                return fixture.inventory(ignored: query.value("include_ignored") == "true" ? SourceFilterFixture.ignoredItems(host) : nil)
            case ("GET", "/api/v1/resolved/daily"):
                return fixture.daily(query)
            case ("GET", "/api/v1/resolved/trend"):
                return fixture.trend(query)
            case ("GET", "/api/v1/sources/series"):
                return fixture.sourceSeries(query)
            case ("GET", "/api/v1/coverage"):
                return fixture.coverage(query)
            case ("GET", "/api/v1/measurements") where query.value("start_date") != nil:
                return fixture.measurements(query)
            case ("GET", "/api/v1/overrides"):
                return fixture.listOverrides(query)
            case ("POST", "/api/v1/overrides"):
                return state.create(body)
            case ("POST", _) where parts.count == 5 && parts[2] == "overrides" && parts[4] == "revoke":
                return state.revoke(parts[3])
            case ("GET", _) where parts.count == 4 && parts[2] == "metrics":
                return fixture.metric(parts[3])
            case ("GET", _) where parts.count == 5 && parts[2] == "provenance":
                return fixture.provenance(entity: parts[3], id: parts[4])
            case ("GET", _) where parts.count == 6 && parts[2] == "resolved" && parts[5] == "sources":
                return fixture.drilldown(metric: parts[3], key: parts[4])
            default:
                return nil
            }
        }
    }
}

/// The synthetic dataset and the state Explore's writes change (overrides and the dashboard
/// layout), kept per fake host so parallel unit tests never share it.
struct ExploreFixture {
    static let states = Mutex<[String: State]>([:])
    static let timezone = "Europe/Berlin"
    static let zone = TimeZone(identifier: timezone)!
    /// Data runs from 400 days before today, so "All" reads weekly rollups.
    static let history = 400

    struct State {
        var overrides: [Override] = []
        var nextOverride = 1
    }

    struct Override {
        var id: String
        var metric: String
        var kind: String
        var key: String
        var date: String
        var action: String
        var inputID: String?
        var group: String?
        var value: Double?
        var unit: String?
        var note: String?
        var active = true
        var revokedAt: String?

        var json: [String: Any] {
            [
                "id": id, "metric": metric, "window": ["kind": kind, "key": key, "local_date": date],
                "action": action, "input_id": inputID ?? NSNull(), "group": group ?? NSNull(),
                "value": value ?? NSNull(), "unit": unit ?? NSNull(), "note": note ?? NSNull(),
                "active": active, "created_by": FakeServer.Owner.username, "created_at": "2026-01-02T07:00:00Z",
                "revoked_by": revokedAt == nil ? NSNull() : FakeServer.Owner.username, "revoked_at": revokedAt ?? NSNull(),
            ]
        }
    }

    // MARK: Catalogue and sources

    /// One source of a metric: its rule group, provider, device and value offset.
    struct Source {
        var group: String
        var provider: String
        var device: String
        var offset: Double
        /// A source with no values (a degraded stream), with the reason the rule gives.
        var degraded: String?
        var origin: (key: String, name: String)?
    }

    /// How a metric resolves in the fixture.
    struct Series {
        var code: String
        var unit: String
        var strategy: String
        var base: Double
        var swing: Double
        var digits: Int
        var additive = false
        /// Values on every `every`-th day only (a scale, a cuff).
        var every = 1
        /// No value on every `gap`-th day.
        var gap: Int?
        var sources: [Source]
        var basis = "daily_value"
    }

    static let watch = ["id": "dev_" + String(repeating: "a", count: 32), "type": "watch", "model": "Synthetic Watch"]
    static let scale = ["id": "dev_" + String(repeating: "b", count: 32), "type": "scale", "model": "Synthetic Scale"]
    static let band = ["id": "dev_" + String(repeating: "c", count: 32), "type": "band", "model": "Synthetic Band"]
    static let phoneApp = (key: "com.example.health", name: "Example Health")

    static let series: [Series] = [
        Series(code: "resting_heart_rate", unit: "bpm", strategy: "first_available", base: 52, swing: 2, digits: 0, sources: [
            Source(group: "whoop", provider: "whoop", device: "band", offset: 0, degraded: "stream degraded: schema_drift since 2026-01-01T06:00Z"),
            Source(group: "garmin", provider: "garmin", device: "watch", offset: 0),
            Source(group: "apple_watch", provider: "apple_health", device: "watch", offset: 2),
        ]),
        Series(code: "heart_rate", unit: "bpm", strategy: "mean_across_sources", base: 61, swing: 3, digits: 1, sources: [
            Source(group: "garmin", provider: "garmin", device: "watch", offset: 1),
            Source(group: "apple_watch", provider: "apple_health", device: "watch", offset: -1),
        ], basis: "samples"),
        Series(code: "steps", unit: "count", strategy: "first_available", base: 8000, swing: 2200, digits: 0, additive: true, sources: [
            Source(group: "apple_watch", provider: "apple_health", device: "watch", offset: 0, origin: phoneApp),
            Source(group: "garmin", provider: "garmin", device: "watch", offset: -400),
        ], basis: "intervals"),
        Series(code: "body_mass", unit: "kg", strategy: "latest", base: 72.4, swing: 0.6, digits: 1, every: 3, sources: [
            Source(group: "withings", provider: "withings", device: "scale", offset: 0),
        ], basis: "reading"),
        Series(code: "spo2", unit: "%", strategy: "first_available", base: 96, swing: 1.5, digits: 0, gap: 9, sources: [
            Source(group: "apple_watch", provider: "apple_health", device: "watch", offset: 0),
        ], basis: "samples"),
        Series(code: "hrv_rmssd_nightly", unit: "ms", strategy: "single_source", base: 48, swing: 6, digits: 0, sources: [
            Source(group: "garmin", provider: "garmin", device: "watch", offset: 0),
        ]),
        Series(code: "bp_systolic", unit: "mmHg", strategy: "latest", base: 121, swing: 4, digits: 0, every: 4, sources: [
            Source(group: "withings", provider: "withings", device: "bp_monitor", offset: 0),
        ], basis: "reading"),
        Series(code: "sleep_total", unit: "s", strategy: "first_available", base: 26_100, swing: 1_800, digits: 0, sources: [
            Source(group: "garmin", provider: "garmin", device: "watch", offset: 0),
            Source(group: "apple_watch", provider: "apple_health", device: "watch", offset: -900),
        ], basis: "sessions"),
    ]

    /// Catalogue entries beside the shell's four; vo2max has no data, for "show metrics without data".
    static let extraMetrics: [Entry] = [
        Entry(code: "spo2", section: "respiration", unit: "%", kind: "sample", aggregation: "intensive"),
        Entry(code: "hrv_rmssd_nightly", section: "heart", unit: "ms", kind: "daily_value", aggregation: "daily_summary"),
        Entry(code: "bp_systolic", section: "blood pressure", unit: "mmHg", kind: "sample", aggregation: "latest", group: "bp_reading", family: "blood_pressure"),
        Entry(code: "sleep_total", section: "sleep", unit: "s", kind: "interval", aggregation: "sleep_derived", family: "sleep"),
        Entry(code: "vo2max", section: "heart", unit: "mL/kg/min", kind: "sample", aggregation: "latest"),
    ]

    struct Entry {
        var code: String
        var section: String
        var unit: String
        var kind: String
        var aggregation: String
        var group: String?
        var family: String?

        var json: [String: Any] {
            var entry: [String: Any] = [
                "code": code, "section": section, "unit": unit, "kinds": [kind], "aggregation": aggregation,
                "windows": ["local_day"], "strategies": ["single_source", "first_available", "mean_across_sources", "latest"],
                "plausible_range": [0, 100_000], "provider_scoped": false, "selection_only": false,
            ]
            if let group { entry["group"] = group }
            if let family { entry["family"] = family }
            return entry
        }
    }

    let state: State
    let today = LocalDate.today(in: ExploreFixture.zone)
    var first: LocalDate { today.adding(days: -Self.history) }

    init(state: State) {
        self.state = state
    }

    static func spec(_ code: String) -> Series? {
        series.first { $0.code == code }
    }

    static func catalogue(_ code: String) -> [String: Any]? {
        FakeServer.allMetrics.first { $0["code"] as? String == code }
    }

    /// Days since 1970 of a local date: the seed of every synthetic value.
    static func number(_ date: LocalDate) -> Int {
        Int(date.start(in: .gmt).timeIntervalSince1970 / 86_400)
    }

    /// The measurement id behind a source's value on a date (digits only, as the API's ids).
    static func record(_ spec: Series, source: Int, date: LocalDate) -> String {
        let metric = (series.firstIndex { $0.code == spec.code } ?? 0) + 1
        return "\(metric)\(source + 1)\(number(date))"
    }

    /// A source's own value on a date, nil when it has none.
    func value(_ spec: Series, source index: Int, on date: LocalDate) -> Double? {
        let source = spec.sources[index]
        let n = Self.number(date)
        guard date >= first, date <= today, source.degraded == nil, n % spec.every == 0 else { return nil }
        if let gap = spec.gap, n % gap == 0 { return nil }
        let raw = spec.base + source.offset + spec.swing * sin(Double(n) / 5) + spec.swing * 0.3 * cos(Double(n) / 2.3)
        let scale = pow(10, Double(spec.digits))
        return (raw * scale).rounded() / scale
    }

    // MARK: Resolution (overrides applied the way the engine applies them)

    func resolve(_ code: String, on date: LocalDate) -> [String: Any] {
        let window: [String: Any] = ["kind": "local_day", "local_date": date.description, "key": date.description]
        guard let spec = Self.spec(code) else {
            return ["status": "no_data", "explanation": "No source had a value.", "window": window]
        }
        let live = state.overrides.filter { $0.active && $0.metric == code && $0.date == date.description }
        let excluded = Set(live.filter { $0.action == "exclude_input" }.compactMap(\.inputID))
        let forced = live.first { $0.action == "force_source" }?.group
        let set = live.first { $0.action == "set_value" }
        var inputs: [[String: Any]] = []
        var valid: [(index: Int, value: Double)] = []
        for (i, source) in spec.sources.enumerated() {
            let record = Self.record(spec, source: i, date: date)
            guard let value = value(spec, source: i, on: date) else {
                inputs.append(["group": source.group, "status": "no_data", "reason": source.degraded ?? "no value in this window"])
                continue
            }
            if excluded.contains(record) {
                inputs.append(["group": source.group, "status": "excluded", "reason": "record \(record) excluded by override"])
                continue
            }
            inputs.append([
                "group": source.group, "status": "used", "selected": false, "value": value, "basis": spec.basis, "coverage": 1,
                "record_refs": [record], "sources": [["provider": source.provider, "connection_id": Self.connection(source.provider)]],
            ])
            valid.append((i, value))
        }
        var result: [String: Any] = [
            "unit": spec.unit, "window": window, "computed_at": "\(date.description)T23:30:00Z",
            "rule": ["ref": "builtin:\(code):1", "version": 1, "strategy": spec.strategy],
            "overrides": ["applied": live.map(\.id), "ignored": [String]()],
        ]
        let forcedPick = forced.flatMap { group in valid.first { spec.sources[$0.index].group == group } }
        let picks: [(index: Int, value: Double)]
        if let forcedPick {
            picks = [forcedPick]
        } else if spec.strategy == "mean_across_sources" {
            picks = valid
        } else {
            picks = valid.prefix(1).map(\.self)
        }
        for pick in picks {
            if let k = inputs.firstIndex(where: { $0["group"] as? String == spec.sources[pick.index].group }) { inputs[k]["selected"] = true }
        }
        result["inputs"] = inputs
        let format = { (v: Double) in Self.text(v, unit: spec.unit) }
        if let set, let value = set.value {
            result["status"] = "overridden"
            result["value"] = value
            result["explanation"] = "Set manually to \(format(value)): \(set.note ?? ""). Computed value kept."
            return result
        }
        guard !picks.isEmpty else {
            result["status"] = "no_data"
            result["explanation"] = "No source had a value."
            return result
        }
        let label = { (i: Int) in Self.groupLabel(spec.sources[i].group) }
        if picks.count > 1 {
            let mean = picks.map(\.value).reduce(0, +) / Double(picks.count)
            result["status"] = "calculated"
            result["value"] = (mean * 10).rounded() / 10
            result["explanation"] = "Mean of \(picks.count) eligible sources: "
                + picks.map { "\(label($0.index)) \(format($0.value))" }.joined(separator: "; ") + "."
            return result
        }
        let pick = picks[0]
        result["value"] = pick.value
        result["selected"] = spec.sources[pick.index].group
        if forcedPick != nil {
            result["status"] = "direct"
            result["explanation"] = "Forced to \(label(pick.index)): \(format(pick.value))."
        } else if pick.index == 0 {
            result["status"] = "direct"
            result["explanation"] = "First available source: \(label(pick.index)) \(format(pick.value))."
        } else {
            let skipped = spec.sources[0]
            result["status"] = "fallback"
            result["explanation"] = "\(Self.groupLabel(skipped.group)) had no value\(skipped.degraded == nil ? "" : " (stream degraded)"). Fell back to \(label(pick.index)): \(format(pick.value))."
            if skipped.degraded != nil { result["warnings"] = [["code": "preferred_source_unavailable", "group": skipped.group]] }
        }
        return result
    }

    /// The resolved number of a day, nil without one.
    func number(_ code: String, on date: LocalDate) -> Double? {
        resolve(code, on: date)["value"] as? Double
    }

    // MARK: Endpoints

    /// `ignored`: with include_ignored, the records a source filter held raw (FakeServer+SourceFilter.swift).
    func inventory(ignored: [[String: Any]]? = nil) -> Reply {
        let lastAt = { (date: LocalDate) in Self.instant(date, hour: 7) }
        var items: [[String: Any]] = Self.series.compactMap { spec in
            guard let meta = Self.catalogue(spec.code) else { return nil }
            let days = (0...Self.history).map { today.adding(days: -$0) }.filter { number(spec.code, on: $0) != nil }
            guard let last = days.first else { return nil }
            let devices = Self.devices(spec.sources)
            return [
                "kind": "metric", "code": spec.code, "count": days.count * (spec.basis == "samples" ? 288 : 1), "days": days.count,
                "first_date": days.last!.description, "last_date": last.description,
                "first_at": lastAt(days.last!), "last_at": lastAt(last),
                "latest": ["local_date": last.description, "value": number(spec.code, on: last)!, "unit": spec.unit],
                "providers": Array(NSOrderedSet(array: spec.sources.map(\.provider))) as! [String],
                "devices": devices, "origins": spec.sources.compactMap(\.origin).map { ["key": $0.key, "name": $0.name] },
                "metric": meta,
            ]
        }
        let item = { (kind: String, code: String, extra: [String: Any]) -> [String: Any] in
            [
                "kind": kind, "code": code, "count": 40, "days": 30, "first_date": first.description, "last_date": today.description,
                "first_at": lastAt(first), "last_at": lastAt(today), "providers": [String](), "devices": [[String: String]](),
                "origins": [[String: String]](),
            ].merging(extra) { $1 }
        }
        items += [
            item("group", "bp_reading", [
                "components": ["bp_systolic", "bp_diastolic", "bp_pulse"], "providers": ["withings"],
                "latest": ["local_date": today.description, "components": ["bp_systolic": 121, "bp_diastolic": 79]],
            ]),
            item("sleep", "sleep", ["providers": ["garmin", "apple_health"], "devices": [Self.watch], "latest": ["local_date": today.description, "value": 26_100]]),
            item("workouts", "workouts", ["providers": ["garmin"], "latest": ["local_date": today.description, "text": "running"]]),
            item("event", "irregular_rhythm", [
                "providers": ["apple_health"], "origins": [["key": Self.phoneApp.key, "name": Self.phoneApp.name]], "days": 2,
                "latest": ["local_date": today.description, "level": "low"],
            ]),
            item("analyte", "ldl_c", [
                "analyte": ["code": "ldl_c", "name": "LDL cholesterol", "canonical_unit": "mmol/L"], "days": 3,
                "latest": ["local_date": today.description, "value": 2.9, "unit": "mmol/L", "text": "2.9 mmol/L"],
            ]),
        ]
        var body: [String: Any] = ["items": items, "aggregates_pending": true]
        if let ignored { body["ignored"] = ignored }
        return Self.json(200, body)
    }

    func metric(_ code: String) -> Reply {
        guard let entry = Self.catalogue(code) else { return Self.problem(404, "not_found", "no metric with the code \(code)") }
        return Self.json(200, entry)
    }

    func daily(_ query: Query) -> Reply {
        guard let range = query.dates(max: 366) else { return Self.problem(422, "validation_failed", "start_date and end_date must span at most 366 dates") }
        let codes = query.list("metrics")
        let days = range.map { date in
            ["local_date": date.description, "metrics": Dictionary(uniqueKeysWithValues: codes.map { ($0, resolve($0, on: date)) })] as [String: Any]
        }
        return Self.json(200, ["timezone": Self.timezone, "days": days])
    }

    /// Plain statistics of the resolved values from `from` through `to`, as the server's rollups.
    func rollup(_ code: String, from: LocalDate, to: LocalDate) -> [String: Any] {
        var dates: [LocalDate] = []
        var d = from
        while d <= to {
            dates.append(d)
            d = d.adding(days: 1)
        }
        let values = dates.compactMap { number(code, on: $0) }
        var out: [String: Any] = [
            "start_date": from.description, "end_date": to.description, "days": dates.count, "n": values.count,
            "coverage": dates.isEmpty ? 0 : Double(values.count) / Double(dates.count),
        ]
        if !values.isEmpty {
            out["mean"] = (values.reduce(0, +) / Double(values.count) * 10).rounded() / 10
            out["min"] = values.min()!
            out["max"] = values.max()!
            if Self.spec(code)?.additive == true { out["sum"] = values.reduce(0, +) }
        }
        return out
    }

    func trend(_ query: Query) -> Reply {
        guard let code = query.value("metric"), let range = query.dates(max: 3660), let start = range.first, let end = range.last else {
            return Self.problem(422, "validation_failed", "metric, start_date and end_date are required")
        }
        let grain = query.value("grain") ?? "week"
        var calendar = Calendar(identifier: .iso8601)
        calendar.timeZone = .gmt
        var buckets: [[String: Any]] = []
        var from = start
        while from <= end {
            let instant = from.start(in: .gmt)
            let next: LocalDate
            if grain == "month" {
                let month = calendar.dateInterval(of: .month, for: instant)!
                next = LocalDate(month.end, in: .gmt)
            } else {
                let week = calendar.dateInterval(of: .weekOfYear, for: instant)!
                next = LocalDate(week.end, in: .gmt)
            }
            buckets.append(rollup(code, from: from, to: min(next.adding(days: -1), end)))
            from = next
        }
        var out: [String: Any] = [
            "metric": code, "grain": grain, "timezone": Self.timezone, "start_date": start.description, "end_date": end.description, "buckets": buckets,
        ]
        if let spec = Self.spec(code) {
            out["unit"] = spec.unit
            out["rule"] = ["ref": "builtin:\(code):1", "version": 1, "strategy": spec.strategy]
        }
        return Self.json(200, out)
    }

    func sourceSeries(_ query: Query) -> Reply {
        guard let code = query.value("metric"), let start = query.value("start").flatMap(Self.date), let end = query.value("end").flatMap(Self.date) else {
            return Self.problem(422, "validation_failed", "metric, start and end are required")
        }
        guard let spec = Self.spec(code), let meta = Self.catalogue(code) else {
            return Self.json(200, ["metric": code, "unit": "", "aggregation": "intensive", "grain": "day", "timezone": Self.timezone, "behind": false, "sources": [Any]()])
        }
        var sources: [[String: Any]] = []
        for (i, source) in spec.sources.enumerated() where source.degraded == nil {
            var points: [[String: Any]] = []
            var d = start
            while d < end {
                if let v = value(spec, source: i, on: d) {
                    var point: [String: Any] = ["local_date": d.description, "n": spec.basis == "samples" ? 288 : 1]
                    if spec.additive { point["sum"] = v } else { point["mean"] = v; point["min"] = v; point["max"] = v }
                    if spec.basis == "daily_value" { point["daily_value"] = v }
                    points.append(point)
                }
                d = d.adding(days: 1)
            }
            var entry: [String: Any] = [
                "provider": source.provider, "connection_id": Self.connection(source.provider), "group": source.group,
                "rule_status": "used", "points": points, "device": Self.device(source.device),
            ]
            if let origin = source.origin { entry["origin"] = ["key": origin.key, "name": origin.name] }
            sources.append(entry)
        }
        let aggregation = (meta["aggregation"] as? String).map { $0 == "sleep_derived" ? "daily_summary" : $0 } ?? "intensive"
        return Self.json(200, [
            "metric": code, "unit": spec.unit, "aggregation": aggregation, "grain": "day", "timezone": Self.timezone,
            "behind": false, "sources": sources, "rule": ["ref": "builtin:\(code):1", "version": 1],
        ])
    }

    func coverage(_ query: Query) -> Reply {
        guard let range = query.dates(max: 366), let start = range.first, let end = range.last else {
            return Self.problem(422, "validation_failed", "start_date and end_date must span at most 366 dates")
        }
        var rows: [[String: Any]] = []
        for code in query.list("metric") {
            guard let spec = Self.spec(code) else { continue }
            for (i, source) in spec.sources.enumerated() where source.degraded == nil && !rows.contains(where: { $0["metric"] as? String == code && $0["source"] as? String == source.provider }) {
                rows.append(["metric": code, "source": source.provider, "days": range.map { value(spec, source: i, on: $0) == nil ? 0 : 0.9 }])
            }
        }
        return Self.json(200, ["start_date": start.description, "end_date": end.description, "rows": rows])
    }

    /// Every source of one metric and day, including one excluded and one outside the rule.
    func drilldown(metric code: String, key: String) -> Reply {
        guard let date = LocalDate(key) else { return Self.problem(422, "validation_failed", "window key must be a local date") }
        let window: [String: Any] = ["kind": "local_day", "local_date": key, "key": key]
        guard let spec = Self.spec(code) else {
            // A catalogue metric without data: its default rule and no sources.
            guard Self.catalogue(code) != nil else { return Self.problem(404, "not_found", "no metric with the code \(code)") }
            return Self.json(200, ["metric": code, "window": window, "rule": ["ref": "default:\(code):fake", "version": 1], "sources": [Any]()])
        }
        let records = { (provider: String, origin: String?) in
            "/api/v1/measurements?metric=\(code)&provider=\(provider)\(origin.map { "&origin=\($0)" } ?? "")&start_date=\(key)&end_date=\(key)"
        }
        var sources: [[String: Any]] = spec.sources.enumerated().map { i, source in
            var entry: [String: Any] = [
                "group": source.group, "rule_status": "used", "provider": source.provider,
                "connection_id": Self.connection(source.provider), "device": Self.device(source.device),
                "records": ["href": records(source.provider, nil)],
            ]
            if let origin = source.origin { entry["origin"] = ["key": origin.key, "name": origin.name] }
            if let v = value(spec, source: i, on: date) {
                entry["values"] = [spec.basis == "samples" ? "samples" : spec.basis == "intervals" ? "interval_sum" : "daily_value": spec.basis == "samples" ? 288 : v]
                entry["count"] = spec.basis == "samples" ? 288 : 1
                entry["provenance"] = ["raw_payload_ids": ["4411"], "normalizer": "\(source.provider).daily_summary@3", "fetched_at": Self.instant(date, hour: 21)]
            } else {
                entry["reason"] = source.degraded ?? "no value in this window"
                entry["values"] = [String: Double]()
            }
            return entry
        }
        if spec.sources.contains(where: { $0.provider == "garmin" }) {
            sources.append([
                "group": NSNull(), "rule_status": "excluded", "reason": "exclude: provider=apple_health relayed=true", "provider": "apple_health",
                "connection_id": Self.connection("apple_health"),
                "origin": ["key": "com.garmin.connect.mobile", "relayed": true, "relayed_provider": "garmin"],
                "values": ["daily_value": spec.base], "records": ["href": records("apple_health", "com.garmin.connect.mobile")],
            ])
        }
        sources.append([
            "group": NSNull(), "rule_status": "not_in_rule", "provider": "manual", "connection_id": Self.connection("manual"),
            "values": [String: Double](), "reason": "manual entries are outside the rule",
        ])
        return Self.json(200, [
            "metric": code, "window": window,
            "rule": ["ref": "builtin:\(code):1", "version": 1, "strategy": spec.strategy], "sources": sources,
        ])
    }

    /// The drilldown's records: one row per source and day, or a sample every 5 minutes for
    /// sampled metrics, paged by an opaque cursor.
    func measurements(_ query: Query) -> Reply {
        guard let range = query.dates(max: 366) else { return Self.problem(422, "validation_failed", "invalid dates") }
        let codes = query.list("metric")
        let providers = query.list("provider")
        let origins = query.list("origin")
        var rows: [[String: Any]] = []
        for spec in Self.series where codes.isEmpty || codes.contains(spec.code) {
            if !origins.isEmpty, origins.contains("com.garmin.connect.mobile"), providers.contains("apple_health") {
                // The relayed copy of garmin, excluded by the rule.
                guard let garmin = spec.sources.firstIndex(where: { $0.provider == "garmin" }) else { continue }
                rows += range.flatMap { rowsFor(spec, source: garmin, date: $0, provider: "apple_health", idPrefix: "8") }
                continue
            }
            for (i, source) in spec.sources.enumerated() where providers.isEmpty || providers.contains(source.provider) {
                rows += range.flatMap { rowsFor(spec, source: i, date: $0, provider: source.provider, idPrefix: "") }
            }
        }
        let limit = query.value("limit").flatMap(Int.init) ?? 500
        var offset = 0
        if let cursor = query.value("cursor") {
            guard cursor.hasPrefix("x"), let parsed = Int(cursor.dropFirst()), (0..<rows.count).contains(parsed) else {
                return Self.problem(422, "validation_failed", "invalid or expired cursor")
            }
            offset = parsed
        }
        let end = min(offset + max(limit, 1), rows.count)
        var page: [String: Any] = ["measurements": Array(rows[offset..<end]), "has_more": end < rows.count]
        if end < rows.count { page["next_cursor"] = "x\(end)" }
        return Self.json(200, page)
    }

    private func rowsFor(_ spec: Series, source i: Int, date: LocalDate, provider: String, idPrefix: String) -> [[String: Any]] {
        guard let v = value(spec, source: i, on: date) else { return [] }
        let record = idPrefix + Self.record(spec, source: i, date: date)
        let row = { (id: String, at: Date, value: Double, kind: String) -> [String: Any] in
            [
                "id": id, "metric": spec.code, "kind": kind, "start_at": Self.iso(at), "end_at": NSNull(), "tz_offset_min": 60,
                "local_date": date.description, "value": value, "unit": spec.unit, "source_value": NSNull(), "source_unit": NSNull(),
                "quality_flags": idPrefix.isEmpty ? 0 : 8, "group_id": NSNull(),
                "source": [
                    "provider": provider, "connection_id": Self.connection(provider), "device": NSNull(),
                    "device_type": spec.sources[i].device, "origin": idPrefix.isEmpty ? NSNull() : "com.garmin.connect.mobile",
                    "external_id": NSNull(), "dedupe_key": String(format: "%032x", Int(id)!),
                ],
                "provenance": [
                    "raw_payload_id": "4411", "normalizer": "\(provider).daily_summary@3", "ingested_at": Self.instant(date, hour: 21),
                    "normalized_at": Self.instant(date, hour: 21), "superseded_at": NSNull(), "superseded_by": NSNull(),
                    "deleted_at": NSNull(), "deleted_by_raw_id": NSNull(),
                ],
            ]
        }
        let start = date.start(in: Self.zone)
        guard spec.basis == "samples" else {
            return [row(record, start.addingTimeInterval(7 * 3600), v, spec.basis == "daily_value" ? "daily_value" : "sample")]
        }
        // 288 samples, the first carrying the record id the rule lists.
        return (0..<288).map { k in
            let wobble = spec.swing * sin(Double(k) / 18) + Double(k % 5) * 0.2
            return row(k == 0 ? record : "\(record)\(String(format: "%03d", k))", start.addingTimeInterval(Double(k) * 300), ((v + wobble) * 10).rounded() / 10, "sample")
        }
    }

    func listOverrides(_ query: Query) -> Reply {
        let codes = query.list("metric")
        let overrides = state.overrides.filter { codes.isEmpty || codes.contains($0.metric) }.map(\.json)
        return Self.json(200, ["overrides": overrides, "has_more": false])
    }

    /// The chain of a record: one earlier version it replaced, the record, nothing later.
    func provenance(entity: String, id: String) -> Reply {
        guard ["measurement", "group", "sleep", "workout"].contains(entity) else { return Self.problem(422, "validation_failed", "unknown entity") }
        guard id.allSatisfy(\.isNumber) else { return Self.problem(404, "not_found", "no \(entity) \(id)") }
        let version = { (vid: String, superseded: Bool) -> [String: Any] in
            [
                "id": vid, "superseded_by": superseded ? id : NSNull(), "record": ["id": vid, "value": superseded ? 53 : 52],
                "provider": "garmin", "connection_id": Self.connection("garmin"), "connection_mode": "in_process", "client": NSNull(),
                "batch": ["id": "11111111-1111-4111-8111-111111111111", "source_kind": "sync", "migration_source": NSNull(), "received_at": "2026-01-01T21:02:11Z"],
                "raw": [
                    "id": "4411", "stream": "daily_summary", "external_key": "2026-01-01", "version": 1, "content_sha256": String(repeating: "ab", count: 32),
                    "content_type": "application/json", "size_bytes": 2048, "fetched_at": "2026-01-01T21:02:11Z", "stored_at": "2026-01-01T21:02:11Z",
                    "request_meta": [String: Any](), "shape_fingerprint": "fp", "status": "normalized",
                ],
                "normalizer": ["name": "garmin.daily_summary", "version": 3, "git_sha": "abcdef0123456789"],
                "fetched_at": "2026-01-01T21:02:11Z", "ingested_at": "2026-01-01T21:02:11Z", "normalized_at": "2026-01-01T21:02:12Z",
                "corrected_at": superseded ? NSNull() : "2026-01-01T22:00:00Z", "superseded_at": superseded ? "2026-01-01T22:00:00Z" : NSNull(),
                "deleted_at": NSNull(), "deleted_by": NSNull(),
            ]
        }
        return Self.json(200, ["entity": entity, "row": version(id, false), "earlier": [version("9182000", true)], "later": [Any]()])
    }

    // MARK: Helpers

    /// A request's query items.
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

        /// start_date through end_date, at most `max` dates.
        func dates(max: Int) -> [LocalDate]? {
            guard let start = value("start_date").flatMap(LocalDate.init), let end = value("end_date").flatMap(LocalDate.init), start <= end else { return nil }
            var out: [LocalDate] = []
            var d = start
            while d <= end {
                out.append(d)
                guard out.count <= max else { return nil }
                d = d.adding(days: 1)
            }
            return out
        }
    }

    static func groupLabel(_ group: String) -> String {
        ["apple_watch": "Apple Watch", "garmin": "Garmin", "whoop": "WHOOP", "withings": "Withings"][group] ?? group
    }

    static func text(_ value: Double, unit: String) -> String {
        let number = value.rounded() == value ? String(Int(value)) : String(value)
        return unit == "count" ? "\(number) steps" : "\(number) \(unit)"
    }

    static func connection(_ provider: String) -> String {
        let index = ["withings": 1, "garmin": 2, "apple_health": 3, "whoop": 4, "manual": 5][provider] ?? 9
        return "conn_" + String(format: "%032x", index)
    }

    static func device(_ type: String) -> [String: String] {
        switch type {
        case "watch": watch
        case "scale": scale
        case "band": band
        default: ["type": type]
        }
    }

    static func devices(_ sources: [Source]) -> [[String: String]] {
        var seen = Set<String>()
        return sources.map { device($0.device) }.filter { seen.insert($0["id"] ?? $0["type"] ?? "").inserted }
    }

    /// The local date of an RFC 3339 instant in the owner's timezone.
    static func date(_ text: String) -> LocalDate? {
        (try? RFC3339DateTranscoder().decode(text)).map { LocalDate($0, in: .gmt) }
    }

    static func instant(_ date: LocalDate, hour: Int) -> String {
        iso(date.start(in: zone).addingTimeInterval(TimeInterval(hour * 3600)))
    }

    static func iso(_ date: Date) -> String {
        date.formatted(Date.ISO8601FormatStyle())
    }

    static func json(_ status: Int, _ object: [String: Any]) -> Reply {
        Reply(status: status, body: try! JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]))
    }

    static func problem(_ status: Int, _ code: String, _ detail: String, errors: [(String, String)] = []) -> Reply {
        let titles = ["validation_failed": "Validation failed", "not_found": "Not found", "conflict": "Conflict"]
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

// MARK: Writes

extension ExploreFixture.State {
    /// POST /overrides, validated as the server does (internal/api/overrides.go): one active
    /// force_source and one active set_value per window.
    mutating func create(_ body: Data) -> Reply {
        typealias F = ExploreFixture
        guard let input = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any],
              let metric = input["metric"] as? String, let window = input["window"] as? [String: Any],
              let action = input["action"] as? String, let kind = window["kind"] as? String,
              let key = window["key"] as? String, let date = window["local_date"] as? String
        else { return F.problem(422, "validation_failed", "invalid override", errors: [("/window", "is required")]) }
        let field = { (name: String) in (input[name] as? String).flatMap { $0.isEmpty ? nil : $0 } }
        var override = ExploreFixture.Override(id: "", metric: metric, kind: kind, key: key, date: date, action: action)
        switch action {
        case "exclude_input":
            guard let id = field("input_id"), id.allSatisfy(\.isNumber) else {
                return F.problem(422, "validation_failed", "invalid override", errors: [("/input_id", "is required for exclude_input and must be a measurement id")])
            }
            override.inputID = id
        case "force_source":
            guard let group = field("group") else {
                return F.problem(422, "validation_failed", "invalid override", errors: [("/group", "is required for force_source")])
            }
            override.group = group
        case "set_value":
            var errors: [(String, String)] = []
            if !(input["value"] is NSNumber) { errors.append(("/value", "is required for set_value")) }
            if field("unit") == nil { errors.append(("/unit", "is required for set_value")) }
            if field("note") == nil { errors.append(("/note", "is required for set_value")) }
            guard errors.isEmpty else { return F.problem(422, "validation_failed", "invalid override", errors: errors) }
            override.value = (input["value"] as! NSNumber).doubleValue
            override.unit = field("unit")
            override.note = field("note")
        default:
            return F.problem(422, "validation_failed", "invalid override", errors: [("/action", "must be exclude_input, force_source or set_value")])
        }
        if action != "exclude_input", overrides.contains(where: { $0.active && $0.metric == metric && $0.key == key && $0.action == action }) {
            return F.problem(409, "conflict", "an active \(action) override exists for this window; revoke it first")
        }
        override.id = String(format: "00000000-0000-4000-8000-%012d", nextOverride)
        nextOverride += 1
        overrides.append(override)
        return F.json(201, override.json)
    }

    mutating func revoke(_ id: String) -> Reply {
        guard let index = overrides.firstIndex(where: { $0.id == id }) else {
            return ExploreFixture.problem(404, "not_found", "no such override")
        }
        guard overrides[index].active else { return ExploreFixture.problem(409, "conflict", "the override is already revoked") }
        overrides[index].active = false
        overrides[index].revokedAt = "2026-01-02T08:00:00Z"
        return ExploreFixture.json(200, overrides[index].json)
    }
}
#endif
