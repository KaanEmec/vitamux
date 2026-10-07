import SwiftUI

/// The 44-point minimum hit area (Human Interface Guidelines) for small controls: the kit's range
/// stepper and the app's icon buttons, steppers and chips use it through these two helpers.
public enum TapTarget {
    public static let minimum: CGFloat = 44
}

extension View {
    /// Grows the view's hit area (and its accessibility frame) to at least 44 by 44 points
    /// without changing how it looks. Put it inside a button's label.
    public func tapTarget() -> some View {
        frame(minWidth: TapTarget.minimum, minHeight: TapTarget.minimum).contentShape(.rect)
    }
}

/// An icon-only label in a 44-point hit area; the title stays its accessibility label.
public struct IconTapTargetLabelStyle: LabelStyle {
    public init() {}

    public func makeBody(configuration: Configuration) -> some View {
        configuration.icon
            .tapTarget()
            .accessibilityRepresentation { configuration.title }
    }
}

extension LabelStyle where Self == IconTapTargetLabelStyle {
    /// `.iconOnly` with a 44-point hit area.
    public static var iconTapTarget: IconTapTargetLabelStyle { .init() }
}
