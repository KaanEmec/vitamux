import SwiftUI
import UIKit
import UniformTypeIdentifiers
import VitamuxKit

typealias ServerSettings = Components.Schemas.Settings

/// The screen for each Settings page (`vitamux://settings/{page}`), as the panel's Settings section.
struct SettingsPageView: View {
    let page: Route.SettingsPage

    var body: some View {
        switch page {
        case .profile: ProfileView()
        case .sources: SourcesSettingsView()
        case .devices: DevicesView()
        case .ai: AIProvidersView()
        case .apiKeys: APIKeysView()
        case .security: SecurityView()
        case .retention: RetentionView()
        case .backups: BackupsView()
        case .system: SystemStatusView()
        case .app: AppSettingsView()
        }
    }
}

/// Sizes and HealthKit names as the panel's settings pages print them (web/src/lib/settings/format.ts).
enum SettingsFormat {
    static func bytes(_ count: Int64) -> String {
        count == 0 ? "Empty" : count.formatted(.byteCount(style: .file))
    }

    /// "HKQuantityTypeIdentifierHeartRate" as "Heart rate"; anything else unchanged.
    static func typeLabel(_ id: String) -> String {
        guard let match = id.wholeMatch(of: /HK(?:Quantity|Category|Correlation|Data|Characteristic)?TypeIdentifier(.+)/) else { return id }
        let words = String(match.1).replacing(/([a-z0-9])([A-Z])/) { "\($0.1) \($0.2)" }.lowercased()
        return words.prefix(1).uppercased() + words.dropFirst()
    }
}

/// A secret the server shows once (an API key, a sidecar secret, a TOTP key, recovery codes): on
/// screen only while its sheet is open, never cached or logged, redacted in snapshots, and copied
/// to this iPhone's pasteboard only, for two minutes.
struct SecretValue: View {
    let label: String
    let value: String
    @State private var copied = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(value)
                .font(.body.monospaced())
                .textSelection(.enabled)
                .privacySensitive()
                .accessibilityLabel(label)
                .accessibilityIdentifier("secretValue")
            Button(copied ? "Copied" : "Copy", systemImage: copied ? "checkmark" : "doc.on.doc") {
                UIPasteboard.general.setItems(
                    [[UTType.plainText.identifier: value]],
                    options: [.localOnly: true, .expirationDate: Date.now.addingTimeInterval(120)]
                )
                copied = true
            }
            .accessibilityIdentifier("copySecret")
        }
    }
}

/// A server value a sheet presents, by its id (the generated types are not `Identifiable`).
struct SettingsItem<Value>: Identifiable {
    let id: String
    let value: Value
}

extension Client {
    /// PATCH /settings with only `patch`'s keys; the server answers the whole map.
    func patch(_ patch: ServerSettings) async throws -> ServerSettings {
        try await updateSettings(body: .json(patch)).ok.body.json
    }
}
