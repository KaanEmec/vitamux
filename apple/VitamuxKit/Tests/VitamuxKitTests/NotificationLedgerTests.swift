import Foundation
import Testing
@testable import VitamuxKit

/// The local notifications' transitions (J22.21): once per condition, updated on a new state,
/// withdrawn when it clears, and untouched when its source could not be read. Synthetic items.
struct NotificationLedgerTests {
    typealias Item = NotificationLedger.Item

    static let connections = "notifications.connectionAttention"
    static let backup = "notifications.staleBackup"
    static let all: Set<String> = [connections, backup]

    func garmin(_ state: String) -> Item {
        Item(id: "connection:conn_2", category: Self.connections, state: state, title: "Garmin Connect",
             body: "Open the connection.", link: "vitamux://connections/conn_2")
    }

    let staleBackup = Item(id: "backup", category: backup, state: "2026-09-26T03:00:00Z", title: "The last backup is old",
                           body: "Backups shows when it last ran.", link: "vitamux://settings/backups")

    @Test func `a new condition notifies once`() {
        var ledger = NotificationLedger()
        let first = ledger.update(current: [garmin("needs_reauth"), staleBackup], checked: Self.all)
        #expect(first.post.map(\.id) == ["connection:conn_2", "backup"])
        #expect(first.remove.isEmpty)
        let again = ledger.update(current: [garmin("needs_reauth"), staleBackup], checked: Self.all)
        #expect(again == .init(), "the same state never repeats")
    }

    @Test func `a new state updates the same notification`() {
        var ledger = NotificationLedger()
        _ = ledger.update(current: [garmin("needs_reauth")], checked: Self.all)
        let changed = ledger.update(current: [garmin("failing")], checked: Self.all)
        #expect(changed.post == [garmin("failing")], "posted under the same identifier, so it replaces the old one")
        #expect(changed.remove.isEmpty)
    }

    @Test func `a cleared condition is withdrawn and notifies again when it returns`() {
        var ledger = NotificationLedger()
        _ = ledger.update(current: [garmin("needs_reauth"), staleBackup], checked: Self.all)
        let fixed = ledger.update(current: [staleBackup], checked: Self.all)
        #expect(fixed.post.isEmpty)
        #expect(fixed.remove == ["connection:conn_2"])
        #expect(ledger.notified == ["backup"])
        let back = ledger.update(current: [garmin("needs_reauth"), staleBackup], checked: Self.all)
        #expect(back.post == [garmin("needs_reauth")])
    }

    @Test func `an unread source keeps its items`() {
        var ledger = NotificationLedger()
        _ = ledger.update(current: [garmin("needs_reauth"), staleBackup], checked: Self.all)
        // GET /connections failed: its category is not checked, so nothing about it changes.
        let partial = ledger.update(current: [staleBackup], checked: [Self.backup])
        #expect(partial == .init())
        #expect(ledger.notified == ["connection:conn_2", "backup"])
        // Items of an unchecked category are ignored, not posted.
        var fresh = NotificationLedger()
        #expect(fresh.update(current: [garmin("stale")], checked: [Self.backup]) == .init())
    }

    @Test func `a category turned off is withdrawn and notifies when turned on`() {
        var ledger = NotificationLedger()
        _ = ledger.update(current: [garmin("needs_reauth"), staleBackup], checked: Self.all)
        let off = ledger.update(current: [staleBackup], checked: Self.all)
        #expect(off.remove == ["connection:conn_2"])
        let on = ledger.update(current: [garmin("needs_reauth"), staleBackup], checked: Self.all)
        #expect(on.post.map(\.id) == ["connection:conn_2"])
    }

    @Test func `duplicate items post once`() {
        var ledger = NotificationLedger()
        let changes = ledger.update(current: [garmin("failing"), garmin("failing")], checked: Self.all)
        #expect(changes.post.count == 1)
    }

    @Test func `the ledger survives a round trip`() throws {
        var ledger = NotificationLedger()
        _ = ledger.update(current: [garmin("needs_reauth"), staleBackup], checked: Self.all)
        var stored = try JSONDecoder().decode(NotificationLedger.self, from: JSONEncoder().encode(ledger))
        #expect(stored == ledger)
        #expect(stored.update(current: [garmin("needs_reauth"), staleBackup], checked: Self.all) == .init())
    }
}

/// The fake's document waiting for review (`-uitest-lab-review`), listed by `GET /documents`.
struct NotificationFakeTests {
    @Test func `the review document is listed once`() async throws {
        let fake = FakeServer()
        let sessions = SessionStore.inMemory()
        let client = fake.client(sessions: sessions)
        let body = Components.Schemas.LoginRequest(
            username: FakeServer.Owner.username, password: FakeServer.Owner.password, client: .app, deviceName: "Test iPhone"
        )
        guard case .AppSession(let session) = try await client.login(body: .json(body)).ok.body.json else {
            Issue.record("expected an app session")
            return
        }
        try sessions.save(session, for: fake.profile)
        fake.addDocumentForReview()
        fake.addDocumentForReview()
        let documents = try await client.listDocuments(query: .init(limit: 500)).ok.body.json.value2.documents
        let waiting = documents.filter { $0.status == .needsReview }
        #expect(waiting.map(\.id) == [FakeServer.reviewDocumentID])
        #expect(documents.count == 2, "the seeded confirmed document stays")
    }
}
