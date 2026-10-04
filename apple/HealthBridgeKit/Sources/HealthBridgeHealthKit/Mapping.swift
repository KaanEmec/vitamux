import Foundation
import HealthBridgeCore
import HealthKit

/// Maps HealthKit objects to the payload, keeping UUID, timestamps with offset, source revision,
/// device and metadata.
enum Mapping {
    static func sample(_ s: HKSample, unit: String?) -> Sample {
        let tz = (s.metadata?[HKMetadataKeyTimeZone] as? String).flatMap(TimeZone.init(identifier:)) ?? .current
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
            let totals = w.allStatistics.reduce(into: [String: Double]()) { acc, entry in
                if let unit = Registry.unit(for: entry.key.identifier), let sum = entry.value.sumQuantity() {
                    acc[entry.key.identifier] = sum.doubleValue(for: HKUnit(from: unit))
                }
            }
            out.workout = Workout(activityType: w.workoutActivityType.rawValue, durationS: w.duration, totals: totals.isEmpty ? nil : totals)
        default:
            break
        }
        return out
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
}
