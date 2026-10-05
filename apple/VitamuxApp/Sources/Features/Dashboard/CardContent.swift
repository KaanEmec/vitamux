import Foundation
import OpenAPIRuntime
import VitamuxKit

/// What a card shows for one `GET /resolved/summary` entry: the value, a neutral delta against the
/// 30-day mean, a sparkline, source chips and the status (the twin of the panel's `cardView` in
/// web/src/lib/dashboard/summary.ts). Plain statistics only; nothing is judged.
struct CardContent: Equatable {
    struct Chip: Equatable {
        var provider: String?
        var label: String
    }

    struct Stage: Equatable {
        var kind: SleepStageKind
        var seconds: Double
    }

    var value = "–"
    var unit = ""
    /// A second line: the pulse of a blood-pressure reading, the time in bed.
    var sub = ""
    var delta = ""
    var values: [Double?] = []
    var band: ClosedRange<Double>?
    var mean: Double?
    var bars = false
    var status = DataStatus.noData
    /// Anything stored in the last 90 days, or a value on the day.
    var hasData = false
    var chips: [Chip] = []
    /// Sleep: seconds per stage in display order; nil when the source has no stage data.
    var stages: [Stage]?

    /// The code whose daily values draw a family's sparkline.
    static let lead = ["sleep": "sleep_total", "blood_pressure": "bp_systolic"]

    private static let stageCodes: [(SleepStageKind, String)] = [
        (.deep, "sleep_deep"), (.light, "sleep_light"), (.rem, "sleep_rem"), (.asleep, "sleep_unspecified"), (.awake, "sleep_awake"),
    ]

    init() {}

    init(code: String, summary s: Components.Schemas.MetricSummary, additive: Bool) {
        let v = s.value
        let lead = Self.lead[code]
        let point = { (x: OpenAPIValueContainer?) in lead.map { Self.part(x, $0) } ?? Self.number(x?.value) }
        let thirty = s.stats.count > 1 ? s.stats[1] : nil
        let component = lead.flatMap { thirty?.components?.additionalProperties[$0] }
        let mean = lead == nil ? thirty?.mean : component?.mean
        values = s.sparkline.map { point($0.value) }
        bars = additive || code == "sleep"
        status = DataStatus(status: v.status.rawValue, partial: v.partial ?? false)
        hasData = v.status != .noData || s.sparkline.contains { $0.status != .noData } || s.stats.contains { $0.n > 0 }
        chips = Self.chips(v.inputs ?? [])
        self.mean = mean
        if mean != nil, (thirty?.n ?? 0) > 1, !bars {
            let low = lead == nil ? thirty?.min : component?.min
            let high = lead == nil ? thirty?.max : component?.max
            if let low, let high, low <= high { band = low...high }
        }

        switch code {
        case "sleep":
            if let total = Self.part(v.value, "sleep_total") {
                value = Self.hm(total)
                unit = "asleep"
                if let inBed = Self.part(v.value, "sleep_in_bed") { sub = "in bed \(Self.hm(inBed))" }
                let parts = Self.stageCodes.map { Stage(kind: $0.0, seconds: Self.part(v.value, $0.1) ?? 0) }
                if parts.contains(where: { $0.seconds > 0 && $0.kind != .asleep }) { stages = parts.filter { $0.seconds > 0 } }
            }
            if let mean { delta = "30-day mean \(Self.hm(mean))" }
        case "blood_pressure":
            if let sys = Self.part(v.value, "bp_systolic"), let dia = Self.part(v.value, "bp_diastolic") {
                value = "\(Self.format(sys))/\(Self.format(dia))"
                unit = "mmHg"
                if let pulse = Self.part(v.value, "bp_pulse") { sub = "pulse \(Self.format(pulse)) bpm" }
            }
            if let mean, let diastolic = thirty?.components?.additionalProperties["bp_diastolic"]?.mean {
                delta = "30-day mean \(Self.format(mean))/\(Self.format(diastolic))"
            }
        default:
            if let n = Self.number(v.value?.value) {
                value = Self.format(n)
                let canonical = v.unit ?? s.unit ?? ""
                unit = canonical == "count" ? (v.partial == true ? "so far" : "") : canonical
                if let mean { delta = additive ? "30-day mean \(Self.format(mean))" : "\(Self.signed(n - mean)) vs 30-day mean" }
            }
        }
    }

    /// One chip per selected rule group, named after its provider or device group.
    private static func chips(_ inputs: [Components.Schemas.ResolvedInput]) -> [Chip] {
        var seen: Set<String> = []
        return inputs.compactMap { input in
            guard input.selected == true, let group = input.group, seen.insert(group).inserted else { return nil }
            let provider = input.sources?.first?.provider
            let label = provider == group ? providerLabel(group) : groupLabel(group)
            return Chip(provider: provider, label: label)
        }
    }

    /// The panel's `groupLabel`: devices and `<brand>_apple` relays by name, else the code made readable.
    private static func groupLabel(_ group: String) -> String {
        switch group {
        case "apple_watch": return "Apple Watch"
        case "iphone": return "iPhone"
        default:
            if group.hasSuffix("_apple") { return "\(providerLabel(String(group.dropLast(6)))) via Apple Health" }
            return metricLabel(group)
        }
    }

    // MARK: - Values and formatting

    static func number(_ value: (any Sendable)?) -> Double? {
        switch value {
        case let d as Double: d
        case let i as Int: Double(i)
        default: nil
        }
    }

    /// One code of a family value (`{"bp_systolic": 118, …}`).
    static func part(_ value: OpenAPIValueContainer?, _ code: String) -> Double? {
        guard let object = value?.value as? [String: (any Sendable)?], let item = object[code] else { return nil }
        return number(item)
    }

    /// "8,412", "52", "74.6": whole numbers from 1,000, else at most one decimal.
    static func format(_ n: Double) -> String {
        abs(n) >= 1000 ? n.formatted(.number.precision(.fractionLength(0))) : n.formatted(.number.precision(.fractionLength(0...1)))
    }

    /// "+4", "−1.5", "±0": a difference rounded as it is shown.
    static func signed(_ d: Double) -> String {
        let r = abs(d) >= 100 ? d.rounded() : (d * 10).rounded() / 10
        let sign = r > 0 ? "+" : r < 0 ? "−" : "±"
        return sign + format(abs(r))
    }

    /// "7h 19m" for seconds.
    static func hm(_ seconds: Double) -> String {
        let minutes = Int((seconds / 60).rounded())
        return "\(minutes / 60)h " + String(format: "%02dm", minutes % 60)
    }
}
