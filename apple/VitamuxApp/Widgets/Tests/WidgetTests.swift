import Foundation
import SwiftUI
import Testing
import WidgetKit

/// The widgets without a home screen: the snapshot file, the session and stale rules, the
/// timeline, the deep links, and every family rendered fresh, stale, signed out, locked and
/// unlocked. A real lock screen and a day of refreshes need a device (J22.23).
struct WidgetSnapshotTests {
    private let url = URL.temporaryDirectory.appending(path: "widget-tests-\(UUID().uuidString)/snapshot.json")

    @Test func `a written snapshot reads back the same`() throws {
        try WidgetSnapshot.sample.write(to: url)
        #expect(WidgetSnapshot.read(from: url) == .sample)
        WidgetSnapshot.remove(at: url)
        #expect(WidgetSnapshot.read(from: url) == nil)
    }

    @Test func `a snapshot of another version is ignored`() throws {
        var other = WidgetSnapshot.sample
        other.version = WidgetSnapshot.version + 1
        try other.write(to: url)
        #expect(WidgetSnapshot.read(from: url) == nil)
    }

    @Test func `no file and no location read as nothing`() {
        #expect(WidgetSnapshot.read(from: url) == nil)
        #expect(WidgetSnapshot.read(from: nil) == nil)
    }
}

struct WidgetStoreTests {
    private let asOf = WidgetSnapshot.sample.asOf

    private func store(_ snapshot: WidgetSnapshot?, session: WidgetStore.Session, redacts: Bool = true) throws -> WidgetStore {
        let url = URL.temporaryDirectory.appending(path: "widget-store-\(UUID().uuidString)/snapshot.json")
        if let snapshot { try snapshot.write(to: url) }
        return WidgetStore(snapshotURL: url, redacts: { redacts }, session: { session })
    }

    @Test func `an active session shows the snapshot`() throws {
        let entry = try store(.sample, session: .active(until: asOf.addingTimeInterval(86_400)))
            .entry(at: asOf.addingTimeInterval(60), metrics: ["steps"])
        #expect(!entry.isSignedOut)
        #expect(!entry.isStale)
        #expect(entry.redacts)
        #expect(entry.cards(limit: 1).map(\.metric) == ["steps"])
    }

    @Test(arguments: [
        WidgetStore.Session.none,
        .active(until: WidgetSnapshot.sample.asOf.addingTimeInterval(30)),
    ])
    func `a revoked or expired session signs out`(session: WidgetStore.Session) throws {
        let entry = try store(.sample, session: session).entry(at: asOf.addingTimeInterval(60), metrics: [])
        #expect(entry.isSignedOut)
        #expect(entry.cards(limit: 3).isEmpty)
    }

    @Test func `no snapshot signs out`() throws {
        #expect(try store(nil, session: .unknown).entry(at: asOf, metrics: []).isSignedOut)
    }

    @Test func `an unreadable Keychain keeps the snapshot until the idle limit`() throws {
        let store = try store(.sample, session: .unknown)
        #expect(!store.entry(at: asOf.addingTimeInterval(86_400), metrics: []).isSignedOut)
        #expect(store.entry(at: asOf.addingTimeInterval(WidgetStore.sessionIdle), metrics: []).isSignedOut)
    }

    @Test func `the redaction preference reaches the entry`() throws {
        #expect(try !store(.sample, session: .unknown, redacts: false).entry(at: asOf, metrics: []).redacts)
    }

    @Test func `the timeline turns stale, then signs out when the session ends`() throws {
        let end = asOf.addingTimeInterval(2 * 86_400)
        let now = asOf.addingTimeInterval(60)
        let timeline = try store(.sample, session: .active(until: end)).timeline(metrics: [], now: now)
        #expect(timeline.entries.map(\.date) == [now, asOf.addingTimeInterval(WidgetEntry.staleAfter), end])
        #expect(timeline.entries.map(\.isStale) == [false, true, false])
        #expect(timeline.entries.map(\.isSignedOut) == [false, false, true])
        #expect(timeline.policy == .after(now.addingTimeInterval(4 * 3600)))
    }

    @Test func `the stale line names the day`() {
        let fresh = WidgetEntry(date: asOf.addingTimeInterval(60), snapshot: .sample)
        let stale = WidgetEntry(date: asOf.addingTimeInterval(WidgetEntry.staleAfter + 1), snapshot: .sample)
        #expect(fresh.asOfText.hasPrefix("As of "))
        #expect(stale.isStale)
        #expect(stale.asOfText.count > fresh.asOfText.count, "the weekday is added")
    }

    @Test func `chosen metrics keep their order, unknown ones fall back to the dashboard`() {
        let entry = WidgetEntry(date: asOf, snapshot: .sample, metrics: ["steps", "gone", "sleep"])
        #expect(entry.cards(limit: 3).map(\.metric) == ["steps", "sleep"])
        let fallback = WidgetEntry(date: asOf, snapshot: .sample, metrics: ["gone"])
        #expect(fallback.cards(limit: 3).map(\.metric) == ["sleep", "resting_heart_rate", "hrv_rmssd_nightly"])
    }

    @Test(arguments: [
        ("sleep", "vitamux://explore/sleep"),
        ("blood_pressure", "vitamux://explore/blood-pressure"),
        ("resting_heart_rate", "vitamux://explore/resting_heart_rate"),
    ])
    func `a tap opens the matching Explore route`(metric: String, link: String) throws {
        let card = try #require(WidgetSnapshot.sample.cards.first { $0.metric == metric })
        #expect(card.link.absoluteString == link)
    }
}

/// Each family in each state renders, and private values change only when redaction applies.
@MainActor
struct WidgetPreviewTests {
    enum State: String, CaseIterable {
        case fresh, stale, signedOut
    }

    static let families: [(WidgetFamily, VitamuxWidgetView.Layout, CGSize)] = [
        (.systemSmall, .card, CGSize(width: 170, height: 170)),
        (.systemMedium, .card, CGSize(width: 364, height: 170)),
        (.systemMedium, .row, CGSize(width: 364, height: 170)),
        (.accessoryRectangular, .card, CGSize(width: 172, height: 76)),
        (.accessoryCircular, .card, CGSize(width: 76, height: 76)),
        (.accessoryInline, .card, CGSize(width: 250, height: 20)),
    ]

    private func entry(_ state: State, metrics: [String] = ["resting_heart_rate"], redacts: Bool = true) -> WidgetEntry {
        let asOf = WidgetSnapshot.sample.asOf
        return switch state {
        case .fresh: WidgetEntry(date: asOf.addingTimeInterval(60), snapshot: .sample, metrics: metrics, redacts: redacts)
        case .stale: WidgetEntry(date: asOf.addingTimeInterval(WidgetEntry.staleAfter + 60), snapshot: .sample, metrics: metrics, redacts: redacts)
        case .signedOut: WidgetEntry(date: asOf, snapshot: nil)
        }
    }

    private func render(_ entry: WidgetEntry, _ family: WidgetFamily, _ layout: VitamuxWidgetView.Layout, _ size: CGSize, locked: Bool) -> Data? {
        let view = VitamuxWidgetView(entry: entry, layout: layout, fixedFamily: family)
            .padding(12)
            .frame(width: size.width, height: size.height)
            .background(Color.white)
            .redacted(reason: locked ? .privacy : [])
            .environment(\.colorScheme, .light)
        let renderer = ImageRenderer(content: view)
        renderer.scale = 2
        let image = renderer.uiImage
        if let image, let folder = ProcessInfo.processInfo.environment["WIDGET_RENDER_DIR"] {
            let name = "\(family)-\(layout)-\(entry.isSignedOut ? "signedOut" : entry.isStale ? "stale" : "fresh")-\(locked ? "locked" : "unlocked")-\(entry.redacts ? "redacts" : "shows")-\(entry.metrics.first ?? "")"
            try? image.pngData()?.write(to: URL(filePath: folder).appending(path: name + ".png"))
        }
        return image?.pngData()
    }

    @Test(arguments: State.allCases)
    func `every family renders`(state: State) throws {
        for (family, layout, size) in Self.families {
            for locked in [false, true] {
                // The row shows the dashboard's first three cards.
                let data = render(entry(state, metrics: layout == .row ? [] : ["resting_heart_rate"]), family, layout, size, locked: locked)
                #expect(data?.isEmpty == false, "\(family) \(layout) \(state) locked: \(locked)")
            }
        }
    }

    @Test(arguments: ["sleep", "blood_pressure", "steps"])
    func `the sleep and family cards render`(metric: String) {
        for (family, layout, size) in Self.families where layout == .card {
            #expect(render(entry(.fresh, metrics: [metric]), family, layout, size, locked: false)?.isEmpty == false)
        }
    }

    @Test func `locked hides values only while redaction is on`() throws {
        let size = CGSize(width: 170, height: 170)
        let on = entry(.fresh)
        #expect(render(on, .systemSmall, .card, size, locked: true) != render(on, .systemSmall, .card, size, locked: false))
        let off = entry(.fresh, redacts: false)
        #expect(render(off, .systemSmall, .card, size, locked: true) == render(off, .systemSmall, .card, size, locked: false))
    }
}
