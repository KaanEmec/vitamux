import SwiftUI
import UIKit
import VitamuxKit

/// Settings › This app: whether iOS lets Vitamux notify, and the way to allow it. Asked here (or
/// when a category is turned on), never at launch.
struct NotificationPermissionRow: View {
    @Environment(Notifier.self) private var notifier

    var body: some View {
        switch notifier.permission {
        case .allowed:
            LabeledContent("Permission", value: "Allowed")
                .accessibilityIdentifier("notificationPermission")
        case .notAsked:
            Button("Allow notifications") { Task { await notifier.requestPermission() } }
                .accessibilityIdentifier("allowNotifications")
        case .denied:
            LabeledContent("Permission", value: "Off in the Settings app")
                .accessibilityIdentifier("notificationPermission")
            Button("Open the Settings app") {
                if let url = URL(string: UIApplication.openNotificationSettingsURLString) { UIApplication.shared.open(url) }
            }
        }
    }
}

#if DEBUG
/// Under `-uitest`: the notifications this iPhone would show (the simulator's notification centre
/// cannot be inspected), a check on demand, and a tap that follows a notification's link as the
/// system's tap does. Real delivery and the system tap need a device (J22.23).
struct NotificationTestSection: View {
    @Environment(Notifier.self) private var notifier

    var body: some View {
        Section("Delivered (UI test)") {
            Button("Check now") { Task { await notifier.check() } }
                .accessibilityIdentifier("checkNotifications")
            Text("\(notifier.posted) posted")
                .accessibilityIdentifier("notificationsPosted")
            ForEach(notifier.delivered) { item in
                Button {
                    notifier.open(link: item.link)
                } label: {
                    VStack(alignment: .leading) {
                        Text(item.title)
                        Text(item.body).font(.footnote).foregroundStyle(.secondary)
                    }
                }
                .accessibilityIdentifier("notice-\(item.category)")
            }
        }
    }
}
#endif
