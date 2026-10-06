import Foundation
import SwiftUI
import VitamuxKit

// Labels and wording of the Lab tab, the twin of web/src/lib/lab/format.ts. Copy states facts
// only: what was read, what differs from the PDF, what is missing. It never rates a value.

typealias LabDocument = Components.Schemas.Document
typealias Extraction = Components.Schemas.Extraction
typealias ExtractionRow = Components.Schemas.ExtractionRow
typealias Extractor = Components.Schemas.Extractor

enum LabText {
    static func provider(_ id: String) -> String {
        switch id {
        case "fake": "Built-in test extractor"
        case "gemini": "Google Gemini"
        case "openai": "OpenAI"
        case "openai_compatible": "OpenAI-compatible server"
        default: id
        }
    }

    /// "Google Gemini (gemini-x)" or the provider alone.
    static func readBy(_ provider: String, model: String?) -> String {
        model.map { "\(Self.provider(provider)) (\($0))" } ?? Self.provider(provider)
    }

    /// Why a run ended without rows (extraction_runs.error_class).
    static func errorClass(_ code: String?) -> String? {
        switch code {
        case "provider_disabled": "The provider was disabled after the run was queued."
        case "consent_mismatch": "The configured model changed after consent was given; start a new extraction."
        case "auth": "The provider refused the configured key."
        case "rejected": "The provider refused the request."
        case "rate_limited": "The provider asked to wait; the run is retried."
        case "transient": "The provider could not be reached; the run is retried."
        case "too_large": "The PDF is larger than the provider accepts."
        case "refused": "The provider declined to answer."
        case "invalid_output": "The answer did not match the extraction format."
        case "unknown_document": "The built-in test extractor only reads the synthetic fixture PDFs."
        case "provider_unavailable": "The provider is no longer configured on this server."
        case "abandoned": "The run stopped without an outcome."
        default: nil
        }
    }

    /// Upload refusals (422 `errors[0].detail`).
    static func uploadReason(_ code: String?) -> String? {
        switch code {
        case "not_pdf": "This file is not a PDF."
        case "empty": "This file is empty."
        case "encrypted": "Password-protected PDFs cannot be stored. Save an unprotected copy and add that."
        case "too_many_pages": "The PDF has more than 50 pages."
        case "malformed": "The PDF structure could not be read."
        default: nil
        }
    }

    /// A deterministic check or an extractor warning: what to compare with the PDF.
    static func warning(_ code: String) -> String {
        switch code {
        case "value_mismatch": "The value and the printed value text read differently."
        case "comparator_mismatch": "The comparator and the printed value text read differently."
        case "range_mismatch": "The range limits and the printed range text read differently."
        case "evidence_unverified": "The evidence text was not found in the PDF text layer."
        case "value_not_in_evidence": "The value text does not appear in the evidence text."
        case "unknown_unit": "The unit is not one listed for this analyte."
        case "unit_not_convertible": "No conversion is listed from this unit for this analyte; the printed value is kept as is."
        case "unit_missing": "No unit was read. Accepting confirms the value as unitless."
        case "date_missing": "No collection date was read. Add it before confirming."
        case "date_in_future": "A date on this row is later than today."
        case "date_ambiguous": "The printed date reads both as day/month and month/day."
        case "reported_before_collected": "The report date is earlier than the collection date."
        case "duplicate_in_run": "Another row of this extraction has the same analyte."
        case "already_confirmed": "A result for this analyte and collection date is already confirmed from another document."
        case "unknown_analyte": "No analyte matches this label."
        case "unreadable_label": "The extractor could not read the label."
        case "unreadable_value": "The extractor could not read the value."
        case "unreadable_unit": "The extractor could not read the unit."
        case "unreadable_range": "The extractor could not read the range."
        case "unreadable_flag": "The extractor could not read the flag."
        case "uncertain_reading": "The extractor marked this reading as uncertain."
        case "handwritten": "Part of this row is handwritten."
        case "crossed_out": "Part of this row is crossed out."
        case "split_across_pages": "This row continues on another page."
        case "footnote": "This row has a footnote."
        case "multiple_values": "More than one value is printed for this row."
        case "page_unreadable": "A page could not be read."
        case "low_quality_image": "The scan is of low quality."
        case "possibly_truncated": "The document may be cut off."
        case "not_lab_report": "The extractor did not recognise a lab report."
        case "multiple_reports": "The PDF holds more than one report."
        case "dates_not_found": "No dates were found."
        default: code
        }
    }

    /// "1.2 MiB" for a byte count.
    static func size(_ bytes: Int) -> String {
        if bytes < 1024 { return "\(bytes) B" }
        if bytes < 1024 * 1024 { return String(format: "%.1f KiB", Double(bytes) / 1024) }
        return String(format: "%.1f MiB", Double(bytes) / 1024 / 1024)
    }

    /// The value as printed, with its comparator when the text lacks it: "< 0.5"; "–" when none was read.
    static func printed(_ value: String?, comparator: String?) -> String {
        guard let value else { return "–" }
        guard let comparator, !value.trimmingCharacters(in: .whitespaces).hasPrefix(comparator) else { return value }
        return "\(comparator) \(value)"
    }

    static func plural(_ count: Int, _ one: String, _ many: String? = nil) -> String {
        "\(count) \(count == 1 ? one : many ?? one + "s")"
    }
}

extension LabDocument {
    /// The original filename, or "Document from <date>" when none was given or it was deleted.
    var name: String {
        filename ?? "Document from \(Format.instant(uploadedAt))"
    }
}

/// A state shown with a symbol and a word, never colour alone.
struct LabStatus: View {
    let label: String
    let symbol: String
    var tint: Color = .secondary

    var body: some View {
        Label(label, systemImage: symbol)
            .font(.caption.weight(.medium))
            .foregroundStyle(tint)
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(label)
    }
}

extension LabStatus {
    init(_ status: LabDocument.StatusPayload) {
        switch status {
        case .uploaded: self.init(label: "Uploaded", symbol: "arrow.up.doc")
        case .extracting: self.init(label: "Extracting", symbol: "hourglass", tint: .blue)
        case .needsReview: self.init(label: "Needs review", symbol: "exclamationmark.circle", tint: .orange)
        case .confirmed: self.init(label: "Confirmed", symbol: "checkmark.circle.fill", tint: .green)
        case .deleted: self.init(label: "Deleted", symbol: "trash", tint: .secondary)
        }
    }

    init(_ status: Extraction.StatusPayload) {
        switch status {
        case .queued: self.init(label: "Queued", symbol: "hourglass", tint: .blue)
        case .running: self.init(label: "Running", symbol: "hourglass", tint: .blue)
        case .succeeded: self.init(label: "Ready for review", symbol: "exclamationmark.circle", tint: .orange)
        case .failed: self.init(label: "Failed", symbol: "xmark.octagon", tint: .red)
        case .confirmed: self.init(label: "Confirmed", symbol: "checkmark.circle.fill", tint: .green)
        }
    }

    init(_ status: ExtractionRow.ReviewStatusPayload) {
        switch status {
        case .pending: self.init(label: "Not reviewed", symbol: "circle.dashed", tint: .orange)
        case .accepted: self.init(label: "Accepted", symbol: "checkmark.circle.fill", tint: .green)
        case .edited: self.init(label: "Edited", symbol: "pencil.circle.fill", tint: .green)
        case .rejected: self.init(label: "Rejected", symbol: "xmark.circle", tint: .secondary)
        }
    }
}

/// A notice after an action: a symbol and a sentence.
struct LabNotice: View {
    let text: String

    var body: some View {
        Label(text, systemImage: "checkmark.circle.fill")
            .foregroundStyle(.green)
            .font(.subheadline)
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(text)
            .accessibilityIdentifier("labNotice")
    }
}

/// A field's message from the server, matched by JSON pointer, under its input.
struct LabFieldError: View {
    let text: String?

    var body: some View {
        if let text {
            Text(text).font(.footnote).foregroundStyle(.red)
        }
    }
}
