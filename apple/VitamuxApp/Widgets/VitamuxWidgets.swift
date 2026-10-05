import SwiftUI
import WidgetKit

// The widget extension stub. Metric widgets read the app group's cache and redact while locked
// (docs/architecture/ios-app.md#offline-cache-widgets-and-notifications); until then one widget
// opens the app.

@main
struct VitamuxWidgets: WidgetBundle {
    var body: some Widget {
        OpenAppWidget()
    }
}

struct OpenAppWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: "org.vitamux.open", provider: Provider()) { _ in
            VStack(spacing: 6) {
                Image(systemName: "waveform.path.ecg").font(.title)
                Text("Vitamux").font(.headline)
            }
            .containerBackground(.fill.tertiary, for: .widget)
            .widgetURL(URL(string: "vitamux://dashboard"))
        }
        .configurationDisplayName("Vitamux")
        .description("Opens the dashboard.")
        .supportedFamilies([.systemSmall])
    }
}

struct Provider: TimelineProvider {
    struct Entry: TimelineEntry {
        let date: Date
    }

    func placeholder(in context: Context) -> Entry { Entry(date: .now) }

    func getSnapshot(in context: Context, completion: @escaping (Entry) -> Void) {
        completion(Entry(date: .now))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<Entry>) -> Void) {
        completion(Timeline(entries: [Entry(date: .now)], policy: .never))
    }
}
