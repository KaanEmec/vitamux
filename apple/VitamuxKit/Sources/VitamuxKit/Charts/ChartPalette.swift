import SwiftUI

// Flat colour lookups for the chart kit (docs/architecture/ios-app.md#charts): a hue and a tile
// tint per metric, a colour per sleep stage and per source, and the six data states, each a
// light/dark pair (docs/architecture/ios-design.md#tokens). A hue names the metric only; it never
// says whether a value is good.

/// The hue of a metric's marks.
public enum MetricHue: String, Sendable, CaseIterable {
    case heartRate, hrv, steps, sleep, bloodPressure, spo2, weight, activeEnergy, vo2, lab, other

    /// The marks' colour: the darker shade in the light theme (docs/architecture/ios-design.md#tokens).
    public var color: Color {
        switch self {
        case .heartRate: Color(light: 0xBE123C, dark: 0xFB7185)
        case .hrv: Color(light: 0x6D28D9, dark: 0xA78BFA)
        case .steps: Color(light: 0x047857, dark: 0x34D399)
        case .sleep: Color(light: 0x4338CA, dark: 0x818CF8)
        case .bloodPressure: Color(light: 0xBE185D, dark: 0xF472B6)
        case .spo2: Color(light: 0x0E7490, dark: 0x22D3EE)
        case .weight: Color(light: 0xB45309, dark: 0xFBBF24)
        case .activeEnergy: Color(light: 0xC2410C, dark: 0xFB923C)
        case .vo2: Color(light: 0x0F766E, dark: 0x2DD4BF)
        case .lab: Color(light: 0x4D7C0F, dark: 0xA3E635)
        case .other: Color(light: 0x4F5861, dark: 0x94A3B8)
        }
    }

    /// The fill of the metric's icon tile behind `color` (the panel's `--metric-<hue>-tint`).
    public var tint: Color {
        switch self {
        case .heartRate: Color(light: 0xFFE4E6, dark: 0x2E151B)
        case .hrv: Color(light: 0xEDE9FE, dark: 0x211A33)
        case .steps: Color(light: 0xD1FAE5, dark: 0x0F2A20)
        case .sleep: Color(light: 0xE0E7FF, dark: 0x1B1D36)
        case .bloodPressure: Color(light: 0xFCE7F3, dark: 0x2E1526)
        case .spo2: Color(light: 0xCFFAFE, dark: 0x0F2A30)
        case .weight: Color(light: 0xFEF3C7, dark: 0x2B2210)
        case .activeEnergy: Color(light: 0xFFEDD5, dark: 0x2E1C10)
        case .vo2: Color(light: 0xE0F2EF, dark: 0x0F2321)
        case .lab: Color(light: 0xECFCCB, dark: 0x1F2A10)
        case .other: Color(light: 0xE7EEEE, dark: 0x1B1F23)
        }
    }

    /// The hue of a catalogue code, given its section (GET /metrics `section`) when known.
    public static func of(code: String, section: String? = nil) -> MetricHue {
        if code.hasPrefix("hrv_") { return .hrv }
        if code.contains("vo2") { return .vo2 }
        if code.hasSuffix("_energy") { return .activeEnergy }
        if code.hasPrefix("bp_") || code == "blood_pressure" { return .bloodPressure }
        if code.hasPrefix("spo2") { return .spo2 }
        if code.hasPrefix("lab_") { return .lab }
        return switch section {
        case "Activity", "Mobility": .steps
        case "Heart and circulation": .heartRate
        case "Blood pressure": .bloodPressure
        case "Respiration and oxygen": .spo2
        case "Body composition", "Temperature": .weight
        case "Glucose and metabolism", "Nutrition and intake": .activeEnergy
        case "Sleep": .sleep
        case "Lab": .lab
        default: code.hasPrefix("sleep") ? .sleep : .other
        }
    }
}

/// Sleep stages in display order (awake on top), with their labels and colours: the twin of
/// `web/src/lib/charts/sleep.ts`.
public enum SleepStageKind: String, Sendable, CaseIterable {
    case awake, rem, light, deep
    case asleep = "asleep_unspecified"
    case inBed = "in_bed"
    case restless
    case outOfBed = "out_of_bed"
    case unknown

    public init(code: String) { self = SleepStageKind(rawValue: code) ?? .unknown }

    public var label: String {
        switch self {
        case .awake: "Awake"
        case .rem: "REM"
        case .light: "Light"
        case .deep: "Deep"
        case .asleep: "Asleep"
        case .inBed: "In bed"
        case .restless: "Restless"
        case .outOfBed: "Out of bed"
        case .unknown: "Unknown"
        }
    }

    public var color: Color {
        switch self {
        case .deep: Color(light: 0x4F46E5, dark: 0x6366F1)
        case .light: Color(light: 0x0284C7, dark: 0x38BDF8)
        case .rem: Color(light: 0x9333EA, dark: 0xC084FC)
        case .awake: Color(hex: 0x71717A)
        default: Color(light: 0x5F6873, dark: 0x94A3B8)
        }
    }
}

/// The stable colour and name of a provider; unknown providers get one of three extra colours by
/// a stable hash, so a source keeps its colour everywhere (the twin of `web/src/lib/ui/source.ts`).
public enum SourceStyle {
    public static func color(_ provider: String) -> Color {
        switch provider {
        case "apple_health": return Color(light: 0xDB2777, dark: 0xFF8FB3)
        case "whoop": return Color(light: 0xC2410C, dark: 0xFFA463)
        case "withings": return Color(light: 0x4D7C0F, dark: 0xA3E635)
        case "garmin": return Color(light: 0x4F46E5, dark: 0x8C9EFF)
        case "manual", "push", "file_import": return Color(light: 0x4F5861, dark: 0xA1A9B1)
        default:
            let extra: [(UInt32, UInt32)] = [(0x0E7490, 0x67E8F9), (0xC2410C, 0xFDBA74), (0x86198F, 0xF0ABFC)]
            let hash = provider.unicodeScalars.reduce(UInt32(0)) { $0 &* 31 &+ $1.value }
            let pick = extra[Int(hash % 3)]
            return Color(light: pick.0, dark: pick.1)
        }
    }

    public static func label(_ provider: String) -> String {
        switch provider {
        case "apple_health": "Apple Health"
        case "whoop": "WHOOP"
        case "withings": "Withings"
        case "garmin": "Garmin"
        case "manual": "Manual"
        default: provider
        }
    }

    /// Dash patterns for overlay series: sources differ by dash as well as colour.
    static let dashes: [[CGFloat]] = [[], [5, 4], [1.5, 3], [10, 3, 2, 3], [4, 4]]
}

/// The six data states of a resolved value: a shape, a colour and a word, so status never depends
/// on colour alone (the twin of `web/src/lib/ui/status.ts`).
public enum DataStatus: String, Sendable, CaseIterable {
    case direct, fallback, calculated, overridden, partial
    case noData = "no_data"

    /// A resolved value's display status: `partial` wins over a direct or calculated result.
    public init(status: String, partial: Bool = false) {
        let base = DataStatus(rawValue: status) ?? .noData
        self = partial && base != .noData && base != .overridden ? .partial : base
    }

    public var label: String {
        switch self {
        case .direct: "Direct"
        case .fallback: "Fallback"
        case .calculated: "Calculated"
        case .overridden: "Overridden"
        case .partial: "Partial"
        case .noData: "No data"
        }
    }

    public var color: Color {
        switch self {
        case .direct: Color(light: 0x0F766E, dark: 0x2DD4BF)
        case .fallback: Color(light: 0xB45309, dark: 0xF2B54A)
        case .calculated: Color(light: 0x2563EB, dark: 0x7CB4FF)
        case .overridden: Color(light: 0x7C3AED, dark: 0xB99CFF)
        case .partial: Color(light: 0x5F6873, dark: 0x9AA3AB)
        case .noData: Color(light: 0x868F97, dark: 0x6B737B)
        }
    }
}

extension Color {
    init(hex: UInt32) {
        self.init(.sRGB, red: Double(hex >> 16 & 0xFF) / 255, green: Double(hex >> 8 & 0xFF) / 255, blue: Double(hex & 0xFF) / 255)
    }

    /// A colour that follows the colour scheme.
    init(light: UInt32, dark: UInt32) {
        #if canImport(UIKit)
        self.init(uiColor: UIColor { $0.userInterfaceStyle == .dark ? UIColor(Color(hex: dark)) : UIColor(Color(hex: light)) })
        #else
        self.init(nsColor: NSColor(name: nil) { $0.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua ? NSColor(Color(hex: dark)) : NSColor(Color(hex: light)) })
        #endif
    }

    /// The muted fill of "No data" stretches and empty cells.
    static let chartMuted = Color(light: 0xC8CDD2, dark: 0x2C3137)
}
