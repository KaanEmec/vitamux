import SwiftUI

/// The app's mark: a pulse line in a rounded square with a soft accent glow (sign-in, lock screen,
/// app-switcher cover; the app icon draws the same line).
struct AppMark: View {
    var body: some View {
        Image(systemName: "waveform.path.ecg")
            .font(.system(size: 40, weight: .semibold))
            .foregroundStyle(Color.accent)
            .frame(width: 84, height: 84)
            .background(Color.accentSoft, in: .rect(cornerRadius: 24, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 24, style: .continuous).strokeBorder(Color.accent.opacity(0.25)))
            .shadow(color: Color.accent.opacity(0.18), radius: 16)
            .accessibilityHidden(true)
    }
}
