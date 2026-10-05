import SwiftUI

/// Tab root (`vitamux://connections`, also the OAuth return with `?connected=` or
/// `?auth_error=`); built in J22.11.
struct SourcesView: View {
    let back: Route.ConnectionsReturn

    var body: some View {
        PlaceholderView(
            title: "Sources",
            detail: [back.connected.map { "connected \($0)" }, back.authError.map { "auth_error \($0)" }, back.provider, back.removed],
            job: "J22.11"
        )
    }
}
