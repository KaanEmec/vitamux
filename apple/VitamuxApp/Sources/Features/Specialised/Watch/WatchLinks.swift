import SwiftUI
import VitamuxKit

// Where the Explore and Specialised screens open the Apple Watch views.

/// Under a metric's chart: activity-summary codes point at the rings, HRV codes at the beats.
struct WatchMetricNote: View {
    let code: String

    var body: some View {
        if ActivityRingsModel.codes.contains(code) {
            NavigationLink("Each day against Apple’s goal is in the activity rings view.", value: Route.exploreView(.activityRings))
                .font(.footnote)
                .accessibilityIdentifier("ringsLink")
        } else if code.hasPrefix("hrv_") || code == "rr_interval" {
            NavigationLink("The beat-to-beat intervals behind HRV readings are in the beat-to-beat view.", value: Route.beats())
                .font(.footnote)
                .accessibilityIdentifier("beatsLink")
        }
    }
}

/// In an HRV day's point sheet: that day's beat-to-beat series.
struct BeatsPointLink: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let code: String
    let date: LocalDate

    var body: some View {
        if code.hasPrefix("hrv_") {
            Section {
                Button {
                    dismiss()
                    state.paths[state.tab, default: []].append(.beats(date: date.description))
                } label: {
                    Label("Beat-to-beat intervals on this day", systemImage: "heart.text.square")
                }
                .accessibilityIdentifier("openBeats")
            }
        }
    }
}
