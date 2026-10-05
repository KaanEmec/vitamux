import SwiftUI
import VitamuxKit

typealias SourceDevice = Components.Schemas.SourceDevice
typealias DeviceRecords = Components.Schemas.DeviceRecords

/// The devices a connection's records were measured on (the panel's DevicesTab.svelte): type,
/// name and records, saved on change. The type drives device-type rule groups, so a type set here
/// wins over the provider's. Merge moves every record of one device onto another of the same
/// provider, for one physical device reported twice; it cannot be undone.
@Observable
final class DevicesModel {
    private(set) var devices: Loadable<[SourceDevice]> = .loading
    private(set) var types: [String] = []
    private(set) var problem: Problem?
    private(set) var saved = ""
    private(set) var busy = false

    func load(_ client: Client?) async {
        guard let client else { return }
        let answer = await Loadable { try await client.listSourceDevices(query: .init(include: [.records])).ok.body.json }
        devices = switch answer {
        case .loaded(let value): .loaded(value.devices)
        case .failed(let problem): .failed(problem)
        case .loading: .loading
        }
        types = answer.value?.deviceTypes ?? types
    }

    /// The provider's devices with records from this connection, or none anywhere yet.
    func listed(for connection: Connection) -> [SourceDevice] {
        (devices.value ?? []).filter { d in
            d.provider == connection.provider && d.mergedInto == nil && (own(d, connection) != nil || (d.connections ?? []).isEmpty)
        }
    }

    func merged(for connection: Connection) -> [SourceDevice] {
        let listed = Set(listed(for: connection).map(\.id))
        return (devices.value ?? []).filter { $0.mergedInto.map(listed.contains) ?? false }
    }

    func own(_ d: SourceDevice, _ connection: Connection) -> DeviceRecords? {
        d.connections?.first { $0.connectionId == connection.id }?.records
    }

    /// Every record of the device over all its connections: what a merge moves.
    func total(_ d: SourceDevice) -> DeviceRecords {
        var out = DeviceRecords(measurements: 0, groups: 0, sleepSessions: 0, workouts: 0, events: 0)
        for c in d.connections ?? [] {
            out.measurements += c.records.measurements
            out.groups += c.records.groups
            out.sleepSessions += c.records.sleepSessions
            out.workouts += c.records.workouts
            out.events += c.records.events
        }
        return out
    }

    func setType(_ d: SourceDevice, _ type: String, client: Client?) async {
        await patch(d, .init(deviceType: type), done: "\(DevicesModel.label(d)) is a \(type.replacingOccurrences(of: "_", with: " ")). Resolved values are being recomputed.", client: client)
    }

    func setName(_ d: SourceDevice, _ value: String, client: Client?) async {
        let name = value.trimmingCharacters(in: .whitespaces)
        guard !name.isEmpty, name != (d.name ?? "") else { return }
        await patch(d, .init(name: name), done: "Named \(name).", client: client)
    }

    private func patch(_ d: SourceDevice, _ body: Components.Schemas.SourceDevicePatch, done: String, client: Client?) async {
        guard let client else { return }
        problem = nil
        saved = ""
        busy = true
        do {
            _ = try await client.updateSourceDevice(path: .init(id: d.id), body: .json(body)).noContent
            saved = done
        } catch {
            problem = Problem(error)
        }
        busy = false
        await load(client)
    }

    func merge(_ d: SourceDevice, into target: SourceDevice, client: Client?) async {
        guard let client else { return }
        busy = true
        problem = nil
        saved = ""
        do {
            let moved = try await client.mergeSourceDevice(path: .init(id: d.id), body: .json(.init(into: target.id))).ok.body.json.moved
            saved = "\(Self.label(d)) merged into \(Self.label(target)): \(Self.recordText(moved).lowercased()) moved. Resolved values are being recomputed."
        } catch {
            problem = Problem(error)
        }
        busy = false
        await load(client)
    }

    static func label(_ d: SourceDevice) -> String {
        d.name ?? d.model ?? d.fingerprint
    }

    static func recordText(_ r: DeviceRecords?) -> String {
        guard let r else { return "No records" }
        let parts: [(Int64, String, String)] = [
            (r.measurements, "measurement", "measurements"), (r.groups, "reading", "readings"),
            (r.sleepSessions, "sleep session", "sleep sessions"), (r.workouts, "workout", "workouts"), (r.events, "event", "events"),
        ]
        let out = parts.filter { $0.0 > 0 }.map { "\($0.0.formatted()) \($0.0 == 1 ? $0.1 : $0.2)" }
        return out.isEmpty ? "No records" : out.joined(separator: " · ")
    }
}

struct DevicesTab: View {
    @Environment(AppState.self) private var state
    @State private var model = DevicesModel()
    let connection: Connection

    var body: some View {
        let listed = model.listed(for: connection)
        Section {
            if let problem = model.problem { ProblemRow(problem: problem) }
            if !model.saved.isEmpty { NoticeRow(text: model.saved, identifier: "deviceSaved") }
            switch model.devices {
            case .loading:
                ProgressView("Loading devices")
            case .failed(let problem):
                ProblemRow(problem: problem)
            case .loaded:
                if listed.isEmpty {
                    Text("No devices yet. They appear once this connection has stored records.").foregroundStyle(.secondary)
                }
            }
        } header: {
            Text("Devices")
        } footer: {
            if !listed.isEmpty {
                Text("Rules select sources by device type, for example a watch before a phone for steps. Set the type when the provider does not say what a device is, and merge two entries of one physical device so all its data is on one device.")
            }
        }
        .task { await model.load(state.client) }
        ForEach(listed, id: \.id) { device in
            DeviceSection(device: device, model: model, connection: connection, targets: listed.filter { $0.id != device.id })
        }
        let merged = model.merged(for: connection)
        if !merged.isEmpty {
            Section("Merged devices") {
                ForEach(merged, id: \.id) { d in
                    let into = listed.first { $0.id == d.mergedInto }.map(DevicesModel.label) ?? "another device"
                    Text("\(d.fingerprint) was merged into \(into); its new records are stored there.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }
}


private struct DeviceSection: View {
    @Environment(AppState.self) private var state
    let device: SourceDevice
    let model: DevicesModel
    let connection: Connection
    let targets: [SourceDevice]
    @State private var name = ""
    @State private var merging = false

    var body: some View {
        Section {
            VStack(alignment: .leading, spacing: 2) {
                Text(DevicesModel.label(device)).font(.headline)
                Text(([device.fingerprint] + [device.manufacturer].compactMap(\.self)).joined(separator: " · "))
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                Text(DevicesModel.recordText(model.own(device, connection))).font(.footnote).foregroundStyle(.secondary)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("device-\(device.fingerprint)")
            Picker("Type", selection: Binding { device.deviceType ?? "" } set: { type in
                Task { await model.setType(device, type, client: state.client) }
            }) {
                // A type can be set here, not cleared: the generated client cannot send null.
                if device.deviceType == nil { Text("Not set").tag("") }
                ForEach(types, id: \.self) { Text($0.replacingOccurrences(of: "_", with: " ")).tag($0) }
            }
            .disabled(model.busy)
            .accessibilityIdentifier("deviceType-\(device.fingerprint)")
            TextField("Name", text: $name, prompt: Text(device.model ?? device.fingerprint))
                .submitLabel(.done)
                .onSubmit { Task { await model.setName(device, name, client: state.client) } }
                .disabled(model.busy)
                .accessibilityIdentifier("deviceName-\(device.fingerprint)")
            if !targets.isEmpty {
                Button("Merge into…") { merging = true }
                    .disabled(model.busy)
                    .accessibilityLabel("Merge \(DevicesModel.label(device)) into another device")
                    .accessibilityIdentifier("merge-\(device.fingerprint)")
                    // On the row, not on the Section, so the list presents it once.
                    .sheet(isPresented: $merging) {
                        MergeSheet(device: device, targets: targets, model: model)
                    }
            }
        }
        .onAppear { name = device.name ?? "" }
    }

    private var types: [String] {
        let all = model.types
        guard let own = device.deviceType, !all.contains(own) else { return all }
        return all + [own]
    }
}

/// Merge with its confirmation: what moves, and that it cannot be undone here.
private struct MergeSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let device: SourceDevice
    let targets: [SourceDevice]
    let model: DevicesModel
    @State private var target = ""
    @State private var confirmed = false

    var body: some View {
        NavigationStack {
            Form {
                Picker("Merge into", selection: $target) {
                    ForEach(targets, id: \.id) { Text("\(DevicesModel.label($0)) (\($0.fingerprint))").tag($0.id) }
                }
                .accessibilityIdentifier("mergeTarget")
                Section {
                    Text("All records of \(DevicesModel.label(device)) (\(DevicesModel.recordText(model.total(device)).lowercased()), older versions included) move to \(chosen.map(DevicesModel.label) ?? "the device you pick"), and records it reports later are stored there too. Its own type and name no longer apply. Rules that name \(DevicesModel.label(device)) by its id stop matching it.")
                        .font(.subheadline)
                    Toggle("I understand that a merge cannot be undone here.", isOn: $confirmed)
                        .accessibilityIdentifier("confirmMerge")
                }
                Section {
                    Button("Merge devices", role: .destructive) {
                        Task {
                            guard let chosen else { return }
                            dismiss()
                            await model.merge(device, into: chosen, client: state.client)
                        }
                    }
                    .disabled(!confirmed || chosen == nil || model.busy)
                    .accessibilityIdentifier("mergeDevices")
                }
            }
            .navigationTitle("Merge \(DevicesModel.label(device))")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } } }
            .onAppear { if target.isEmpty { target = targets.first?.id ?? "" } }
        }
    }

    private var chosen: SourceDevice? { targets.first { $0.id == target } }
}
