import SwiftUI
import VitamuxKit
import WidgetKit

/// The entry view of every Vitamux widget: signed out, nothing to show, or the cards for the
/// family. Stock WidgetKit views on the app's card surface with the chart kit's colours
/// (docs/architecture/ios-design.md#widgets); values are marked private.
struct VitamuxWidgetView: View {
    enum Layout {
        /// One card: small, medium or a lock-screen value.
        case card
        /// Up to three cards side by side (medium).
        case row
    }

    let entry: WidgetEntry
    let layout: Layout
    /// Tests render a family outside WidgetKit, where the environment's family is fixed.
    var fixedFamily: WidgetFamily?
    @Environment(\.widgetFamily) private var environmentFamily

    private var family: WidgetFamily { fixedFamily ?? environmentFamily }

    var body: some View {
        content
            .foregroundStyle(Color.ink)
            .containerBackground(for: .widget) {
                LinearGradient(colors: [.cardTop, .cardBottom], startPoint: .top, endPoint: .bottom)
            }
    }

    @ViewBuilder private var content: some View {
        if entry.isSignedOut {
            SignedOutView(family: family)
                .widgetURL(.vitamuxDashboard)
        } else if layout == .row, family == .systemMedium {
            let cards = entry.cards(limit: 3)
            if cards.isEmpty { EmptyDashboardView(family: family) } else { RowView(entry: entry, cards: cards) }
        } else if let card = entry.cards(limit: 1).first {
            CardView(entry: entry, card: card, family: family)
                .widgetURL(card.link)
        } else {
            EmptyDashboardView(family: family)
        }
    }
}

// MARK: - States without values

/// A revoked, expired or never-started session: no values, a way into the app.
struct SignedOutView: View {
    let family: WidgetFamily

    var body: some View {
        switch family {
        case .accessoryInline:
            Text("Open Vitamux to sign in")
        case .accessoryCircular:
            ZStack {
                AccessoryWidgetBackground()
                Image(systemName: "person.crop.circle")
                    .font(.title2)
                    .accessibilityLabel("Open Vitamux to sign in")
            }
        case .accessoryRectangular:
            VStack(alignment: .leading) {
                Text("Vitamux").font(.headline).widgetAccentable()
                Text("Open Vitamux to sign in").font(.caption)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        default:
            VStack(alignment: .leading, spacing: 6) {
                Image(systemName: "person.crop.circle")
                    .font(.title2)
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                Spacer(minLength: 0)
                Text("Open Vitamux to sign in")
                    .font(.headline)
                Text("Values show here once you're signed in.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        }
    }
}

/// Signed in, but the dashboard had no card with data when the app last loaded it.
struct EmptyDashboardView: View {
    let family: WidgetFamily

    var body: some View {
        switch family {
        case .accessoryInline, .accessoryCircular, .accessoryRectangular:
            Text("No data yet").font(.caption)
        default:
            VStack(alignment: .leading, spacing: 6) {
                Image(systemName: "square.grid.2x2")
                    .font(.title2)
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                Spacer(minLength: 0)
                Text("No data yet").font(.headline)
                Text("Metrics appear once a source provides them.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            .widgetURL(.vitamuxDashboard)
        }
    }
}

// MARK: - One card

struct CardView: View {
    let entry: WidgetEntry
    let card: WidgetSnapshot.Card
    let family: WidgetFamily

    var body: some View {
        switch family {
        case .accessoryInline: InlineValue(entry: entry, card: card)
        case .accessoryCircular: CircularValue(entry: entry, card: card)
        case .accessoryRectangular: RectangularValue(entry: entry, card: card)
        case .systemMedium: MediumCard(entry: entry, card: card)
        default: SmallCard(entry: entry, card: card)
        }
    }
}

/// The card in the small family: name and status, the value, the trend (or sleep stages), as of.
private struct SmallCard: View {
    let entry: WidgetEntry
    let card: WidgetSnapshot.Card

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            CardHeader(card: card, showsWord: false)
            Group {
                Reading(card: card, size: .widget)
                if !card.sub.isEmpty {
                    Text(card.sub).font(.caption).foregroundStyle(Color.inkMuted).lineLimit(1)
                }
            }
            .privateValue(entry.redacts)
            Spacer(minLength: 0)
            Group {
                if let stages = card.stages {
                    StageBar(stages: stages, showsLegend: false)
                } else if card.hasValues {
                    Sparkline(values: card.values, bars: card.bars, band: card.band, mean: card.mean, hue: card.metricHue)
                }
            }
            .privateValue(entry.redacts)
            AsOf(entry: entry)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }
}

/// The card in the medium family: the reading with its second line and neutral delta on the
/// left, the trend (and sleep stages) on the right.
private struct MediumCard: View {
    let entry: WidgetEntry
    let card: WidgetSnapshot.Card

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            CardHeader(card: card, showsWord: true)
            HStack(alignment: .top, spacing: 14) {
                VStack(alignment: .leading, spacing: 4) {
                    Reading(card: card, size: .widget)
                    if !card.sub.isEmpty {
                        Text(card.sub).font(.caption).foregroundStyle(Color.inkMuted).lineLimit(1)
                    }
                    if !card.delta.isEmpty {
                        Text(card.delta).font(.caption).foregroundStyle(Color.inkMuted).lineLimit(2).monospacedDigit()
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                VStack(alignment: .leading, spacing: 6) {
                    if let stages = card.stages {
                        StageBar(stages: stages, showsLegend: true)
                    } else if card.hasValues {
                        Sparkline(values: card.values, bars: card.bars, band: card.band, mean: card.mean, hue: card.metricHue)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .privateValue(entry.redacts)
            Spacer(minLength: 0)
            AsOf(entry: entry)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }
}

// MARK: - Three cards

private struct RowView: View {
    let entry: WidgetEntry
    let cards: [WidgetSnapshot.Card]

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .top, spacing: 12) {
                ForEach(cards) { card in
                    Link(destination: card.link) {
                        RowColumn(card: card, redacts: entry.redacts)
                    }
                    .foregroundStyle(Color.ink) // a link would tint the column
                }
                ForEach(cards.count..<3, id: \.self) { _ in
                    Color.clear.frame(maxWidth: .infinity)
                }
            }
            Spacer(minLength: 0)
            AsOf(entry: entry)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .widgetURL(.vitamuxDashboard)
    }
}

private struct RowColumn: View {
    let card: WidgetSnapshot.Card
    let redacts: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                MetricTile(hue: card.metricHue, size: 28)
                Spacer(minLength: 0)
                StatusGlyph(status: card.dataStatus).frame(width: 10, height: 10)
            }
            Text(card.label)
                .font(.caption)
                .foregroundStyle(Color.inkMuted)
                .lineLimit(2, reservesSpace: true)
                .multilineTextAlignment(.leading)
            VStack(alignment: .leading, spacing: 0) {
                Text(card.value)
                    .font(.title3.bold())
                    .monospacedDigit()
                    .lineLimit(1)
                    .minimumScaleFactor(0.6)
                Text(card.unit.isEmpty ? " " : card.unit)
                    .font(.caption2)
                    .foregroundStyle(Color.inkMuted)
                    .lineLimit(1)
            }
            .privateValue(redacts)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .combine)
        .accessibilityValue(card.dataStatus.label)
    }
}

// MARK: - Lock screen

private struct RectangularValue: View {
    let entry: WidgetEntry
    let card: WidgetSnapshot.Card

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Label(card.label, systemImage: card.metricHue.symbol)
                .font(.caption)
                .lineLimit(1)
                .widgetAccentable()
            HStack(alignment: .firstTextBaseline, spacing: 4) {
                Text(card.value).font(.title3.bold()).monospacedDigit().minimumScaleFactor(0.6)
                if !card.unit.isEmpty { Text(card.unit).font(.caption) }
            }
            .lineLimit(1)
            .privateValue(entry.redacts)
            AsOf(entry: entry)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

private struct CircularValue: View {
    let entry: WidgetEntry
    let card: WidgetSnapshot.Card

    var body: some View {
        ZStack {
            AccessoryWidgetBackground()
            VStack(spacing: 0) {
                Image(systemName: card.metricHue.symbol)
                    .font(.caption2)
                    .widgetAccentable()
                    .accessibilityHidden(true)
                Text(card.value)
                    .font(.headline)
                    .monospacedDigit()
                    .lineLimit(1)
                    .minimumScaleFactor(0.5)
                    .privateValue(entry.redacts)
            }
            .padding(4)
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(card.label)
    }
}

private struct InlineValue: View {
    let entry: WidgetEntry
    let card: WidgetSnapshot.Card

    var body: some View {
        ViewThatFits {
            Text("\(card.label) \(card.value) \(card.unit)").privateValue(entry.redacts)
            Text("\(card.value) \(card.unit)").privateValue(entry.redacts)
        }
    }
}

// MARK: - Parts

private struct CardHeader: View {
    let card: WidgetSnapshot.Card
    let showsWord: Bool

    var body: some View {
        HStack(spacing: 6) {
            MetricTile(hue: card.metricHue, size: 28)
            Text(card.label)
                .font(.caption.weight(.medium))
                .foregroundStyle(Color.inkMuted)
                .lineLimit(1)
                .minimumScaleFactor(0.8)
            Spacer(minLength: 2)
            HStack(spacing: 4) {
                StatusGlyph(status: card.dataStatus).frame(width: 10, height: 10)
                if showsWord {
                    Text(card.dataStatus.label).font(.caption2).foregroundStyle(Color.inkMuted)
                }
            }
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(card.dataStatus.label)
        }
    }
}

private struct Reading: View {
    let card: WidgetSnapshot.Card
    let size: ValueText.Size

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 4) {
            ValueText(card.value, size: size)
                .lineLimit(1)
                .minimumScaleFactor(0.5)
            if !card.unit.isEmpty {
                Text(card.unit).font(.caption).foregroundStyle(Color.inkMuted).lineLimit(1)
            }
        }
    }
}

/// "As of 08:12"; once stale, with the weekday and a clock.
private struct AsOf: View {
    let entry: WidgetEntry

    var body: some View {
        HStack(spacing: 3) {
            if entry.isStale {
                Image(systemName: "clock").accessibilityHidden(true)
            }
            Text(entry.asOfText)
        }
        .font(.caption2)
        .foregroundStyle(.secondary)
        .lineLimit(1)
    }
}

/// Time in each sleep stage as one stacked bar, with an optional legend (as the dashboard card).
private struct StageBar: View {
    let stages: [WidgetSnapshot.Stage]
    let showsLegend: Bool

    var body: some View {
        let total = max(stages.reduce(0) { $0 + $1.seconds }, 1)
        VStack(alignment: .leading, spacing: 6) {
            GeometryReader { geometry in
                let width = geometry.size.width - CGFloat(max(stages.count - 1, 0)) * 2
                HStack(spacing: 2) {
                    ForEach(stages, id: \.kind) { stage in
                        Rectangle()
                            .fill(SleepStageKind(code: stage.kind).color)
                            .frame(width: max(0, width * stage.seconds / total))
                    }
                }
            }
            .frame(height: 10)
            .clipShape(.capsule)
            if showsLegend {
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(stages, id: \.kind) { stage in
                        HStack(spacing: 4) {
                            RoundedRectangle(cornerRadius: 2)
                                .fill(SleepStageKind(code: stage.kind).color)
                                .frame(width: 7, height: 7)
                            Text("\(SleepStageKind(code: stage.kind).label) \(Self.clock(stage.seconds))").monospacedDigit()
                        }
                    }
                }
                .font(.caption2)
                .foregroundStyle(.secondary)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Time in each sleep stage")
        .accessibilityValue(stages.map { "\(SleepStageKind(code: $0.kind).label) \(Self.clock($0.seconds))" }.joined(separator: ", "))
    }

    /// "1:23" for seconds.
    static func clock(_ seconds: Double) -> String {
        let minutes = Int((seconds / 60).rounded())
        return "\(minutes / 60):" + String(format: "%02d", minutes % 60)
    }
}

// MARK: - Privacy

extension View {
    /// Health values: private, so WidgetKit hides them while the iPhone is locked, drawn as a
    /// blurred placeholder. Off when the owner turned "Hide values while locked" off.
    func privateValue(_ redacts: Bool) -> some View {
        modifier(PrivateValue(redacts: redacts))
    }
}

private struct PrivateValue: ViewModifier {
    let redacts: Bool
    @Environment(\.redactionReasons) private var reasons

    func body(content: Content) -> some View {
        if redacts, reasons.contains(.privacy) {
            content
                .hidden()
                .accessibilityHidden(true)
                .overlay(alignment: .leading) {
                    RoundedRectangle(cornerRadius: 6)
                        .fill(.secondary.opacity(0.35))
                        .blur(radius: 3)
                        .accessibilityLabel("Hidden while locked")
                }
        } else {
            content.privacySensitive(redacts)
        }
    }
}
