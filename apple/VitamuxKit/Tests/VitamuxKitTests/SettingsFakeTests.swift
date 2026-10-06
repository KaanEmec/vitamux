import Foundation
import OpenAPIRuntime
import Testing
@testable import VitamuxKit

/// The fake server's Settings endpoints (FakeServer+Settings.swift) decode through the generated
/// client and change state as the server does, so the app's Settings UI tests stand on them.
struct SettingsFakeTests {
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

    /// The `Problem` a call fails with (the generated client wraps it in a `ClientError`).
    func failure(_ call: () async throws -> some Any) async -> Problem? {
        do {
            _ = try await call()
            Issue.record("expected the call to fail")
            return nil
        } catch {
            return Problem(error)
        }
    }

    @Test func `timezone periods are added, changed and removed, each ending where the next starts`() async throws {
        let client = try await signedIn()
        let start = try Date("2026-06-01T10:00:00Z", strategy: .iso8601)
        let added = try await client.createTimezonePeriod(body: .json(.init(tz: "Asia/Tokyo", validFrom: start))).created.body.json
        var periods = try await client.listTimezonePeriods().ok.body.json.timezonePeriods
        #expect(periods.map(\.tz) == ["America/New_York", "Europe/Berlin", "Asia/Tokyo"])
        #expect(periods[1].validTo == start)
        #expect(periods[2].validTo == nil)

        _ = try await client.updateTimezonePeriod(path: .init(id: added.id), body: .json(.init(tz: "Asia/Seoul", validFrom: start))).ok
        _ = try await client.deleteTimezonePeriod(path: .init(id: periods[0].id)).noContent
        periods = try await client.listTimezonePeriods().ok.body.json.timezonePeriods
        #expect(periods.map(\.tz) == ["Europe/Berlin", "Asia/Seoul"])

        let problem = await failure {
            try await client.createTimezonePeriod(body: .json(.init(tz: "Mars/Olympus", validFrom: start)))
        }
        #expect(problem?.detail(for: "/tz") != nil)
    }

    @Test func `settings patch merges, raw days per provider`() async throws {
        let client = try await signedIn()
        let before = try await client.getSettings().ok.body.json
        #expect(before.retention_rawDays?.additionalProperties == ["withings": 90])
        #expect(before.sources_priority == ["whoop"])
        let patch = Components.Schemas.Settings(
            documents_externalAi_gemini_enabled: true, documents_retentionDays: 30,
            retention_rawDays: .init(additionalProperties: ["garmin": 14])
        )
        let after = try await client.updateSettings(body: .json(patch)).ok.body.json
        #expect(after.documents_externalAi_gemini_enabled == true)
        #expect(after.documents_retentionDays == 30)
        #expect(after.retention_rawDays?.additionalProperties == ["withings": 90, "garmin": 14])
        let problem = await failure {
            try await client.updateSettings(body: .json(.init(retention_idempotencyKeyDays: 3)))
        }
        #expect(problem?.detail(for: "/retention.idempotency_key_days") != nil)
    }

    @Test func `devices pair, resync, revoke, and origins classify`() async throws {
        let client = try await signedIn()
        let code = try await client.createPairingCode().created.body.json
        #expect(code.code.wholeMatch(of: /[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}/) != nil)
        #expect(code.qrPayload?.contains(code.code) == true)
        #expect(code.expiresAt > .now)

        let devices = try await client.listDevices().ok.body.json.devices
        let phone = try #require(devices.first)
        #expect(phone.possiblyDenied == ["HKCategoryTypeIdentifierSleepAnalysis"])
        _ = try await client.requestDeviceAnchorReset(path: .init(id: phone.id), body: .json(.init(types: ["HKQuantityTypeIdentifierHeartRate"]))).noContent
        _ = try await client.revokeDevice(path: .init(id: phone.id)).noContent
        let after = try #require(try await client.listDevices().ok.body.json.devices.first)
        #expect(after.anchorResets.map(\._type) == ["HKQuantityTypeIdentifierHeartRate"])
        #expect(after.revokedAt != nil)
        #expect(after.possiblyDenied.isEmpty)

        let origins = try await client.listOrigins().ok.body.json
        #expect(origins.relayTargets.map(\.code).contains("garmin"))
        let relayed = try #require(origins.origins.first { $0.relayedProvider == "garmin" })
        _ = try await client.classifyOrigin(path: .init(id: relayed.id), body: .json(.init(relayedProvider: nil))).noContent
        #expect(try await client.listOrigins().ok.body.json.origins.first { $0.id == relayed.id }?.relayedProvider == nil)
    }

    @Test func `an API key's secret comes once and revoke keeps the row`() async throws {
        let client = try await signedIn()
        let created = try await client.createAPIKey(body: .json(.init(name: "test key", scopes: [.read_colon_health, .admin]))).created.body.json
        #expect(created.value2.token == SettingsFixture.apiKeyToken)
        let keys = try await client.listAPIKeys().ok.body.json.apiKeys
        #expect(keys.count == 2)
        _ = try await client.revokeAPIKey(path: .init(id: created.value1.id)).noContent
        #expect(try await client.listAPIKeys().ok.body.json.apiKeys.first { $0.id == created.value1.id }?.revokedAt != nil)
    }

    @Test func `TOTP enrols, confirms with recovery codes, and turns off with password and code`() async throws {
        let client = try await signedIn()
        let enrolment = try await client.enrollTOTP().ok.body.json
        #expect(enrolment.otpauthUri.hasPrefix("otpauth://totp/"))
        let wrong = await failure { try await client.confirmTOTP(body: .json(.init(code: "000000"))) }
        #expect(wrong?.detail(for: "/code") != nil)
        let codes = try await client.confirmTOTP(body: .json(.init(code: FakeServer.Owner.totp))).ok.body.json.recoveryCodes
        #expect(codes.count == 3)
        #expect(fake.totpEnabled)
        #expect(try await client.getSession().ok.body.json.user.totpEnabled)

        let forbidden = await failure {
            try await client.disableTOTP(body: .json(.init(password: "wrong", totpCode: FakeServer.Owner.totp)))
        }
        #expect(forbidden?.status == 403)
        _ = try await client.disableTOTP(body: .json(.init(password: FakeServer.Owner.password, totpCode: FakeServer.Owner.totp))).noContent
        #expect(!fake.totpEnabled)
    }

    @Test func `sessions list this app, end others, and a password change ends every other`() async throws {
        let client = try await signedIn()
        var sessions = try await client.listSessions().ok.body.json.sessions
        #expect(sessions.count == 3)
        #expect(sessions.first?.current == true && sessions.first?.name == "Test iPhone")
        _ = try await client.revokeSession(path: .init(id: sessions[1].id)).noContent
        sessions = try await client.listSessions().ok.body.json.sessions
        #expect(sessions.count == 2)

        let wrong = await failure {
            try await client.changePassword(body: .json(.init(currentPassword: "nope", newPassword: "synthetic-new-password")))
        }
        #expect(wrong?.detail(for: "/current_password") != nil)
        _ = try await client.changePassword(body: .json(.init(currentPassword: FakeServer.Owner.password, newPassword: "synthetic-new-password"))).noContent
        #expect(try await client.listSessions().ok.body.json.sessions.count == 1)
    }

    @Test func `app credentials and sidecars need a second confirmation while in use`() async throws {
        let client = try await signedIn()
        let withings = try #require(try await client.listProviders().ok.body.json.providers.first { $0.code == "withings" })
        #expect(withings.appCredentials?.set == true)
        let inUse = await failure { try await client.deleteProviderAppCredentials(path: .init(provider: "withings")) }
        #expect(inUse?.status == 409)
        _ = try await client.deleteProviderAppCredentials(path: .init(provider: "withings"), query: .init(confirm: true)).noContent
        let put = try await client.putProviderAppCredentials(path: .init(provider: "withings"), body: .json(.init(clientId: "synthetic-id-2", clientSecret: "synthetic-secret"))).ok.body.json
        #expect(put.appCredentials?.clientId == "synthetic-id-2")
        #expect(try await client.verifyProviderAppCredentials(path: .init(provider: "withings")).ok.body.json.result == .valid)

        let created = try await client.createSidecar(body: .json(.init(name: "synthetic_scale", url: "http://scale-sidecar:8080"))).created.body.json
        #expect(created.secret.count == 64)
        let refused = await failure {
            try await client.createSidecar(body: .json(.init(name: "public", url: "https://example.org")))
        }
        #expect(refused?.detail(for: "/url") != nil)
        _ = try await client.deleteSidecar(path: .init(name: "synthetic_scale")).noContent
        let ring = await failure { try await client.deleteSidecar(path: .init(name: "synthetic_ring")) }
        #expect(ring?.status == 409)
        _ = try await client.deleteSidecar(path: .init(name: "synthetic_ring"), query: .init(confirm: true)).noContent
        #expect(try await client.listSidecars().ok.body.json.sidecars.map(\.name) == ["garmin"])
    }

    @Test func `an export runs, finishes and downloads once with its token`() async throws {
        let client = try await signedIn()
        let started = try await client.createExport(body: .json(.init(format: .csv, includeRaw: true))).accepted.body.json
        #expect(started.status == .queued)
        #expect(try await client.getExport(path: .init(id: started.id)).ok.body.json.status == .running)
        let done = try await client.getExport(path: .init(id: started.id)).ok.body.json
        #expect(done.status == .done && done.format == .csv && done.includeRaw == true)
        let link = try #require(done.downloadUrl.flatMap { URLComponents(string: $0) })
        let token = try #require(link.queryItems?.first { $0.name == "token" }?.value)
        let body = try await client.downloadExport(path: .init(id: started.id), query: .init(token: token)).ok.body.applicationZip
        #expect(try await Data(collecting: body, upTo: 1024) == SettingsFixture.exportFile)
    }
}
