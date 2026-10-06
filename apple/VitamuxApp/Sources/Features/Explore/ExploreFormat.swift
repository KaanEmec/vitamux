import Foundation
import OpenAPIRuntime
import SwiftUI
import VitamuxKit

// Display helpers Explore's screens share: resolved values as numbers and text, dates, rule groups
// and the data status word. Neutral copy only (docs/architecture/frontend.md#design-system).

typealias ResolvedValue = Components.Schemas.ResolvedValue

extension OpenAPIValueContainer {
    /// A resolved value as a number: itself, or the metric's entry of a family value.
    func number(for code: String) -> Double? {
        if let number = Self.double(value) { return number }
        return (value as? [String: (any Sendable)?]).flatMap { Self.double($0[code] ?? nil) }
    }

    /// A family value's numeric components by code (`bp_systolic`, …).
    var components: [String: Double] {
        ((value as? [String: (any Sendable)?]) ?? [:]).compactMapValues { Self.double($0) }
    }

    private static func double(_ value: (any Sendable)?) -> Double? {
        switch value {
        case let number as Double: number
        case let number as Int: Double(number)
        case let number as Int64: Double(number)
        default: nil
        }
    }
}

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

/// The providers behind a resolved day: its selected inputs' sources, else its group.
func dayProviders(_ value: ResolvedValue?) -> [String] {
    guard let value, value.status != .noData else { return [] }
    var seen = Set<String>()
    let used = (value.inputs ?? []).filter { $0.selected == true }.flatMap { ($0.sources ?? []).map(\.provider) }.filter { seen.insert($0).inserted }
    if !used.isEmpty { return used }
    let group = value.selected ?? value.inputs?.first { $0.selected == true }?.group ?? nil
    return group.map { [$0] } ?? []
}

/// The rule groups a window's inputs name, for "Force a source".
func ruleGroups(_ value: ResolvedValue?) -> [String] {
    (value?.inputs ?? []).compactMap(\.group)
}

/// A resolved value's warning codes with the group they concern.
func warningText(_ value: ResolvedValue?) -> [String] {
    (value?.warnings ?? []).map { warning in warning.group.map { "\(warning.code) (\(groupLabel($0)))" } ?? warning.code }
}

/// "Built-in rule, first available".
func ruleSummary(_ rule: Components.Schemas.RuleRef) -> String {
    let name = rule.ref.hasPrefix("default:") ? "Default rule" : rule.ref.hasPrefix("builtin:") ? "Built-in rule" : "Rule v\(rule.version)"
    return rule.strategy.map { "\(name), \($0.replacingOccurrences(of: "_", with: " "))" } ?? name
}

extension DataStatus {
    init(_ value: ResolvedValue?) {
        self.init(status: value?.status.rawValue ?? "no_data", partial: value?.partial ?? false)
    }

    /// A shape per state, so status never depends on colour alone.
    var symbol: String {
        switch self {
        case .direct: "circle.fill"
        case .fallback: "diamond.fill"
        case .calculated: "triangle.fill"
        case .overridden: "square.fill"
        case .partial: "circle.lefthalf.filled"
        case .noData: "circle.dashed"
        }
    }
}

