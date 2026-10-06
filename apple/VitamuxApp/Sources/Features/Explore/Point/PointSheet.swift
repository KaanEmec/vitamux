import SwiftUI
import VitamuxKit

/// One window of the metric detail chart (the panel's PointPanel): its resolved value, status,
/// source and explanation, the rule's inputs with their records (provenance, exclude), and the
/// override actions. "All sources and overrides" opens the all-sources day. It reads the day
/// from the detail model, so a saved override shows here as soon as the values reload.
struct PointSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let model: MetricDetailModel
    let date: LocalDate
    @State private var override: OverrideRequest?
    @State private var provenance: ProvenanceRequest?

    var body: some View {
        let value = model.value(on: date)
        NavigationStack {
            List {
                ResolvedSection(code: model.code, value: value)
                InputsSection(code: model.code, value: value, override: $override, provenance: $provenance)
                BeatsPointLink(code: model.code, date: date)
                Section("Override") {
                    OverrideButtons(value: value, override: $override)
                    Button {
                        dismiss()
                        state.paths[state.tab, default: []].append(.metricDay(code: model.code, date: date.description))
                    } label: {
                        Label("All sources and overrides", systemImage: "square.stack.3d.up")
                    }
                    .accessibilityIdentifier("allSources")
                }
            }
            .navigationTitle(Format.day(date))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() }.accessibilityIdentifier("closePoint") }
            }
        }
        .presentationDetents([.medium, .large])
        .sheet(item: $override) { request in
            OverrideSheet(request: request, metric: model.code, date: date, value: value) { model.changed() }
        }
        .sheet(item: $provenance) { request in
            ProvenanceSheet(request: request)
        }
    }
}

/// What an override button opens: the action, and the record when it came from one.
struct OverrideRequest: Identifiable {
    var action: Components.Schemas.OverrideInput.ActionPayload
    var inputID = ""
    var id: String { "\(action.rawValue)/\(inputID)" }
}

/// A record whose provenance chain to show.
struct ProvenanceRequest: Identifiable {
    var entity = Operations.GetProvenance.Input.Path.EntityPayload.measurement
    var recordID: String
    var id: String { "\(entity.rawValue)/\(recordID)" }
}

/// The value, its status and source, the explanation and warnings.
struct ResolvedSection: View {
    let code: String
    let value: ResolvedValue?

    var body: some View {
        Section {
            if let value {
                HStack(alignment: .firstTextBaseline) {
                    Text(Format.resolved(value, code: code))
                        .font(.system(.title, design: .rounded, weight: .semibold))
                        .monospacedDigit()
                        .accessibilityIdentifier("pointValue")
                    Spacer()
                    StatusLabel(status: DataStatus(value)).accessibilityIdentifier("pointStatus")
                }
                if let group = value.selected ?? value.inputs?.first(where: { $0.selected == true })?.group {
                    Text("from \(groupLabel(group))").foregroundStyle(.secondary).accessibilityIdentifier("pointSource")
                }
                Text(value.explanation).accessibilityIdentifier("pointExplanation")
                let warnings = warningText(value)
                if !warnings.isEmpty {
                    Label("Warnings: \(warnings.joined(separator: ", "))", systemImage: "exclamationmark.triangle")
                        .font(.footnote)
                        .foregroundStyle(.orange)
                }
            } else {
                Text("No resolved value for this window.").foregroundStyle(.secondary)
            }
        }
    }
}

/// Each rule group's input with its records: provenance and exclusion per record.
struct InputsSection: View {
    let code: String
    let value: ResolvedValue?
    @Binding var override: OverrideRequest?
    @Binding var provenance: ProvenanceRequest?

    var body: some View {
        if let inputs = value?.inputs, !inputs.isEmpty {
            Section("Rule inputs") {
                ForEach(inputs.indices, id: \.self) { i in
                    InputRow(input: inputs[i], unit: value?.unit ?? "", code: code, override: $override, provenance: $provenance)
                }
            }
        }
    }
}

private struct InputRow: View {
    let input: Components.Schemas.ResolvedInput
    let unit: String
    let code: String
    @Binding var override: OverrideRequest?
    @Binding var provenance: ProvenanceRequest?

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(input.group.map(groupLabel) ?? "Outside the rule").font(.body.weight(.semibold))
                if input.selected == true {
                    Text("selected").font(.caption.weight(.semibold)).padding(.horizontal, 6).padding(.vertical, 2).background(.fill.tertiary, in: .capsule)
                }
                Spacer()
                if let number = input.value?.number(for: code) { Text(Format.value(number, unit: unit)).monospacedDigit() }
            }
            Text(details).font(.footnote).foregroundStyle(.secondary)
            ForEach(input.recordRefs ?? [], id: \.self) { ref in
                HStack(spacing: 16) {
                    Text(ref).font(.caption.monospaced()).foregroundStyle(.secondary)
                    Button("Provenance") { provenance = ProvenanceRequest(recordID: ref) }
                        .accessibilityIdentifier("provenance-\(ref)")
                    Button("Exclude") { override = OverrideRequest(action: .excludeInput, inputID: ref) }
                        .accessibilityIdentifier("exclude-\(ref)")
                }
                .buttonStyle(.borderless)
                .font(.footnote)
            }
        }
        .padding(.vertical, 2)
    }

    private var details: String {
        var parts = [input.status.replacingOccurrences(of: "_", with: " ")]
        if let basis = input.basis { parts.append(basis.replacingOccurrences(of: "_", with: " ")) }
        if let coverage = input.coverage { parts.append("\(Int((coverage * 100).rounded())) % coverage") }
        if let reason = input.reason, !reason.isEmpty { parts.append(reason) }
        return parts.joined(separator: " · ")
    }
}

/// Exclude an input, force a source, set a value.
struct OverrideButtons: View {
    let value: ResolvedValue?
    @Binding var override: OverrideRequest?

    var body: some View {
        Button("Exclude an input…", systemImage: "minus.circle") { override = OverrideRequest(action: .excludeInput) }
            .accessibilityIdentifier("excludeInput")
        Button("Force a source…", systemImage: "arrow.right.circle") { override = OverrideRequest(action: .forceSource) }
            .disabled(ruleGroups(value).isEmpty)
            .accessibilityIdentifier("forceSource")
        Button("Set a value…", systemImage: "pencil.circle") { override = OverrideRequest(action: .setValue) }
            .accessibilityIdentifier("setValue")
    }
}
