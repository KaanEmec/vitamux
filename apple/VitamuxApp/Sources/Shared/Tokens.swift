import SwiftUI

// The app's design tokens (docs/architecture/ios-design.md#tokens): one flat file. Colours are
// light/dark pairs in `Tokens.xcassets`, compiled into the app and the widget; metric, source,
// stage and status colours stay in VitamuxKit's `ChartPalette`. Sizes follow the artboards and
// scale with Dynamic Type where a view reads them through `@ScaledMetric`.

extension Color {
    /// The screen ground behind cards and lists.
    static let ground = token("Ground")
    /// A card fades from `cardTop` to `cardBottom` inside a `cardBorder` hairline.
    static let cardTop = token("CardTop")
    static let cardBottom = token("CardBottom")
    static let cardBorder = token("CardBorder")
    /// Row separators inside a card.
    static let hairline = token("Hairline")
    /// Chips, segmented tracks and secondary buttons on a card.
    static let raised = token("Raised")

    /// Text: primary, secondary (labels, units, sub-lines) and tertiary (section headers, axes).
    static let ink = token("Ink")
    static let inkMuted = token("InkMuted")
    static let inkFaint = token("InkFaint")

    /// The one teal accent (also the asset catalogue's global `AccentColor`).
    static let accent = token("Accent")
    static let accentSoft = token("AccentSoft")
    static let onAccent = token("OnAccent")

    /// Feedback about the app itself (connection health, errors, notices), always with a shape
    /// and a word; never about whether a value is good.
    static let feedbackOK = token("FeedbackOK")
    static let feedbackWarn = token("FeedbackWarn")
    static let feedbackWarnSoft = token("FeedbackWarnSoft")
    static let feedbackError = token("FeedbackError")
    static let feedbackErrorSoft = token("FeedbackErrorSoft")
    static let feedbackInfo = token("FeedbackInfo")
    static let feedbackInfoSoft = token("FeedbackInfoSoft")

    /// The rule lens and draft series.
    static let draft = token("Draft")
    static let draftSoft = token("DraftSoft")

    private static func token(_ name: String) -> Color { Color(name, bundle: .tokens) }
}

/// Corner radii by element.
enum Radius {
    /// A card (dashboard, chart, specialised sections).
    static let card: CGFloat = 18
    /// An alert banner or a stat tile.
    static let tile: CGFloat = 14
    /// A text field or a full-width button.
    static let control: CGFloat = 12
}

/// Spacing on the 4-point grid.
enum Space {
    /// The screen's side gutter.
    static let gutter: CGFloat = 16
    /// Inside a card.
    static let card: CGFloat = 14
    /// Between stacked blocks on a screen.
    static let stack: CGFloat = 14
    /// Between cards in a grid.
    static let grid: CGFloat = 12
}

/// Icon-tile sides: rows and cards, settings rows, screen headers, widgets.
enum TileSize {
    static let row: CGFloat = 44
    static let settings: CGFloat = 40
    static let header: CGFloat = 56
    static let widget: CGFloat = 34
}

private final class TokenBundleMarker {}

extension Bundle {
    /// The bundle that holds `Tokens.xcassets`: the app, the widget extension or its test bundle.
    static let tokens = Bundle(for: TokenBundleMarker.self)
}
