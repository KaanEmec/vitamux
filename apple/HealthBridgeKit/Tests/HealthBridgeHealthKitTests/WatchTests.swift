import CoreLocation
import CryptoKit
import Foundation
import HealthBridgeCore
import HealthKit
import XCTest
@testable import HealthBridgeHealthKit

/// Registry v2 and the Apple Watch readers (ADR-0024), against fakes and the synthetic examples in
/// schemas/examples/healthkit-samples.v1. Synthetic values only.
final class WatchTests: XCTestCase {
    let ecgType = registryType(Registry.electrocardiogramID)
    let beatsType = registryType(HKDataTypeIdentifierHeartbeatSeries)
    let routeType = registryType(HKWorkoutRouteTypeIdentifier)
    let workoutType = registryType(HKWorkoutTypeIdentifier)
    let summaryType = registryType(Registry.activitySummaryID)

    /// The examples' shared sample header: Watch device, Amsterdam zone.
    let watch = HKDevice(name: "Synthetic Watch", manufacturer: "Apple Inc.", model: "Watch", hardwareVersion: "Watch7,1",
                         firmwareVersion: nil, softwareVersion: "11.0", localIdentifier: nil, udiDeviceIdentifier: nil)
    let zone: [String: Any] = [HKMetadataKeyTimeZone: "Europe/Amsterdam"]

    func date(_ text: String) -> Date {
        (try? Date(text, strategy: Date.ISO8601FormatStyle(includingFractionalSeconds: true))) ?? (try! Date(text, strategy: .iso8601))
    }

    func placeholders(_ n: Int, from start: Date = t0) -> [HKSample] {
        (0..<n).map { placeholderSample(start: start + Double($0), end: start + Double($0) + 1) }
    }

    func decode<T: Decodable>(_ type: T.Type, _ object: Any) throws -> T {
        try JSONDecoder().decode(T.self, from: JSONSerialization.data(withJSONObject: object))
    }

    func exampleSample(_ name: String) throws -> [String: Any] {
        try XCTUnwrap((try example(name)["samples"] as? [[String: Any]])?.first)
    }

    /// Syncs one page holding `sample` and checks the uploaded sample against the example's (without the
    /// host-dependent `source_revision` and the random `uuid`) and the page against the schema.
    func assertMatchesExample(_ name: String, type: HealthType, sample: HKSample, ignoring: Set<String> = [],
                              store configure: (FakeHealthStore) -> Void,
                              file: StaticString = #filePath, line: UInt = #line) async throws {
        let h = Harness()
        h.store.typedPages[type.sampleType!.identifier] = [nil: AnchoredPage(samples: [sample], deleted: [], anchor: a1)]
        configure(h.store)
        try await h.sync.sync(type)
        let page = try XCTUnwrap(h.server.bodies.first, file: file, line: line)
        XCTAssertEqual(try SchemaCheck().errors(page), [], file: file, line: line)
        let uploaded = try XCTUnwrap(page.samples.first, file: file, line: line)
        XCTAssertEqual(uploaded.uuid, sample.uuid.uuidString, file: file, line: line)
        let drop = ignoring.union(["uuid", "source_revision"])
        XCTAssertEqual(try canonical(jsonObject(uploaded), dropping: drop), try canonical(exampleSample(name), dropping: drop),
                       file: file, line: line)
    }

    // MARK: Registry v2

    func testRegistryV2KeepsV1AndAddsWatchTypes() {
        XCTAssertEqual(Array(Registry.v2.prefix(Registry.v1.count)), Registry.v1, "v1 types keep id, group, unit and anchor")
        XCTAssertTrue(Registry.v1.allSatisfy { $0.pageLimit == Batcher.maxSamples && !$0.group.isOptIn })
        let ids = Registry.v2.map(\.id)
        XCTAssertEqual(Set(ids).count, ids.count)
        XCTAssertEqual(Registry.types(in: [.cycle]).count, 18)
        XCTAssertEqual(Registry.types(in: [.symptoms]).count, 39)
        XCTAssertEqual(Registry.types(in: [.mind]).map(\.id), ["HKCategoryTypeIdentifierMindfulSession", "HKDataTypeStateOfMind"])
        XCTAssertEqual([ecgType, beatsType, routeType].map(\.pageLimit), [5, 100, 1])
        XCTAssertEqual([ecgType, beatsType, routeType].map(\.group), [.ecg, .beats, .routes])
        XCTAssertEqual(Set(MetricGroup.allCases.filter(\.isOptIn)), [.mind, .cycle, .symptoms, .ecg, .beats, .routes])
        XCTAssertEqual(MetricGroup.routes.requires, .workouts)
        XCTAssertTrue(MetricGroup.allCases.filter { $0 != .routes }.allSatisfy { $0.requires == nil })
        XCTAssertTrue(summaryType.isActivitySummary)
        XCTAssertNil(summaryType.sampleType)
        XCTAssertEqual(Registry.activitySummaryID, "HKActivitySummaryTypeIdentifier")
        XCTAssertEqual(Registry.electrocardiogramID, "HKDataTypeIdentifierElectrocardiogram")
        if #available(macOS 15.0, iOS 18.0, *) { XCTAssertEqual(Registry.stateOfMindID, HKObjectType.stateOfMindType().identifier) }
    }

    func testRegistryV2IdsResolveOnThisOS() {
        // New ids newer than the macOS 14 deployment target, with the macOS version that added them.
        let macOS15 = OperatingSystemVersion(majorVersion: 15, minorVersion: 0, patchVersion: 0)
        let macOS27 = OperatingSystemVersion(majorVersion: 27, minorVersion: 0, patchVersion: 0)
        let newer: [String: OperatingSystemVersion] = [
            "HKQuantityTypeIdentifierHeartRateVariabilityRMSSD": macOS27, "HKCategoryTypeIdentifierBleedingAfterMenopause": macOS27,
            "HKCategoryTypeIdentifierMenopausalState": macOS27, "HKQuantityTypeIdentifierCrossCountrySkiingSpeed": macOS15,
            "HKQuantityTypeIdentifierWorkoutEffortScore": macOS15, "HKQuantityTypeIdentifierEstimatedWorkoutEffortScore": macOS15,
            "HKCategoryTypeIdentifierBleedingAfterPregnancy": macOS15, "HKCategoryTypeIdentifierBleedingDuringPregnancy": macOS15,
            Registry.stateOfMindID: macOS15,
        ]
        for type in Registry.v2[Registry.v1.count...] where !type.isActivitySummary {
            if let since = newer[type.id], !ProcessInfo.processInfo.isOperatingSystemAtLeast(since) {
                XCTAssertNil(type.sampleType, "\(type.id) is skipped where the OS does not know it")
                continue
            }
            XCTAssertEqual(type.sampleType?.identifier, type.id)
        }
    }

    func testAuthorizationIsPerGroupAndOptInGroupsStayOut() async throws {
        let h = Harness()
        try await h.sync.requestAuthorization(for: [.ecg])
        try await h.sync.requestAuthorization(for: [.beats])
        try await h.sync.requestAuthorization(for: [.routes])
        try await h.sync.requestAuthorization(for: [.activity])
        try await h.sync.requestAuthorization(for: Set(MetricGroup.allCases.filter { !$0.isOptIn }))
        let asked = h.store.authorized
        XCTAssertEqual(asked[0], [HKObjectType.electrocardiogramType()])
        XCTAssertEqual(asked[1], [HKSeriesType.heartbeat()])
        XCTAssertEqual(asked[2], [HKSeriesType.workoutRoute()])
        XCTAssertTrue(asked[3].contains(HKObjectType.activitySummaryType()))
        let optIn = Registry.types(in: Set(MetricGroup.allCases.filter(\.isOptIn))).reduce(into: Set<HKObjectType>()) { $0.formUnion($1.readTypes) }
        XCTAssertTrue(asked[4].isDisjoint(with: optIn), "the v1 groups never ask for an opt-in type")
        XCTAssertFalse(asked[4].contains(HKObjectType.electrocardiogramType()))
    }

    func testBackgroundDeliveryForNewSampleTypesSurvivesRefusals() async {
        let h = Harness()
        let mind = Registry.types(in: [.mind])
        h.store.refusedBackground = [Registry.electrocardiogramID]
        let failures = await h.sync.observe([ecgType, beatsType, routeType, summaryType] + mind)
        let registered = h.store.observers.map(\.0.identifier)
        XCTAssertEqual(Array(registered.prefix(3)), [Registry.electrocardiogramID, HKDataTypeIdentifierHeartbeatSeries, HKWorkoutRouteTypeIdentifier])
        XCTAssertFalse(registered.contains(Registry.activitySummaryID), "a summary has no observer query")
        XCTAssertEqual(h.store.backgroundTypes.map(\.identifier), registered, "a refusal does not stop the next types")
        XCTAssertEqual(Array(failures.keys), [Registry.electrocardiogramID])
    }

    // MARK: ECG, beats, routes

    func testECGPagesOfFiveWithVoltagesAndDeletions() async throws {
        let first = placeholders(5), second = placeholders(2, from: t0 + 100)
        let gone = UUID()
        let h = Harness()
        h.store.typedPages[Registry.electrocardiogramID] = [
            nil: AnchoredPage(samples: first, deleted: [], anchor: a1),
            a1: AnchoredPage(samples: second, deleted: [gone], anchor: a2),
        ]
        let ecg = ECG(classification: 1, symptomsStatus: 1, averageHeartRate: 64, samplingFrequencyHz: 512, lead: 1,
                      voltageCount: 3, voltages: [0, 15.5, -2.25])
        for s in first + second { h.store.ecgs[s.uuid] = ecg }
        try await h.sync.sync(ecgType)

        XCTAssertEqual(h.store.requestedLimits, [5, 5])
        XCTAssertEqual(h.server.bodies.map(\.samples.count), [5, 2])
        XCTAssertTrue(h.server.bodies.flatMap(\.samples).allSatisfy { $0.ecg == ecg && $0.value == nil })
        XCTAssertEqual(h.server.bodies[1].deleted, [Deleted(uuid: gone.uuidString)])
        XCTAssertEqual(h.anchors.anchor(for: ecgType.id), a2)
        for page in h.server.bodies { XCTAssertEqual(try SchemaCheck().errors(page), []) }
    }

    func testFailedDetailReadSendsNothingAndKeepsAnchor() async throws {
        let h = Harness(pages: [a0: AnchoredPage(samples: placeholders(2), deleted: [], anchor: a1)])
        h.anchors.set(a0, for: ecgType.id)
        do {
            try await h.sync.sync(ecgType) // no scripted voltages
            XCTFail("a missing ECG detail did not fail the page")
        } catch {}
        XCTAssertEqual(h.server.requests, 0)
        XCTAssertEqual(h.anchors.anchor(for: ecgType.id), a0)
    }

    func testStoreWithoutDetailReadsFailsVisibly() async throws {
        struct OldStore: HealthStore { // a store written before registry v2
            func requestReadAuthorization(_ types: Set<HKObjectType>) async throws {}
            func earliestPermittedSampleDate() -> Date { .distantPast }
            func anchoredPage(of type: HKSampleType, from start: Date, anchor: Data?, limit: Int,
                              excluding: [HealthSource]) async throws -> AnchoredPage {
                AnchoredPage(samples: [], deleted: [], anchor: anchor)
            }
            func sources(for type: HKSampleType) async throws -> [HealthSource] { [] }
            func lastSampleDate(of type: HKSampleType, from source: HealthSource) async throws -> Date? { nil }
            func observe(_ type: HKSampleType, onUpdate: @escaping @Sendable (@escaping @Sendable () -> Void) -> Void) {}
            func enableBackgroundDelivery(for type: HKSampleType) async throws {}
        }
        let sample = placeholders(1)[0]
        do { _ = try await OldStore().electrocardiogram(sample); XCTFail() } catch {
            XCTAssertEqual(error as? HealthStoreError, .unsupported("electrocardiogram"))
        }
        let link = try await OldStore().workoutUUID(of: sample)
        XCTAssertNil(link)
    }

    func testBeatsPageOfHundredThenShortPage() async throws {
        let first = placeholders(100), second = placeholders(3, from: t0 + 1000)
        let h = Harness()
        h.store.typedPages[HKDataTypeIdentifierHeartbeatSeries] = [
            nil: AnchoredPage(samples: first, deleted: [], anchor: a1),
            a1: AnchoredPage(samples: second, deleted: [UUID()], anchor: a2),
        ]
        let beats = Beats(offsetsS: [0, 0.9, 2.7], precededByGap: [false, false, true])
        for s in first + second { h.store.beats[s.uuid] = beats }
        try await h.sync.sync(beatsType)

        XCTAssertEqual(h.store.requestedLimits, [100, 100])
        XCTAssertEqual(h.server.bodies.map(\.samples.count), [100, 3])
        XCTAssertEqual(h.server.bodies[1].deleted.count, 1)
        XCTAssertEqual(h.server.bodies[0].samples[0].beats?.count, 3)
        XCTAssertEqual(h.anchors.anchor(for: beatsType.id), a2)
    }

    func testRouteBeforeItsWorkoutCarriesTheLinkAndAnchorsStayApart() async throws {
        let workoutID = UUID()
        let linked = placeholderSample(start: t0, end: t0 + 4), unlinked = placeholderSample(start: t0 + 10, end: t0 + 14)
        let h = Harness()
        h.store.typedPages[HKWorkoutRouteTypeIdentifier] = [
            nil: AnchoredPage(samples: [linked], deleted: [], anchor: a1),
            a1: AnchoredPage(samples: [unlinked], deleted: [], anchor: a2),
        ]
        let route = Route(count: 1, offsetsS: [0], latitude: [0.0001], longitude: [0.0001])
        h.store.routes = [linked.uuid: route, unlinked.uuid: route]
        h.store.links = [linked.uuid: workoutID]

        try await h.sync.sync(routeType) // the workout has not synced yet
        XCTAssertEqual(h.store.requestedLimits, [1, 1, 1], "one route per page, until a short page")
        XCTAssertEqual(h.server.bodies.map { $0.samples.first?.workoutUUID }, [workoutID.uuidString, nil])
        XCTAssertEqual(h.server.bodies.map(\.samples.first?.route), [route, route])
        XCTAssertEqual(h.anchors.anchor(for: routeType.id), a2)
        XCTAssertNil(h.anchors.anchor(for: workoutType.id), "the workout type keeps its own anchor")
    }

    func testTwelveHourRouteIsSentAloneUnderTheServerLimit() async throws {
        let n = 12 * 3600
        let locations: [CLLocation] = (0..<n).map { i in
            let latitude: Double = Double(i % 997) * 1e-6
            let longitude: Double = Double(i % 991) * 1e-6
            let course: Double = Double(i % 360)
            let timestamp: Date = t0.addingTimeInterval(Double(i))
            return CLLocation(coordinate: CLLocationCoordinate2D(latitude: latitude, longitude: longitude),
                              altitude: 1.5, horizontalAccuracy: 4, verticalAccuracy: 3, course: course, courseAccuracy: 5,
                              speed: 3, speedAccuracy: 0.4, timestamp: timestamp)
        }
        let sample = placeholderSample(start: t0, end: t0 + Double(n))
        let h = Harness()
        h.store.typedPages[HKWorkoutRouteTypeIdentifier] = [nil: AnchoredPage(samples: [sample], deleted: [], anchor: a1)]
        h.store.routes[sample.uuid] = Mapping.route(locations, start: t0)
        try await h.sync.sync(routeType)

        XCTAssertEqual(h.server.requests, 1)
        XCTAssertEqual(h.server.bodies[0].samples[0].route?.count, n)
        let gz = try (JSONEncoder().encode(h.server.bodies[0]) as NSData).compressed(using: .zlib) as Data
        XCTAssertLessThan(gz.count, 10 << 20, "within the server's 10 MiB body limit")
    }

    // MARK: Pages built from the synthetic examples

    /// The ECG example's algorithm-version key. Metadata is passed through as written, and the value of the
    /// SDK constant `HKMetadataKeyAppleECGAlgorithmVersion` is the longer string asserted here (D13 checks it).
    let exampleECGAlgorithmKey = "HKMetadataKeyAppleECGAlgorithmVersion"

    func testECGMatchesExample() async throws {
        XCTAssertEqual(HKMetadataKeyAppleECGAlgorithmVersion, "HKMetadataKeyAppleECGAlgorithmVersion")
        let ex = try exampleSample("ecg")
        let sample = placeholderSample(start: date(ex["start"] as! String), end: date(ex["end"] as! String),
                                       metadata: zone.merging([exampleECGAlgorithmKey: 2]) { $1 }, device: watch)
        let ecg = try decode(ECG.self, ex["ecg"]!)
        try await assertMatchesExample("ecg", type: ecgType, sample: sample) { $0.ecgs[sample.uuid] = ecg }
    }

    func testBeatsMatchExample() async throws {
        let ex = try exampleSample("beats")
        let sample = placeholderSample(start: date(ex["start"] as! String), end: date(ex["end"] as! String), metadata: zone, device: watch)
        let beats = try decode(Beats.self, ex["beats"]!)
        try await assertMatchesExample("beats", type: beatsType, sample: sample) { $0.beats[sample.uuid] = beats }
    }

    func testRouteFromLocationsMatchesExample() async throws {
        let ex = try exampleSample("route")
        let r = ex["route"] as! [String: Any]
        func col(_ key: String) -> [Double] { r[key] as! [Double] }
        let start = date(ex["start"] as! String)
        // CLLocation has no public initializer taking an ellipsoidal altitude, so that column is not compared.
        let lat = col("latitude"), lon = col("longitude"), alt = col("altitude_m")
        let hAcc = col("horizontal_accuracy_m"), vAcc = col("vertical_accuracy_m"), course = col("course_deg")
        let courseAcc = col("course_accuracy_deg"), speed = col("speed_mps"), speedAcc = col("speed_accuracy_mps"), offsets = col("offsets_s")
        var locations: [CLLocation] = []
        for i in lat.indices {
            let coordinate = CLLocationCoordinate2D(latitude: lat[i], longitude: lon[i])
            let time: Date = start + offsets[i]
            locations.append(CLLocation(coordinate: coordinate, altitude: alt[i], horizontalAccuracy: hAcc[i],
                                        verticalAccuracy: vAcc[i], course: course[i], courseAccuracy: courseAcc[i], speed: speed[i],
                                        speedAccuracy: speedAcc[i], timestamp: time, sourceInfo: CLLocationSourceInformation()))
        }
        let sample = placeholderSample(start: start, end: date(ex["end"] as! String), metadata: zone, device: watch)
        // CLLocation may round a coordinate in its last bit, so coordinates are compared with a tolerance.
        let route = Mapping.route(locations, start: start)
        for (got, want) in zip(route.latitude + route.longitude, lat + lon) { XCTAssertEqual(got, want, accuracy: 1e-12) }
        try await assertMatchesExample("route", type: routeType, sample: sample,
                                       ignoring: ["ellipsoidal_altitude_m", "latitude", "longitude"]) {
            $0.routes[sample.uuid] = route
            $0.links[sample.uuid] = UUID(uuidString: ex["workout_uuid"] as! String)
        }
    }

    func testEffortScoreMatchesExample() async throws {
        let type = registryType("HKQuantityTypeIdentifierWorkoutEffortScore")
        guard let quantityType = type.sampleType as? HKQuantityType else { throw XCTSkip("effort scores need macOS 15") }
        let ex = try exampleSample("workout_effort")
        let sample = HKQuantitySample(type: quantityType, quantity: HKQuantity(unit: HKUnit(from: "appleEffortScore"), doubleValue: 6),
                                      start: date(ex["start"] as! String), end: date(ex["end"] as! String), device: watch, metadata: zone)
        try await assertMatchesExample("workout_effort", type: type, sample: sample) {
            $0.links[sample.uuid] = UUID(uuidString: ex["workout_uuid"] as! String)
        }
    }

    func testStateOfMindMatchesExample() async throws {
        guard #available(macOS 15.0, iOS 18.0, *) else { throw XCTSkip("State of Mind needs macOS 15") }
        let ex = try exampleSample("state_of_mind")
        let m = ex["state_of_mind"] as! [String: Any]
        let sample = HKStateOfMind(date: date(ex["start"] as! String), kind: HKStateOfMind.Kind(rawValue: m["kind"] as! Int)!,
                                   valence: m["valence"] as! Double,
                                   labels: (m["labels"] as! [Int]).map { HKStateOfMind.Label(rawValue: $0)! },
                                   associations: (m["associations"] as! [Int]).map { HKStateOfMind.Association(rawValue: $0)! },
                                   metadata: zone)
        // HKStateOfMind has no device parameter; compare without it.
        let h = Harness()
        let type = registryType(Registry.stateOfMindID)
        h.store.typedPages[type.id] = [nil: AnchoredPage(samples: [sample], deleted: [], anchor: a1)]
        try await h.sync.sync(type)
        let page = try XCTUnwrap(h.server.bodies.first)
        XCTAssertEqual(try SchemaCheck().errors(page), [])
        XCTAssertEqual(try canonical(jsonObject(page.samples[0]), dropping: ["uuid", "source_revision", "device"]),
                       try canonical(ex, dropping: ["uuid", "source_revision", "device"]))
    }

    @available(*, deprecated, message: "HKWorkout(activityType:start:end:workoutEvents:…) is the simplest synthetic workout with events")
    func testWorkoutEventsMatchExampleShape() throws {
        let ex = try exampleSample("workout_detail")
        let start = date(ex["start"] as! String), end = date(ex["end"] as! String)
        let events = ((ex["workout"] as! [String: Any])["events"] as! [[String: Any]]).map {
            HKWorkoutEvent(type: HKWorkoutEventType(rawValue: $0["type"] as! Int)!,
                           dateInterval: DateInterval(start: date($0["start"] as! String), end: date($0["end"] as! String)), metadata: nil)
        }
        let w = HKWorkout(activityType: .running, start: start, end: end, workoutEvents: events, totalEnergyBurned: nil,
                          totalDistance: nil, metadata: zone)
        let out = Mapping.sample(w, unit: nil)
        XCTAssertEqual(try canonical(jsonObject(out.workout?.events)),
                       try canonical((ex["workout"] as! [String: Any])["events"]!))
        // HealthKit reports a single-activity workout as one activity spanning it; it is sent as reported.
        let activities = try XCTUnwrap(out.workout?.activities)
        XCTAssertEqual(activities.map(\.activityType), [HKWorkoutActivityType.running.rawValue])
        XCTAssertEqual(activities[0].events, out.workout?.events)
        let page = SamplesPage(type: HKWorkoutTypeIdentifier, anchor: AnchorInfo(beforeHash: "none", afterHash: "x", queryStartedAt: rfc3339(t0)),
                               samples: [out], deleted: [])
        XCTAssertEqual(try SchemaCheck().errors(page), [])
    }

    func testEveryExampleRoundTripsThroughThePayloadModel() throws {
        for name in ["activity_summary", "beats", "ecg", "route", "state_of_mind", "workout_detail", "workout_effort"] {
            var ex = try example(name)
            ex["synthetic"] = nil
            let page = try decode(SamplesPage.self, ex)
            XCTAssertEqual(try canonical(jsonObject(page)), try canonical(ex), name)
            XCTAssertEqual(try SchemaCheck().errors(page), [], name)
        }
        // The checker itself rejects an undeclared key, a missing required key and a wrong type.
        let check = try SchemaCheck()
        let sample = try exampleSample("beats")
        XCTAssertFalse(check.errors(sample.merging(["beat": 1]) { $1 }, check.root["$defs"].flatMap { ($0 as! [String: Any])["sample"] } as! [String: Any], "$").isEmpty)
        XCTAssertFalse(check.errors(sample.filter { $0.key != "uuid" }, ["$ref": "#/$defs/sample"], "$").isEmpty)
        XCTAssertFalse(check.errors(sample.merging(["was_user_entered": 0]) { $1 }, ["$ref": "#/$defs/sample"], "$").isEmpty)
    }

    func testUnevenOffsetsOnlyWhenSpacingDiffers() {
        XCTAssertNil(Mapping.unevenOffsets([0, 1.0 / 512, 2.0 / 512], frequency: 512))
        XCTAssertEqual(Mapping.unevenOffsets([0, 0.01, 0.02], frequency: 512), [0, 0.01, 0.02])
        XCTAssertEqual(Mapping.unevenOffsets([0, 0.01], frequency: nil), [0, 0.01])
    }

    // MARK: Activity summaries

    func summary(_ day: String, kcal: Double = 500) -> ActivitySummary {
        ActivitySummary(date: day, moveMode: 1, paused: false, activeEnergyKcal: kcal, activeEnergyGoalKcal: 600, moveTimeS: 2400,
                        moveTimeGoalS: 1800, exerciseTimeS: 1980, exerciseTimeGoalS: 1800, standHours: 10, standHoursGoal: 12)
    }

    func testSummaryKeysAndDayBoundsMatchExample() throws {
        let ex = try example("activity_summary")
        for s in ex["samples"] as! [[String: Any]] {
            let summary = try decode(ActivitySummary.self, s["activity_summary"]!)
            let sample = try XCTUnwrap(Mapping.sample(summary, calendar: amsterdam))
            XCTAssertEqual(sample.uuid, s["uuid"] as? String)
            XCTAssertEqual(date(sample.start), date(s["start"] as! String))
            XCTAssertEqual(date(sample.end), date(s["end"] as! String))
            XCTAssertEqual(sample.sourceRevision, SourceRevision(bundleId: "vitamux.activity-summary", name: "Activity summary"))
            XCTAssertEqual(try canonical(jsonObject(sample), dropping: ["start", "end"]), try canonical(s, dropping: ["start", "end"]))
        }
        XCTAssertEqual(Mapping.uuidV5(namespace: UUID(uuidString: "6ba7b810-9dad-11d1-80b4-00c04fd430c8")!, name: "python.org").uuidString,
                       "886313E1-3B8A-5372-9B90-0C9AEE199E5D", "RFC 4122 DNS example")
    }

    func testSummaryFirstSyncBackfillsInYearPagesThenRereadsAWeek() async throws {
        let now = date("2026-09-14T21:00:00+02:00")
        let h = Harness(backfillStart: date("2025-01-01T00:00:00+01:00"), now: now)
        h.store.summaries = ["2025-06-01": summary("2025-06-01"), "2026-09-13": summary("2026-09-13"), "2026-09-14": summary("2026-09-14")]
        try await h.sync.sync(summaryType)

        let day = ISO8601DateFormatter.day(amsterdam)
        XCTAssertEqual(h.store.summaryRanges.map { day.string(from: $0.0) + "/" + day.string(from: $0.1) },
                       ["2025-01-01/2026-01-01", "2026-01-02/2026-09-14"], "366 days per page, up to today")
        XCTAssertEqual(h.server.bodies.map { $0.samples.map { $0.activitySummary!.date } }, [["2025-06-01"], ["2026-09-13", "2026-09-14"]])
        XCTAssertTrue(h.server.bodies.allSatisfy { $0.type == "HKActivitySummaryTypeIdentifier" && $0.deleted.isEmpty })
        XCTAssertTrue(h.server.bodies.allSatisfy { $0.anchor.beforeHash == $0.anchor.afterHash && $0.anchor.beforeHash.count == 64 })
        XCTAssertEqual(h.anchors.anchor(for: summaryType.id), Data("2026-09-14".utf8))
        for page in h.server.bodies { XCTAssertEqual(try SchemaCheck().errors(page), []) }

        try await h.sync.sync(summaryType)
        XCTAssertEqual(h.store.summaryRanges.last.map { day.string(from: $0.0) + "/" + day.string(from: $0.1) }, "2026-09-08/2026-09-14")
        XCTAssertEqual(h.server.bodies[2].anchor.beforeHash, h.server.bodies[1].anchor.beforeHash,
                       "an unchanged week re-sends the same key, which the server treats as a duplicate")
    }

    func testRereadSummaryDayChangesTheKeyOnlyWhenTheDayChanges() async throws {
        let now = date("2026-09-14T21:00:00+02:00")
        let h = Harness(now: now)
        h.anchors.set(Data("2026-09-14".utf8), for: summaryType.id)
        h.store.summaries = ["2026-09-14": summary("2026-09-14", kcal: 400)]
        try await h.sync.sync(summaryType)
        try await h.sync.sync(summaryType)
        h.store.summaries["2026-09-14"] = summary("2026-09-14", kcal: 655)
        try await h.sync.sync(summaryType)

        let keys = h.server.bodies.map(\.anchor.beforeHash)
        XCTAssertEqual(keys.count, 3)
        XCTAssertEqual(keys[0], keys[1])
        XCTAssertNotEqual(keys[1], keys[2])
        XCTAssertEqual(h.server.bodies.map { $0.samples[0].uuid }, Array(repeating: h.server.bodies[0].samples[0].uuid, count: 3),
                       "the day keeps its UUIDv5, so the changed day supersedes the old row")
    }

    func testInterruptedSummaryBackfillResumesAfterTheLastAcceptedPage() async throws {
        let now = date("2026-09-14T21:00:00+02:00")
        let h = Harness(now: now)
        h.anchors.set(Data("2024-03-31".utf8), for: summaryType.id) // the backfill stopped here
        try await h.sync.sync(summaryType)
        let day = ISO8601DateFormatter.day(amsterdam)
        XCTAssertEqual(h.store.summaryRanges.first.map { day.string(from: $0.0) }, "2024-04-01")
        XCTAssertEqual(h.anchors.anchor(for: summaryType.id), Data("2026-09-14".utf8))
    }

    func testSummaryHashIsCompactSortedKeyJSON() throws {
        let summaries = [summary("2026-09-13")]
        let json = #"[{"active_energy_goal_kcal":600,"active_energy_kcal":500,"date":"2026-09-13","exercise_time_goal_s":1800,"exercise_time_s":1980,"move_mode":1,"move_time_goal_s":1800,"move_time_s":2400,"paused":false,"stand_hours":10,"stand_hours_goal":12}]"#
        let expected = sha256Hex(json)
        XCTAssertEqual(Mapping.summaryHash(summaries), expected)
    }

    func testActivitySummaryMapping() {
        let s = HKActivitySummary()
        s.activityMoveMode = .activeEnergy
        s.activeEnergyBurned = HKQuantity(unit: .kilocalorie(), doubleValue: 512.4)
        s.activeEnergyBurnedGoal = HKQuantity(unit: .kilocalorie(), doubleValue: 600)
        s.appleMoveTime = HKQuantity(unit: .minute(), doubleValue: 40)
        s.appleMoveTimeGoal = HKQuantity(unit: .minute(), doubleValue: 30)
        s.appleExerciseTime = HKQuantity(unit: .minute(), doubleValue: 33)
        s.exerciseTimeGoal = HKQuantity(unit: .minute(), doubleValue: 30)
        s.appleStandHours = HKQuantity(unit: .count(), doubleValue: 10)
        s.standHoursGoal = HKQuantity(unit: .count(), doubleValue: 12)
        XCTAssertNil(Mapping.activitySummary(s, day: DateComponents()))
        let out = Mapping.activitySummary(s, day: DateComponents(year: 2026, month: 9, day: 3))
        var expected = summary("2026-09-03", kcal: 512.4)
        if #unavailable(macOS 15.0, iOS 18.0) { expected.paused = nil }
        XCTAssertEqual(out, expected)
    }
}

func sha256Hex(_ text: String) -> String { SHA256.hash(data: Data(text.utf8)).map { String(format: "%02x", $0) }.joined() }
