import Foundation
import Observation
import SwiftUI
import VitamuxKit

/// A manual override for one metric window (`POST /overrides`, the panel's OverrideDialog):
/// exclude one input, force a rule group, or set a value with a note. Overrides are audited and
/// reversible; source rows never change (docs/architecture/resolution.md#manual-overrides).
@Observable
final class OverrideModel {
    typealias Action = Components.Schemas.OverrideInput.ActionPayload

    let metric: String
    let window: Components.Schemas.OverrideWindow
    let groups: [String]
    var action: Action
    var recordID: String
    var group: String
    var value = ""
    var unit: String
    var note = ""
    private(set) var problem: Problem?
    private(set) var isBusy = false

    init(request: OverrideRequest, metric: String, date: LocalDate, resolved: ResolvedValue?) {
        self.metric = metric
        let kind = (resolved?.window?.kind).flatMap(Components.Schemas.OverrideWindow.KindPayload.init(rawValue:)) ?? .localDay
        window = .init(kind: kind, key: resolved?.window?.key ?? date.description, localDate: date.description)
        groups = ruleGroups(resolved)
        action = request.action
        recordID = request.inputID
        group = groups.first ?? ""
        unit = resolved?.unit ?? ""
    }

    var canSave: Bool {
        guard !isBusy else { return false }
        return switch action {
        case .excludeInput: !recordID.trimmingCharacters(in: .whitespaces).isEmpty
        case .forceSource: !group.isEmpty
        case .setValue: Format.decimal(value) != nil && !unit.trimmingCharacters(in: .whitespaces).isEmpty && !note.trimmingCharacters(in: .whitespaces).isEmpty
        }
    }

    /// Saves the override; true once the server stored it.
    func save(_ client: Client?) async -> Bool {
        guard let client, canSave else { return false }
        isBusy = true
        defer { isBusy = false }
        problem = nil
        var body = Components.Schemas.OverrideInput(metric: metric, window: window, action: action)
        switch action {
        case .excludeInput: body.inputId = recordID.trimmingCharacters(in: .whitespaces)
        case .forceSource: body.group = group
        case .setValue:
            body.value = Format.decimal(value)
            body.unit = unit.trimmingCharacters(in: .whitespaces)
            body.note = note.trimmingCharacters(in: .whitespaces)
        }
        do {
            _ = try await client.createOverride(body: .json(body)).created
            return true
        } catch {
            problem = Problem(error)
            return false
        }
    }

    /// The field's message from the server, matched by JSON pointer.
    func error(_ field: String) -> String? {
        problem?.detail(for: "/\(field)")
    }
}

struct OverrideSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    @State private var model: OverrideModel
    let onSaved: () -> Void

    init(request: OverrideRequest, metric: String, date: LocalDate, value: ResolvedValue?, onSaved: @escaping () -> Void) {
        _model = State(initialValue: OverrideModel(request: request, metric: metric, date: date, resolved: value))
        self.onSaved = onSaved
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Picker("Action", selection: $model.action) {
                        Text("Exclude").tag(OverrideModel.Action.excludeInput)
                        Text("Force").tag(OverrideModel.Action.forceSource)
                        Text("Set value").tag(OverrideModel.Action.setValue)
                    }
                    .pickerStyle(.segmented)
                    .accessibilityIdentifier("overrideAction")
                } footer: {
                    Text("Overrides are audited and can be revoked. Source records are never changed.")
                }
                fields
                if let problem = model.problem, problem.fieldErrors.isEmpty {
                    Section { ProblemView(problem: problem) }
                }
            }
            .navigationTitle("Override \(Format.day(model.window.localDate, weekday: false))")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") {
                        Task {
                            if await model.save(state.client) {
                                onSaved()
                                dismiss()
                            }
                        }
                    }
                    .disabled(!model.canSave)
                    .accessibilityIdentifier("saveOverride")
                }
            }
        }
        .sheetBackground()
    }

    @ViewBuilder private var fields: some View {
        switch model.action {
        case .excludeInput:
            Section {
                TextField("Record id", text: $model.recordID)
                    .keyboardType(.numberPad)
                    .accessibilityIdentifier("recordField")
                FieldMessage(text: model.error("input_id"))
            } header: {
                Text("Record id")
            } footer: {
                Text("The id of a measurement listed under Rule inputs. The window is resolved again without it.")
            }
        case .forceSource:
            Section {
                Picker("Source group", selection: $model.group) {
                    ForEach(model.groups, id: \.self) { Text(groupLabel($0)).tag($0) }
                }
                .pickerStyle(.inline)
                .labelsHidden()
                .accessibilityIdentifier("groupPicker")
                FieldMessage(text: model.error("group"))
            } header: {
                Text("Source group")
            } footer: {
                Text("Used if it has a value in this window.")
            }
        case .setValue:
            Section {
                TextField("Value", text: $model.value)
                    .keyboardType(.decimalPad)
                    .accessibilityIdentifier("valueField")
                FieldMessage(text: model.error("value"))
                TextField("Unit", text: $model.unit)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .accessibilityIdentifier("unitField")
                FieldMessage(text: model.error("unit"))
            } header: {
                Text("Value")
            }
            Section {
                TextField("Why this value is set", text: $model.note, axis: .vertical)
                    .lineLimit(2...5)
                    .accessibilityIdentifier("noteField")
                FieldMessage(text: model.error("note"))
            } header: {
                Text("Note")
            } footer: {
                Text("Stored with the override.")
            }
        }
    }
}
