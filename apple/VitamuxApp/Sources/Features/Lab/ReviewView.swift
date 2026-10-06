import PDFKit
import SwiftUI
import VitamuxKit

/// Review of one document (`vitamux://lab/documents/{id}`): the PDF page with the selected row's
/// evidence outlined above the rows, each row's editor, and confirm. Rows show what was printed;
/// nothing is rated. A run in progress is polled every 2 s while the screen is visible.
struct ReviewView: View {
    @Environment(AppState.self) private var state
    let id: String
    @State private var model: ReviewModel
    @State private var extracting: DocumentAction?
    @State private var deleting: LabDocument?

    init(id: String) {
        self.id = id
        _model = State(initialValue: ReviewModel(id: id))
    }

    var body: some View {
        content
            .navigationTitle("Review")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                if let document = model.document.value, document.status != .deleted {
                    ToolbarItem(placement: .primaryAction) {
                        ReviewMenu(model: model, extract: { extracting = DocumentAction(document: document) }, delete: { deleting = document })
                    }
                }
            }
            .task(id: id) {
                // A link to another document can land on this screen: start over for its id.
                if model.id != id { model = ReviewModel(id: id) }
                await model.load(state.client)
            }
            .task(id: model.isActive) {
                guard model.isActive else { return }
                await Poller.extraction.run {
                    await model.loadRuns(state.client)
                    return model.isActive
                }
            }
            .sheet(item: $extracting) { action in
                ExtractSheet(document: action.document) { run in
                    Task { await model.started(run, client: state.client) }
                }
            }
            .deleteDocumentDialog($deleting) { _, derived in
                Task {
                    if await model.delete(derived: derived, client: state.client) {
                        state.paths[.lab] = []
                    }
                }
            }
    }

    @ViewBuilder private var content: some View {
        switch model.document {
        case .loading:
            ProgressView("Loading document").frame(maxWidth: .infinity, maxHeight: .infinity)
        case .failed(let problem):
            ProblemView(problem: problem)
        case .loaded(let document):
            VStack(spacing: 0) {
                if model.isReviewable, let pdf = model.pdf {
                    PDFPageView(
                        document: pdf, page: $model.page, box: model.box,
                        rowLabel: model.row.map { "Row \($0.index + 1) (\($0.analyteLabel))" }
                    )
                    .frame(height: 280)
                    Divider()
                }
                ReviewList(model: model, document: document)
            }
        }
    }
}

private struct ReviewMenu: View {
    @Environment(AppState.self) private var state
    let model: ReviewModel
    let extract: () -> Void
    let delete: () -> Void

    var body: some View {
        Menu {
            Button(model.runs.isEmpty ? "Extract" : "Extract again", systemImage: "text.viewfinder", action: extract)
                .disabled(model.isActive)
                .accessibilityIdentifier("extractAgain")
            if model.runs.count > 1 {
                Picker("Extraction run", selection: Binding { model.runID ?? "" } set: { id in
                    Task { await model.pick(run: id, client: state.client) }
                }) {
                    ForEach(model.runs, id: \.id) { run in
                        Text("\(Format.instant(run.createdAt)) · \(LabText.readBy(run.provider, model: run.model))").tag(run.id)
                    }
                }
            }
            Button("Delete document", systemImage: "trash", role: .destructive, action: delete)
                .accessibilityIdentifier("deleteDocument")
        } label: {
            Label("Document actions", systemImage: "ellipsis.circle")
        }
        .accessibilityIdentifier("reviewActions")
    }
}

private struct ReviewList: View {
    @Environment(AppState.self) private var state
    let model: ReviewModel
    let document: LabDocument

    var body: some View {
        ScrollViewReader { proxy in
            List {
                ReviewHeader(model: model, document: document)
                if !model.blockers.isEmpty || model.notice != nil || (model.confirmProblem != nil && model.confirmProblem?.status != 422) {
                    ConfirmStatus(model: model) { model.select($0) }
                        .id("confirmStatus")
                }
                RunState(model: model, document: document)
                if model.isReviewable {
                    RowsSection(model: model)
                }
            }
            .listStyle(.insetGrouped)
            .accessibilityIdentifier("reviewList")
            .sheet(isPresented: Binding { model.row != nil && model.isReviewable } set: { if !$0 { model.deselect() } }) {
                if let row = model.row, let run = model.run {
                    RowEditor(row: row, runID: run.id, codes: model.codes) { saved in
                        Task { await model.saved(saved, client: state.client) }
                    } onClose: {
                        model.deselect()
                    }
                    .presentationDetents([.medium, .large])
                    .presentationBackgroundInteraction(.enabled(upThrough: .medium))
                }
            }
            .safeAreaInset(edge: .bottom) {
                if model.isReviewable {
                    ConfirmBar(model: model) {
                        withAnimation { proxy.scrollTo("confirmStatus", anchor: .top) }
                    }
                }
            }
        }
    }
}

private struct ReviewHeader: View {
    let model: ReviewModel
    let document: LabDocument

    var body: some View {
        Section {
            VStack(alignment: .leading, spacing: 4) {
                Text(document.name).font(.headline)
                Text("Uploaded \(Format.instant(document.uploadedAt)) · \(LabText.plural(document.pageCount, "page"))")
                    .font(.footnote).foregroundStyle(.secondary)
                LabStatus(document.status).accessibilityIdentifier("documentStatus")
            }
            .accessibilityElement(children: .combine)
            if let run = model.run, model.isReviewable {
                let rows = model.rows
                VStack(alignment: .leading, spacing: 6) {
                    Text(readBy(run, rows: rows)).font(.footnote).foregroundStyle(.secondary)
                    ProgressView(value: Double(rows.count - model.pending), total: Double(max(rows.count, 1))) {
                        HStack {
                            Text("\(rows.count - model.pending) of \(rows.count) reviewed")
                            Spacer()
                            if model.withChecks > 0 { Text("\(model.withChecks) with checks") }
                        }
                        .font(.footnote)
                    }
                    .accessibilityIdentifier("reviewProgress")
                }
                ForEach(run.warnings, id: \.self) { code in
                    Label(LabText.warning(code), systemImage: "exclamationmark.triangle").font(.footnote)
                }
            }
        }
    }

    private func readBy(_ run: Extraction, rows: [ExtractionRow]) -> String {
        let when = Format.instant(run.finishedAt ?? run.createdAt)
        let pending = model.pending
        let progress = pending > 0 ? "\(pending) of \(rows.count) rows not reviewed yet." : "All \(rows.count) rows reviewed."
        return "Read by \(LabText.readBy(run.provider, model: run.model)) on \(when). \(progress) Nothing is saved as a result until you confirm."
    }
}

/// No run yet, a run in progress, a failed run, or the PDF's own state.
private struct RunState: View {
    let model: ReviewModel
    let document: LabDocument

    var body: some View {
        if let message {
            Section {
                Label(message.text, systemImage: message.symbol)
                    .accessibilityIdentifier(message.id)
            }
        }
    }

    private var message: (text: String, symbol: String, id: String)? {
        let run = model.run
        if document.status == .deleted { return ("The original PDF was deleted.", "trash", "pdfDeleted") }
        if run == nil, !model.isActive { return ("No extraction yet. Choose Extract to read the printed rows.", "text.viewfinder", "noExtraction") }
        if model.isActive, run == nil || run?.status == .queued || run?.status == .running {
            return ("Extraction in progress… \(LabText.errorClass(run?.errorClass) ?? "")", "hourglass", "extractionInProgress")
        }
        if let run, run.status == .failed {
            return ("The extraction failed. \(LabText.errorClass(run.errorClass) ?? "")", "xmark.octagon", "extractionFailed")
        }
        if model.isReviewable, model.pdf == nil {
            if let problem = model.pdfProblem { return ("The PDF could not be loaded: \(problem.title)", "exclamationmark.triangle", "pdfProblem") }
            return ("Loading PDF…", "doc", "pdfLoading")
        }
        if model.isReviewable, model.row?.bbox == nil, model.row != nil {
            return ("No row area was read for this row; compare the printed text instead.", "rectangle.dashed", "noRowArea")
        }
        return nil
    }
}

private struct RowsSection: View {
    let model: ReviewModel

    var body: some View {
        Section {
            ForEach(model.rows, id: \.index) { row in
                Button { model.select(row) } label: {
                    RowLine(row: row, selected: model.selected == row.index)
                }
                .listRowBackground(model.selected == row.index ? Color.accentColor.opacity(0.12) : nil)
                .accessibilityAddTraits(model.selected == row.index ? .isSelected : [])
                .accessibilityIdentifier("row-\(row.index)")
            }
        } header: {
            Text("Extracted rows")
        } footer: {
            Text("Select a row to compare it with the PDF and review it.")
        }
    }
}

/// One row: label, value and unit as printed, the range and flag as printed, checks and review state.
private struct RowLine: View {
    let row: ExtractionRow
    let selected: Bool

    var body: some View {
        HStack(alignment: .top) {
            VStack(alignment: .leading, spacing: 2) {
                Text(row.analyteLabel)
                    .font(.body.weight(selected ? .semibold : .regular))
                    .strikethrough(row.reviewStatus == .rejected)
                    .foregroundStyle(.primary)
                Text(detail).font(.footnote).foregroundStyle(.secondary)
            }
            Spacer()
            VStack(alignment: .trailing, spacing: 2) {
                LabStatus(row.reviewStatus)
                let checks = row.validation.count + row.warnings.count
                if checks > 0, row.reviewStatus == .pending {
                    Label(LabText.plural(checks, "check"), systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.secondary)
                }
            }
        }
        .accessibilityElement(children: .combine)
    }

    private var detail: String {
        var parts = ["\(LabText.printed(row.valueText, comparator: row.comparator)) \(row.unitText ?? "")".trimmingCharacters(in: .whitespaces)]
        if let range = row.referenceRangeText { parts.append(range) }
        if let flag = row.printedFlag { parts.append("flag as printed \(flag)") }
        return parts.joined(separator: " · ")
    }
}

/// What confirming answered: the rows still blocking it, with a way to each, or the result.
private struct ConfirmStatus: View {
    let model: ReviewModel
    let goTo: (ExtractionRow) -> Void

    var body: some View {
        Section {
            if let notice = model.notice {
                LabNotice(text: notice)
                NavigationLink("See results", value: Route.labResults).accessibilityIdentifier("seeResults")
            }
            if !model.blockers.isEmpty {
                Label("Not ready to confirm.", systemImage: "exclamationmark.octagon")
                    .font(.subheadline.weight(.semibold))
                    .accessibilityIdentifier("notReady")
                ForEach(model.blockers) { blocker in
                    VStack(alignment: .leading, spacing: 4) {
                        Text(blocker.text).font(.footnote)
                        if let row = blocker.row {
                            Button("Go to row \(row.index + 1)") { goTo(row) }
                                .buttonStyle(.borderless)
                                .accessibilityIdentifier("goToRow-\(row.index)")
                        }
                    }
                }
            } else if let problem = model.confirmProblem {
                ProblemView(problem: problem)
            }
        }
    }
}

private struct ConfirmBar: View {
    @Environment(AppState.self) private var state
    let model: ReviewModel
    let answered: () -> Void

    var body: some View {
        VStack(spacing: 8) {
            if model.run?.status == .confirmed {
                Text("Confirmed. Edits to rows apply after confirming again, which records a new revision of each changed result.")
                    .font(.caption).foregroundStyle(.secondary).frame(maxWidth: .infinity, alignment: .leading)
                HStack {
                    Button("Unconfirm") { act { await model.unconfirm(state.client) } }
                        .buttonStyle(.bordered)
                        .accessibilityIdentifier("unconfirm")
                    Button("Confirm again") { act { await model.confirm(state.client) } }
                        .buttonStyle(.borderedProminent)
                        .foregroundStyle(Color.onAccent)
                        .frame(maxWidth: .infinity, alignment: .trailing)
                        .accessibilityIdentifier("confirmAgain")
                }
            } else {
                Button { act { await model.confirm(state.client) } } label: {
                    Text(model.pending > 0 ? "Confirm results · \(LabText.plural(model.pending, "row")) still need\(model.pending == 1 ? "s" : "") review" : "Confirm results")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .foregroundStyle(Color.onAccent)
                .controlSize(.large)
                .accessibilityIdentifier("confirmResults")
            }
        }
        .disabled(model.isBusy)
        .padding(.horizontal)
        .padding(.vertical, 10)
        .background(.bar)
    }

    private func act(_ work: @escaping () async -> Void) {
        Task {
            await work()
            answered()
        }
    }
}
