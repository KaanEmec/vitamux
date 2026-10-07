import BackgroundTasks
import Foundation
import Observation
import UserNotifications
import VitamuxKit

/// Local notifications (J22.21; docs/architecture/ios-app.md#offline-cache-widgets-and-notifications).
/// A check runs when the app comes to the foreground and from the `BGAppRefreshTask` after the
/// Apple Health work: it reads `GET /connections`, `/jobs?status=dead`, `/documents` and
/// `/system/status` (when signed in) and this iPhone's upload state, and `NotificationLedger`
/// decides what to post, update or withdraw. Nothing is pushed by the server. A tap opens the
/// notification's `vitamux://` link. The permission is asked from Settings › This app, never at launch.
@Observable
final class Notifier: NSObject, UNUserNotificationCenterDelegate {
    enum Permission {
        case notAsked, allowed, denied
    }

    private(set) var permission = Permission.notAsked
    /// How often iOS ran the background refresh, for the device campaign.
    private(set) var backgroundRuns: Int
    private(set) var lastBackgroundRun: Date?
    /// Under `-uitest`: what the notification centre would show and how many posts were made, for
    /// Settings › This app's test section (the simulator's centre cannot be inspected). Empty otherwise.
    private(set) var delivered: [NotificationLedger.Item] = []
    private(set) var posted = 0

    @ObservationIgnored private let state: AppState
    @ObservationIgnored private var isChecking = false
    @ObservationIgnored private var defaults: UserDefaults { AppPreferences.store }

    private enum Key {
        static let ledger = "notifier.ledger", runs = "notifier.backgroundRuns", lastRun = "notifier.lastBackgroundRun"
    }

    init(state: AppState) {
        self.state = state
        let defaults = AppPreferences.store
        backgroundRuns = defaults.integer(forKey: Key.runs)
        lastBackgroundRun = defaults.object(forKey: Key.lastRun) as? Date
        super.init()
    }

    // MARK: Checking

    /// The foreground check, after the Apple Health sync so its state is current. One at a time.
    func check(now: Date = .now) async {
        guard !isChecking else { return }
        isChecking = true
        defer { isChecking = false }
        await refreshPermission()
        // Without the permission nothing is recorded, so allowing later notifies what is current.
        guard permission == .allowed else { return }
        let (items, checked) = await gather(now: now)
        var ledger = defaults.data(forKey: Key.ledger).flatMap { try? JSONDecoder().decode(NotificationLedger.self, from: $0) }
            ?? NotificationLedger()
        let changes = ledger.update(current: items, checked: checked)
        defaults.set(try? JSONEncoder().encode(ledger), forKey: Key.ledger)
        deliver(changes)
    }

    /// The `BGAppRefreshTask` check, counted for the device campaign.
    func backgroundCheck() async {
        backgroundRuns += 1
        lastBackgroundRun = .now
        defaults.set(backgroundRuns, forKey: Key.runs)
        defaults.set(lastBackgroundRun, forKey: Key.lastRun)
        await check()
        scheduleRefresh()
    }

    /// The device schedules the shared refresh task while paired; a signed-in iPhone that is not
    /// paired schedules it here, so server conditions are still checked.
    func scheduleRefresh() {
        guard !state.isUITest, state.isSignedIn, !state.device.isPaired, AppPreferences.notificationsOn > 0 else { return }
        let request = BGAppRefreshTaskRequest(identifier: ThisDevice.refreshTaskID)
        request.earliestBeginDate = Date(timeIntervalSinceNow: 3600)
        try? BGTaskScheduler.shared.submit(request)
    }

    /// The current items of the categories turned on, and the categories this check can speak for:
    /// those whose source answered, plus those turned off (which withdraws their notifications).
    private func gather(now: Date) async -> (items: [NotificationLedger.Item], checked: Set<String>) {
        typealias Category = AppPreferences.Notification
        var items: [NotificationLedger.Item] = []
        var answered: Set<Category> = [.uploadStalled]
        if let item = NotificationContent.upload(state.device, now: now) { items.append(item) }
        if let client = state.client {
            async let connections = try? client.listConnections().ok.body.json.connections
            async let jobs = try? client.listJobs(query: .init(status: "dead", limit: 20)).ok.body.json.value2.jobs
            async let documents = try? client.listDocuments(query: .init(limit: 500)).ok.body.json.value2.documents
            async let status = try? client.getSystemStatus().ok.body.json
            let connectionList = await connections
            if let connectionList {
                answered.insert(.connectionAttention)
                items += connectionList.compactMap(NotificationContent.connection)
            }
            if let jobs = await jobs {
                answered.insert(.failedJob)
                // The dashboard's window: a permanent failure stays news for a week.
                let weekAgo = now.addingTimeInterval(-7 * 86_400)
                items += jobs.filter { ($0.finishedAt ?? $0.createdAt) >= weekAgo }
                    .map { NotificationContent.job($0, connections: connectionList ?? []) }
            }
            if let documents = await documents {
                answered.insert(.labReady)
                items += documents.compactMap(NotificationContent.document)
            }
            if let status = await status {
                answered.insert(.staleBackup)
                if let item = NotificationContent.backup(status.lastBackupAt, now: now) { items.append(item) }
            }
        }
        let on = Set(Category.allCases.filter(\.isOn).map(\.rawValue))
        let off = Set(Category.allCases.filter { !$0.isOn }.map(\.rawValue))
        return (items.filter { on.contains($0.category) }, Set(answered.map(\.rawValue)).union(off))
    }

    // MARK: Delivery

    private func deliver(_ changes: NotificationLedger.Changes) {
        #if DEBUG
        if state.isUITest {
            delivered.removeAll { changes.remove.contains($0.id) || changes.post.map(\.id).contains($0.id) }
            delivered += changes.post
            posted += changes.post.count
            return
        }
        #endif
        let center = UNUserNotificationCenter.current()
        if !changes.remove.isEmpty {
            center.removeDeliveredNotifications(withIdentifiers: changes.remove)
            center.removePendingNotificationRequests(withIdentifiers: changes.remove)
        }
        for item in changes.post {
            let content = UNMutableNotificationContent()
            content.title = item.title
            content.body = item.body
            content.threadIdentifier = item.category
            content.userInfo = ["link": item.link]
            content.sound = .default
            // No trigger: shown now; the same identifier replaces a delivered one.
            center.add(UNNotificationRequest(identifier: item.id, content: content, trigger: nil))
        }
    }

    /// Opens a notification's link; while signed out or locked it waits (`AppState.open`).
    func open(link: String?) {
        guard let link, let url = URL(string: link) else { return }
        state.open(url)
    }

    // MARK: Permission

    func refreshPermission() async {
        #if DEBUG
        if state.isUITest { return }
        #endif
        let status = await UNUserNotificationCenter.current().notificationSettings().authorizationStatus
        permission = switch status {
        case .notDetermined: .notAsked
        case .denied: .denied
        default: .allowed
        }
    }

    /// Asks once, in context (the permission row, or turning a category on), then checks.
    func requestPermission() async {
        #if DEBUG
        if state.isUITest {
            permission = .allowed
            await check()
            return
        }
        #endif
        guard permission == .notAsked else { return }
        _ = try? await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound])
        await refreshPermission()
        if permission == .allowed { await check() }
    }

    // MARK: UNUserNotificationCenterDelegate

    /// Shown in the foreground too: the check that posts it runs as the app opens.
    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter, willPresent notification: UNNotification
    ) async -> UNNotificationPresentationOptions {
        [.banner, .list]
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        let link = response.notification.request.content.userInfo["link"] as? String
        await open(link: link)
    }
}
