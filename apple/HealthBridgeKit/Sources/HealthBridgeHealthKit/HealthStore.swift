import CoreLocation
import Foundation
import HealthBridgeCore
import HealthKit

/// One anchored-query page. `anchor` is the archived `HKQueryAnchor`.
public struct AnchoredPage: @unchecked Sendable {
    public var samples: [HKSample]
    public var deleted: [UUID]
    public var anchor: Data?

    public init(samples: [HKSample], deleted: [UUID], anchor: Data?) {
        self.samples = samples
        self.deleted = deleted
        self.anchor = anchor
    }
}

/// An app or device that wrote to Apple Health (`HKSource`). `source` is nil in tests, which cannot
/// create an `HKSource`; such a source cannot be excluded from a query. `HKSource` is immutable,
/// hence `@unchecked Sendable`.
public struct HealthSource: Equatable, @unchecked Sendable {
    public var bundleID: String
    public var name: String
    public let source: HKSource?

    public init(bundleID: String, name: String, source: HKSource? = nil) {
        self.bundleID = bundleID
        self.name = name
        self.source = source
    }

    public init(_ source: HKSource) {
        self.init(bundleID: source.bundleIdentifier, name: source.name, source: source)
    }
}

public enum HealthStoreError: Error, Equatable {
    /// The store cannot read this detail (a store written before registry v2), so the page is not sent.
    case unsupported(String)
    /// HealthKit returned an object of another class or shape than the type promises.
    case unexpectedShape(String)
}

/// The HealthKit calls the bridge makes, behind a protocol so tests can fake them.
public protocol HealthStore: Sendable {
    func requestReadAuthorization(_ types: Set<HKObjectType>) async throws
    func earliestPermittedSampleDate() -> Date
    /// One anchored page of `type` from `start`, leaving out the samples of `excluding` (the apps a
    /// source filter ignores). No default: a wrapping store must forward the exclusion.
    func anchoredPage(of type: HKSampleType, from start: Date, anchor: Data?, limit: Int, excluding: [HealthSource]) async throws -> AnchoredPage
    /// The apps and devices that wrote `type` (`HKSourceQuery`).
    func sources(for type: HKSampleType) async throws -> [HealthSource]
    /// The end of the newest sample of `type` from `source`, nil when there is none.
    func lastSampleDate(of type: HKSampleType, from source: HealthSource) async throws -> Date?
    /// Calls `onUpdate` on each change; `onUpdate` must call its completion handler exactly once.
    func observe(_ type: HKSampleType, onUpdate: @escaping @Sendable (@escaping @Sendable () -> Void) -> Void)
    func enableBackgroundDelivery(for type: HKSampleType) async throws

    // Registry v2 detail reads (ADR-0024). A wrapping store must forward these too.
    /// The fields and voltages of an `HKElectrocardiogram` (`HKElectrocardiogramQuery`).
    func electrocardiogram(_ sample: HKSample) async throws -> ECG
    /// The beats of an `HKHeartbeatSeriesSample` (`HKHeartbeatSeriesQuery`).
    func heartbeats(_ sample: HKSample) async throws -> Beats
    /// The locations of an `HKWorkoutRoute` (`HKWorkoutRouteQuery`).
    func route(_ sample: HKSample) async throws -> Route
    /// The workout a route or effort score belongs to, or nil when HealthKit reports no link.
    func workoutUUID(of sample: HKSample) async throws -> UUID?
    /// The activity summaries of the days `start` through `end` (inclusive), in `calendar`.
    func activitySummaries(from start: Date, through end: Date, in calendar: Calendar) async throws -> [ActivitySummary]
}

/// Stores written before registry v2 compile unchanged. They cannot read the detail, so those types
/// fail visibly instead of uploading incomplete samples; a missing workout link is allowed by the contract.
extension HealthStore {
    public func electrocardiogram(_ sample: HKSample) async throws -> ECG { throw HealthStoreError.unsupported("electrocardiogram") }
    public func heartbeats(_ sample: HKSample) async throws -> Beats { throw HealthStoreError.unsupported("heartbeats") }
    public func route(_ sample: HKSample) async throws -> Route { throw HealthStoreError.unsupported("route") }
    public func workoutUUID(of sample: HKSample) async throws -> UUID? { nil }
    public func activitySummaries(from start: Date, through end: Date, in calendar: Calendar) async throws -> [ActivitySummary] {
        throw HealthStoreError.unsupported("activity summary")
    }
}

extension HKHealthStore: HealthStore {
    public func requestReadAuthorization(_ types: Set<HKObjectType>) async throws {
        try await requestAuthorization(toShare: [], read: types)
    }

    public func anchoredPage(of type: HKSampleType, from start: Date, anchor: Data?, limit: Int,
                             excluding: [HealthSource]) async throws -> AnchoredPage {
        let before = try anchor.flatMap { try NSKeyedUnarchiver.unarchivedObject(ofClass: HKQueryAnchor.self, from: $0) }
        let predicate = HKSamplePredicate.sample(type: type, predicate: Self.anchoredPredicate(from: start, excluding: excluding))
        let result = try await HKAnchoredObjectQueryDescriptor(predicates: [predicate], anchor: before, limit: limit).result(for: self)
        return AnchoredPage(samples: result.addedSamples, deleted: result.deletedObjects.map(\.uuid),
                            anchor: try NSKeyedArchiver.archivedData(withRootObject: result.newAnchor, requiringSecureCoding: true))
    }

    /// Samples from `start` on, minus those of `excluding`. NOT(ignored) rather than "from the taken
    /// sources", so an app that starts writing between discovery and the query is taken (unknown apps
    /// are taken by default) and none of its samples are skipped past the anchor.
    static func anchoredPredicate(from start: Date, excluding: [HealthSource]) -> NSPredicate {
        let window = HKQuery.predicateForSamples(withStart: start, end: nil)
        let excluded = Set(excluding.compactMap(\.source))
        guard !excluded.isEmpty else { return window }
        let ignored = NSCompoundPredicate(notPredicateWithSubpredicate: HKQuery.predicateForObjects(from: excluded))
        return NSCompoundPredicate(andPredicateWithSubpredicates: [window, ignored])
    }

    public func sources(for type: HKSampleType) async throws -> [HealthSource] {
        try await HKSourceQueryDescriptor(predicate: .sample(type: type)).result(for: self).map(HealthSource.init)
    }

    public func lastSampleDate(of type: HKSampleType, from source: HealthSource) async throws -> Date? {
        guard let hkSource = source.source else { return nil }
        let descriptor = HKSampleQueryDescriptor(predicates: [.sample(type: type, predicate: HKQuery.predicateForObjects(from: hkSource))],
                                                 sortDescriptors: [SortDescriptor(\.endDate, order: .reverse)], limit: 1)
        return try await descriptor.result(for: self).first?.endDate
    }

    public func observe(_ type: HKSampleType, onUpdate: @escaping @Sendable (@escaping @Sendable () -> Void) -> Void) {
        execute(HKObserverQuery(sampleType: type, predicate: nil) { _, completion, error in
            nonisolated(unsafe) let completion = completion // HealthKit allows calling it from any thread
            guard error == nil else { return completion() }
            onUpdate { completion() }
        })
    }

    public func enableBackgroundDelivery(for type: HKSampleType) async throws {
        #if os(iOS) || os(watchOS)
        try await enableBackgroundDelivery(for: type, frequency: .immediate)
        #endif
    }

    public func electrocardiogram(_ sample: HKSample) async throws -> ECG {
        guard let ecg = sample as? HKElectrocardiogram else { throw HealthStoreError.unexpectedShape(sample.sampleType.identifier) }
        let unit = HKUnit(from: "mcV")
        var voltages: [Double] = [], offsets: [Double] = []
        voltages.reserveCapacity(ecg.numberOfVoltageMeasurements)
        offsets.reserveCapacity(ecg.numberOfVoltageMeasurements)
        for try await measurement in HKElectrocardiogramQueryDescriptor(ecg).results(for: self) {
            guard let value = measurement.quantity(for: .appleWatchSimilarToLeadI) else {
                throw HealthStoreError.unexpectedShape("electrocardiogram measurement without lead I")
            }
            voltages.append(value.doubleValue(for: unit))
            offsets.append(measurement.timeSinceSampleStart)
        }
        let frequency = ecg.samplingFrequency?.doubleValue(for: .hertz())
        return ECG(classification: ecg.classification.rawValue, symptomsStatus: ecg.symptomsStatus.rawValue,
                   averageHeartRate: ecg.averageHeartRate?.doubleValue(for: HKUnit(from: "count/min")),
                   samplingFrequencyHz: frequency, lead: HKElectrocardiogram.Lead.appleWatchSimilarToLeadI.rawValue,
                   voltageCount: ecg.numberOfVoltageMeasurements, voltages: voltages,
                   offsetsS: Mapping.unevenOffsets(offsets, frequency: frequency))
    }

    public func heartbeats(_ sample: HKSample) async throws -> Beats {
        guard let series = sample as? HKHeartbeatSeriesSample else { throw HealthStoreError.unexpectedShape(sample.sampleType.identifier) }
        var offsets: [Double] = [], gaps: [Bool] = []
        for try await beat in HKHeartbeatSeriesQueryDescriptor(series).results(for: self) {
            offsets.append(beat.timeIntervalSinceStart)
            gaps.append(beat.precededByGap)
        }
        return Beats(offsetsS: offsets, precededByGap: gaps)
    }

    public func route(_ sample: HKSample) async throws -> Route {
        guard let route = sample as? HKWorkoutRoute else { throw HealthStoreError.unexpectedShape(sample.sampleType.identifier) }
        var locations: [CLLocation] = []
        for try await location in HKWorkoutRouteQueryDescriptor(route).results(for: self) { locations.append(location) }
        return Mapping.route(locations, start: route.startDate)
    }

    /// Looks at the workouts overlapping the sample (a second either side) and returns the one HealthKit
    /// links it to: a route through `predicateForObjects(from:)`, an effort score through
    /// `predicateForWorkoutEffortSamplesRelated(workout:activity:)`.
    public func workoutUUID(of sample: HKSample) async throws -> UUID? {
        let window = HKQuery.predicateForSamples(withStart: sample.startDate - 1, end: sample.endDate + 1)
        let workouts = try await HKSampleQueryDescriptor(predicates: [.workout(window)], sortDescriptors: []).result(for: self)
        for workout in workouts {
            let linked: NSPredicate
            if sample is HKWorkoutRoute {
                linked = HKQuery.predicateForObjects(from: workout)
            } else if #available(iOS 18.0, macOS 15.0, *) {
                linked = HKQuery.predicateForWorkoutEffortSamplesRelated(workout: workout, activity: nil)
            } else {
                return nil
            }
            let both = NSCompoundPredicate(andPredicateWithSubpredicates: [linked, HKQuery.predicateForObject(with: sample.uuid)])
            let found = try await HKSampleQueryDescriptor(predicates: [.sample(type: sample.sampleType, predicate: both)],
                                                          sortDescriptors: [], limit: 1).result(for: self)
            if !found.isEmpty { return workout.uuid }
        }
        return nil
    }

    public func activitySummaries(from start: Date, through end: Date, in calendar: Calendar) async throws -> [ActivitySummary] {
        func day(_ date: Date) -> DateComponents {
            var components = calendar.dateComponents([.era, .year, .month, .day], from: date)
            components.calendar = calendar
            return components
        }
        let predicate = HKQuery.predicate(forActivitySummariesBetweenStart: day(start), end: day(end))
        let summaries = try await HKActivitySummaryQueryDescriptor(predicate: predicate).result(for: self)
        return summaries.compactMap { Mapping.activitySummary($0, day: $0.dateComponents(for: calendar)) }
    }
}
