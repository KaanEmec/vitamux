import Foundation
import OpenAPIRuntime
import SwiftUI
import VitamuxKit

// Display helpers Explore's screens share: resolved values as numbers, rule groups and the data
// status word (the app-wide formats and labels are in Shared/Format.swift). Neutral copy only (docs/architecture/frontend.md#design-system).

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

    /// A JSON number as a Double, nil for anything else.
    static func double(_ value: (any Sendable)?) -> Double? {
        switch value {
        case let number as Double: number
        case let number as Int: Double(number)
        case let number as Int64: Double(number)
        default: nil
        }
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

