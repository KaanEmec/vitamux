import WidgetKit

/// The metric widget's timeline: the chosen card from the snapshot.
struct MetricProvider: AppIntentTimelineProvider {
    var store = WidgetStore.live

    func placeholder(in context: Context) -> WidgetEntry {
        WidgetEntry(date: .now, snapshot: .sample, redacts: false)
    }

    func snapshot(for configuration: SelectMetricIntent, in context: Context) async -> WidgetEntry {
        context.isPreview ? placeholder(in: context) : store.entry(at: .now, metrics: Self.codes(configuration))
    }

    func timeline(for configuration: SelectMetricIntent, in context: Context) async -> Timeline<WidgetEntry> {
        store.timeline(metrics: Self.codes(configuration))
    }

    static func codes(_ configuration: SelectMetricIntent) -> [String] {
        configuration.metric.map { [$0.id] } ?? []
    }
}

/// The three-metric row's timeline.
struct MetricsProvider: AppIntentTimelineProvider {
    var store = WidgetStore.live

    func placeholder(in context: Context) -> WidgetEntry {
        WidgetEntry(date: .now, snapshot: .sample, redacts: false)
    }

    func snapshot(for configuration: SelectMetricsIntent, in context: Context) async -> WidgetEntry {
        context.isPreview ? placeholder(in: context) : store.entry(at: .now, metrics: Self.codes(configuration))
    }

    func timeline(for configuration: SelectMetricsIntent, in context: Context) async -> Timeline<WidgetEntry> {
        store.timeline(metrics: Self.codes(configuration))
    }

    static func codes(_ configuration: SelectMetricsIntent) -> [String] {
        (configuration.metrics ?? []).map(\.id)
    }
}

/// The sleep widget's timeline: always the sleep card.
struct SleepProvider: TimelineProvider {
    var store = WidgetStore.live

    func placeholder(in context: Context) -> WidgetEntry {
        WidgetEntry(date: .now, snapshot: .sample, metrics: ["sleep"], redacts: false)
    }

    func getSnapshot(in context: Context, completion: @escaping (WidgetEntry) -> Void) {
        completion(context.isPreview ? placeholder(in: context) : store.entry(at: .now, metrics: ["sleep"]))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<WidgetEntry>) -> Void) {
        completion(store.timeline(metrics: ["sleep"]))
    }
}
