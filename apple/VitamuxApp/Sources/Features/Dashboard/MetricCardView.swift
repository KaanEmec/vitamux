import SwiftUI
import VitamuxKit

/// One card: the metric's tile and name, the status (shape and word), the value with its unit, a
/// second line (pulse, time in bed), sleep stages, a sparkline in the metric hue, the neutral
/// delta and the sources. S is compact (half width); L draws the value larger. A tap opens the
/// metric in Explore.
struct MetricCardView: View {
    let card: DashboardCard
    let content: CardContent
    let section: String?
    let open: () -> Void

    var body: some View {
        Button(action: open) {
            VStack(alignment: .leading, spacing: 10) {
                HStack(spacing: 10) {
                    MetricTile(hue: hue, size: compact ? TileSize.settings : TileSize.row)
                    Text(DashboardLayout.label(card.metric))
                        .font(.subheadline)
                        .foregroundStyle(Color.inkMuted)
                        .lineLimit(2)
                        .minimumScaleFactor(0.85)
                        .layoutPriority(1)
                    Spacer(minLength: 4)
                    StatusMark(status: content.status, showsWord: !compact)
                }
                Reading(content: content, large: card.size == .l)
                if !content.sub.isEmpty {
                    Text(content.sub).font(.subheadline).foregroundStyle(Color.inkMuted)
                }
                if let stages = content.stages {
                    StageBar(stages: stages, showsLegend: !compact)
                }
                if content.values.contains(where: { $0 != nil }) {
                    Sparkline(values: content.values, bars: content.bars, band: content.band, mean: content.mean, hue: hue)
                }
                if !footnote.isEmpty {
                    Text(footnote).font(.footnote).foregroundStyle(Color.inkMuted).monospacedDigit()
                }
                if !content.chips.isEmpty {
                    SourceChips(chips: content.chips, limit: compact ? 1 : 3)
                }
            }
            .padding(Space.card)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            .background(CardBackground())
            .contentShape(.rect(cornerRadius: Radius.card))
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("card-\(card.metric)")
    }

    private var compact: Bool { card.size.isHalf }

    /// The delta; a compact card names a status other than direct here ("Fallback · +0.2 …").
    private var footnote: String {
        guard compact, content.status != .direct else { return content.delta }
        return [content.status.label, content.delta].filter { !$0.isEmpty }.joined(separator: " · ")
    }
    private var hue: MetricHue { MetricHue.of(code: card.metric, section: section) }
}

private struct Reading: View {
    let content: CardContent
    let large: Bool

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            ValueText(content.value, size: large ? .hero : .card)
                .lineLimit(1)
                .minimumScaleFactor(0.6)
            if !content.unit.isEmpty {
                Text(content.unit).font(.subheadline).foregroundStyle(Color.inkMuted)
            }
        }
    }
}

/// The data status as its glyph, with the word unless a compact card shows a direct value.
private struct StatusMark: View {
    let status: DataStatus
    let showsWord: Bool

    var body: some View {
        HStack(spacing: 5) {
            StatusGlyph(status: status).frame(width: 12, height: 12)
            if showsWord {
                Text(status.label).font(.subheadline).foregroundStyle(Color.inkMuted)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(status.label)
    }
}

/// Time in each sleep stage as one stacked bar, with a legend.
private struct StageBar: View {
    let stages: [CardContent.Stage]
    let showsLegend: Bool

    var body: some View {
        let total = max(stages.reduce(0) { $0 + $1.seconds }, 1)
        VStack(alignment: .leading, spacing: 8) {
            GeometryReader { geometry in
                let width = geometry.size.width - CGFloat(stages.count - 1) * 2
                HStack(spacing: 2) {
                    ForEach(stages, id: \.kind) { stage in
                        Rectangle().fill(stage.kind.color).frame(width: max(0, width * stage.seconds / total))
                    }
                }
            }
            .frame(height: 12)
            .clipShape(.capsule)
            if showsLegend {
                ViewThatFits {
                    HStack(spacing: 12) { legend }
                    VStack(alignment: .leading, spacing: 4) { legend }
                }
                .font(.footnote)
                .foregroundStyle(Color.inkMuted)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Time in each sleep stage")
        .accessibilityValue(stages.map { "\($0.kind.label) \(Self.clock($0.seconds))" }.joined(separator: ", "))
    }

    private var legend: some View {
        ForEach(stages, id: \.kind) { stage in
            HStack(spacing: 5) {
                RoundedRectangle(cornerRadius: 2).fill(stage.kind.color).frame(width: 8, height: 8)
                Text("\(stage.kind.label) \(Self.clock(stage.seconds))").monospacedDigit()
            }
        }
    }

    /// "1:23" for seconds.
    static func clock(_ seconds: Double) -> String {
        let minutes = Int((seconds / 60).rounded())
        return "\(minutes / 60):" + String(format: "%02d", minutes % 60)
    }
}

/// The sources of the value, each in its stable colour; more than `limit` show as a count.
private struct SourceChips: View {
    let chips: [CardContent.Chip]
    let limit: Int

    var body: some View {
        HStack(spacing: 6) {
            ForEach(Array(chips.prefix(limit).enumerated()), id: \.element.label) { index, chip in
                SourcePill(provider: chip.provider, label: chip.label, emphasised: index == 0)
            }
            if chips.count > limit {
                Text("+\(chips.count - limit)").font(.footnote).foregroundStyle(Color.inkMuted)
            }
        }
    }
}

