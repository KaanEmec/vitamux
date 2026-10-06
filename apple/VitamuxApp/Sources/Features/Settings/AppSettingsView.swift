import SwiftUI
import VitamuxKit

/// Settings › This app (`vitamux://settings/app`): appearance, app lock and its timeout,
/// notification categories (J22.21), widget redaction (J22.20), the offline cache (J22.19), and the
/// server with sign-out. Local to this iPhone; nothing here goes to the server.
struct AppSettingsView: View {
    @Environment(AppState.self) private var state
    @State private var problem: Problem?
    @State private var cacheBytes = AppPreferences.cacheBytes
    @State private var isClearing = false
    @State private var isSigningOut = false
    @AppStorage(AppPreferences.redactWidgets, store: AppPreferences.store) private var redactWidgets = true

    var body: some View {
        @Bindable var state = state
        Form {
            Section("Appearance") {
                Picker("Theme", selection: $state.theme) {
                    ForEach(Theme.allCases, id: \.self) { Text($0.label).tag($0) }
                }
                .pickerStyle(.segmented)
            }
            Section {
                Toggle("Lock with \(state.lockMethod)", isOn: Binding {
                    state.appLock
                } set: { on in
                    Task { problem = await state.setAppLock(on) }
                })
                .accessibilityIdentifier("appLockToggle")
                if state.appLock {
                    Picker("Require", selection: $state.lockAfter) {
                        ForEach(LockTimeout.allCases, id: \.self) { Text($0.label).tag($0) }
                    }
                }
            } header: {
                Text("App lock")
            } footer: {
                Text("Asks when the app opens and after it has been away. The app-switcher preview is always blurred.")
            }
            if let problem {
                Section { ProblemView(problem: problem) }
            }
            Section {
                ForEach(AppPreferences.Notification.allCases, id: \.self) { NotificationToggle(category: $0) }
            } header: {
                Text("Notifications")
            } footer: {
                Text("Local notifications from this iPhone, sent when something changes. They never contain health values.")
            }
            Section {
                Toggle("Hide values while locked", isOn: $redactWidgets)
                    .accessibilityIdentifier("redactWidgets")
            } header: {
                Text("Widgets")
            } footer: {
                Text("Widgets on the lock screen and home screen show placeholders until the iPhone is unlocked.")
            }
            Section {
                LabeledContent("Stored on this iPhone", value: SettingsFormat.bytes(cacheBytes))
                    .accessibilityIdentifier("cacheSize")
                Button("Clear the cache…", role: .destructive) { isClearing = true }
                    .accessibilityIdentifier("clearCache")
            } header: {
                Text("Offline cache")
            } footer: {
                Text("Screens you opened, kept for reading offline. Signing out or changing the server clears it too.")
            }
            Section {
                LabeledContent("Signed in to", value: state.profile?.baseURL.host() ?? "")
                Button("Sign out…", role: .destructive) { isSigningOut = true }
                    .accessibilityIdentifier("appSignOut")
            } header: {
                Text("Server")
            }
        }
        .navigationTitle("This app")
        .confirmationDialog("Clear the offline cache?", isPresented: $isClearing, titleVisibility: .visible) {
            Button("Clear the cache", role: .destructive) {
                AppPreferences.clearCache()
                cacheBytes = AppPreferences.cacheBytes
            }
            .accessibilityIdentifier("confirmClearCache")
        } message: {
            Text("Screens load from the server again the next time you open them.")
        }
        .sheet(isPresented: $isSigningOut) { SignOutSheet() }
    }
}

private struct NotificationToggle: View {
    let category: AppPreferences.Notification
    @AppStorage private var isOn: Bool

    init(category: AppPreferences.Notification) {
        self.category = category
        _isOn = AppStorage(wrappedValue: true, category.rawValue, store: AppPreferences.store)
    }

    var body: some View {
        Toggle(category.title, isOn: $isOn)
            .accessibilityIdentifier(category.rawValue)
    }
}
