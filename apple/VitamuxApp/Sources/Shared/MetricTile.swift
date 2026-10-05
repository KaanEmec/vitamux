import SwiftUI
import VitamuxKit

/// The metric's icon on a tint of its hue. The hue names the metric only, never a judgement.
struct MetricTile: View {
    let hue: MetricHue
    @ScaledMetric(relativeTo: .title3) private var side: CGFloat = 36

    init(hue: MetricHue, size: CGFloat = 36) {
        self.hue = hue
        _side = ScaledMetric(wrappedValue: size, relativeTo: .title3)
    }

    init(code: String, section: String? = nil, size: CGFloat = 36) {
        self.init(hue: MetricHue.of(code: code, section: section), size: size)
    }

    var body: some View {
        Image(systemName: hue.symbol)
            .font(.system(size: side * 0.45, weight: .semibold))
            .foregroundStyle(hue.color)
            .frame(width: side, height: side)
            .background(hue.color.opacity(0.16), in: .rect(cornerRadius: side * 0.28))
            .accessibilityHidden(true)
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
