import SwiftUI
import VitamuxKit

// Small views and helpers the rule screens share: version names, the source order, the field
// diff, the problem banner and preview text. Neutral copy only.

typealias RuleVersion = Components.Schemas.RuleVersion

extension RuleVersion {
    /// The typed rule, in order-free form.
    var rule: JSONValue { JSONValue(spec) }

    /// "Default rule", "Built-in", "Version 3".
    var name: String {
        _default ? "Default rule" : builtin ? "Built-in" : "Version \(version)"
    }
}

/// The last `days` local dates ending today.
func lastDays(_ days: Int, today: LocalDate = .today(in: .current)) -> (start: LocalDate, end: LocalDate) {
    (today.adding(days: -(days - 1)), today)
}

/// "No rule", "Default", "Built-in default" or "Your rule · version 2".
struct RuleBadge: View {
    let rule: RuleVersion?

    var body: some View {
        Text(text)
            .font(.caption.weight(.semibold))
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .foregroundStyle(custom ? Color.accentColor : .secondary)
            .background(custom ? Color.accentColor.opacity(0.15) : Color(.tertiarySystemFill), in: .capsule)
    }

    private var custom: Bool { rule.map { !$0.builtin } ?? false }

    private var text: String {
        guard let rule else { return "No rule" }
        if rule._default { return "Default" }
        if rule.builtin { return "Built-in default" }
        return "Your rule · version \(rule.version)"
    }
}

/// "Draft · not saved" and similar markers.
struct DraftBadge: View {
    var text = "Draft · not saved"

    var body: some View {
        Text(text)
            .font(.caption.weight(.semibold))
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .foregroundStyle(.purple)
            .background(Color.purple.opacity(0.15), in: .capsule)
    }
}

/// The groups in ladder order, each with its source colour.
struct SourceOrder: View {
    let spec: JSONValue

    var body: some View {
        let groups = RuleForm(spec: spec).groups
        let parts = groups.map { g in
            Text("\(Text("●").foregroundStyle(SourceStyle.color(RuleText.provider(of: g)))) \(RuleText.groupLabel(g.name))")
        }
        parts.dropFirst().reduce(parts.first ?? Text("")) { line, part in Text("\(line)  ›  \(part)") }
            .font(.footnote.weight(.medium))
            .accessibilityLabel("Source order: " + groups.map { RuleText.groupLabel($0.name) }.joined(separator: ", "))
    }
}

/// Field-level changes between two specs, one row per field.
struct RuleDiffList: View {
    let changes: [RuleChange]
    var emptyText = "No differences."

    var body: some View {
        if changes.isEmpty {
            Text(emptyText).foregroundStyle(.secondary)
        }
        ForEach(changes, id: \.path) { change in
            VStack(alignment: .leading, spacing: 2) {
                Text(change.path).font(.footnote.monospaced().weight(.semibold))
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(change.before?.text ?? "(none)").strikethrough(change.before != nil).foregroundStyle(.secondary)
                    Image(systemName: "arrow.right").font(.caption2).foregroundStyle(.tertiary)
                    Text(change.after?.text ?? "(removed)")
                }
                .font(.footnote.monospaced())
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("diff-\(change.path)")
        }
    }
}

/// A failed save or preview: its title and detail, and the field errors no control shows.
struct ProblemBanner: View {
    let problem: Problem
    var hidden: (String) -> Bool = { _ in false }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Label(problem.title, systemImage: "exclamationmark.triangle.fill")
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(.red)
            if let detail = problem.detail { Text(detail).font(.footnote) }
            ForEach(problem.fieldErrors.filter { !hidden($0.pointer) }, id: \.pointer) { error in
                Text("\(error.pointer): \(error.detail)").font(.footnote.monospaced())
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("ruleProblem")
    }
}

/// An inline error under a control.
struct InlineError: View {
    let text: String?
    let id: String

    var body: some View {
        if let text, !text.isEmpty {
            Label {
                Text(text).accessibilityIdentifier(id)
            } icon: {
                Image(systemName: "exclamationmark.circle.fill").accessibilityHidden(true)
            }
            .font(.footnote)
            .foregroundStyle(.red)
        }
    }
}

/// A preview's resolved value: "61.5 bpm · garmin", or "no value".
func previewText(_ value: RulePreview.Resolved) -> String {
    let shown = RulePreview.number(value).map { Format.value($0, unit: value.unit ?? "") } ?? "no value"
    let group = RulePreview.selectedGroup(value)
    return group.isEmpty ? shown : "\(shown) · \(group)"
}

/// The errors of a problem by JSON pointer: the message for `pointer` or anything under it.
extension Problem {
    func detail(under pointer: String) -> String? {
        let hits = fieldErrors.filter { $0.pointer == pointer || $0.pointer.hasPrefix(pointer + "/") }.map(\.detail)
        return hits.isEmpty ? nil : hits.joined(separator: "; ")
    }
}

/// How a draft preview stands.
enum PreviewState {
    case idle
    case loading
    /// 404 or 503: the server cannot resolve drafts yet.
    case unavailable
    case failed(Problem)
    case loaded(Components.Schemas.ResolutionPreview)

    /// POST /resolution/preview for `spec` over `start` … `end`.
    static func run(_ client: Client, spec: JSONValue, start: LocalDate, end: LocalDate) async -> PreviewState {
        do {
            let body = Components.Schemas.ResolutionPreviewRequest(spec: try spec.container, startDate: start.description, endDate: end.description)
            return .loaded(try await client.previewResolution(body: .json(body)).ok.body.json)
        } catch {
            let problem = Problem(error)
            return problem.status == 404 || problem.status == 503 ? .unavailable : .failed(problem)
        }
    }

    var days: [Components.Schemas.PreviewDay] {
        if case .loaded(let preview) = self { preview.days } else { [] }
    }

    var problem: Problem? {
        if case .failed(let problem) = self { problem } else { nil }
    }
}
