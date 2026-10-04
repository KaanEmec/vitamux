import Foundation
import HealthBridgeCore
import HealthKit
import XCTest
@testable import HealthBridgeHealthKit

/// Golden payloads per type class. `source_revision` depends on the test host and `uuid` is
/// random, so both are stripped and the uuid is checked separately.
final class MappingTests: XCTestCase {
    let zone: [String: Any] = [HKMetadataKeyTimeZone: "Europe/Amsterdam"]
    let startAms = "2026-07-01T08:00:00.000+02:00", endAms = "2026-07-01T08:01:00.000+02:00"

    func testQuantity() throws {
        let device = HKDevice(name: "Synthetic Watch", manufacturer: "Example", model: "Watch1", hardwareVersion: "hw1",
                              firmwareVersion: "fw1", softwareVersion: "sw1", localIdentifier: nil, udiDeviceIdentifier: nil)
        let s = HKQuantitySample(type: HKQuantityType(.heartRate), quantity: HKQuantity(unit: HKUnit(from: "count/min"), doubleValue: 72),
                                 start: t0, end: t0 + 60, device: device,
                                 metadata: zone.merging([HKMetadataKeyWasUserEntered: true]) { $1 })
        let out = Mapping.sample(s, unit: heartRate.unit)
        XCTAssertEqual(out.uuid, s.uuid.uuidString)
        XCTAssertEqual(try golden(out), """
            {"device":{"hardware_version":"hw1","manufacturer":"Example","model":"Watch1","name":"Synthetic Watch","software_version":"sw1"},\
            "end":"\(endAms)","metadata":{"HKTimeZone":"Europe/Amsterdam","HKWasUserEntered":1},"start":"\(startAms)",\
            "unit":"count/min","value":72,"was_user_entered":true}
            """)
    }

    func testCategory() throws {
        let s = HKCategorySample(type: HKCategoryType(.sleepAnalysis), value: HKCategoryValueSleepAnalysis.asleepREM.rawValue,
                                 start: t0, end: t0 + 60, metadata: zone)
        XCTAssertEqual(try golden(Mapping.sample(s, unit: nil)), """
            {"end":"\(endAms)","metadata":{"HKTimeZone":"Europe/Amsterdam"},"start":"\(startAms)","value":\
            \(HKCategoryValueSleepAnalysis.asleepREM.rawValue),"was_user_entered":false}
            """)
    }

    func testBloodPressureCorrelation() throws {
        let mmHg = HKUnit.millimeterOfMercury()
        let members: [HKSample] = [(HKQuantityType(.bloodPressureSystolic), 120.0), (HKQuantityType(.bloodPressureDiastolic), 80.0)].map {
            HKQuantitySample(type: $0.0, quantity: HKQuantity(unit: mmHg, doubleValue: $0.1), start: t0, end: t0 + 60, metadata: zone)
        }
        let c = HKCorrelation(type: HKCorrelationType(.bloodPressure), start: t0, end: t0 + 60, objects: Set(members), metadata: zone)
        let out = Mapping.sample(c, unit: nil)

        let sorted = members.sorted { $0.uuid.uuidString < $1.uuid.uuidString }
        XCTAssertEqual(out.objects?.map(\.uuid), sorted.map(\.uuid.uuidString))
        let member = { (s: HKSample) in
            let value = (s as! HKQuantitySample).quantity.doubleValue(for: mmHg)
            return #"{"end":"\#(self.endAms)","metadata":{"HKTimeZone":"Europe/Amsterdam"},"start":"\#(self.startAms)","type":"\#(s.sampleType.identifier)","unit":"mmHg","value":\#(Int(value)),"was_user_entered":false}"#
        }
        XCTAssertEqual(try golden(out), """
            {"end":"\(endAms)","metadata":{"HKTimeZone":"Europe/Amsterdam"},"objects":[\(sorted.map(member).joined(separator: ","))],\
            "start":"\(startAms)","was_user_entered":false}
            """)
    }

    @available(*, deprecated, message: "HKWorkout(activityType:start:end:) is the simplest synthetic workout")
    func testWorkout() throws {
        let w = HKWorkout(activityType: .running, start: t0, end: t0 + 1800)
        // No time zone metadata: Mapping falls back to the current zone.
        XCTAssertEqual(try golden(Mapping.sample(w, unit: nil)), """
            {"end":"\(rfc3339(t0 + 1800))","start":"\(rfc3339(t0))","was_user_entered":false,"workout":{"activity_type":37,"duration_s":1800}}
            """)
    }

    /// Sorted-key JSON of `sample` without `uuid` and `source_revision`, recursively.
    func golden(_ sample: Sample) throws -> String {
        func strip(_ v: Any) -> Any {
            if var d = v as? [String: Any] {
                d["uuid"] = nil
                d["source_revision"] = nil
                return d.mapValues(strip)
            }
            if let a = v as? [Any] { return a.map(strip) }
            return v
        }
        let raw = try JSONSerialization.jsonObject(with: JSONEncoder().encode(sample))
        let data = try JSONSerialization.data(withJSONObject: strip(raw), options: [.sortedKeys, .withoutEscapingSlashes])
        return String(decoding: data, as: UTF8.self)
    }
}
