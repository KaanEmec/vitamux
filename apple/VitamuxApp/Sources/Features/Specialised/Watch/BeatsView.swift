import Foundation
import Observation
import SwiftUI
import VitamuxKit

/// Beat-to-beat (`vitamux://explore/beats?date=`): the day's `rr_interval` samples (`GET
/// /measurements`, every page), one chart per heartbeat series. A series is the HealthKit sample
/// the beats came from (`<series uuid>#<beat>`); beats after a gap were never stored. Opened from
/// an HRV reading; RR intervals are a raw series and are never resolved.
@Observable
final class BeatsModel {
    struct Series: Identifiable {
        let id: String
        let start: Date
        let end: Date
        let source: String
        let zone: TimeZone
        /// Milliseconds, ascending by time.
        let points: [ChartPoint]
    }

    var date: LocalDate
    let latest = LocalDate.today(in: .current)
    private(set) var series: Loadable<[Series]> = .loading

    init(date: String?) {
        self.date = date.flatMap(LocalDate.init) ?? latest
    }

    func load(_ client: Client?) async {
        guard let client else { return }
        let day = date.description
        series = await Loadable {
            let beats = try await readAll { cursor in
                let page = try await client.listMeasurements(query: .init(startDate: day, endDate: day, metric: ["rr_interval"], limit: 500, cursor: cursor)).ok.body.json
                return (page.value2.measurements, nextCursor(page.value1))
            }
            return Self.series(beats)
        }
    }

    /// Beats grouped by their series, each in time order; series by start.
    static func series(_ beats: [SourceMeasurement]) -> [Series] {
        let groups = Dictionary(grouping: beats) { beat in
            beat.source.externalId.flatMap { $0.split(separator: "#").first.map(String.init) } ?? beat.source.dedupeKey
        }
        return groups.compactMap { key, beats -> Series? in
            let sorted = beats.sorted { $0.startAt < $1.startAt }
            guard let first = sorted.first, let last = sorted.last else { return nil }
            return Series(id: key, start: first.startAt, end: last.startAt, source: WatchText.source(first.source),
                          zone: TimeZone(offsetMinutes: first.tzOffsetMin),
                          points: sorted.map { ChartPoint(x: $0.startAt, y: ($0.unit == "s" ? $0.value * 1_000 : $0.value).rounded()) })
        }
        .sorted { $0.start < $1.start }
    }
}

struct BeatsView: View {
    @Environment(AppState.self) private var state
    @State private var model: BeatsModel

    init(date: String?) {
        _model = State(initialValue: BeatsModel(date: date))
    }

    var body: some View {
        @Bindable var model = model
        List {
            Section {
                WatchIntro(hue: .hrv, symbol: "heart.text.square",
                           text: "The time between heartbeats (RR intervals) that Apple Watch recorded around its HRV readings, one chart per series, as recorded.")
                DayStepper(date: $model.date, latest: model.latest)
            }
            switch model.series {
            case .loading:
                ProgressView("Loading beats").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let series) where series.isEmpty:
                EmptyRangeView(title: "No beat-to-beat series on this day", text: "Turn on the Beat-to-beat group in Apple Health on this iPhone to send them from Apple Watch.")
            case .loaded(let series):
                ForEach(Array(series.enumerated()), id: \.element.id) { index, item in
                    SeriesSection(index: index, series: item)
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Beat-to-beat")
        .task(id: model.date) { await model.load(state.client) }
    }
}

private struct SeriesSection: View {
    let index: Int
    let series: BeatsModel.Series

    var body: some View {
        Section {
            TimeSeries(title: "RR intervals of series \(index + 1)", series: [ChartSeries(label: "RR interval", points: series.points)],
                       unit: "ms", hue: .hrv, timeZone: series.zone, zoomable: false, height: 180)
                .accessibilityIdentifier("beatsChart-\(index)")
        } header: {
            Text("Series \(index + 1) · \(clockText(series.start, in: series.zone))–\(clockText(series.end, in: series.zone))")
        } footer: {
            Text("\(series.points.count) intervals · \(series.source)").accessibilityIdentifier("beatsCount-\(index)")
        }
    }
}
