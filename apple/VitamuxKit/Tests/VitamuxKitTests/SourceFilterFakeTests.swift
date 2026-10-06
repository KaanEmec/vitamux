import Foundation
import Testing
@testable import VitamuxKit

/// The fake server's source filter (FakeServer+SourceFilter.swift) decodes through the generated
/// client and follows internal/sourcefilter's rules, so the app's Apple Health › Sources UI tests
/// stand on it.
struct SourceFilterFakeTests {
    typealias Scenario = FakeServer.SourceFilterScenario
    let fake = FakeServer()
    let sessions = SessionStore.inMemory()
    let deviceID = FakeServer.AppleHealth.deviceID

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

    func failure(_ call: () async throws -> Void) async -> Problem? {
        do {
            try await call()
            return nil
        } catch {
            return Problem(error)
        }
    }

    /// A device-token request, as HealthBridgeKit's `IngestClient` sends it.
    func ingest(_ method: String, _ path: String, body: [String: Any]? = nil) async throws -> (Int, [String: Any]) {
        var request = URLRequest(url: fake.profile.baseURL.appending(path: path))
        request.httpMethod = method
        request.setValue("Bearer \(FakeServer.AppleHealth.deviceToken)", forHTTPHeaderField: "Authorization")
        if let body { request.httpBody = try JSONSerialization.data(withJSONObject: body) }
        let (data, response) = try await fake.urlSession.data(for: request)
        let object = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
        return ((response as! HTTPURLResponse).statusCode, object)
    }

    func set(_ client: Client, _ origins: [Components.Schemas.SourceFilterChoice], version: Int?) async throws -> Components.Schemas.SourceFilterView {
        try await client.setDeviceSourceFilter(path: .init(id: deviceID), body: .json(.init(version: version, origins: origins))).ok.body.json
    }

    @Test func `the paired iPhone lists the band ignored by default`() async throws {
        let client = try await signedIn()
        let view = try await client.getDeviceSourceFilter(path: .init(id: deviceID)).ok.body.json
        #expect(view.version == 1 && view.sourcesReportedAt != nil)
        #expect(view.origins.map(\.bundleId) == [Scenario.watch, Scenario.band, Scenario.scale])
        let band = try #require(view.origins.first { $0.bundleId == Scenario.band })
        #expect(band.mode == .ignore && !band.explicit && band.defaultMode == .ignore && band.defaultReason == .directConnection)
        #expect(band.reasonProviderName == Scenario.bandProviderName && band.classification == .relayed)
        #expect(band.ignoredRecords == Scenario.bandIgnoredRecords && band.writes.count == 2)
        let watch = try #require(view.origins.first { $0.bundleId == Scenario.watch })
        #expect(watch.mode == .take && watch.defaultReason == .native && watch.classification == .native)
        let scale = try #require(view.origins.first { $0.bundleId == Scenario.scale })
        #expect(scale.mode == .take && scale.defaultReason == nil && scale.writes.map(\._type) == ["HKQuantityTypeIdentifierBodyMass"])
    }

    @Test func `taking the band again asks the device to pull its types`() async throws {
        let client = try await signedIn()
        let view = try await set(client, [.init(bundleId: Scenario.band, name: Scenario.bandName, mode: .take)], version: 1)
        let band = try #require(view.origins.first { $0.bundleId == Scenario.band })
        #expect(view.version == 2 && band.mode == .take && band.explicit && band.ignoredRecords == 0)

        let (status, me) = try await ingest("GET", "api/ingest/v1/devices/self")
        #expect(status == 200)
        let filter = try #require(me["source_filter"] as? [String: Any])
        #expect(filter["version"] as? Int == 2)
        #expect((filter["origins"] as? [[String: Any]])?.first?["mode"] as? String == "take")
        #expect((filter["default_ignore"] as? [[String: String]])?.first?["provider_name"] == Scenario.bandProviderName)
        let resets = try #require(me["anchor_resets"] as? [[String: Any]])
        #expect(Set(resets.compactMap { $0["type"] as? String }) == ["HKQuantityTypeIdentifierHeartRate", "HKQuantityTypeIdentifierHeartRateVariabilitySDNN"])
    }

    @Test func `a stale version answers 409 and an invalid choice 422`() async throws {
        let client = try await signedIn()
        _ = try await set(client, [.init(bundleId: Scenario.scale, mode: .ignore)], version: 1)
        #expect(await failure { _ = try await set(client, [], version: 1) }?.status == 409)
        #expect(await failure { _ = try await set(client, [.init(bundleId: Scenario.scale, mode: .perType, types: [])], version: nil) }?.status == 422)
        #expect(fake.sourceFilterVersion == 2)
        // Per type: only the listed types are taken; leaving an app out returns it to its default.
        let view = try await set(client, [.init(bundleId: Scenario.band, mode: .perType, types: ["HKQuantityTypeIdentifierHeartRate"])], version: 2)
        let band = try #require(view.origins.first { $0.bundleId == Scenario.band })
        #expect(band.mode == .perType && band.types == ["HKQuantityTypeIdentifierHeartRate"])
        #expect(view.origins.first { $0.bundleId == Scenario.scale }?.explicit == false)
    }

    @Test func `the phone's report replaces what each app writes`() async throws {
        let client = try await signedIn()
        let report: [String: Any] = ["sources": [
            ["bundle_id": "com.example.synthetic.ring", "name": "Synthetic Ring",
             "types": [["type": "HKQuantityTypeIdentifierHeartRate", "last_sample_at": "2026-09-30T06:00:00Z"]]],
        ]]
        #expect(try await ingest("PUT", "api/ingest/v1/devices/self/sources", body: report).0 == 204)
        let view = try await client.getDeviceSourceFilter(path: .init(id: deviceID)).ok.body.json
        let ring = try #require(view.origins.first { $0.bundleId == "com.example.synthetic.ring" })
        #expect(ring.name == "Synthetic Ring" && ring.mode == .take && ring.originId == nil && ring.writes.count == 1)
        #expect(view.origins.first { $0.bundleId == Scenario.band }?.writes.isEmpty == true, "seen in data, not reported any more")
        #expect(try await ingest("PUT", "api/ingest/v1/devices/self/sources", body: ["sources": "x"]).0 == 422)
    }

    @Test func `the inventory lists records held raw only when asked`() async throws {
        let client = try await signedIn()
        #expect(try await client.getInventory().ok.body.json.ignored == nil)
        let ignored = try #require(try await client.getInventory(query: .init(includeIgnored: true)).ok.body.json.ignored)
        #expect(ignored.count == 1)
        #expect(ignored.first?.origin.key == Scenario.band && ignored.first?.records == Scenario.bandIgnoredRecords)

        _ = try await set(client, [.init(bundleId: Scenario.band, mode: .take)], version: nil)
        #expect(try await client.getInventory(query: .init(includeIgnored: true)).ok.body.json.ignored?.isEmpty == true)
    }

    @Test func `an unknown device answers 404`() async throws {
        let client = try await signedIn()
        #expect(await failure { _ = try await client.getDeviceSourceFilter(path: .init(id: "00000000-0000-4000-8000-0000000000ff")).ok }?.status == 404)
    }
}
