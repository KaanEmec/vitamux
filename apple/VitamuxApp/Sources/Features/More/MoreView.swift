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
    @State private var cacheBytes = AppPreferences.cacheBytes

    var body: some View {
        @Bindable var state = state
        List {
            Section {
                NavigationLink(value: Route.rules) { Label("Rules", systemImage: "slider.horizontal.3") }
                NavigationLink(value: Route.appleHealth) {
                    Row(title: "Apple Health", systemImage: "heart.fill", value: "iPhone and Watch")
                }
            }
            Section("Server settings") {
                ForEach(Self.serverPages, id: \.page) { item in
                    NavigationLink(value: Route.settings(item.page)) {
                        Row(title: item.title, systemImage: item.systemImage, value: value(for: item.page))
                    }
                    .accessibilityIdentifier("more-\(item.page.rawValue)")
                }
            }
            Section("This app") {
                Picker(selection: $state.theme) {
                    ForEach(Theme.allCases, id: \.self) { Text($0.label).tag($0) }
                } label: {
                    Label("Appearance", systemImage: "circle.lefthalf.filled")
                }
                .accessibilityIdentifier("themePicker")
                NavigationLink(value: Route.settings(.app)) {
                    Row(title: "App lock", systemImage: "faceid", value: state.appLock ? state.lockMethod : "Off")
                }
                .accessibilityIdentifier("appLockRow")
                NavigationLink(value: Route.settings(.app)) {
                    Row(title: "Notifications", systemImage: "bell",
                        value: "\(notificationsOn) of \(AppPreferences.Notification.allCases.count) on")
                }
                .accessibilityIdentifier("notificationsRow")
                NavigationLink(value: Route.settings(.app)) {
                    Row(title: "Offline cache", systemImage: "internaldrive", value: SettingsFormat.bytes(cacheBytes))
                }
            }
            Section {
                LabeledContent {
                    Text(state.profile?.baseURL.host() ?? "").accessibilityIdentifier("signedInServer")
                } label: {
                    Label("Signed in to", systemImage: "server.rack")
                }
                Button("Sign out…", role: .destructive) { isSigningOut = true }
                    .accessibilityIdentifier("signOutButton")
            }
        }
        .navigationTitle("More")
        .task { await model.load(state.client) }
        .onAppear {
            notificationsOn = AppPreferences.notificationsOn
            cacheBytes = AppPreferences.cacheBytes
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

    /// The artboard's order and copy; each opens `vitamux://settings/{page}`.
    static let serverPages: [(page: Route.SettingsPage, title: String, systemImage: String)] = [
        (.profile, "Profile and timezone", "person.crop.circle"),
        (.sources, "Sources", "point.3.connected.trianglepath.dotted"),
        (.devices, "Devices and origins", "iphone"),
        (.security, "Security", "lock.shield"),
        (.apiKeys, "API keys", "key"),
        (.ai, "AI providers", "sparkles"),
        (.retention, "Retention", "clock.arrow.circlepath"),
        (.backups, "Backups and export", "externaldrive"),
        (.system, "System status", "waveform.path.ecg"),
    ]
}

private struct Row: View {
    let title: String
    let systemImage: String
    let value: String?

    var body: some View {
        if let value {
            LabeledContent {
                Text(value)
            } label: {
                Label(title, systemImage: systemImage)
            }
        } else {
            Label(title, systemImage: systemImage)
        }
    }
}
