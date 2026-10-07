import Foundation
import OpenAPIRuntime

/// A calendar date as the API writes it (`YYYY-MM-DD`): a day in the owner's timezone, never UTC.
/// The generated client carries these as `String`; convert at the edges with `description`.
public struct LocalDate: Hashable, Comparable, Sendable, Codable, LosslessStringConvertible {
    public let year: Int
    public let month: Int
    public let day: Int

    /// Parses `YYYY-MM-DD`; nil for anything else or a date that does not exist.
    public init?(_ description: String) {
        let parts = description.split(separator: "-", omittingEmptySubsequences: false)
        guard parts.count == 3, parts[0].count == 4, parts[1].count == 2, parts[2].count == 2,
              let year = Int(parts[0]), let month = Int(parts[1]), let day = Int(parts[2])
        else { return nil }
        let components = DateComponents(year: year, month: month, day: day)
        guard components.isValidDate(in: Self.calendar(in: .gmt)) else { return nil }
        self.year = year
        self.month = month
        self.day = day
    }

    /// The local date of `instant` in `timeZone`.
    public init(_ instant: Date, in timeZone: TimeZone) {
        let parts = Self.calendar(in: timeZone).dateComponents([.year, .month, .day], from: instant)
        year = parts.year!
        month = parts.month!
        day = parts.day!
    }

    /// Today in `timeZone`.
    public static func today(in timeZone: TimeZone, now: Date = .now) -> LocalDate {
        LocalDate(now, in: timeZone)
    }

    /// The first instant of this day in `timeZone` (local midnight, or the first instant after a
    /// transition that skips it).
    public func start(in timeZone: TimeZone) -> Date {
        let calendar = Self.calendar(in: timeZone)
        return calendar.startOfDay(for: calendar.date(from: DateComponents(year: year, month: month, day: day, hour: 12))!)
    }

    public func adding(days: Int) -> LocalDate {
        let calendar = Self.calendar(in: .gmt)
        let noon = calendar.date(from: DateComponents(year: year, month: month, day: day, hour: 12))!
        return LocalDate(calendar.date(byAdding: .day, value: days, to: noon)!, in: .gmt)
    }

    public var description: String {
        String(format: "%04d-%02d-%02d", year, month, day)
    }

    public static func < (lhs: LocalDate, rhs: LocalDate) -> Bool {
        (lhs.year, lhs.month, lhs.day) < (rhs.year, rhs.month, rhs.day)
    }

    public init(from decoder: any Decoder) throws {
        let text = try decoder.singleValueContainer().decode(String.self)
        guard let date = LocalDate(text) else {
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "Expected YYYY-MM-DD."))
        }
        self = date
    }

    public func encode(to encoder: any Encoder) throws {
        var container = encoder.singleValueContainer()
        try container.encode(description)
    }

    private static func calendar(in timeZone: TimeZone) -> Calendar {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = timeZone
        return calendar
    }
}

extension Array where Element == Components.Schemas.TimezonePeriod {
    /// The owner's timezone at `instant`, from `GET /timezone-periods`: the period that contains it.
    /// Nil before the first period or while none is configured.
    public func timeZone(at instant: Date = .now) -> TimeZone? {
        first { $0.validFrom <= instant && ($0.validTo.map { instant < $0 } ?? true) }
            .flatMap { TimeZone(identifier: $0.tz) }
    }
}

/// RFC 3339 with or without fractional seconds, as the server writes instants (Go's RFC3339Nano
/// drops a zero fraction). The client's `Configuration` uses it for every `date-time`.
public struct RFC3339DateTranscoder: DateTranscoder {
    private static let fractional = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
    private static let whole = Date.ISO8601FormatStyle()

    public init() {}

    public func encode(_ date: Date) throws -> String {
        Self.fractional.format(date)
    }

    public func decode(_ text: String) throws -> Date {
        if let date = try? Self.fractional.parse(text) { return date }
        return try Self.whole.parse(text)
    }
}
