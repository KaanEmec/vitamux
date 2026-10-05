import SwiftUI

/// Tab root for Rules, Settings and Apple Health (J22.10, J22.13, J22.14).
struct MoreView: View {
    var body: some View {
        List {
            Section("Settings") {
                NavigationLink("System status", value: Route.systemStatus)
            }
        }
        .navigationTitle("More")
    }
}
