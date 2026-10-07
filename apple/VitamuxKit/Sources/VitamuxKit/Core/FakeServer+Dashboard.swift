#if DEBUG
import Foundation
import Synchronization

// The dashboard's endpoints on the fake (J22.7), the twin of web/e2e/dashboard-fake.ts: the stored
// layout (GET/PUT /settings/dashboard), GET /resolved/summary, and the alert sources
// (GET /jobs?status=dead, GET /system/status). All values are synthetic and deterministic.
//
// Seed: the curated default layout; sleep, resting heart rate, HRV (RMSSD), steps, VO2 max, weight,
// blood pressure, SpO2 (a fallback), respiratory rate, active and total energy have data; HRV (SDNN)
// has none, so its card hides. Today's steps are partial. Garmin needs reauthorization (the
// connections fixture), one job failed permanently two days ago (one eleven days ago is past the
// week), and the last backup is ten days old. `.empty` is a fresh install: no data, connections,
// jobs or backup. `.panelLayout` starts with a layout the panel saved, the backup alert dismissed.
extension FakeServer {
    /// What the fake's dashboard starts with.
    public enum DashboardInstall: Sendable {
        case synthetic, empty, panelLayout

        /// The UI tests' launch arguments: `-uitest-empty-install`, `-uitest-panel-layout`.
        public init(arguments: [String]) {
            self = arguments.contains("-uitest-empty-install") ? .empty
                : arguments.contains("-uitest-panel-layout") ? .panelLayout : .synthetic
        }
    }

    public struct DashboardCard: Sendable, Equatable {
        public var metric: String
        public var size: String
        public var hidden: Bool

        public init(_ metric: String, _ size: String, hidden: Bool = false) {
            self.metric = metric
            self.size = size
            self.hidden = hidden
        }
    }

    struct Dashboard {
        var install = DashboardInstall.synthetic
        /// The saved layout; nil until a PUT, so GET answers the curated default.
        var stored: [DashboardCard]?
        var hero: [String]?
        var dismissed: [String] = []
        /// Dates of GET /resolved/summary (nil: the owner's today), in order.
        var summaryDates: [String?] = []

        init(install: DashboardInstall = .synthetic) {
            self.install = install
            if install == .panelLayout {
                stored = FakeServer.panelLayout
                dismissed = ["backup:" + FakeServer.backupAt()]
            }
        }
    }

    /// Per host, so parallel unit tests never share a layout.
    static let dashboards = Mutex<[String: Dashboard]>([:])

    private var host: String { profile.baseURL.host() ?? "" }

    /// Resets the dashboard to `install`, as on a fresh server.
    public var dashboardInstall: DashboardInstall {
        get { Self.dashboards.withLock { $0[host]?.install ?? .synthetic } }
        set { Self.dashboards.withLock { $0[host] = Dashboard(install: newValue) } }
    }

    /// The stored layout's cards; nil while the curated default is served.
    public var storedDashboard: [DashboardCard]? {
        Self.dashboards.withLock { $0[host]?.stored }
    }

    /// The `date` of each GET /resolved/summary, in order (nil: today).
    public var summaryDates: [String?] {
        Self.dashboards.withLock { $0[host]?.summaryDates ?? [] }
    }

    /// The server's curated default (internal/api/dashboard.go).
    public static let defaultDashboard: [DashboardCard] = [
        .init("sleep", "L"), .init("resting_heart_rate", "M"), .init("hrv_rmssd_nightly", "M"), .init("hrv_sdnn", "M"),
        .init("steps", "M"), .init("vo2max", "S"), .init("weight", "S"), .init("blood_pressure", "S"), .init("spo2", "S"),
        .init("respiratory_rate", "S"), .init("active_energy", "S"), .init("total_energy", "S"),
    ]

    /// A layout as the panel saves it: reordered, resized, one card hidden and the rest dropped.
    public static let panelLayout: [DashboardCard] = [
        .init("steps", "L"), .init("weight", "S"), .init("blood_pressure", "S"),
        .init("sleep", "M"), .init("resting_heart_rate", "S", hidden: true), .init("vo2max", "S"),
    ]

    static let defaultHero = ["steps", "resting_heart_rate", "hrv_rmssd_nightly", "weight"]
    /// The owner's timezone in the fake (its current timezone period).
    static let dashboardTimeZone = timeZone.identifier

    // MARK: - Routing

    /// Answers a dashboard endpoint, or nil to leave the request to the other fixtures. On an empty
    /// install it also answers `GET /connections` with none.
    static func dashboard(method: String, url: URL, body: Data) -> Reply? {
        let host = url.host() ?? ""
        let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        let value = { (name: String) in query.first { $0.name == name }?.value }
        return dashboards.withLock { all -> Reply? in
            var state = all[host] ?? Dashboard()
            defer { all[host] = state }
            let empty = state.install == .empty
            switch (method, url.path) {
            case ("GET", "/api/v1/settings/dashboard"):
                return layout(state, isDefault: state.stored == nil)
            case ("PUT", "/api/v1/settings/dashboard"):
                return putLayout(body, state: &state)
            case ("GET", "/api/v1/resolved/summary"):
                let metrics = query.filter { $0.name == "metrics" }.flatMap { ($0.value ?? "").split(separator: ",").map(String.init) }
                guard (1...20).contains(metrics.count) else {
                    return Reply.problem(422, "validation_failed", "metrics: 1 to 20 codes")
                }
                state.summaryDates.append(value("date"))
                return summary(metrics, date: value("date").flatMap(LocalDate.init), empty: empty, compare: value("compare") == "true")
            case ("GET", "/api/v1/jobs"):
                return Reply.json(200, ["jobs": value("status") == "dead" && !empty ? deadJobs : [], "has_more": false])
            case ("GET", "/api/v1/system/status"):
                return Reply.json(200, systemStatus(empty: empty))
            case ("GET", "/api/v1/connections") where empty:
                return Reply.json(200, ["connections": []])
            default:
                return nil
            }
        }
    }

    // MARK: - Layout

    private static func layout(_ state: Dashboard, isDefault: Bool) -> Reply {
        Reply.json(200, [
            "version": 1, "cards": (state.stored ?? defaultDashboard).map(object), "hero": state.hero ?? defaultHero,
            "dismissed": state.dismissed, "is_default": isDefault,
        ])
    }

    private static func putLayout(_ body: Data, state: inout Dashboard) -> Reply {
        guard let input = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any],
              input["version"] as? Int == 1, let raw = input["cards"] as? [[String: Any]]
        else { return Reply.problem(422, "validation_failed", "invalid dashboard layout") }
        let cards = raw.compactMap { card -> DashboardCard? in
            guard let metric = card["metric"] as? String, let size = card["size"] as? String, ["S", "M", "L"].contains(size),
                  let hidden = card["hidden"] as? Bool else { return nil }
            return DashboardCard(metric, size, hidden: hidden)
        }
        guard cards.count == raw.count, cards.count <= 50, Set(cards.map(\.metric)).count == cards.count else {
            return Reply.problem(422, "validation_failed", "cards: a metric at most once, at most 50")
        }
        state.stored = cards
        if let hero = input["hero"] as? [String] { state.hero = hero }
        if let dismissed = input["dismissed"] as? [String] { state.dismissed = dismissed }
        return layout(state, isDefault: false)
    }

    private static func object(_ card: DashboardCard) -> [String: Any] {
        ["metric": card.metric, "size": card.size, "hidden": card.hidden]
    }

    // MARK: - Catalogue: the dashboard's codes, added to the shell's (`metrics` in the fixtures)

    private struct Spec {
        var unit: String, base: Double, amp: Double, aggregation: String, group: String, provider: String
        var status = "direct"
        var section: String
    }

    private static let specs: [String: Spec] = [
        "resting_heart_rate": Spec(unit: "bpm", base: 52, amp: 4, aggregation: "daily_summary", group: "whoop", provider: "whoop", section: "Heart and circulation"),
        "hrv_rmssd_nightly": Spec(unit: "ms", base: 64, amp: 10, aggregation: "daily_summary", group: "whoop", provider: "whoop", section: "Heart and circulation"),
        "steps": Spec(unit: "count", base: 8700, amp: 3000, aggregation: "additive", group: "apple_watch", provider: "apple_health", section: "Activity"),
        "vo2max": Spec(unit: "mL/kg/min", base: 48, amp: 1, aggregation: "latest", group: "apple_watch", provider: "apple_health", section: "Heart and circulation"),
        "weight": Spec(unit: "kg", base: 74.5, amp: 0.8, aggregation: "latest", group: "scale", provider: "withings", section: "Body composition"),
        "spo2": Spec(unit: "%", base: 96, amp: 1, aggregation: "intensive", group: "whoop", provider: "whoop", status: "fallback", section: "Respiration and oxygen"),
        "respiratory_rate": Spec(unit: "breaths/min", base: 14, amp: 1, aggregation: "intensive", group: "apple_watch", provider: "apple_health", section: "Respiration and oxygen"),
        "active_energy": Spec(unit: "kcal", base: 700, amp: 150, aggregation: "additive", group: "apple_watch", provider: "apple_health", section: "Activity"),
        "total_energy": Spec(unit: "kcal", base: 2400, amp: 300, aggregation: "additive", group: "whoop", provider: "whoop", section: "Activity"),
    ]

    /// Catalogue entries the dashboard needs beyond the shell's: its cards' codes (steps is already
    /// there), a code without data, the parts of the two families, and one more body metric.
    static var dashboardMetrics: [[String: Any]] {
        var out = specs.filter { $0.key != "steps" }.sorted { $0.key < $1.key }.map { code, spec in
            catalogueEntry(code, section: spec.section, unit: spec.unit, aggregation: spec.aggregation)
        }
        out.append(catalogueEntry("hrv_sdnn", section: "Heart and circulation", unit: "ms", aggregation: "daily_summary"))
        out.append(catalogueEntry("body_fat_ratio", section: "Body composition", unit: "%", aggregation: "latest", group: "body_composition"))
        for code in ["bp_systolic", "bp_diastolic", "bp_pulse"] {
            out.append(catalogueEntry(code, section: "Blood pressure", unit: code == "bp_pulse" ? "bpm" : "mmHg", aggregation: "latest",
                                      group: "bp_reading", family: "blood_pressure"))
        }
        for code in ["sleep_total", "sleep_in_bed", "sleep_deep", "sleep_light", "sleep_rem", "sleep_awake"] {
            out.append(catalogueEntry(code, section: "Sleep", unit: "s", aggregation: "sleep_derived", family: "sleep"))
        }
        return out
    }

    private static func catalogueEntry(
        _ code: String, section: String, unit: String, aggregation: String, group: String? = nil, family: String? = nil
    ) -> [String: Any] {
        var entry: [String: Any] = [
            "code": code, "section": section, "unit": unit, "kinds": aggregation == "sleep_derived" ? [] : ["daily_value"],
            "aggregation": aggregation, "windows": ["local_day"], "strategies": ["first_available"],
            "plausible_range": [0, 100_000], "provider_scoped": false, "selection_only": false,
        ]
        if let group { entry["group"] = group }
        if let family { entry["family"] = family }
        return entry
    }

    // MARK: - Resolved summary

    static func today() -> LocalDate {
        LocalDate.today(in: TimeZone(identifier: dashboardTimeZone)!)
    }

    private static func summary(_ metrics: [String], date: LocalDate?, empty: Bool, compare: Bool) -> Reply {
        let today = today()
        let on = date ?? today
        var out: [String: Any] = [:]
        for code in metrics { out[code] = metricSummary(code, on: on, today: today, empty: empty, compare: compare) }
        return Reply.json(200, ["date": on.description, "timezone": dashboardTimeZone, "metrics": out])
    }

    private static func hasData(_ code: String, empty: Bool) -> Bool {
        !empty && (specs[code] != nil || code == "sleep" || code == "blood_pressure")
    }

    private static func metricSummary(_ code: String, on date: LocalDate, today: LocalDate, empty: Bool, compare: Bool) -> [String: Any] {
        let dates = (0..<30).map { date.adding(days: $0 - 29) }
        let has = hasData(code, empty: empty)
        let series = dates.map { point(code, $0) }
        let spec = specs[code]
        var value: [String: Any] = ["status": "no_data", "explanation": "No source had a value."]
        if has {
            let family = spec == nil
            let provider = spec?.provider ?? (code == "sleep" ? "whoop" : "withings")
            value = [
                "status": spec?.status ?? "direct", "value": series[29],
                "inputs": [[
                    "group": spec?.group ?? (family && code == "sleep" ? "whoop" : "bp_monitor"), "status": "used", "selected": true,
                    "sources": [["provider": provider]],
                ]],
                "explanation": "Synthetic value.",
            ]
            if let spec { value["unit"] = spec.unit }
            if code == "steps", date == today { value["partial"] = true }
        }
        var out: [String: Any] = [
            "metric": code, "value": value,
            "sparkline": zip(dates, series).map { day, reading -> [String: Any] in
                has ? ["local_date": day.description, "status": "direct", "value": reading] : ["local_date": day.description, "status": "no_data"]
            },
            "stats": [7, 30, 90].map { rollup(code, series, days: $0, end: date, empty: !has) },
        ]
        if let spec { out["unit"] = spec.unit }
        if compare {
            // `compare=true` (the metric detail): each span against the one before it.
            out["comparisons"] = [7, 30, 90, 365].map { n -> [String: Any] in
                let days = (0..<2 * n).map { date.adding(days: $0 - 2 * n + 1) }
                let all = days.map { point(code, $0) }
                return ["days": n, "current": rollup(code, Array(all.suffix(n)), days: n, end: date, empty: !has),
                        "previous": rollup(code, Array(all.prefix(n)), days: n, end: date.adding(days: -n), empty: !has)]
            }
        }
        return out
    }

    /// A slow wave plus a small date-keyed wobble, as the panel's fake.
    private static func point(_ code: String, _ date: LocalDate) -> Any {
        let n = date.month * 31 + date.day
        let wobble = Double((n * 37) % 11) / 10 - 0.5
        if code == "sleep" {
            let total = 26_000 + wobble * 3000
            return ["sleep_total": total, "sleep_in_bed": total + 2400, "sleep_deep": 4700, "sleep_light": total - 4700 - 8200 - 1400,
                    "sleep_rem": 8200, "sleep_awake": 1400]
        }
        if code == "blood_pressure" {
            return ["bp_systolic": 118 + (wobble * 8).rounded(), "bp_diastolic": 76 + (wobble * 4).rounded(), "bp_pulse": 58]
        }
        guard let spec = specs[code] else { return NSNull() }
        let v = spec.base + wobble * spec.amp * 2
        return spec.aggregation == "additive" ? v.rounded() : (v * 10).rounded() / 10
    }

    private static func rollup(_ code: String, _ series: [Any], days: Int, end: LocalDate, empty: Bool) -> [String: Any] {
        let tail = Array(series.suffix(days))
        var out: [String: Any] = [
            "start_date": end.adding(days: 1 - days).description, "end_date": end.description, "days": days,
            "n": empty ? 0 : tail.count, "coverage": empty ? 0 : Double(tail.count) / Double(days),
        ]
        guard !empty else { return out }
        func stat(_ xs: [Double]) -> [String: Any] {
            ["n": xs.count, "mean": xs.reduce(0, +) / Double(xs.count), "min": xs.min()!, "max": xs.max()!]
        }
        let part = { (key: String) in tail.compactMap { ($0 as? [String: Double])?[key] } }
        switch code {
        case "sleep":
            out["components"] = ["sleep_total": stat(part("sleep_total"))]
        case "blood_pressure":
            out["components"] = ["bp_systolic": stat(part("bp_systolic")), "bp_diastolic": stat(part("bp_diastolic"))]
        default:
            let s = stat(tail.compactMap { $0 as? Double })
            out["mean"] = s["mean"]
            out["min"] = s["min"]
            out["max"] = s["max"]
        }
        return out
    }

    // MARK: - Alert sources

    /// The newest backup: ten days before today at 03:00 UTC (fixed per day, so its alert key is stable).
    static func backupAt() -> String {
        today().adding(days: -10).description + "T03:00:00Z"
    }

    private static func instant(daysAgo: Int) -> String {
        Date.now.addingTimeInterval(-Double(daysAgo) * 86_400).formatted(.iso8601)
    }

    private static var deadJobs: [[String: Any]] {
        [(2, 1), (11, 2)].map { daysAgo, index in
            [
                "id": "00000000-0000-4000-8000-0000000000a\(index)", "kind": "sync", "status": "dead",
                "connection_id": "conn_" + String(format: "%032x", 2), "priority": 0, "attempts": 5, "max_attempts": 5,
                "payload": [String: Any](), "run_at": instant(daysAgo: daysAgo), "created_at": instant(daysAgo: daysAgo),
                "started_at": instant(daysAgo: daysAgo), "finished_at": instant(daysAgo: daysAgo),
            ]
        }
    }

    private static func systemStatus(empty: Bool) -> [String: Any] {
        [
            "versions": ["app": "0.0.0-fake", "commit": "fake", "schema": "1", "postgres": "17"],
            "database_size_bytes": 1_048_576, "blob_size_bytes": 524_288,
            "last_backup_at": empty ? NSNull() : backupAt(), "degraded_connections": [], "failing_jobs": [],
        ]
    }

    // MARK: - Responses

}
#endif
