import CoreLocation
import CryptoKit
import Foundation
import HealthBridgeCore
import HealthKit

/// Maps HealthKit objects to the payload, keeping UUID, timestamps with offset, source revision,
/// device and metadata.
enum Mapping {
    static func sample(_ s: HKSample, unit: String?) -> Sample {
        let tz = timeZone(of: s)
        var out = Sample(uuid: s.uuid.uuidString, start: rfc3339(s.startDate, in: tz), end: rfc3339(s.endDate, in: tz),
                         sourceRevision: revision(s.sourceRevision), device: s.device.map(device),
                         metadata: s.metadata.map(metadata), wasUserEntered: s.metadata?[HKMetadataKeyWasUserEntered] as? Bool ?? false)
        switch s {
        case let q as HKQuantitySample:
            guard let unit = unit ?? Registry.unit(for: q.quantityType.identifier) else { break }
            out.value = q.quantity.doubleValue(for: HKUnit(from: unit))
            out.unit = unit
        case let c as HKCategorySample:
            out.value = Double(c.value)
        case let c as HKCorrelation:
            out.objects = c.objects.sorted { $0.uuid.uuidString < $1.uuid.uuidString }.map {
                var member = sample($0, unit: nil)
                member.type = $0.sampleType.identifier
                return member
            }
        case let w as HKWorkout:
            out.workout = Workout(activityType: w.workoutActivityType.rawValue, durationS: w.duration,
                                  totals: totals(w.allStatistics), stats: stats(w.allStatistics),
                                  events: events(w.workoutEvents ?? [], in: tz),
                                  activities: nonEmpty(w.workoutActivities.map { activity($0, in: tz) }))
        default:
            if #available(iOS 18.0, macOS 15.0, *), let m = s as? HKStateOfMind {
                out.stateOfMind = StateOfMind(kind: m.kind.rawValue, valence: m.valence,
                                              valenceClassification: m.valenceClassification.rawValue,
                                              labels: m.labels.map(\.rawValue), associations: m.associations.map(\.rawValue))
            }
        }
        return out
    }

    static func timeZone(of s: HKSample) -> TimeZone {
        (s.metadata?[HKMetadataKeyTimeZone] as? String).flatMap(TimeZone.init(identifier:)) ?? .current
    }

    static func revision(_ r: HKSourceRevision) -> SourceRevision {
        let os = r.operatingSystemVersion
        return SourceRevision(bundleId: r.source.bundleIdentifier, name: r.source.name, version: r.version,
                              productType: r.productType, osVersion: "\(os.majorVersion).\(os.minorVersion).\(os.patchVersion)")
    }

    static func device(_ d: HKDevice) -> Device {
        Device(name: d.name, manufacturer: d.manufacturer, model: d.model, hardwareVersion: d.hardwareVersion, softwareVersion: d.softwareVersion)
    }

    static func metadata(_ m: [String: Any]) -> [String: MetadataValue] {
        m.mapValues { v in
            switch v {
            case let n as NSNumber: .number(n.doubleValue)
            case let d as Date: .string(rfc3339(d, in: TimeZone(identifier: "UTC")!))
            default: .string("\(v)")
            }
        }
    }

    // MARK: Workout detail

    /// Sums of cumulative types, in the registry unit.
    static func totals(_ all: [HKQuantityType: HKStatistics]) -> [String: Double]? {
        nonEmpty(all.reduce(into: [String: Double]()) { acc, entry in
            if let unit = Registry.unit(for: entry.key.identifier), let sum = entry.value.sumQuantity() {
                acc[entry.key.identifier] = sum.doubleValue(for: HKUnit(from: unit))
            }
        })
    }

    /// Average, minimum and maximum of discrete types (e.g. heart rate), in the registry unit.
    static func stats(_ all: [HKQuantityType: HKStatistics]) -> [String: Stats]? {
        nonEmpty(all.reduce(into: [String: Stats]()) { acc, entry in
            guard let unit = Registry.unit(for: entry.key.identifier).map(HKUnit.init(from:)) else { return }
            let s = entry.value
            let out = Stats(avg: s.averageQuantity()?.doubleValue(for: unit), min: s.minimumQuantity()?.doubleValue(for: unit),
                            max: s.maximumQuantity()?.doubleValue(for: unit))
            if out != Stats() { acc[entry.key.identifier] = out }
        })
    }

    static func events(_ events: [HKWorkoutEvent], in tz: TimeZone) -> [WorkoutEvent]? {
        nonEmpty(events.map {
            WorkoutEvent(type: $0.type.rawValue, start: rfc3339($0.dateInterval.start, in: tz), end: rfc3339($0.dateInterval.end, in: tz),
                         metadata: $0.metadata.map(metadata))
        })
    }

    static func activity(_ a: HKWorkoutActivity, in tz: TimeZone) -> WorkoutActivity {
        let config = a.workoutConfiguration
        return WorkoutActivity(uuid: a.uuid.uuidString, activityType: config.activityType.rawValue,
                               locationType: config.locationType.rawValue,
                               swimmingLocationType: config.swimmingLocationType == .unknown ? nil : config.swimmingLocationType.rawValue,
                               lapLengthM: config.lapLength?.doubleValue(for: .meter()), start: rfc3339(a.startDate, in: tz),
                               end: a.endDate.map { rfc3339($0, in: tz) }, durationS: a.duration, totals: totals(a.allStatistics),
                               stats: stats(a.allStatistics), events: events(a.workoutEvents, in: tz), metadata: a.metadata.map(metadata))
    }

    static func nonEmpty<C: Collection>(_ c: C) -> C? { c.isEmpty ? nil : c }

    // MARK: Series detail

    /// `offsets` when they differ from `i / frequency` by more than 0.1 ms anywhere, else nil.
    static func unevenOffsets(_ offsets: [Double], frequency: Double?) -> [Double]? {
        guard let frequency, frequency > 0 else { return offsets.isEmpty ? nil : offsets }
        let even = offsets.enumerated().allSatisfy { abs($0.element - Double($0.offset) / frequency) < 1e-4 }
        return even ? nil : offsets
    }

    /// Route locations as parallel arrays, offsets from the route sample's start.
    static func route(_ locations: [CLLocation], start: Date) -> Route {
        Route(count: locations.count, offsetsS: locations.map { $0.timestamp.timeIntervalSince(start) },
              latitude: locations.map(\.coordinate.latitude), longitude: locations.map(\.coordinate.longitude),
              altitudeM: locations.map(\.altitude), ellipsoidalAltitudeM: locations.map(\.ellipsoidalAltitude),
              horizontalAccuracyM: locations.map(\.horizontalAccuracy), verticalAccuracyM: locations.map(\.verticalAccuracy),
              speedMps: locations.map(\.speed), speedAccuracyMps: locations.map(\.speedAccuracy),
              courseDeg: locations.map(\.course), courseAccuracyDeg: locations.map(\.courseAccuracy))
    }

    // MARK: Activity summaries (ADR-0024)

    /// UUIDv5 namespace of activity-summary keys.
    static let summaryNamespace = UUID(uuidString: "beff15b8-8908-4179-976c-1ebb715deb23")!
    /// HealthKit reports no source for a summary, so the payload carries this fixed marker.
    static let summaryRevision = SourceRevision(bundleId: "vitamux.activity-summary", name: "Activity summary")

    /// One summary, or nil when HealthKit gives no full date. Quantities in an incompatible unit are left out.
    static func activitySummary(_ s: HKActivitySummary, day: DateComponents) -> ActivitySummary? {
        guard let year = day.year, let month = day.month, let dayOfMonth = day.day else { return nil }
        func value(_ q: HKQuantity?, _ unit: HKUnit) -> Double? { q.flatMap { $0.is(compatibleWith: unit) ? $0.doubleValue(for: unit) : nil } }
        let kcal = HKUnit.kilocalorie(), s0 = HKUnit.second(), count = HKUnit.count()
        var out = ActivitySummary(date: String(format: "%04d-%02d-%02d", year, month, dayOfMonth),
                                  moveMode: s.activityMoveMode.rawValue,
                                  activeEnergyKcal: value(s.activeEnergyBurned, kcal), activeEnergyGoalKcal: value(s.activeEnergyBurnedGoal, kcal),
                                  moveTimeS: value(s.appleMoveTime, s0), moveTimeGoalS: value(s.appleMoveTimeGoal, s0),
                                  exerciseTimeS: value(s.appleExerciseTime, s0), exerciseTimeGoalS: value(s.exerciseTimeGoal, s0),
                                  standHours: value(s.appleStandHours, count), standHoursGoal: value(s.standHoursGoal, count))
        if #available(iOS 18.0, macOS 15.0, *) { out.paused = s.isPaused }
        return out
    }

    /// A summary as a sample: UUIDv5 of the day, the local day as start and end, the marker source.
    static func sample(_ summary: ActivitySummary, calendar: Calendar) -> Sample? {
        let parts = summary.date.split(separator: "-").compactMap { Int($0) }
        guard parts.count == 3, let start = calendar.date(from: DateComponents(year: parts[0], month: parts[1], day: parts[2])),
              let next = calendar.date(byAdding: .day, value: 1, to: start) else { return nil }
        return Sample(uuid: uuidV5(namespace: summaryNamespace, name: "activity_summary:" + summary.date).uuidString,
                      start: rfc3339(start, in: calendar.timeZone), end: rfc3339(next, in: calendar.timeZone),
                      sourceRevision: summaryRevision, activitySummary: summary)
    }

    /// SHA-256 (hex) of the summaries as compact sorted-key JSON: the page's anchor hash, so a page's
    /// idempotency key changes exactly when a day changes.
    static func summaryHash(_ summaries: [ActivitySummary]) -> String {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        let data = (try? encoder.encode(summaries)) ?? Data()
        return SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    /// RFC 4122 name-based UUID, version 5 (SHA-1).
    static func uuidV5(namespace: UUID, name: String) -> UUID {
        var bytes = withUnsafeBytes(of: namespace.uuid) { Array($0) }
        bytes.append(contentsOf: Array(name.utf8))
        var hash = Array(Insecure.SHA1.hash(data: bytes).prefix(16))
        hash[6] = (hash[6] & 0x0F) | 0x50
        hash[8] = (hash[8] & 0x3F) | 0x80
        return UUID(uuid: (hash[0], hash[1], hash[2], hash[3], hash[4], hash[5], hash[6], hash[7],
                           hash[8], hash[9], hash[10], hash[11], hash[12], hash[13], hash[14], hash[15]))
    }
}
