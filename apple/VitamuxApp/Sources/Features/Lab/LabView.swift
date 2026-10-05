import SwiftUI

/// Tab root; built in J22.12.
struct LabView: View {
    var body: some View {
        ContentUnavailableView("Lab", systemImage: "hammer", description: Text("Not built yet."))
            .navigationTitle("Lab")
    }
}
