import SwiftUI
import VitamuxKit

/// Settings › This app (`vitamux://settings/app`): appearance and app lock. J22.13 adds
/// notifications, the offline cache and widget redaction.
struct AppSettingsView: View {
    @Environment(AppState.self) private var state
    @State private var problem: Problem?

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
        }
        .navigationTitle("This app")
    }
}
