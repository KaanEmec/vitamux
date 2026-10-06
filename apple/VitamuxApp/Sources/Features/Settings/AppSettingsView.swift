import SwiftUI
import VitamuxKit

/// Settings › This app (`vitamux://settings/app`): appearance, app lock and the offline cache.
/// J22.13 adds notifications and widget redaction.
struct AppSettingsView: View {
    @Environment(AppState.self) private var state
    @State private var problem: Problem?
    /// Bytes in the offline cache; nil until read.
    @State private var cacheSize: Int?

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
            Section {
                LabeledContent("Stored", value: cacheSize.map(Self.sizeText) ?? "…")
                    .accessibilityIdentifier("cacheSize")
                Button("Clear cache", role: .destructive) {
                    state.cache.clear()
                    cacheSize = state.cache.size
                }
                .disabled(cacheSize == 0)
                .accessibilityIdentifier("clearCacheButton")
            } header: {
                Text("Offline cache")
            } footer: {
                Text("What the app last showed, up to 100 MB on this iPhone, for when the server can't be reached. Never sign-in details, keys or lab PDFs. Cleared on sign-out.")
            }
            if let problem {
                Section { ProblemView(problem: problem) }
            }
        }
        .navigationTitle("This app")
        .task { cacheSize = state.cache.size }
    }

    private static func sizeText(_ bytes: Int) -> String {
        bytes == 0 ? "Empty" : Int64(bytes).formatted(.byteCount(style: .file))
    }
}
