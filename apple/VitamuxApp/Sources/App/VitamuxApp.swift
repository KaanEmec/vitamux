import SwiftUI
import UIKit
import VitamuxKit

@main
struct VitamuxApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @Environment(\.scenePhase) private var phase

    var body: some Scene {
        let device = delegate.state.device
        WindowGroup {
            RootView()
                .environment(delegate.state)
        }
        // Apple Health sync runs signed in or not: it has its own device token (ThisDevice).
        .onChange(of: phase) { _, new in
            if new == .background { device.scheduleRefresh() }
            if new == .active { Task { await device.start() } }
        }
        .backgroundTask(.appRefresh(ThisDevice.refreshTaskID)) {
            await device.backgroundRefresh()
        }
    }
}

/// Owns the one `AppState`, so it exists before `didFinishLaunching`: HealthKit relaunches the
/// app in the background for observer queries, which must be registered at launch
/// (docs/architecture/apple-health.md#sync-algorithm).
final class AppDelegate: NSObject, UIApplicationDelegate {
    let state: AppState

    override init() {
        #if DEBUG
        if FakeServer.isUITestRun {
            state = AppState.uiTest(arguments: ProcessInfo.processInfo.arguments)
            super.init()
            return
        }
        #endif
        state = AppState.live()
        super.init()
    }

    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?) -> Bool {
        // Registers the observer queries with background delivery for the enabled types, applies
        // server-requested resets and syncs; each observer callback completes even on failure.
        let device = state.device
        Task { await device.start() }
        return true
    }
}
