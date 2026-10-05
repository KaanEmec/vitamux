import Foundation
import Observation
import VitamuxKit

/// The all-sources day (the panel's `/explore/{metric}/day/{date}`): the resolved result and why
/// (`GET /resolved/daily`), every source seen for the window, used, excluded or outside the rule
/// (`GET /resolved/{metric}/{window_key}/sources`), each source's raw readings overlaid
/// (`GET /measurements`, every page), and the day's overrides (`GET /overrides`) with Revoke.
@Observable
final class AllSourcesDayModel {
    typealias Source = Components.Schemas.DrilldownSource

    struct Day {
        var result: ResolvedValue?
        var timezone: String
    }

    let code: String
    let date: LocalDate
    private(set) var day: Loadable<Day> = .loading
    private(set) var sources: Loadable<[Source]> = .loading
    private(set) var overrides: Loadable<[Components.Schemas.Override]> = .loading
    private(set) var chart: Loadable<[ChartSeries]> = .loading
    /// The first record of each source (same order as `sources`), for "Trace a record".
    private(set) var firstRecords: [String?] = []
    private(set) var unit = ""
    private(set) var actionProblem: Problem?
    private(set) var version = 0

    init(code: String, date: LocalDate) {
        self.code = code
        self.date = date
    }

    var timeZone: TimeZone {
        day.value.flatMap { TimeZone(identifier: $0.timezone) } ?? .current
    }

    /// The day's overrides, newest last, active and revoked.
    var todays: [Components.Schemas.Override] {
        (overrides.value ?? []).filter { $0.window.localDate == date.description }
    }

    func changed() {
        version += 1
    }

    func load(_ client: Client?) async {
        guard let client else { return }
        async let result: Void = loadResult(client)
        async let drilldown = Loadable { try await client.getResolvedSources(path: .init(metric: self.code, windowKey: self.date.description)).ok.body.json.sources }
        async let meta = try? client.getMetric(path: .init(code: code)).ok.body.json
        sources = await drilldown
        unit = await meta?.unit ?? ""
        await result
        await loadChart(client)
    }

    private func loadResult(_ client: Client) async {
        let date = date.description
        async let daily = Loadable {
            let answer = try await client.getResolvedDaily(query: .init(startDate: date, endDate: date, metrics: [self.code])).ok.body.json
            return Day(result: answer.days.first { $0.localDate == date }?.metrics.additionalProperties[self.code], timezone: answer.timezone)
        }
        async let listed = Loadable { try await client.listOverrides(query: .init(metric: [self.code], limit: 500)).ok.body.json.value2.overrides }
        day = await daily
        overrides = await listed
    }

    /// Every page of each source's records, one series per source.
    private func loadChart(_ client: Client) async {
        let sources = sources.value ?? []
        var series: [ChartSeries] = []
        var firsts: [String?] = []
        do {
            for source in sources {
                let rows = try await records(of: source, client: client)
                firsts.append(rows.first?.id)
                guard !rows.isEmpty else { continue }
                if unit.isEmpty { unit = rows[0].unit }
                series.append(ChartSeries(label: label(source), points: rows.map { ChartPoint(x: $0.startAt, y: $0.value) }, source: source.provider))
            }
            firstRecords = firsts
            chart = .loaded(series)
        } catch {
            chart = .failed(Problem(error))
        }
    }

    /// The source's records link when it has one, else a provider filter for the day.
    private func records(of source: Source, client: Client) async throws -> [Components.Schemas.Measurement] {
        var query = Operations.ListMeasurements.Input.Query(limit: 2000)
        if let href = source.records?.href, let items = URLComponents(string: href)?.queryItems {
            let all = { (name: String) in items.filter { $0.name == name }.compactMap(\.value) }
            let one = { (name: String) in all(name).first }
            query.metric = all("metric").nilIfEmpty
            query.provider = all("provider").nilIfEmpty
            query.connection = all("connection").nilIfEmpty
            query.device = all("device").nilIfEmpty
            query.origin = all("origin").nilIfEmpty
            let transcoder = RFC3339DateTranscoder()
            query.start = one("start").flatMap { try? transcoder.decode($0) }
            query.end = one("end").flatMap { try? transcoder.decode($0) }
            query.startDate = one("start_date")
            query.endDate = one("end_date")
            if query.start == nil, query.end == nil, query.startDate == nil {
                query.startDate = date.description
                query.endDate = date.description
            }
        } else {
            guard source.ruleStatus != .notInRule || source.provider != "manual" else { return [] }
            query.metric = [code]
            query.provider = [source.provider]
            query.origin = source.origin?.key.map { [$0] }
            query.startDate = date.description
            query.endDate = date.description
        }
        var rows: [Components.Schemas.Measurement] = []
        // At most 50 pages (100,000 rows), so a broken cursor cannot loop forever.
        for _ in 0..<50 {
            let page = try await client.listMeasurements(query: query).ok.body.json
            rows += page.value2.measurements
            guard page.value1.hasMore, let next = page.value1.nextCursor else { break }
            query.cursor = next
        }
        return rows
    }

    func revoke(_ id: String, client: Client?) async {
        guard let client else { return }
        actionProblem = nil
        do {
            _ = try await client.revokeOverride(path: .init(id: id)).ok
            changed()
        } catch {
            actionProblem = Problem(error)
        }
    }

    /// "garmin", "apple_watch · Apple Health · Example Health".
    func label(_ source: Source) -> String {
        var parts = [source.group.map(groupLabel) ?? providerLabel(source.provider)]
        if source.group != nil, source.group != source.provider { parts.append(providerLabel(source.provider)) }
        if let detail = source.origin?.name ?? source.origin?.key ?? source.device?.model ?? source.device?._type { parts.append(detail) }
        return parts.joined(separator: " · ")
    }
}

extension Array {
    var nilIfEmpty: Self? { isEmpty ? nil : self }
}

extension Components.Schemas.Override {
    /// "Exclude input 1220366", "Force source garmin", "Set value 49 bpm".
    var summary: String {
        switch action {
        case .excludeInput: "Exclude input \(inputId ?? "")"
        case .forceSource: "Force source \(group.map(groupLabel) ?? "")"
        case .setValue: "Set value \(value.map(Format.number) ?? "") \(unit ?? "")".trimmingCharacters(in: .whitespaces)
        }
    }
}
