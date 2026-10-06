import Foundation

/// The local notifications' memory (J22.21, docs/architecture/ios-app.md#offline-cache-widgets-and-notifications):
/// the state last notified per item, so a condition notifies once when it starts or changes, the
/// delivered notification is replaced rather than repeated, and it is withdrawn when the condition
/// clears. Pure value logic; the app stores it as JSON and talks to `UNUserNotificationCenter`.
public struct NotificationLedger: Codable, Equatable, Sendable {
    /// One condition worth a notification: a connection, a job, a document, the backup or this
    /// iPhone's upload. Text names the thing and the next step, never a health value.
    public struct Item: Codable, Equatable, Sendable, Identifiable {
        /// Stable per thing (`connection:<id>`), the notification's identifier.
        public var id: String
        /// The app's category key; categories are toggled as a whole.
        public var category: String
        /// What makes it news: a new state (`needs_reauth` → `failing`) notifies again.
        public var state: String
        public var title: String
        public var body: String
        /// The `vitamux://` link a tap opens.
        public var link: String

        public init(id: String, category: String, state: String, title: String, body: String, link: String) {
            self.id = id
            self.category = category
            self.state = state
            self.title = title
            self.body = body
            self.link = link
        }
    }

    /// What to do with the notification centre after a check.
    public struct Changes: Equatable, Sendable {
        /// Post, replacing any delivered notification with the same identifier.
        public var post: [Item] = []
        /// Withdraw: the condition cleared (or its category was turned off).
        public var remove: [String] = []

        public init(post: [Item] = [], remove: [String] = []) {
            self.post = post
            self.remove = remove
        }
    }

    struct Seen: Codable, Equatable, Sendable {
        var category: String
        var state: String
    }

    private var seen: [String: Seen] = [:]

    public init() {}

    /// The item identifiers notified and not cleared yet.
    public var notified: Set<String> { Set(seen.keys) }

    /// Compares a check's `current` items with what was notified. Only `checked` categories are
    /// compared: a category whose source could not be read keeps its items as they were, so an
    /// unreachable server never reads as "fixed". A category turned off is passed as checked with
    /// no items, which withdraws its notifications; turning it on again notifies what is current.
    public mutating func update(current: [Item], checked: Set<String>) -> Changes {
        var changes = Changes()
        var present: Set<String> = []
        for item in current where checked.contains(item.category) && present.insert(item.id).inserted {
            guard seen[item.id] != Seen(category: item.category, state: item.state) else { continue }
            seen[item.id] = Seen(category: item.category, state: item.state)
            changes.post.append(item)
        }
        for (id, entry) in seen where checked.contains(entry.category) && !present.contains(id) {
            changes.remove.append(id)
        }
        for id in changes.remove { seen[id] = nil }
        changes.remove.sort()
        return changes
    }
}
