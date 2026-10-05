import Foundation
import Observation
import VitamuxKit

typealias WorkoutCluster = Components.Schemas.ResolvedWorkout
typealias WorkoutMember = Components.Schemas.WorkoutMember

/// Workouts (the panel's `/explore/workouts`): a month of resolved workouts
/// (`GET /resolved/workouts`). Workouts that overlap in time are one cluster, listed one row per
/// source with the rule's pick marked, so duplicates across sources are visible. A day picked in
/// the calendar narrows the list to it.
@Observable
final class WorkoutsModel {
    /// The first day of the shown month.
    var month: LocalDate
    var picked: String?
    private(set) var workouts: Loadable<Components.Schemas.ResolvedWorkouts> = .loading
    let latest = LocalDate.today(in: .current)

    init() {
        month = Self.firstOfMonth(latest)
    }

    static func firstOfMonth(_ date: LocalDate) -> LocalDate {
        date.adding(days: 1 - date.day)
    }

    /// The last day of the shown month.
    var monthEnd: LocalDate {
        Self.firstOfMonth(month.adding(days: 32)).adding(days: -1)
    }

    func step(_ months: Int) {
        picked = nil
        month = Self.firstOfMonth(months < 0 ? month.adding(days: -1) : month.adding(days: 32))
    }

    var isLatestMonth: Bool { month >= Self.firstOfMonth(latest) }

    func load(_ client: Client?) async {
        guard let client else { return }
        let start = month.description, end = monthEnd.description
        workouts = await Loadable { try await client.getResolvedWorkouts(query: .init(startDate: start, endDate: end)).ok.body.json }
    }

    var timeZone: TimeZone {
        workouts.value.flatMap { TimeZone(identifier: $0.timezone) } ?? .current
    }

    /// Workouts per local date of the month.
    var counts: [String: Int] {
        (workouts.value?.workouts ?? []).reduce(into: [:]) { $0[$1.localDate, default: 0] += 1 }
    }

    /// The listed clusters, newest first, only the picked day's when one is picked.
    var listed: [WorkoutCluster] {
        (workouts.value?.workouts ?? []).filter { picked == nil || $0.localDate == picked }.sorted { $0.start > $1.start }
    }

    func pick(_ date: String) {
        picked = picked == date ? nil : date
    }
}

extension WorkoutMember {
    var name: String { memberName(provider: provider, origin: origin, device: device) }
    var tag: String { ruleTag(selected: selected, status: ruleStatus.rawValue, reason: reason) }
    var duration: TimeInterval { endAt.timeIntervalSince(startAt) }
}
