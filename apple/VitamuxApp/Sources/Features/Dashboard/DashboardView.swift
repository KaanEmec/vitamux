import SwiftUI

/// Tab root; built in J22.7.
struct DashboardView: View {
    var body: some View {
        ContentUnavailableView("Dashboard", systemImage: "hammer", description: Text("Not built yet."))
            .navigationTitle("Dashboard")
    }
}
