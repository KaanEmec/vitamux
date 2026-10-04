import HealthBridgeHealthKit
import SwiftUI

struct RootView: View {
    @Bindable var model: AppModel

    var body: some View {
        if model.isPaired {
            TabView {
                NavigationStack { MetricsView(model: model) }
                    .tabItem { Label("Metrics", systemImage: "heart.text.square") }
                NavigationStack { StatusView(model: model) }
                    .tabItem { Label("Status", systemImage: "arrow.triangle.2.circlepath") }
                NavigationStack { SettingsView(model: model) }
                    .tabItem { Label("Settings", systemImage: "gearshape") }
            }
        } else {
            NavigationStack { PairingView(model: model) }
        }
    }
}

// MARK: Pairing

struct PairingView: View {
    @Bindable var model: AppModel
    @State private var url = ""
    @State private var code = ""
    @State private var scanning = false

    var body: some View {
        Form {
            Section {
                Text("Pair this iPhone with your own Vitamux server. In the Vitamux web panel, create a pairing code and scan its QR code, or type the address and code.")
                    .font(.callout)
                Button("Scan QR code", systemImage: "qrcode.viewfinder") { scanning = true }
                    .accessibilityIdentifier("scanButton")
            }
            Section("Enter manually") {
                TextField("https://vitamux.example.com", text: $url)
                    .textInputAutocapitalization(.never).autocorrectionDisabled().keyboardType(.URL).textContentType(.URL)
                    .accessibilityIdentifier("serverURLField")
                TextField("Pairing code", text: $code)
                    .textInputAutocapitalization(.characters).autocorrectionDisabled()
                    .accessibilityIdentifier("codeField")
                Button {
                    Task { await model.pair(url: url, code: code) }
                } label: {
                    HStack { Text("Pair"); if model.isBusy { Spacer(); ProgressView() } }
                }
                .disabled(model.isBusy || url.isEmpty || code.isEmpty)
                .accessibilityIdentifier("pairButton")
            }
            if let message = model.message {
                Section { Text(message).foregroundStyle(.red).accessibilityIdentifier("pairError") }
            }
            Section { NavigationLink("What this app reads and sends") { PrivacyView() } }
        }
        .navigationTitle("Pair")
        .sheet(isPresented: $scanning) {
            ScannerSheet { payload in
                scanning = false
                if let parsed = AppModel.parsePairingPayload(payload) {
                    url = parsed.url
                    code = parsed.code
                    Task { await model.pair(url: parsed.url, code: parsed.code) }
                } else {
                    model.message = "That QR code is not a Vitamux pairing code."
                }
            }
        }
    }
}

// MARK: Metric groups

struct MetricsView: View {
    @Bindable var model: AppModel

    var body: some View {
        List {
            Section {
                Label("Paired with \(model.credentials?.baseURL.host() ?? "server")", systemImage: "checkmark.seal")
                    .accessibilityIdentifier("pairedState")
            }
            if let message = model.message {
                Section { Text(message).foregroundStyle(.red) }
            }
            if !model.healthAvailable {
                Section { Text("Health data is not available on this device.").foregroundStyle(.secondary) }
            }
            Section {
                ForEach(MetricGroup.allCases, id: \.self) { group in
                    VStack(alignment: .leading, spacing: 4) {
                        Toggle(group.title, isOn: Binding(
                            get: { model.enabled.contains(group) },
                            set: { on in Task { await model.setGroup(group, enabled: on) } }))
                            .accessibilityIdentifier("group-\(group.rawValue)")
                        if model.enabled.contains(group) {
                            Text("Requested")
                                .font(.caption).foregroundStyle(.secondary)
                                .accessibilityIdentifier("requested-\(group.rawValue)")
                        }
                    }
                }
            } header: {
                Text("Metric groups")
            } footer: {
                Text("Turning a group on asks iOS for read access to its types and pulls their full history. iOS never says whether you allowed or denied a type, so this app shows \"Requested\", not \"Granted\". Change access in Settings > Health > Data Access.")
            }
        }
        .navigationTitle("Metrics")
    }
}

// MARK: Status

struct StatusView: View {
    @Bindable var model: AppModel

    var body: some View {
        List {
            Section {
                Button {
                    Task { await model.syncNow() }
                } label: {
                    HStack { Text("Sync now"); if model.isSyncing { Spacer(); ProgressView() } }
                }
                .disabled(model.isSyncing)
                .accessibilityIdentifier("syncNowButton")
                if model.needsRepair {
                    Text("The server rejected this device. Open Settings, unpair, and pair again.").foregroundStyle(.red)
                }
            } footer: {
                Text("iOS decides when background syncs run, and some types update at most hourly. Data arrives eventually, not instantly.")
            }
            if model.enabledTypes.isEmpty {
                Section { Text("No metric groups enabled yet.").foregroundStyle(.secondary) }
            }
            ForEach(MetricGroup.allCases.filter { model.enabled.contains($0) }, id: \.self) { group in
                Section(group.title) {
                    ForEach(model.types(in: group), id: \.id) { type in
                        TypeRow(model: model, type: type)
                    }
                }
            }
        }
        .navigationTitle("Status")
    }
}

struct TypeRow: View {
    let model: AppModel
    let type: HealthType

    var body: some View {
        let status = model.status[type.id]
        HStack(alignment: .top) {
            VStack(alignment: .leading, spacing: 2) {
                Text(type.displayName)
                if let last = status?.lastSync {
                    Text("Last sync \(last.formatted(.relative(presentation: .named)))").font(.caption).foregroundStyle(.secondary)
                } else {
                    Text("Not synced yet").font(.caption).foregroundStyle(.secondary)
                }
                if let error = status?.lastError {
                    Text(error).font(.caption).foregroundStyle(.red)
                }
            }
            Spacer()
            Menu {
                Button("Reset anchor and re-pull", systemImage: "arrow.counterclockwise") {
                    Task { await model.resetAnchor(type) }
                }
            } label: { Image(systemName: "ellipsis.circle") }
                .accessibilityLabel("Actions for \(type.displayName)")
        }
    }
}

// MARK: Settings and privacy

struct SettingsView: View {
    @Bindable var model: AppModel
    @State private var confirmReset = false
    @State private var confirmUnpair = false

    var body: some View {
        List {
            Section("Server") {
                LabeledContent("Address", value: model.credentials?.baseURL.absoluteString ?? "")
                LabeledContent("Device", value: model.credentials?.deviceID ?? "")
            }
            Section {
                Button("Reset all anchors", role: .destructive) { confirmReset = true }
                    .accessibilityIdentifier("resetAllButton")
                Button("Unpair this device", role: .destructive) { confirmUnpair = true }
                    .accessibilityIdentifier("unpairButton")
            } footer: {
                Text("Resetting anchors re-sends every enabled type in full; the server skips what it already has. Unpairing removes the token from this phone. Also revoke the device in the Vitamux web panel.")
            }
            Section { NavigationLink("Privacy") { PrivacyView() } }
        }
        .navigationTitle("Settings")
        .confirmationDialog("Re-send everything for all enabled types?", isPresented: $confirmReset, titleVisibility: .visible) {
            Button("Reset all anchors", role: .destructive) { Task { await model.resetAllAnchors() } }
        }
        .confirmationDialog("Unpair this device?", isPresented: $confirmUnpair, titleVisibility: .visible) {
            Button("Unpair", role: .destructive) { model.unpair() }
        }
    }
}

struct PrivacyView: View {
    var body: some View {
        List {
            Section("What is read") {
                Text("Only the Apple Health types in the metric groups you turn on, and only for reading. The app never writes to Apple Health. You can change access at any time in Settings > Health > Data Access & Devices.")
            }
            Section("Where it goes") {
                Text("Samples go over HTTPS to the Vitamux server address you paired with, which you run yourself. Each sample keeps its source app, device and timestamps so your server can choose between sources.")
            }
            Section("Nothing else") {
                Text("No analytics, no ads, no third-party services, no other servers. The device token is stored in this phone's Keychain. The camera is used only to scan the pairing QR code.")
            }
        }
        .navigationTitle("Privacy")
    }
}
