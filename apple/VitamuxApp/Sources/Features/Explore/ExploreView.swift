import SwiftUI
import VitamuxKit

/// Tab root (`vitamux://explore`, artboard Explore): everything stored, by section, with search,
/// source, device and origin filters, catalogue metrics without data, and pins.
struct ExploreView: View {
    @Environment(AppState.self) private var state
    @State private var model = ExploreModel()
    @State private var pins = Pins()

    var body: some View {
        List {
            switch model.inventory {
            case .loading:
                ProgressView("Loading the inventory").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let inventory):
                ExploreHeader(model: model, pending: inventory.aggregatesPending, pinProblem: pins.problem)
                ExploreSections(model: model, pins: pins, isEmpty: inventory.items.isEmpty)
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Explore")
        .searchable(text: $model.filter.text, placement: .navigationBarDrawer(displayMode: .always), prompt: "Metrics, devices, analytes")
        .refreshable { await ResponseCache.refreshing { await model.load(state.client) } }
        .task {
            async let pinsLoaded: Void = pins.load(state.client)
            await model.load(state.client)
            await pinsLoaded
        }
        .task(id: model.showEmpty) { await model.loadCatalogue(state.client) }
    }
}

/// Counts, the catching-up note and the filters.
private struct ExploreHeader: View {
    @Bindable var model: ExploreModel
    let pending: Bool
    let pinProblem: Problem?

    var body: some View {
        Section {
            if !model.counts.isEmpty {
                Text("Everything Vitamux has stored · \(model.counts)")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            if pending {
                Label("Counts are catching up while the hourly aggregates are rebuilt.", systemImage: "clock.arrow.circlepath")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("aggregatesPending")
            }
            if let pinProblem { ProblemView(problem: pinProblem) }
            if model.providers.count > 1 { SourceChips(model: model) }
            if model.devices.count > 1 {
                Picker("Device", selection: $model.filter.device) {
                    Text("All devices").tag("")
                    ForEach(model.devices, id: \.key) { Text($0.label).tag($0.key) }
                }
                .accessibilityIdentifier("deviceFilter")
            }
            if model.origins.count > 1 {
                Picker("Origin app", selection: $model.filter.origin) {
                    Text("All origin apps").tag("")
                    ForEach(model.origins, id: \.key) { Text($0.label).tag($0.key ?? "") }
                }
                .accessibilityIdentifier("originFilter")
            }
            Toggle("Show metrics without data", isOn: $model.showEmpty)
                .accessibilityIdentifier("showEmptyToggle")
        }
    }
}

/// "All sources" and one chip per provider, each with its stable colour dot.
private struct SourceChips: View {
    @Bindable var model: ExploreModel

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                Chip(title: "All sources", provider: nil, isOn: model.filter.provider.isEmpty) { model.filter.provider = "" }
                ForEach(model.providers, id: \.self) { provider in
                    Chip(title: providerLabel(provider), provider: provider, isOn: model.filter.provider == provider) {
                        model.filter.provider = model.filter.provider == provider ? "" : provider
                    }
                }
            }
            .padding(.vertical, 2)
        }
        .listRowInsets(EdgeInsets(top: 8, leading: 16, bottom: 8, trailing: 16))
    }
}

private struct Chip: View {
    let title: String
    let provider: String?
    let isOn: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 6) {
                if let provider { SourceDot(provider: provider) }
                Text(title).font(.subheadline.weight(.semibold))
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 8)
            .foregroundStyle(isOn ? Color(.systemBackground) : .primary)
            .background(isOn ? Color.primary : Color(.tertiarySystemFill), in: .capsule)
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(isOn ? .isSelected : [])
        .accessibilityIdentifier("sourceFilter-\(provider ?? "all")")
    }
}

private struct ExploreSections: View {
    @Environment(AppState.self) private var state
    let model: ExploreModel
    let pins: Pins
    let isEmpty: Bool

    var body: some View {
        let sections = model.sections
        if isEmpty && !model.showEmpty {
            ContentUnavailableView {
                Label("Nothing stored yet", systemImage: "safari")
            } description: {
                Text("Connect a source and its data shows up here after the first sync.")
            } actions: {
                Button("Sources") { state.open(.connections()) }
            }
        } else if sections.isEmpty && !model.isLoadingCatalogue {
            ContentUnavailableView {
                Label("Nothing matches these filters", systemImage: "magnifyingglass")
            } actions: {
                if model.filter.isActive { Button("Clear filters") { model.clearFilters() } }
            }
        }
        ForEach(sections) { section in
            Section {
                ForEach(section.items, id: \.key) { item in
                    ExploreRow(item: item, spark: model.sparks[item.code], pins: pins)
                }
            } header: {
                Text(section.name)
            } footer: {
                Text(section.items.count == 1 ? "1 item" : "\(section.items.count) items")
            }
        }
        if model.isLoadingCatalogue { ProgressView("Loading the catalogue").frame(maxWidth: .infinity) }
    }
}
