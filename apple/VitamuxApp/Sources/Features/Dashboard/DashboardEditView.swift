import SwiftUI
import VitamuxKit

/// Edit mode: reorder (drag, or Move up and down), S/M/L, hide and show, add metric, reset to the
/// curated default, then Cancel or Save (`PUT /settings/dashboard`). The layout is the panel's.
struct DashboardEditView: View {
    @Environment(AppState.self) private var state
    let model: DashboardModel
    @State private var isAdding = false

    var body: some View {
        NavigationStack {
            List {
                if let problem = model.saveProblem {
                    Section {
                        Label(problem.title, systemImage: "exclamationmark.triangle").foregroundStyle(Color.feedbackError)
                        if let detail = problem.detail { Text(detail).font(.footnote) }
                    }
                }
                Section {
                    ForEach(visible, id: \.metric) { card in
                        EditRow(card: card, model: model, isFirst: card.metric == visible.first?.metric,
                                isLast: card.metric == visible.last?.metric)
                    }
                    .onMove { model.move(fromOffsets: $0, toOffset: $1) }
                } header: {
                    Text("Drag to reorder. The layout is saved on your server and shared with the web panel.")
                        .textCase(nil)
                }
                Section("Hidden · \(hidden.count)") {
                    if hidden.isEmpty {
                        Text("Cards you hide wait here.").foregroundStyle(.secondary)
                    }
                    ForEach(hidden, id: \.metric) { card in
                        HStack {
                            Text(DashboardLayout.label(card.metric))
                            Spacer()
                            Button("Show", systemImage: "eye") { model.setHidden(false, card.metric) }
                                .labelStyle(.iconOnly)
                                .buttonStyle(.borderless)
                                .accessibilityLabel("Show \(DashboardLayout.label(card.metric))")
                                .accessibilityIdentifier("show-\(card.metric)")
                        }
                    }
                }
                Section {
                    Button("Add metric", systemImage: "plus") { isAdding = true }
                        .accessibilityIdentifier("addMetric")
                    Button("Reset to default", systemImage: "arrow.counterclockwise") { model.resetToDefault() }
                        .accessibilityIdentifier("resetLayout")
                }
            }
            .environment(\.editMode, .constant(.active))
            .navigationTitle("Customize")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { model.cancelEditing() }
                        .accessibilityIdentifier("editCancel")
                }
                ToolbarItem(placement: .confirmationAction) {
                    if model.isSaving {
                        ProgressView().accessibilityLabel("Loading")
                    } else {
                        Button("Save") { save() }
                            .accessibilityIdentifier("editSave")
                    }
                }
            }
            .sheet(isPresented: $isAdding) {
                AddMetricView(model: model)
            }
        }
        .sheetBackground()
        .interactiveDismissDisabled(model.draft != model.layout.value?.cards)
    }

    private var visible: [DashboardCard] { (model.draft ?? []).filter { !$0.hidden } }
    private var hidden: [DashboardCard] { (model.draft ?? []).filter(\.hidden) }

    private func save() {
        guard let client = state.client else { return }
        Task { await model.save(client) }
    }
}

/// A visible card: its name, the size, Move up and down, and hide.
private struct EditRow: View {
    let card: DashboardCard
    let model: DashboardModel
    let isFirst: Bool
    let isLast: Bool

    var body: some View {
        let label = DashboardLayout.label(card.metric)
        HStack(spacing: 8) {
            Text(label)
                .lineLimit(2)
                .frame(maxWidth: .infinity, alignment: .leading)
            Picker("Size", selection: Binding { card.size } set: { model.setSize($0, of: card.metric) }) {
                ForEach(CardSize.allCases, id: \.self) { Text($0.label).tag($0) }
            }
            .pickerStyle(.segmented)
            .fixedSize()
            .accessibilityLabel("Size of \(label)")
            .accessibilityIdentifier("size-\(card.metric)")
            Menu {
                Button("Move up", systemImage: "arrow.up") { model.move(card.metric, by: -1) }
                    .disabled(isFirst)
                Button("Move down", systemImage: "arrow.down") { model.move(card.metric, by: 1) }
                    .disabled(isLast)
            } label: {
                Label("Move \(label)", systemImage: "arrow.up.arrow.down")
                    .labelStyle(.iconOnly)
            }
            .accessibilityIdentifier("move-\(card.metric)")
            Button("Hide \(label)", systemImage: "eye.slash") { model.setHidden(true, card.metric) }
                .labelStyle(.iconOnly)
                .accessibilityIdentifier("hide-\(card.metric)")
        }
        .buttonStyle(.borderless)
        .accessibilityElement(children: .contain)
        .accessibilityActions {
            if !isFirst { Button("Move up") { model.move(card.metric, by: -1) } }
            if !isLast { Button("Move down") { model.move(card.metric, by: 1) } }
        }
        .accessibilityIdentifier("edit-\(card.metric)")
    }
}

/// "Add metric": the catalogue (`GET /metrics`) by section, searchable. Sleep stages and blood
/// pressure parts are offered as the one card they belong to.
private struct AddMetricView: View {
    @Environment(\.dismiss) private var dismiss
    let model: DashboardModel
    @State private var query = ""

    private struct Entry: Identifiable {
        var code: String
        var label: String
        var unit: String
        var id: String { code }
    }

    var body: some View {
        NavigationStack {
            List {
                ForEach(sections, id: \.name) { section in
                    Section(section.name) {
                        ForEach(section.entries) { entry in
                            row(entry)
                        }
                    }
                }
            }
            .overlay {
                if sections.isEmpty {
                    ContentUnavailableView.search(text: query)
                }
            }
            .searchable(text: $query, placement: .navigationBarDrawer(displayMode: .always), prompt: "Weight, body fat, steps")
            .navigationTitle("Add metric")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }.accessibilityIdentifier("addMetricDone")
                }
            }
        }
        .sheetBackground()
    }

    private func row(_ entry: Entry) -> some View {
        let pinned = model.draft?.contains { $0.metric == entry.code } == true
        return HStack {
            VStack(alignment: .leading) {
                Text(entry.label)
                if !entry.unit.isEmpty { Text(entry.unit).font(.caption).foregroundStyle(.secondary) }
            }
            Spacer()
            Button(pinned ? "Pinned" : "Pin") {
                if pinned { model.unpin(entry.code) } else { model.pin(entry.code) }
            }
            .buttonStyle(.bordered)
            .tint(pinned ? .accentColor : .secondary)
            .accessibilityLabel(pinned ? "Unpin \(entry.label)" : "Pin \(entry.label)")
            .accessibilityIdentifier("pin-\(entry.code)")
        }
    }

    /// One entry per card, grouped by catalogue section in catalogue order.
    private var sections: [(name: String, entries: [Entry])] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        var out: [(name: String, entries: [Entry])] = []
        var seen: Set<String> = []
        for metric in model.catalogue {
            let code = DashboardLayout.card(of: metric)
            guard seen.insert(code).inserted else { continue }
            let label = DashboardLayout.label(code)
            if !q.isEmpty, !label.lowercased().contains(q), !code.contains(q) { continue }
            let name = code == "sleep" ? "Sleep" : code == "blood_pressure" ? "Blood pressure" : metric.section
            let entry = Entry(code: code, label: label, unit: code == metric.code ? metric.unit : "")
            if let index = out.firstIndex(where: { $0.name == name }) {
                out[index].entries.append(entry)
            } else {
                out.append((name, [entry]))
            }
        }
        return out
    }
}
