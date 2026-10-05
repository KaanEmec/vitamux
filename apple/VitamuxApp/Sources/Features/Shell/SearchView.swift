import SwiftUI
import VitamuxKit

/// The search sheet every tab root opens: pick a result to go there.
struct SearchView: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let connections: [Components.Schemas.Connection]
    @State private var model = SearchModel()
    @State private var query = ""
    @State private var isPresented = true

    var body: some View {
        NavigationStack {
            let items = model.items(matching: query, connections: connections)
            List {
                ForEach(SearchModel.groups, id: \.self) { group in
                    let rows = items.filter { $0.group == group }
                    if !rows.isEmpty {
                        Section(group) {
                            ForEach(rows) { item in
                                ResultRow(item: item) { state.open(item.route) }
                            }
                        }
                    }
                }
            }
            .overlay {
                if items.isEmpty { ContentUnavailableView.search(text: query) }
            }
            .searchable(text: $query, isPresented: $isPresented, placement: .navigationBarDrawer(displayMode: .always),
                        prompt: "Metric, source, rule or setting")
            .navigationTitle("Search")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Close") { dismiss() } }
            }
            .task { await model.load(state.client) }
        }
    }
}

private struct ResultRow: View {
    let item: SearchModel.Item
    let open: () -> Void

    var body: some View {
        Button(action: open) {
            HStack {
                Text(item.label).foregroundStyle(.primary)
                Spacer()
                if let detail = item.detail {
                    Text(detail).font(.footnote.monospaced()).foregroundStyle(.secondary)
                }
            }
        }
        .accessibilityIdentifier("result-\(item.group)-\(item.detail ?? item.label)")
    }
}
