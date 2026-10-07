import Foundation
import HealthBridgeCore
import HealthKit
import XCTest
@testable import HealthBridgeHealthKit

/// Serves scripted pages per anchor and records every call. Unscripted anchors yield an empty page
/// that keeps the anchor.
final class FakeHealthStore: HealthStore, @unchecked Sendable {
    typealias Callback = @Sendable (@escaping @Sendable () -> Void) -> Void

    private let lock = NSLock()
    private var pages: [Data?: AnchoredPage]
    private let earliest: Date
    private var _anchors: [Data?] = []
    private var _starts: [Date] = []
    private var _limits: [Int] = []
    private var _authorized: [Set<HKObjectType>] = []
    private var _observers: [(HKSampleType, Callback)] = []
    private var _background: [HKSampleType] = []
    private var _summaryRanges: [(Date, Date)] = []
    private var _detailReads: [UUID] = []
    private var _excluded: [[String]] = []
    private var _sourceReads: [String] = []

    /// Pages per type identifier, checked before `pages`.
    var typedPages: [String: [Data?: AnchoredPage]] = [:]
    /// Detail per sample UUID; a sample without scripted detail fails its read.
    var ecgs: [UUID: ECG] = [:], beats: [UUID: Beats] = [:], routes: [UUID: Route] = [:], links: [UUID: UUID] = [:]
    /// Summaries by `YYYY-MM-DD`; a read returns the days inside the range.
    var summaries: [String: ActivitySummary] = [:]
    /// Sources per type identifier, and the newest sample end per bundle id and type identifier.
    var sources: [String: [HealthSource]] = [:]
    var lastSamples: [String: [String: Date]] = [:]
    /// Types whose background delivery fails.
    var refusedBackground: Set<String> = []

    init(pages: [Data?: AnchoredPage] = [:], earliest: Date = .distantPast) {
        self.pages = pages
        self.earliest = earliest
    }

    var requestedAnchors: [Data?] { lock.withLock { _anchors } }
    var requestedStarts: [Date] { lock.withLock { _starts } }
    var requestedLimits: [Int] { lock.withLock { _limits } }
    var authorized: [Set<HKObjectType>] { lock.withLock { _authorized } }
    var observers: [(HKSampleType, Callback)] { lock.withLock { _observers } }
    var backgroundTypes: [HKSampleType] { lock.withLock { _background } }
    var summaryRanges: [(Date, Date)] { lock.withLock { _summaryRanges } }
    var detailReads: [UUID] { lock.withLock { _detailReads } }
    /// The bundle ids each anchored read excluded.
    var requestedExclusions: [[String]] { lock.withLock { _excluded } }
    /// The types whose sources were read.
    var sourceReads: [String] { lock.withLock { _sourceReads } }

    func requestReadAuthorization(_ types: Set<HKObjectType>) async throws {
        lock.withLock { _authorized.append(types) }
    }

    func earliestPermittedSampleDate() -> Date { earliest }

    func anchoredPage(of type: HKSampleType, from start: Date, anchor: Data?, limit: Int,
                      excluding: [HealthSource]) async throws -> AnchoredPage {
        lock.withLock {
            _excluded.append(excluding.map(\.bundleID))
            _anchors.append(anchor)
            _starts.append(start)
            _limits.append(limit)
            return typedPages[type.identifier]?[anchor] ?? pages[anchor] ?? AnchoredPage(samples: [], deleted: [], anchor: anchor)
        }
    }

    func sources(for type: HKSampleType) async throws -> [HealthSource] {
        lock.withLock {
            _sourceReads.append(type.identifier)
            return sources[type.identifier] ?? []
        }
    }

    func lastSampleDate(of type: HKSampleType, from source: HealthSource) async throws -> Date? {
        lock.withLock { lastSamples[source.bundleID]?[type.identifier] }
    }

    func observe(_ type: HKSampleType, onUpdate: @escaping Callback) {
        lock.withLock { _observers.append((type, onUpdate)) }
    }

    func enableBackgroundDelivery(for type: HKSampleType) async throws {
        try lock.withLock {
            _background.append(type)
            if refusedBackground.contains(type.identifier) { throw HealthStoreError.unsupported("background delivery") }
        }
    }

    func electrocardiogram(_ sample: HKSample) async throws -> ECG { try detail(ecgs, sample) }
    func heartbeats(_ sample: HKSample) async throws -> Beats { try detail(beats, sample) }
    func route(_ sample: HKSample) async throws -> Route { try detail(routes, sample) }
    func workoutUUID(of sample: HKSample) async throws -> UUID? { lock.withLock { links[sample.uuid] } }

    func activitySummaries(from start: Date, through end: Date, in calendar: Calendar) async throws -> [ActivitySummary] {
        lock.withLock {
            _summaryRanges.append((start, end))
            return summaries.values.filter {
                guard let day = ISO8601DateFormatter.day(calendar).date(from: $0.date) else { return false }
                return day >= start && day <= end
            }
        }
    }

    private func detail<T>(_ scripted: [UUID: T], _ sample: HKSample) throws -> T {
        try lock.withLock {
            _detailReads.append(sample.uuid)
            guard let value = scripted[sample.uuid] else { throw HealthStoreError.unexpectedShape("no scripted detail") }
            return value
        }
    }
}

extension ISO8601DateFormatter {
    static func day(_ calendar: Calendar) -> ISO8601DateFormatter {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withFullDate]
        f.timeZone = calendar.timeZone
        return f
    }
}

/// A plain `HKSample` (no subclass), standing in for the classes tests cannot create (ECG, heartbeat
/// series, route): HealthKit has no public initializer for them, and the readers dispatch on the
/// registry type, not the class. Made by decoding an archived category sample as its superclass.
func placeholderSample(start: Date, end: Date, metadata: [String: Any]? = nil, device: HKDevice? = nil) -> HKSample {
    let seed = HKCategorySample(type: HKCategoryType(.mindfulSession), value: 0, start: start, end: end, device: device, metadata: metadata)
    let data = try! NSKeyedArchiver.archivedData(withRootObject: seed, requiringSecureCoding: true)
    let unarchiver = try! NSKeyedUnarchiver(forReadingFrom: data)
    unarchiver.requiresSecureCoding = false
    unarchiver.setClass(HKSample.self, forClassName: "HKCategorySample")
    let sample = unarchiver.decodeObject(forKey: NSKeyedArchiveRootObjectKey) as! HKSample
    precondition(!(sample is HKCategorySample))
    return sample
}

/// Records gunzipped batch bodies and answers every request with `status`.
final class FakeTransport: @unchecked Sendable {
    private let lock = NSLock()
    private var _bodies: [SamplesPage] = []
    private var _requests = 0
    let status: Int

    init(status: Int = 202) { self.status = status }

    var bodies: [SamplesPage] { lock.withLock { _bodies } }
    var requests: Int { lock.withLock { _requests } }

    var transport: Transport {
        { [self] request in
            let page = try Self.page(fromGzip: request.httpBody ?? Data())
            lock.withLock {
                _requests += 1
                _bodies.append(page)
            }
            return (Data(), HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: nil)!)
        }
    }

    static func page(fromGzip gz: Data) throws -> SamplesPage {
        struct Batch: Decodable {
            struct Item: Decodable { let body: SamplesPage }
            let items: [Item]
        }
        let deflated = gz.dropFirst(10).dropLast(8)
        let json = try (Data(deflated) as NSData).decompressed(using: .zlib) as Data
        let batch = try JSONDecoder().decode(Batch.self, from: json)
        XCTAssertEqual(batch.items.count, 1)
        return batch.items[0].body
    }
}

/// A sync wired to fakes; synthetic values only.
struct Harness {
    let store: FakeHealthStore
    let server: FakeTransport
    let anchors = AnchorStore(defaults: UserDefaults(suiteName: UUID().uuidString)!)
    let sync: HealthSync

    init(pages: [Data?: AnchoredPage] = [:], earliest: Date = .distantPast, backfillStart: Date = .distantPast, status: Int = 202,
         calendar: Calendar = amsterdam, now: Date = t0) {
        store = FakeHealthStore(pages: pages, earliest: earliest)
        server = FakeTransport(status: status)
        let credentials = Credentials(baseURL: URL(string: "https://vitamux.example.test")!, deviceID: "device-synthetic",
                                      connectionID: "conn-synthetic", token: "synthetic-token-not-secret")
        let client = IngestClient(transport: server.transport, sleep: { _ in })
        let sender = BatchSender(credentials: credentials, anchors: anchors, client: client, clientVersion: "0.0.0-test")
        sync = HealthSync(store: store, sender: sender, anchors: anchors, backfillStart: backfillStart, calendar: calendar, now: { now })
    }
}

let amsterdam: Calendar = {
    var c = Calendar(identifier: .gregorian)
    c.timeZone = TimeZone(identifier: "Europe/Amsterdam")!
    return c
}()

func registryType(_ id: String) -> HealthType { Registry.v2.first { $0.id == id }! }

let heartRate = Registry.v1.first { $0.id == HKQuantityTypeIdentifier.heartRate.rawValue }!
let a0 = Data("a0".utf8), a1 = Data("a1".utf8), a2 = Data("a2".utf8)
let t0 = ISO8601DateFormatter().date(from: "2026-07-01T06:00:00Z")!

func hrSamples(_ n: Int) -> [HKSample] {
    let type = HKQuantityType(.heartRate)
    let unit = HKUnit(from: "count/min")
    return (0..<n).map { i in
        let start = t0.addingTimeInterval(Double(i))
        return HKQuantitySample(type: type, quantity: HKQuantity(unit: unit, doubleValue: 60), start: start, end: start)
    }
}
