import SwiftUI
import VitamuxKit

// The builder's five steps (frontend.md#rule-builder), one screen each. Every input writes the
// form's text; `RuleForm.spec` turns it into the rule JSON.

extension RuleBuilderModel {
    /// A binding to one form field.
    func binding<T>(_ path: WritableKeyPath<RuleForm, T>, _ fallback: T) -> Binding<T> {
        Binding { self.form?[keyPath: path] ?? fallback } set: { self.form?[keyPath: path] = $0 }
    }
}

// MARK: 1. Metric

struct MetricStep: View {
    @Bindable var model: RuleBuilderModel

    var body: some View {
        Section("1. Metric") {
            Picker("Metric", selection: $model.metric) {
                Text("Choose a metric…").tag("")
                ForEach(model.metrics, id: \.self) { Text($0).tag($0) }
            }
            .accessibilityIdentifier("metricPicker")
            Picker("Start from", selection: $model.start) {
                Text("The rule in effect").tag(RuleBuilderModel.Start.current)
                Text("An empty rule").tag(RuleBuilderModel.Start.blank)
            }
            .pickerStyle(.inline)
            .accessibilityIdentifier("startFrom")
            if let r = model.activeRule(model.metric) {
                Text("In effect: \(r._default ? "default rule" : r.builtin ? "built-in default" : "version \(r.version)"). \(r.reason ?? "")")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
        .onChange(of: model.metric) { model.restart() }
        .onChange(of: model.start) { model.restart() }
    }
}

// MARK: 2. Sources

struct SourcesStep: View {
    let model: RuleBuilderModel

    var body: some View {
        let groups = model.form?.groups ?? []
        Section {
            Text("Inputs join the first group they match, top to bottom. With “first source with data”, the order is the fallback ladder.")
                .font(.footnote)
                .foregroundStyle(.secondary)
            InlineError(text: model.error("/spec/groups"), id: "groupsError")
        } header: {
            Text("2. Sources")
        }
        ForEach(Array(groups.enumerated()), id: \.element.key) { i, group in
            GroupEditor(model: model, index: i, count: groups.count)
        }
        Section {
            Button("Add group", systemImage: "plus") {
                model.form?.groups.append(.init(name: "", match: [RuleSelector([.provider: .text("")])]))
            }
            .accessibilityIdentifier("addGroup")
        }
        Section {
            Text("Inputs matching any exclusion are never used, whatever group they match.").font(.footnote).foregroundStyle(.secondary)
            if model.relaySuggestion {
                VStack(alignment: .leading, spacing: 6) {
                    Label("A group names a provider that may also relay into Apple Health.", systemImage: "info.circle").font(.footnote)
                    Button("Exclude Apple Health relays") { model.excludeRelays() }
                        .buttonStyle(.bordered)
                        .accessibilityIdentifier("excludeRelays")
                }
            }
            SelectorListEditor(list: model.binding(\.exclude, []), kind: "Exclusion", pointer: "/spec/exclude", model: model)
        } header: {
            Text("Exclusions")
        }
    }
}

private struct GroupEditor: View {
    let model: RuleBuilderModel
    let index: Int
    let count: Int

    var body: some View {
        let pointer = "/spec/groups/\(index)"
        Section {
            TextField("Group id", text: Binding { model.form?.groups[safe: index]?.name ?? "" } set: { model.form?.groups[index].name = $0 })
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .accessibilityIdentifier("groupID-\(index)")
                .id("\(pointer)/id")
            InlineError(text: model.error("\(pointer)/id"), id: "groupIDError-\(index)")
            InlineError(text: model.error(pointer), id: "groupError-\(index)")
            HStack {
                Button("Move group \(index + 1) up", systemImage: "arrow.up") { model.moveGroup(index, by: -1) }
                    .disabled(index == 0)
                Button("Move group \(index + 1) down", systemImage: "arrow.down") { model.moveGroup(index, by: 1) }
                    .disabled(index == count - 1)
                Spacer()
                Button("Remove group \(index + 1)", role: .destructive) { model.form?.groups.remove(at: index) }
                    .disabled(count == 1)
            }
            .labelStyle(.iconOnly)
            .buttonStyle(.borderless)
            SelectorListEditor(
                list: Binding { model.form?.groups[safe: index]?.match ?? [] } set: { model.form?.groups[index].match = $0 },
                kind: "Match", pointer: "\(pointer)/match", model: model
            )
        } header: {
            Text("Group \(index + 1)")
        } footer: {
            Text("Lowercase letters, digits and _")
        }
    }
}

/// A list of selectors (ORed), each a set of conditions (ANDed), with named choices and
/// one-click selectors to fill them.
private struct SelectorListEditor: View {
    @Binding var list: [RuleSelector]
    let kind: String
    let pointer: String
    let model: RuleBuilderModel

    var body: some View {
        ForEach(Array(list.enumerated()), id: \.offset) { si, selector in
            VStack(alignment: .leading, spacing: 6) {
                HStack {
                    Text("\(kind) \(si + 1)").font(.footnote.weight(.semibold))
                    Spacer()
                    if !model.choices.isEmpty {
                        Menu("Choose a source or device") {
                            ForEach(model.choices, id: \.label) { c in Button(c.label) { list[si] = c.selector } }
                        }
                        .font(.footnote)
                    }
                }
                ForEach(Array(selector.conditions.enumerated()), id: \.offset) { ci, condition in
                    ConditionRow(condition: Binding { list[safe: si]?.conditions[safe: ci] ?? condition } set: { list[si].conditions[ci] = $0 },
                                 used: Set(selector.conditions.map(\.field))) {
                        list[si].conditions.remove(at: ci)
                    }
                    InlineError(text: model.error("\(pointer)/\(si)/\(condition.field.rawValue)"), id: "selectorError-\(si)-\(ci)")
                }
                InlineError(text: model.error("\(pointer)/\(si)"), id: "selectorError-\(si)")
                HStack {
                    if let field = RuleSelector.Field.allCases.first(where: { f in !selector.conditions.contains { $0.field == f } }) {
                        Button("And…") { list[si].conditions.append(.init(field, field == .relayed ? .flag(false) : .text(""))) }
                    }
                    Spacer()
                    Button("Remove", role: .destructive) { list.remove(at: si) }
                }
                .font(.footnote)
                .buttonStyle(.borderless)
            }
        }
        HStack {
            Button(list.isEmpty ? "Add \(kind.lowercased())" : "Or \(kind.lowercased())…") {
                list.append(RuleSelector([.provider: .text("")]))
            }
            Spacer()
            if !model.chips.isEmpty {
                Menu("One-click") {
                    ForEach(model.chips, id: \.label) { c in Button(c.label) { list.append(c.selector) } }
                }
            }
        }
        .font(.footnote)
        .buttonStyle(.borderless)
    }
}

private struct ConditionRow: View {
    @Binding var condition: RuleSelector.Condition
    let used: Set<RuleSelector.Field>
    let remove: () -> Void

    var body: some View {
        HStack {
            Picker("Field", selection: Binding { condition.field } set: { f in
                condition = .init(f, f == .relayed ? .flag(false) : f == .entry ? .text("device") : .text(""))
            }) {
                ForEach(RuleSelector.Field.allCases.filter { $0 == condition.field || !used.contains($0) }, id: \.self) { Text($0.label).tag($0) }
            }
            .labelsHidden()
            .fixedSize()
            switch condition.field {
            case .relayed:
                Picker("Value", selection: Binding { condition.value == .flag(true) } set: { condition.value = .flag($0) }) {
                    Text("relayed by another app").tag(true)
                    Text("not relayed (direct)").tag(false)
                }
                .labelsHidden()
            case .entry:
                Picker("Value", selection: Binding { if case .text(let t) = condition.value { t } else { "" } } set: { condition.value = .text($0) }) {
                    Text("measured by a device").tag("device")
                    Text("entered manually").tag("manual")
                }
                .labelsHidden()
            default:
                TextField("Value", text: Binding { if case .text(let t) = condition.value { t } else { "" } } set: { condition.value = .text($0) })
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
            }
            Button("Remove condition", systemImage: "minus.circle", action: remove)
                .labelStyle(.iconOnly)
                .buttonStyle(.borderless)
        }
    }
}

// MARK: 3. Strategy

struct StrategyStep: View {
    let model: RuleBuilderModel

    var body: some View {
        let form = model.form
        Section("3. Strategy · how to combine the groups") {
            ForEach(RuleOp.allCases, id: \.self) { op in
                Button {
                    model.form?.op = op
                } label: {
                    HStack(alignment: .top) {
                        Image(systemName: form?.op == op ? "largecircle.fill.circle" : "circle").foregroundStyle(.tint)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(op.label).foregroundStyle(.primary)
                            Text(op.hint).font(.footnote).foregroundStyle(.secondary)
                        }
                    }
                }
                .accessibilityAddTraits(form?.op == op ? .isSelected : [])
                .accessibilityIdentifier("op-\(op.rawValue)")
            }
            if form?.op == .singleSource, (form?.groups.count ?? 0) > 1 {
                Label("One source only needs exactly one group; remove the others in step 2.", systemImage: "exclamationmark.triangle")
                    .font(.footnote)
            }
        }
        if form?.op.pooling == true {
            Section {
                TextField("Minimum sources (default 1)", text: model.binding(\.minSources, ""))
                    .keyboardType(.numberPad)
                    .id("/spec/strategy/min_sources")
                InlineError(text: model.error("/spec/strategy/min_sources"), id: "minSourcesError")
                Picker("With fewer sources", selection: model.binding(\.onInsufficient, "")) {
                    Text("Default (use what is available)").tag("")
                    Text("Use what is available, with a warning").tag("use_available")
                    Text("No value").tag("no_value")
                }
            }
        }
        Section("Within each group") {
            Picker("Several sources in one group", selection: model.binding(\.intraGroup, "")) {
                Text("Default").tag("")
                Text("Automatic (mean, or max for counts)").tag("auto")
                Text("Mean").tag("mean")
                Text("Highest").tag("max")
                Text("Add them (duplicate risk)").tag("sum")
            }
            Picker("Daily totals", selection: model.binding(\.dailyValuePolicy, "")) {
                Text("Default").tag("")
                Text("Prefer the provider's daily total").tag("prefer_reported")
                Text("Only add up intervals").tag("intervals_only")
            }
            Picker("Statistic", selection: model.binding(\.statistic, "")) {
                Text("Default").tag("")
                Text("Latest reading of the day").tag("latest")
                Text("Mean of the day's readings").tag("mean")
                Text("Lowest bucket").tag("min")
                Text("Lowest rolling mean").tag("min_rolling_mean")
            }
            if form?.statistic == "min_rolling_mean" {
                TextField("Rolling span, e.g. 30m", text: model.binding(\.span, "")).id("/spec/within_source/span")
                InlineError(text: model.error("/spec/within_source/span"), id: "spanError")
            }
        }
        if form?.needsSumAck == true {
            Section {
                Label("Adding sources can count the same activity twice.", systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                Text("If two devices recorded the same steps, a sum doubles them. Every result will carry this warning.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                Toggle("I understand the duplicate risk", isOn: Binding { model.form?.isSumAcknowledged ?? false } set: { model.setAcknowledged($0) })
                    .accessibilityIdentifier("ackToggle")
                InlineError(text: model.ackError.isEmpty ? model.error("/spec/acknowledged_warnings") : model.ackError, id: "ackError")
            }
            .id("/spec/acknowledged_warnings")
        }
    }
}

// MARK: 4. Window and quality

struct WindowStep: View {
    let model: RuleBuilderModel

    var body: some View {
        let form = model.form
        Section("4. Window") {
            Picker("Window", selection: model.binding(\.windowKind, "local_day")) {
                ForEach(RuleWindow.kinds, id: \.kind) { Text($0.label).tag($0.kind) }
            }
            .id("/spec/window")
            if form?.windowKind == "bucket" {
                Picker("Bucket size", selection: model.binding(\.bucketSize, "5m")) {
                    ForEach(RuleWindow.bucketSizes, id: \.self) { Text($0).tag($0) }
                }
            }
            InlineError(text: model.error("/spec/window"), id: "windowError")
        }
        Section("Quality gates") {
            NumberField(label: "Minimum coverage", hint: "Opt-in, 0–1, e.g. 0.5", text: model.binding(\.minCoverage, ""), error: model.error("/spec/quality/min_coverage"), pointer: "/spec/quality/min_coverage")
            NumberField(label: "Plausible low", hint: "", text: model.binding(\.rangeLow, ""), error: model.error("/spec/quality/plausible_range/0") ?? model.error("/spec/quality/plausible_range"), pointer: "/spec/quality/plausible_range/0")
            NumberField(label: "Plausible high", hint: "", text: model.binding(\.rangeHigh, ""), error: model.error("/spec/quality/plausible_range/1"), pointer: "/spec/quality/plausible_range/1")
            NumberField(label: "Maximum staleness", hint: "e.g. 36h or 30d", text: model.binding(\.maxStaleness, ""), error: model.error("/spec/quality/max_staleness"), pointer: "/spec/quality/max_staleness")
            NumberField(label: "Count only while worn (wear metric)", hint: "Opt-in, e.g. heart_rate", text: model.binding(\.requireWear, ""), error: model.error("/spec/quality/require_wear"), pointer: "/spec/quality/require_wear")
        }
        Section("Ignore inputs flagged as") {
            ForEach(RuleSpec.qualityFlags, id: \.self) { flag in
                Toggle(flag.replacingOccurrences(of: "_", with: " "), isOn: Binding {
                    model.form?.excludeFlags.contains(flag) ?? false
                } set: { on in
                    model.form?.excludeFlags.removeAll { $0 == flag }
                    if on { model.form?.excludeFlags.append(flag) }
                })
            }
        }
        Section {
            DisclosureGroup("Sleep alignment") {
                NumberField(label: "Episode match overlap", hint: "Default 0.5", text: model.binding(\.matchOverlap, ""), error: nil, pointer: "/spec/quality/sleep/match_overlap")
                NumberField(label: "Minimum episode coverage", hint: "Opt-in, e.g. 0.7", text: model.binding(\.minEpisodeCoverage, ""), error: nil, pointer: "/spec/quality/sleep/min_episode_coverage")
                Picker("Naps", selection: model.binding(\.includeNaps, "")) {
                    Text("Default").tag("")
                    Text("Leave out naps").tag("false")
                    Text("Include naps").tag("true")
                }
                NumberField(label: "Night anchor", hint: "Default 18:00", text: model.binding(\.nightAnchor, ""), error: nil, pointer: "/spec/quality/sleep/night_anchor")
            }
            DisclosureGroup("Advanced") {
                NumberField(label: "Use the source another metric selected", hint: "e.g. weight", text: model.binding(\.follow, ""), error: model.error("/spec/follow"), pointer: "/spec/follow")
                Picker("Build the day from hourly picks", selection: model.binding(\.compose, "")) {
                    Text("No").tag("")
                    Text("Yes, first source with data per hour").tag("first_available")
                    Text("Yes, highest source per hour").tag("max")
                }
                if let contexts = form?.contexts {
                    Text("Workout and sleep orderings are kept from the starting rule: \(contexts.text)").font(.footnote.monospaced())
                    Button("Remove them") { model.form?.contexts = nil }
                }
            }
        }
    }
}

/// A labelled text input with its hint and field error.
private struct NumberField: View {
    let label: String
    let hint: String
    @Binding var text: String
    let error: String?
    let pointer: String

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            LabeledContent(label) {
                TextField(hint.isEmpty ? label : hint, text: $text)
                    .multilineTextAlignment(.trailing)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
            }
            InlineError(text: error, id: "error-\(pointer)")
        }
        .id(pointer)
    }
}

// MARK: 5. Review

struct ReviewStep: View {
    @Environment(AppState.self) private var state
    @Bindable var model: RuleBuilderModel

    var body: some View {
        if let draft = model.form?.spec {
            Section("5. Review") {
                LabeledContent("Metric", value: draft["metric"]?.string ?? "")
                LabeledContent("Window", value: RuleWindow.label(draft["window"]))
                LabeledContent("Strategy", value: model.form?.op.label ?? "")
                ForEach(Array((model.form?.groups ?? []).enumerated()), id: \.offset) { i, g in
                    LabeledContent("\(i + 1). \(RuleText.groupLabel(g.name))", value: g.match.map { RuleText.selector($0, choices: model.choices) }.joined(separator: " or "))
                }
                if let ex = model.form?.exclude, !ex.isEmpty {
                    LabeledContent("Excluded", value: ex.map { RuleText.selector($0, choices: model.choices) }.joined(separator: "; "))
                }
                if let ack = model.form?.acknowledged, !ack.isEmpty { LabeledContent("Acknowledged", value: ack.joined(separator: ", ")) }
                Text(RuleText.sentence(draft)).font(.subheadline).accessibilityIdentifier("reviewSentence")
                DisclosureGroup("Rule JSON") {
                    Text(prettyJSON(draft)).font(.caption.monospaced()).textSelection(.enabled)
                }
                if let current = model.currentSpec {
                    DisclosureGroup("Changes from the rule in effect") {
                        RuleDiffList(changes: RuleSpec.diff(current, draft))
                    }
                    .accessibilityIdentifier("reviewDiff")
                }
            }
            Section("Preview: last 14 days") {
                switch model.preview {
                case .idle, .loading:
                    Text("Resolving the draft…").foregroundStyle(.secondary)
                case .unavailable:
                    Text("Preview unavailable: this server cannot resolve drafts yet. You can still save the rule.")
                case .failed(let problem):
                    ProblemBanner(problem: problem)
                case .loaded(let preview):
                    Text("\(preview.days.filter(RulePreview.changed).count) of \(preview.days.count) days change with this draft.")
                        .accessibilityIdentifier("reviewSummary")
                    ForEach(preview.days, id: \.localDate) { d in
                        let changed = RulePreview.changed(d)
                        VStack(alignment: .leading, spacing: 2) {
                            HStack {
                                Text(d.localDate).font(.footnote.monospaced())
                                Spacer()
                                Label(changed ? "Changed" : "Same", systemImage: changed ? "arrow.triangle.swap" : "equal")
                                    .font(.caption)
                                    .foregroundStyle(changed ? .orange : .secondary)
                            }
                            Text("Active: \(previewText(d.active))").font(.footnote)
                            Text("Draft: \(previewText(d.draft))").font(.footnote)
                        }
                        .accessibilityElement(children: .combine)
                    }
                }
                Button("Refresh preview") { Task { await model.runPreview(state.client) } }
            }
            Section {
                TextField("Note (optional)", text: $model.note).accessibilityIdentifier("builderNote")
                Toggle("Make it the active rule", isOn: $model.activate).accessibilityIdentifier("activateToggle")
            }
        }
    }

    private func prettyJSON(_ v: JSONValue, indent: String = "") -> String {
        let inner = indent + "  "
        switch v {
        case .array(let items) where !items.isEmpty:
            return "[\n" + items.map { inner + prettyJSON($0, indent: inner) }.joined(separator: ",\n") + "\n\(indent)]"
        case .object(let members) where !members.isEmpty:
            return "{\n" + members.map { "\(inner)\(JSONValue.string($0.key).text): \(prettyJSON($0.value, indent: inner))" }.joined(separator: ",\n") + "\n\(indent)}"
        default:
            return v.text
        }
    }
}

extension Array {
    subscript(safe index: Int) -> Element? {
        indices.contains(index) ? self[index] : nil
    }
}
