import Foundation
import XCTest
@testable import HealthBridgeCore

// MARK: - Helpers

/// Records requests and replays scripted statuses; the last status repeats.
final class FakeServer: @unchecked Sendable {
    private let lock = NSLock()
    private var _requests: [URLRequest] = []
    private var _sleeps: [Double] = []
    private var script: [(Int, [String: String], Data)]

    init(_ script: [(Int, [String: String], Data)]) { self.script = script }
    convenience init(statuses: [Int]) { self.init(statuses.map { ($0, [:], Data()) }) }

    var requests: [URLRequest] { lock.withLock { _requests } }
    var sleeps: [Double] { lock.withLock { _sleeps } }

    var transport: Transport {
        { [self] request in
            let (status, headers, data) = lock.withLock { () -> (Int, [String: String], Data) in
                _requests.append(request)
                return script.count > 1 ? script.removeFirst() : script[0]
            }
            let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: headers)!
            return (data, response)
        }
    }

    var sleep: @Sendable (Double) async throws -> Void {
        { [self] s in lock.withLock { _sleeps.append(s) } }
    }

    func client(maxAttempts: Int = 3) -> IngestClient {
        IngestClient(transport: transport, maxAttempts: maxAttempts, sleep: sleep)
    }
}

let baseURL = URL(string: "https://vitamux.example.test")!
let connID = "conn_" + String(repeating: "ab", count: 16)
let deviceID = "00000000-0000-4000-8000-0000000000d1"
let credentials = Credentials(baseURL: baseURL, deviceID: deviceID, connectionID: connID, token: "synthetic-token-not-secret-1234")
let hrType = "HKQuantityTypeIdentifierHeartRate"

func uuid(_ n: Int) -> String { String(format: "00000000-0000-4000-8000-%012d", n) }

func sample(_ n: Int) -> Sample {
    Sample(uuid: uuid(n), start: "2026-01-01T00:00:00.000+00:00", end: "2026-01-01T00:00:01.000+00:00",
           value: 60, unit: "count/min",
           sourceRevision: SourceRevision(bundleId: "com.example.synthetic", name: "Synthetic"))
}

func page(samples n: Int, deleted d: Int = 0, before: String = "none") -> SamplesPage {
    SamplesPage(type: hrType,
                anchor: AnchorInfo(beforeHash: before, afterHash: "after", queryStartedAt: "2026-01-01T00:00:00.000+00:00"),
                samples: (1...max(n, 1)).prefix(n).map(sample),
                deleted: (0..<d).map { Deleted(uuid: uuid(9000 + $0)) })
}

func freshAnchors() -> AnchorStore { AnchorStore(defaults: UserDefaults(suiteName: UUID().uuidString)!) }

func sender(_ server: FakeServer, anchors: AnchorStore, maxAttempts: Int = 3) -> BatchSender {
    BatchSender(credentials: credentials, anchors: anchors, client: server.client(maxAttempts: maxAttempts), clientVersion: "0.0.0-test")
}

func gunzip(_ data: Data) throws -> Data {
    XCTAssertEqual(Array(data.prefix(2)), [0x1f, 0x8b], "gzip magic")
    let raw = Data(data.dropFirst(10).dropLast(8))
    return try (raw as NSData).decompressed(using: .zlib) as Data
}

func json(_ request: URLRequest) throws -> [String: Any] {
    try JSONSerialization.jsonObject(with: try gunzip(request.httpBody!)) as! [String: Any]
}

let anchorData = Data([1, 2, 3])

// MARK: - BatchSender

final class BatchSenderTests: XCTestCase {
    func testAnchorAdvancesAfter202() async throws {
        let anchors = freshAnchors()
        let server = FakeServer(statuses: [202])
        try await sender(server, anchors: anchors).send(page(samples: 3), newAnchor: anchorData)
        XCTAssertEqual(anchors.anchor(for: hrType), anchorData)
        XCTAssertEqual(server.requests.count, 1)
    }

    func test409CountsAsAccepted() async throws {
        let anchors = freshAnchors()
        try await sender(FakeServer(statuses: [409]), anchors: anchors).send(page(samples: 1), newAnchor: anchorData)
        XCTAssertEqual(anchors.anchor(for: hrType), anchorData)
    }

    func testAnchorNotAdvancedWhenServerKeeps500() async throws {
        let anchors = freshAnchors()
        let server = FakeServer(statuses: [500])
        do {
            try await sender(server, anchors: anchors, maxAttempts: 3).send(page(samples: 1), newAnchor: anchorData)
            XCTFail("expected failure")
        } catch let e as BridgeError {
            XCTAssertEqual(e, .rejected(status: 500))
        }
        XCTAssertNil(anchors.anchor(for: hrType))
        XCTAssertEqual(server.requests.count, 3)
    }

    func testAnchorNotAdvancedWhen422() async throws {
        let anchors = freshAnchors()
        let server = FakeServer(statuses: [422])
        do {
            try await sender(server, anchors: anchors).send(page(samples: 1), newAnchor: anchorData)
            XCTFail("expected failure")
        } catch let e as BridgeError {
            XCTAssertEqual(e, .rejected(status: 422))
        }
        XCTAssertNil(anchors.anchor(for: hrType))
        XCTAssertEqual(server.requests.count, 1, "422 is not retried")
    }

    func test401IsUnauthorizedAndKeepsAnchor() async throws {
        let anchors = freshAnchors()
        anchors.set(Data([9]), for: hrType)
        do {
            try await sender(FakeServer(statuses: [401]), anchors: anchors).send(page(samples: 1), newAnchor: anchorData)
            XCTFail("expected failure")
        } catch let e as BridgeError {
            XCTAssertEqual(e, .unauthorized)
        }
        XCTAssertEqual(anchors.anchor(for: hrType), Data([9]))
    }

    func testAnchorNotAdvancedIfLaterChunkFails() async throws {
        let anchors = freshAnchors()
        let server = FakeServer(statuses: [202, 422])
        do {
            try await sender(server, anchors: anchors).send(page(samples: 2_500), newAnchor: anchorData)
            XCTFail("expected failure")
        } catch is BridgeError {}
        XCTAssertNil(anchors.anchor(for: hrType))
        XCTAssertEqual(server.requests.count, 2)
    }

    func testEmptyPageCommitsAnchorWithoutRequest() async throws {
        let anchors = freshAnchors()
        let server = FakeServer(statuses: [500])
        let empty = SamplesPage(type: hrType, anchor: page(samples: 1).anchor, samples: [], deleted: [])
        try await sender(server, anchors: anchors).send(empty, newAnchor: anchorData)
        XCTAssertEqual(anchors.anchor(for: hrType), anchorData)
        XCTAssertTrue(server.requests.isEmpty)
    }

    func testResendKeepsIdempotencyKeyAndBeforeHashChangesIt() async throws {
        let server = FakeServer(statuses: [202])
        let s = sender(server, anchors: freshAnchors())
        let p = page(samples: 3, before: "aaaa")
        try await s.send(p, newAnchor: nil)
        try await s.send(p, newAnchor: nil)
        try await s.send(page(samples: 3, before: "bbbb"), newAnchor: nil)
        let keys = server.requests.map { $0.value(forHTTPHeaderField: "Idempotency-Key") }
        XCTAssertNotNil(keys[0])
        XCTAssertEqual(keys[0], keys[1])
        XCTAssertNotEqual(keys[0], keys[2])
    }

    func testUploadRequestShape() async throws {
        let server = FakeServer(statuses: [202])
        var p = page(samples: 1)
        p.samples[0].wasUserEntered = true
        try await sender(server, anchors: freshAnchors()).send(p, newAnchor: nil)

        let request = try XCTUnwrap(server.requests.first)
        XCTAssertEqual(request.httpMethod, "POST")
        XCTAssertEqual(request.url?.path, "/api/ingest/v1/batches")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Content-Encoding"), "gzip")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer \(credentials.token)")

        let root = try json(request)
        XCTAssertEqual(root["schema"] as? String, "vitamux.ingest.batch/1")
        XCTAssertEqual(root["connection_id"] as? String, connID)
        let item = try XCTUnwrap((root["items"] as? [[String: Any]])?.first)
        XCTAssertEqual(item["stream"] as? String, "healthkit.samples.v1")
        let body = try XCTUnwrap(item["body"] as? [String: Any])
        XCTAssertEqual((body["anchor"] as? [String: Any])?["before_hash"] as? String, "none")
        let s = try XCTUnwrap((body["samples"] as? [[String: Any]])?.first)
        XCTAssertNotNil(s["source_revision"])
        XCTAssertEqual(s["was_user_entered"] as? Bool, true)
        XCTAssertNil(s["sourceRevision"])
    }

    func testRetryAfterHeaderDrivesSleep() async throws {
        let server = FakeServer([(429, ["Retry-After": "7"], Data()), (202, [:], Data())])
        let anchors = freshAnchors()
        try await sender(server, anchors: anchors).send(page(samples: 1), newAnchor: anchorData)
        XCTAssertEqual(server.sleeps, [7])
        XCTAssertEqual(server.requests.count, 2)
        XCTAssertEqual(anchors.anchor(for: hrType), anchorData)
    }
}

// MARK: - Batcher

final class BatcherTests: XCTestCase {
    func testSplitsIntoChunksOfAtMost2000AndDeletionsOnlyInLast() {
        let chunks = Batcher.chunks(page(samples: 4_500, deleted: 2), size: { _ in 10 })
        XCTAssertEqual(chunks.map(\.samples.count), [2_000, 2_000, 500])
        XCTAssertEqual(chunks.map(\.deleted.count), [0, 0, 2])
        XCTAssertEqual(chunks.flatMap(\.samples).map(\.uuid), page(samples: 4_500).samples.map(\.uuid))
    }

    func testEmptyPageYieldsNoChunks() {
        XCTAssertTrue(Batcher.chunks(page(samples: 0), size: { _ in 10 }).isEmpty)
    }

    func testDeletionsOnlyPageYieldsOneChunk() {
        let chunks = Batcher.chunks(page(samples: 0, deleted: 3), size: { _ in 10 })
        XCTAssertEqual(chunks.count, 1)
        XCTAssertEqual(chunks[0].deleted.count, 3)
    }

    func testOversizedChunkIsHalved() {
        // Pretend a chunk is too big whenever it holds more than 500 samples.
        let chunks = Batcher.chunks(page(samples: 1_000, deleted: 1), size: { $0.samples.count > 500 ? Batcher.maxGzipBytes + 1 : 10 })
        XCTAssertEqual(chunks.map(\.samples.count), [500, 500])
        XCTAssertEqual(chunks.map(\.deleted.count), [0, 1])
    }
}

// MARK: - IngestClient.pair

final class PairTests: XCTestCase {
    func testPairPostsCodeAndNameAndDecodesCredentials() async throws {
        let reply = #"{"device_id":"\#(deviceID)","connection_id":"\#(connID)","token":"synthetic-token"}"#
        let server = FakeServer([(201, [:], Data(reply.utf8))])
        let creds = try await server.client().pair(baseURL: baseURL, code: "123456", deviceName: "Synthetic iPhone")

        let request = try XCTUnwrap(server.requests.first)
        XCTAssertEqual(request.httpMethod, "POST")
        XCTAssertEqual(request.url?.path, "/api/ingest/v1/devices/pair")
        let sent = try JSONSerialization.jsonObject(with: request.httpBody!) as? [String: String]
        XCTAssertEqual(sent, ["code": "123456", "name": "Synthetic iPhone"])
        XCTAssertEqual(creds, Credentials(baseURL: baseURL, deviceID: deviceID, connectionID: connID, token: "synthetic-token"))
    }
}

final class DeviceSelfTests: XCTestCase {
    func testDeviceSelfSendsBearerAndDecodesAnchorResets() async throws {
        let reply = #"{"device_id":"d","connection_id":"c","name":"Synthetic iPhone","anchor_resets":[{"type":"*","requested_at":"2026-09-14T08:00:03Z"},{"type":"HKQuantityTypeIdentifierHeartRate","requested_at":"2026-09-14T08:00:03.250+02:00"}]}"#
        let server = FakeServer([(200, [:], Data(reply.utf8))])
        let credentials = Credentials(baseURL: baseURL, deviceID: "d", connectionID: "c", token: "synthetic-token")
        let me = try await server.client().deviceSelf(credentials: credentials)

        let request = try XCTUnwrap(server.requests.first)
        XCTAssertEqual(request.httpMethod ?? "GET", "GET")
        XCTAssertEqual(request.url?.path, "/api/ingest/v1/devices/self")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer synthetic-token")
        XCTAssertEqual(me.name, "Synthetic iPhone")
        XCTAssertEqual(me.anchorResets.map(\.type), ["*", "HKQuantityTypeIdentifierHeartRate"])
        XCTAssertEqual(me.anchorResets[0].requestedAt, ISO8601DateFormatter().date(from: "2026-09-14T08:00:03Z"))
    }
}

final class HeartbeatTests: XCTestCase {
    func testHeartbeatPostsCheckpointWithHashesOnly() async throws {
        let server = FakeServer(statuses: [204])
        let credentials = Credentials(baseURL: baseURL, deviceID: "d", connectionID: "conn_0190a1b2c3d47e8f9a0b1c2d3e4f5a6b", token: "synthetic-token")
        let now = Date(timeIntervalSince1970: 1_790_000_000)
        try await server.client().heartbeat(credentials: credentials, clientVersion: "0.0.0-test",
                                            anchorHashes: ["HKQuantityTypeIdentifierHeartRate": "7a90f1e2"], lastSuccessAt: now,
                                            failedUnits: 1, errorClass: "network", now: now)

        let request = try XCTUnwrap(server.requests.first)
        XCTAssertEqual(request.httpMethod, "POST")
        XCTAssertEqual(request.url?.path, "/api/ingest/v1/heartbeat")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer synthetic-token")
        let body = try XCTUnwrap(JSONSerialization.jsonObject(with: request.httpBody!) as? [String: Any])
        XCTAssertEqual(body["schema"] as? String, "vitamux.ingest.heartbeat/1")
        XCTAssertEqual(body["connection_id"] as? String, "conn_0190a1b2c3d47e8f9a0b1c2d3e4f5a6b")
        XCTAssertEqual(body["client"] as? [String: String], ["kind": "device", "name": "healthbridge-ios", "version": "0.0.0-test"])
        XCTAssertEqual(body["pending_failed_units"] as? Int, 1)
        XCTAssertEqual(body["last_error_class"] as? String, "network")
        let stream = try XCTUnwrap((body["streams"] as? [[String: Any]])?.first)
        XCTAssertEqual(stream["stream"] as? String, "healthkit.samples.v1")
        XCTAssertEqual((stream["checkpoint"] as? [String: Any])?["anchors"] as? [String: String], ["HKQuantityTypeIdentifierHeartRate": "7a90f1e2"])
        XCTAssertNotNil(stream["last_success_at"])
    }
}

// MARK: - TokenStore

final class TokenStoreTests: XCTestCase {
    func testRoundTripAndTokenNeverInUserDefaults() throws {
        let store = TokenStore(service: "org.vitamux.healthbridge.tests." + UUID().uuidString)
        defer { try? store.delete() }
        do {
            try store.save(credentials)
        } catch let e as KeychainError {
            throw XCTSkip("Keychain unavailable in this `swift test` environment (OSStatus \(e.status)).")
        }
        XCTAssertEqual(try store.load(), credentials)

        let anchors = freshAnchors()
        anchors.set(anchorData, for: hrType)
        XCTAssertFalse(String(describing: anchors.defaults.dictionaryRepresentation()).contains(credentials.token))
        XCTAssertFalse(String(describing: UserDefaults.standard.dictionaryRepresentation()).contains(credentials.token))

        try store.delete()
        XCTAssertNil(try store.load())
    }
}
