import Foundation
import Observation
import SwiftUI
import VitamuxKit

/// The provenance chain of one record (`GET /provenance/{entity}/{id}`, the panel's
/// ProvenanceDialog): every earlier version, the record and every later version, each with its
/// connection, pushing client, batch, raw payload metadata and normalizer. Raw bodies are never
/// shown.
@Observable
final class ProvenanceModel {
    private(set) var trace: Loadable<Components.Schemas.Provenance> = .loading

    func load(_ request: ProvenanceRequest, client: Client?) async {
        guard let client else { return }
        trace = await Loadable { try await client.getProvenance(path: .init(entity: request.entity, id: request.recordID)).ok.body.json }
    }
}

struct ProvenanceSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let request: ProvenanceRequest
    @State private var model = ProvenanceModel()

    var body: some View {
        NavigationStack {
            List {
                switch model.trace {
                case .loading:
                    ProgressView("Loading the chain").frame(maxWidth: .infinity)
                case .failed(let problem):
                    ProblemView(problem: problem)
                case .loaded(let trace):
                    let chain = trace.earlier.map { ($0, "Earlier version") } + [(trace.row, "This version")] + trace.later.map { ($0, "Later version") }
                    ForEach(chain, id: \.0.id) { version, role in
                        VersionSection(version: version, role: role)
                    }
                }
            }
            .accessibilityIdentifier("provenanceChain")
            .navigationTitle("Provenance of \(request.entity.rawValue) \(request.recordID)")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() }.accessibilityIdentifier("closeProvenance") }
            }
        }
        .task { await model.load(request, client: state.client) }
    }
}

private struct VersionSection: View {
    let version: Components.Schemas.ProvenanceVersion
    let role: String

    var body: some View {
        Section {
            LabeledContent("State", value: state)
            LabeledContent("Source", value: "\(providerLabel(version.provider)) · \(version.connectionMode)")
            LabeledContent("Connection") { Text(version.connectionId).font(.caption.monospaced()) }
            if let client = version.client { LabeledContent("Pushed by", value: "\(client.name) (\(client.kind))") }
            if let batch = version.batch {
                LabeledContent("Batch", value: "\(batch.sourceKind) · received \(Format.instant(batch.receivedAt))")
            }
            if let raw = version.raw {
                LabeledContent("Raw payload") {
                    Text("\(raw.id) · stream \(raw.stream) · key \(raw.externalKey) · version \(raw.version) · \(raw.sizeBytes.formatted()) bytes · sha256 \(raw.contentSha256.prefix(12)) · \(raw.status)")
                        .font(.caption)
                        .multilineTextAlignment(.trailing)
                }
            } else {
                LabeledContent("Raw payload", value: "None (migrated without raw)")
            }
            LabeledContent("Normalizer", value: "\(version.normalizer.name)@\(version.normalizer.version) (\(version.normalizer.gitSha.prefix(8)))")
                .accessibilityIdentifier("normalizer")
            if let fetched = version.fetchedAt { LabeledContent("Fetched", value: Format.instant(fetched)) }
            LabeledContent("Ingested", value: Format.instant(version.ingestedAt))
            LabeledContent("Normalized", value: Format.instant(version.normalizedAt))
            if let corrected = version.correctedAt { LabeledContent("Corrected", value: Format.instant(corrected)) }
            if let by = version.supersededBy { LabeledContent("Replaced by", value: "#\(by)") }
            if let deleted = version.deletedBy { LabeledContent("Deleted by raw", value: deleted.rawId) }
            DisclosureGroup("Stored row") {
                Text(storedRow).font(.caption.monospaced()).textSelection(.enabled)
            }
        } header: {
            Text("\(role) #\(version.id)")
                .accessibilityIdentifier(role == "This version" ? "thisVersion" : "version-\(version.id)")
        }
    }

    private var state: String {
        if let deleted = version.deletedAt { return "Deleted upstream \(Format.instant(deleted))" }
        if let superseded = version.supersededAt { return "Superseded \(Format.instant(superseded))" }
        return "Active"
    }

    private var storedRow: String {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        return (try? encoder.encode(version.record)).flatMap { String(data: $0, encoding: .utf8) } ?? "–"
    }
}
