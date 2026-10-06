import SwiftUI
import VitamuxKit

typealias Connection = Components.Schemas.Connection
typealias Provider = Components.Schemas.Provider

/// The panel's status icons (web/src/lib/components/StatusIcon.svelte): a shape and a colour, and
/// always a word beside them, so a state never depends on colour alone.
enum StatusKind {
    case ok, warn, error, pending, off, info

    var symbol: String {
        switch self {
        case .ok: "checkmark.circle.fill"
        case .warn: "exclamationmark.triangle.fill"
        case .error: "xmark.octagon.fill"
        case .pending: "clock.fill"
        case .off: "minus.circle"
        case .info: "info.circle.fill"
        }
    }

    var color: Color {
        switch self {
        case .ok: .green
        case .warn: .orange
        case .error: .red
        case .pending, .info: .blue
        case .off: .secondary
        }
    }

    /// Run outcomes recorded by internal/jobs/runner.go.
    init(outcome: String?) {
        switch outcome {
        case nil: self = .pending
        case "succeeded": self = .ok
        case "failed": self = .error
        case "lease_expired": self = .warn
        default: self = .info
        }
    }
}

struct StatusIcon: View {
    let kind: StatusKind

    var body: some View {
        Image(systemName: kind.symbol).foregroundStyle(kind.color).imageScale(.small).accessibilityHidden(true)
    }
}

extension Components.Schemas.Health {
    /// The panel's health labels (HealthBadge.svelte).
    var label: String {
        switch self {
        case .ok: "Healthy"
        case .degraded: "Degraded"
        case .failing: "Failing"
        case .needsReauth: "Needs reauthorization"
        case .paused: "Paused"
        case .disabled: "Disabled"
        case .stale: "Stale"
        }
    }

    var kind: StatusKind {
        switch self {
        case .ok: .ok
        case .degraded, .stale: .warn
        case .failing, .needsReauth: .error
        case .paused: .pending
        case .disabled: .off
        }
    }

    var needsAttention: Bool { SyncStatusModel.alerting.contains(self) }
}

/// A connection's health: its icon and label.
struct HealthBadge: View {
    let health: Components.Schemas.Health

    var body: some View {
        Label {
            Text(health.label)
        } icon: {
            StatusIcon(kind: health.kind)
        }
        .accessibilityElement(children: .combine)
    }
}

/// "Unofficial": the API can change without notice.
struct UnofficialBadge: View {
    var body: some View {
        Text("Unofficial")
            .font(.caption2.weight(.semibold))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .foregroundStyle(.orange)
            .overlay(Capsule().strokeBorder(.orange.opacity(0.6)))
            .accessibilityLabel("Unofficial API")
    }
}

/// The provider's colour, as on every chart, in a rounded square with its initial.
struct SourceMonogram: View {
    let provider: String
    var size: CGFloat = 40

    var body: some View {
        Text(providerLabel(provider).prefix(1))
            .font(.system(size: size * 0.45, weight: .bold, design: .rounded))
            .foregroundStyle(SourceStyle.color(provider))
            .frame(width: size, height: size)
            .background(SourceStyle.color(provider).opacity(0.16), in: .rect(cornerRadius: size * 0.28))
            .accessibilityHidden(true)
    }
}

/// Display copy shared by the Sources screens (web/src/lib/connections/connections.ts, setup.ts).
enum SourcesCopy {
    /// How a connection gets its data.
    static func mode(_ mode: Connection.ModePayload) -> String {
        switch mode {
        case .inProcess: "Server sync"
        case .push: "Push uploads"
        case .remote: "Sidecar"
        }
    }

    /// The OAuth callback's `auth_error` codes (internal/api/oauth.go).
    static func authError(_ code: String) -> String {
        switch code {
        case "invalid_state": "The authorization link expired or was already used. Start again."
        case "denied": "Access was not granted at the provider."
        case "account_mismatch": "You signed in to a different account than this connection uses. Nothing changed."
        case "exchange_failed": "The provider did not accept the authorization. Try again later."
        case "unavailable": "The provider or Vitamux could not complete the connection right now. Try again later."
        default: "the provider answered \(code)."
        }
    }

    /// What a known provider brings.
    static func about(_ code: String) -> String? {
        switch code {
        case "withings": "Blood pressure, weight, body composition, activity, intraday heart rate and sleep through the official Withings API."
        case "garmin": "Daily summaries, heart rate, sleep, stress, HRV and activities. The first backfill goes day by day to stay within Garmin’s limits."
        case "whoop": "Heart rate every 6 seconds, cycles, sleep and workouts, paced at one request per second. Heart-rate history goes back 90 days by default."
        default: nil
        }
    }

    /// Where the owner creates their own provider application.
    static func appDashboard(_ code: String) -> URL? {
        code == "withings" ? URL(string: "https://developer.withings.com/dashboard/") : nil
    }

    /// Backfill unit chosen by the connector, in days.
    static func unitDays(_ provider: String) -> Int? {
        provider == "withings" ? 30 : nil
    }

    /// Streams the server starts at most `perDay` units a day.
    static func paced(_ stream: String) -> (perDay: Int, note: String)? {
        guard stream == "garmin.intraday_reload" else { return nil }
        return (20, "Garmin moves heart rate, steps, stress, sleep and similar detail of older days to cold storage. This asks Garmin to restore one day at a time, then fetches it again. Garmin refuses about 30 requests a day, so each day is its own unit and at most 20 start a day. It is opt-in and slow: 300 days take about 15 days.")
    }

    /// "synced 5 minutes ago", "last upload …" for a push source, "not synced yet".
    static func lastSync(_ c: Connection) -> String {
        guard let at = c.lastSuccessAt else { return "Not synced yet" }
        return (c.mode == .push ? "Last upload " : "Synced ") + ago(at)
    }

    static func ago(_ date: Date?) -> String {
        guard let date else { return "never" }
        if abs(date.timeIntervalSinceNow) < 60 { return "just now" }
        return date.formatted(.relative(presentation: .named))
    }

    static func when(_ date: Date?) -> String {
        date.map { $0.formatted(date: .abbreviated, time: .shortened) } ?? "–"
    }

    static func day(_ date: Date) -> String {
        date.formatted(date: .abbreviated, time: .omitted)
    }

    /// "15 min", "2 h", "1 day".
    static func span(_ seconds: Int) -> String {
        if seconds % 86_400 == 0 { return seconds == 86_400 ? "1 day" : "\(seconds / 86_400) days" }
        if seconds % 3_600 == 0 { return "\(seconds / 3_600) h" }
        return "\(seconds / 60) min"
    }

    static func every(_ seconds: Int) -> String { "every " + span(seconds) }

    /// "1 min 5 s" between two instants, or "running".
    static func elapsed(_ from: Date, _ to: Date?) -> String {
        guard let to else { return "running" }
        let s = max(0, Int(to.timeIntervalSince(from).rounded()))
        return s >= 60 ? "\(s / 60) min \(s % 60) s" : "\(s) s"
    }

    static func plural(_ n: Int, _ one: String, _ many: String? = nil) -> String {
        "\(n) \(n == 1 ? one : many ?? one + "s")"
    }

    /// Plain language for a failed sign-in step (setup.ts `signInError`); the server's own words
    /// stay below it.
    static func signInError(_ problem: Problem, name: String, codeStep: Bool, sidecarDown: Bool) -> String {
        if sidecarDown { return "The \(name) sidecar is not running, so Vitamux cannot reach \(name). Turn it on under Connect a source, then start again." }
        switch problem.status {
        case 429:
            return "\(name) is limiting sign-in attempts. Wait \(wait(problem.retryAfter)), then start again."
        case 409:
            return "You signed in to a different \(name) account than this connection uses. Nothing changed."
        case 400, 422:
            if problem.detail(for: "/state") != nil { return "The sign-in took too long or was already used. Start again." }
            return codeStep
                ? "\(name) did not accept the verification code, or it expired. Start again and use the newest code."
                : "\(name) did not accept the email or password. Check them, then start again."
        case 503:
            return "\(name) did not answer. Try again in a few minutes."
        default:
            return ""
        }
    }

    private static func wait(_ duration: Duration?) -> String {
        let seconds = Int(duration?.components.seconds ?? 0)
        guard seconds > 60 else { return "a minute" }
        let minutes = (seconds + 59) / 60
        return minutes < 60 ? "\(minutes) minutes" : "\((minutes + 59) / 60) hours"
    }
}

