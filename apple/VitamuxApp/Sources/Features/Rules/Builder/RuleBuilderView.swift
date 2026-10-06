import SwiftUI
import VitamuxKit

/// Rule builder (`vitamux://rules/new?metric=&from=&blank=`): five steps, one screen each, with a
/// live preview while editing and a 14-day preview on review. A save opens the rule page; a server
/// field error returns to its step and scrolls to its control.
struct RuleBuilderView: View {
    @Environment(AppState.self) private var state
    @State private var model: RuleBuilderModel

    init(metric: String?, from: Int?, blank: Bool) {
        _model = State(initialValue: RuleBuilderModel(metric: metric, from: from, blank: blank))
    }

    var body: some View {
        ScrollViewReader { proxy in
            List {
                if let problem = model.loadProblem { ProblemBanner(problem: problem) }
                if let problem = model.problem { ProblemBanner(problem: problem, hidden: RuleBuilderModel.shownByControl) }
                if !model.isLoaded, model.loadProblem == nil {
                    ProgressView("Loading rules").frame(maxWidth: .infinity)
                } else {
                    switch model.step {
                    case 0: MetricStep(model: model)
                    case 1: SourcesStep(model: model)
                    case 2: StrategyStep(model: model)
                    case 3: WindowStep(model: model)
                    default: ReviewStep(model: model)
                    }
                    if (1...3).contains(model.step) { LivePreview(model: model) }
                }
            }
            .listStyle(.insetGrouped)
            .onChange(of: model.step) { _, _ in
                if let pointer = model.problem?.fieldErrors.first?.pointer {
                    Task { withAnimation { proxy.scrollTo(pointer, anchor: .center) } }
                } else if !model.ackError.isEmpty {
                    Task { withAnimation { proxy.scrollTo("/spec/acknowledged_warnings", anchor: .center) } }
                }
            }
        }
        .navigationTitle("Rule builder")
        .navigationBarTitleDisplayMode(.inline)
        .safeAreaInset(edge: .top, spacing: 0) { StepBar(model: model) }
        .safeAreaInset(edge: .bottom) { StepButtons(model: model, saved: open) }
        .task { await model.load(state.client) }
        .task(id: model.sideKey) { await model.sidePreviewAfterPause(state.client) }
    }

    /// Replaces the builder with the saved rule's page.
    private func open(_ saved: RuleVersion) {
        var path = state.paths[state.tab] ?? []
        if !path.isEmpty { path.removeLast() }
        path.append(.rule(metric: saved.metric, saved: saved.version))
        state.paths[state.tab] = path
    }
}

/// The five steps; each shows a mark when a field error belongs to it.
private struct StepBar: View {
    @Environment(AppState.self) private var state
    let model: RuleBuilderModel

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                ForEach(Array(RuleBuilderModel.steps.enumerated()), id: \.offset) { i, label in
                    Button {
                        Task { await model.go(i, client: state.client) }
                    } label: {
                        ZStack(alignment: .topTrailing) {
                            Text(i < model.step && model.form != nil ? "✓" : "\(i + 1)")
                                .font(.subheadline.weight(.semibold).monospacedDigit())
                                .frame(width: 34, height: 34)
                                .foregroundStyle(i == model.step ? Color.white : .primary)
                                .background(i == model.step ? Color.accentColor : Color.raised, in: .circle)
                            if model.stepHasErrors(i) {
                                Image(systemName: "exclamationmark.circle.fill").font(.caption).foregroundStyle(Color.feedbackError)
                            }
                        }
                    }
                    .buttonStyle(.plain)
                    .disabled(i > 0 && model.form == nil && model.metric.isEmpty)
                    .accessibilityLabel("\(i + 1). \(label)\(model.stepHasErrors(i) ? ", has errors" : "")")
                    .accessibilityAddTraits(i == model.step ? .isSelected : [])
                    .accessibilityIdentifier("builderStep-\(i)")
                    if i < RuleBuilderModel.steps.count - 1 {
                        Rectangle().fill(Color(.separator)).frame(height: 1)
                    }
                }
            }
            Text("Step \(model.step + 1) of 5 · \(RuleBuilderModel.steps[model.step])")
                .font(.subheadline.weight(.semibold))
            if let form = model.form {
                Text("\(form.metric) · starting from \(model.start == .blank ? "an empty rule" : model.fromVersion.map { "version \($0)" } ?? "the rule in effect")")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.horizontal)
        .padding(.vertical, 8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.bar)
    }
}

/// Back, Next and Save.
private struct StepButtons: View {
    @Environment(AppState.self) private var state
    let model: RuleBuilderModel
    let saved: (RuleVersion) -> Void

    var body: some View {
        HStack {
            if model.step > 0 {
                Button("Back") { Task { await model.go(model.step - 1, client: state.client) } }
                    .buttonStyle(.bordered)
                    .accessibilityIdentifier("previousStep")
            }
            Spacer()
            if model.step < 4 {
                Button("Next: \(RuleBuilderModel.steps[model.step + 1].lowercased())") {
                    Task { await model.go(model.step + 1, client: state.client) }
                }
                .buttonStyle(.borderedProminent)
                .foregroundStyle(Color.onAccent)
                .disabled(model.step == 0 && model.metric.isEmpty)
                .accessibilityIdentifier("nextStep")
            } else {
                Button(model.saving ? "Saving…" : "Save version") {
                    Task { if let v = await model.save(state.client) { saved(v) } }
                }
                .buttonStyle(.borderedProminent)
                .foregroundStyle(Color.onAccent)
                .disabled(model.saving)
                .accessibilityIdentifier("saveRule")
            }
        }
        .padding(.horizontal)
        .padding(.vertical, 10)
        .background(.bar)
    }
}

/// The draft against the rule in effect over the last 14 days, while editing.
private struct LivePreview: View {
    let model: RuleBuilderModel

    var body: some View {
        Section("Live preview · last 14 days") {
            if let form = model.form { Text(RuleText.sentence(form.spec)).font(.subheadline).accessibilityIdentifier("builderSentence") }
            switch model.side {
            case .idle, .loading:
                Text(model.changed ? "Resolving the draft…" : "No changes from the rule in effect yet.").font(.footnote).foregroundStyle(.secondary)
            case .unavailable:
                Text("Preview unavailable on this server; the review step still checks the rule.").font(.footnote)
            case .failed(let problem):
                Text("Not ready to preview: \(problem.detail ?? problem.title)").font(.footnote).foregroundStyle(.secondary)
            case .loaded(let preview):
                Text("\(preview.days.filter(RulePreview.changed).count) of \(preview.days.count) days differ from the rule in effect.")
                    .font(.footnote.weight(.semibold))
                    .accessibilityIdentifier("liveSummary")
            }
        }
    }
}
