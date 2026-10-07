import Foundation

// Value types the chart views share, plus decimation, formatting and a small memo. Plain values;
// no view state lives here.

/// One value at an instant; a nil value is a gap (never zero).
public struct ChartPoint: Hashable, Sendable {
    public var x: Date
    public var y: Double?
    /// Non-direct statuses get a marker (shape, colour and word).
    public var status: DataStatus?
    /// The providers behind the value (GET /resolved/series `providers`), for the callout.
    public var providers: [String]?

    public init(x: Date, y: Double?, status: DataStatus? = nil, providers: [String]? = nil) {
        self.x = x
        self.y = y
        self.status = status
        self.providers = providers
    }
}

/// One line on a `TimeSeries`.
public struct ChartSeries: Hashable, Sendable, Identifiable {
    public enum Style: Sendable {
        /// A line (the default).
        case line
        /// A draft overlay for the rule lens: dashed, in the text colour.
        case ghost
        /// Readings without a line, e.g. lab results.
        case dots
        /// A solid line in the metric hue over the readings, e.g. a moving average.
        case trend
    }

    public var id: String { label }
    public var label: String
    /// Ascending by `x`.
    public var points: [ChartPoint]
    /// Provider code: picks the stable source colour. Without it the series uses the metric hue.
    public var source: String?
    public var style: Style

    public init(label: String, points: [ChartPoint], source: String? = nil, style: Style = .line) {
        self.label = label
        self.points = points
        self.source = source
        self.style = style
    }
}

/// A shaded low–high range over time (the min–max band of an intensive metric).
public struct ChartBand: Hashable, Sendable {
    public struct Point: Hashable, Sendable {
        public var x: Date
        public var low: Double?
        public var high: Double?
        public init(x: Date, low: Double?, high: Double?) {
            self.x = x
            self.low = low
            self.high = high
        }
    }

    public var label: String
    public var points: [Point]
    public init(label: String, points: [Point]) {
        self.label = label
        self.points = points
    }
}

/// A labelled horizontal line ("30-day mean"), with an optional shaded range across the chart
/// (a lab result's printed range, "as printed").
public struct ChartBaseline: Hashable, Sendable {
    public var value: Double?
    public var label: String
    public var range: ClosedRange<Double>?
    public init(value: Double?, label: String, range: ClosedRange<Double>? = nil) {
        self.value = value
        self.label = label
        self.range = range
    }
}

/// A span or an instant drawn under a time chart's marks and named in its legend: shading (a
/// night), a tint in the metric hue (a workout) or a dashed line (now).
public struct ChartOverlay: Hashable, Sendable, Identifiable {
    public enum Style: Hashable, Sendable {
        case shade, tint, line
    }

    public var label: String
    public var start: Date
    /// Equal to `start` for a line.
    public var end: Date
    public var style: Style
    public var id: String { "\(style)-\(start.timeIntervalSinceReferenceDate)-\(label)" }

    public init(label: String, start: Date, end: Date? = nil, style: Style) {
        self.label = label
        self.start = start
        self.end = max(end ?? start, start)
        self.style = style
    }

    /// The overlays inside `domain`, clipped to it.
    static func clipped(_ overlays: [ChartOverlay], to domain: ClosedRange<Date>) -> [ChartOverlay] {
        overlays.compactMap { o in
            var o = o
            if o.style == .line { return domain.contains(o.start) ? o : nil }
            o.start = max(o.start, domain.lowerBound)
            o.end = min(o.end, domain.upperBound)
            return o.start < o.end ? o : nil
        }
    }
}

/// The accessible table behind a chart, newest first.
public struct ChartTable: Sendable {
    public struct Row: Identifiable, Sendable {
        public var id: String
        public var cells: [String]
    }

    public var columns: [String]
    public var rows: [Row]
}

/// Callout content for one selected point: the twin of the web's `Tip`.
public struct ChartTip: Hashable, Sendable {
    public struct Row: Hashable, Sendable {
        public var label: String
        public var value: String
        public var source: String?
        public var status: DataStatus?
    }

    public var title: String
    public var value: String
    public var unit: String?
    public var status: DataStatus?
    public var providers: [String] = []
    public var rows: [Row] = []
    public var note: String?

    /// The same content as one sentence, for VoiceOver.
    var spoken: String {
        let status = status.map { " (\($0.label))" } ?? ""
        let from = providers.isEmpty ? "" : " from \(providers.map(SourceStyle.label).joined(separator: ", "))"
        let lead = "\(value)\(unit.map { " \($0)" } ?? "")\(status)\(from)"
        let rest = rows.map { "\($0.label) \($0.value)\($0.status.map { " (\($0.label))" } ?? "")" }
        return "\(title): \(([lead] + rest).joined(separator: ", "))\(note.map { ". \($0)" } ?? "")"
    }
}

// MARK: Decimation

/// A point as drawn: gaps become a new `segment`, so a line never bridges missing data.
struct DrawnPoint: Hashable, Sendable {
    var x: Date
    var y: Double
    var segment: Int
}

/// Above this many points a series is reduced to min/max per pixel column.
let decimationThreshold = 2_000

/// The rows to draw for `points` over `domain`: a dense series (a 14,400-point day) keeps the min
/// and max of each of `columns` columns, in time order, which keeps its shape at any density.
/// A nil stays a gap; series below about 2,000 points are drawn as they are.
func decimate(_ points: [ChartPoint], over domain: ClosedRange<Date>, columns: Int) -> [DrawnPoint] {
    var out: [DrawnPoint] = []
    var segment = 0
    var open = false
    let reduce = points.count > max(decimationThreshold, columns * 2)
    out.reserveCapacity(reduce ? columns * 2 + 16 : points.count)
    guard reduce else {
        for p in points {
            guard let y = p.y, y.isFinite else {
                if open { segment += 1; open = false }
                continue
            }
            out.append(DrawnPoint(x: p.x, y: y, segment: segment))
            open = true
        }
        return out
    }
    let lo = domain.lowerBound.timeIntervalSinceReferenceDate
    let width = max(domain.upperBound.timeIntervalSinceReferenceDate - lo, 1) / Double(max(columns, 1))
    var column = Int.min
    var minIndex = -1
    var maxIndex = -1
    func flush() {
        guard minIndex >= 0 else { return }
        let first = min(minIndex, maxIndex)
        let second = max(minIndex, maxIndex)
        out.append(DrawnPoint(x: points[first].x, y: points[first].y!, segment: segment))
        if first != second { out.append(DrawnPoint(x: points[second].x, y: points[second].y!, segment: segment)) }
        minIndex = -1
        maxIndex = -1
        open = true
    }
    for (i, p) in points.enumerated() {
        guard let y = p.y, y.isFinite else {
            flush()
            if open { segment += 1; open = false }
            column = Int.min
            continue
        }
        let c = Int(((p.x.timeIntervalSinceReferenceDate - lo) / width).rounded(.down))
        if c != column {
            flush()
            column = c
            minIndex = i
            maxIndex = i
        } else if y < points[minIndex].y! {
            minIndex = i
        } else if y > points[maxIndex].y! {
            maxIndex = i
        }
    }
    flush()
    return out
}

/// Runs of missing values, from the last value before to the first value after (as the web shades them).
func holes(in points: [ChartPoint]) -> [ClosedRange<Date>] {
    var out: [ClosedRange<Date>] = []
    var i = 0
    while i < points.count {
        guard points[i].y == nil else { i += 1; continue }
        var j = i
        while j + 1 < points.count, points[j + 1].y == nil { j += 1 }
        let from = points[max(i - 1, 0)].x
        let to = points[min(j + 1, points.count - 1)].x
        if to > from { out.append(from ... to) }
        i = j + 1
    }
    return out
}

/// [min, max] of the values, padded by `pad` of the span on both sides; `empty` when there are none.
func extent(_ values: some Sequence<Double?>, pad: Double = 0, empty: ClosedRange<Double> = 0 ... 1) -> ClosedRange<Double> {
    var lo = Double.infinity
    var hi = -Double.infinity
    for case let v? in values where v.isFinite {
        lo = min(lo, v)
        hi = max(hi, v)
    }
    guard lo <= hi else { return empty }
    let p = (hi - lo == 0 ? (abs(hi) == 0 ? 1 : abs(hi)) : hi - lo) * pad
    return (lo - p) ... (hi + p)
}

/// Index of the instant in ascending `xs` nearest to `x`, or nil when empty.
func nearest<C: RandomAccessCollection<Date>>(_ xs: C, to x: Date) -> Int? where C.Index == Int {
    guard !xs.isEmpty else { return nil }
    var lo = 0
    var hi = xs.count - 1
    while hi - lo > 1 {
        let mid = (lo + hi) / 2
        if xs[mid] < x { lo = mid } else { hi = mid }
    }
    return abs(xs[hi].timeIntervalSince(x)) < abs(xs[lo].timeIntervalSince(x)) ? hi : lo
}

/// The x range of a set of instants; a single instant gets a day around it.
func timeDomain(_ xs: some Sequence<Date>) -> ClosedRange<Date> {
    var lo = Date.distantFuture
    var hi = Date.distantPast
    for x in xs {
        lo = min(lo, x)
        hi = max(hi, x)
    }
    guard lo <= hi else { return Date(timeIntervalSince1970: 0) ... Date(timeIntervalSince1970: 86_400) }
    return lo < hi ? lo ... hi : lo.addingTimeInterval(-43_200) ... hi.addingTimeInterval(43_200)
}

// MARK: Formatting

enum ChartFormat {
    /// "Sun 4 Oct 2026, 07:12" (or without the time) in the timezone.
    static func instant(_ date: Date, in timeZone: TimeZone, withTime: Bool = true) -> String {
        var style = Date.FormatStyle(date: .abbreviated, time: withTime ? .shortened : .omitted)
        style.timeZone = timeZone
        return date.formatted(style.weekday(.abbreviated))
    }

    /// "07:12" in the timezone.
    static func clock(_ date: Date, in timeZone: TimeZone) -> String {
        var style = Date.FormatStyle(date: .omitted, time: .shortened)
        style.timeZone = timeZone
        return date.formatted(style)
    }

    /// A number with at most `digits` decimals, in the person's locale.
    static func number(_ value: Double, digits: Int = 1) -> String {
        value.formatted(.number.precision(.fractionLength(0 ... digits)))
    }

    /// The value with its unit, or "–" for none.
    static func value(_ value: Double?, unit: String) -> String {
        guard let value else { return "–" }
        return unit.isEmpty ? number(value) : "\(number(value)) \(unit)"
    }
}

// MARK: Memo

/// Keeps one derived value until its inputs change. Held in `@State` and read in `body`, so a
/// re-render with the same inputs (array equality starts with an identity check) costs nothing.
final class Memo<Key: Equatable, Value> {
    private var key: Key?
    private var value: Value?

    func callAsFunction(_ key: Key, _ make: (Key) -> Value) -> Value {
        if let value, self.key == key { return value }
        let made = make(key)
        self.key = key
        value = made
        return made
    }
}
