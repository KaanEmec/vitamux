import SwiftUI
import WidgetKit

/// The extension's entry point; kept apart so the widget tests compile everything else.
@main
struct VitamuxWidgets: WidgetBundle {
    var body: some Widget {
        MetricWidget()
        SleepWidget()
        MetricsWidget()
    }
}
