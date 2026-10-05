import Foundation
import Observation
import VitamuxKit

/// The rule lens (the panel's `RuleLens`): a draft of the rule in effect
/// (`GET /rules/{metric}/versions`) with reorderable groups, exclusions, and the strategy, window
/// and minimum coverage the catalogue entry allows (`GET /metrics/{code}`); a debounced
/// `POST /resolution/preview` against the rule in effect; save, save and activate, revert and the
/// version history. Exclusion chips come from `GET /origins`, `/source-devices` and `/providers`.
@Observable
final class RuleLensModel {
    let metric: String
    /// The preview range: the chart's dates, at most the last 366 of them.
    let start: LocalDate
    let end: LocalDate

    private(set) var catalogue: Components.Schemas.Metric?
    private(set) var versions: Loadable<[RuleVersion]> = .loading
    private(set) var choices: [RuleChoice] = []
    private(set) var chips: [RuleChoice] = []
    private(set) var base: RuleVersion?
    private var baseText = ""
    private(set) var baseIDs: [String] = []
    var form: RuleForm?

    private(set) var preview = PreviewState.idle
    private(set) var problem: Problem?
    var ackError = ""
    var note = ""
    private(set) var busy = false
    private(set) var message = ""
    private(set) var revertTo: Int?
    /// Read aloud after a move ("garmin moved to position 1 of 3").
    private(set) var moved = ""

    init(metric: String, start: LocalDate, end: LocalDate) {
        self.metric = metric
        self.end = end
        self.start = max(start, end.adding(days: -365))
    }

    func load(_ client: Client?) async {
        guard let client else { return }
        async let named: Void = loadChoices(client)
        async let entry = try? client.getMetric(path: .init(code: metric)).ok.body.json
        await reload(client)
        catalogue = await entry
        await named
    }

    private func loadChoices(_ client: Client) async {
        async let origins = try? client.listOrigins().ok.body.json.origins
        async let devices = try? client.listSourceDevices().ok.body.json.devices
        async let providers = try? client.listProviders().ok.body.json.providers
        let (o, d, p) = await (origins ?? [], devices ?? [], providers ?? [])
        chips = RuleChoice.chips(origins: o, devices: d)
        choices = RuleChoice.sources(devices: d, providers: p, name: providerLabel)
    }

    private func reload(_ client: Client) async {
        let answer = await Loadable { try await client.listRuleVersions(path: .init(metric: metric)).ok.body.json.versions }
        // A metric without any rule answers 404: an empty history, not a failure.
        if case .failed(let p) = answer, p.status == 404 { versions = .loaded([]) } else { versions = answer }
        rebase()
    }

    /// Starts the draft again from the rule in effect.
    func rebase() {
        base = versions.value?.first(where: \.active)
        form = base.map { RuleForm(spec: $0.rule) }
        baseText = form?.spec.text ?? ""
        baseIDs = form?.groups.map(\.name) ?? []
        problem = nil
        ackError = ""
    }

    var draft: JSONValue? { form?.spec }
    var dirty: Bool { draft.map { $0.text != baseText } ?? false }
    /// Changes whenever the previewed draft does; the view's debounce task restarts on it.
    var draftKey: String { dirty ? draft?.text ?? "" : "" }

    /// Waits for the edits to settle, then resolves the draft beside the rule in effect.
    func previewAfterPause(_ client: Client?) async {
        problem = nil // a field error belongs to the draft that was saved
        guard let client, dirty, let spec = draft else {
            preview = .idle
            return
        }
        do { try await Task.sleep(for: .milliseconds(350)) } catch { return }
        preview = .loading
        let out = await PreviewState.run(client, spec: spec, start: start, end: end)
        guard !Task.isCancelled else { return }
        preview = out
    }

    // MARK: Edits

    func move(_ from: Int, to: Int) {
        guard var form, form.groups.indices.contains(from), form.groups.indices.contains(to), from != to else { return }
        let group = form.groups.remove(at: from)
        form.groups.insert(group, at: to)
        self.form = form
        moved = "\(RuleText.groupLabel(group.name)) moved to position \(to + 1) of \(form.groups.count)."
    }

    func move(fromOffsets source: IndexSet, toOffset destination: Int) {
        form?.groups.move(fromOffsets: source, toOffset: destination)
    }

    /// One-click exclusions: the named choices, then the origins and device types, minus those already excluded.
    var exclusionChoices: [RuleChoice] {
        let excluded = Set((form?.exclude ?? []).map(\.key))
        var seen = Set<String>()
        return (choices + chips).filter { !excluded.contains($0.selector.key) && seen.insert($0.label).inserted }
    }

    func exclude(_ choice: RuleChoice) {
        form?.exclude.append(choice.selector)
    }

    func removeExclusion(at index: Int) {
        guard form?.exclude.indices.contains(index) == true else { return }
        form?.exclude.remove(at: index)
    }

    func setAcknowledged(_ on: Bool) {
        guard var form else { return }
        form.acknowledged.removeAll { $0 == RuleSpec.sumWarning }
        if on {
            form.acknowledged.append(RuleSpec.sumWarning)
            ackError = ""
        }
        self.form = form
    }

    /// Minimum coverage in percent, 0 for none (opt-in).
    var coveragePercent: Double {
        get { ((Double(form?.minCoverage ?? "") ?? 0) * 100).rounded() }
        set { form?.minCoverage = newValue <= 0 ? "" : JSONValue.numberText(newValue / 100) }
    }

    // MARK: What the catalogue allows

    var allowedOps: [RuleOp] {
        RuleOp.allCases.filter { op in catalogue.map { c in c.strategies.contains { $0.rawValue == op.rawValue } } ?? true || op == form?.op }
    }

    var allowedWindows: [(kind: String, label: String)] {
        RuleWindow.kinds.filter { w in catalogue.map { c in c.windows.contains { $0.rawValue == w.kind } } ?? true || w.kind == form?.windowKind }
    }

    var hint: String {
        var parts = [form?.op.hint ?? ""]
        if let catalogue, !catalogue.strategies.contains(.sumAcrossSources) { parts.append("Sum isn't offered: this metric isn't additive.") }
        return parts.filter { !$0.isEmpty }.joined(separator: " ")
    }

    var nextVersion: Int {
        ((versions.value ?? []).filter { !$0.builtin }.map(\.version).max() ?? 1) + 1
    }

    var active: RuleVersion? { versions.value?.first(where: \.active) }

    /// A save's problem, else the preview's.
    var shownProblem: Problem? { problem ?? preview.problem }

    func error(_ pointer: String) -> String? {
        shownProblem?.detail(under: pointer)
    }

    /// Field errors a control shows; the banner lists the rest.
    static let controls = ["/spec/groups", "/spec/exclude", "/spec/strategy", "/spec/window", "/spec/quality/min_coverage", "/spec/acknowledged_warnings"]

    static func shownByControl(_ pointer: String) -> Bool {
        controls.contains { pointer == $0 || pointer.hasPrefix($0 + "/") }
    }

    // MARK: Saving

    /// POST /rules/{metric}/versions; a sum needs its acknowledgement first. True when saved.
    func save(activate: Bool, client: Client?) async -> Bool {
        guard let client, let form else { return false }
        message = ""
        if form.needsSumAck, !form.isSumAcknowledged {
            ackError = "Confirm that you understand the duplicate risk before saving a sum."
            return false
        }
        let previous = base
        busy = true
        defer { busy = false }
        problem = nil
        do {
            let trimmed = note.trimmingCharacters(in: .whitespacesAndNewlines)
            let body = Components.Schemas.RuleVersionInput(spec: try form.spec.container, note: trimmed.isEmpty ? nil : trimmed, activate: activate)
            let saved = try await client.createRuleVersion(path: .init(metric: metric), body: .json(body)).created.body.json
            note = ""
            await reload(client)
            // The version that was active before; a built-in becomes the owner's version 1 on first save.
            let back = previous.flatMap { p in p.builtin ? versions.value?.first { $0.basedOn == p.ref } : p }
            revertTo = activate ? back?.version : nil
            message = activate ? "Version \(saved.version) is now active." : "Saved version \(saved.version). The rule in effect is unchanged."
            return true
        } catch {
            let p = Problem(error)
            problem = p
            if p.code == "rule_warning_unacknowledged" { ackError = p.detail ?? "Acknowledge the duplicate risk." }
            return false
        }
    }

    /// POST /rules/{metric}/activate; `revert` words the message as going back.
    func activate(_ version: Int, revert: Bool = false, client: Client?) async -> Bool {
        guard let client else { return false }
        busy = true
        defer { busy = false }
        problem = nil
        do {
            _ = try await client.activateRule(path: .init(metric: metric), body: .json(.init(version: version))).ok.body.json
            await reload(client)
            revertTo = nil
            message = revert ? "Reverted: version \(version) is active again." : "Version \(version) is now active."
            return true
        } catch {
            problem = Problem(error)
            return false
        }
    }

    // MARK: The preview

    var summary: RulePreview.Summary? {
        preview.days.isEmpty ? nil : RulePreview.summary(preview.days)
    }

    /// "4 of 30 days change · mean +0.3 bpm · no new gaps".
    var summaryText: String {
        guard let summary else { return "" }
        var parts = ["\(summary.changed.count) of \(preview.days.count) days change"]
        if let shift = summary.shift {
            let size = Format.value(abs(shift), unit: summary.unit)
            parts.append("mean \(shift > 0 ? "+" : shift < 0 ? "−" : "±")\(size)")
        }
        parts.append(summary.newGaps == 0 ? "no new gaps" : "\(summary.newGaps) new \(summary.newGaps == 1 ? "gap" : "gaps")")
        return parts.joined(separator: " · ")
    }
}
