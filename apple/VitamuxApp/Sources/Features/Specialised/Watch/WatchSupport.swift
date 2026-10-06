import Foundation
import OpenAPIRuntime
import SwiftUI
import VitamuxKit

// What the Apple Watch views (J22.18, docs/adr/0024-watch-data.md) share: reading an event's or a
// value's `context`, the event families, and Apple's own words. Copy rule: a value is shown as
// recorded, with its source. An ECG classification is HealthKit's label, never rephrased; nothing
// rates an ECG, rhythm, cycle or mood value, and no mark is coloured by its result.

typealias SourceMeasurement = Components.Schemas.Measurement

extension OpenAPIObjectContainer {
    func text(_ key: String) -> String? {
        (value[key] ?? nil) as? String
    }

    func number(_ key: String) -> Double? {
        Self.double(value[key] ?? nil)
    }

    /// A number inside an object member (`totals.distance_m`).
    func number(_ key: String, in parent: String) -> Double? {
        Self.double(((value[parent] ?? nil) as? [String: (any Sendable)?])?[key] ?? nil)
    }

    private static func double(_ value: (any Sendable)?) -> Double? {
        switch value {
        case let number as Double: number
        case let number as Int: Double(number)
        case let number as Int64: Double(number)
        default: nil
        }
    }

    func flag(_ key: String) -> Bool? {
        (value[key] ?? nil) as? Bool
    }

    /// A list of words (`labels`, `associations`).
    func words(_ key: String) -> [String] {
        ((value[key] ?? nil) as? [(any Sendable)?])?.compactMap { $0 as? String } ?? []
    }
}

/// The families the Events lanes and their type picker group codes by (docs/metrics.md#events).
enum EventFamily: Int, CaseIterable, Comparable {
    case heart, mind, cycle, symptoms, alerts, other

    static let cycleCodes: Set<String> = [
        "menstrual_flow", "bleeding_after_pregnancy", "bleeding_during_pregnancy", "bleeding_after_menopause",
        "intermenstrual_bleeding", "sexual_activity", "pregnancy", "lactation", "cervical_mucus", "ovulation_test",
        "pregnancy_test", "progesterone_test", "contraceptive", "menopausal_state", "irregular_cycles_alert",
        "infrequent_cycles_alert", "prolonged_periods_alert", "persistent_intermenstrual_bleeding_alert",
    ]
    static let heartCodes: Set<String> = [
        "ecg_recording", "irregular_rhythm_alert", "irregular_rhythm", "afib_ecg_result", "afib_ppg_result",
        "high_heart_rate_alert", "low_heart_rate_alert", "low_cardio_fitness_alert", "hypertension_alert",
    ]

    init(code: String) {
        self = if Self.heartCodes.contains(code) { .heart }
            else if code == "state_of_mind" || code == "mindful_session" { .mind }
            else if Self.cycleCodes.contains(code) { .cycle }
            else if code.hasPrefix("symptom_") { .symptoms }
            else if code.hasSuffix("_alert") { .alerts }
            else { .other }
    }

    var title: String {
        switch self {
        case .heart: "Heart rhythm and rate"
        case .mind: "Mind"
        case .cycle: "Cycle tracking"
        case .symptoms: "Symptoms"
        case .alerts: "Other alerts"
        case .other: "Other events"
        }
    }

    static func < (lhs: EventFamily, rhs: EventFamily) -> Bool { lhs.rawValue < rhs.rawValue }
}

/// The Watch view an event type has, besides its lane.
func watchRoute(forEvent code: String) -> Route? {
    switch code {
    case "ecg_recording": .exploreView(.ecg)
    case "state_of_mind", "mindful_session": .exploreView(.stateOfMind)
    case "workout_route": .exploreView(.workouts)
    default: nil
    }
}

enum WatchText {
    /// HealthKit's `HKElectrocardiogram.Classification`, word for word: what the ECG app recorded.
    static func classification(_ level: String?) -> String {
        switch level {
        case "sinus_rhythm": "Sinus rhythm"
        case "atrial_fibrillation": "Atrial fibrillation"
        case "inconclusive_low_heart_rate": "Inconclusive: low heart rate"
        case "inconclusive_high_heart_rate": "Inconclusive: high heart rate"
        case "inconclusive_poor_reading": "Inconclusive: poor reading"
        case "inconclusive_other": "Inconclusive: other"
        case "not_set": "Not set"
        case "unrecognized": "Unrecognized"
        case let other?: metricLabel(other)
        case nil: "No classification recorded"
        }
    }

    /// HealthKit's `HKElectrocardiogram.SymptomsStatus`, as recorded.
    static func symptoms(_ status: String?) -> String {
        switch status {
        case "present": "Recorded as present"
        case "none": "None recorded"
        default: "Not set"
        }
    }

    static func lead(_ lead: String?) -> String {
        lead == "apple_watch_similar_to_lead_i" ? "Apple Watch, similar to lead I" : lead.map(metricLabel) ?? "–"
    }

    /// A word list as "Calm, content".
    static func words(_ words: [String]) -> String {
        words.map { metricLabel($0) }.joined(separator: ", ")
    }

    /// "Apple Health · Synthetic Watch" for a record.
    static func source(_ source: Components.Schemas.SourceRef, device: String? = nil) -> String {
        sourceName(source.provider, detail: device ?? source.origin ?? source.deviceType.map(metricLabel))
    }
}

/// A view's tile in the given hue and a one-line description.
struct WatchIntro: View {
    let hue: MetricHue
    let symbol: String
    let text: String

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: symbol)
                .font(.system(size: 16, weight: .semibold))
                .foregroundStyle(hue.color)
                .frame(width: 36, height: 36)
                .background(hue.color.opacity(0.16), in: .rect(cornerRadius: 10))
                .accessibilityHidden(true)
            Text(text).font(.subheadline).foregroundStyle(.secondary)
        }
    }
}

/// Pushes a route onto the current tab's stack from a row that has other buttons.
struct PushButton: View {
    @Environment(AppState.self) private var state
    let title: String
    let systemImage: String
    let route: Route

    var body: some View {
        Button(title, systemImage: systemImage) {
            state.paths[state.tab, default: []].append(route)
        }
        .font(.footnote)
        .buttonStyle(.borderless)
    }
}

/// One local day back or forward, for the views that show a single day.
struct DayStepper: View {
    @Binding var date: LocalDate
    let latest: LocalDate

    var body: some View {
        HStack {
            Button("Earlier day", systemImage: "chevron.left") { date = date.adding(days: -1) }
                .labelStyle(.iconOnly)
                .accessibilityIdentifier("earlierDay")
            Spacer()
            Text(Format.day(date)).font(.headline).accessibilityIdentifier("shownDay")
            Spacer()
            Button("Later day", systemImage: "chevron.right") { date = date.adding(days: 1) }
                .labelStyle(.iconOnly)
                .disabled(date >= latest)
                .accessibilityIdentifier("laterDay")
        }
        .buttonStyle(.borderless)
    }
}
