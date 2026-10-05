import SwiftUI
import VitamuxKit

@main
struct VitamuxApp: App {
    @State private var state: AppState

    init() {
        #if DEBUG
        if FakeServer.isUITestRun {
            _state = State(initialValue: AppState.uiTest(arguments: ProcessInfo.processInfo.arguments))
            return
        }
        #endif
        _state = State(initialValue: AppState.live())
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(state)
        }
    }
}
