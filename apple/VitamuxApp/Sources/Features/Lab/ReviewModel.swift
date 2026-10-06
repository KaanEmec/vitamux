import Foundation
import Observation
import PDFKit
import VitamuxKit

/// The review of one document (the panel's `/lab/documents/{id}`): the document, its runs
/// (`GET /documents/{id}/extractions`, newest first), the run under review with its rows
/// (`GET /extractions/{id}`), the PDF (`GET /documents/{id}/file`, held in memory only, never
/// written to the cache) and the analyte codes the row editor offers (`GET /analytes/aliases`).
/// Every row is accepted, edited or rejected before `confirm` saves the results; a confirmed run
/// can be edited and confirmed again (new revisions) or unconfirmed. Extracted values are shown,
/// never logged.
@Observable
final class ReviewModel {
    let id: String
    private(set) var document: Loadable<LabDocument> = .loading
    private(set) var runs: [Extraction] = []
    /// The run under review; nil picks the confirmed one, else the latest that succeeded.
    var runID: String?
    private(set) var run: Extraction?
    private(set) var pdf: PDFDocument?
    private(set) var pdfProblem: Problem?
    private(set) var codes: [String] = []
    private(set) var selected: Int?
    /// The PDF page shown, 1-based.
    var page = 1
    private(set) var confirmProblem: Problem?
    private(set) var notice: String?
    private(set) var isBusy = false

    init(id: String) {
        self.id = id
    }

    // MARK: Loading

    func load(_ client: Client?) async {
        guard let client else { return }
        await loadRuns(client)
        if pdf == nil, pdfProblem == nil, let doc = document.value, doc.status != .deleted {
            await loadPDF(client)
        }
        if codes.isEmpty, let aliases = try? await client.listAnalyteAliases().ok.body.json.aliases {
            codes = Array(Set(aliases.map(\.analyte))).sorted()
        }
    }

    /// The runs, then the document (so its status reflects them), then the run under review.
    func loadRuns(_ client: Client?) async {
        guard let client else { return }
        // Everything is read first and shown together: a change of `isActive` restarts the
        // screen's polling task, which must not cancel this read halfway.
        let runs: [Extraction], document: LabDocument
        do {
            runs = try await client.listExtractions(path: .init(id: id)).ok.body.json.extractions
            document = try await client.getDocument(path: .init(id: id)).ok.body.json
        } catch {
            if !Task.isCancelled { self.document = .failed(Problem(error)) }
            return
        }
        var runID = runID
        if !runs.contains(where: { $0.id == runID }) {
            runID = (runs.first { $0.status == .confirmed } ?? runs.first { $0.status == .succeeded } ?? runs.first)?.id
        }
        let run = await fetchRun(runID, client: client)
        guard !Task.isCancelled else { return }
        self.document = .loaded(document)
        self.runID = runID
        if run != nil || runID == nil { self.run = run }
        self.runs = runs
    }

    func loadRun(_ client: Client?) async {
        guard let client else { return }
        let run = await fetchRun(runID, client: client)
        if run != nil || runID == nil { self.run = run }
    }

    private func fetchRun(_ id: String?, client: Client) async -> Extraction? {
        guard let id else { return nil }
        do {
            return try await client.getExtraction(path: .init(id: id)).ok.body.json
        } catch {
            if !Task.isCancelled { confirmProblem = Problem(error) }
            return nil
        }
    }

    func pick(run id: String, client: Client?) async {
        runID = id
        selected = nil
        confirmProblem = nil
        notice = nil
        await loadRun(client)
    }

    private func loadPDF(_ client: Client) async {
        do {
            let body = try await client.getDocumentFile(path: .init(id: id)).ok.body.pdf
            let data = try await Data(collecting: body, upTo: LabUpload.maxBytes + 1024 * 1024)
            pdf = PDFDocument(data: data)
            if pdf == nil { pdfProblem = Problem(title: "The PDF could not be shown") }
        } catch {
            pdfProblem = Problem(error)
        }
    }

    // MARK: State

    var rows: [ExtractionRow] { run?.rows ?? [] }

    var row: ExtractionRow? {
        selected.flatMap { index in rows.first { $0.index == index } }
    }

    var pending: Int { rows.filter { $0.reviewStatus == .pending }.count }

    var withChecks: Int {
        rows.filter { $0.reviewStatus == .pending && !($0.validation.isEmpty && $0.warnings.isEmpty) }.count
    }

    /// A run is queued or running: the screen polls every 2 s.
    var isActive: Bool { runs.contains { $0.status == .queued || $0.status == .running } }

    var isReviewable: Bool { run?.status == .succeeded || run?.status == .confirmed }

    var pageCount: Int { pdf?.pageCount ?? document.value?.pageCount ?? 1 }

    /// The selected row's area on the shown page, in page fractions with the origin top left.
    var box: ExtractionRow.BboxPayload? {
        guard let row, row.page == page else { return nil }
        return row.bbox
    }

    func deselect() {
        selected = nil
    }

    func select(_ row: ExtractionRow) {
        selected = row.index
        if let page = row.page { self.page = page }
    }

    // MARK: Review

    /// After a row was saved: reload, then move on to the next row still waiting for review.
    func saved(_ row: ExtractionRow, client: Client?) async {
        await loadRuns(client)
        confirmProblem = nil
        notice = nil
        guard row.reviewStatus != .pending else { return }
        if let next = rows.first(where: { $0.reviewStatus == .pending && $0.index > row.index }) ?? rows.first(where: { $0.reviewStatus == .pending }) {
            select(next)
        } else {
            deselect() // every row is reviewed: back to the list and confirm
        }
    }

    func confirm(_ client: Client?) async {
        guard let client, let run else { return }
        isBusy = true
        defer { isBusy = false }
        confirmProblem = nil
        notice = nil
        do {
            let confirmed = try await client.confirmExtraction(path: .init(id: run.id)).ok.body.json
            let kept = (confirmed.rows ?? []).filter { $0.reviewStatus != .rejected }.count
            notice = "Confirmed \(LabText.plural(kept, "result"))."
            await loadRuns(client)
        } catch {
            confirmProblem = Problem(error)
        }
    }

    func unconfirm(_ client: Client?) async {
        guard let client, let run else { return }
        isBusy = true
        defer { isBusy = false }
        confirmProblem = nil
        notice = nil
        do {
            _ = try await client.unconfirmExtraction(path: .init(id: run.id)).ok.body.json
            notice = "Unconfirmed: the results from this run were removed and the rows are back in review."
            await loadRuns(client)
        } catch {
            confirmProblem = Problem(error)
        }
    }

    func started(_ run: Extraction, client: Client?) async {
        runID = run.id
        selected = nil
        notice = nil
        confirmProblem = nil
        await loadRuns(client)
    }

    /// Deletes the document; true once the server did.
    func delete(derived: LabModel.Derived, client: Client?) async -> Bool {
        guard let client else { return false }
        do {
            _ = try await client.deleteDocument(path: .init(id: id), query: .init(derived: derived == .keep ? .keep : .delete)).noContent
            return true
        } catch {
            confirmProblem = Problem(error)
            return false
        }
    }

    /// What blocks confirming, one line per row: "Row 4 (HbA1c) collected at: is required…".
    struct Blocker: Identifiable {
        var id: String
        var text: String
        var row: ExtractionRow?
    }

    var blockers: [Blocker] {
        guard let problem = confirmProblem, problem.status == 422 else { return [] }
        return problem.fieldErrors.map { error in
            let parts = error.pointer.split(separator: "/").map(String.init)
            let index = parts.count > 1 && parts[0] == "rows" ? Int(parts[1]) : nil
            let row = index.flatMap { i in rows.first { $0.index == i } }
            let field = parts.count > 2 ? " " + parts[2].replacingOccurrences(of: "_", with: " ") : ""
            let label = row.map { "Row \($0.index + 1) (\($0.analyteLabel))" } ?? "The extraction"
            return Blocker(id: error.pointer, text: "\(label)\(field): \(error.detail)", row: row)
        }
    }
}
