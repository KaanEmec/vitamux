import SwiftUI

/// Tab root for Rules, Apple Health, the server's Settings pages and this app's own settings
/// (artboard Settings). Rows link to their routes; later jobs fill the pages.
struct MoreView: View {
    @Environment(AppState.self) private var state
    @State private var isSigningOut = false

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
                    NavigationLink(value: Route.settings(item.page)) { Label(item.title, systemImage: item.systemImage) }
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
        .sheet(isPresented: $isSigningOut) { SignOutSheet() }
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
    let value: String

    var body: some View {
        LabeledContent {
            Text(value)
        } label: {
            Label(title, systemImage: systemImage)
        }
    }
}
