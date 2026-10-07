import Foundation
import Observation
import VitamuxKit

typealias BloodPressureReading = Components.Schemas.BloodPressureReading

/// Blood pressure (the panel's `/explore/blood-pressure`): every reading in the range
/// (`GET /blood-pressure`, every page), grouped into sessions of readings within 30 minutes whose
/// mean is plotted as a dumbbell, the pulse per session, the range's mean and the readings.
/// Readings are shown as measured: no categories, no thresholds, no colouring by value.
@Observable
final class BloodPressureModel {
    enum Part: String, CaseIterable, Identifiable {
        case all = "All", morning = "Morning", evening = "Evening"
        var id: Self { self }
    }

    /// Readings close together, plotted as their mean.
    struct Session: Identifiable {
        var readings: [BloodPressureReading]
        var id: String { readings[0].id }
        var at: Date { readings[0].measuredAt }
        var systolic: Double? { Self.mean(readings.compactMap(\.systolic)) }
        var diastolic: Double? { Self.mean(readings.compactMap(\.diastolic)) }
        var pulse: Double? { Self.mean(readings.compactMap(\.pulse)) }

        static func mean(_ values: [Double]) -> Double? {
            values.isEmpty ? nil : values.reduce(0, +) / Double(values.count)
        }
    }

    var span = ViewRange(.quarter)
    var part = Part.all
    private(set) var readings: Loadable<[BloodPressureReading]> = .loading

    static let sessionGap: TimeInterval = 30 * 60

    func load(_ client: Client?) async {
        guard let client else { return }
        let start = span.start()?.description, end = span.end.description
        readings = await Loadable {
            try await readAll { cursor in
                let page = try await client.listBloodPressure(query: .init(startDate: start, endDate: end, limit: 500, cursor: cursor)).ok.body.json
                return (page.value2.readings, nextCursor(page.value1))
            }
        }
    }

    /// The local hour of a reading, from its own UTC offset: morning is before 12:00, evening from 17:00.
    private func inPart(_ reading: BloodPressureReading) -> Bool {
        guard part != .all else { return true }
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(offsetMinutes: reading.tzOffsetMin)
        let hour = calendar.component(.hour, from: reading.measuredAt)
        return part == .morning ? hour < 12 : hour >= 17
    }

    /// The readings of the chosen part of the day, oldest first.
    var chosen: [BloodPressureReading] {
        (readings.value ?? []).filter(inPart).sorted { $0.measuredAt < $1.measuredAt }
    }

    var sessions: [Session] {
        var groups: [[BloodPressureReading]] = []
        for reading in chosen where reading.systolic != nil && reading.diastolic != nil {
            if let last = groups.last?.last, reading.measuredAt.timeIntervalSince(last.measuredAt) <= Self.sessionGap {
                groups[groups.count - 1].append(reading)
            } else {
                groups.append([reading])
            }
        }
        return groups.map(Session.init)
    }

    /// "121/79", or nil without a pair.
    var meanText: String? {
        let pairs = chosen.filter { $0.systolic != nil && $0.diastolic != nil }
        guard let sys = Session.mean(pairs.compactMap(\.systolic)), let dia = Session.mean(pairs.compactMap(\.diastolic)) else { return nil }
        return "\(Int(sys.rounded()))/\(Int(dia.rounded()))"
    }

    /// One dumbbell per session: systolic high, diastolic low. Pulse, context and source are in the
    /// pulse chart and the readings list (the kit's `ChartTip.Row` has no public initializer).
    func dumbbells(_ sessions: [Session]) -> [RangeDumbbell.Reading] {
        sessions.map { RangeDumbbell.Reading(x: $0.at, low: $0.diastolic, high: $0.systolic) }
    }

    func pulse(_ sessions: [Session]) -> [ChartSeries] {
        let points = sessions.compactMap { s in s.pulse.map { ChartPoint(x: s.at, y: $0) } }
        return points.isEmpty ? [] : [ChartSeries(label: "Pulse", points: points, style: .dots)]
    }

    /// "Position seated, Arm left", as given by the source.
    static func context(_ reading: BloodPressureReading) -> String {
        reading.context.value
            .sorted { $0.key < $1.key }
            .compactMap { key, value in value.map { "\(metricLabel(key)) \($0)" } }
            .joined(separator: ", ")
    }
}
