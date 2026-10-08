#if DEBUG
import CryptoKit
import Foundation
import Synchronization

// Lab documents, extraction, review and result history (J22.12) for the fake server, mirroring
// web/e2e/lab-fake.ts and the server's handlers (internal/api/documents.go, extractions.go,
// review.go): uploads are multipart (or a raw PDF), anything without `%PDF-` is 422 `not_pdf`,
// more than 20 MiB is 413, and the same bytes again answer 200 with the stored document. External
// extractors need consent naming the configured model (ADR-0013); a run is `running` on the first
// poll of the document's runs and `succeeded` on the next. Row patches follow the review states,
// confirm lists unreviewed rows (422), unconfirm returns the run to review. GET /lab-results
// itself belongs to FakeServer+Specialised.swift; history answers for its results and for the
// ones confirmed here. The PDF and every value are synthetic.
//
// Seeded: one confirmed document (`doc_…01`, the document the specialised results point at first).
extension FakeServer {
    /// The synthetic one-page lab report the fake serves and accepts; UI tests pick it "from Files".
    public static var syntheticLabPDF: Data { LabFixture.pdf }

    /// Answers a lab endpoint, or nil for a request this file does not handle.
    static func lab(method: String, url: URL, body: Data) -> Reply? {
        let path = url.path
        let parts = path.split(separator: "/").map(String.init)
        let owned = path.hasPrefix("/api/v1/documents") || path.hasPrefix("/api/v1/extract") || path == "/api/v1/analytes/aliases"
            || (parts.count == 5 && parts[2] == "lab-results" && parts[4] == "history")
        guard owned else { return nil }
        let host = url.host() ?? ""
        return LabFixture.states.withLock { states in
            var state = states[host] ?? LabFixture.State()
            defer { states[host] = state }
            return state.route(method: method, url: url, parts: parts, body: body)
        }
    }
}

struct LabFixture {
    typealias JSON = [String: Any]

    static let states = Mutex<[String: State]>([:])
    static let geminiModel = "gemini-synthetic-model"
    static let at = "2026-10-01T09:00:00Z"
    static let maxBytes = 20 * 1024 * 1024

    static func hex(_ n: Int) -> String { String(format: "%032x", n) }

    static var extractors: [JSON] { [
        ["id": "fake", "external": false, "model": NSNull(), "enabled": true],
        ["id": "gemini", "external": true, "model": geminiModel, "enabled": true],
        ["id": "openai", "external": true, "model": "openai-synthetic-model", "enabled": false],
    ] }

    static let aliases = ["glucose", "creatinine", "hba1c", "sodium", "ldl_c"]

    static let editable: Set<String> = [
        "analyte_label", "value_text", "value_numeric", "comparator", "unit_text", "reference_range_text", "ref_low", "ref_high",
        "printed_flag", "specimen_type", "collected_at", "reported_at", "laboratory", "analyte",
    ]

    // MARK: The synthetic PDF

    static let lines = [
        "SYNTHETIC LAB - not a real report",
        "Glucose 5.1 mmol/L 3.9 - 5.5",
        "Creatinine 112 H umol/L 60 - 110",
        "####### smudged line",
        "HbA1c 39 mmol/mol 20 - 42",
    ]

    /// A one-page PDF with a Helvetica text layer: the printed lines of the synthetic report.
    static let pdf: Data = {
        let text = "BT /F1 12 Tf 72 740 Td " + lines.enumerated().map { i, line in (i > 0 ? "0 -20 Td " : "") + "(\(line)) Tj" }.joined(separator: " ") + " ET"
        let objects = [
            "<< /Type /Catalog /Pages 2 0 R >>",
            "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
            "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
            "<< /Length \(text.utf8.count) >>\nstream\n\(text)\nendstream",
            "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
        ]
        var out = "%PDF-1.4\n"
        var offsets: [Int] = []
        for (i, object) in objects.enumerated() {
            offsets.append(out.utf8.count)
            out += "\(i + 1) 0 obj\n\(object)\nendobj\n"
        }
        let xref = out.utf8.count
        out += "xref\n0 \(objects.count + 1)\n0000000000 65535 f \n" + offsets.map { String(format: "%010d 00000 n \n", $0) }.joined()
        out += "trailer\n<< /Size \(objects.count + 1) /Root 1 0 R >>\nstartxref\n\(xref)\n%%EOF\n"
        return Data(out.utf8)
    }()

    /// Row area of printed line `n` (0 = header) in page fractions, origin top left.
    static func lineBox(_ n: Int) -> JSON {
        ["x0": 0.11, "y0": Double(792 - 740 - 12 + 20 * n) / 792, "x1": 0.6, "y1": Double(792 - 740 + 5 + 20 * n) / 792]
    }

    static func extractedRows() -> [JSON] {
        func row(_ index: Int, _ fields: JSON) -> JSON {
            var base: JSON = [
                "index": index, "page": 1, "value_numeric": NSNull(), "comparator": NSNull(), "ref_low": NSNull(), "ref_high": NSNull(),
                "printed_flag": NSNull(), "specimen_type": "Serum", "collected_at": "2026-09-28", "reported_at": "2026-09-29",
                "laboratory": "Synthetic Lab", "confidence": 0.9, "review_status": "pending", "reviewed_at": NSNull(), "warnings": [String](),
                "validation": [String](), "edits": [JSON](), "lab_result_id": NSNull(), "bbox": lineBox(index + 1),
            ]
            base.merge(fields) { $1 }
            return base
        }
        return [
            row(0, ["analyte_label": "Glucose", "value_text": "5.1", "value_numeric": 5.1, "unit_text": "mmol/L", "reference_range_text": "3.9 - 5.5",
                    "ref_low": 3.9, "ref_high": 5.5, "evidence_text": lines[1], "analyte": "glucose", "suggested_analyte": "glucose"]),
            row(1, ["analyte_label": "Creatinine", "value_text": "112", "value_numeric": 112, "unit_text": "umol/L", "reference_range_text": "60 - 110",
                    "ref_low": 60, "ref_high": 110, "printed_flag": "H", "evidence_text": lines[2], "analyte": "creatinine", "suggested_analyte": "creatinine"]),
            row(2, ["analyte_label": "#######", "value_text": NSNull(), "unit_text": NSNull(), "reference_range_text": NSNull(), "evidence_text": lines[3],
                    "analyte": NSNull(), "suggested_analyte": NSNull(), "confidence": 0.31, "warnings": ["unreadable_value"],
                    "validation": ["unit_missing", "unknown_analyte"]]),
            row(3, ["analyte_label": "HbA1c", "value_text": "39", "value_numeric": 39, "unit_text": "mmol/mol", "reference_range_text": "20 - 42",
                    "ref_low": 20, "ref_high": 42, "collected_at": NSNull(), "evidence_text": lines[4], "analyte": "hba1c", "suggested_analyte": "hba1c",
                    "validation": ["date_missing"]]),
        ]
    }

    // MARK: State

    struct Run {
        var json: JSON
        var rows: [JSON]

        var id: String { json["id"] as! String }
        var documentID: String { json["document_id"] as! String }
        var status: String {
            get { json["status"] as! String }
            set { json["status"] = newValue }
        }
    }

    struct State {
        var docs: [JSON] = []
        var runs: [Run] = []
        var results: [JSON] = []
        var revisions: [String: [JSON]] = [:]
        private var next = 16

        init() {
            // One confirmed document with every row accepted, so the list and the analyte view's
            // links have something to open.
            docs = [document(id: "doc_\(LabFixture.hex(1))", filename: "synthetic-report-2025.pdf", sha: String(repeating: "ab", count: 32),
                             size: LabFixture.pdf.count, uploadedAt: "2025-10-01T09:00:00Z", status: "confirmed")]
            var run = Run(json: runJSON(id: "ext_\(LabFixture.hex(2))", doc: "doc_\(LabFixture.hex(1))", extractor: LabFixture.extractors[0], consent: nil), rows: [])
            run.json.merge(["status": "succeeded", "row_count": 4, "started_at": LabFixture.at, "finished_at": LabFixture.at]) { $1 }
            run.rows = LabFixture.extractedRows().map { row in
                var row = row
                row["review_status"] = "accepted"
                row["reviewed_at"] = LabFixture.at
                if row["collected_at"] is NSNull { row["collected_at"] = "2025-09-28" }
                return row
            }
            runs = [run]
            _ = confirm(run.id)
        }

        mutating func route(method: String, url: URL, parts: [String], body: Data) -> Reply {
            let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
            switch (method, parts.dropFirst(2).first, parts.count) {
            case ("GET", "documents", 3):
                return Reply.json(200, ["documents": docs, "has_more": false])
            case ("POST", "documents", 3):
                return upload(body)
            case (_, "documents", 4):
                guard let index = docs.firstIndex(where: { $0["id"] as? String == parts[3] }) else { return notFound("no such document") }
                if method == "DELETE" { return remove(at: index, derived: query.first { $0.name == "derived" }?.value ?? "") }
                return Reply.json(200, docs[index])
            case ("GET", "documents", 5) where parts[4] == "file":
                guard let doc = docs.first(where: { $0["id"] as? String == parts[3] }), doc["status"] as? String != "deleted" else {
                    return notFound("no such document")
                }
                return Reply(status: 200, headers: ["Content-Type": "application/pdf", "Cache-Control": "no-store"], body: LabFixture.pdf)
            case (_, "documents", 5) where parts[4] == "extractions":
                guard let index = docs.firstIndex(where: { $0["id"] as? String == parts[3] }) else { return notFound("no such document") }
                if method == "POST" { return start(docAt: index, body: body) }
                let ids = runs.filter { $0.documentID == parts[3] }.map(\.id).reversed()
                return Reply.json(200, ["extractions": ids.map { poll($0) }])
            case ("GET", "extractors", 3):
                return Reply.json(200, ["extractors": LabFixture.extractors])
            case ("GET", "extractions", 4):
                guard let run = runs.first(where: { $0.id == parts[3] }) else { return notFound("no such extraction") }
                return Reply.json(200, withRows(run))
            case ("PATCH", "extractions", 6) where parts[4] == "rows":
                return patch(run: parts[3], row: Int(parts[5]) ?? -1, body: body)
            case ("POST", "extractions", 5) where parts[4] == "confirm":
                return confirm(parts[3])
            case ("POST", "extractions", 5) where parts[4] == "unconfirm":
                return unconfirm(parts[3])
            case ("GET", "lab-results", 5):
                return history(parts[3])
            case ("GET", "analytes", 4):
                return Reply.json(200, ["aliases": LabFixture.aliases.enumerated().map { i, code in
                    ["id": String(i + 1), "label": code, "analyte": code, "source": "seed", "created_at": LabFixture.at]
                }])
            default:
                return Reply.problem(404, "not_found", "not stubbed in the fake server")
            }
        }

        // MARK: Documents

        private func document(id: String, filename: String?, sha: String, size: Int, uploadedAt: String, status: String) -> JSON {
            [
                "id": id, "status": status, "sha256": sha, "filename": filename ?? NSNull(), "size_bytes": size, "page_count": 1,
                "uploaded_at": uploadedAt, "retention_until": NSNull(), "deleted_at": NSNull(),
            ]
        }

        private mutating func upload(_ body: Data) -> Reply {
            guard let (pdf, filename) = Self.pdfPart(of: body) else {
                return Reply.problem(422, "validation_failed", "the upload has no file part", errors: [("/file", "empty")])
            }
            if pdf.count > LabFixture.maxBytes { return Reply.problem(413, "payload_too_large", "the PDF is larger than 20 MiB") }
            if pdf.isEmpty { return Reply.problem(422, "validation_failed", "the upload is not an acceptable PDF", errors: [("/file", "empty")]) }
            guard pdf.starts(with: Data("%PDF-".utf8)) else {
                return Reply.problem(422, "validation_failed", "the upload is not an acceptable PDF", errors: [("/file", "not_pdf")])
            }
            let sha = SHA256.hash(data: pdf).map { String(format: "%02x", $0) }.joined()
            if let existing = docs.first(where: { $0["sha256"] as? String == sha }) { return Reply.json(200, existing) }
            let id = "doc_\(LabFixture.hex(take()))"
            let uploaded = Date(timeIntervalSince1970: 1_790_000_000 + TimeInterval(next * 60)).formatted(Date.ISO8601FormatStyle())
            let doc = document(id: id, filename: filename, sha: sha, size: pdf.count, uploadedAt: uploaded, status: "uploaded")
            docs.append(doc)
            return Reply.json(201, doc)
        }

        /// The PDF of a raw `application/pdf` body or of the multipart form's first part, with its filename.
        static func pdfPart(of body: Data) -> (Data, String?)? {
            if body.starts(with: Data("%PDF-".utf8)) { return (body, nil) }
            let crlf = Data("\r\n".utf8)
            guard let firstLine = body.range(of: crlf) else { return nil }
            let boundary = body[body.startIndex..<firstLine.lowerBound]
            guard boundary.starts(with: Data("--".utf8)),
                  let headerEnd = body.range(of: Data("\r\n\r\n".utf8), in: firstLine.upperBound..<body.endIndex) else { return nil }
            let headers = String(decoding: body[firstLine.upperBound..<headerEnd.lowerBound], as: UTF8.self)
            let filename = headers.firstMatch(of: /filename="([^"]*)"/).map { String($0.1) }
            let end = body.range(of: crlf + boundary, in: headerEnd.upperBound..<body.endIndex)?.lowerBound ?? body.endIndex
            return (Data(body[headerEnd.upperBound..<end]), filename)
        }

        private mutating func remove(at index: Int, derived: String) -> Reply {
            guard derived == "keep" || derived == "delete" else {
                return Reply.problem(422, "validation_failed", "derived must be keep or delete", errors: [("/derived", "must be keep or delete")])
            }
            let id = docs[index]["id"] as! String
            docs[index].merge(["status": "deleted", "sha256": NSNull(), "filename": NSNull(), "deleted_at": LabFixture.at, "retention_until": NSNull()]) { $1 }
            runs.removeAll { $0.documentID == id }
            for i in results.indices.reversed() where (results[i]["provenance"] as? JSON)?["document_id"] as? String == id {
                if derived == "delete" {
                    results.remove(at: i)
                } else {
                    var provenance = results[i]["provenance"] as! JSON
                    provenance["extraction_id"] = NSNull()
                    results[i]["provenance"] = provenance
                }
            }
            return Reply(status: 204)
        }

        // MARK: Runs

        private func runJSON(id: String, doc: String, extractor: JSON, consent: JSON?) -> JSON {
            let external = extractor["external"] as! Bool
            return [
                "id": id, "document_id": doc, "status": "queued", "provider": extractor["id"]!, "model": extractor["model"]!,
                "external": external, "consent": consent ?? NSNull(), "schema_version": "vitamux.lab.extraction/1",
                "prompt_version": "lab-extraction/v1", "provider_request_id": NSNull(), "document": JSON(), "usage": JSON(),
                "warnings": [String](), "error_class": NSNull(), "row_count": 0, "created_by": FakeServer.Owner.username,
                "created_at": LabFixture.at, "started_at": NSNull(), "finished_at": NSNull(),
            ]
        }

        private mutating func start(docAt index: Int, body: Data) -> Reply {
            guard let input = (try? JSONSerialization.jsonObject(with: body)) as? JSON, let provider = input["provider"] as? String else {
                return Reply.problem(422, "validation_failed", "request body is not valid JSON")
            }
            guard let extractor = LabFixture.extractors.first(where: { $0["id"] as? String == provider }) else {
                return Reply.problem(403, "forbidden", "\(provider) is not configured on this server")
            }
            guard extractor["enabled"] as? Bool == true else { return Reply.problem(403, "forbidden", "\(provider) is disabled; enable it in settings") }
            let consent = input["consent"] as? JSON
            let external = extractor["external"] as! Bool
            if external, consent?["provider"] as? String != provider || consent?["model"] as? String != extractor["model"] as? String
                || consent?["acknowledged_at"] as? String == nil {
                return Reply.problem(409, "consent_required", "consent must name provider \(provider) and model \(extractor["model"] as? String ?? "")")
            }
            let docID = docs[index]["id"] as! String
            if runs.contains(where: { $0.documentID == docID && ["queued", "running"].contains($0.status) }) {
                return Reply.problem(409, "conflict", "an extraction of this document is already queued or running")
            }
            let run = Run(json: runJSON(id: "ext_\(LabFixture.hex(take()))", doc: docID, extractor: extractor, consent: external ? consent : nil), rows: [])
            runs.append(run)
            docs[index]["status"] = "extracting"
            return Reply.json(202, run.json)
        }

        /// A queued run starts on the first poll and finishes on the next: rows stored, document in review.
        private mutating func poll(_ id: String) -> JSON {
            let i = runs.firstIndex { $0.id == id }!
            switch runs[i].status {
            case "queued":
                runs[i].json.merge(["status": "running", "started_at": LabFixture.at]) { $1 }
            case "running":
                runs[i].rows = LabFixture.extractedRows()
                runs[i].json.merge(["status": "succeeded", "row_count": runs[i].rows.count, "finished_at": LabFixture.at]) { $1 }
                setStatus(of: runs[i].documentID, to: "needs_review")
            default:
                break
            }
            return runs[i].json
        }

        private mutating func setStatus(of document: String, to status: String) {
            if let d = docs.firstIndex(where: { $0["id"] as? String == document }) { docs[d]["status"] = status }
        }

        private func withRows(_ run: Run) -> JSON {
            var out = run.json
            out["rows"] = run.rows.map(validated)
            return out
        }

        /// The row with its checks recomputed, as the server does on every read.
        private func validated(_ row: JSON) -> JSON {
            var row = row
            var checks = (row["validation"] as? [String] ?? []).filter { $0 != "unit_missing" && $0 != "date_missing" }
            if row["unit_text"] is NSNull { checks.append("unit_missing") }
            if row["collected_at"] is NSNull { checks.append("date_missing") }
            row["validation"] = checks
            return row
        }

        // MARK: Review

        private mutating func patch(run id: String, row index: Int, body: Data) -> Reply {
            guard let r = runs.firstIndex(where: { $0.id == id }), let i = runs[r].rows.firstIndex(where: { $0["index"] as? Int == index }) else {
                return notFound("no such row")
            }
            guard ["succeeded", "confirmed"].contains(runs[r].status) else { return Reply.problem(409, "conflict", "this extraction has no rows to review") }
            guard var set = (try? JSONSerialization.jsonObject(with: body)) as? JSON else {
                return Reply.problem(422, "validation_failed", "request body is not valid JSON")
            }
            let review = set.removeValue(forKey: "review") as? String
            if let date = set["collected_at"] as? String, date.wholeMatch(of: /\d{4}-\d{2}-\d{2}(T\d{2}:\d{2})?/) == nil {
                return Reply.problem(422, "validation_failed", "the review is incomplete or the edit is invalid",
                               errors: [("/collected_at", "must be an ISO 8601 local date or date-time without offset")])
            }
            var row = runs[r].rows[i]
            var changes: JSON = [:]
            for (key, value) in set.sorted(by: { $0.key < $1.key }) {
                guard LabFixture.editable.contains(key) else {
                    return Reply.problem(422, "validation_failed", "invalid edit", errors: [("/\(key)", "is not an editable field")])
                }
                let old = row[key] ?? NSNull()
                if !(old as AnyObject).isEqual(value) { changes[key] = ["from": old, "to": value] }
                row[key] = value
            }
            let edited = !changes.isEmpty
            if review == "reject" {
                row["review_status"] = "rejected"
            } else if edited {
                row["review_status"] = "edited"
            } else if review == "accept" {
                row["review_status"] = "accepted"
            }
            row["reviewed_at"] = LabFixture.at
            var edits = row["edits"] as? [JSON] ?? []
            if edited { edits.append(["action": "edit", "changes": changes, "actor": FakeServer.Owner.username, "created_at": LabFixture.at]) }
            if let review, review == "reject" || !edited {
                edits.append(["action": review, "changes": JSON(), "actor": FakeServer.Owner.username, "created_at": LabFixture.at])
            }
            row["edits"] = edits
            runs[r].rows[i] = row
            return Reply.json(200, validated(row))
        }

        private mutating func confirm(_ id: String) -> Reply {
            guard let r = runs.firstIndex(where: { $0.id == id }) else { return notFound("no such extraction") }
            guard ["succeeded", "confirmed"].contains(runs[r].status) else { return Reply.problem(409, "conflict", "this extraction is not ready to confirm") }
            var errors: [(String, String)] = []
            for row in runs[r].rows {
                let n = row["index"] as! Int
                switch row["review_status"] as? String {
                case "pending": errors.append(("/rows/\(n)", "not reviewed: accept, edit or reject it"))
                case "rejected": break
                default:
                    if row["collected_at"] is NSNull { errors.append(("/rows/\(n)/collected_at", "is required: edit the row to add the collection date")) }
                }
            }
            if !errors.isEmpty { return Reply.problem(422, "validation_failed", "the review is incomplete or the edit is invalid", errors: errors) }
            for i in runs[r].rows.indices where runs[r].rows[i]["review_status"] as? String != "rejected" {
                let row = runs[r].rows[i]
                let values: JSON = [
                    "analyte": row["analyte"]!, "original_label": row["analyte_label"]!, "value_text": row["value_text"]!,
                    "value_numeric": row["value_numeric"]!, "comparator": row["comparator"]!, "unit_text": row["unit_text"]!,
                    "reference_range_text": row["reference_range_text"]!, "ref_low": row["ref_low"]!, "ref_high": row["ref_high"]!,
                    "printed_flag": row["printed_flag"]!, "specimen_type": row["specimen_type"]!, "collected_date": row["collected_at"]!,
                    "page": row["page"]!, "evidence_text": row["evidence_text"]!,
                ]
                if let resultID = row["lab_result_id"] as? String, let c = results.firstIndex(where: { $0["id"] as? String == resultID }) {
                    if values.contains(where: { !(results[c][$0.key] as AnyObject).isEqual($0.value) }) {
                        revisions[resultID, default: []].insert(results[c], at: 0)
                        results[c].merge(values) { $1 }
                        results[c]["revision"] = (results[c]["revision"] as! Int) + 1
                        results[c]["updated_at"] = "2026-10-02T09:00:00Z"
                    }
                    continue
                }
                let resultID = "lab_\(LabFixture.hex(take()))"
                runs[r].rows[i]["lab_result_id"] = resultID
                var result: JSON = [
                    "id": resultID, "revision": 1, "canonical_value": NSNull(), "canonical_unit": NSNull(), "conversion_factor": NSNull(),
                    "conversion_offset": NSNull(), "catalog_version": NSNull(), "collected_at": NSNull(), "created_at": LabFixture.at,
                    "updated_at": LabFixture.at,
                    "provenance": [
                        "report_id": "rpt_\(LabFixture.hex(1))", "document_id": runs[r].documentID, "extraction_id": id, "row_index": row["index"]!,
                        "laboratory": row["laboratory"]!, "reported_at": NSNull(), "provider": runs[r].json["provider"]!, "model": runs[r].json["model"]!,
                        "schema_version": runs[r].json["schema_version"]!, "prompt_version": runs[r].json["prompt_version"]!,
                        "confirmed_by": FakeServer.Owner.username, "confirmed_at": LabFixture.at,
                    ] as JSON,
                ]
                result.merge(values) { $1 }
                results.append(result)
            }
            runs[r].status = "confirmed"
            setStatus(of: runs[r].documentID, to: "confirmed")
            return Reply.json(200, withRows(runs[r]))
        }

        private mutating func unconfirm(_ id: String) -> Reply {
            guard let r = runs.firstIndex(where: { $0.id == id }), runs[r].status == "confirmed" else {
                return Reply.problem(409, "conflict", "this extraction is not the document's confirmed one")
            }
            results.removeAll { ($0["provenance"] as? JSON)?["extraction_id"] as? String == id }
            for i in runs[r].rows.indices { runs[r].rows[i]["lab_result_id"] = NSNull() }
            runs[r].status = "succeeded"
            setStatus(of: runs[r].documentID, to: "needs_review")
            return Reply.json(200, withRows(runs[r]))
        }

        // MARK: Results

        /// Revisions of a result confirmed here, or of one of the specialised fixture's results.
        private func history(_ id: String) -> Reply {
            if let current = results.first(where: { $0["id"] as? String == id }) {
                return Reply.json(200, ["revisions": [current] + (revisions[id] ?? [])])
            }
            let all = SpecialisedFixture(query: .init(URL(string: "https://fake.invalid/?limit=100")!)).labResults()
            let rows = all.body.flatMap { (try? JSONSerialization.jsonObject(with: $0)) as? JSON }?["lab_results"] as? [JSON] ?? []
            guard let current = rows.first(where: { $0["id"] as? String == id }) else { return notFound("no such result") }
            return Reply.json(200, ["revisions": [current]])
        }

        // MARK: Helpers

        private mutating func take() -> Int {
            defer { next += 1 }
            return next
        }

        private func notFound(_ detail: String) -> Reply {
            Reply.problem(404, "not_found", detail)
        }

    }
}
#endif
