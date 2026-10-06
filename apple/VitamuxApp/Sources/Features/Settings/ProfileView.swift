import Foundation
import Observation
import SwiftUI
import VitamuxKit

/// Settings › Profile (the panel's `/settings`): the signed-in owner, the timezone periods every
/// local date follows (`/timezone-periods`), and Withings notifications (`withings.notifications`).
@Observable
final class ProfileModel {
    typealias Period = Components.Schemas.TimezonePeriod

    private(set) var periods: Loadable<[ProfileModel.Period]> = .loading
    private(set) var withings: Bool?
    private(set) var withingsSaved = false
    private(set) var withingsProblem: Problem?
    private(set) var notice: String?
    private(set) var problem: Problem?
    private(set) var isBusy = false

    func load(_ client: Client?) async {
        guard let client else { return }
        periods = await Loadable { try await client.listTimezonePeriods().ok.body.json.timezonePeriods }
        let settings = await Loadable { try await client.getSettings().ok.body.json }
        switch settings {
        case .loaded(let value): withings = value.withings_notifications ?? false
        case .failed(let problem): withingsProblem = problem
        case .loading: break
        }
    }

    /// Adds a period, or changes `editing`; nil once saved, else the problem for the sheet.
    func save(tz: String, from: Date, editing: ProfileModel.Period?, client: Client?) async -> Problem? {
        guard let client else { return nil }
        let zone = tz.trimmingCharacters(in: .whitespaces)
        guard let timeZone = TimeZone(identifier: zone) else {
            return Problem(title: "Unknown timezone", fieldErrors: [.init(pointer: "/tz", detail: "Enter an IANA timezone such as Europe/Berlin.")])
        }
        isBusy = true
        defer { isBusy = false }
        notice = nil
        problem = nil
        let body = Components.Schemas.TimezonePeriodInput(tz: zone, validFrom: from)
        do {
            if let editing {
                _ = try await client.updateTimezonePeriod(path: .init(id: editing.id), body: .json(body)).ok
            } else {
                _ = try await client.createTimezonePeriod(body: .json(body)).created
            }
        } catch {
            return Problem(error)
        }
        notice = "Saved. Local dates from \(Self.label(from, in: timeZone)) (\(zone)) onward are being recomputed; affected days update shortly."
        await reload(client)
        return nil
    }

    func remove(_ period: ProfileModel.Period, client: Client?) async {
        guard let client else { return }
        isBusy = true
        defer { isBusy = false }
        notice = nil
        problem = nil
        do {
            _ = try await client.deleteTimezonePeriod(path: .init(id: period.id)).noContent
            notice = "Removed the \(period.tz) period. The previous period now extends over it and local dates are being recomputed."
        } catch {
            problem = Problem(error)
        }
        await reload(client)
    }

    func setWithings(_ on: Bool, client: Client?) async {
        guard let client else { return }
        withings = on
        withingsSaved = false
        withingsProblem = nil
        do {
            withings = try await client.patch(.init(withings_notifications: on)).withings_notifications ?? false
            withingsSaved = true
        } catch {
            withingsProblem = Problem(error)
            withings = !on
        }
    }

    private func reload(_ client: Client) async {
        if let fresh = try? await client.listTimezonePeriods().ok.body.json.timezonePeriods { periods = .loaded(fresh) }
    }

    /// An instant as wall-clock time in `zone`, as the panel's `zonedLabel`.
    static func label(_ date: Date, in zone: TimeZone) -> String {
        date.formatted(Date.FormatStyle(date: .abbreviated, time: .shortened, timeZone: zone))
    }
}

struct ProfileView: View {
    @Environment(AppState.self) private var state
    @State private var model = ProfileModel()
    @State private var editor: PeriodEditor?
    @State private var removing: ProfileModel.Period?

    var body: some View {
        List {
            Section("Account") {
                LabeledContent("Signed in as", value: state.username ?? "–")
                    .accessibilityIdentifier("accountUsername")
                NavigationLink("Password and two-factor", value: Route.settings(.security))
            }
            periodsSection
            withingsSection
        }
        .navigationTitle("Profile")
        .task { await model.load(state.client) }
        .refreshable { await model.load(state.client) }
        .sheet(item: $editor) { editor in
            PeriodSheet(model: model, editor: editor)
        }
        .confirmationDialog(
            "Remove the \(removing?.tz ?? "") period?",
            isPresented: Binding { removing != nil } set: { if !$0 { removing = nil } },
            titleVisibility: .visible,
            presenting: removing
        ) { period in
            Button("Remove \(period.tz)", role: .destructive) {
                Task { await model.remove(period, client: state.client) }
            }
            .accessibilityIdentifier("confirmRemovePeriod")
        } message: { _ in
            Text("The previous period extends over it and the local dates it covered are recomputed.")
        }
    }

    @ViewBuilder private var periodsSection: some View {
        Section {
            if let notice = model.notice { NoticeRow(text: notice) }
            if let problem = model.problem { ProblemRow(problem: problem) }
            switch model.periods {
            case .loading:
                ProgressView()
            case .failed(let problem):
                ProblemRow(problem: problem)
            case .loaded(let periods) where periods.isEmpty:
                Text("No timezone periods yet. Add the one you are in now.").foregroundStyle(.secondary)
            case .loaded(let periods):
                ForEach(periods, id: \.id) { period in
                    Button { editor = PeriodEditor(period: period) } label: { PeriodRow(period: period) }
                        .foregroundStyle(.primary)
                        .accessibilityIdentifier("period-\(period.tz)")
                        .swipeActions {
                            Button("Remove", role: .destructive) { removing = period }
                        }
                }
            }
            Button("Add a period", systemImage: "plus") { editor = PeriodEditor(period: nil) }
                .accessibilityIdentifier("addPeriod")
        } header: {
            Text("Time zones")
        } footer: {
            Text("Local dates (days, nights, sleep) follow the timezone in effect when a value was measured. Add a period when you move; changing one recomputes the local dates it covers.")
        }
    }

    @ViewBuilder private var withingsSection: some View {
        Section {
            if let on = model.withings {
                Toggle("Subscribe to Withings notifications", isOn: Binding { on } set: { value in
                    Task { await model.setWithings(value, client: state.client) }
                })
                .accessibilityIdentifier("withingsToggle")
            } else if model.withingsProblem == nil {
                ProgressView()
            }
            if model.withingsSaved { NoticeRow(text: "Saved.") }
            if let problem = model.withingsProblem { ProblemRow(problem: problem) }
        } header: {
            Text("Withings notifications")
        } footer: {
            Text("New measurements arrive sooner. Polling runs either way. Needs the public URL configured for the server.")
        }
    }
}

/// The sheet's subject: a new period, or the one being changed.
struct PeriodEditor: Identifiable {
    var period: ProfileModel.Period?
    var id: String { period?.id ?? "new" }
}

private struct PeriodRow: View {
    let period: ProfileModel.Period

    var body: some View {
        let zone = TimeZone(identifier: period.tz) ?? .gmt
        VStack(alignment: .leading, spacing: 2) {
            HStack {
                Text(period.tz).font(.headline)
                if period.validTo == nil {
                    Text("Current").font(.caption).foregroundStyle(.secondary)
                }
            }
            Text("From \(ProfileModel.label(period.validFrom, in: zone))").font(.subheadline)
            if let end = period.validTo {
                Text("Until \(ProfileModel.label(end, in: zone))").font(.subheadline).foregroundStyle(.secondary)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

/// Add or change a period: an IANA zone with suggestions, and the start as wall-clock time there.
private struct PeriodSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let model: ProfileModel
    let editor: PeriodEditor
    @State private var tz: String
    @State private var from: Date
    @State private var problem: Problem?
    @State private var isRemoving = false

    init(model: ProfileModel, editor: PeriodEditor) {
        self.model = model
        self.editor = editor
        _tz = State(initialValue: editor.period?.tz ?? TimeZone.current.identifier)
        _from = State(initialValue: editor.period?.validFrom ?? .now)
    }

    private var zone: TimeZone? { TimeZone(identifier: tz.trimmingCharacters(in: .whitespaces)) }

    /// Up to eight known zones containing what was typed, until it names one.
    private var suggestions: [String] {
        let typed = tz.trimmingCharacters(in: .whitespaces)
        guard typed.count >= 2, zone?.identifier != typed else { return [] }
        return Array(TimeZone.knownTimeZoneIdentifiers.filter { $0.localizedCaseInsensitiveContains(typed) }.prefix(8))
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("Timezone", text: $tz)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .accessibilityIdentifier("periodZone")
                    ForEach(suggestions, id: \.self) { name in
                        Button(name) { tz = name }
                            .accessibilityIdentifier("zoneSuggestion-\(name)")
                    }
                    FieldMessage(text: problem?.detail(for: "/tz"))
                } header: {
                    Text("Timezone")
                } footer: {
                    Text("An IANA name, like Europe/Berlin.")
                }
                Section {
                    DatePicker("Starts at", selection: $from)
                        .environment(\.timeZone, zone ?? .current)
                        .accessibilityIdentifier("periodStart")
                    FieldMessage(text: problem?.detail(for: "/valid_from"))
                } footer: {
                    Text("Wall-clock time in that timezone.")
                }
                if let problem, problem.fieldErrors.isEmpty {
                    Section { ProblemRow(problem: problem) }
                }
                if editor.period != nil {
                    Section {
                        Button("Remove period", role: .destructive) { isRemoving = true }
                            .accessibilityIdentifier("removePeriod")
                    }
                }
            }
            .navigationTitle(editor.period == nil ? "Add a period" : "Change period")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button(editor.period == nil ? "Add" : "Save") {
                        Task {
                            problem = await model.save(tz: tz, from: from, editing: editor.period, client: state.client)
                            if problem == nil { dismiss() }
                        }
                    }
                    .disabled(model.isBusy || tz.trimmingCharacters(in: .whitespaces).isEmpty)
                    .accessibilityIdentifier("savePeriod")
                }
            }
            .confirmationDialog("Remove the \(editor.period?.tz ?? "") period?", isPresented: $isRemoving, titleVisibility: .visible) {
                Button("Remove \(editor.period?.tz ?? "")", role: .destructive) {
                    guard let period = editor.period else { return }
                    Task {
                        await model.remove(period, client: state.client)
                        dismiss()
                    }
                }
                .accessibilityIdentifier("confirmRemovePeriod")
            } message: {
                Text("The previous period extends over it and the local dates it covered are recomputed.")
            }
        }
    }
}
