import SwiftUI
import VitamuxKit

typealias Schedule = Components.Schemas.Schedule

/// Connection settings (the panel's SettingsTab.svelte): pause or resume, sync schedules (interval
/// and on/off, saved on change), and removal keeping or deleting the data.
struct SettingsTab: View {
    @Environment(AppState.self) private var state
    @State private var schedules: Loadable<[Schedule]> = .loading
    @State private var problem: Problem?
    @State private var message = ""
    @State private var busy = false
    @State private var removing = false
    let connection: Connection
    let onChange: (Connection) -> Void

    private static let intervals = [900, 1_800, 3_600, 3 * 3_600, 6 * 3_600, 12 * 3_600, 86_400]

    var body: some View {
        Section("Syncing") {
            if let problem { ProblemRow(problem: problem) }
            if !message.isEmpty { NoticeRow(text: message, identifier: "settingsMessage") }
            switch connection.status {
            case .paused:
                Text("Scheduled and manual syncs are paused. Data already collected stays available.")
                Button("Resume syncing") { Task { await setPaused(false) } }
                    .disabled(busy)
                    .accessibilityIdentifier("resumeSyncing")
            case .active, .degraded:
                Text("Pausing stops scheduled syncs until you resume; nothing is deleted.")
                Button("Pause syncing") { Task { await setPaused(true) } }
                    .disabled(busy)
                    .accessibilityIdentifier("pauseSyncing")
            case .needsReauth:
                Text("Use Reauthorize at the top of this page to resume syncing.").foregroundStyle(.secondary)
            default:
                Text("This connection is \(connection.status.rawValue); connect the account again from Sources to resume.").foregroundStyle(.secondary)
            }
        }
        .task(id: connection.id) {
            guard let client = state.client else { return }
            schedules = await Loadable { try await client.listSchedules(query: .init(connection: connection.id)).ok.body.json.schedules }
        }
        switch schedules {
        case .loading:
            Section("Schedules") { ProgressView("Loading schedules") }
        case .failed(let problem):
            Section("Schedules") { ProblemRow(problem: problem) }
        case .loaded(let list):
            if list.isEmpty { Section("Schedules") { Text("No schedules.").foregroundStyle(.secondary) } }
            ForEach(list, id: \.id) { schedule in
                ScheduleSection(schedule: schedule, intervals: options(schedule)) { patch in
                    Task { await save(schedule, patch) }
                }
            }
        }
        Section {
            Text("Disconnect \(providerLabel(connection.provider)) and keep its data, or delete the connection with everything it collected.")
            Button("Remove connection…", role: .destructive) { removing = true }
                .accessibilityIdentifier("removeConnection")
                // On the row, not on the Section, so the list presents it once.
                .sheet(isPresented: $removing) {
                    DeleteSheet(connection: connection) { data in
                        Task { await deleted(data) }
                    }
                }
        } header: {
            Text("Remove")
        }
    }

    private func options(_ s: Schedule) -> [Int] {
        Self.intervals.contains(s.intervalSeconds) ? Self.intervals : (Self.intervals + [s.intervalSeconds]).sorted()
    }

    private func setPaused(_ paused: Bool) async {
        guard let client = state.client else { return }
        busy = true
        problem = nil
        message = ""
        defer { busy = false }
        do {
            let changed = try await client.updateConnection(path: .init(id: connection.id), body: .json(.init(status: paused ? .paused : .active))).ok.body.json
            message = paused ? "Syncing is paused." : "Syncing resumed."
            onChange(changed)
        } catch {
            problem = Problem(error)
        }
    }

    private func save(_ s: Schedule, _ patch: Components.Schemas.SchedulePatch) async {
        guard let client = state.client else { return }
        problem = nil
        message = ""
        do {
            let saved = try await client.updateSchedule(path: .init(id: s.id), body: .json(patch)).ok.body.json
            if case .loaded(var list) = schedules, let index = list.firstIndex(where: { $0.id == s.id }) {
                list[index] = saved
                schedules = .loaded(list)
            }
            message = "Schedule \(saved.stream) (\(saved.mode.rawValue)) saved."
        } catch {
            problem = Problem(error)
        }
    }

    private func deleted(_ data: Operations.DeleteConnection.Input.Query.DataPayload) async {
        if data == .delete {
            state.open(.connections(.init(removed: connection.provider)))
            return
        }
        if let changed = try? await state.client?.getConnection(path: .init(id: connection.id)).ok.body.json { onChange(changed) }
        message = "Disconnected. The data stays; connect the same account again to resume."
    }
}

/// One schedule: interval and on/off, each saved on change.
private struct ScheduleSection: View {
    let schedule: Schedule
    let intervals: [Int]
    let save: (Components.Schemas.SchedulePatch) -> Void

    var body: some View {
        Section {
            Picker("Interval", selection: Binding { schedule.intervalSeconds } set: { save(.init(intervalSeconds: $0)) }) {
                ForEach(intervals, id: \.self) { Text(SourcesCopy.every($0)).tag($0) }
            }
            .accessibilityIdentifier("interval-\(schedule.stream)-\(schedule.mode.rawValue)")
            Toggle("Enabled", isOn: Binding { schedule.enabled } set: { save(.init(enabled: $0)) })
                .accessibilityIdentifier("enabled-\(schedule.stream)-\(schedule.mode.rawValue)")
        } header: {
            Text("Schedule · \(schedule.stream) · \(schedule.mode.rawValue)").textCase(nil)
        } footer: {
            Text((schedule.lookbackSeconds > 0 ? "Looks back \(SourcesCopy.span(schedule.lookbackSeconds)). " : "")
                 + (schedule.enabled ? "Next run \(SourcesCopy.when(schedule.nextRunAt))." : "Off."))
        }
    }
}

/// Remove a connection, keeping or deleting its data (DELETE /connections/{id}?data=keep|delete).
private struct DeleteSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let connection: Connection
    let onDeleted: (Operations.DeleteConnection.Input.Query.DataPayload) -> Void
    @State private var data = Operations.DeleteConnection.Input.Query.DataPayload.keep
    @State private var confirmed = false
    @State private var problem: Problem?
    @State private var busy = false

    var body: some View {
        NavigationStack {
            Form {
                Section("What happens to its data?") {
                    choice(.keep, "Disconnect and keep the data")
                    choice(.delete, "Delete the connection and its data")
                    Text(data == .keep
                         ? "Vitamux forgets the authorization and stops syncing. Everything already collected stays, and connecting the same account again resumes it."
                         : "Also removes its original provider responses, records, cursors, schedules and job history. This cannot be undone.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
                if data == .delete {
                    Toggle("I understand that all data from this connection is deleted permanently.", isOn: $confirmed)
                        .accessibilityIdentifier("confirmDelete")
                }
                if let problem { Section { ProblemRow(problem: problem) } }
                Section {
                    Button(data == .delete ? "Delete connection and data" : "Disconnect and keep data", role: data == .delete ? .destructive : nil) {
                        Task { await submit() }
                    }
                    .disabled(busy || (data == .delete && !confirmed))
                    .accessibilityIdentifier("submitDelete")
                }
            }
            .scrollBounceBehavior(.basedOnSize)
            .navigationTitle("Remove the \(providerLabel(connection.provider)) connection")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } } }
        }
        .sheetBackground()
    }

    private func choice(_ value: Operations.DeleteConnection.Input.Query.DataPayload, _ title: String) -> some View {
        Button {
            data = value
            confirmed = false
        } label: {
            Label(title, systemImage: data == value ? "largecircle.fill.circle" : "circle")
        }
        .accessibilityAddTraits(data == value ? .isSelected : [])
        .accessibilityIdentifier("delete-\(value.rawValue)")
    }

    private func submit() async {
        guard let client = state.client else { return }
        busy = true
        problem = nil
        defer { busy = false }
        do {
            _ = try await client.deleteConnection(path: .init(id: connection.id), query: .init(data: data)).noContent
            dismiss()
            onDeleted(data)
        } catch {
            problem = Problem(error)
        }
    }
}
