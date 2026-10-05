import SwiftUI
import VitamuxKit

/// How every screen shows a failed load.
struct ProblemView: View {
    let problem: Problem

    var body: some View {
        ContentUnavailableView {
            Label(problem.title, systemImage: "exclamationmark.triangle")
        } description: {
            if let detail = problem.detail { Text(detail) }
        }
    }
}
