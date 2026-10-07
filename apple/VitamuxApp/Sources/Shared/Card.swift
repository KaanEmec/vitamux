import SwiftUI

/// The card surface (docs/architecture/ios-design.md#surfaces): a top-to-bottom fade inside a
/// hairline border. In the light theme both ends are white, so the border carries the edge.
struct CardBackground: View {
    var radius: CGFloat = Radius.card

    var body: some View {
        RoundedRectangle(cornerRadius: radius, style: .continuous)
            .fill(LinearGradient(colors: [.cardTop, .cardBottom], startPoint: .top, endPoint: .bottom))
            .overlay(RoundedRectangle(cornerRadius: radius, style: .continuous).strokeBorder(Color.cardBorder))
    }
}

extension View {
    /// Pads the content and puts it on a card.
    func card(padding: CGFloat = Space.card, radius: CGFloat = Radius.card) -> some View {
        self.padding(padding)
            .background(CardBackground(radius: radius))
            .contentShape(.rect(cornerRadius: radius, style: .continuous))
    }

    /// The ground behind a screen. Lists and forms drop their own background, so their stock
    /// inset-grouped rows sit on the ground like the artboards' card groups.
    func screenBackground() -> some View {
        self.scrollContentBackground(.hidden)
            .background(Color.ground.ignoresSafeArea())
    }

    /// The same ground for a sheet's navigation stack.
    func sheetBackground() -> some View {
        self.scrollContentBackground(.hidden)
            .presentationBackground(Color.ground)
    }
}

/// A section title above cards: 13-point uppercase, tracked, tertiary (the artboards' `h2`).
struct SectionHeader: View {
    let title: String

    init(_ title: String) {
        self.title = title
    }

    var body: some View {
        Text(title)
            .font(.footnote.weight(.medium))
            .textCase(.uppercase)
            .tracking(1)
            .foregroundStyle(Color.inkFaint)
            .padding(.leading, 4)
            .frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityAddTraits(.isHeader)
    }
}

/// A value in the artboards' figures: bold SF with tabular digits and tight tracking, scaled with
/// Dynamic Type from its base size.
struct ValueText: View {
    /// The base sizes by place.
    enum Size: CGFloat {
        /// A metric detail's latest value.
        case detail = 44
        /// A large dashboard card.
        case hero = 40
        /// A widget's single value.
        case widget = 34
        /// A dashboard card.
        case card = 30
        /// A stat tile.
        case stat = 20

        var textStyle: Font.TextStyle {
            switch self {
            case .detail, .hero, .widget: .largeTitle
            case .card: .title
            case .stat: .title3
            }
        }

        var weight: Font.Weight { self == .stat ? .semibold : .bold }
    }

    let text: String
    let size: Size
    @ScaledMetric private var points: CGFloat

    init(_ text: String, size: Size) {
        self.text = text
        self.size = size
        _points = ScaledMetric(wrappedValue: size.rawValue, relativeTo: size.textStyle)
    }

    var body: some View {
        Text(text)
            .font(.system(size: points, weight: size.weight))
            .tracking(size == .stat ? 0 : -0.02 * points)
            .monospacedDigit()
    }
}
