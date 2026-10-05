import SwiftUI

/// The worked example's screen (`Route.systemStatus`, `vitamux://settings/system`). It owns its
/// model as `@State`, loads in `.task`, and switches on the model's `Loadable`.
struct SystemStatusView: View {
    @State private var model = SystemStatusModel()

    var body: some View {
        List {
            switch model.versions {
            case .loading: ProgressView()
            case .loaded(let versions): VersionsSection(versions: versions)
            case .failed(let problem): ProblemView(problem: problem)
            }
        }
        .navigationTitle("System status")
        .task { await model.load() }
    }
}

private struct VersionsSection: View {
    let versions: SystemStatusModel.Versions

    var body: some View {
        Section("Versions") {
            LabeledContent("App", value: "\(versions.app) (\(versions.build))")
                .accessibilityIdentifier("appVersion")
        }
    }
}
