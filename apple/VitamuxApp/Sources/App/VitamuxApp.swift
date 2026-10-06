import SwiftUI
import UIKit
import UserNotifications
import VitamuxKit

@main
struct VitamuxApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @Environment(\.scenePhase) private var phase

    var body: some Scene {
        let device = delegate.state.device
        let notifier = delegate.notifier
        WindowGroup {
            RootView()
                .environment(delegate.state)
                .environment(notifier)
        }
        // Apple Health sync runs signed in or not: it has its own device token (ThisDevice). The
        // notification check follows it, so it sees the upload state the sync left.
        .onChange(of: phase) { _, new in
            if new == .background {
                device.scheduleRefresh()
                notifier.scheduleRefresh()
            }
            if new == .active {
                Task {
                    await device.start()
                    await notifier.check()
                }
            }
        }
        .backgroundTask(.appRefresh(ThisDevice.refreshTaskID)) {
            await device.backgroundRefresh()
            await notifier.backgroundCheck()
        }
    }
}

/// Owns the one `AppState`, so it exists before `didFinishLaunching`: HealthKit relaunches the
/// app in the background for observer queries, which must be registered at launch
/// (docs/architecture/apple-health.md#sync-algorithm).
final class AppDelegate: NSObject, UIApplicationDelegate {
    let state: AppState
    /// Local notifications (J22.21); the notification centre's delegate, so a tap that launches
    /// the app still opens its link.
    let notifier: Notifier

    override init() {
        #if DEBUG
        if FakeServer.isUITestRun {
            let arguments = ProcessInfo.processInfo.arguments
            state = AppState.uiTest(arguments: arguments)
            notifier = Notifier(state: state)
            // A lab document waiting for review, for the notifications' UI test.
            if arguments.contains("-uitest-lab-review") { FakeServer.uiTest.addDocumentForReview() }
            super.init()
            return
        }
        #endif
        state = AppState.live()
        notifier = Notifier(state: state)
        super.init()
    }

    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?) -> Bool {
        // Registers the observer queries with background delivery for the enabled types, applies
        // server-requested resets and syncs; each observer callback completes even on failure.
        let device = state.device
        Task { await device.start() }
        if !state.isUITest { UNUserNotificationCenter.current().delegate = notifier }
        return true
    }
}
