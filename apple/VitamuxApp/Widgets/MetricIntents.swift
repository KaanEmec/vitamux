import AppIntents
import WidgetKit

/// A dashboard card, as the widgets' configuration lists it: the cards of the app's last dashboard
/// (`GET /settings/dashboard` with the catalogue's names), read from the snapshot.
struct MetricEntity: AppEntity {
    static let typeDisplayRepresentation: TypeDisplayRepresentation = "Metric"
    static let defaultQuery = MetricQuery()

    var id: String
    var label: String

    var displayRepresentation: DisplayRepresentation { DisplayRepresentation(title: "\(label)") }

    init(id: String, label: String) {
        self.id = id
        self.label = label
    }

    init(_ card: WidgetSnapshot.Card) {
        self.init(id: card.metric, label: card.label)
    }
}

struct MetricQuery: EntityQuery {
    /// The dashboard's cards; empty until the app has shown a dashboard.
    private var cards: [WidgetSnapshot.Card] {
        WidgetSnapshot.read(from: WidgetStore.live.snapshotURL)?.cards ?? []
    }

    func entities(for identifiers: [MetricEntity.ID]) async throws -> [MetricEntity] {
        let known = Dictionary(cards.map { ($0.metric, $0.label) }, uniquingKeysWith: { first, _ in first })
        // A metric no longer on the dashboard keeps its code as the name until it returns.
        return identifiers.map { MetricEntity(id: $0, label: known[$0] ?? $0) }
    }

    func suggestedEntities() async throws -> [MetricEntity] {
        cards.map(MetricEntity.init)
    }

    func defaultResult() async -> MetricEntity? {
        cards.first.map(MetricEntity.init)
    }
}

/// One metric: the small and medium card and the lock-screen value.
struct SelectMetricIntent: WidgetConfigurationIntent {
    static let title: LocalizedStringResource = "Metric"
    static let description = IntentDescription("Choose a metric from your dashboard.")

    @Parameter(title: "Metric")
    var metric: MetricEntity?

    init() {}

    init(metric: MetricEntity?) {
        self.metric = metric
    }
}

/// Up to three metrics side by side.
struct SelectMetricsIntent: WidgetConfigurationIntent {
    static let title: LocalizedStringResource = "Metrics"
    static let description = IntentDescription("Choose up to three metrics from your dashboard.")

    @Parameter(title: "Metrics", size: 3)
    var metrics: [MetricEntity]?

    init() {}

    init(metrics: [MetricEntity]) {
        self.metrics = metrics
    }
}
