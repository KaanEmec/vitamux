import Foundation
import Observation
import VitamuxKit

typealias HealthEvent = Components.Schemas.HealthEvent

/// Events (the panel's `/explore/events?code=`): health events (alerts, symptoms and other typed
/// events) in the range (`GET /events`, every page), one lane per type on a shared time axis. The
/// types to pick from come from the inventory (`GET /inventory`); one type can be shown alone.
@Observable
final class EventsModel {
    struct EventType: Identifiable, Hashable {
        var code: String
        var count: Int
        var id: String { code }
    }

    var span = ViewRange(.quarter)
    /// The one type shown, or nil for every type.
    var code: String?
    private(set) var types: [EventType] = []
    private(set) var events: Loadable<[HealthEvent]> = .loading

    init(code: String?) {
        self.code = code
    }

    var key: String { "\(span.key)/\(code ?? "")" }

    func loadTypes(_ client: Client?) async {
        guard let client else { return }
        let items = (try? await client.getInventory().ok.body.json.items) ?? []
        types = items.filter { $0.kind == .event }.map { EventType(code: $0.code, count: Int($0.count)) }
    }

    func load(_ client: Client?) async {
        guard let client else { return }
        let start = span.start()?.description, end = span.end.description, only = code.map { [$0] }
        events = await Loadable {
            try await readAll { cursor in
                let page = try await client.listEvents(query: .init(startDate: start, endDate: end, code: only, limit: 500, cursor: cursor)).ok.body.json
                return (page.value2.events, nextCursor(page.value1))
            }
        }
    }

    /// The types in the picker: the inventory's, plus a linked type it does not list.
    var options: [EventType] {
        guard let code, !types.contains(where: { $0.code == code }) else { return types }
        return types + [EventType(code: code, count: 0)]
    }

    /// One lane per type, by family (heart rhythm, mind, cycle tracking, symptoms, alerts) and
    /// name; each event labelled with its level or value as given.
    var lanes: [EventLanes.Lane] {
        Dictionary(grouping: events.value ?? [], by: \.code)
            .sorted { (EventFamily(code: $0.key), $0.key) < (EventFamily(code: $1.key), $1.key) }
            .map { code, list in
                EventLanes.Lane(label: metricLabel(code), events: list.map { event in
                    let label = event.level.map(metricLabel) ?? event.value.map(Format.number) ?? metricLabel(code)
                    return EventLanes.Event(start: event.startAt, end: event.endAt ?? event.startAt, label: label)
                })
            }
    }

    /// The shared axis: the range's first day (or the first event for All) to the end of its last.
    var axis: (from: Date, to: Date) {
        let zone = TimeZone.current
        let first = span.start()?.start(in: zone) ?? (events.value ?? []).map(\.startAt).min() ?? span.end.start(in: zone)
        return (first, span.end.adding(days: 1).start(in: zone))
    }
}
