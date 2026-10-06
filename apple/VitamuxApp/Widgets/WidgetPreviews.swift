import SwiftUI
import WidgetKit

// Previews of every family, unlocked and locked (privacy redaction), stale and signed out, with
// the synthetic sample. `WidgetPreviewTests` renders the same matrix.

private let now = WidgetSnapshot.sample.asOf.addingTimeInterval(1800)
private let fresh = WidgetEntry(date: now, snapshot: .sample)
private let stale = WidgetEntry(date: now.addingTimeInterval(WidgetEntry.staleAfter), snapshot: .sample)
private let signedOut = WidgetEntry(date: now, snapshot: nil)
private let sleep = WidgetEntry(date: now, snapshot: .sample, metrics: ["sleep"])

#Preview("Metric, small", as: .systemSmall) {
    MetricWidget()
} timeline: {
    WidgetEntry(date: now, snapshot: .sample, metrics: ["resting_heart_rate"])
    stale
    signedOut
}

#Preview("Metric, medium", as: .systemMedium) {
    MetricWidget()
} timeline: {
    WidgetEntry(date: now, snapshot: .sample, metrics: ["hrv_rmssd_nightly"])
    WidgetEntry(date: now, snapshot: .sample, metrics: ["blood_pressure"])
    signedOut
}

#Preview("Sleep", as: .systemMedium) {
    SleepWidget()
} timeline: {
    sleep
    WidgetEntry(date: stale.date, snapshot: .sample, metrics: ["sleep"])
}

#Preview("Three metrics", as: .systemMedium) {
    MetricsWidget()
} timeline: {
    WidgetEntry(date: now, snapshot: .sample, metrics: ["resting_heart_rate", "steps", "blood_pressure"])
    stale
    signedOut
}

#Preview("Lock screen", as: .accessoryRectangular) {
    MetricWidget()
} timeline: {
    WidgetEntry(date: now, snapshot: .sample, metrics: ["resting_heart_rate"])
    signedOut
}

#Preview("Lock screen, circular", as: .accessoryCircular) {
    MetricWidget()
} timeline: {
    WidgetEntry(date: now, snapshot: .sample, metrics: ["resting_heart_rate"])
    signedOut
}

#Preview("Locked: values hidden") {
    VitamuxWidgetView(entry: fresh, layout: .card)
        .redacted(reason: .privacy)
        .frame(width: 170, height: 170)
}

#Preview("Locked, redaction off") {
    VitamuxWidgetView(entry: WidgetEntry(date: now, snapshot: .sample, redacts: false), layout: .card)
        .redacted(reason: .privacy)
        .frame(width: 170, height: 170)
}
