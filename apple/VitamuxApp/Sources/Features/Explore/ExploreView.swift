import SwiftUI

/// Tab root; built in J22.8.
struct ExploreView: View {
    var body: some View {
        ContentUnavailableView("Explore", systemImage: "hammer", description: Text("Not built yet."))
            .navigationTitle("Explore")
    }
}
