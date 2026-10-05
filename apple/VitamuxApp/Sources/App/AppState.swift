import Foundation
import Observation

/// The one shared object, in the environment. It holds the router today; J22.4 and J22.5 add the
/// server profile, the session and the generated client. Screens keep their own state.
@Observable
final class AppState {
    var tab: AppTab = .dashboard
    var paths: [AppTab: [Route]] = [:]

    /// Shows `route` on its tab's stack.
    func open(_ route: Route) {
        tab = route.tab
        paths[route.tab] = [route]
    }

    /// A `vitamux://` link: its route, or the tab root for a path without one.
    func open(_ url: URL) {
        if let route = Route(url: url) {
            open(route)
        } else if let tab = AppTab(url: url) {
            self.tab = tab
            paths[tab] = []
        }
    }
}
