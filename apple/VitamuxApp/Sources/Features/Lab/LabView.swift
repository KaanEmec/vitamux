import SwiftUI
import VitamuxKit

/// Tab root (`vitamux://lab`): add a PDF (scan, Files, or "Open in Vitamux" from another app),
/// the documents with their status, and the action each allows: extract, review, open or delete.
/// Nothing is saved as a result until a review is confirmed.
struct LabView: View {
    @Environment(AppState.self) private var state
    @State private var model = LabModel()
    @State private var intake = LabIntake()
    @State private var extracting: DocumentAction?
    @State private var deleting: LabDocument?

    var body: some View {
        List {
            Section {
                NavigationLink(value: Route.labResults) {
                    Label("Confirmed results", systemImage: "list.bullet.rectangle")
                }
                .accessibilityIdentifier("openResults")
            } footer: {
                Text("Each extraction is reviewed row by row against the PDF before anything is saved as a result. Ranges and flags are shown as the lab printed them.")
            }
            IntakeSection(intake: intake, model: model)
            DocumentsSection(model: model, extracting: $extracting, deleting: $deleting)
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Lab")
        .toolbar {
            ToolbarItem(placement: .primaryAction) { AddDocumentMenu(intake: intake) }
        }
        .labIntake(intake, client: state.client)
        .refreshable { await model.load(state.client) }
        .task { await model.load(state.client) }
        .onChange(of: intake.isUploading) { _, uploading in
            guard !uploading else { return }
            model.show(nil)
            Task { await model.load(state.client) }
        }
        .sheet(item: $extracting) { action in
            ExtractSheet(document: action.document) { run in
                Task { await model.load(state.client) }
                state.paths[.lab, default: []].append(.labDocument(id: run.documentId))
            }
        }
        .deleteDocumentDialog($deleting) { document, derived in
            intake.reset()
            Task { await model.delete(document, derived: derived, client: state.client) }
        }
    }
}

/// A document an action sheet is about.
struct DocumentAction: Identifiable {
    let document: LabDocument
    var id: String { document.id }
}

/// Uploading, and what the last upload or deletion answered.
private struct IntakeSection: View {
    let intake: LabIntake
    let model: LabModel

    var body: some View {
        if intake.isUploading || intake.problem != nil || intake.notice != nil || model.notice != nil || model.problem != nil {
            Section {
                if intake.isUploading {
                    ProgressView("Uploading…").accessibilityIdentifier("uploading")
                }
                if let problem = intake.problem ?? model.problem {
                    VStack(alignment: .leading, spacing: 4) {
                        Label(problem.title, systemImage: "exclamationmark.triangle").font(.subheadline.weight(.semibold))
                        if let detail = problem.detail { Text(detail).font(.subheadline) }
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("labProblem")
                }
                if let notice = intake.notice ?? model.notice { LabNotice(text: notice) }
            }
        }
    }
}

private struct DocumentsSection: View {
    let model: LabModel
    @Binding var extracting: DocumentAction?
    @Binding var deleting: LabDocument?

    var body: some View {
        Section {
            switch model.documents {
            case .loading:
                ProgressView("Loading documents").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let documents) where documents.isEmpty:
                ContentUnavailableView {
                    Label("No documents yet", systemImage: "doc.text")
                } description: {
                    Text("Scan a lab report or choose a PDF to begin.")
                }
            case .loaded(let documents):
                ForEach(documents, id: \.id) { document in
                    DocumentRow(document: document, extracting: $extracting, deleting: $deleting)
                }
            }
        } header: {
            Text("Documents")
        } footer: {
            Text("A PDF stays on your Vitamux server. It is sent to an AI provider only if you pick one and consent for that document. You can also share a PDF to Vitamux from another app.")
        }
    }
}

private struct DocumentRow: View {
    @Environment(AppState.self) private var state
    let document: LabDocument
    @Binding var extracting: DocumentAction?
    @Binding var deleting: LabDocument?

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            VStack(alignment: .leading, spacing: 2) {
                Text(document.name).font(.body.weight(.semibold)).lineLimit(2)
                Text("\(Format.instant(document.uploadedAt)) · \(LabText.plural(document.pageCount, "page")) · \(LabText.size(document.sizeBytes))")
                    .font(.footnote).foregroundStyle(.secondary)
            }
            .accessibilityElement(children: .combine)
            HStack {
                LabStatus(document.status).accessibilityIdentifier("status-\(document.id)")
                Spacer()
                actions
            }
        }
        .padding(.vertical, 2)
        .swipeActions {
            if document.status != .deleted {
                Button("Delete", systemImage: "trash", role: .destructive) { deleting = document }
            }
        }
    }

    @ViewBuilder private var actions: some View {
        switch document.status {
        case .uploaded:
            Button("Extract") { extracting = DocumentAction(document: document) }
                .buttonStyle(.borderedProminent)
                .accessibilityIdentifier("extract-\(document.id)")
        case .needsReview, .confirmed, .extracting:
            Button(document.status == .needsReview ? "Review" : "Open") {
                state.paths[.lab, default: []].append(.labDocument(id: document.id))
            }
            .buttonStyle(.bordered)
            .accessibilityIdentifier("open-\(document.id)")
        case .deleted:
            EmptyView()
        }
        if document.status != .deleted {
            Button("Delete", systemImage: "trash", role: .destructive) { deleting = document }
                .labelStyle(.iconOnly)
                .buttonStyle(.borderless)
                .accessibilityLabel("Delete \(document.name)")
                .accessibilityIdentifier("delete-\(document.id)")
        }
    }
}

extension View {
    /// Asks before deleting a document: the PDF, its filename and every run are destroyed, and the
    /// owner chooses whether the results confirmed from it stay (`derived=keep`) or go too.
    func deleteDocumentDialog(_ document: Binding<LabDocument?>, onDelete: @escaping (LabDocument, LabModel.Derived) -> Void) -> some View {
        confirmationDialog(
            "Delete \(document.wrappedValue?.name ?? "document")?",
            isPresented: Binding { document.wrappedValue != nil } set: { if !$0 { document.wrappedValue = nil } },
            titleVisibility: .visible,
            presenting: document.wrappedValue
        ) { doc in
            Button("Delete, keep its results", role: .destructive) { onDelete(doc, .keep) }
                .accessibilityIdentifier("deleteKeep")
            Button("Delete with its results", role: .destructive) { onDelete(doc, .delete) }
                .accessibilityIdentifier("deleteAll")
            Button("Cancel", role: .cancel) {}
        } message: { _ in
            Text("This destroys the PDF, its filename and every extraction run, and cannot be undone. Kept results keep their printed values, page and evidence text.")
        }
    }
}
