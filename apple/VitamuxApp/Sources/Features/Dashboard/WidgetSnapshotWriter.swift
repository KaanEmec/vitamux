import Foundation
import VitamuxKit
import WidgetKit

/// Hands today's dashboard to the widgets (J22.20): one `WidgetSnapshot` file in the app group,
/// written after each load of today and removed when the session ends. The widgets never fetch.
enum WidgetSnapshotWriter {
    /// The app group's file; under UI tests a temporary one.
    static var url: URL? {
        #if DEBUG
        if FakeServer.isUITestRun { return URL.temporaryDirectory.appending(path: "uitest-widgets/snapshot.json") }
        #endif
        return WidgetSnapshot.groupURL
    }

    /// When the widgets' values were fetched; nil before the first dashboard load.
    static var asOf: Date? { WidgetSnapshot.read(from: url)?.asOf }

    /// Writes today's shown cards once every visible card has its summary. Answers served from
    /// the offline cache keep their stored time, and never replace newer values.
    static func write(_ model: DashboardModel, status: ResponseCache.Status, now: Date = .now) {
        guard model.isToday, model.isReady, model.summaryProblem == nil, model.layout.value != nil, let url else { return }
        let fetched: Date
        switch status {
        case .online: fetched = now
        case .offline(let from?): fetched = from
        case .offline(nil): return
        }
        if let current = asOf, current > fetched { return }
        let snapshot = WidgetSnapshot(asOf: fetched, day: model.day.description, timeZone: model.timeZoneName, cards: model.widgetCards)
        do {
            try snapshot.write(to: url)
            WidgetCenter.shared.reloadAllTimelines()
        } catch {
            // The widgets keep their last values; nothing to show the owner.
        }
    }

    /// Sign-out, an expired session, or a new sign-in: the widgets ask to sign in.
    static func clear() {
        WidgetSnapshot.remove(at: url)
        WidgetCenter.shared.reloadAllTimelines()
    }
}

extension DashboardModel {
    /// The shown cards as the widgets draw them.
    var widgetCards: [WidgetSnapshot.Card] {
        shown.compactMap { card in
            guard let content = contents[card.metric] else { return nil }
            return WidgetSnapshot.Card(
                metric: card.metric,
                label: DashboardLayout.label(card.metric),
                hue: MetricHue.of(code: card.metric, section: section(of: card.metric)).rawValue,
                value: content.value,
                unit: content.unit,
                sub: content.sub,
                delta: content.delta,
                status: content.status.rawValue,
                values: content.values,
                bars: content.bars,
                band: content.band,
                mean: content.mean,
                stages: content.stages?.map { WidgetSnapshot.Stage(kind: $0.kind.rawValue, seconds: $0.seconds) }
            )
        }
    }
}
