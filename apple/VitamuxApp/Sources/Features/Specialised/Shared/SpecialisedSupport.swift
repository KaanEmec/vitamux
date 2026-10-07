import Foundation
import SwiftUI
import VitamuxKit

// What the specialised views (J22.9) share, the twin of web/src/lib/views/format.ts and
// web/src/lib/data/paging.ts: paging to the end, range dates, source and rule labels, local clock
// times and a few small rows. Neutral copy only: a value is shown, never rated.

/// Every page of a keyset-paged list, as the panel's `readAll`: `page` answers one page and the
/// next cursor. At most 50 pages, so a broken cursor cannot loop forever.
func readAll<Item>(_ page: (_ cursor: String?) async throws -> (items: [Item], next: String?)) async throws -> [Item] {
    var items: [Item] = []
    var cursor: String?
    for _ in 0..<50 {
        let answer = try await page(cursor)
        items += answer.items
        guard let next = answer.next else { break }
        cursor = next
    }
    return items
}

/// The next cursor of a page, nil on the last.
func nextCursor(_ page: Components.Schemas.PageInfo) -> String? {
    page.hasMore ? page.nextCursor : nil
}

/// A range preset and its end date: what every ranged view keeps.
struct ViewRange: Equatable {
    var range: ChartRange
    var end: LocalDate
    let latest: LocalDate

    init(_ range: ChartRange, latest: LocalDate = .today(in: .current)) {
        self.range = range
        self.latest = latest
        end = latest
    }

    /// The first date: nil for All, or the end minus `cap` days when the endpoint needs a start.
    func start(cap: Int? = nil) -> LocalDate? {
        range.start(endingOn: end) ?? cap.map { end.adding(days: 1 - $0) }
    }

    var key: String { "\(range.rawValue)/\(end)" }

    /// "30-day mean", "Mean in range" for All.
    var meanLabel: String {
        range.days.map { "\($0)-day mean" } ?? "Mean in range"
    }
}

// MARK: Labels

/// "Apple Health · Synthetic Watch": a provider and, when known, its origin app or device.
func sourceName(_ provider: String, detail: String? = nil) -> String {
    guard let detail, !detail.isEmpty else { return providerLabel(provider) }
    return "\(providerLabel(provider)) · \(detail)"
}

/// A sleep or workout member's label: provider plus origin app, device model or device type.
func memberName(provider: String, origin: Components.Schemas.OriginRef?, device: Components.Schemas.DeviceRef?) -> String {
    sourceName(provider, detail: origin?.name ?? device?.model ?? device?._type.map(metricLabel))
}

/// A record's source label (SourceRef): provider plus origin app or device type.
func recordName(_ source: Components.Schemas.SourceRef) -> String {
    sourceName(source.provider, detail: source.origin ?? source.deviceType.map(metricLabel))
}

/// Where a source stands under the rule, in words, as the panel's `ruleTag`.
func ruleTag(selected: Bool, status: String, reason: String?) -> String {
    if selected { return "Selected" }
    switch status {
    case "used": return "In the rule"
    case "excluded": return reason.map { "Excluded: \($0)" } ?? "Excluded"
    default: return "Not in the rule"
    }
}

/// "7 h 19" for seconds, "–" for none.
func asleepText(_ seconds: Double?) -> String {
    seconds.map(Format.duration) ?? "–"
}

// MARK: Local times

extension TimeZone {
    /// The zone of a record's own UTC offset, else the device's.
    init(offsetMinutes: Int?) {
        self = offsetMinutes.flatMap { TimeZone(secondsFromGMT: $0 * 60) } ?? .current
    }
}

/// "07:12" in `zone`.
func clockText(_ date: Date, in zone: TimeZone) -> String {
    var style = Date.FormatStyle(date: .omitted, time: .shortened)
    style.timeZone = zone
    return date.formatted(style)
}

/// "Sun 4 Oct, 07:12" in `zone`.
func instantText(_ date: Date, in zone: TimeZone) -> String {
    var style = Date.FormatStyle(date: .abbreviated, time: .shortened)
    style.timeZone = zone
    return date.formatted(style.weekday(.abbreviated))
}

/// Midnight UTC of a local date: the x of a per-day point (charts then label in UTC).
func dayX(_ date: String) -> Date? {
    LocalDate(date)?.start(in: .gmt)
}

// MARK: Small views

/// The view's tile and a one-line description, as the panel's `ViewHead`.
struct ViewIntro: View {
    let code: String
    let text: String

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            MetricTile(code: code)
            Text(text).font(.subheadline).foregroundStyle(.secondary)
        }
    }
}

/// Where a source stands under the rule: a shape and a word, so the pick never rests on colour.
struct RuleTagLabel: View {
    let selected: Bool
    let text: String

    var body: some View {
        Label(text, systemImage: selected ? "checkmark.circle.fill" : "circle")
            .font(.caption.weight(selected ? .semibold : .regular))
            .foregroundStyle(selected ? AnyShapeStyle(.tint) : AnyShapeStyle(.secondary))
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(text)
    }
}

/// A label and a value, stacked, for a view's stats row.
struct StatTile: View {
    let label: String
    let value: String
    var unit: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(label).font(.footnote).foregroundStyle(Color.inkFaint)
            HStack(alignment: .firstTextBaseline, spacing: 3) {
                ValueText(value, size: .stat).lineLimit(1).minimumScaleFactor(0.7)
                if let unit, !unit.isEmpty { Text(unit).font(.caption).foregroundStyle(Color.inkMuted) }
            }
        }
        .frame(maxWidth: .infinity, alignment: .topLeading)
        .card(padding: 12, radius: Radius.tile)
        .accessibilityElement(children: .combine)
    }
}

/// "Nothing in this range", for a range with no records.
struct EmptyRangeView: View {
    let title: String
    let text: String

    var body: some View {
        ContentUnavailableView {
            Label(title, systemImage: "calendar.badge.exclamationmark")
        } description: {
            Text(text)
        }
        .accessibilityIdentifier("emptyRange")
    }
}

/// Shows the next page of a long list, as the panel's `TableCard`.
struct ShowMoreButton: View {
    let remaining: Int
    let action: () -> Void

    var body: some View {
        if remaining > 0 {
            Button("Show \(min(remaining, ListPage.size)) more of \(remaining)", action: action)
                .accessibilityIdentifier("showMore")
        }
    }
}

enum ListPage {
    static let size = 50
}

/// The provenance button of one record.
struct ProvenanceButton: View {
    let entity: Operations.GetProvenance.Input.Path.EntityPayload
    let id: String
    @Binding var request: ProvenanceRequest?

    var body: some View {
        Button {
            request = ProvenanceRequest(entity: entity, recordID: id)
        } label: {
            Label("Provenance", systemImage: "point.3.connected.trianglepath.dotted").tapTarget()
        }
        .font(.footnote)
        .buttonStyle(.borderless)
        .accessibilityIdentifier("provenance-\(id)")
    }
}
