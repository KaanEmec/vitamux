import SwiftUI
import VitamuxKit

/// Not paired yet: one tap pairs with the signed-in server; a code from the panel (typed or
/// scanned) pairs a phone with another server. Bridge's Pair screen.
struct PairingSections: View {
    @Environment(AppState.self) private var state
    let device: ThisDevice
    @State private var address = ""
    @State private var code = ""
    @State private var isScanning = false

    var body: some View {
        Section {
            Text("Upload the Apple Health data you choose from this iPhone to \(state.profile?.baseURL.host() ?? "your server"). Syncing keeps running in the background, also while you are signed out.")
            if let client = state.client, let profile = state.profile {
                Button {
                    Task { await device.pairHere(using: client, on: profile) }
                } label: {
                    HStack {
                        Label("Sync Apple Health from this iPhone", systemImage: "heart.text.square")
                        if device.isPairing {
                            Spacer()
                            ProgressView()
                        }
                    }
                }
                .disabled(device.isPairing)
                .accessibilityIdentifier("pairHereButton")
            }
        }
        if let problem = device.problem {
            Section { ProblemView(problem: problem) }
        }
        Section {
            Button("Scan pairing QR code", systemImage: "qrcode.viewfinder") { isScanning = true }
                .accessibilityIdentifier("scanButton")
            TextField("https://vitamux.example.com", text: $address)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .keyboardType(.URL)
                .textContentType(.URL)
                .accessibilityIdentifier("serverURLField")
            TextField("Pairing code", text: $code)
                .textInputAutocapitalization(.characters)
                .autocorrectionDisabled()
                .accessibilityIdentifier("codeField")
            Button("Pair with code") {
                Task { await device.pair(address: address, code: code) }
            }
            .disabled(device.isPairing || address.isEmpty || code.isEmpty)
            .accessibilityIdentifier("pairButton")
        } header: {
            Text("Pair with a code")
        } footer: {
            Text("For another server: in its web panel open Settings › Devices, create a pairing code, and scan its QR code or type the address and code. Codes work once and expire after 10 minutes.")
        }
        .sheet(isPresented: $isScanning) {
            ScannerSheet { payload in
                isScanning = false
                if let parsed = ThisDevice.parsePairingPayload(payload) {
                    address = parsed.url
                    code = parsed.code
                    Task { await device.pair(address: parsed.url, code: parsed.code) }
                } else {
                    device.problem = Problem(title: "Not a pairing code", detail: "Scan the QR code shown in Settings › Devices on your server.")
                }
            }
        }
    }
}
