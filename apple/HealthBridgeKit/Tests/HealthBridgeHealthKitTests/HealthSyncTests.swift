import Foundation
import HealthBridgeCore
import HealthKit
import XCTest
@testable import HealthBridgeHealthKit

final class HealthSyncTests: XCTestCase {
    func testPagesUntilShortPageAndStoresFinalAnchor() async throws {
        let first = hrSamples(HealthSync.pageLimit)
        let h = Harness(pages: [
            nil: AnchoredPage(samples: first, deleted: [], anchor: a1),
            a1: AnchoredPage(samples: hrSamples(3), deleted: [], anchor: a2),
        ])
        try await h.sync.sync(heartRate)

        XCTAssertEqual(h.store.requestedAnchors, [nil, a1])
        XCTAssertEqual(h.server.bodies.map(\.samples.count), [HealthSync.pageLimit, 3])
        XCTAssertEqual(h.server.bodies.map(\.anchor.beforeHash), ["none", AnchorStore.hash(a1)])
        XCTAssertEqual(h.server.bodies[1].anchor.afterHash, AnchorStore.hash(a2))
        XCTAssertEqual(h.anchors.anchor(for: heartRate.id), a2)
    }

    func testDeletionOnlyPage() async throws {
        let gone = [UUID(), UUID()]
        let h = Harness(pages: [nil: AnchoredPage(samples: [], deleted: gone, anchor: a1)])
        try await h.sync.sync(heartRate)

        XCTAssertEqual(h.server.bodies.count, 1)
        XCTAssertEqual(h.server.bodies[0].samples, [])
        XCTAssertEqual(h.server.bodies[0].deleted, gone.map { Deleted(uuid: $0.uuidString) })
        XCTAssertEqual(h.anchors.anchor(for: heartRate.id), a1)
    }

    func testNewTypePullsFromNilAndResetStartsOver() async throws {
        let h = Harness(pages: [nil: AnchoredPage(samples: hrSamples(1), deleted: [], anchor: a1)])
        try await h.sync.sync(heartRate)
        XCTAssertEqual(h.server.bodies.first?.anchor.beforeHash, "none")

        try await h.sync.sync(heartRate)
        await h.sync.resetAnchor(heartRate)
        XCTAssertNil(h.anchors.anchor(for: heartRate.id))
        try await h.sync.sync(heartRate)

        XCTAssertEqual(h.store.requestedAnchors, [nil, a1, nil])
        XCTAssertEqual(h.server.bodies.map(\.anchor.beforeHash), ["none", "none"]) // the a1 sync was empty
    }

    func testFailedUploadKeepsAnchor() async throws {
        for status in [500, 422] {
            let h = Harness(pages: [a0: AnchoredPage(samples: hrSamples(1), deleted: [], anchor: a1)], status: status)
            h.anchors.set(a0, for: heartRate.id)
            do {
                try await h.sync.sync(heartRate)
                XCTFail("status \(status) did not throw")
            } catch {
                XCTAssertEqual(error as? BridgeError, .rejected(status: status))
            }
            XCTAssertEqual(h.anchors.anchor(for: heartRate.id), a0, "status \(status)")
            XCTAssertEqual(h.store.requestedAnchors, [a0], "status \(status)")
        }
    }

    func testStartIsLaterOfEarliestPermittedAndBackfill() async throws {
        let early = t0.addingTimeInterval(-86_400), late = t0
        let backfillWins = Harness(earliest: early, backfillStart: late)
        try await backfillWins.sync.sync(heartRate)
        XCTAssertEqual(backfillWins.store.requestedStarts, [late])

        let healthKitWins = Harness(earliest: late, backfillStart: early)
        try await healthKitWins.sync.sync(heartRate)
        XCTAssertEqual(healthKitWins.store.requestedStarts, [late])
    }

    func testObserverSyncsAndCompletesOnce() async throws {
        let sleep = Registry.v1.first { $0.id == HKCategoryTypeIdentifier.sleepAnalysis.rawValue }!
        for status in [202, 422] {
            let h = Harness(pages: [nil: AnchoredPage(samples: hrSamples(1), deleted: [], anchor: a1)], status: status)
            await h.sync.observe([heartRate, sleep])
            XCTAssertEqual(h.store.observers.map { $0.0 }, [heartRate.sampleType!, sleep.sampleType!])
            XCTAssertEqual(h.store.backgroundTypes, [heartRate.sampleType!, sleep.sampleType!])

            let done = expectation(description: "completion \(status)")
            done.assertForOverFulfill = true
            h.store.observers[0].1 { done.fulfill() }
            await fulfillment(of: [done], timeout: 5)

            XCTAssertEqual(h.store.requestedAnchors, [nil], "status \(status)")
            XCTAssertEqual(h.server.requests, 1, "status \(status)")
            XCTAssertEqual(h.anchors.anchor(for: heartRate.id), status == 202 ? a1 : nil, "status \(status)")
        }
    }

    func testHeartAuthorizationUsesBloodPressureMembers() async throws {
        let h = Harness()
        try await h.sync.requestAuthorization(for: [.heart])
        let types = try XCTUnwrap(h.store.authorized.first)
        XCTAssertTrue(types.contains(HKQuantityType(.bloodPressureSystolic)))
        XCTAssertTrue(types.contains(HKQuantityType(.bloodPressureDiastolic)))
        XCTAssertTrue(types.contains(HKQuantityType(.heartRate)))
        XCTAssertFalse(types.contains(HKCorrelationType(.bloodPressure)))
    }

    func testRegistryIdsUniqueAndResolvable() {
        let ids = Registry.v1.map(\.id)
        XCTAssertEqual(Set(ids).count, ids.count)
        // Ids newer than the macOS 14 deployment target, with the macOS version that added them.
        let newer: [String: OperatingSystemVersion] = [
            "HKQuantityTypeIdentifierAppleSleepingBreathingDisturbances": .init(majorVersion: 15, minorVersion: 0, patchVersion: 0),
            "HKCategoryTypeIdentifierSleepApneaEvent": .init(majorVersion: 15, minorVersion: 0, patchVersion: 0),
            "HKCategoryTypeIdentifierHypertensionEvent": .init(majorVersion: 26, minorVersion: 2, patchVersion: 0),
        ]
        for type in Registry.v1 {
            if let since = newer[type.id], !ProcessInfo.processInfo.isOperatingSystemAtLeast(since) { continue }
            XCTAssertNotNil(type.sampleType, type.id)
        }
    }
}
