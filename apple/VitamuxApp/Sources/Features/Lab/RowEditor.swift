import Foundation
import SwiftUI
import VitamuxKit

/// What the row editor holds: each field as text, as the panel's RowEditor form.
struct RowForm: Equatable {
    var label: String
    var value: String
    var numeric: String
    var comparator: String
    var unit: String
    var range: String
    var low: String
    var high: String
    var flag: String
    var collected: String
    var analyte: String

    static let comparators = ["", "<", ">", "<=", ">="]

    init(_ row: ExtractionRow) {
        let text = { (number: Double?) in number.map { $0.formatted(.number.grouping(.never)) } ?? "" }
        label = row.analyteLabel
        value = row.valueText ?? ""
        numeric = text(row.valueNumeric)
        comparator = row.comparator ?? ""
        unit = row.unitText ?? ""
        range = row.referenceRangeText ?? ""
        low = text(row.refLow)
        high = text(row.refHigh)
        flag = row.printedFlag ?? ""
        collected = row.collectedAt ?? ""
        analyte = row.analyte ?? ""
    }

    /// The changed fields as a merge patch, and the fields that cannot be sent, by API field name.
    /// The generated client leaves nil fields out, so a field emptied here cannot be cleared: the
    /// editor says so instead of sending something else.
    func patch(from initial: RowForm) -> (patch: Components.Schemas.ExtractionRowPatch, errors: [String: String]) {
        var patch = Components.Schemas.ExtractionRowPatch()
        var errors: [String: String] = [:]
        func text(_ new: String, _ old: String, _ field: String, set: (String) -> Void) {
            let new = new.trimmingCharacters(in: .whitespaces), old = old.trimmingCharacters(in: .whitespaces)
            guard new != old else { return }
            if new.isEmpty {
                errors[field] = field == "analyte_label" ? "is required" : Self.cannotClear
            } else {
                set(new)
            }
        }
        func number(_ new: String, _ old: String, _ field: String, set: (Double) -> Void) {
            text(new, old, field) { value in
                guard let number = Self.number(value) else {
                    errors[field] = "must be a number"
                    return
                }
                set(number)
            }
        }
        text(label, initial.label, "analyte_label") { patch.analyteLabel = $0 }
        text(value, initial.value, "value_text") { patch.valueText = $0 }
        number(numeric, initial.numeric, "value_numeric") { patch.valueNumeric = $0 }
        text(comparator, initial.comparator, "comparator") { patch.comparator = .init(rawValue: $0) }
        text(unit, initial.unit, "unit_text") { patch.unitText = $0 }
        text(range, initial.range, "reference_range_text") { patch.referenceRangeText = $0 }
        number(low, initial.low, "ref_low") { patch.refLow = $0 }
        number(high, initial.high, "ref_high") { patch.refHigh = $0 }
        text(flag, initial.flag, "printed_flag") { patch.printedFlag = $0 }
        text(collected, initial.collected, "collected_at") { patch.collectedAt = $0 }
        text(analyte, initial.analyte, "analyte") { patch.analyte = $0 }
        return (patch, errors)
    }

    static let cannotClear = "Clearing a field is not available in the app yet; enter what is printed, or clear it in the web panel."

    /// A decimal in the person's locale or with a point.
    static func number(_ text: String) -> Double? {
        (try? Double(text, format: .number)) ?? Double(text.replacingOccurrences(of: ",", with: "."))
    }
}

/// Review of one extracted row (`PATCH /extractions/{id}/rows/{row}`, changed fields only): edit
/// what was read, accept it, or reject it. Checks point at what to compare with the PDF and never
/// rate the value. It is a sheet over the review that leaves the PDF page and its outline in view;
/// a saved or newly selected row resets the form.
struct RowEditor: View {
    @Environment(AppState.self) private var state
    let row: ExtractionRow
    let runID: String
    let codes: [String]
    let onSaved: (ExtractionRow) -> Void
    let onClose: () -> Void
    @State private var form: RowForm
    @State private var problem: Problem?
    @State private var localErrors: [String: String] = [:]
    @State private var isBusy = false
    @FocusState private var focused: String?

    init(row: ExtractionRow, runID: String, codes: [String], onSaved: @escaping (ExtractionRow) -> Void, onClose: @escaping () -> Void) {
        self.row = row
        self.runID = runID
        self.codes = codes
        self.onSaved = onSaved
        self.onClose = onClose
        _form = State(initialValue: RowForm(row))
    }

    static let fields: Set<String> = [
        "analyte_label", "value_text", "unit_text", "reference_range_text", "printed_flag", "collected_at",
        "value_numeric", "comparator", "ref_low", "ref_high", "analyte",
    ]

    private var initial: RowForm { RowForm(row) }
    private var isDirty: Bool { form != initial }
    private var notes: [String] { row.validation + row.warnings }

    var body: some View {
        NavigationStack {
            List {
                Section {
                    field("Value", text: $form.value, key: "value_text")
                    field("Unit", text: $form.unit, key: "unit_text", hint: row.unitText == nil ? "Leave empty to confirm the value as unitless." : nil)
                    field("Range", text: $form.range, key: "reference_range_text")
                    field("Flag", text: $form.flag, key: "printed_flag", hint: "Only what the report prints, such as H or L.")
                    field("Collected", text: $form.collected, key: "collected_at", hint: "YYYY-MM-DD or YYYY-MM-DDTHH:MM, local time as printed.")
                    field("Label", text: $form.label, key: "analyte_label")
                } header: {
                    VStack(alignment: .leading, spacing: 4) {
                        LabStatus(row.reviewStatus)
                        Text("Confidence \(row.confidence.formatted(.number.precision(.fractionLength(2)))), an extractor hint only · \(notes.isEmpty ? "no checks" : LabText.plural(notes.count, "check") + " to compare with the PDF")")
                    }
                    .textCase(nil)
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("rowMeta")
                }

                if let problem, !problem.fieldErrors.contains(where: { Self.fields.contains(String($0.pointer.dropFirst())) }) {
                    // Field messages show under their field; anything else here.
                    Section { ProblemView(problem: problem) }
                }

                Section("Compare with the PDF") {
                    ForEach(notes, id: \.self) { code in
                        Label(LabText.warning(code), systemImage: "exclamationmark.triangle").font(.footnote)
                    }
                    Text("Printed text: “\(row.evidenceText)”").font(.footnote).accessibilityIdentifier("evidence")
                }

                Section {
                    AnalyteField(analyte: $form.analyte, suggested: row.suggestedAnalyte, codes: codes)
                    error("analyte")
                    field("Numeric value", text: $form.numeric, key: "value_numeric", decimal: true)
                    Picker("Comparator", selection: $form.comparator) {
                        ForEach(RowForm.comparators, id: \.self) { Text($0.isEmpty ? "None" : $0).tag($0) }
                    }
                    .accessibilityIdentifier("field-comparator")
                    error("comparator")
                    field("Range low", text: $form.low, key: "ref_low", decimal: true)
                    field("Range high", text: $form.high, key: "ref_high", decimal: true)
                } header: {
                    Text("Read as")
                } footer: {
                    Text("An empty analyte records it as unknown; printed values are kept either way.")
                }

                if !row.edits.isEmpty {
                    Section {
                        DisclosureGroup("Review trail (\(row.edits.count))") {
                            ForEach(Array(row.edits.enumerated()), id: \.offset) { _, edit in
                                Text(trail(edit)).font(.footnote)
                            }
                        }
                        .accessibilityIdentifier("reviewTrail")
                    }
                }
            }
            .listStyle(.insetGrouped)
            .navigationTitle("Row \(row.index + 1)\(row.page.map { ", page \($0)" } ?? "") · \(row.analyteLabel)")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onClose).accessibilityIdentifier("closeRow")
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(isDirty ? "Save and accept" : "Accept as read") {
                        Task { await send(.accept) }
                    }
                    .disabled(isBusy)
                    .accessibilityIdentifier("acceptRow")
                }
                ToolbarItemGroup(placement: .bottomBar) {
                    if row.reviewStatus != .rejected {
                        Button("Reject row", systemImage: "xmark", role: .destructive) {
                            Task { await send(.reject) }
                        }
                        .labelStyle(.titleAndIcon)
                        .disabled(isBusy)
                        .accessibilityIdentifier("rejectRow")
                    }
                    Spacer()
                    if isDirty {
                        Button("Undo changes", systemImage: "arrow.uturn.backward") {
                            form = initial
                            localErrors = [:]
                        }
                        .labelStyle(.titleAndIcon)
                        .disabled(isBusy)
                        .accessibilityIdentifier("undoRow")
                    }
                }
            }
        }
        .sheetBackground()
        .onChange(of: row) {
            // Another row, or this one saved: start from what the server holds now.
            form = RowForm(row)
            problem = nil
            localErrors = [:]
        }
    }


    private func field(_ title: String, text: Binding<String>, key: String, hint: String? = nil, decimal: Bool = false) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            LabeledContent(title) {
                TextField(title, text: text, prompt: Text("–"))
                    .multilineTextAlignment(.trailing)
                    .keyboardType(decimal ? .decimalPad : .default)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .focused($focused, equals: key)
                    .accessibilityIdentifier("field-\(key)")
            }
            if let hint { Text(hint).font(.caption).foregroundStyle(.secondary) }
            LabFieldError(text: message(key))
        }
    }

    private func error(_ key: String) -> some View {
        LabFieldError(text: message(key))
    }

    private func message(_ key: String) -> String? {
        localErrors[key] ?? problem?.detail(for: "/\(key)")
    }

    private func send(_ review: Components.Schemas.ExtractionRowPatch.ReviewPayload) async {
        guard let client = state.client else { return }
        focused = nil // the keyboard would cover the sheet's bottom bar
        var body = Components.Schemas.ExtractionRowPatch(review: review)
        if review == .accept {
            let (patch, errors) = form.patch(from: initial)
            localErrors = errors
            guard errors.isEmpty else { return }
            body = patch
            body.review = .accept
        }
        problem = nil
        isBusy = true
        defer { isBusy = false }
        do {
            let saved = try await client.updateExtractionRow(path: .init(id: runID, row: String(row.index)), body: .json(body)).ok.body.json
            onSaved(saved)
        } catch {
            problem = Problem(error)
        }
    }

    private func trail(_ edit: Components.Schemas.ExtractionRowEdit) -> String {
        let what = switch edit.action {
        case .edit: "edited " + edit.changes.value.keys.sorted().map { $0.replacingOccurrences(of: "_", with: " ") }.joined(separator: ", ")
        case .accept: "accepted"
        case .reject: "rejected"
        }
        return "\(Format.instant(edit.createdAt)): \(what) by \(edit.actor)"
    }
}

/// The analyte code, the label's alias match to take over, and the known codes to pick from.
private struct AnalyteField: View {
    @Binding var analyte: String
    let suggested: String?
    let codes: [String]

    var body: some View {
        LabeledContent("Analyte") {
            HStack {
                TextField("Analyte", text: $analyte, prompt: Text("unknown"))
                    .multilineTextAlignment(.trailing)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .font(.body.monospaced())
                    .accessibilityIdentifier("field-analyte")
                if !codes.isEmpty {
                    Menu {
                        ForEach(codes, id: \.self) { code in Button(code) { analyte = code } }
                    } label: {
                        Label("Known analytes", systemImage: "list.bullet")
                    }
                    .labelStyle(.iconOnly)
                    .accessibilityIdentifier("analyteCodes")
                }
            }
        }
        if let suggested, suggested != analyte.trimmingCharacters(in: .whitespaces) {
            HStack {
                Text("Label alias match: \(Text(suggested).monospaced())")
                Spacer()
                Button("Use") { analyte = suggested }
                    .buttonStyle(.borderless)
                    .accessibilityIdentifier("useSuggestion")
            }
            .font(.footnote)
        }
    }
}
