import Foundation
import Observation
import SwiftUI
import VitamuxKit

/// Confirmed results (the panel's `/lab/results`): every result (`GET /lab-results`, every page)
/// grouped by analyte, or by printed label when the analyte is unknown, newest first, as printed.
/// Each result opens its revisions; each group links to its analyte view.
@Observable
final class ResultsModel {
    struct Group: Identifiable {
        /// The analyte code, or `label:<printed label>` (the analyte view's address for it).
        var id: String
        var title: String
        var code: String?
        var items: [LabResult]
    }

    private(set) var all: Loadable<[LabResult]> = .loading
    var filter = ""

    func load(_ client: Client?) async {
        guard let client else { return }
        all = await Loadable {
            try await readAll { cursor in
                let page = try await client.listLabResults(query: .init(limit: 1000, cursor: cursor)).ok.body.json
                return (page.value2.labResults, nextCursor(page.value1))
            }
        }
    }

    var groups: [Group] {
        let query = filter.trimmingCharacters(in: .whitespaces).lowercased()
        var byKey: [String: Group] = [:]
        for result in all.value ?? [] {
            if !query.isEmpty, !(result.analyte ?? "").contains(query), !result.originalLabel.lowercased().contains(query) { continue }
            let key = result.analyte ?? "label:\(result.originalLabel)"
            byKey[key, default: Group(id: key, title: result.analyte ?? result.originalLabel, code: result.analyte, items: [])].items.append(result)
        }
        return byKey.values
            .map { group in
                var group = group
                group.items.sort { $0.collectedDate > $1.collectedDate }
                return group
            }
            .sorted { $0.title.localizedCaseInsensitiveCompare($1.title) == .orderedAscending }
    }
}

/// `vitamux://lab/results`.
struct ResultsView: View {
    @Environment(AppState.self) private var state
    @State private var model = ResultsModel()
    @State private var history: LabResultRef?

    var body: some View {
        List {
            switch model.all {
            case .loading:
                ProgressView("Loading results").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let results) where results.isEmpty:
                ContentUnavailableView {
                    Label("No confirmed results yet", systemImage: "list.bullet.rectangle")
                } description: {
                    Text("Add and review a lab report under Documents.")
                }
            case .loaded:
                Section {
                    Text("Values, units, ranges and flags as the lab printed them. Converted values appear only where a conversion for the printed unit is listed.")
                        .font(.footnote).foregroundStyle(.secondary)
                }
                let groups = model.groups
                if groups.isEmpty {
                    Text("No result matches the filter.").foregroundStyle(.secondary)
                }
                ForEach(groups) { group in
                    GroupSection(group: group, history: $history)
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Results")
        .searchable(text: $model.filter, prompt: "Analyte or label")
        .refreshable { await model.load(state.client) }
        .task { await model.load(state.client) }
        .sheet(item: $history) { HistorySheet(result: $0.result) }
    }
}

/// A result a history sheet is about.
struct LabResultRef: Identifiable {
    let result: LabResult
    var id: String { result.id }
}

private struct GroupSection: View {
    let group: ResultsModel.Group
    @Binding var history: LabResultRef?

    var body: some View {
        Section {
            ForEach(group.items, id: \.id) { result in
                Button { history = LabResultRef(result: result) } label: {
                    ResultLine(result: result)
                }
                .accessibilityHint("Shows its revisions")
                .accessibilityIdentifier("result-\(result.id)")
            }
            NavigationLink(value: Route.analyte(code: group.id)) {
                Label("View over time", systemImage: "chart.xyaxis.line")
            }
            .accessibilityIdentifier("trend-\(group.id)")
        } header: {
            VStack(alignment: .leading, spacing: 2) {
                if let code = group.code {
                    Text(code).font(.subheadline.monospaced())
                } else {
                    Text("\(group.title) (unknown analyte)")
                }
                if let latest = group.items.first {
                    Text("\(LabText.plural(group.items.count, "result")) · latest \(latest.printedValue) \(latest.unitText ?? "") on \(Format.day(latest.collectedDate, weekday: false))")
                        .textCase(nil)
                }
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("group-\(group.id)")
        }
    }
}

private struct ResultLine: View {
    let result: LabResult

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline) {
                Text(Format.day(result.collectedDate, weekday: false)).foregroundStyle(.primary)
                Spacer()
                Text(result.printedValue).font(.body.weight(.semibold)).monospacedDigit().foregroundStyle(.primary)
                Text(unitLabel(result.unitText ?? "")).font(.caption).foregroundStyle(.secondary)
            }
            Grid(alignment: .leading, horizontalSpacing: 8, verticalSpacing: 2) {
                GridRow { Text("Label as printed").foregroundStyle(.secondary); Text(result.originalLabel) }
                GridRow { Text("Range as printed").foregroundStyle(.secondary); Text(result.referenceRangeText ?? "–") }
                GridRow { Text("Flag as printed").foregroundStyle(.secondary); Text(result.printedFlag ?? "–") }
                if let value = result.canonicalValue, let unit = result.canonicalUnit {
                    GridRow { Text("Converted").foregroundStyle(.secondary); Text("\(Format.number(value)) \(unit)") }
                }
                GridRow { Text("Laboratory").foregroundStyle(.secondary); Text(result.provenance.laboratory ?? "–") }
                if result.revision > 1 {
                    GridRow { Text("Revisions").foregroundStyle(.secondary); Text("\(result.revision)") }
                }
            }
            .font(.footnote)
            .foregroundStyle(.primary)
        }
        .accessibilityElement(children: .combine)
    }
}

/// Revisions of one confirmed result (`GET /lab-results/{id}/history`), newest (current) first,
/// with where it was read from.
struct HistorySheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let result: LabResult
    @State private var revisions: Loadable<[LabResult]> = .loading

    var body: some View {
        NavigationStack {
            List {
                switch revisions {
                case .loading:
                    ProgressView("Loading history").frame(maxWidth: .infinity)
                case .failed(let problem):
                    ProblemView(problem: problem)
                case .loaded(let revisions):
                    Section("Revisions") {
                        ForEach(Array(revisions.enumerated()), id: \.element.revision) { index, revision in
                            RevisionRow(revision: revision, current: index == 0)
                        }
                    }
                    Section("Provenance") {
                        Text(provenance).font(.footnote)
                        if let evidence = result.evidenceText {
                            Text("Printed text: “\(evidence)”").font(.footnote)
                        }
                    }
                }
            }
            .navigationTitle("History of \(result.originalLabel)")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }.accessibilityIdentifier("closeHistory")
                }
            }
            .task {
                guard let client = state.client else { return }
                revisions = await Loadable { try await client.getLabResultHistory(path: .init(id: result.id)).ok.body.json.revisions }
            }
        }
        .accessibilityIdentifier("historySheet")
    }

    private var provenance: String {
        let p = result.provenance
        return "Confirmed by \(p.confirmedBy) on \(Format.instant(p.confirmedAt)) from page \(result.page.map(String.init) ?? "–"), read by \(LabText.readBy(p.provider, model: p.model)), prompt \(p.promptVersion), schema \(p.schemaVersion)."
    }
}

private struct RevisionRow: View {
    let revision: LabResult
    let current: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(alignment: .firstTextBaseline) {
                Text("Revision \(revision.revision)\(current ? " (current)" : "")").font(.subheadline.weight(.semibold))
                Spacer()
                Text("\(revision.printedValue) \(unitLabel(revision.unitText ?? ""))").monospacedDigit()
            }
            Text("\(revision.originalLabel) · range as printed \(revision.referenceRangeText ?? "–") · flag as printed \(revision.printedFlag ?? "–")")
                .font(.footnote).foregroundStyle(.secondary)
            Text("Written \(Format.instant(revision.updatedAt))").font(.caption).foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("revision-\(revision.revision)")
    }
}
