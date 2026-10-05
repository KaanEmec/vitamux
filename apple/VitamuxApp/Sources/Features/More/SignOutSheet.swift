import SwiftUI
import VitamuxKit

/// Sign-out: ends this app session on the server. Apple Health sync has its own device token
/// and keeps running unless "also unpair this iPhone" is on.
struct SignOutSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    @State private var isPaired = false
    @State private var unpair = false
    @State private var isBusy = false
    @State private var problem: Problem?

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Text("You can sign in again with your username and password.")
                }
                if isPaired {
                    Section {
                        Toggle("Also unpair this iPhone", isOn: $unpair)
                            .accessibilityIdentifier("unpairToggle")
                    } footer: {
                        Text(unpair
                             ? "Apple Health stops uploading from this iPhone and its device token is revoked. Data already on the server stays."
                             : "Apple Health keeps uploading from this iPhone while you are signed out.")
                    }
                }
                if let problem {
                    Section {
                        ProblemView(problem: problem)
                    }
                }
                Section {
                    Button(role: .destructive) {
                        Task { await signOut() }
                    } label: {
                        if isBusy { ProgressView() } else { Text("Sign out") }
                    }
                    .disabled(isBusy)
                    .accessibilityIdentifier("confirmSignOut")
                }
            }
            .navigationTitle("Sign out")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            }
        }
        .presentationDetents([.medium, .large])
        .onAppear { isPaired = state.device.credentials != nil }
    }

    private func signOut() async {
        isBusy = true
        defer { isBusy = false }
        do {
            try await state.signOut(unpairing: unpair)
        } catch {
            problem = Problem(error)
        }
    }
}
