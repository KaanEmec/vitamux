import Foundation
import VitamuxKit

/// The text and link of each local notification (J22.21). Each names the connection, document or
/// job and the next step; none carries a health value or judges one. The conditions are the
/// dashboard's alerts (`DashboardAlert`) plus a document waiting for review and this iPhone's
/// Apple Health upload.
enum NotificationContent {
    typealias Item = NotificationLedger.Item
    typealias Category = AppPreferences.Notification

    /// A connection the panel flags (`SyncStatusModel.alerting`), keyed by its health.
    static func connection(_ c: Components.Schemas.Connection) -> Item? {
        guard SyncStatusModel.alerting.contains(c.health) else { return nil }
        let name = providerLabel(c.provider)
        let base = "vitamux://connections/\(c.id)"
        let (title, body, link): (String, String, String) = switch c.health {
        case .needsReauth:
            ("\(name) needs you to sign in again", "Vitamux can't sync \(name) until it is reauthorized. Open the connection to reauthorize it.", base)
        case .failing:
            ("\(name) syncs are failing", "The last \(c.consecutiveFailures) runs failed. Open its history to see why.", base + "?tab=history")
        case .stale:
            ("\(name) has not synced recently", c.lastSuccessAt.map { "No successful sync since \(Format.date($0)). Open the connection to check it." }
                ?? "It has not synced successfully yet. Open the connection to check it.", base)
        default:
            ("\(name) is degraded", "Some of its streams have problems. Open its streams to see which.", base + "?tab=streams")
        }
        return Item(id: "connection:\(c.id)", category: Category.connectionAttention.rawValue, state: c.health.rawValue,
                    title: title, body: body, link: link)
    }

    /// A job that failed permanently; `connections` names its source.
    static func job(_ job: Components.Schemas.Job, connections: [Components.Schemas.Connection]) -> Item {
        let attempts = "after \(job.attempts) attempts"
        let (body, link): (String, String) = if let id = job.connectionId {
            (connections.first { $0.id == id }.map { "The \(job.kind) job for \(providerLabel($0.provider)) stopped \(attempts). Open its history to see why." }
                ?? "The \(job.kind) job stopped \(attempts). Open the connection's history to see why.",
             "vitamux://connections/\(id)?tab=history")
        } else {
            ("The \(job.kind) job stopped \(attempts). System status lists the failing jobs.", "vitamux://settings/system")
        }
        return Item(id: "job:\(job.id)", category: Category.failedJob.rawValue, state: "dead",
                    title: "A \(job.kind) job failed for good", body: body, link: link)
    }

    /// A lab document whose extraction waits for the owner's review.
    static func document(_ document: LabDocument) -> Item? {
        guard document.status == .needsReview else { return nil }
        return Item(id: "document:\(document.id)", category: Category.labReady.rawValue, state: "needs_review",
                    title: "A lab document is ready for review", body: "\(document.name) is ready. Open it to check the rows before confirming them.",
                    link: "vitamux://lab/documents/\(document.id)")
    }

    /// The last backup, when it is older than the panel's threshold (`DashboardAlert.backupMaxAge`).
    /// A newer backup that is still old notifies again; a fresh one withdraws it.
    static func backup(_ lastBackup: Date?, now: Date = .now) -> Item? {
        guard let lastBackup, now.timeIntervalSince(lastBackup) > DashboardAlert.backupMaxAge else { return nil }
        return Item(id: "backup", category: Category.staleBackup.rawValue, state: DashboardAlert.instant(lastBackup),
                    title: "The last backup is old",
                    body: "The last backup finished on \(Format.date(lastBackup)). Run vitamux backup on the server; Backups shows when it last ran.",
                    link: "vitamux://settings/backups")
    }

    /// Nothing has uploaded for this long while types are enabled.
    static let uploadMaxSilence: TimeInterval = 2 * 86_400

    /// This iPhone's Apple Health upload: revoked, types failing, or silent for two days.
    static func upload(_ device: ThisDevice, now: Date = .now) -> Item? {
        guard device.isPaired, !device.enabledTypes.isEmpty || device.isRevoked else { return nil }
        let found: (state: String, title: String, body: String)? = if device.isRevoked {
            ("revoked", "Apple Health upload stopped", "The server no longer accepts this iPhone. Open Apple Health in Vitamux to pair it again.")
        } else if !device.waiting.isEmpty {
            ("failing", "Apple Health upload stalled", "Some types could not upload. Open Apple Health in Vitamux to see which and sync again.")
        } else if let last = device.lastUpload, now.timeIntervalSince(last) > uploadMaxSilence {
            ("silent", "Apple Health upload stalled", "Nothing has uploaded since \(Format.date(last)). Open Apple Health in Vitamux to sync now.")
        } else {
            nil
        }
        guard let found else { return nil }
        return Item(id: "apple-health", category: Category.uploadStalled.rawValue, state: found.state, title: found.title, body: found.body,
                    link: "vitamux://apple-health")
    }
}
