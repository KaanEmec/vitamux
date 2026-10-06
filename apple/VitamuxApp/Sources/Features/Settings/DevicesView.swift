import CoreImage.CIFilterBuiltins
import Foundation
import Observation
import SwiftUI
import VitamuxKit

/// Settings › Devices (the panel's `/settings/devices`): a pairing code with QR for another phone
/// (`POST /devices/pairing-codes`), the paired devices with requested and possibly-denied types
/// (`GET /devices`), resync (`request-anchor-reset`) and revoke, and the origin apps in Apple
/// Health classified native, relayed or direct (`/origins`).
@Observable
final class DevicesModel {
    typealias Device = Components.Schemas.PairedDevice
    typealias Origin = Components.Schemas.DataOrigin

    private(set) var devices: Loadable<[DevicesModel.Device]> = .loading
    private(set) var origins: Loadable<[DevicesModel.Origin]> = .loading
    private(set) var targets: [Components.Schemas.RelayTarget] = []
    private(set) var code: Components.Schemas.PairingCode?
    private(set) var codeProblem: Problem?
    private(set) var notice: String?
    private(set) var problem: Problem?
    private(set) var originNotice: String?
    private(set) var originProblem: Problem?
    private(set) var isBusy = false

    func load(_ client: Client?) async {
        guard let client else { return }
        await loadDevices(client)
        await loadOrigins(client)
    }

    func loadDevices(_ client: Client?) async {
        guard let client else { return }
        devices = await Loadable { try await client.listDevices().ok.body.json.devices }
    }

    private func loadOrigins(_ client: Client) async {
        let answer = await Loadable { try await client.listOrigins().ok.body.json }
        origins = switch answer {
        case .loaded(let value): .loaded(value.origins)
        case .failed(let problem): .failed(problem)
        case .loading: .loading
        }
        targets = answer.value?.relayTargets ?? targets
    }

    func createCode(_ client: Client?) async {
        guard let client else { return }
        isBusy = true
        defer { isBusy = false }
        codeProblem = nil
        do {
            code = try await client.createPairingCode().created.body.json
        } catch {
            codeProblem = Problem(error)
        }
    }

    /// Resets every type when `types` is empty.
    func resync(_ device: DevicesModel.Device, types: [String], client: Client?) async -> Problem? {
        guard let client else { return nil }
        isBusy = true
        defer { isBusy = false }
        do {
            let body = Operations.RequestDeviceAnchorReset.Input.Body.json(.init(types: types.isEmpty ? nil : types))
            _ = try await client.requestDeviceAnchorReset(path: .init(id: device.id), body: body).noContent
        } catch {
            return Problem(error)
        }
        let what = types.isEmpty ? "every type" : types.map(SettingsFormat.typeLabel).joined(separator: ", ")
        notice = "Resync requested for \(what). The device starts it on its next contact."
        problem = nil
        await loadDevices(client)
        return nil
    }

    func revoke(_ device: DevicesModel.Device, client: Client?) async {
        guard let client else { return }
        isBusy = true
        defer { isBusy = false }
        notice = nil
        problem = nil
        do {
            _ = try await client.revokeDevice(path: .init(id: device.id)).noContent
            notice = "Revoked \(device.name). Its next request is refused."
        } catch {
            problem = Problem(error)
        }
        await loadDevices(client)
    }

    func classify(_ origin: DevicesModel.Origin, relays target: String?, client: Client?) async {
        guard let client else { return }
        isBusy = true
        defer { isBusy = false }
        originNotice = nil
        originProblem = nil
        do {
            _ = try await client.classifyOrigin(path: .init(id: origin.id), body: .json(.init(relayedProvider: target))).noContent
            let name = origin.name ?? origin.originKey
            let change = target.map { "now relays \(targetName($0))" } ?? "now records its own data"
            originNotice = "\(name) \(change). Resolved values are being recomputed."
        } catch {
            originProblem = Problem(error)
        }
        await loadOrigins(client)
    }

    func targetName(_ code: String) -> String {
        targets.first { $0.code == code }?.name ?? code
    }
}

struct DevicesView: View {
    @Environment(AppState.self) private var state
    @State private var model = DevicesModel()
    @State private var resyncing: SettingsItem<DevicesModel.Device>?
    @State private var revoking: DevicesModel.Device?
    /// This iPhone's own pairing, read from the Keychain once.
    @State private var thisDeviceID: String?

    var body: some View {
        List {
            PairingSection(model: model)
            devicesSection
            OriginsSection(model: model)
            Section {
                LabeledContent {
                    Text("Not built yet (J22.25)")
                } label: {
                    Label("Take or ignore per app", systemImage: "line.3.horizontal.decrease.circle")
                }
                .foregroundStyle(.secondary)
                .accessibilityIdentifier("sourceFilterPlaceholder")
            } header: {
                Text("Apple Health source filter")
            } footer: {
                Text("Choose per app seen in Apple Health whether Vitamux takes its data or ignores it.")
            }
        }
        .navigationTitle("Devices")
        .task {
            thisDeviceID = state.device.credentials?.deviceID
            await model.load(state.client)
        }
        .refreshable { await model.load(state.client) }
        .sheet(item: $resyncing) { item in
            ResyncSheet(model: model, device: item.value)
        }
        .confirmationDialog(
            "Revoke \(revoking?.name ?? "")?",
            isPresented: Binding { revoking != nil } set: { if !$0 { revoking = nil } },
            titleVisibility: .visible,
            presenting: revoking
        ) { device in
            Button("Revoke \(device.name)", role: .destructive) {
                Task { await model.revoke(device, client: state.client) }
            }
            .accessibilityIdentifier("confirmRevokeDevice")
        } message: { device in
            Text(isThisIPhone(device)
                 ? "Apple Health stops uploading from this iPhone; its next request is refused. Data already on the server stays."
                 : "Its next request is refused. Data already on the server stays.")
        }
    }

    private func isThisIPhone(_ device: DevicesModel.Device) -> Bool {
        thisDeviceID == device.id
    }

    @ViewBuilder private var devicesSection: some View {
        Section {
            if let notice = model.notice { NoticeRow(text: notice) }
            if let problem = model.problem { ProblemRow(problem: problem) }
            switch model.devices {
            case .loading: ProgressView()
            case .failed(let problem): ProblemRow(problem: problem)
            case .loaded(let devices) where devices.isEmpty:
                Text("No devices paired yet.").foregroundStyle(.secondary)
            case .loaded: EmptyView()
            }
        } header: {
            Text("Paired devices")
        }
        ForEach(model.devices.value ?? [], id: \.id) { device in
            DeviceSection(
                device: device, isThisIPhone: isThisIPhone(device),
                resync: { resyncing = SettingsItem(id: device.id, value: device) }, revoke: { revoking = device }
            )
        }
    }
}

/// A one-time pairing code for another phone, with its QR and a countdown to its expiry.
private struct PairingSection: View {
    @Environment(AppState.self) private var state
    let model: DevicesModel

    var body: some View {
        Section {
            if let code = model.code {
                TimelineView(.periodic(from: .now, by: 1)) { context in
                    CodeView(code: code, payload: payload(code), now: context.date)
                }
                .task(id: code.code) {
                    // Reload once the code runs out: a device may have joined with it.
                    try? await Task.sleep(for: .seconds(max(0, code.expiresAt.timeIntervalSinceNow)))
                    guard !Task.isCancelled else { return }
                    await model.loadDevices(state.client)
                }
            }
            if let problem = model.codeProblem { ProblemRow(problem: problem) }
            Button(model.code == nil ? "Create a pairing code" : "Create a new code", systemImage: "qrcode") {
                Task { await model.createCode(state.client) }
            }
            .disabled(model.isBusy)
            .accessibilityIdentifier("createPairingCode")
        } header: {
            Text("Pair another phone")
        } footer: {
            Text("Scan the QR code with the Vitamux app on another iPhone. The code works once and expires after 10 minutes. This iPhone pairs itself under Apple Health.")
        }
    }

    /// The server's QR text, or the same `{"url","code"}` for this server when it sends none.
    private func payload(_ code: Components.Schemas.PairingCode) -> String {
        if let payload = code.qrPayload { return payload }
        let object = ["url": code.url ?? state.profile?.baseURL.absoluteString ?? "", "code": code.code]
        let data = (try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])) ?? Data()
        return String(decoding: data, as: UTF8.self)
    }
}

private struct CodeView: View {
    let code: Components.Schemas.PairingCode
    let payload: String
    let now: Date

    var body: some View {
        let left = max(0, Int(code.expiresAt.timeIntervalSince(now).rounded(.up)))
        VStack(spacing: 12) {
            if left > 0, let image = QRCode.image(payload) {
                Image(uiImage: image)
                    .interpolation(.none)
                    .resizable()
                    .scaledToFit()
                    .frame(maxWidth: 220)
                    .accessibilityLabel("Pairing QR code")
                    .accessibilityIdentifier("pairingQR")
            }
            Text(code.code)
                .font(.title2.monospaced())
                .textSelection(.enabled)
                .accessibilityIdentifier("pairingCode")
            Text(left > 0 ? "Expires in \(left / 60):\(String(format: "%02d", left % 60))" : "Expired. Create a new code.")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .monospacedDigit()
                .accessibilityIdentifier("pairingCountdown")
        }
        .frame(maxWidth: .infinity)
    }
}

enum QRCode {
    /// A QR code of `text`, scaled up without smoothing.
    static func image(_ text: String) -> UIImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(text.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage?.transformed(by: CGAffineTransform(scaleX: 8, y: 8)),
              let image = CIContext().createCGImage(output, from: output.extent)
        else { return nil }
        return UIImage(cgImage: image)
    }
}

/// One paired device: last contact, requested types with "possibly denied" hints, resync and revoke.
private struct DeviceSection: View {
    let device: DevicesModel.Device
    let isThisIPhone: Bool
    let resync: () -> Void
    let revoke: () -> Void

    var body: some View {
        Section {
            LabeledContent("Last seen", value: device.lastSeenAt.map(Self.contact) ?? "Never")
            LabeledContent("Last sync", value: device.lastSyncAt.map(Self.contact) ?? "Never")
            if device.types.isEmpty {
                Text("Requested types not reported yet").foregroundStyle(.secondary)
            } else {
                ForEach(device.types, id: \.self) { type in
                    TypeRow(type: type, denied: device.possiblyDenied.contains(type))
                }
            }
            if let reset = device.anchorResets.map(\.requestedAt).max() {
                Text("Resync requested \(SettingsFormat.when(reset)).").font(.footnote).foregroundStyle(.secondary)
            }
            if let revoked = device.revokedAt {
                Label("Revoked \(SettingsFormat.when(revoked))", systemImage: "nosign")
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("revoked-\(device.name)")
            } else {
                Button("Resync…", systemImage: "arrow.clockwise", action: resync)
                    .accessibilityIdentifier("resync-\(device.name)")
                Button("Revoke…", systemImage: "xmark.circle", role: .destructive, action: revoke)
                    .accessibilityIdentifier("revoke-\(device.name)")
            }
        } header: {
            HStack {
                Text(device.name)
                if isThisIPhone { Text("· This iPhone") }
            }
        } footer: {
            if !device.possiblyDenied.isEmpty {
                Text("iOS does not tell Vitamux when you deny a type. A requested type that stored nothing for 7 days is flagged as possibly denied; check Settings › Health › Data Access on the phone. It may also just have nothing new.")
            }
        }
    }

    private static func contact(_ date: Date) -> String {
        "\(SettingsFormat.when(date)) (\(SettingsFormat.ago(date)))"
    }
}

private struct TypeRow: View {
    let type: String
    let denied: Bool

    var body: some View {
        HStack {
            Text(SettingsFormat.typeLabel(type))
            Spacer()
            if denied {
                Label("possibly denied", systemImage: "exclamationmark.triangle")
                    .font(.footnote)
                    .foregroundStyle(Color.feedbackWarn)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

/// Resync every type, or chosen ones: the device sends them again from scratch on its next contact.
private struct ResyncSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let model: DevicesModel
    let device: DevicesModel.Device
    @State private var everything = true
    @State private var chosen: Set<String> = []
    @State private var problem: Problem?

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Picker("Resync", selection: $everything) {
                        Text("Every type").tag(true)
                        Text("Chosen types").tag(false)
                    }
                    .pickerStyle(.segmented)
                    .accessibilityIdentifier("resyncScope")
                } footer: {
                    Text("Samples sent again are deduplicated by their HealthKit id, so nothing is counted twice.")
                }
                if !everything {
                    Section("Types") {
                        ForEach(device.types, id: \.self) { type in
                            Toggle(SettingsFormat.typeLabel(type), isOn: Binding {
                                chosen.contains(type)
                            } set: { on in
                                if on { chosen.insert(type) } else { chosen.remove(type) }
                            })
                        }
                    }
                }
                if let problem { Section { ProblemRow(problem: problem) } }
            }
            .navigationTitle("Resync \(device.name)")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Request") {
                        Task {
                            let types = everything ? [] : device.types.filter(chosen.contains)
                            problem = await model.resync(device, types: types, client: state.client)
                            if problem == nil { dismiss() }
                        }
                    }
                    .disabled(model.isBusy || (!everything && chosen.isEmpty))
                    .accessibilityIdentifier("requestResync")
                }
            }
        }
        .sheetBackground()
    }
}

/// The apps that recorded data inside Apple Health, and the vendor each one relays.
private struct OriginsSection: View {
    @Environment(AppState.self) private var state
    let model: DevicesModel

    var body: some View {
        Section {
            if let notice = model.originNotice { NoticeRow(text: notice) }
            if let problem = model.originProblem { ProblemRow(problem: problem) }
            switch model.origins {
            case .loading: ProgressView()
            case .failed(let problem): ProblemRow(problem: problem)
            case .loaded(let origins) where origins.isEmpty:
                Text("No origins yet. They appear once a device has synced.").foregroundStyle(.secondary)
            case .loaded(let origins):
                ForEach(origins, id: \.id) { origin in
                    OriginRow(origin: origin, model: model)
                }
            }
        } header: {
            Text("Origin apps in Apple Health")
        } footer: {
            Text("Mark an app that relays another vendor's data (such as Garmin Connect), so rules can prefer direct data or leave the relayed copy out.")
        }
    }
}

private struct OriginRow: View {
    @Environment(AppState.self) private var state
    let origin: DevicesModel.Origin
    let model: DevicesModel

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(origin.name ?? origin.originKey).font(.headline)
            if origin.name != nil {
                Text(origin.originKey).font(.caption.monospaced()).foregroundStyle(.secondary)
            }
            if origin.isNative {
                Label("Native", systemImage: "checkmark.seal").font(.subheadline)
            } else {
                Picker("Relays", selection: Binding {
                    origin.relayedProvider ?? ""
                } set: { value in
                    Task { await model.classify(origin, relays: value.isEmpty ? nil : value, client: state.client) }
                }) {
                    Text("Nothing (direct)").tag("")
                    ForEach(model.targets, id: \.code) { Text($0.name).tag($0.code) }
                }
                .disabled(model.isBusy)
                .accessibilityIdentifier("relays-\(origin.originKey)")
                Text(origin.relayedProvider.map { "Relayed from \(model.targetName($0))" } ?? "Direct: records its own data")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("originState-\(origin.originKey)")
            }
        }
    }
}
