import SwiftUI
import VitamuxKit

/// The rule lens (artboard RuleLens): a bottom sheet over the metric chart with the rule in plain
/// words, the source order to reorder, exclusion chips, strategy, window and minimum coverage,
/// the sum acknowledgement, the draft drawn as a ghost over the rule in effect with the changed
/// days marked, save, save and activate, revert, and the version history.
struct RuleLensSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    @State private var model: RuleLensModel
    let showsHistory: Bool
    let onSaved: () async -> Void

    init(metric: String, start: LocalDate, end: LocalDate, showsHistory: Bool = true, onSaved: @escaping () async -> Void = {}) {
        _model = State(initialValue: RuleLensModel(metric: metric, start: start, end: end))
        self.showsHistory = showsHistory
        self.onSaved = onSaved
    }

    var body: some View {
        NavigationStack {
            ScrollViewReader { proxy in
                List {
                    switch model.versions {
                    case .loading:
                        ProgressView("Loading the rule").frame(maxWidth: .infinity)
                    case .failed(let problem):
                        ProblemView(problem: problem)
                    case .loaded:
                        if model.form == nil {
                            NoRuleYet(metric: model.metric, open: openBuilder)
                        } else {
                            LensPreview(model: model)
                            LensSentence(model: model)
                            if let problem = model.shownProblem {
                                ProblemBanner(problem: problem, hidden: RuleLensModel.shownByControl)
                            }
                            LensGroups(model: model)
                            LensExclusions(model: model)
                            LensStrategy(model: model)
                            if model.form?.needsSumAck == true { LensSumAck(model: model) }
                            Section {
                                Button("Plausible range, flags, staleness… open full builder") { openBuilder(.ruleNew(metric: model.metric)) }
                                    .font(.footnote)
                            }
                            LensSave(model: model, onSaved: onSaved)
                        }
                        LensMessage(model: model, onSaved: onSaved)
                        if showsHistory { LensHistory(model: model, onSaved: onSaved) }
                    }
                }
                .listStyle(.insetGrouped)
                .onChange(of: model.shownProblem) { _, problem in
                    guard let pointer = problem?.fieldErrors.first?.pointer else { return }
                    withAnimation { proxy.scrollTo(LensAnchor.of(pointer), anchor: .center) }
                }
                .onChange(of: model.ackError) { _, error in
                    if !error.isEmpty { withAnimation { proxy.scrollTo(LensAnchor.ack, anchor: .center) } }
                }
            }
            .navigationTitle("How it's calculated")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }.accessibilityIdentifier("lensDone")
                }
            }
        }
        .sheetBackground()
        .presentationDetents([.medium, .large])
        .task { await model.load(state.client) }
        .task(id: model.draftKey) { await model.previewAfterPause(state.client) }
    }

    /// Closes the lens and opens `route` on the current tab.
    private func openBuilder(_ route: Route) {
        dismiss()
        state.paths[state.tab, default: []].append(route)
    }
}

/// Where a field error scrolls to.
enum LensAnchor: Hashable {
    case groups, exclude, strategy, coverage, ack

    static func of(_ pointer: String) -> LensAnchor {
        if pointer.hasPrefix("/spec/groups") { return .groups }
        if pointer.hasPrefix("/spec/exclude") { return .exclude }
        if pointer.hasPrefix("/spec/quality/min_coverage") { return .coverage }
        if pointer.hasPrefix("/spec/acknowledged_warnings") { return .ack }
        return .strategy
    }
}

private struct NoRuleYet: View {
    let metric: String
    let open: (Route) -> Void

    var body: some View {
        Section {
            ContentUnavailableView("No rule for this metric yet", systemImage: "slider.horizontal.3",
                                   description: Text("Only the all-sources view shows it until you pick a source."))
            Button("Create a rule") { open(.ruleNew(metric: metric, blank: true)) }
        }
    }
}

/// The rule in plain words, marked while it is a draft.
private struct LensSentence: View {
    let model: RuleLensModel

    var body: some View {
        Section {
            if model.dirty { DraftBadge().accessibilityIdentifier("draftBadge") }
            if let draft = model.draft {
                Text(RuleText.sentence(draft)).accessibilityIdentifier("lensSentence")
            }
        }
    }
}

/// The draft against the rule in effect: a ghost line with the changed days marked, the summary
/// and the days that change.
private struct LensPreview: View {
    let model: RuleLensModel

    var body: some View {
        Section {
            switch model.preview {
            case .idle:
                Text(model.dirty ? "Resolving the draft…" : "Change the rule to preview it on \(Format.day(model.start, weekday: false)) to \(Format.day(model.end, weekday: false)).")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("previewIdle")
            case .loading:
                Text("Resolving the draft…").font(.footnote).foregroundStyle(.secondary)
            case .unavailable:
                Label("Preview unavailable: this server cannot resolve drafts yet. You can still save the rule.", systemImage: "info.circle")
                    .font(.footnote)
            case .failed:
                Text("Not ready to preview; see the message below.").font(.footnote).foregroundStyle(.secondary)
            case .loaded(let preview):
                PreviewChart(days: preview.days, unit: model.summary?.unit ?? "")
                Text(model.summaryText)
                    .font(.subheadline.weight(.semibold))
                    .accessibilityIdentifier("previewSummary")
                ChangedDays(days: preview.days.filter(RulePreview.changed))
            }
        } header: {
            HStack {
                Text("Draft preview")
                Spacer()
                if model.dirty { Text("vs active") }
            }
        }
    }
}

private struct PreviewChart: View {
    let days: [Components.Schemas.PreviewDay]
    let unit: String

    var body: some View {
        let x = { (d: Components.Schemas.PreviewDay) in LocalDate(d.localDate)?.start(in: .gmt) ?? .distantPast }
        let active = days.map { ChartPoint(x: x($0), y: RulePreview.number($0.active)) }
        let draft = days.map { ChartPoint(x: x($0), y: RulePreview.number($0.draft)) }
        let changed = days.filter(RulePreview.changed).map { ChartPoint(x: x($0), y: RulePreview.number($0.draft)) }
        TimeSeries(
            title: "Rule in effect (solid) and draft (dashed) over \(days.count) days",
            series: [
                ChartSeries(label: "Rule in effect", points: active),
                ChartSeries(label: "Draft", points: draft, style: .ghost),
                ChartSeries(label: "Changed days", points: changed, style: .dots),
            ],
            unit: unit, timeZone: .gmt, withTime: false, zoomable: false, height: 150
        )
    }
}

private struct ChangedDays: View {
    let days: [Components.Schemas.PreviewDay]

    var body: some View {
        if !days.isEmpty {
            DisclosureGroup("Days that change") {
                ForEach(days, id: \.localDate) { d in
                    VStack(alignment: .leading, spacing: 2) {
                        Text(Format.day(d.localDate)).font(.footnote.weight(.semibold))
                        Text("Active: \(previewText(d.active))").font(.footnote)
                        Text("Draft: \(previewText(d.draft))").font(.footnote)
                        Text(d.draft.explanation).font(.caption).foregroundStyle(.secondary)
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("changedDay-\(d.localDate)")
                }
            }
            .accessibilityIdentifier("changedDays")
        }
    }
}

/// Source priority: drag to reorder, or the arrows.
private struct LensGroups: View {
    let model: RuleLensModel

    var body: some View {
        let groups = model.form?.groups ?? []
        Section {
            ForEach(Array(groups.enumerated()), id: \.element.key) { i, group in
                GroupRow(model: model, group: group, index: i, count: groups.count)
            }
            .onMove { model.move(fromOffsets: $0, toOffset: $1) }
            InlineError(text: model.error("/spec/groups"), id: "groupsError")
        } header: {
            HStack {
                Text("Source priority")
                Spacer()
                Text("Drag, or use the arrows").textCase(nil)
            }
        }
        .id(LensAnchor.groups)
    }
}

private struct GroupRow: View {
    let model: RuleLensModel
    let group: RuleForm.Group
    let index: Int
    let count: Int

    var body: some View {
        let label = RuleText.groupLabel(group.name)
        HStack(spacing: 10) {
            Text("\(index + 1)").font(.footnote.monospacedDigit()).foregroundStyle(.secondary)
            SourceDot(provider: RuleText.provider(of: group))
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(label).font(.body.weight(.medium)).accessibilityIdentifier("lensGroup-\(index)")
                    if model.baseIDs.firstIndex(of: group.name) != index { DraftBadge(text: "moved") }
                }
                Text(group.match.map { RuleText.selector($0, choices: model.choices) }.joined(separator: " or "))
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: 4)
            Button("Move \(label) up", systemImage: "arrow.up") { model.move(index, to: index - 1) }
                .labelStyle(.iconTapTarget)
                .disabled(index == 0)
                .accessibilityIdentifier("moveUp-\(group.name)")
            Button("Move \(label) down", systemImage: "arrow.down") { model.move(index, to: index + 1) }
                .labelStyle(.iconTapTarget)
                .disabled(index == count - 1)
                .accessibilityIdentifier("moveDown-\(group.name)")
        }
        .buttonStyle(.borderless)
    }
}

/// "Never use": the exclusions as removable chips, and one-click choices to add.
private struct LensExclusions: View {
    let model: RuleLensModel

    var body: some View {
        let exclude = model.form?.exclude ?? []
        Section("Never use") {
            if exclude.isEmpty {
                Text("Nothing excluded").foregroundStyle(.secondary)
            }
            ForEach(Array(exclude.enumerated()), id: \.offset) { i, selector in
                let text = RuleText.selector(selector, choices: model.choices)
                HStack {
                    Image(systemName: "nosign").foregroundStyle(.secondary)
                    Text(text.isEmpty ? "empty exclusion" : text)
                    Spacer()
                    Button("Remove exclusion \(text)", systemImage: "xmark.circle.fill") { model.removeExclusion(at: i) }
                        .labelStyle(.iconTapTarget)
                        .foregroundStyle(.secondary)
                        .buttonStyle(.borderless)
                }
            }
            let options = model.exclusionChoices
            if !options.isEmpty {
                Menu {
                    ForEach(options, id: \.label) { choice in
                        Button(choice.label) { model.exclude(choice) }
                    }
                } label: {
                    Label("Exclude a source…", systemImage: "plus.circle")
                }
                .accessibilityIdentifier("addExclusion")
            }
            InlineError(text: model.error("/spec/exclude"), id: "excludeError")
        }
        .id(LensAnchor.exclude)
    }
}

/// Strategy, window and minimum coverage, limited to what the metric allows.
private struct LensStrategy: View {
    @Bindable var model: RuleLensModel

    var body: some View {
        Section {
            Picker("Strategy", selection: Binding { model.form?.op ?? .firstAvailable } set: { model.form?.op = $0 }) {
                ForEach(model.allowedOps, id: \.self) { Text($0.short).tag($0) }
            }
            .accessibilityIdentifier("strategyPicker")
            InlineError(text: model.error("/spec/strategy"), id: "strategyError")
            Picker("Window", selection: Binding { model.form?.windowKind ?? "" } set: { model.form?.windowKind = $0 }) {
                ForEach(model.allowedWindows, id: \.kind) { Text($0.label).tag($0.kind) }
            }
            .accessibilityIdentifier("windowPicker")
            InlineError(text: model.error("/spec/window"), id: "windowError")
            if model.form?.windowKind == "bucket" {
                Picker("Bucket size", selection: Binding { model.form?.bucketSize ?? "5m" } set: { model.form?.bucketSize = $0 }) {
                    ForEach(RuleWindow.bucketSizes, id: \.self) { Text($0).tag($0) }
                }
            }
            VStack(alignment: .leading) {
                let percent = Int(model.coveragePercent)
                Text("Minimum coverage · \(percent > 0 ? "\(percent)%" : "none (opt-in)")")
                    .accessibilityIdentifier("coverageLabel")
                Slider(value: $model.coveragePercent, in: 0 ... 100, step: 5) {
                    Text("Minimum coverage")
                }
                .accessibilityIdentifier("coverageSlider")
                InlineError(text: model.error("/spec/quality/min_coverage"), id: "coverageError")
            }
            .id(LensAnchor.coverage)
        } footer: {
            Text(model.hint)
        }
        .id(LensAnchor.strategy)
    }
}

/// A sum counts the same activity twice when two sources recorded it; it needs a tick to save.
private struct LensSumAck: View {
    let model: RuleLensModel

    var body: some View {
        Section {
            Label("Adding sources can count the same activity twice.", systemImage: "exclamationmark.triangle.fill")
                .foregroundStyle(Color.feedbackWarn)
            Toggle("I understand the duplicate risk", isOn: Binding { model.form?.isSumAcknowledged ?? false } set: { model.setAcknowledged($0) })
                .accessibilityIdentifier("ackToggle")
            InlineError(text: model.ackError.isEmpty ? model.error("/spec/acknowledged_warnings") : model.ackError, id: "ackError")
        }
        .id(LensAnchor.ack)
    }
}

private struct LensSave: View {
    @Environment(AppState.self) private var state
    @Bindable var model: RuleLensModel
    let onSaved: () async -> Void

    var body: some View {
        Section {
            TextField("Note (optional)", text: $model.note)
                .accessibilityIdentifier("noteField")
            Button("Save and activate") { save(activate: true) }
                .disabled(!model.dirty || model.busy)
                .accessibilityIdentifier("saveActivate")
            Button("Save as version \(model.nextVersion)") { save(activate: false) }
                .disabled(!model.dirty || model.busy)
                .accessibilityIdentifier("saveVersion")
            Button("Discard", role: .destructive) { model.rebase() }
                .disabled(!model.dirty || model.busy)
                .accessibilityIdentifier("discard")
        }
    }

    private func save(activate: Bool) {
        Task {
            if await model.save(activate: activate, client: state.client) { await onSaved() }
        }
    }
}

/// What the last save or activation did, with the way back.
private struct LensMessage: View {
    @Environment(AppState.self) private var state
    let model: RuleLensModel
    let onSaved: () async -> Void

    var body: some View {
        if !model.message.isEmpty {
            Section {
                Label {
                    Text(model.message).accessibilityIdentifier("lensMessage")
                } icon: {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(Color.feedbackOK).accessibilityHidden(true)
                }
                if let back = model.revertTo {
                    Button("Revert to version \(back)") {
                        Task { if await model.activate(back, revert: true, client: state.client) { await onSaved() } }
                    }
                    .disabled(model.busy)
                    .accessibilityIdentifier("revert")
                }
            }
        }
    }
}

/// Every version; an inactive owner version can be activated, an older one reverted to.
private struct LensHistory: View {
    @Environment(AppState.self) private var state
    let model: RuleLensModel
    let onSaved: () async -> Void

    var body: some View {
        let versions = model.versions.value ?? []
        if !versions.isEmpty {
            Section("History") {
                ForEach(versions, id: \.ref) { v in
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(v.name + (v.note.map { " · \($0)" } ?? ""))
                            Text([v.active ? "Active" : nil, v.builtin ? "shipped default" : v.createdAt?.formatted(date: .abbreviated, time: .omitted)]
                                .compactMap(\.self).joined(separator: " · "))
                                .font(.footnote)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        if !v.active, !v.builtin {
                            let older = model.active.map { !$0.builtin && v.version < $0.version } ?? false
                            Button(older ? "Revert" : "Activate") {
                                Task { if await model.activate(v.version, revert: older, client: state.client) { await onSaved() } }
                            }
                            .buttonStyle(.borderless)
                            .disabled(model.busy)
                            .accessibilityLabel("\(older ? "Revert to" : "Activate") version \(v.version)")
                            .accessibilityIdentifier("history-\(v.version)")
                        }
                    }
                }
            }
        }
    }
}
