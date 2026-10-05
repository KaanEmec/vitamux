import SwiftUI

/// Tab root (`vitamux://dashboard?date=`); built in J22.7.
struct DashboardView: View {
    let date: String?

    var body: some View {
        PlaceholderView(title: "Dashboard", detail: [date], job: "J22.7")
    }
}
