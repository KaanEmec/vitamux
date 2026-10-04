import Foundation
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

/// The HealthKit calls the bridge makes, behind a protocol so tests can fake them.
public protocol HealthStore: Sendable {
    func requestReadAuthorization(_ types: Set<HKObjectType>) async throws
    func earliestPermittedSampleDate() -> Date
    func anchoredPage(of type: HKSampleType, from start: Date, anchor: Data?, limit: Int) async throws -> AnchoredPage
    /// Calls `onUpdate` on each change; `onUpdate` must call its completion handler exactly once.
    func observe(_ type: HKSampleType, onUpdate: @escaping @Sendable (@escaping @Sendable () -> Void) -> Void)
    func enableBackgroundDelivery(for type: HKSampleType) async throws
}

extension HKHealthStore: HealthStore {
    public func requestReadAuthorization(_ types: Set<HKObjectType>) async throws {
        try await requestAuthorization(toShare: [], read: types)
    }

    public func anchoredPage(of type: HKSampleType, from start: Date, anchor: Data?, limit: Int) async throws -> AnchoredPage {
        let before = try anchor.flatMap { try NSKeyedUnarchiver.unarchivedObject(ofClass: HKQueryAnchor.self, from: $0) }
        let predicate = HKSamplePredicate.sample(type: type, predicate: HKQuery.predicateForSamples(withStart: start, end: nil))
        let result = try await HKAnchoredObjectQueryDescriptor(predicates: [predicate], anchor: before, limit: limit).result(for: self)
        return AnchoredPage(samples: result.addedSamples, deleted: result.deletedObjects.map(\.uuid),
                            anchor: try NSKeyedArchiver.archivedData(withRootObject: result.newAnchor, requiringSecureCoding: true))
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
}
