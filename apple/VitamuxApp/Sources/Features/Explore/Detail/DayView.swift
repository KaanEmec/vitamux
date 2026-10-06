import SwiftUI
import VitamuxKit

/// The Day tab of metric detail (J22.26): one local day of a metric with `intraday` (DayModel).
/// The range picker's arrows step the day; pinch, drag or the zoom buttons walk the ladder down
/// to the finest bucket or the raw readings. The detail screen's toggles apply: Baseline is the
/// min–max band, each source overlays its own series, Coverage adds the strip per source. A tap
/// opens the bucket's sheet; "Show as table" lists the buckets.
struct DayView: View {
    @Environment(AppState.self) private var state
    let detail: MetricDetailModel
    /// Seeded once from the catalogue entry; the screen owns it afterwards.
    @State private var model: DayModel

    init(detail: MetricDetailModel, metric: Components.Schemas.Metric, intraday: Components.Schemas.Intraday) {
        self.detail = detail
        _model = State(initialValue: DayModel(metric: metric, intraday: intraday))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            DayToolbar(model: model)
            if let problem = model.problem { ProblemView(problem: problem) }
            DayContent(model: model, hue: MetricHue.of(code: model.code, section: detail.meta.value?.section), shown: detail.shownSources, band: detail.showBaseline)
            if detail.showCoverage, let layer = model.layer { DayCoverage(model: model, layer: layer) }
        }
        // Several controls share this list row: each button keeps its own hit area ("Show as table").
        .buttonStyle(.borderless)
        .task(id: detail.end) { await model.load(state.client, date: detail.end) }
        .sheet(item: $model.picked) { pick in
            DayPointSheet(model: model, pick: pick)
        }
    }
}

/// The current step, the night the view is centred on, and the zoom buttons.
private struct DayToolbar: View {
    let model: DayModel

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 8) {
                Text(model.step.label.prefix(1).uppercased() + model.step.label.dropFirst())
                    .font(.subheadline.weight(.semibold))
                    .accessibilityIdentifier("dayStep")
                if model.loading { ProgressView().controlSize(.small) }
                Spacer()
                ZoomButtons(model: model)
            }
            if let night = model.night {
                Text("Centred on the night \(model.clock(night.lowerBound))–\(model.clock(night.upperBound))")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("dayNight")
            } else {
                Text("Pinch or use the buttons to zoom in; drag to move along the day.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
    }
}

/// Zoom in and out one rung of the ladder (an alternative to pinching).
private struct ZoomButtons: View {
    let model: DayModel

    var body: some View {
        HStack(spacing: 4) {
            Button("Zoom out", systemImage: "minus.magnifyingglass") { model.zoom(in: false) }
                .disabled(model.window == nil)
                .accessibilityIdentifier("dayZoomOut")
            Button("Zoom in", systemImage: "plus.magnifyingglass") { model.zoom(in: true) }
                .disabled(!model.canZoomIn || model.domain == nil)
                .accessibilityIdentifier("dayZoomIn")
        }
        .labelStyle(.iconOnly)
        .buttonStyle(.bordered)
        .controlSize(.small)
    }
}

/// The chart of the shown layer, an empty note, or the loading state.
private struct DayContent: View {
    let model: DayModel
    let hue: MetricHue
    let shown: Set<String>
    let band: Bool

    var body: some View {
        if let layer = model.layer, let domain = model.domain {
            if layer.hasValues {
                DayChart(
                    layer: layer, domain: domain, overlays: model.overlays,
                    window: Binding(get: { model.window }, set: { model.setWindow($0) }),
                    title: "\(metricLabel(model.code)) on \(model.date.map { Format.day($0) } ?? ""), \(layer.step.label)",
                    unit: model.unit, hue: hue, timeZone: model.timeZone, additive: model.additive, shown: shown, band: band
                ) { model.pick($0, in: layer) }
            } else {
                ContentUnavailableView(
                    "No values on \(model.date.map { Format.day($0) } ?? "this day")", systemImage: "chart.xyaxis.line",
                    description: Text("Step to another day, or choose a longer range.")
                )
                .accessibilityIdentifier("dayNoValues")
            }
        } else if model.loading {
            ProgressView("Loading the day").frame(maxWidth: .infinity, minHeight: 240)
        }
    }
}

/// Bars for additive metrics without source overlays, otherwise the resolved line with its band
/// and the shown sources' own series.
private struct DayChart: View {
    let layer: DayModel.Layer
    let domain: ClosedRange<Date>
    let overlays: [ChartOverlay]
    let window: Binding<ClosedRange<Date>?>
    let title: String
    let unit: String
    let hue: MetricHue
    let timeZone: TimeZone
    let additive: Bool
    let shown: Set<String>
    let band: Bool
    let onSelect: (Int) -> Void

    var body: some View {
        let overlaid = layer.sources.indices.filter { shown.contains(layer.sources[$0].provider) }.map { layer.series[$0] }
        Group {
            if additive && overlaid.isEmpty {
                Bars(
                    title: title, xs: layer.xs, values: layer.values, unit: unit, hue: hue, status: layer.status, providers: layer.providers,
                    timeZone: timeZone, height: 260, binWidth: layer.bucket.seconds, domain: domain, overlays: overlays, window: window, onSelect: onSelect
                )
            } else {
                TimeSeries(
                    title: title, series: [layer.resolved] + overlaid, unit: unit, hue: hue, area: !additive, band: band ? layer.band : nil,
                    timeZone: timeZone, height: 260, domain: domain, overlays: overlays, window: window, onSelect: onSelect
                )
            }
        }
        .accessibilityIdentifier("dayChart")
        .onAppear { Signposts.chartDidRender() }
        .onChange(of: layer.id) { Signposts.chartDidRender() }
    }
}

/// Each source's coverage and the source behind each value, in at most 96 windows.
private struct DayCoverage: View {
    let model: DayModel
    let layer: DayModel.Layer

    var body: some View {
        let strip = DayStrip(layer)
        CoverageStrip(
            caption: "\(metricLabel(model.code)): each source and the source behind each value", rows: strip.rows,
            start: model.date ?? .today(in: model.timeZone), noun: "windows",
            cellLabel: { i in strip.starts.indices.contains(i) ? model.clock(strip.starts[i]) : "" }
        )
        .accessibilityIdentifier("dayCoverage")
    }
}

/// The strip rows of a layer: buckets grouped into windows of `size` buckets.
struct DayStrip {
    var rows: [CoverageStrip.Row] = []
    var starts: [Date] = []

    init(_ layer: DayModel.Layer) {
        let n = layer.points.count
        guard n > 0, let origin = layer.xs.first else { return }
        let size = max(1, Int((Double(n) / 96).rounded(.up)))
        let cells = (n + size - 1) / size
        let length = layer.bucket.seconds
        starts = (0 ..< cells).map { layer.xs[$0 * size] }
        let share = { (marked: [Bool]) in
            (0 ..< cells).map { c in
                let slice = marked[(c * size) ..< min((c + 1) * size, n)]
                return Double(slice.filter(\.self).count) / Double(slice.count)
            }
        }
        for source in layer.sources {
            var marked = [Bool](repeating: false, count: n)
            let span = source.step == .raw ? 0 : source.step.seconds
            for p in source.points {
                guard let t = p.start else { continue }
                let first = Int((t.timeIntervalSince(origin) / length).rounded(.down))
                let last = span > 0 ? Int((t.addingTimeInterval(span).timeIntervalSince(origin) / length).rounded(.up)) - 1 : first
                guard last >= 0, first < n else { continue }
                for i in max(first, 0) ... min(last, n - 1) { marked[i] = true }
            }
            rows.append(CoverageStrip.Row(label: source.label, source: source.provider, cells: share(marked)))
        }
        let picks: [String?] = (0 ..< cells).map { c in
            var counts: [String: Int] = [:]
            for i in (c * size) ..< min((c + 1) * size, n) {
                if let p = layer.providers[i]?.first { counts[p, default: 0] += 1 }
            }
            return counts.max { ($0.value, $1.key) < ($1.value, $0.key) }?.key
        }
        rows.append(CoverageStrip.Row(label: "Source per window", cells: picks.map { $0 == nil ? 0 : 1 }, picks: picks))
    }
}
