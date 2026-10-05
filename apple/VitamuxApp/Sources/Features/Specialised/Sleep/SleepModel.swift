import Foundation
import Observation
import VitamuxKit

typealias Night = Components.Schemas.ResolvedNight
typealias SleepSession = Components.Schemas.SleepSession

/// Sleep (the panel's `/explore/sleep`): the resolved night per local date from the rule
/// (`GET /resolved/sleep`, with the episode and the time per stage), stage bars per night, bed and
/// wake times and the stats of the range. A night opens `NightView`.
@Observable
final class SleepModel {
    var span = ViewRange(.month)
    private(set) var sleep: Loadable<Components.Schemas.ResolvedSleep> = .loading

    /// The endpoint answers at most 366 nights, so All reads the year up to the end date.
    static let cap = 366

    func load(_ client: Client?) async {
        guard let client else { return }
        let start = span.start(cap: Self.cap) ?? span.end
        sleep = await Loadable {
            try await client.getResolvedSleep(query: .init(startDate: start.description, endDate: span.end.description)).ok.body.json
        }
    }

    var timeZone: TimeZone {
        sleep.value.flatMap { TimeZone(identifier: $0.timezone) } ?? .current
    }

    var nights: [Night] { sleep.value?.nights ?? [] }

    /// Nights whose result has values, oldest first.
    var withData: [Night] { nights.filter { !$0.seconds.isEmpty } }

    /// One stack per stage, deep at the bottom; a night without the stage has no bar.
    var stacks: [BarStack] {
        let stages: [(String, SleepStageKind)] = [("sleep_deep", .deep), ("sleep_light", .light), ("sleep_rem", .rem), ("sleep_awake", .awake), ("sleep_unspecified", .asleep)]
        return stages
            .map { code, stage in BarStack(label: stage.label, values: nights.map { $0.seconds[code].map { $0 / 3600 } }, stage: stage) }
            .filter { $0.values.contains { $0 != nil } }
    }

    var xs: [Date] { nights.compactMap { dayX($0.localDate) } }

    /// Mean time asleep over the nights with data, in seconds.
    var meanAsleep: Double? {
        let totals = withData.compactMap { $0.seconds["sleep_total"] }
        return totals.isEmpty ? nil : totals.reduce(0, +) / Double(totals.count)
    }

    /// Bed and wake as clock hours on one axis (before noon counts as after midnight).
    var spans: [NightSpan] { withData.compactMap { NightSpan($0, in: timeZone) } }

    var medianBed: String { Self.median(spans.map(\.bed)).map(clockHours) ?? "–" }
    var medianWake: String { Self.median(spans.map(\.wake)).map(clockHours) ?? "–" }

    static func median(_ values: [Double]) -> Double? {
        guard !values.isEmpty else { return nil }
        let sorted = values.sorted()
        let middle = Double(sorted.count - 1) / 2
        return (sorted[Int(middle.rounded(.down))] + sorted[Int(middle.rounded(.up))]) / 2
    }
}

/// The night's main episode as clock hours: bed, and wake unwrapped past 24.
struct NightSpan {
    var bed: Double
    var wake: Double

    init?(_ night: Night, in zone: TimeZone) {
        guard let episode = night.episode else { return nil }
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = zone
        let parts = calendar.dateComponents([.hour, .minute], from: episode.start)
        let hour = Double(parts.hour ?? 0) + Double(parts.minute ?? 0) / 60
        bed = hour < 12 ? hour + 24 : hour
        wake = bed + episode.end.timeIntervalSince(episode.start) / 3600
    }
}

/// "23:12" for clock hours (values past 24 wrap to the next day).
func clockHours(_ hours: Double) -> String {
    let minutes = Int((hours * 60).rounded()) % 1440
    return String(format: "%02d:%02d", minutes / 60, minutes % 60)
}

extension Night {
    /// Seconds per sleep code (sleep_total, sleep_deep, …) of the result; empty when it has none.
    var seconds: [String: Double] {
        result.status == .noData ? [:] : result.value?.components ?? [:]
    }

    /// "Sat 3 → Sun 4 Oct": a night is dated by the day you woke up.
    var label: String {
        guard let date = LocalDate(localDate) else { return localDate }
        return "\(Self.day(date.adding(days: -1), month: false)) → \(Self.day(date, month: true))"
    }

    private static func day(_ date: LocalDate, month: Bool) -> String {
        var style = Date.FormatStyle().weekday(.abbreviated).day()
        if month { style = style.month(.abbreviated) }
        style.timeZone = .gmt
        return date.start(in: .gmt).formatted(style)
    }

    /// "23:12 – 07:01" of the episode in `zone`.
    func bedWake(in zone: TimeZone) -> String? {
        episode.map { "\(clockText($0.start, in: zone)) – \(clockText($0.end, in: zone))" }
    }

    var selectedMember: Components.Schemas.SleepMember? { members.first(where: \.selected) }
}
