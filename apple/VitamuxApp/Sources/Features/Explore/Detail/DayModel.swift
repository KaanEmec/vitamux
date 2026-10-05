import Foundation
import Observation
import VitamuxKit

/// The Day view of a metric with `intraday` (J22.26; the panel's DayChart): one local day through
/// the catalogue's ladder (`DayLadder`). The 24-hour span reads the default bucket; zooming picks
/// a finer step for the visible window and loads it after the gesture settles, never drawing a
/// source finer than its native spacing. Each layer is GET /sources/series at the step (raw rows
/// paged) and GET /resolved/series at the bucket the sources allow; its chart series are built
/// once when it loads. Overlays: the resolved sleep episode, workouts and now. Sleep-related
/// metrics open on the 24 hours around the night instead of the calendar day.
@Observable
final class DayModel {
    typealias Point = Components.Schemas.ResolvedPoint
    typealias SourcePoint = Components.Schemas.SourcePoint

    /// One source's points at the step it is drawn at.
    struct DaySource: Identifiable {
        let id: String
        let provider: String
        /// "Apple Health · Synthetic Watch".
        let label: String
        let device: String?
        let origin: String?
        let used: Bool
        let spacing: Double?
        var step: IntradayStep
        var points: [SourcePoint]
    }

    /// One zoom level: the resolved points and each source's points over `span`, with what the
    /// chart draws.
    struct Layer: Identifiable {
        let id: Int
        let step: IntradayStep
        let bucket: IntradayStep
        let span: ClosedRange<Date>
        let points: [Point]
        let sources: [DaySource]
        let rule: Components.Schemas.RuleRef?
        let xs: [Date]
        let values: [Double?]
        let status: [DataStatus?]
        let providers: [[String]?]
        let resolved: ChartSeries
        let band: ChartBand?
        /// Each source's own series, in `sources` order.
        let series: [ChartSeries]

        var hasValues: Bool {
            values.contains { $0 != nil } || series.contains { $0.points.contains { $0.y != nil } }
        }
    }

    /// The bucket a tap or a table row opened.
    struct Pick: Identifiable {
        let layer: Layer
        let index: Int
        var id: String { "\(layer.id)-\(index)" }
        var point: Point { layer.points[index] }
    }

    let code: String
    let unit: String
    let additive: Bool
    let ladder: DayLadder
    /// Sleep-related metrics (measured mostly overnight) open on the night.
    let opensOnNight: Bool

    private(set) var date: LocalDate?
    private(set) var timeZone: TimeZone = .current
    private(set) var domain: ClosedRange<Date>?
    private(set) var overlays: [ChartOverlay] = []
    /// The night the view is centred on, when it is.
    private(set) var night: ClosedRange<Date>?
    private(set) var layer: Layer?
    private(set) var step: IntradayStep
    private(set) var loading = false
    private(set) var problem: Problem?
    /// The zoomed window the chart shows (nil: the whole span), read and set by the chart.
    private(set) var window: ClosedRange<Date>?
    var picked: Pick?

    @ObservationIgnored private var base: Layer?
    @ObservationIgnored private var detail: Layer?
    @ObservationIgnored private var client: Client?
    @ObservationIgnored private var settle: Task<Void, Never>?
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private var layers = 0

    init(metric: Components.Schemas.Metric, intraday: Components.Schemas.Intraday) {
        code = metric.code
        unit = metric.unit
        additive = metric.aggregation == .additive
        ladder = DayLadder(intraday)
        step = ladder.coarsest
        opensOnNight = Self.sleepRelated(metric)
    }

    /// Intensive metrics of the sleep, respiration and temperature sections are read mostly at
    /// night (SpO2, respiratory rate, skin temperature, sleep movement): catalogue metadata, no
    /// per-metric code.
    static func sleepRelated(_ metric: Components.Schemas.Metric) -> Bool {
        let section = metric.section.lowercased()
        return metric.aggregation == .intensive && ["sleep", "respiration", "temperature"].contains { section.hasPrefix($0) }
    }

    // MARK: Loading

    func load(_ client: Client?, date: LocalDate) async {
        guard let client else { return }
        self.client = client
        generation += 1
        let mine = generation
        settle?.cancel()
        (self.date, domain, night, layer, base, detail, window, picked, problem, overlays) = (date, nil, nil, nil, nil, nil, nil, nil, nil, [])
        step = ladder.coarsest
        loading = true
        defer { if mine == generation { loading = false } }

        async let sleep = try? client.getResolvedSleep(query: .init(startDate: date.adding(days: -1).description, endDate: date.adding(days: 1).description)).ok.body.json
        async let sport = try? client.getResolvedWorkouts(query: .init(startDate: date.adding(days: -1).description, endDate: date.adding(days: 1).description)).ok.body.json
        let (nights, workouts) = await (sleep, sport)
        guard mine == generation else { return }
        let zone = TimeZone(identifier: nights?.timezone ?? workouts?.timezone ?? "") ?? .current
        timeZone = zone
        let midnight = date.start(in: zone)
        let length = date.adding(days: 1).start(in: zone).timeIntervalSince(midnight)
        var span = midnight ... midnight.addingTimeInterval(length)
        let episodes = (nights?.nights ?? []).compactMap { $0.episode.map { ($0.start ... $0.end, $0) } }
        if opensOnNight, let tonight = nights?.nights.first(where: { $0.localDate == date.description })?.episode {
            // The 24 hours around the night's middle, on whole half hours of the local clock.
            let middle = tonight.start.addingTimeInterval(tonight.end.timeIntervalSince(tonight.start) / 2)
            let from = midnight.addingTimeInterval(((middle.timeIntervalSince(midnight) - length / 2) / 1_800).rounded(.down) * 1_800)
            span = from ... from.addingTimeInterval(length)
            night = tonight.start ... tonight.end
        }
        var marks = episodes.map { ChartOverlay(label: "Night \(clock($0.0.lowerBound))–\(clock($0.0.upperBound))", start: $0.0.lowerBound, end: $0.0.upperBound, style: .shade) }
        marks += (workouts?.workouts ?? []).map { ChartOverlay(label: "\(metricLabel($0.sport)) \(clock($0.start))–\(clock($0.end))", start: $0.start, end: $0.end, style: .tint) }
        let now = Date()
        if span.contains(now) { marks.append(ChartOverlay(label: "Now \(clock(now))", start: now, style: .line)) }
        overlays = marks.filter { $0.end >= span.lowerBound && $0.start <= span.upperBound }
        domain = span

        let first = await loadLayer(client, step: ladder.coarsest, span: span)
        guard mine == generation else { return }
        base = first.layer
        layer = first.layer
        problem = first.problem
    }

    // MARK: Zoom

    /// The chart's window changed (pinch, pan, reset or a zoom button). The step follows at once;
    /// a finer layer loads once the window settles.
    func setWindow(_ new: ClosedRange<Date>?) {
        guard new != window else { return }
        window = new
        let span = new.map { $0.upperBound.timeIntervalSince($0.lowerBound) } ?? .infinity
        let next = ladder.step(forSpan: span)
        if next != step { step = next }
        settle?.cancel()
        if next == ladder.coarsest {
            loading = false
            show(base)
            return
        }
        settle = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(250))
            guard !Task.isCancelled else { return }
            await self?.loadWindow()
        }
    }

    /// The next rung's span around the window's middle, or the previous rung's (the whole span
    /// from the second rung).
    func zoom(in: Bool) {
        guard let domain else { return }
        let current = window ?? domain
        let index = ladder.index(of: step) ?? 0
        let target = index + (`in` ? 1 : -1)
        guard ladder.rungs.indices.contains(target) else { return }
        let length = ladder.rungs[target].span
        guard length.isFinite else { return setWindow(nil) }
        let middle = current.lowerBound.addingTimeInterval(current.upperBound.timeIntervalSince(current.lowerBound) / 2)
        let from = max(domain.lowerBound, min(middle.addingTimeInterval(-length / 2), domain.upperBound.addingTimeInterval(-length)))
        setWindow(from ... from.addingTimeInterval(length))
    }

    var canZoomIn: Bool { (ladder.index(of: step) ?? 0) < ladder.rungs.count - 1 }

    private func loadWindow() async {
        guard let client, let domain, let window, step != ladder.coarsest else { return }
        if let detail, detail.step == step, detail.span.lowerBound <= window.lowerBound, detail.span.upperBound >= window.upperBound {
            return show(detail)
        }
        let mine = generation
        let wanted = step
        loading = true
        let loaded = await loadLayer(client, step: wanted, span: DayLadder.loadSpan(wanted, visible: window, day: domain))
        guard mine == generation, wanted == step else { return }
        loading = false
        detail = loaded.layer
        problem = loaded.problem
        if let shown = self.window, let layer = loaded.layer, layer.span.lowerBound <= shown.lowerBound, layer.span.upperBound >= shown.upperBound {
            show(layer)
        }
    }

    private func show(_ next: Layer?) {
        if next?.id != layer?.id { layer = next }
    }

    /// Opens the point sheet for a bucket of the layer the chart drew.
    func pick(_ index: Int, in layer: Layer) {
        guard layer.points.indices.contains(index) else { return }
        picked = Pick(layer: layer, index: index)
    }

    // MARK: Requests

    private func loadLayer(_ client: Client, step: IntradayStep, span: ClosedRange<Date>) async -> (layer: Layer?, problem: Problem?) {
        var (sources, problem) = await readSources(client, grain: step, span: span)
        // Sources sparser than the step are read again at their own.
        for grain in Set(sources.map(\.step)).subtracting([step]) {
            let again = await readSources(client, grain: grain, span: span)
            problem = problem ?? again.problem
            let byKey = Dictionary(again.sources.map { ($0.id, $0.points) }) { a, _ in a }
            for i in sources.indices where sources[i].step == grain { sources[i].points = byKey[sources[i].id] ?? [] }
        }
        let spacings = sources.filter(\.used).compactMap(\.spacing)
        let bucket = ladder.resolvedBucket(for: step, spacings: spacings)
        var points: [Point] = []
        var rule: Components.Schemas.RuleRef?
        var cursor: String?
        do {
            for _ in 0 ..< 20 {
                let page = try await client.getResolvedSeries(query: .init(
                    metric: code, start: span.lowerBound, end: span.upperBound, window: bucket.rawValue, limit: 3_000, cursor: cursor
                )).ok.body.json
                points += page.points
                rule = page.rule
                guard page.hasMore, let next = page.nextCursor else { break }
                cursor = next
            }
        } catch {
            return (nil, Problem(error))
        }
        layers += 1
        return (makeLayer(id: layers, step: step, bucket: bucket, span: span, points: points, sources: sources, rule: rule), problem)
    }

    /// Every source's points at one grain, following raw pages.
    private func readSources(_ client: Client, grain: IntradayStep, span: ClosedRange<Date>) async -> (sources: [DaySource], problem: Problem?) {
        var out: [DaySource] = []
        var index: [String: Int] = [:]
        var cursor: String?
        do {
            for _ in 0 ..< 50 {
                let page = try await client.getSourceSeries(query: .init(
                    metric: code, start: span.lowerBound, end: span.upperBound, grain: grain.grain, cursor: cursor
                )).ok.body.json
                for s in page.sources {
                    let key = [s.provider, s.connectionId, s.device?.id ?? "", s.origin?.key ?? ""].joined(separator: "|")
                    if let i = index[key] {
                        out[i].points += s.points
                        continue
                    }
                    index[key] = out.count
                    let device = s.device?.model ?? s.device?._type
                    let origin = s.origin?.name ?? s.origin?.key
                    out.append(DaySource(
                        id: key, provider: s.provider, label: [providerLabel(s.provider), origin ?? device].compactMap(\.self).joined(separator: " · "),
                        device: device, origin: origin, used: s.ruleStatus == .used, spacing: s.spacingS,
                        step: DayLadder.sourceStep(grain, spacing: s.spacingS), points: s.points
                    ))
                }
                guard page.hasMore == true, let next = page.nextCursor else { break }
                cursor = next
            }
        } catch {
            return (out, Problem(error))
        }
        return (out, nil)
    }

    private func makeLayer(
        id: Int, step: IntradayStep, bucket: IntradayStep, span: ClosedRange<Date>, points: [Point], sources: [DaySource], rule: Components.Schemas.RuleRef?
    ) -> Layer {
        let xs = points.map { $0.start ?? $0.end }
        let values = points.map { $0.status == .noData ? nil : $0.value?.number(for: code) }
        let status = points.map { DataStatus(status: $0.status.rawValue, partial: $0.partial ?? false) as DataStatus? }
        let providers = points.map(\.providers)
        let resolved = ChartSeries(label: "Resolved, \(bucket.label)", points: xs.indices.map { i in
            ChartPoint(x: xs[i], y: values[i], status: status[i], providers: providers[i])
        })
        let band = additive ? nil : ChartBand(label: "Min–max per bucket", points: xs.indices.map { i in
            .init(x: xs[i], low: points[i].min, high: points[i].max)
        })
        let series = sources.map { s in
            ChartSeries(
                label: s.step == step ? s.label : "\(s.label), \(s.step.label)",
                points: s.points.compactMap { p in p.start.map { ChartPoint(x: $0, y: s.step == .raw ? p.value : additive ? p.sum : p.mean) } },
                source: s.provider
            )
        }
        return Layer(
            id: id, step: step, bucket: bucket, span: span, points: points, sources: sources, rule: rule,
            xs: xs, values: values, status: status, providers: providers, resolved: resolved, band: band, series: series
        )
    }

    // MARK: Formatting

    /// "07:12" in the day's timezone.
    func clock(_ date: Date) -> String {
        var style = Date.FormatStyle(date: .omitted, time: .shortened)
        style.timeZone = timeZone
        return date.formatted(style)
    }

    /// "07:12:06" in the day's timezone.
    func seconds(_ date: Date) -> String {
        var style = Date.FormatStyle(date: .omitted, time: .standard)
        style.timeZone = timeZone
        return date.formatted(style)
    }
}
