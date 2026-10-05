import Foundation
import HealthBridgeCore
import HealthKit

/// Anchored incremental sync of enabled types: page through each type, upload, commit the anchor
/// after the server accepted the page, and repeat until a short page.
public actor HealthSync {
    /// The default page size; a type's own limit is `HealthType.pageLimit`.
    public static let pageLimit = Batcher.maxSamples
    /// Activity summaries: the days re-read on every sync (today and the 6 before) and the days per page.
    public static let summaryRecentDays = 7, summaryPageDays = 366
    /// No activity summary predates the Apple Watch, so a backfill never reads before 2015.
    static let summaryFloor = DateComponents(year: 2015, month: 1, day: 1)

    let store: HealthStore
    let sender: BatchSender
    let anchors: AnchorStore
    let backfillStart: Date
    let calendar: Calendar
    let now: @Sendable () -> Date
    var running: Set<String> = []

    /// `backfillStart`: the earliest date to pull; HealthKit's earliest permitted date wins if later.
    /// `calendar` decides activity-summary days; `now` is injectable for tests.
    public init(store: HealthStore, sender: BatchSender, anchors: AnchorStore, backfillStart: Date = .distantPast,
                calendar: Calendar = .current, now: @escaping @Sendable () -> Date = { Date() }) {
        self.store = store
        self.sender = sender
        self.anchors = anchors
        self.backfillStart = backfillStart
        self.calendar = calendar
        self.now = now
    }

    /// Asks for read access to every type of `groups`, and only those. HealthKit never reveals a denial.
    public func requestAuthorization(for groups: Set<MetricGroup>) async throws {
        try await store.requestReadAuthorization(Registry.types(in: groups).reduce(into: []) { $0.formUnion($1.readTypes) })
    }

    /// Syncs one type. A concurrent call for the same type returns at once; a type this OS does
    /// not know is skipped.
    public func sync(_ type: HealthType) async throws {
        if type.isActivitySummary { return try await syncSummaries(type) }
        guard let sampleType = type.sampleType, running.insert(type.id).inserted else { return }
        defer { running.remove(type.id) }
        let start = max(store.earliestPermittedSampleDate(), backfillStart)
        while true {
            let before = anchors.anchor(for: type.id)
            let startedAt = rfc3339(now())
            let page = try await store.anchoredPage(of: sampleType, from: start, anchor: before, limit: type.pageLimit)
            var samples: [Sample] = []
            samples.reserveCapacity(page.samples.count)
            for sample in page.samples { samples.append(try await detailed(sample, of: type)) }
            let body = SamplesPage(
                type: type.id,
                anchor: AnchorInfo(beforeHash: AnchorStore.hash(before), afterHash: AnchorStore.hash(page.anchor), queryStartedAt: startedAt),
                samples: samples,
                deleted: page.deleted.map { Deleted(uuid: $0.uuidString) })
            try await sender.send(body, newAnchor: page.anchor)
            if page.samples.count + page.deleted.count < type.pageLimit { return }
        }
    }

    /// Maps a sample and adds the detail its type needs. A failed detail read fails the page, so the
    /// anchor stays and the page is read again; nothing is sent without its detail.
    func detailed(_ sample: HKSample, of type: HealthType) async throws -> Sample {
        var out = Mapping.sample(sample, unit: type.unit)
        switch type.detail {
        case .ecg: out.ecg = try await store.electrocardiogram(sample)
        case .beats: out.beats = try await store.heartbeats(sample)
        case .route:
            out.route = try await store.route(sample)
            out.workoutUUID = try await store.workoutUUID(of: sample)?.uuidString
        case .workoutLink: out.workoutUUID = try await store.workoutUUID(of: sample)?.uuidString
        case nil: break
        }
        return out
    }

    /// Activity summaries have no anchor: the first sync reads from the backfill start in pages of
    /// `summaryPageDays`, later syncs re-read the last `summaryRecentDays` days. The stored "anchor" is the
    /// last day read (`YYYY-MM-DD`), so an interrupted backfill resumes. Each page's anchor hash is the
    /// hash of its summaries, so an unchanged day is a duplicate and a changed day a new raw version.
    func syncSummaries(_ type: HealthType) async throws {
        guard running.insert(type.id).inserted else { return }
        defer { running.remove(type.id) }
        let today = calendar.startOfDay(for: now())
        let recent = calendar.date(byAdding: .day, value: 1 - Self.summaryRecentDays, to: today)!
        var from: Date
        if let last = anchors.anchor(for: type.id).flatMap({ day(String(decoding: $0, as: UTF8.self)) }) {
            from = min(calendar.date(byAdding: .day, value: 1, to: last)!, recent)
        } else {
            let floor = calendar.date(from: Self.summaryFloor)!
            from = max(calendar.startOfDay(for: max(store.earliestPermittedSampleDate(), backfillStart)), floor)
        }
        while from <= today {
            let through = min(calendar.date(byAdding: .day, value: Self.summaryPageDays - 1, to: from)!, today)
            let startedAt = rfc3339(now())
            let summaries = try await store.activitySummaries(from: from, through: through, in: calendar).sorted { $0.date < $1.date }
            let hash = Mapping.summaryHash(summaries)
            let body = SamplesPage(type: type.id, anchor: AnchorInfo(beforeHash: hash, afterHash: hash, queryStartedAt: startedAt),
                                   samples: summaries.compactMap { Mapping.sample($0, calendar: calendar) }, deleted: [])
            try await sender.send(body, newAnchor: Data(dayString(through).utf8))
            from = calendar.date(byAdding: .day, value: 1, to: through)!
        }
    }

    func dayString(_ date: Date) -> String {
        let c = calendar.dateComponents([.year, .month, .day], from: date)
        return String(format: "%04d-%02d-%02d", c.year!, c.month!, c.day!)
    }

    func day(_ text: String) -> Date? {
        let parts = text.split(separator: "-").compactMap { Int($0) }
        guard parts.count == 3 else { return nil }
        return calendar.date(from: DateComponents(year: parts[0], month: parts[1], day: parts[2]))
    }

    /// Syncs each type in turn and returns the failures by type id.
    public func syncAll(_ types: [HealthType]) async -> [String: any Error] {
        var failures: [String: any Error] = [:]
        for type in types {
            do { try await sync(type) } catch { failures[type.id] = error }
        }
        return failures
    }

    /// Registers observer queries with background delivery for every sample type (an activity summary
    /// has neither). Each callback syncs its type and always completes, even on failure, so iOS keeps
    /// delivering. A type whose background delivery iOS refuses keeps its observer and does not stop the
    /// others; the refusals are returned by type id.
    @discardableResult
    public func observe(_ types: [HealthType]) async -> [String: any Error] {
        var failures: [String: any Error] = [:]
        for type in types {
            guard let sampleType = type.sampleType else { continue }
            store.observe(sampleType) { done in
                Task {
                    defer { done() }
                    try? await self.sync(type)
                }
            }
            do { try await store.enableBackgroundDelivery(for: sampleType) } catch { failures[type.id] = error }
        }
        return failures
    }

    /// Enabling a type or a server-requested reset: the next sync is a full pull.
    public func resetAnchor(_ type: HealthType) {
        anchors.reset(type.id)
    }
}
