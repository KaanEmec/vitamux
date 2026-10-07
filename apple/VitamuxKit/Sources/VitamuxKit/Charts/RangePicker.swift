import SwiftUI

/// Range presets of a chart. "All" plots week or month rollups (GET /resolved/trend), as the
/// panel does; "1D" is the Day view of a metric with `intraday` (one local day at its own
/// resolution); the others plot daily windows ending on the picked end date.
public enum ChartRange: String, CaseIterable, Sendable, Identifiable {
    case day = "1D", week = "1W", month = "1M", quarter = "3M", year = "1Y", all = "All"

    /// The presets of a view without a Day view.
    public static let periods: [ChartRange] = [.week, .month, .quarter, .year, .all]

    public var id: String { rawValue }

    public var days: Int? {
        switch self {
        case .day: 1
        case .week: 7
        case .month: 30
        case .quarter: 90
        case .year: 365
        case .all: nil
        }
    }

    /// First local date of the range ending on `end`, or nil for All.
    public func start(endingOn end: LocalDate) -> LocalDate? {
        days.map { end.adding(days: 1 - $0) }
    }
}

/// The range presets (1W, 1M, 3M, 1Y, All, and 1D where a Day view exists) and the end date,
/// stepped a range at a time (a day at a time for 1D) and never past `latest`.
public struct RangePicker: View {
    @Binding var range: ChartRange
    @Binding var end: LocalDate
    let latest: LocalDate
    let ranges: [ChartRange]
    @Environment(\.dynamicTypeSize) private var typeSize

    public init(range: Binding<ChartRange>, end: Binding<LocalDate>, latest: LocalDate, ranges: [ChartRange] = ChartRange.periods) {
        _range = range
        _end = end
        self.latest = latest
        self.ranges = ranges
    }

    public var body: some View {
        VStack(spacing: 8) {
            Picker("Range", selection: $range) {
                ForEach(ranges) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            HStack {
                Button("Earlier", systemImage: "chevron.backward") { end = end.adding(days: -(range.days ?? 0)) }
                    .disabled(range == .all)
                Spacer()
                Text(label).font(.subheadline.monospacedDigit()).multilineTextAlignment(.center)
                Spacer()
                Button("Later", systemImage: "chevron.forward") { end = min(latest, end.adding(days: range.days ?? 0)) }
                    .disabled(range == .all || end >= latest)
            }
            .labelStyle(.iconTapTarget)
        }
    }

    private var label: String {
        let day = { (d: LocalDate) in
            var style = Date.FormatStyle(date: .abbreviated, time: .omitted)
            style.timeZone = .gmt
            return d.start(in: .gmt).formatted(range == .day ? style.weekday(.abbreviated) : style)
        }
        guard let start = range.start(endingOn: end) else { return "Up to \(day(end))" }
        if start == end { return day(end) }
        return "\(day(start)) – \(day(end))"
    }
}

/// Week and month rollups for long ranges, from the shape of GET /resolved/trend, read the way the
/// panel reads them: monthly over ten years, or weekly from the first data when that is within two
/// years; leading empty buckets are dropped.
public enum TrendRollup {
    public typealias Grain = Components.Schemas.ResolvedTrend.GrainPayload

    /// The first request for "All": monthly rollups over the ten years ending on `end`.
    public static func query(endingOn end: LocalDate) -> (start: LocalDate, grain: Grain) {
        (end.adding(days: -3659), .month)
    }

    /// The weekly request to make instead, when the first month with data is within two years.
    public static func refinement(of trend: Components.Schemas.ResolvedTrend, endingOn end: LocalDate) -> (start: LocalDate, grain: Grain)? {
        guard trend.grain == .month, let first = trend.buckets.first(where: { $0.n > 0 }), let start = LocalDate(first.startDate),
              start > end.adding(days: -730) else { return nil }
        return (start, .week)
    }

    /// The mean per bucket as a series, its min–max as a band and the bar bin; nil when no bucket
    /// has data. Each point sits at its bucket's first local day.
    public static func chart(_ trend: Components.Schemas.ResolvedTrend, timeZone: TimeZone) -> (series: ChartSeries, band: ChartBand, bin: Calendar.Component)? {
        guard let first = trend.buckets.firstIndex(where: { $0.n > 0 }) else { return nil }
        let buckets = trend.buckets[first...].compactMap { b in LocalDate(b.startDate).map { ($0.start(in: timeZone), b) } }
        let weekly = trend.grain == .week
        return (
            ChartSeries(label: weekly ? "Weekly mean" : "Monthly mean", points: buckets.map { ChartPoint(x: $0.0, y: $0.1.mean) }),
            ChartBand(label: weekly ? "Weekly range" : "Monthly range", points: buckets.map { .init(x: $0.0, low: $0.1.min, high: $0.1.max) }),
            weekly ? .weekOfYear : .month
        )
    }
}
