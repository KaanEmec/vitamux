import SwiftUI

/// What the app reads from Apple Health and where it goes (Bridge's Privacy screen).
struct AppleHealthPrivacyView: View {
    var body: some View {
        List {
            Section("What is read") {
                Text("Only the Apple Health types in the data groups you turn on, and only for reading. Vitamux never writes to Apple Health. You can change access at any time in Settings › Health › Data Access & Devices.")
            }
            Section("Where it goes") {
                Text("Samples go over HTTPS to the Vitamux server this iPhone is paired with, which you run yourself. Each sample keeps its source app, device and timestamps so your server can choose between sources.")
            }
            Section("Nothing else") {
                Text("No analytics, no ads, no third-party services, no other servers. The device token is stored in this iPhone's Keychain, apart from your sign-in. The camera is used only to scan pairing QR codes.")
            }
        }
        .navigationTitle("Privacy")
    }
}
