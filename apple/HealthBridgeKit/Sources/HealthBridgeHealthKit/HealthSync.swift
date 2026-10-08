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
        let excluded = try await exclusions(for: type, sampleType)
        while true {
            let before = anchors.anchor(for: type.id)
            let startedAt = rfc3339(now())
            let page = try await store.anchoredPage(of: sampleType, from: start, anchor: before, limit: type.pageLimit, excluding: excluded)
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

    /// The sources of `type` the stored filter does not take. When an app the last read excluded is
    /// present and taken now, the type's anchor is reset first, so the app's history is pulled
    /// (take to ignore needs no reset: later reads leave it out). Without a filter, and with nothing
    /// excluded before, HealthKit is not asked for the sources at all.
    func exclusions(for type: HealthType, _ sampleType: HKSampleType) async throws -> [HealthSource] {
        let filter = anchors.sourceFilter
        let before = anchors.excluded(for: type.id)
        guard filter != nil || !before.isEmpty else { return [] }
        let sources = try await store.sources(for: sampleType)
        let excluded = sources.filter { !(filter?.takes($0.bundleID, type: type.id) ?? true) }
        let now = Set(excluded.map(\.bundleID))
        let present = Set(sources.map(\.bundleID))
        if !before.subtracting(now).isDisjoint(with: present) { anchors.reset(type.id) }
        anchors.setExcluded(now, for: type.id)
        return excluded
    }

    /// Stores the server's source filter; the next sync of each type applies it. Nil (a server
    /// without the filter) takes every app.
    public func setSourceFilter(_ filter: SourceFilter?) {
        anchors.sourceFilter = filter
    }

    /// The apps that wrote each of `types` (activity summaries have no source), with the end of each
    /// type's newest sample, merged per bundle id. A Watch extension whose parent app is also found is
    /// merged into the parent; otherwise it is listed on its own.
    public func discoverSources(_ types: [HealthType]) async throws -> [DiscoveredSource] {
        var found: [String: (name: String, types: [String: Date?])] = [:]
        for type in types {
            guard let sampleType = type.sampleType else { continue }
            for source in try await store.sources(for: sampleType) {
                let last = try await store.lastSampleDate(of: sampleType, from: source)
                found[source.bundleID, default: (source.name, [:])].types[type.id] = last
            }
        }
        for bundleID in found.keys.sorted() {
            guard let parent = SourceFilter.parent(of: bundleID), found[parent] != nil, let child = found.removeValue(forKey: bundleID) else {
                continue
            }
            for (type, last) in child.types {
                let existing = found[parent]!.types[type] ?? nil
                found[parent]!.types[type] = [existing, last].compactMap { $0 }.max()
            }
        }
        return found.keys.sorted().map { bundleID in
            let source = found[bundleID]!
            return DiscoveredSource(bundleID: bundleID, name: source.name,
                                    types: source.types.keys.sorted().map { .init(type: $0, lastSampleAt: source.types[$0] ?? nil) })
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
