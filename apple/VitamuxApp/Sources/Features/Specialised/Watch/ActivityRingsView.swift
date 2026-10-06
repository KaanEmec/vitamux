import Foundation
import Observation
import SwiftUI
import VitamuxKit

/// Activity rings (`vitamux://explore/activity-rings`): Apple's daily activity summaries, stored as
/// daily values of `active_energy` or `move_time`, `exercise_time` and `stand_hours` with Apple's
/// goal, move mode and paused flag in `context` (ADR-0024). Each day shows move, exercise and stand
/// as a plain value-against-goal bar; the goals are Apple's.
@Observable
final class ActivityRingsModel {
    struct Ring: Identifiable {
        enum Kind: String { case move, exercise, stand }
        let kind: Kind
        let value: Double
        let goal: Double?
        let unit: String
        var id: String { kind.rawValue }

        var title: String {
            switch kind {
            case .move: "Move"
            case .exercise: "Exercise"
            case .stand: "Stand"
            }
        }

        var hue: MetricHue {
            switch kind {
            case .move: .activeEnergy
            case .exercise: .steps
            case .stand: .spo2
            }
        }

        /// "420 kcal of 500 kcal", "26 min of 30 min", "9 of 12 hours".
        var text: String {
            let goalText = goal.map { " of \(Self.format($0, unit: unit))" } ?? ""
            if unit == "hours" { return "\(Format.number(value))\(goal.map { " of \(Format.number($0))" } ?? "") hours" }
            return Self.format(value, unit: unit) + goalText
        }

        static func format(_ value: Double, unit: String) -> String {
            unit == "s" ? "\(Format.number((value / 60).rounded())) min" : "\(Format.number(value)) \(unit)"
        }
    }

    struct Day: Identifiable {
        let date: LocalDate
        let rings: [Ring]
        let paused: Bool
        let source: String
        var id: String { date.description }
    }

    static let codes = ["active_energy", "move_time", "exercise_time", "stand_hours"]

    var span = ViewRange(.week)
    private(set) var days: Loadable<[Day]> = .loading

    func load(_ client: Client?) async {
        guard let client else { return }
        let start = span.start(cap: 366)?.description, end = span.end.description
        days = await Loadable {
            let rows = try await readAll { cursor in
                let page = try await client.listMeasurements(query: .init(startDate: start, endDate: end, metric: Self.codes, provider: ["apple_health"],
                                                                          kind: [.dailyValue], limit: 500, cursor: cursor)).ok.body.json
                return (page.value2.measurements, nextCursor(page.value1))
            }
            return Self.days(rows)
        }
    }

    /// The summary rows (those with Apple's goal or move mode) by day, newest first.
    static func days(_ rows: [SourceMeasurement]) -> [Day] {
        let summaries = rows.filter { $0.context?.number("goal") != nil || $0.context?.text("move_mode") != nil }
        return Dictionary(grouping: summaries, by: \.localDate).compactMap { date, rows -> Day? in
            guard let day = LocalDate(date), let first = rows.first else { return nil }
            let row = { (code: String) in rows.first { $0.metric == code } }
            let moveCode = rows.compactMap { $0.context?.text("move_mode") }.first == "move_time" ? "move_time" : "active_energy"
            let ring = { (kind: Ring.Kind, code: String, unit: String) -> Ring? in
                row(code).map { Ring(kind: kind, value: $0.value, goal: $0.context?.number("goal"), unit: unit) }
            }
            let rings = [
                ring(.move, moveCode, moveCode == "move_time" ? "s" : "kcal"),
                ring(.exercise, "exercise_time", "s"),
                ring(.stand, "stand_hours", "hours"),
            ].compactMap(\.self)
            return Day(date: day, rings: rings, paused: rows.contains { $0.context?.flag("paused") == true }, source: WatchText.source(first.source))
        }
        .sorted { $0.date > $1.date }
    }
}

struct ActivityRingsView: View {
    @Environment(AppState.self) private var state
    @State private var model = ActivityRingsModel()

    var body: some View {
        @Bindable var model = model
        List {
            Section {
                WatchIntro(hue: .activeEnergy, symbol: "circle.circle",
                           text: "Move, exercise and stand per day from Apple’s activity summaries, each against the goal set in Apple’s Activity app that day.")
                RangePicker(range: $model.span.range, end: $model.span.end, latest: model.span.latest)
                    .buttonStyle(.borderless)
                    .accessibilityIdentifier("rangePicker")
            }
            switch model.days {
            case .loading:
                ProgressView("Loading activity summaries").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let days) where days.isEmpty:
                EmptyRangeView(title: "No activity summaries in this range", text: "Apple Health sends one a day while the Activity group is on.")
            case .loaded(let days):
                ForEach(days) { day in
                    RingsDaySection(day: day)
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Activity rings")
        .task(id: model.span.key) { await model.load(state.client) }
    }
}

private struct RingsDaySection: View {
    let day: ActivityRingsModel.Day

    var body: some View {
        Section {
            ForEach(day.rings) { ring in
                RingBar(ring: ring)
                    .accessibilityIdentifier("ring-\(ring.id)-\(day.id)")
            }
        } header: {
            Text(Format.day(day.date)).accessibilityIdentifier("ringsDay-\(day.id)")
        } footer: {
            Text(day.paused ? "Apple’s rings were paused on this day. Goals are Apple’s. \(day.source)" : "Goals are Apple’s. \(day.source)")
        }
    }
}

/// The value against Apple's goal: the numbers as recorded over a bar filled up to the goal.
private struct RingBar: View {
    let ring: ActivityRingsModel.Ring

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline) {
                Text(ring.title).font(.subheadline.weight(.medium))
                Spacer()
                Text(ring.text).font(.subheadline).monospacedDigit()
            }
            if let goal = ring.goal, goal > 0 {
                Gauge(value: min(ring.value, goal), in: 0...goal) { Text(ring.title) }
                    .gaugeStyle(.linearCapacity)
                    .labelsHidden()
                    .tint(ring.hue.color)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(ring.title)
        .accessibilityValue(ring.goal == nil ? ring.text : "\(ring.text), Apple’s goal")
    }
}
