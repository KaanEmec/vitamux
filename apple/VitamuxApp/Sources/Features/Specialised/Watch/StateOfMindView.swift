import Foundation
import Observation
import SwiftUI
import VitamuxKit

/// State of Mind (`vitamux://explore/state-of-mind`): the `state_of_mind` and `mindful_session`
/// events in the range (`GET /events`). An entry shows its kind, the valence on Apple's −1 to 1
/// scale with Apple's word for it, and its labels and associations, all as logged.
@Observable
final class StateOfMindModel {
    var span = ViewRange(.month)
    private(set) var events: Loadable<[HealthEvent]> = .loading

    func load(_ client: Client?) async {
        guard let client else { return }
        let start = span.start()?.description, end = span.end.description
        events = await Loadable {
            try await readAll { cursor in
                let page = try await client.listEvents(query: .init(startDate: start, endDate: end, code: ["state_of_mind", "mindful_session"], limit: 500, cursor: cursor)).ok.body.json
                return (page.value2.events, nextCursor(page.value1))
            }
            .sorted { $0.startAt > $1.startAt }
        }
    }

    /// Valence over time, oldest first.
    var valence: [ChartPoint] {
        (events.value ?? []).filter { $0.code == "state_of_mind" }.compactMap { event in
            event.value.map { ChartPoint(x: event.startAt, y: $0) }
        }
        .sorted { $0.x < $1.x }
    }
}

struct StateOfMindView: View {
    @Environment(AppState.self) private var state
    @State private var model = StateOfMindModel()

    var body: some View {
        @Bindable var model = model
        List {
            Section {
                WatchIntro(hue: .sleep, symbol: "brain.head.profile",
                           text: "Emotions and moods logged in Apple’s State of Mind, and mindful sessions, as logged.")
                RangePicker(range: $model.span.range, end: $model.span.end, latest: model.span.latest)
                    .buttonStyle(.borderless)
                    .accessibilityIdentifier("rangePicker")
            }
            switch model.events {
            case .loading:
                ProgressView("Loading entries").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let events) where events.isEmpty:
                EmptyRangeView(title: "Nothing logged in this range", text: "Turn on the Mind group in Apple Health on this iPhone to send State of Mind and mindful sessions.")
            case .loaded(let events):
                let valence = model.valence
                if !valence.isEmpty {
                    Section {
                        TimeSeries(title: "Valence over time", series: [ChartSeries(label: "Valence", points: valence, style: .dots)],
                                   hue: .sleep, baseline: ChartBaseline(value: 0, label: "0"), zoomable: false, height: 180)
                            .accessibilityIdentifier("valenceChart")
                    } footer: {
                        Text("Valence is Apple’s scale from −1 to 1, as logged.")
                    }
                }
                Section {
                    ForEach(events, id: \.id) { event in
                        MindRow(event: event).accessibilityIdentifier("mind-\(event.id)")
                    }
                } header: {
                    Text(events.count == 1 ? "1 entry" : "\(events.count) entries").accessibilityIdentifier("mindCount")
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("State of Mind")
        .task(id: model.span.key) { await model.load(state.client) }
    }
}

private struct MindRow: View {
    let event: HealthEvent

    var body: some View {
        let zone = TimeZone(offsetMinutes: event.tzOffsetMin)
        VStack(alignment: .leading, spacing: 3) {
            HStack(alignment: .firstTextBaseline) {
                Text(title).font(.body.weight(.medium))
                Spacer()
                Text(instantText(event.startAt, in: zone)).font(.footnote).foregroundStyle(.secondary)
            }
            ForEach(details, id: \.self) { line in
                Text(line).font(.footnote).foregroundStyle(.secondary)
            }
        }
        .accessibilityElement(children: .combine)
    }

    private var title: String {
        guard event.code == "state_of_mind" else { return "Mindful session" }
        return event.level.map(metricLabel) ?? "State of Mind"
    }

    private var details: [String] {
        let context = event.context
        if event.code == "mindful_session" {
            let minutes = event.endAt.map { Int(($0.timeIntervalSince(event.startAt) / 60).rounded()) }
            return [minutes.map { "\($0) min" } ?? "No end recorded"]
        }
        var lines: [String] = []
        let word = context.text("valence_classification").map { " · \(metricLabel($0))" } ?? ""
        if let value = event.value { lines.append("Valence \(Format.signed(value)) on −1 to 1\(word)") }
        let labels = context.words("labels"), associations = context.words("associations")
        if !labels.isEmpty { lines.append("Labels: \(WatchText.words(labels))") }
        if !associations.isEmpty { lines.append("Associations: \(WatchText.words(associations))") }
        lines.append(WatchText.source(event.source))
        return lines
    }
}
