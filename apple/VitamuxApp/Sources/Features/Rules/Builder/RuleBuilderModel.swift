import Foundation
import Observation
import VitamuxKit

/// The guided rule builder (the panel's `/rules/new`): metric → sources → strategy → window and
/// quality → review with a 14-day preview, one screen per step. It starts from the rule in
/// effect, a saved version (`from`) or an empty rule (`blank`), writes the typed rule JSON
/// (`RuleForm.spec`), and saves with `POST /rules/{metric}/versions`; server field errors send the
/// owner back to their step and control.
@Observable
final class RuleBuilderModel {
    enum Start: Hashable { case current, blank }

    static let steps = ["Metric", "Sources", "Strategy", "Window and quality", "Review"]

    private(set) var step = 0
    private(set) var active: [RuleVersion] = []
    private(set) var metrics: [String] = []
    private(set) var loadProblem: Problem?
    private(set) var isLoaded = false
    private(set) var chips: [RuleChoice] = []
    private(set) var choices: [RuleChoice] = []

    var metric = ""
    var start = Start.current
    private(set) var fromVersion: Int?
    private var builtFrom = ""
    var form: RuleForm?

    private(set) var problem: Problem?
    var ackError = ""
    var note = ""
    var activate = true
    private(set) var saving = false
    private(set) var preview = PreviewState.idle
    /// The live preview beside steps 2–4.
    private(set) var side = PreviewState.idle

    private let requested: (metric: String?, from: Int?, blank: Bool)

    init(metric: String?, from: Int?, blank: Bool) {
        requested = (metric, from, blank)
    }

    func load(_ client: Client?) async {
        guard let client, !isLoaded else { return }
        async let named: Void = loadChoices(client)
        async let catalogue = try? client.listMetrics().ok.body.json.metrics
        do {
            active = try await client.listRules().ok.body.json.rules
        } catch {
            loadProblem = Problem(error)
            await named
            return
        }
        var codes = active.map(\.metric)
        for m in await catalogue ?? [] where !codes.contains(m.code) { codes.append(m.code) }
        metrics = codes
        isLoaded = true
        await named
        if let m = requested.metric {
            metric = m
            start = requested.blank ? .blank : .current
            fromVersion = requested.from
            await go(1, client: client)
        }
    }

    private func loadChoices(_ client: Client) async {
        async let origins = try? client.listOrigins().ok.body.json.origins
        async let devices = try? client.listSourceDevices().ok.body.json.devices
        async let providers = try? client.listProviders().ok.body.json.providers
        let (o, d, p) = await (origins ?? [], devices ?? [], providers ?? [])
        chips = RuleChoice.chips(origins: o, devices: d)
        choices = RuleChoice.sources(devices: d, providers: p, name: providerLabel)
    }

    func activeRule(_ metric: String) -> RuleVersion? {
        active.first { $0.metric == metric }
    }

    /// Builds the form from the chosen metric and starting point; false if loading failed.
    private func build(_ client: Client) async -> Bool {
        let key = "\(metric)|\(start)|\(fromVersion ?? 0)"
        if form != nil, key == builtFrom { return true }
        loadProblem = nil
        if start == .blank {
            form = .blank(metric: metric)
        } else if let fromVersion {
            let versions = try? await client.listRuleVersions(path: .init(metric: metric)).ok.body.json.versions
            guard let v = versions?.first(where: { $0.version == fromVersion }) else {
                loadProblem = Problem(title: "Not found", detail: "No version \(fromVersion) of \(metric).", status: 404, code: "not_found")
                return false
            }
            form = RuleForm(spec: v.rule)
        } else {
            form = activeRule(metric).map { RuleForm(spec: $0.rule) } ?? .blank(metric: metric)
        }
        builtFrom = key
        problem = nil
        preview = .idle
        return true
    }

    func go(_ n: Int, client: Client?) async {
        guard let client else { return }
        if n > 0, !(await build(client)) { return }
        step = n
        if n == 4 { await runPreview(client) }
    }

    /// Changing the metric or start point starts the form again.
    func restart() {
        fromVersion = nil
    }

    func runPreview(_ client: Client?) async {
        guard let client, let form else { return }
        preview = .loading
        let range = lastDays(14)
        preview = await PreviewState.run(client, spec: form.spec, start: range.start, end: range.end)
    }

    // MARK: The live preview

    var currentSpec: JSONValue? {
        form.flatMap { activeRule($0.metric)?.rule }
    }

    /// The draft differs from the rule in effect.
    var changed: Bool {
        guard let form else { return false }
        return form.spec.text != currentSpec.map { RuleForm(spec: $0).spec.text }
    }

    var sideKey: String {
        step >= 1 && step <= 3 && changed ? form?.spec.text ?? "" : ""
    }

    func sidePreviewAfterPause(_ client: Client?) async {
        guard let client, !sideKey.isEmpty, let spec = form?.spec else {
            side = .idle
            return
        }
        do { try await Task.sleep(for: .milliseconds(400)) } catch { return }
        side = .loading
        let range = lastDays(14)
        let out = await PreviewState.run(client, spec: spec, start: range.start, end: range.end)
        if !Task.isCancelled { side = out }
    }

    // MARK: Edits

    func moveGroup(_ i: Int, by offset: Int) {
        guard var form, form.groups.indices.contains(i), form.groups.indices.contains(i + offset) else { return }
        form.groups.swapAt(i, i + offset)
        self.form = form
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

    /// A group names a provider that may also relay into Apple Health, and relays are not excluded.
    var relaySuggestion: Bool {
        guard let form else { return false }
        let direct = form.groups.contains { g in g.match.contains { if case .text(let p)? = $0[.provider] { !p.isEmpty && p != "apple_health" } else { false } } }
        let excluded = form.exclude.contains { $0[.provider] == .text("apple_health") && $0[.relayed] == .flag(true) }
        return direct && !excluded
    }

    func excludeRelays() {
        form?.exclude.append(RuleSelector([.provider: .text("apple_health"), .relayed: .flag(true)]))
    }

    // MARK: Errors

    func error(_ pointer: String) -> String? {
        problem?.detail(for: pointer)
    }

    /// The step whose controls own a field error.
    static func step(for pointer: String) -> Int {
        let path = pointer.hasPrefix("/spec/") ? String(pointer.dropFirst(6)) : pointer
        if path.hasPrefix("metric") { return 0 }
        if path.hasPrefix("groups") || path.hasPrefix("exclude") { return 1 }
        if path.hasPrefix("strategy") || path.hasPrefix("within_source") || path.hasPrefix("acknowledged_warnings") { return 2 }
        if ["window", "quality", "follow", "compose", "contexts"].contains(where: path.hasPrefix) { return 3 }
        return 4
    }

    func stepHasErrors(_ n: Int) -> Bool {
        (problem?.fieldErrors ?? []).contains { Self.step(for: $0.pointer) == n } || (n == 2 && !ackError.isEmpty)
    }

    /// Field errors an input shows; the banner lists the rest.
    static func shownByControl(_ pointer: String) -> Bool {
        pointer.wholeMatch(of: /\/spec\/(groups(\/\d+(\/id|\/match\/\d+(\/\w+)?)?)?|exclude\/\d+(\/\w+)?|strategy\/min_sources|acknowledged_warnings|within_source\/span|quality\/(min_coverage|plausible_range(\/\d)?|max_staleness|require_wear)|follow)/) != nil
    }

    // MARK: Saving

    /// POST /rules/{metric}/versions; the saved version, or nil with the problem shown on its step.
    func save(_ client: Client?) async -> RuleVersion? {
        guard let client, let form else { return nil }
        if form.needsSumAck, !form.isSumAcknowledged {
            ackError = "Confirm that you understand the duplicate risk before saving a sum."
            await go(2, client: client)
            return nil
        }
        saving = true
        defer { saving = false }
        problem = nil
        do {
            let trimmed = note.trimmingCharacters(in: .whitespacesAndNewlines)
            let body = Components.Schemas.RuleVersionInput(spec: try form.spec.container, note: trimmed.isEmpty ? nil : trimmed, activate: activate)
            return try await client.createRuleVersion(path: .init(metric: form.metric), body: .json(body)).created.body.json
        } catch {
            let p = Problem(error)
            problem = p
            if p.code == "rule_warning_unacknowledged" {
                ackError = p.detail ?? "Acknowledge the duplicate risk."
                await go(2, client: client)
            } else if let first = p.fieldErrors.first {
                await go(Self.step(for: first.pointer), client: client)
            }
            return nil
        }
    }
}
