import SwiftUI

@main
struct HealthBridgeApp: App {
    @State private var model: AppModel
    @Environment(\.scenePhase) private var phase

    init() {
        let args = ProcessInfo.processInfo.arguments
        let services = args.contains("-uitest") ? Services.fake(paired: args.contains("-uitest-paired")) : Services.live()
        _model = State(initialValue: AppModel(services: services))
    }

    var body: some Scene {
        WindowGroup {
            RootView(model: model)
                .task { await model.start() }
        }
        .onChange(of: phase) { _, new in
            if new == .background { model.scheduleRefresh() }
            if new == .active { Task { await model.start() } }
        }
        .backgroundTask(.appRefresh(AppModel.refreshTaskID)) {
            await model.backgroundRefresh()
        }
    }
}
