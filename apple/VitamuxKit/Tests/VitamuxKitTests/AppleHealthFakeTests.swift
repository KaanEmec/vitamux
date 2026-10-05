import Foundation
import Testing
@testable import VitamuxKit

/// The fake server's Apple Health endpoints (FakeServer+AppleHealth.swift) decode through the
/// generated client, and its device-token routes behave as the server's, so the app's UI tests
/// for pairing, resets and revocation stand on them.
struct AppleHealthFakeTests {
    let fake = FakeServer()
    let sessions = SessionStore.inMemory()

    func signedIn() async throws -> Client {
        let client = fake.client(sessions: sessions)
        let body = Components.Schemas.LoginRequest(username: FakeServer.Owner.username, password: FakeServer.Owner.password, client: .app, deviceName: "Test iPhone")
        guard case .AppSession(let session) = try await client.login(body: .json(body)).ok.body.json else {
            Issue.record("expected an app session")
            return client
        }
        try sessions.save(session, for: fake.profile)
        return client
    }

    /// The problem a failing call threw, or nil when it succeeded.
    func failure(_ call: () async throws -> Void) async -> Problem? {
        do {
            try await call()
            return nil
        } catch {
            return Problem(error)
        }
    }

    /// A device-token request, as HealthBridgeKit's `IngestClient` sends it.
    func ingest(_ method: String, _ path: String, token: String?, body: [String: Any]? = nil) async throws -> (Int, [String: Any]) {
        var request = URLRequest(url: fake.profile.baseURL.appending(path: path))
        request.httpMethod = method
        if let token { request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        if let body { request.httpBody = try JSONSerialization.data(withJSONObject: body) }
        let (data, response) = try await fake.urlSession.data(for: request)
        let object = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
        return ((response as! HTTPURLResponse).statusCode, object)
    }

    @Test func `a code the app creates pairs once`() async throws {
        let client = try await signedIn()
        let code = try await client.createPairingCode().created.body.json
        #expect(code.url == nil && code.qrPayload == nil, "an app session gets no QR")
        #expect(code.code.count == 9)

        let pair = { try await self.ingest("POST", "api/ingest/v1/devices/pair", token: nil, body: ["code": code.code, "name": "Test iPhone"]) }
        let (status, paired) = try await pair()
        #expect(status == 201)
        let token = try #require(paired["token"] as? String)
        #expect(try await pair().0 == 422, "single use")

        let (selfStatus, me) = try await ingest("GET", "api/ingest/v1/devices/self", token: token)
        #expect(selfStatus == 200 && me["name"] as? String == "Test iPhone")
        let devices = try await client.listDevices().ok.body.json.devices
        #expect(devices.first?.id == paired["device_id"] as? String, "newest first")
        #expect(devices.contains { $0.id == FakeServer.AppleHealth.deviceID && $0.possiblyDenied == [FakeServer.AppleHealth.possiblyDenied] })
    }

    @Test func `the manual code pairs any number of times`() async throws {
        for _ in 0..<2 {
            let (status, _) = try await ingest("POST", "api/ingest/v1/devices/pair", token: nil, body: ["code": FakeServer.AppleHealth.manualCode.lowercased(), "name": "Sync-only iPhone"])
            #expect(status == 201)
        }
        #expect(try await ingest("POST", "api/ingest/v1/devices/pair", token: nil, body: ["code": "0000-0000", "name": "x"]).0 == 422)
    }

    @Test func `uploads and heartbeats need a live device token`() async throws {
        let token = FakeServer.AppleHealth.deviceToken
        #expect(try await ingest("POST", "api/ingest/v1/batches", token: token).0 == 202)
        #expect(try await ingest("POST", "api/ingest/v1/batches", token: "vmx_ses_not_a_device").0 == 401)
        let heartbeat: [String: Any] = ["streams": [["stream": "healthkit.samples.v1", "checkpoint": ["anchors": ["HKQuantityTypeIdentifierHeartRate": "none"]]]]]
        #expect(try await ingest("POST", "api/ingest/v1/heartbeat", token: token, body: heartbeat).0 == 204)
        #expect(fake.acceptedBatches == 1)

        let client = try await signedIn()
        let device = try #require(try await client.listDevices().ok.body.json.devices.first { $0.id == FakeServer.AppleHealth.deviceID })
        #expect(device.types == ["HKQuantityTypeIdentifierHeartRate"])
        #expect(device.lastSyncAt != nil)
    }

    @Test func `an anchor reset reaches devices self`() async throws {
        let client = try await signedIn()
        _ = try await client.requestDeviceAnchorReset(path: .init(id: FakeServer.AppleHealth.deviceID), body: .json(.init(types: ["HKQuantityTypeIdentifierStepCount"]))).noContent
        fake.requestAnchorReset(deviceID: FakeServer.AppleHealth.deviceID)
        let (_, me) = try await ingest("GET", "api/ingest/v1/devices/self", token: FakeServer.AppleHealth.deviceToken)
        let resets = try #require(me["anchor_resets"] as? [[String: Any]])
        #expect(Set(resets.compactMap { $0["type"] as? String }) == ["*", "HKQuantityTypeIdentifierStepCount"])
        let unknown = await failure { _ = try await client.requestDeviceAnchorReset(path: .init(id: "00000000-0000-4000-8000-0000000000ff")).noContent }
        #expect(unknown?.status == 404)
    }

    @Test func `a revoked token answers 401 and the device lists as revoked`() async throws {
        let client = try await signedIn()
        _ = try await client.revokeDevice(path: .init(id: FakeServer.AppleHealth.deviceID)).noContent
        #expect(try await ingest("GET", "api/ingest/v1/devices/self", token: FakeServer.AppleHealth.deviceToken).0 == 401)
        let device = try #require(try await client.listDevices().ok.body.json.devices.first)
        #expect(device.revokedAt != nil && device.possiblyDenied.isEmpty)
    }

    @Test func `device routes need an app session`() async throws {
        let client = fake.client(sessions: sessions)
        #expect(await failure { _ = try await client.listDevices().ok }?.status == 401)
    }
}
