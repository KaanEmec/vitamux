import SwiftUI
import VitamuxKit

/// A symbol on a tinted rounded square (docs/architecture/ios-design.md#tiles): 44 points in rows
/// and cards, 40 in settings rows, 56 in screen headers, 34 in widgets; the corner is 28% of the
/// side. Scales with Dynamic Type.
struct IconTile: View {
    let symbol: String
    let color: Color
    let tint: Color
    @ScaledMetric(relativeTo: .title3) private var side: CGFloat = TileSize.row

    init(symbol: String, color: Color, tint: Color, size: CGFloat = TileSize.row) {
        self.symbol = symbol
        self.color = color
        self.tint = tint
        _side = ScaledMetric(wrappedValue: size, relativeTo: .title3)
    }

    var body: some View {
        Image(systemName: symbol)
            .font(.system(size: side * 0.5, weight: .semibold))
            .foregroundStyle(color)
            .frame(width: side, height: side)
            .background(tint, in: .rect(cornerRadius: side * 0.28, style: .continuous))
            .accessibilityHidden(true)
    }
}

/// The metric's icon on the tint of its hue. The hue names the metric only, never a judgement.
struct MetricTile: View {
    let hue: MetricHue
    let size: CGFloat

    init(hue: MetricHue, size: CGFloat = TileSize.row) {
        self.hue = hue
        self.size = size
    }

    init(code: String, section: String? = nil, size: CGFloat = TileSize.row) {
        self.init(hue: MetricHue.of(code: code, section: section), size: size)
    }

    var body: some View {
        IconTile(symbol: hue.symbol, color: hue.color, tint: hue.tint, size: size)
    }
}

extension MetricHue {
    /// The tile icon of a hue (SF Symbols, as the panel's icon per hue).
    var symbol: String {
        switch self {
        case .heartRate: "heart.fill"
        case .hrv: "waveform.path.ecg"
        case .steps: "figure.walk"
        case .sleep: "moon.fill"
        case .bloodPressure: "drop.fill"
        case .spo2: "lungs.fill"
        case .weight: "scalemass.fill"
        case .activeEnergy: "flame.fill"
        case .vo2: "wind"
        case .lab: "flask.fill"
        case .other: "chart.xyaxis.line"
        }
    }
}
