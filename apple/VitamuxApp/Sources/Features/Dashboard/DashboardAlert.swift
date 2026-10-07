import Foundation
import VitamuxKit

/// One alert on the dashboard: a connection that needs attention, a job that failed permanently
/// this week, or a stale backup (the twin of web/src/lib/dashboard/Alerts.svelte). Its key is the
/// panel's, so an alert dismissed in the panel stays dismissed here.
struct DashboardAlert: Identifiable, Equatable {
    enum Level { case warning, error }

    var key: String
    var level: Level
    var text: String
    var action: String?
    var route: Route?

    var id: String { key }

    /// Backups older than this are flagged.
    static let backupMaxAge: TimeInterval = 8 * 86_400

    static func alerts(
        connections: [Components.Schemas.Connection], jobs: [Components.Schemas.Job], lastBackup: Date?, now: Date = .now
    ) -> [DashboardAlert] {
        var out = connections.filter { SyncStatusModel.alerting.contains($0.health) }.map(alert)
        for job in jobs.prefix(5) {
            let route = job.connectionId.map { Route.connection(id: $0, tab: "history") }
            out.append(DashboardAlert(
                key: "job:\(job.id)", level: .error,
                text: "Job \(job.kind) failed permanently after \(job.attempts) attempts, \(Format.ago(job.finishedAt ?? job.createdAt)).",
                action: route == nil ? nil : "See history", route: route
            ))
        }
        if jobs.count > 5 {
            out.append(DashboardAlert(key: "jobs-more:\(jobs[5].id)", level: .error, text: "\(jobs.count - 5) more jobs failed permanently this week."))
        }
        if let lastBackup, now.timeIntervalSince(lastBackup) > backupMaxAge {
            out.append(DashboardAlert(
                key: "backup:\(instant(lastBackup))", level: .warning, text: "The last backup is from \(Format.ago(lastBackup)).",
                action: "Backups", route: .settings(.backups)
            ))
        }
        return out
    }

    private static func alert(_ c: Components.Schemas.Connection) -> DashboardAlert {
        let name = providerLabel(c.provider)
        // A problem that ends and returns follows a success, so the last success dates it.
        let since = c.lastSuccessAt.map(instant) ?? "never"
        let key = { (kind: String, from: String) in "\(kind):\(c.id):\(from)" }
        switch c.health {
        case .needsReauth:
            return DashboardAlert(key: key("reauth", since), level: .error, text: "\(name) needs reauthorization.",
                                  action: "Reauthorize", route: .connection(id: c.id))
        case .failing:
            let error = c.lastErrorClass.map { ", \($0)" } ?? ""
            return DashboardAlert(key: key("failing", since), level: .error,
                                  text: "\(name) is failing (\(c.consecutiveFailures) failed runs\(error)).",
                                  action: "See history", route: .connection(id: c.id, tab: "history"))
        case .stale:
            return DashboardAlert(key: key("stale", since), level: .warning,
                                  text: "\(name) has not synced successfully since \(c.lastSuccessAt.map(Format.ago) ?? "never").",
                                  action: "Open", route: .connection(id: c.id))
        default:
            return DashboardAlert(key: key("degraded", c.healthReason ?? ""), level: .warning,
                                  text: "\(name) is degraded" + (c.healthReason.map { ": \($0)" } ?? "."),
                                  action: "See streams", route: .connection(id: c.id, tab: "streams"))
        }
    }

    /// The instant as the server writes it (RFC 3339, fraction only when there is one), for keys.
    static func instant(_ date: Date) -> String {
        let whole = date.formatted(.iso8601)
        let fraction = date.timeIntervalSince1970 - date.timeIntervalSince1970.rounded(.down)
        guard fraction >= 0.000_001 else { return whole }
        var digits = String(format: "%.6f", fraction).dropFirst(2)
        while digits.last == "0" { digits.removeLast() }
        return String(whole.dropLast()) + "." + String(digits) + "Z"
    }
}
