import SwiftUI

/// Tab root; built in J22.11.
struct SourcesView: View {
    var body: some View {
        ContentUnavailableView("Sources", systemImage: "hammer", description: Text("Not built yet."))
            .navigationTitle("Sources")
    }
}
