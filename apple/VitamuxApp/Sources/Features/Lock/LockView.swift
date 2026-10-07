import SwiftUI

/// App lock: shown on launch and after the timeout until Face ID, Touch ID or the passcode
/// unlocks. It asks at once, and again on the button.
struct LockView: View {
    @Environment(AppState.self) private var state

    var body: some View {
        VStack(spacing: 16) {
            AppMark()
            Text("Vitamux is locked").font(.title2.bold())
            Text("Unlock with \(state.lockMethod) to see your data.")
                .foregroundStyle(Color.inkMuted)
                .multilineTextAlignment(.center)
            Button("Unlock") { Task { await state.unlock() } }
                .buttonStyle(.borderedProminent)
                .foregroundStyle(Color.onAccent)
                .accessibilityIdentifier("unlockButton")
        }
        .padding(24)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color.ground)
        .task {
            // UI tests tap the button themselves.
            if !state.isUITest { await state.unlock() }
        }
    }
}
