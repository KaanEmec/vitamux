import SwiftUI

/// Sign-in, the lock screen or the tabs; links, theme and the app-switcher cover for all three.
struct RootView: View {
    @Environment(AppState.self) private var state
    @Environment(\.scenePhase) private var phase

    var body: some View {
        Group {
            if !state.isSignedIn {
                SignInView()
            } else if state.isLocked {
                LockView()
            } else {
                ShellView()
            }
        }
        // The app-switcher snapshot is taken while inactive: blur and cover it.
        .blur(radius: phase == .active ? 0 : 24)
        .overlay {
            if phase != .active { PrivacyCover() }
        }
        .tint(Color.accent)
        .preferredColorScheme(state.theme.colorScheme)
        .onOpenURL { state.open($0) }
        .onChange(of: phase) { _, new in state.scenePhaseChanged(to: new) }
    }
}

private struct PrivacyCover: View {
    var body: some View {
        ZStack {
            Rectangle().fill(.regularMaterial)
            AppMark()
        }
        .ignoresSafeArea()
        .accessibilityHidden(true)
    }
}
