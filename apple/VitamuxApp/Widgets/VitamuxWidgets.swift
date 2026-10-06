import SwiftUI
import WidgetKit

// Home-screen and lock-screen widgets (docs/architecture/ios-app.md#offline-cache-widgets-and-notifications):
// the dashboard's cards from the snapshot the app writes after each dashboard load, private while
// locked unless the owner turns that off, "Open Vitamux to sign in" once the session has ended,
// and a tap into the metric's Explore screen. The bundle (`@main`) is in VitamuxWidgetsBundle.swift.

/// One metric: a card on the home screen, a value on the lock screen.
struct MetricWidget: Widget {
    static let kind = "org.vitamux.metric"

    var body: some WidgetConfiguration {
        AppIntentConfiguration(kind: Self.kind, intent: SelectMetricIntent.self, provider: MetricProvider()) { entry in
            VitamuxWidgetView(entry: entry, layout: .card)
        }
        .configurationDisplayName("Metric")
        .description("A metric from your dashboard, with its trend.")
        .supportedFamilies([.systemSmall, .systemMedium, .accessoryRectangular, .accessoryCircular, .accessoryInline])
    }
}

/// Last night's sleep with its stages.
struct SleepWidget: Widget {
    static let kind = "org.vitamux.sleep"

    var body: some WidgetConfiguration {
        StaticConfiguration(kind: Self.kind, provider: SleepProvider()) { entry in
            VitamuxWidgetView(entry: entry, layout: .card)
        }
        .configurationDisplayName("Sleep")
        .description("Time asleep and in each stage.")
        .supportedFamilies([.systemSmall, .systemMedium, .accessoryRectangular])
    }
}

/// Three metrics side by side.
struct MetricsWidget: Widget {
    static let kind = "org.vitamux.metrics"

    var body: some WidgetConfiguration {
        AppIntentConfiguration(kind: Self.kind, intent: SelectMetricsIntent.self, provider: MetricsProvider()) { entry in
            VitamuxWidgetView(entry: entry, layout: .row)
        }
        .configurationDisplayName("Metrics")
        .description("Up to three metrics from your dashboard.")
        .supportedFamilies([.systemMedium])
    }
}
