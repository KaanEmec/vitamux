import Foundation
import Testing
@testable import VitamuxKit

/// The fake server's Sources endpoints (FakeServer+Sources.swift) decode through the generated
/// client and follow the server's rules, so the app's UI tests stand on them.
struct SourcesFakeTests {
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

    /// The problem a call failed with (the generated client wraps it in a ClientError), or nil.
    func failure(_ work: () async throws -> Void) async -> Problem? {
        do {
            try await work()
            Issue.record("expected a failure")
            return nil
        } catch {
            return Problem(error)
        }
    }

    let withings = SourcesFixture.withingsID
    let garmin = SourcesFixture.garminID

    @Test func `connections, runs, streams, schedules and devices decode`() async throws {
        let client = try await signedIn()
        let connections = try await client.listConnections().ok.body.json.connections
        #expect(connections.map(\.provider) == ["withings", "garmin"])
        #expect(connections.filter { $0.health == .needsReauth }.count == 1)
        #expect(connections[1].upstream?.package == "garminconnect")

        let first = try await client.listConnectionRuns(path: .init(id: withings), query: .init(limit: 50)).ok.body.json
        #expect(first.value2.runs.count == 50 && first.value1.hasMore)
        let rest = try await client.listConnectionRuns(path: .init(id: withings), query: .init(limit: 50, cursor: first.value1.nextCursor)).ok.body.json
        #expect(rest.value2.runs.count == 10 && !rest.value1.hasMore)

        let streams = try await client.listConnectionStreams(path: .init(id: withings)).ok.body.json.streams
        #expect(streams.map(\.name) == ["withings.measures", "withings.sleep"])
        #expect(streams.allSatisfy { $0.hasCursor && $0.schedules.count == 2 })
        let reset = try await client.resetStreamCursor(path: .init(id: withings, stream: "withings.sleep")).ok.body.json
        #expect(!reset.hasCursor)
        #expect(await failure { _ = try await client.resetStreamCursor(path: .init(id: garmin, stream: "garmin.daily_summary")) }?.status == 409)

        let schedules = try await client.listSchedules(query: .init(connection: withings)).ok.body.json.schedules
        #expect(schedules.count == 4)
        let changed = try await client.updateSchedule(path: .init(id: schedules[0].id), body: .json(.init(intervalSeconds: 21_600, enabled: false))).ok.body.json
        #expect(changed.intervalSeconds == 21_600 && !changed.enabled)

        let devices = try await client.listSourceDevices(query: .init(include: [.records])).ok.body.json
        #expect(devices.devices.count == 7 && devices.deviceTypes.contains("scale"))
        #expect(devices.devices[0].connections?.first?.records.groups == 120)
    }

    @Test func `sync, pause, resume and delete`() async throws {
        let client = try await signedIn()
        let queued = try await client.syncConnection(path: .init(id: withings)).accepted.body.json
        #expect(queued.jobs.count == 2)
        let paused = try await client.updateConnection(path: .init(id: withings), body: .json(.init(status: .paused))).ok.body.json
        #expect(paused.status == .paused && paused.health == .paused)
        let refused = await failure { _ = try await client.syncConnection(path: .init(id: withings)) }
        #expect(refused?.status == 409)
        _ = try await client.updateConnection(path: .init(id: withings), body: .json(.init(status: .active))).ok

        _ = try await client.deleteConnection(path: .init(id: garmin), query: .init(data: .keep)).noContent
        #expect(try await client.getConnection(path: .init(id: garmin)).ok.body.json.status == .disabled)
        _ = try await client.deleteConnection(path: .init(id: withings), query: .init(data: .delete)).noContent
        #expect(try await client.listConnections().ok.body.json.connections.map(\.provider) == ["garmin"])
        let providers = try await client.listProviders().ok.body.json.providers
        #expect(providers.first { $0.code == "withings" }?.setupState == .needsAppCredentials, "no connection and no app credentials")
    }

    @Test func `backfills advance, retry, cancel and start`() async throws {
        let client = try await signedIn()
        var running = try await client.listBackfills(path: .init(id: withings)).ok.body.json.backfills[0]
        #expect(running.status == .running)
        for _ in 0..<5 where running.status == .running {
            running = try await client.listBackfills(path: .init(id: withings)).ok.body.json.backfills[0]
        }
        #expect(running.status == .done && running.unitCounts.pending == 0)

        let failed = try await client.listBackfills(path: .init(id: garmin)).ok.body.json.backfills[0]
        #expect(failed.status == .failed && failed.unitCounts.failed == 1)
        let units = try #require(try await client.getBackfill(path: .init(id: garmin, backfillId: failed.id)).ok.body.json.units)
        let unit = try #require(units.first { $0.status == .failed })
        let retried = try await client.retryBackfill(path: .init(id: garmin, backfillId: failed.id), body: .json(.init(unitStart: unit.start))).ok.body.json
        #expect(retried.status == .running && retried.unitCounts.failed == 0)
        let cancelled = try await client.cancelBackfill(path: .init(id: garmin, backfillId: failed.id)).ok.body.json
        #expect(cancelled.status == .cancelled)

        let start = Date.now.addingTimeInterval(-90 * 86_400)
        let created = try await client.createBackfill(path: .init(id: withings), body: .json(.init(stream: "withings.sleep", start: start))).accepted.body.json
        #expect(created.status == .running && created.unitCounts.pending >= 3)
        let invalid = await failure {
            _ = try await client.createBackfill(path: .init(id: withings), body: .json(.init(stream: "nope", start: start)))
        }
        #expect(invalid?.detail(for: "/stream") == "unknown stream")
    }

    @Test func `devices are typed, named and merged`() async throws {
        let client = try await signedIn()
        let duplicate = "dev_" + String(format: "%032x", 2), scale = "dev_" + String(format: "%032x", 1)
        _ = try await client.updateSourceDevice(path: .init(id: duplicate), body: .json(.init(deviceType: "scale", name: "Hall scale"))).noContent
        let moved = try await client.mergeSourceDevice(path: .init(id: duplicate), body: .json(.init(into: scale))).ok.body.json.moved
        #expect(moved.groups == 4)
        let devices = try await client.listSourceDevices(query: .init(include: [.records])).ok.body.json.devices
        #expect(devices.first { $0.id == duplicate }?.mergedInto == scale)
        #expect(devices.first { $0.id == scale }?.connections?.first?.records.groups == 124)
        let again = await failure { _ = try await client.mergeSourceDevice(path: .init(id: duplicate), body: .json(.init(into: scale))) }
        #expect(again?.status == 409)
    }

    @Test func `the Withings app wizard and an OAuth return to the app`() async throws {
        let client = try await signedIn()
        _ = try await client.deleteConnection(path: .init(id: withings), query: .init(data: .delete)).noContent
        let wrong = try await client.putProviderAppCredentials(path: .init(provider: "withings"), body: .json(.init(clientId: "synthetic-client-id", clientSecret: "synthetic-wrong"))).ok.body.json
        #expect(wrong.setupState == .ready && wrong.appCredentials?.set == true)
        #expect(try await client.verifyProviderAppCredentials(path: .init(provider: "withings")).ok.body.json.result == .invalid)
        _ = try await client.putProviderAppCredentials(path: .init(provider: "withings"), body: .json(.init(clientId: "synthetic-client-id", clientSecret: SourcesFixture.appSecret))).ok
        #expect(try await client.verifyProviderAppCredentials(path: .init(provider: "withings")).ok.body.json.result == .valid)

        // A browser return never comes back to the app.
        guard case .AuthRedirect(let browser) = try await client.beginProviderAuth(path: .init(provider: "withings")).ok.body.json else {
            Issue.record("expected a redirect")
            return
        }
        #expect(fake.authorize(URL(string: browser.redirectUrl)!).absoluteString == "vitamux://connections?auth_error=invalid_state&provider=unknown")

        guard case .AuthRedirect(let app) = try await client.beginProviderAuth(path: .init(provider: "withings"), body: .json(.init(_return: .app))).ok.body.json else {
            Issue.record("expected a redirect")
            return
        }
        let start = try #require(URL(string: app.redirectUrl))
        #expect(start.path == "/oauth/withings/start")
        #expect(fake.authorize(start).absoluteString == "vitamux://connections?connected=withings")
        #expect(fake.authorize(start).absoluteString == "vitamux://connections?auth_error=invalid_state&provider=withings", "a ticket works once")
        let connections = try await client.listConnections().ok.body.json.connections
        #expect(connections.contains { $0.provider == "withings" && $0.status == .active })
    }

    @Test func `prompt steps with an MFA code, and their failures`() async throws {
        let client = try await signedIn()
        func begin(_ provider: String) async throws -> Components.Schemas.AuthPromptStep {
            guard case .AuthPromptStep(let step) = try await client.beginProviderAuth(path: .init(provider: provider), body: .json(.init(_return: .app))).ok.body.json else {
                throw Problem(title: "expected a prompt")
            }
            return step
        }
        func send(_ provider: String, _ step: Components.Schemas.AuthPromptStep, _ values: [String: String]) async throws -> Operations.ContinueProviderAuth.Output.Ok.Body.JsonPayload {
            try await client.continueProviderAuth(path: .init(provider: provider), body: .json(.init(state: step.state, values: .init(additionalProperties: values)))).ok.body.json
        }
        let login = [ "email": SourcesFixture.Login.email, "password": SourcesFixture.Login.password]

        // WHOOP's sidecar is off until one probe finds it.
        let off = await failure { _ = try await begin("whoop") }
        #expect(off?.status == 503)
        #expect(try await client.probeProvider(path: .init(provider: "whoop")).ok.body.json.setupState == .needsSidecar)
        #expect(try await client.probeProvider(path: .init(provider: "whoop")).ok.body.json.setupState == .ready)

        let first = try await begin("whoop")
        #expect(first.prompt.fields.map(\.kind) == [.text, .password])
        guard case .AuthPromptStep(let code) = try await send("whoop", first, login) else { throw Problem(title: "expected the code step") }
        #expect(code.prompt.fields.map(\.kind) == [.code])
        let replay = await failure { _ = try await send("whoop", first, login) }
        #expect(replay?.detail(for: "/state") != nil, "a state works once")
        guard case .AuthDone(let done) = try await send("whoop", code, ["code": SourcesFixture.Login.code]) else { throw Problem(title: "expected done") }
        let whoop = try await client.getConnection(path: .init(id: done.connectionId)).ok.body.json
        #expect(whoop.status == .paused && whoop.official == false, "a new unofficial connection starts paused")

        let wrongCode = try await begin("garmin")
        guard case .AuthPromptStep(let step) = try await send("garmin", wrongCode, login) else { throw Problem(title: "expected the code step") }
        let refused = await failure { _ = try await send("garmin", step, ["code": "111111"]) }
        #expect(refused?.status == 422)
        let limited = await failure {
            _ = try await send("garmin", try await begin("garmin"), ["email": SourcesFixture.Login.email, "password": SourcesFixture.Login.limited])
        }
        #expect(limited?.status == 429 && limited?.retryAfter == .seconds(120))

        guard case .AuthPromptStep(let reauth) = try await client.beginConnectionAuth(path: .init(id: garmin), body: .json(.init(_return: .app))).ok.body.json else {
            throw Problem(title: "expected a prompt")
        }
        guard case .AuthPromptStep(let reauthCode) = try await send("garmin", reauth, login) else { throw Problem(title: "expected the code step") }
        guard case .AuthDone(let reauthorized) = try await send("garmin", reauthCode, ["code": SourcesFixture.Login.code]) else { throw Problem(title: "expected done") }
        #expect(reauthorized.connectionId == garmin)
        #expect(try await client.getConnection(path: .init(id: garmin)).ok.body.json.health == .ok)
    }
}
