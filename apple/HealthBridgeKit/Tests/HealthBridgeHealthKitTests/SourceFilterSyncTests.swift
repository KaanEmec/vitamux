import Foundation
import HealthBridgeCore
import HealthKit
import XCTest
@testable import HealthBridgeHealthKit

/// The source filter applied by `HealthSync` (J22.25); synthetic bundle ids only.
final class SourceFilterSyncTests: XCTestCase {
    let band = HealthSource(bundleID: "com.example.synthetic.band", name: "Synthetic Band")
    let ring = HealthSource(bundleID: "com.example.synthetic.ring", name: "Synthetic Ring")
    let watch = HealthSource(bundleID: "com.apple.health.00000000-0000-4000-8000-0000000000aa", name: "Synthetic Watch")
    let steps = Registry.v1.first { $0.id == HKQuantityTypeIdentifier.stepCount.rawValue }!

    let bandDefault = SourceFilter.Default(originPattern: "com.example.synthetic.band%", provider: "synthetic_band",
                                           providerName: "Synthetic Band Cloud")

    func harness(_ filter: SourceFilter?) -> Harness {
        let h = Harness()
        h.store.sources = [heartRate.id: [watch, band, ring], steps.id: [watch, band]]
        h.anchors.sourceFilter = filter
        return h
    }

    func testWithoutFilterNothingIsExcludedAndSourcesAreNotRead() async throws {
        let h = harness(nil)
        try await h.sync.sync(heartRate)
        XCTAssertEqual(h.store.requestedExclusions, [[]])
        XCTAssertEqual(h.store.sourceReads, [])
    }

    func testExcludedSetReachesStore() async throws {
        let h = harness(SourceFilter(version: 1, origins: [.init(bundleID: ring.bundleID, mode: .ignore)], defaultIgnore: [bandDefault]))
        try await h.sync.sync(heartRate)
        XCTAssertEqual(h.store.requestedExclusions, [[band.bundleID, ring.bundleID]])
        XCTAssertEqual(h.anchors.excluded(for: heartRate.id), [band.bundleID, ring.bundleID])
    }

    func testPerTypeExcludesOnlyOtherTypes() async throws {
        let h = harness(SourceFilter(version: 1, origins: [.init(bundleID: band.bundleID, mode: .perType, types: [heartRate.id])]))
        try await h.sync.sync(heartRate)
        try await h.sync.sync(steps)
        XCTAssertEqual(h.store.requestedExclusions, [[], [band.bundleID]])
    }

    func testUnknownSourceIsTakenByDefault() async throws {
        let h = harness(SourceFilter(version: 1, defaultIgnore: [bandDefault]))
        let newcomer = HealthSource(bundleID: "org.example.synthetic.newcomer", name: "Synthetic Newcomer")
        h.store.sources[heartRate.id] = [watch, newcomer]
        try await h.sync.sync(heartRate)
        XCTAssertEqual(h.store.requestedExclusions, [[]])
    }

    func testIgnoreToTakeResetsThatTypeOnly() async throws {
        // Both types last excluded the band; now it is taken for heart rate only.
        let h = harness(SourceFilter(version: 2, origins: [.init(bundleID: band.bundleID, mode: .perType, types: [heartRate.id])],
                                     defaultIgnore: [bandDefault]))
        for type in [heartRate, steps] {
            h.anchors.set(a0, for: type.id)
            h.anchors.setExcluded([band.bundleID], for: type.id)
        }
        try await h.sync.sync(heartRate)
        try await h.sync.sync(steps)
        XCTAssertEqual(h.store.requestedAnchors, [nil, a0])
        XCTAssertEqual(h.store.requestedExclusions, [[], [band.bundleID]])
        XCTAssertEqual(h.anchors.excluded(for: heartRate.id), [])
        XCTAssertEqual(h.anchors.excluded(for: steps.id), [band.bundleID])
    }

    func testTakeToIgnoreDoesNotReset() async throws {
        let h = harness(SourceFilter(version: 2, origins: [.init(bundleID: ring.bundleID, mode: .ignore)]))
        h.anchors.set(a0, for: heartRate.id)
        try await h.sync.sync(heartRate)
        XCTAssertEqual(h.store.requestedAnchors, [a0])
        XCTAssertEqual(h.store.requestedExclusions, [[ring.bundleID]])
    }

    func testAppNoLongerPresentDoesNotReset() async throws {
        let h = harness(SourceFilter(version: 2))
        h.store.sources[heartRate.id] = [watch]
        h.anchors.set(a0, for: heartRate.id)
        h.anchors.setExcluded([band.bundleID], for: heartRate.id)
        try await h.sync.sync(heartRate)
        XCTAssertEqual(h.store.requestedAnchors, [a0])
        XCTAssertEqual(h.anchors.excluded(for: heartRate.id), [])
    }

    func testRemovedFilterTakesBackAndResets() async throws {
        let h = harness(nil)
        h.anchors.set(a0, for: heartRate.id)
        h.anchors.setExcluded([band.bundleID], for: heartRate.id)
        try await h.sync.sync(heartRate)
        XCTAssertEqual(h.store.requestedAnchors, [nil])
        XCTAssertEqual(h.store.requestedExclusions, [[]])
    }

    func testSetSourceFilterPersists() async {
        let h = harness(nil)
        let filter = SourceFilter(version: 7, defaultIgnore: [bandDefault])
        await h.sync.setSourceFilter(filter)
        XCTAssertEqual(h.anchors.sourceFilter, filter)
        await h.sync.setSourceFilter(nil)
        XCTAssertNil(h.anchors.sourceFilter)
    }

    func testDiscoverMergesWatchExtensionIntoParent() async throws {
        let h = harness(nil)
        let ext = HealthSource(bundleID: band.bundleID + ".watchkitapp", name: "Synthetic Band Watch")
        let lone = HealthSource(bundleID: "org.example.synthetic.lone.watchkitextension", name: "Synthetic Lone")
        h.store.sources = [heartRate.id: [watch, band, ext], steps.id: [ext, lone]]
        h.store.lastSamples = [
            band.bundleID: [heartRate.id: t0],
            ext.bundleID: [heartRate.id: t0 + 60, steps.id: t0 - 60],
            watch.bundleID: [heartRate.id: t0 - 3600],
        ]
        let found = try await h.sync.discoverSources([heartRate, steps, registryType(Registry.activitySummaryID)])
        XCTAssertEqual(found, [
            DiscoveredSource(bundleID: watch.bundleID, name: "Synthetic Watch", types: [.init(type: heartRate.id, lastSampleAt: t0 - 3600)]),
            DiscoveredSource(bundleID: band.bundleID, name: "Synthetic Band",
                             types: [.init(type: heartRate.id, lastSampleAt: t0 + 60), .init(type: steps.id, lastSampleAt: t0 - 60)]),
            DiscoveredSource(bundleID: lone.bundleID, name: "Synthetic Lone", types: [.init(type: steps.id)]),
        ].sorted { $0.bundleID < $1.bundleID })
    }

    func testPredicateExcludesIgnoredSources() throws {
        let window = HKQuery.predicateForSamples(withStart: t0, end: nil)
        XCTAssertEqual(HKHealthStore.anchoredPredicate(from: t0, excluding: []), window)
        // A source without an HKSource (tests only) cannot be excluded.
        XCTAssertEqual(HKHealthStore.anchoredPredicate(from: t0, excluding: [band]), window)

        let hkSource = syntheticHKSource()
        let ignored = HealthSource(bundleID: band.bundleID, name: band.name, source: hkSource)
        let predicate = try XCTUnwrap(HKHealthStore.anchoredPredicate(from: t0, excluding: [ignored]) as? NSCompoundPredicate)
        XCTAssertEqual(predicate.compoundPredicateType, .and)
        XCTAssertEqual(predicate.subpredicates as? [NSPredicate],
                       [window, NSCompoundPredicate(notPredicateWithSubpredicate: HKQuery.predicateForObjects(from: [hkSource]))])
    }
}

/// An `HKSource` for predicates (HealthKit has no public initializer, and `HKSource.default()` needs
/// a bundle): decoded from an empty archive, so its name and bundle id are nil.
func syntheticHKSource() -> HKSource {
    let archiver = NSKeyedArchiver(requiringSecureCoding: false)
    archiver.setClassName("HKSource", for: EmptyArchive.self)
    archiver.encode(EmptyArchive(), forKey: NSKeyedArchiveRootObjectKey)
    archiver.finishEncoding()
    let unarchiver = try! NSKeyedUnarchiver(forReadingFrom: archiver.encodedData)
    unarchiver.requiresSecureCoding = false
    return unarchiver.decodeObject(forKey: NSKeyedArchiveRootObjectKey) as! HKSource
}

@objc(VitamuxTestsEmptyArchive)
private final class EmptyArchive: NSObject, NSSecureCoding {
    static var supportsSecureCoding: Bool { true }
    override init() {}
    required init?(coder: NSCoder) {}
    func encode(with coder: NSCoder) {}
}
