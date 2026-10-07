import SwiftUI
import VitamuxKit

/// Events (`vitamux://explore/events?code=`): the range, a single-type filter, and one lane per
/// event type on a shared time axis; the lanes' "Show as table" lists every event.
struct EventsView: View {
    @Environment(AppState.self) private var state
    /// The link's type; it seeds the filter, which the screen owns afterwards.
    let code: String?
    @State private var model: EventsModel

    init(code: String?) {
        self.code = code
        _model = State(initialValue: EventsModel(code: code))
    }

    var body: some View {
        @Bindable var model = model
        List {
            Section {
                ViewIntro(code: "events", text: "Alerts, symptoms and other typed events, one lane per type. A bar spans an event from start to end.")
                RangePicker(range: $model.span.range, end: $model.span.end, latest: model.span.latest)
                    .buttonStyle(.borderless) // one row, two buttons: each takes only its own taps
                    .accessibilityIdentifier("rangePicker")
                Picker("Event type", selection: $model.code) {
                    Text("All types").tag(String?.none)
                    let options = model.options
                    ForEach(EventFamily.allCases.filter { family in options.contains { EventFamily(code: $0.code) == family } }, id: \.self) { family in
                        Section(family.title) {
                            ForEach(options.filter { EventFamily(code: $0.code) == family }) { type in
                                Text(type.count > 0 ? "\(metricLabel(type.code)) (\(type.count))" : metricLabel(type.code)).tag(Optional(type.code))
                            }
                        }
                    }
                }
                .pickerStyle(.menu)
                .accessibilityIdentifier("eventType")
                if let code = model.code, let route = watchRoute(forEvent: code) {
                    NavigationLink("This type has its own view with every detail.", value: route)
                        .font(.footnote)
                        .accessibilityIdentifier("eventTypeView")
                }
            }
            switch model.events {
            case .loading:
                ProgressView("Loading events").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let events) where events.isEmpty:
                EmptyRangeView(title: "No events in this range", text: "Events from connected sources appear here.")
            case .loaded(let events):
                let lanes = model.lanes
                let axis = model.axis
                Section {
                    EventLanes(title: "Events by type over time", lanes: lanes, from: axis.from, to: axis.to)
                        .accessibilityIdentifier("eventLanes")
                } header: {
                    Text("\(count(events.count, "event", "events")) in \(count(lanes.count, "type", "types"))")
                        .accessibilityIdentifier("eventsHeading")
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Events")
        .task { await model.loadTypes(state.client) }
        .onChange(of: code) { _, linked in model.code = linked }
        .task(id: model.key) { await model.load(state.client) }
    }

    private func count(_ n: Int, _ one: String, _ many: String) -> String {
        "\(n) \(n == 1 ? one : many)"
    }
}
