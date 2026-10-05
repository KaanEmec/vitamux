import SwiftUI
import VitamuxKit

/// Rules catalogue (`vitamux://rules`): how each metric picks one value when several sources
/// report it, in plain words, with its source order, reason and 90-day coverage; search and filter.
struct RulesView: View {
    @Environment(AppState.self) private var state
    @State private var model = RulesModel()

    var body: some View {
        List {
            switch model.entries {
            case .loading:
                ProgressView("Loading rules").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded:
                RulesHeader(model: model)
                RuleEntries(model: model)
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Rules")
        .searchable(text: $model.query, prompt: "Search metric")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                NavigationLink(value: Route.ruleNew()) { Label("New rule", systemImage: "plus") }
                    .accessibilityIdentifier("newRule")
            }
        }
        .refreshable { await model.load(state.client) }
        .task { await model.load(state.client) }
    }
}

private struct RulesHeader: View {
    @Bindable var model: RulesModel

    var body: some View {
        Section {
            Text("How each metric picks one value when several sources report it. Built-in defaults apply until you edit a metric; every change is a new version you can roll back.")
                .font(.subheadline)
                .foregroundStyle(.secondary)
            Picker("Filter", selection: $model.filter) {
                ForEach(RulesModel.Filter.allCases) { f in
                    Text("\(f.title) \(model.count(f))").tag(f)
                }
            }
            .pickerStyle(.segmented)
            .accessibilityIdentifier("rulesFilter")
            if model.coverage == nil {
                Label("Coverage heatmaps are not available yet.", systemImage: "info.circle")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
    }
}

private struct RuleEntries: View {
    let model: RulesModel

    var body: some View {
        let visible = model.visible
        if visible.isEmpty {
            ContentUnavailableView("No rules match", systemImage: "magnifyingglass", description: Text("Try another name or filter."))
        }
        ForEach(visible) { entry in
            Section {
                RuleCard(entry: entry, rows: model.coverage == nil ? nil : model.rows(for: entry.metric), start: model.range.start)
            }
        }
    }
}

/// One metric: its rule in plain words, source order, coverage and reason.
private struct RuleCard: View {
    let entry: RulesModel.Entry
    let rows: [CoverageStrip.Row]?
    let start: LocalDate

    var body: some View {
        NavigationLink(value: entry.rule == nil ? Route.ruleNew(metric: entry.metric, blank: true) : Route.rule(metric: entry.metric)) {
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Text(entry.metric).font(.headline.monospaced())
                    Spacer()
                    RuleBadge(rule: entry.rule)
                }
                if let rule = entry.rule {
                    Text(RuleText.sentence(rule.rule)).font(.subheadline).accessibilityIdentifier("ruleSentence-\(entry.metric)")
                    SourceOrder(spec: rule.rule)
                } else {
                    Text("Only the all-sources view shows this metric until you pick a source.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .accessibilityIdentifier("ruleEntry-\(entry.metric)")
        if let rows {
            if rows.isEmpty {
                Text("No data in the last 90 days.").font(.footnote).foregroundStyle(.secondary)
            } else {
                CoverageStrip(caption: "\(entry.metric) coverage, last 90 days", rows: rows, start: start)
            }
        }
        if let rule = entry.rule, let reason = rule.reason {
            Text(rule._default
                ? "\(reason) Your source order comes first; you can reorder or replace it."
                : "\(reason) Suggested order; you can reorder or replace it.")
                .font(.footnote)
                .foregroundStyle(.secondary)
        }
    }
}
