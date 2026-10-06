import SwiftUI
import VitamuxKit

// Status and source cues shared by every screen (docs/architecture/ios-design.md#status-and-source-cues):
// each pairs a shape with a word, and a source keeps its colour everywhere.

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
        case .ok: .feedbackOK
        case .warn: .feedbackWarn
        case .error: .feedbackError
        case .pending, .info: .feedbackInfo
        case .off: .inkFaint
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

/// "Unofficial": the API can change without notice. A neutral pill, as the artboards.
struct UnofficialBadge: View {
    var body: some View {
        Text("Unofficial")
            .font(.footnote.weight(.semibold))
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .foregroundStyle(Color.inkMuted)
            .background(Color.raised, in: .capsule)
            .accessibilityLabel("Unofficial API")
    }
}

/// The status glyph and word of a resolved value.
struct StatusLabel: View {
    let status: DataStatus

    var body: some View {
        Label {
            Text(status.label)
        } icon: {
            Image(systemName: status.symbol).foregroundStyle(status.color).imageScale(.small)
        }
        .accessibilityElement(children: .combine)
    }
}

/// A source's stable colour dot.
struct SourceDot: View {
    let provider: String

    var body: some View {
        Circle().fill(SourceStyle.color(provider)).frame(width: 8, height: 8).accessibilityHidden(true)
    }
}

/// A source's colour dot and name.
struct SourceChip: View {
    let provider: String
    let name: String

    var body: some View {
        HStack(spacing: 6) {
            SourceDot(provider: provider)
            Text(name)
        }
        .accessibilityElement(children: .combine)
    }
}

/// A source as a pill: its dot and name on the raised fill (dashboard cards, Explore filters).
struct SourcePill: View {
    let provider: String?
    let label: String
    var emphasised = false

    var body: some View {
        HStack(spacing: 8) {
            Circle().fill(provider.map(SourceStyle.color) ?? Color.inkFaint).frame(width: 10, height: 10).accessibilityHidden(true)
            Text(label).font(.footnote.weight(.semibold)).lineLimit(1)
        }
        .foregroundStyle(emphasised ? Color.ink : Color.inkMuted)
        .padding(.horizontal, 12)
        .frame(minHeight: 30)
        .background(Color.raised, in: .capsule)
    }
}
