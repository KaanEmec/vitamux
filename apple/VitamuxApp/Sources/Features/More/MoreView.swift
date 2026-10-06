import Observation
import SwiftUI
import VitamuxKit

/// The values the More rows show beside their titles (artboard Settings): paired devices,
/// two-factor, AI providers, the last backup and the server's health. Each is optional: a row
/// without its value still opens its page.
@Observable
final class MoreModel {
    private(set) var devices: Int?
    private(set) var totpEnabled: Bool?
    private(set) var aiEnabled: Bool?
    private(set) var lastBackup: Date??
    private(set) var issues: Int?

    func load(_ client: Client?) async {
        guard let client else { return }
        devices = (try? await client.listDevices().ok.body.json.devices)?.filter { $0.revokedAt == nil }.count
        totpEnabled = (try? await client.getSession().ok.body.json)?.user.totpEnabled
        if let settings = try? await client.getSettings().ok.body.json {
            aiEnabled = [
                settings.documents_externalAi_gemini_enabled, settings.documents_externalAi_openai_enabled,
                settings.documents_externalAi_openaiCompatible_enabled,
            ].contains(true)
        }
        if let status = try? await client.getSystemStatus().ok.body.json {
            lastBackup = .some(status.lastBackupAt)
            issues = status.degradedConnections.count + status.failingJobs.count
        }
    }
}

/// Tab root for Rules, Apple Health, the server's Settings pages and this app's own settings
/// (artboard Settings). Rows link to their routes.
struct MoreView: View {
    @Environment(AppState.self) private var state
    @State private var model = MoreModel()
    @State private var isSigningOut = false
    @State private var notificationsOn = AppPreferences.notificationsOn
    @State private var cacheBytes: Int64 = 0

    var body: some View {
        @Bindable var state = state
        List {
            Section {
                NavigationLink(value: Route.rules) { Row(title: "Rules", systemImage: "slider.horizontal.3", tile: .hue(.hrv), value: nil) }
                NavigationLink(value: Route.appleHealth) {
                    Row(title: "Apple Health", systemImage: "heart.fill", tile: .source("apple_health"), value: "iPhone and Watch")
                }
            }
            Section("Server settings") {
                ForEach(Self.serverPages, id: \.page) { item in
                    NavigationLink(value: Route.settings(item.page)) {
                        Row(title: item.title, systemImage: item.systemImage, tile: item.tile, value: value(for: item.page))
                    }
                    .accessibilityIdentifier("more-\(item.page.rawValue)")
                }
            }
            Section("This app") {
                Picker(selection: $state.theme) {
                    ForEach(Theme.allCases, id: \.self) { Text($0.label).tag($0) }
                } label: {
                    Label { Text("Appearance") } icon: { RowTile(systemImage: "circle.lefthalf.filled", tile: .hue(.sleep)) }
                }
                .accessibilityIdentifier("themePicker")
                NavigationLink(value: Route.settings(.app)) {
                    Row(title: "App lock", systemImage: "faceid", tile: .hue(.steps), value: state.appLock ? state.lockMethod : "Off")
                }
                .accessibilityIdentifier("appLockRow")
                NavigationLink(value: Route.settings(.app)) {
                    Row(title: "Notifications", systemImage: "bell.fill", tile: .hue(.activeEnergy),
                        value: "\(notificationsOn) of \(AppPreferences.Notification.allCases.count) on")
                }
                .accessibilityIdentifier("notificationsRow")
                NavigationLink(value: Route.settings(.app)) {
                    Row(title: "Offline cache", systemImage: "internaldrive.fill", tile: .hue(.other), value: SettingsFormat.bytes(cacheBytes))
                }
            }
            Section {
                LabeledContent {
                    Text(state.profile?.baseURL.host() ?? "").accessibilityIdentifier("signedInServer")
                } label: {
                    Label { Text("Signed in to") } icon: { RowTile(systemImage: "server.rack", tile: .hue(.vo2)) }
                }
                Button("Sign out…", role: .destructive) { isSigningOut = true }
                    .accessibilityIdentifier("signOutButton")
            }
        }
        .navigationTitle("More")
        .task { await model.load(state.client) }
        .onAppear {
            notificationsOn = AppPreferences.notificationsOn
            cacheBytes = Int64(state.cache.size)
        }
        .sheet(isPresented: $isSigningOut) { SignOutSheet() }
    }

    private func value(for page: Route.SettingsPage) -> String? {
        switch page {
        case .devices: model.devices.map { $0 == 1 ? "1 device" : "\($0) devices" }
        case .security: model.totpEnabled.map { $0 ? "Two-factor on" : "Two-factor off" }
        case .ai: model.aiEnabled.map { $0 ? "On" : "Off" }
        case .backups: model.lastBackup.map { $0.map(SettingsFormat.ago) ?? "No backup" }
        case .system: model.issues.map { $0 == 0 ? "Healthy" : $0 == 1 ? "1 issue" : "\($0) issues" }
        default: nil
        }
    }

    /// The artboard's order, copy and tile colours; each opens `vitamux://settings/{page}`.
    static let serverPages: [(page: Route.SettingsPage, title: String, systemImage: String, tile: RowTile.Colours)] = [
        (.profile, "Profile and timezone", "person.crop.circle.fill", .hue(.vo2)),
        (.sources, "Sources", "point.3.connected.trianglepath.dotted", .hue(.lab)),
        (.devices, "Devices and origins", "iphone", .hue(.spo2)),
        (.security, "Security", "lock.shield.fill", .hue(.steps)),
        (.apiKeys, "API keys", "key.fill", .hue(.weight)),
        (.ai, "AI providers", "sparkles", .hue(.bloodPressure)),
        (.retention, "Retention", "clock.arrow.circlepath", .hue(.other)),
        (.backups, "Backups and export", "externaldrive.fill", .info),
        (.system, "System status", "waveform.path.ecg", .hue(.vo2)),
    ]
}

/// A More row: its tile and title, and the page's value on the trailing side.
private struct Row: View {
    let title: String
    let systemImage: String
    let tile: RowTile.Colours
    let value: String?

    var body: some View {
        LabeledContent {
            if let value { Text(value) }
        } label: {
            Label { Text(title) } icon: { RowTile(systemImage: systemImage, tile: tile) }
        }
    }
}

/// The 40-point tile of a settings row, in the metric palette, a source's colour or info blue.
struct RowTile: View {
    enum Colours {
        case hue(MetricHue)
        case source(String)
        case info
    }

    let systemImage: String
    let tile: Colours

    var body: some View {
        switch tile {
        case .hue(let hue): IconTile(symbol: systemImage, color: hue.color, tint: hue.tint, size: TileSize.settings)
        case .source(let provider):
            IconTile(symbol: systemImage, color: SourceStyle.color(provider), tint: SourceStyle.color(provider).opacity(0.16), size: TileSize.settings)
        case .info: IconTile(symbol: systemImage, color: .feedbackInfo, tint: .feedbackInfoSoft, size: TileSize.settings)
        }
    }
}
