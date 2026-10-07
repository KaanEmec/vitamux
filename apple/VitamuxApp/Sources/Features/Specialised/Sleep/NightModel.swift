import Foundation
import Observation
import VitamuxKit

/// One night across its sources (the panel's NightSources): the sessions behind each member with
/// their stages (`GET /sleep?include=stages`, every page, from the day before because a night's
/// sessions can be dated by either day), lined up on one time axis, and the naps of that day.
@Observable
final class NightModel {
    let night: Night
    let timeZone: TimeZone
    private(set) var sessions: Loadable<[SleepSession]> = .loading

    init(night: Night, timeZone: TimeZone) {
        self.night = night
        self.timeZone = timeZone
    }

    func load(_ client: Client?) async {
        guard let client, let date = LocalDate(night.localDate) else { return }
        let start = date.adding(days: -1).description, end = date.description
        sessions = await Loadable {
            try await readAll { cursor in
                let page = try await client.listSleep(query: .init(startDate: start, endDate: end, include: [.stages], limit: 500, cursor: cursor)).ok.body.json
                return (page.value2.sleep, nextCursor(page.value1))
            }
        }
    }

    private var byID: [String: SleepSession] {
        Dictionary((sessions.value ?? []).map { ($0.id, $0) }) { first, _ in first }
    }

    /// The sessions behind a member (one source's episode), in time order.
    func sessions(of member: Components.Schemas.SleepMember) -> [SleepSession] {
        let byID = byID
        return member.sessionRefs.compactMap { byID[$0] }.sorted { $0.startAt < $1.startAt }
    }

    /// The shared axis of every member's staged sessions: the stage rows present and the span.
    var axis: (rows: [SleepStageKind], from: Date, to: Date)? {
        let staged = night.members.flatMap(sessions(of:)).filter { !($0.stages ?? []).isEmpty }
        guard let from = staged.map(\.startAt).min(), let to = staged.map(\.endAt).max() else { return nil }
        let present = Set(staged.flatMap { ($0.stages ?? []).map { SleepStageKind(code: $0.stage.rawValue) } })
        let order: [SleepStageKind] = [.awake, .rem, .light, .deep, .asleep, .inBed, .restless, .outOfBed, .unknown]
        return (order.filter(present.contains), from, to)
    }

    /// Naps of the night's date that no member counts.
    var naps: [SleepSession] {
        let counted = Set(night.members.flatMap(\.sessionRefs))
        return (sessions.value ?? []).filter { $0.isNap && $0.sleepDate == night.localDate && !counted.contains($0.id) }
    }

    func stages(of sessions: [SleepSession]) -> [StageInterval] {
        sessions.flatMap { ($0.stages ?? []).map { StageInterval(stage: SleepStageKind(code: $0.stage.rawValue), start: $0.startAt, end: $0.endAt) } }
    }

    /// "23:12 to 07:01": the clock span of sessions in time order, at the first one's offset.
    func span(_ sessions: [SleepSession]) -> String {
        guard let first = sessions.first, let last = sessions.last else { return "" }
        let zone = TimeZone(offsetMinutes: first.tzOffsetMin)
        return "\(clockText(first.startAt, in: zone)) to \(clockText(last.endAt, in: zone))"
    }
}
