import SwiftUI

/// A linked screen that a later job builds: its title and what the link carried.
struct PlaceholderView: View {
    let title: String
    let detail: [String?]
    let job: String

    var body: some View {
        ContentUnavailableView {
            Label(title, systemImage: "hammer")
        } description: {
            VStack {
                Text("Not built yet (\(job)).")
                if !shown.isEmpty {
                    Text(shown).font(.footnote.monospaced()).accessibilityIdentifier("placeholderDetail")
                }
            }
        }
        .navigationTitle(title)
    }

    private var shown: String {
        detail.compactMap(\.self).joined(separator: " · ")
    }
}
