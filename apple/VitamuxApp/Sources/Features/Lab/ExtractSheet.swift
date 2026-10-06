import Foundation
import Observation
import SwiftUI
import VitamuxKit

/// Starting an extraction (`POST /documents/{id}/extractions`, the panel's ExtractDialog): pick a
/// configured provider (`GET /extractors`). An external one sends the PDF off the server, so the
/// owner consents to that provider and model for this document first and the consent names them
/// verbatim (ADR-0013); the built-in test extractor needs none.
@Observable
final class ExtractModel {
    let document: LabDocument
    private(set) var extractors: Loadable<[Extractor]> = .loading
    var chosen: Extractor.IdPayload?
    var consent = false
    private(set) var problem: Problem?
    private(set) var isBusy = false

    init(document: LabDocument) {
        self.document = document
    }

    var selected: Extractor? {
        extractors.value?.first { $0.id == chosen }
    }

    var canStart: Bool {
        guard let selected, selected.enabled, !isBusy else { return false }
        return !selected.external || consent
    }

    func load(_ client: Client?) async {
        guard let client else { return }
        extractors = await Loadable { try await client.listExtractors().ok.body.json.extractors }
        if chosen == nil { chosen = extractors.value?.first(where: \.enabled)?.id }
    }

    func choose(_ id: Extractor.IdPayload) {
        guard chosen != id else { return }
        chosen = id
        consent = false
    }

    /// Starts the run; nil when it was refused (the problem is shown).
    func start(_ client: Client?) async -> Extraction? {
        guard let client, let selected, canStart else { return nil }
        isBusy = true
        defer { isBusy = false }
        problem = nil
        let consent = selected.external
            ? Components.Schemas.ExtractionConsent(provider: selected.id.rawValue, model: selected.model ?? "", acknowledgedAt: .now)
            : nil
        guard let provider = Components.Schemas.ExtractionInput.ProviderPayload(rawValue: selected.id.rawValue) else { return nil }
        do {
            return try await client.createExtraction(
                path: .init(id: document.id), body: .json(.init(provider: provider, consent: consent))
            ).accepted.body.json
        } catch {
            problem = Problem(error)
            if problem?.code == "consent_required" {
                // The configured model changed: show the current one and ask again.
                self.consent = false
                await load(client)
            }
            return nil
        }
    }
}

struct ExtractSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    @State private var model: ExtractModel
    let onStarted: (Extraction) -> Void

    init(document: LabDocument, onStarted: @escaping (Extraction) -> Void) {
        _model = State(initialValue: ExtractModel(document: document))
        self.onStarted = onStarted
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Text("Read the printed rows of \(model.document.name) into a table that you then review. Nothing is saved as a result until you confirm.")
                        .font(.subheadline).foregroundStyle(.secondary)
                }
                switch model.extractors {
                case .loading:
                    ProgressView("Loading providers").frame(maxWidth: .infinity)
                case .failed(let problem):
                    ProblemView(problem: problem)
                case .loaded(let extractors):
                    Section("Extractor") {
                        ForEach(extractors, id: \.id) { extractor in
                            ExtractorRow(extractor: extractor, selected: model.chosen == extractor.id) { model.choose(extractor.id) }
                        }
                    }
                    if let selected = model.selected, selected.external {
                        ConsentSection(model: model, extractor: selected)
                    }
                }
                if let problem = model.problem {
                    Section { ProblemView(problem: problem).accessibilityIdentifier("extractProblem") }
                }
            }
            .navigationTitle("Extract results")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button(model.selected?.external == true ? "Send and extract" : "Extract") {
                        Task {
                            if let run = await model.start(state.client) {
                                onStarted(run)
                                dismiss()
                            }
                        }
                    }
                    .disabled(!model.canStart)
                    .accessibilityIdentifier("startExtraction")
                }
            }
            .task { await model.load(state.client) }
        }
    }
}

private struct ExtractorRow: View {
    let extractor: Extractor
    let selected: Bool
    let choose: () -> Void

    var body: some View {
        Button(action: choose) {
            HStack(alignment: .top) {
                Image(systemName: selected ? "largecircle.fill.circle" : "circle")
                    .foregroundStyle(selected ? AnyShapeStyle(.tint) : AnyShapeStyle(.secondary))
                VStack(alignment: .leading, spacing: 2) {
                    Text(LabText.provider(extractor.id.rawValue)).foregroundStyle(.primary)
                    if let model = extractor.model { Text("Model \(model)").font(.footnote.monospaced()).foregroundStyle(.secondary) }
                    Text(hint).font(.footnote).foregroundStyle(.secondary)
                }
            }
        }
        .disabled(!extractor.enabled)
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(selected ? .isSelected : [])
        .accessibilityIdentifier("extractor-\(extractor.id.rawValue)")
    }

    private var hint: String {
        if !extractor.external { return "Runs on your server; reads only the synthetic test PDFs." }
        if extractor.enabled { return "Sends the PDF to \(LabText.provider(extractor.id.rawValue))." }
        return "Disabled. Enable it under Settings › AI providers."
    }
}

private struct ConsentSection: View {
    @Bindable var model: ExtractModel
    let extractor: Extractor

    var body: some View {
        let provider = LabText.provider(extractor.id.rawValue)
        let modelName = extractor.model ?? ""
        Section {
            Text("Extracting with \(provider) sends the whole PDF of \(model.document.name) to that provider, using the model \(modelName). It leaves your server and is handled under the provider's terms. The answer is stored encrypted with the document.")
                .font(.subheadline)
            Toggle("I consent to send this PDF to \(provider), model \(modelName).", isOn: $model.consent)
                .accessibilityIdentifier("consentToggle")
        } header: {
            Text("Consent for this document")
        }
    }
}
