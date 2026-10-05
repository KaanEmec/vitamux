import SwiftUI
import VitamuxKit

/// One metric's rule (`vitamux://rules/{metric}?saved=`): the version in effect in plain words,
/// 90-day coverage by origin app, the version timeline with activation, and a field diff of any
/// two versions. "How it's calculated" opens the rule lens against the last 30 days.
struct RuleView: View {
    @Environment(AppState.self) private var state
    @State private var model: RuleModel
    @State private var isEditing = false

    init(metric: String, saved: Int?) {
        _model = State(initialValue: RuleModel(metric: metric, saved: saved))
    }

    var body: some View {
        List {
            if let saved = model.saved { Notice(text: "Saved version \(saved).") }
            if let activated = model.activated { Notice(text: "Version \(activated) is now active.") }
            if let problem = model.problem { ProblemBanner(problem: problem) }
            switch model.versions {
            case .loading:
                ProgressView("Loading versions").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let versions):
                if let current = model.current {
                    InEffect(version: current, metric: model.metric) { isEditing = true }
                } else {
                    NoRule(metric: model.metric)
                }
                CoverageSection(model: model)
                if !versions.isEmpty { VersionHistory(model: model, versions: versions) }
                if versions.count > 1 { CompareVersions(model: model, versions: versions) }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle(model.metric)
        .toolbar {
            if model.current != nil {
                ToolbarItem(placement: .primaryAction) {
                    Button("How it's calculated", systemImage: "slider.horizontal.3") { isEditing = true }
                        .accessibilityIdentifier("openLens")
                }
            }
        }
        .task {
            async let origins: Void = model.loadOrigins(state.client)
            await model.load(state.client)
            await origins
        }
        .task(id: model.origin) { await model.loadCoverage(state.client) }
        .sheet(isPresented: $isEditing) {
            let range = lastDays(30)
            RuleLensSheet(metric: model.metric, start: range.start, end: range.end, showsHistory: false) {
                await model.load(state.client)
            }
        }
    }
}

private struct Notice: View {
    let text: String

    var body: some View {
        Label {
            Text(text).accessibilityIdentifier("ruleNotice")
        } icon: {
            Image(systemName: "checkmark.circle.fill").foregroundStyle(.green).accessibilityHidden(true)
        }
    }
}

/// The rule in effect: sentence, groups with their selectors, exclusions, reason and edits.
private struct InEffect: View {
    let version: RuleVersion
    let metric: String
    let edit: () -> Void

    var body: some View {
        let spec = version.rule
        let form = RuleForm(spec: spec)
        Section {
            Text(RuleText.sentence(spec)).accessibilityIdentifier("inEffectSentence")
            ForEach(Array(form.groups.enumerated()), id: \.offset) { i, group in
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text("\(i + 1)").font(.footnote.monospacedDigit()).foregroundStyle(.secondary)
                    SourceDot(provider: RuleText.provider(of: group))
                    VStack(alignment: .leading, spacing: 2) {
                        Text(RuleText.groupLabel(group.name)).font(.body.weight(.medium))
                        Text(group.match.map { RuleText.selector($0) }.joined(separator: " or ")).font(.footnote).foregroundStyle(.secondary)
                    }
                }
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("inEffectGroup-\(i)")
            }
            if !form.exclude.isEmpty {
                LabeledContent("Never used", value: form.exclude.map { RuleText.selector($0) }.joined(separator: "; "))
                    .font(.footnote)
            }
            if version.builtin {
                Text([version.reason, "Suggested order; you can reorder or replace it."].compactMap(\.self).joined(separator: " "))
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            Button("How it's calculated", systemImage: "slider.horizontal.3", action: edit)
            NavigationLink(value: Route.ruleNew(metric: metric)) { Label("Edit in builder", systemImage: "list.number") }
                .accessibilityIdentifier("editInBuilder")
            NavigationLink(value: Route.ruleNew(metric: metric, blank: true)) { Label("Replace with a new rule", systemImage: "square.and.pencil") }
        } header: {
            Text("In effect: \(version._default ? "default rule" : version.builtin ? "built-in default" : "version \(version.version)")")
                .accessibilityIdentifier("inEffect")
        }
    }
}

private struct NoRule: View {
    let metric: String

    var body: some View {
        Section {
            ContentUnavailableView("No rule for this metric yet", systemImage: "slider.horizontal.3",
                                   description: Text("Only the all-sources view shows it until you pick a source."))
            NavigationLink(value: Route.ruleNew(metric: metric, blank: true)) { Label("Create a rule", systemImage: "plus") }
        }
    }
}

private struct CoverageSection: View {
    @Bindable var model: RuleModel

    var body: some View {
        Section("Coverage, last 90 days") {
            if !model.origins.isEmpty {
                Picker("Origin app", selection: $model.origin) {
                    Text("All apps").tag("")
                    ForEach(model.origins, id: \.id) { Text($0.name ?? $0.originKey).tag($0.originKey) }
                }
                .accessibilityIdentifier("coverageOrigin")
            }
            if model.coverage == nil {
                Text("Coverage is not available yet.").foregroundStyle(.secondary)
            } else if model.rows.isEmpty {
                Text("No data in the last 90 days.").foregroundStyle(.secondary)
            } else {
                CoverageStrip(caption: "\(model.metric) coverage per source", rows: model.rows, start: model.range.start)
            }
        }
    }
}

/// Every version, newest first; any inactive owner version can be activated or edited as a copy.
private struct VersionHistory: View {
    @Environment(AppState.self) private var state
    let model: RuleModel
    let versions: [RuleVersion]

    var body: some View {
        Section("Version history") {
            ForEach(versions, id: \.ref) { v in
                VStack(alignment: .leading, spacing: 6) {
                    HStack {
                        Text(v.ref).font(.footnote.monospaced())
                        Spacer()
                        Text(v.active ? "Active" : "Inactive")
                            .font(.caption.weight(.semibold))
                            .foregroundStyle(v.active ? Color.accentColor : .secondary)
                    }
                    Text(detail(v)).font(.footnote).foregroundStyle(.secondary)
                    HStack {
                        if !v.active, !v.builtin {
                            Button("Activate version \(v.version)") { Task { await model.activate(v, client: state.client) } }
                                .buttonStyle(.bordered)
                                .disabled(model.busy)
                                .accessibilityIdentifier("activate-\(v.version)")
                        }
                        NavigationLink("Edit a copy", value: Route.ruleNew(metric: model.metric, from: v.version))
                            .font(.footnote)
                            .fixedSize()
                    }
                    .buttonStyle(.borderless)
                }
            }
        }
    }

    private func detail(_ v: RuleVersion) -> String {
        var parts = [v.builtin ? "Built-in" : v.createdAt?.formatted(date: .abbreviated, time: .shortened) ?? ""]
        if let by = v.createdBy { parts.append(by) }
        if let note = v.note { parts.append(note) }
        if let base = v.basedOn { parts.append("from \(base)") }
        return parts.filter { !$0.isEmpty }.joined(separator: " · ")
    }
}

/// Any two versions side by side: their sentences and the fields that differ.
private struct CompareVersions: View {
    @Bindable var model: RuleModel
    let versions: [RuleVersion]

    var body: some View {
        Section("Compare versions") {
            Picker("From", selection: $model.left) {
                ForEach(versions, id: \.ref) { Text($0.ref).tag($0.version) }
            }
            .accessibilityIdentifier("compareFrom")
            Picker("To", selection: $model.right) {
                ForEach(versions, id: \.ref) { Text($0.ref).tag($0.version) }
            }
            .accessibilityIdentifier("compareTo")
            let from = model.version(model.left), to = model.version(model.right)
            if let from, let to {
                LabeledContent("From · \(from.name)") { Text(RuleText.sentence(from.rule)).multilineTextAlignment(.trailing) }
                LabeledContent("To · \(to.name)") { Text(RuleText.sentence(to.rule)).multilineTextAlignment(.trailing) }
                RuleDiffList(changes: RuleSpec.diff(from.rule, to.rule), emptyText: "Changes from version \(from.version) to version \(to.version): no differences.")
            }
        }
    }
}
