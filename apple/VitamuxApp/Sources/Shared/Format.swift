import Foundation
import VitamuxKit

// The app's shared formats and labels, as the panel's: values, dates, counts, and the names of
// metrics, providers and rule groups. Neutral copy only (docs/architecture/frontend.md#design-system).

enum Format {
    /// "52 bpm", "8,412 steps", "7 h 15" for seconds, "–" for none.
    static func value(_ value: Double?, unit: String) -> String {
        guard let value else { return "–" }
        switch unit {
        case "s": return duration(value)
        case "count": return number(value)
        case "": return number(value)
        default: return "\(number(value)) \(unit)"
        }
    }

    /// A resolved value as printed: a number with its unit, blood pressure as "121/79".
    static func resolved(_ resolved: ResolvedValue?, code: String) -> String {
        guard let resolved, resolved.status != .noData, let container = resolved.value else { return "–" }
        if let number = container.number(for: code) { return value(number, unit: resolved.unit ?? "") }
        let parts = container.components
        if let sys = parts["bp_systolic"], let dia = parts["bp_diastolic"] { return "\(number(sys))/\(number(dia)) mmHg" }
        return parts.sorted { $0.key < $1.key }.map { "\(metricLabel($0.key)) \(number($0.value))" }.joined(separator: ", ")
    }

    static func number(_ value: Double) -> String {
        value.formatted(.number.precision(.fractionLength(0...1)))
    }

    /// Seconds as "7 h 15".
    static func duration(_ seconds: Double) -> String {
        let minutes = Int((seconds / 60).rounded())
        return "\(minutes / 60) h \(String(format: "%02d", minutes % 60))"
    }

    /// A signed difference without judgement: "+1.2", "−2", "±0".
    static func signed(_ value: Double) -> String {
        let sign = value > 0 ? "+" : value < 0 ? "−" : "±"
        return sign + number(abs(value))
    }

    /// "Sat 4 Oct 2026" for a local date (formatted in UTC, so the date never shifts).
    static func day(_ date: LocalDate, weekday: Bool = true) -> String {
        var style = Date.FormatStyle(date: .abbreviated, time: .omitted)
        style.timeZone = .gmt
        return date.start(in: .gmt).formatted(weekday ? style.weekday(.abbreviated) : style)
    }

    static func day(_ text: String, weekday: Bool = true) -> String {
        LocalDate(text).map { day($0, weekday: weekday) } ?? text
    }

    /// "4 Oct 2026, 07:12".
    static func instant(_ date: Date) -> String {
        date.formatted(date: .abbreviated, time: .shortened)
    }

    /// "3 Oct 2026": a day without its time.
    static func date(_ date: Date) -> String {
        date.formatted(date: .abbreviated, time: .omitted)
    }

    /// "5 minutes ago", "yesterday".
    static func ago(_ date: Date) -> String {
        date.formatted(.relative(presentation: .named))
    }

    /// "1 page", "3 pages"; `many` for an irregular plural.
    static func plural(_ count: Int, _ one: String, _ many: String? = nil) -> String {
        "\(count) \(count == 1 ? one : many ?? one + "s")"
    }

    /// A typed decimal in the person's locale or with a point; nil when it is not a number.
    static func decimal(_ text: String) -> Double? {
        let trimmed = text.trimmingCharacters(in: .whitespaces)
        return (try? Double(trimmed, format: .number)) ?? Double(trimmed.replacingOccurrences(of: ",", with: "."))
    }
}

/// `resting_heart_rate` → "Resting heart rate", as the panel's `metricLabel`.
func metricLabel(_ code: String) -> String {
    let words = code.replacingOccurrences(of: "_", with: " ")
    return words.prefix(1).uppercased() + words.dropFirst()
}

/// The provider's name, else the code made readable (as the panel's `providerLabel`).
func providerLabel(_ code: String) -> String {
    let known = ["withings": "Withings", "apple_health": "Apple Health", "manual": "Manual entries",
                 "garmin": "Garmin Connect", "whoop": "WHOOP", "ultrahuman": "Ultrahuman", "oura": "Oura"]
    return known[code] ?? metricLabel(code)
}

/// A rule group's name, as the panel's `groupLabel`: a provider, Apple Watch, iPhone or an app via
/// Apple Health.
func groupLabel(_ id: String) -> String {
    switch id {
    case "apple_watch": return "Apple Watch"
    case "iphone": return "iPhone"
    default:
        if id.hasSuffix("_apple") { return "\(providerLabel(String(id.dropLast(6)))) via Apple Health" }
        return providerLabel(id)
    }
}
