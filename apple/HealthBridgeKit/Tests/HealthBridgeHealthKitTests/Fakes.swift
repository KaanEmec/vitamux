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
    private var _authorized: [Set<HKObjectType>] = []
    private var _observers: [(HKSampleType, Callback)] = []
    private var _background: [HKSampleType] = []

    init(pages: [Data?: AnchoredPage] = [:], earliest: Date = .distantPast) {
        self.pages = pages
        self.earliest = earliest
    }

    var requestedAnchors: [Data?] { lock.withLock { _anchors } }
    var requestedStarts: [Date] { lock.withLock { _starts } }
    var authorized: [Set<HKObjectType>] { lock.withLock { _authorized } }
    var observers: [(HKSampleType, Callback)] { lock.withLock { _observers } }
    var backgroundTypes: [HKSampleType] { lock.withLock { _background } }

    func requestReadAuthorization(_ types: Set<HKObjectType>) async throws {
        lock.withLock { _authorized.append(types) }
    }

    func earliestPermittedSampleDate() -> Date { earliest }

    func anchoredPage(of type: HKSampleType, from start: Date, anchor: Data?, limit: Int) async throws -> AnchoredPage {
        lock.withLock {
            _anchors.append(anchor)
            _starts.append(start)
            return pages[anchor] ?? AnchoredPage(samples: [], deleted: [], anchor: anchor)
        }
    }

    func observe(_ type: HKSampleType, onUpdate: @escaping Callback) {
        lock.withLock { _observers.append((type, onUpdate)) }
    }

    func enableBackgroundDelivery(for type: HKSampleType) async throws {
        lock.withLock { _background.append(type) }
    }
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

    init(pages: [Data?: AnchoredPage] = [:], earliest: Date = .distantPast, backfillStart: Date = .distantPast, status: Int = 202) {
        store = FakeHealthStore(pages: pages, earliest: earliest)
        server = FakeTransport(status: status)
        let credentials = Credentials(baseURL: URL(string: "https://vitamux.example.test")!, deviceID: "device-synthetic",
                                      connectionID: "conn-synthetic", token: "synthetic-token-not-secret")
        let client = IngestClient(transport: server.transport, sleep: { _ in })
        let sender = BatchSender(credentials: credentials, anchors: anchors, client: client, clientVersion: "0.0.0-test")
        sync = HealthSync(store: store, sender: sender, anchors: anchors, backfillStart: backfillStart)
    }
}

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
