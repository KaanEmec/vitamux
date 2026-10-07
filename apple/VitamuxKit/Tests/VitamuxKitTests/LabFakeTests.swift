import Foundation
import OpenAPIRuntime
import Testing
@testable import VitamuxKit

/// The fake server's lab endpoints (FakeServer+Lab.swift) through the generated client: the
/// multipart upload, extraction with consent, the review states, confirm and unconfirm, deletion
/// and result history, so the app's UI tests stand on them. Everything is synthetic.
struct LabFakeTests {
    typealias Row = Components.Schemas.ExtractionRowPatch

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

    func failure(_ work: () async throws -> Void) async -> Problem? {
        do {
            try await work()
            return nil
        } catch {
            return Problem(error)
        }
    }

    func upload(_ client: Client, _ data: Data, name: String = "synthetic.pdf") async throws -> Operations.UploadDocument.Output {
        let part = Operations.UploadDocument.Input.Body.MultipartFormPayload.file(.init(payload: .init(body: HTTPBody(data)), filename: name))
        return try await client.uploadDocument(body: .multipartForm([part]))
    }

    /// Uploads the synthetic PDF, extracts it with the built-in extractor and polls until it succeeds.
    func reviewable(_ client: Client) async throws -> (Components.Schemas.Document, Components.Schemas.Extraction) {
        let doc = try await upload(client, FakeServer.syntheticLabPDF).created.body.json
        let run = try await client.createExtraction(path: .init(id: doc.id), body: .json(.init(provider: .fake))).accepted.body.json
        #expect(run.status == .queued)
        for _ in 0..<3 {
            let runs = try await client.listExtractions(path: .init(id: doc.id)).ok.body.json.extractions
            if runs.first?.status == .succeeded { break }
        }
        let full = try await client.getExtraction(path: .init(id: run.id)).ok.body.json
        return (doc, full)
    }

    @Test func `a multipart upload is stored once and keeps its filename`() async throws {
        let client = try await signedIn()
        let doc = try await upload(client, FakeServer.syntheticLabPDF, name: "report.pdf").created.body.json
        #expect(doc.status == .uploaded && doc.filename == "report.pdf" && doc.sizeBytes == FakeServer.syntheticLabPDF.count)
        let again = try await upload(client, FakeServer.syntheticLabPDF, name: "copy.pdf").ok.body.json
        #expect(again.id == doc.id, "the same content links to the stored document")
        let listed = try await client.listDocuments().ok.body.json.value2.documents
        #expect(listed.map(\.id).contains(doc.id) && listed.count == 2, "the seeded document and the upload")

        let file = try await client.getDocumentFile(path: .init(id: doc.id)).ok.body.pdf
        let bytes = try await Data(collecting: file, upTo: 1 << 20)
        #expect(bytes == FakeServer.syntheticLabPDF)
    }

    @Test func `uploads that are not a PDF or too large are refused`() async throws {
        let client = try await signedIn()
        let notPDF = await failure { _ = try await upload(client, Data("synthetic text".utf8), name: "notes.txt") }
        #expect(notPDF?.status == 422 && notPDF?.fieldErrors.first?.detail == "not_pdf")
        let large = Data("%PDF-".utf8) + Data(count: 20 * 1024 * 1024)
        let tooLarge = await failure { _ = try await upload(client, large) }
        #expect(tooLarge?.status == 413)
    }

    @Test func `an external extractor needs consent naming its model`() async throws {
        let client = try await signedIn()
        let doc = try await upload(client, FakeServer.syntheticLabPDF).created.body.json
        let missing = await failure {
            _ = try await client.createExtraction(path: .init(id: doc.id), body: .json(.init(provider: .gemini)))
        }
        #expect(missing?.status == 409 && missing?.code == "consent_required")
        let wrong = await failure {
            let consent = Components.Schemas.ExtractionConsent(provider: "gemini", model: "another-model", acknowledgedAt: .now)
            _ = try await client.createExtraction(path: .init(id: doc.id), body: .json(.init(provider: .gemini, consent: consent)))
        }
        #expect(wrong?.code == "consent_required")
        let disabled = await failure { _ = try await client.createExtraction(path: .init(id: doc.id), body: .json(.init(provider: .openai))) }
        #expect(disabled?.status == 403)

        let consent = Components.Schemas.ExtractionConsent(provider: "gemini", model: LabFixture.geminiModel, acknowledgedAt: .now)
        let run = try await client.createExtraction(path: .init(id: doc.id), body: .json(.init(provider: .gemini, consent: consent))).accepted.body.json
        #expect(run.external && run.consent?.model == LabFixture.geminiModel)
        let busy = await failure { _ = try await client.createExtraction(path: .init(id: doc.id), body: .json(.init(provider: .fake))) }
        #expect(busy?.code == "conflict", "one active run per document")
    }

    @Test func `a run is reviewed row by row, confirmed, unconfirmed and confirmed again`() async throws {
        let client = try await signedIn()
        let (doc, run) = try await reviewable(client)
        let rows = try #require(run.rows)
        #expect(rows.count == 4 && rows.allSatisfy { $0.reviewStatus == .pending })
        #expect(rows[0].bbox != nil && rows[2].validation.contains("unit_missing") && rows[3].validation.contains("date_missing"))

        func patch(_ row: Int, _ body: Row) async throws -> Components.Schemas.ExtractionRow {
            try await client.updateExtractionRow(path: .init(id: run.id, row: String(row)), body: .json(body)).ok.body.json
        }
        #expect(try await patch(0, Row(review: .accept)).reviewStatus == .accepted)
        let edited = try await patch(1, Row(review: .accept, unitText: "µmol/L"))
        #expect(edited.reviewStatus == .edited && edited.unitText == "µmol/L" && edited.edits.first?.action == .edit)
        #expect(try await patch(2, Row(review: .reject)).reviewStatus == .rejected)

        let early = await failure { _ = try await client.confirmExtraction(path: .init(id: run.id)) }
        #expect(early?.status == 422 && early?.fieldErrors.map(\.pointer) == ["/rows/3"], "the unreviewed row")
        let badDate = await failure { _ = try await patch(3, Row(collectedAt: "28.09.2026")) }
        #expect(badDate?.detail(for: "/collected_at") != nil)
        #expect(try await patch(3, Row(review: .accept, collectedAt: "2026-09-28")).validation.contains("date_missing") == false)

        let confirmed = try await client.confirmExtraction(path: .init(id: run.id)).ok.body.json
        #expect(confirmed.status == .confirmed)
        let results = try #require(confirmed.rows).compactMap(\.labResultId)
        #expect(results.count == 3, "the rejected row is not saved")
        #expect(try await client.getDocument(path: .init(id: doc.id)).ok.body.json.status == .confirmed)

        // An edit after confirming applies when confirming again, as a new revision.
        _ = try await patch(0, Row(valueText: "5.2", valueNumeric: 5.2))
        _ = try await client.confirmExtraction(path: .init(id: run.id)).ok.body.json
        let history = try await client.getLabResultHistory(path: .init(id: results[0])).ok.body.json.revisions
        #expect(history.map(\.revision) == [2, 1] && history.map(\.valueText) == ["5.2", "5.1"])

        let back = try await client.unconfirmExtraction(path: .init(id: run.id)).ok.body.json
        #expect(back.status == .succeeded && back.rows?.allSatisfy { $0.labResultId == nil } == true)
        #expect(try await client.getDocument(path: .init(id: doc.id)).ok.body.json.status == .needsReview)
    }

    @Test func `deleting keeps or removes the derived results`() async throws {
        let client = try await signedIn()
        let seeded = "doc_" + String(format: "%032x", 1)
        _ = try await client.deleteDocument(path: .init(id: seeded), query: .init(derived: .keep)).noContent
        let doc = try await client.getDocument(path: .init(id: seeded)).ok.body.json
        #expect(doc.status == .deleted && doc.filename == nil && doc.sha256 == nil)
        let gone = await failure { _ = try await client.getDocumentFile(path: .init(id: seeded)) }
        #expect(gone?.status == 404)
    }

    @Test func `extractors, aliases and the specialised results' history answer`() async throws {
        let client = try await signedIn()
        let extractors = try await client.listExtractors().ok.body.json.extractors
        #expect(extractors.map(\.id) == [.fake, .gemini, .openai] && extractors.map(\.enabled) == [true, true, false])
        let aliases = try await client.listAnalyteAliases().ok.body.json.aliases
        #expect(aliases.map(\.analyte).contains("glucose"))

        let first = try #require(try await client.listLabResults(query: .init(limit: 500)).ok.body.json.value2.labResults.first)
        let history = try await client.getLabResultHistory(path: .init(id: first.id)).ok.body.json.revisions
        #expect(history.map(\.id) == [first.id])
        #expect(try await client.getDocument(path: .init(id: first.provenance.documentId)).ok.body.json.status == .confirmed,
                "the first specialised result's document is the seeded one")
    }
}
