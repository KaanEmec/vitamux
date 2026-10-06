import SwiftUI
import VitamuxKit

/// A confirmation or error line under an action, as the panel's Notice and ProblemAlert.
struct NoticeRow: View {
    let text: String
    var kind: StatusKind = .ok
    var identifier = "notice"

    var body: some View {
        Label {
            Text(text).font(.subheadline)
        } icon: {
            StatusIcon(kind: kind)
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier(identifier)
    }
}

/// A failure inline: the plain-language lead, then the server's title and detail.
struct ProblemRow: View {
    let problem: Problem
    var lead = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Label {
                Text(lead.isEmpty ? problem.title : lead).font(.subheadline.weight(.semibold))
            } icon: {
                StatusIcon(kind: .error)
            }
            if let detail = problem.detail {
                Text(lead.isEmpty ? detail : "\(problem.title): \(detail)").font(.footnote).foregroundStyle(.secondary)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("problem")
    }
}
