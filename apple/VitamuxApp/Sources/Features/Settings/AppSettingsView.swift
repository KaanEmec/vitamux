import SwiftUI
import VitamuxKit

/// Settings › This app (`vitamux://settings/app`): appearance, app lock and its timeout,
/// notification categories (J22.21), widget redaction (J22.20), the offline cache (J22.19), and the
/// server with sign-out. Local to this iPhone; nothing here goes to the server.
struct AppSettingsView: View {
    @Environment(AppState.self) private var state
    @Environment(Notifier.self) private var notifier
    @State private var problem: Problem?
    @State private var cacheBytes: Int64 = 0
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
                NotificationPermissionRow()
                ForEach(AppPreferences.Notification.allCases, id: \.self) { NotificationToggle(category: $0) }
                LabeledContent("Background checks", value: backgroundChecks)
                    .accessibilityIdentifier("backgroundChecks")
            } header: {
                Text("Notifications")
            } footer: {
                Text("Local notifications from this iPhone, sent when something changes, once per change. They name the source, document or job and never contain health values. iOS decides how often the background check runs.")
            }
            #if DEBUG
            if state.isUITest { NotificationTestSection() }
            #endif
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
                    .disabled(cacheBytes == 0)
                    .accessibilityIdentifier("clearCache")
            } header: {
                Text("Offline cache")
            } footer: {
                Text("What the app last showed, up to 100 MB on this iPhone, for when the server can't be reached. Never sign-in details, keys or lab PDFs. Cleared on sign-out.")
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
                state.cache.clear()
                cacheBytes = Int64(state.cache.size)
            }
            .accessibilityIdentifier("confirmClearCache")
        } message: {
            Text("Screens load from the server again the next time you open them.")
        }
        .task { cacheBytes = Int64(state.cache.size) }
        .sheet(isPresented: $isSigningOut) { SignOutSheet() }
        .task { await notifier.refreshPermission() }
    }

    private var backgroundChecks: String {
        guard let last = notifier.lastBackgroundRun else { return "None yet" }
        return "\(notifier.backgroundRuns), last \(last.formatted(.relative(presentation: .named)))"
    }
}

/// A category's toggle; turning one on asks for the permission the first time.
private struct NotificationToggle: View {
    @Environment(Notifier.self) private var notifier
    let category: AppPreferences.Notification
    @AppStorage private var isOn: Bool

    init(category: AppPreferences.Notification) {
        self.category = category
        _isOn = AppStorage(wrappedValue: true, category.rawValue, store: AppPreferences.store)
    }

    var body: some View {
        Toggle(category.title, isOn: $isOn)
            .accessibilityIdentifier(category.rawValue)
            .onChange(of: isOn) { _, on in
                Task {
                    if on, notifier.permission == .notAsked {
                        await notifier.requestPermission()
                    } else {
                        // Withdraws a category turned off; one turned on notifies what is current.
                        await notifier.check()
                    }
                }
            }
    }
}
