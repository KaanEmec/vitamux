import SwiftUI

/// The app's mark: a pulse line in a rounded square (sign-in, lock screen, app-switcher cover).
struct AppMark: View {
    var body: some View {
        Image(systemName: "waveform.path.ecg")
            .font(.system(size: 40, weight: .semibold))
            .foregroundStyle(.tint)
            .frame(width: 84, height: 84)
            .background(.tint.opacity(0.12), in: .rect(cornerRadius: 24))
            .accessibilityHidden(true)
    }
}
