import Foundation
import HealthBridgeCore
import HealthKit

/// Anchored incremental sync of enabled types: page through each type, upload, commit the anchor
/// after the server accepted the page, and repeat until a short page.
public actor HealthSync {
    public static let pageLimit = Batcher.maxSamples

    let store: HealthStore
    let sender: BatchSender
    let anchors: AnchorStore
    let backfillStart: Date
    var running: Set<String> = []

    /// `backfillStart`: the earliest date to pull; HealthKit's earliest permitted date wins if later.
    public init(store: HealthStore, sender: BatchSender, anchors: AnchorStore, backfillStart: Date = .distantPast) {
        self.store = store
        self.sender = sender
        self.anchors = anchors
        self.backfillStart = backfillStart
    }

    /// Asks for read access to every type of `groups`. HealthKit never reveals a denial.
    public func requestAuthorization(for groups: Set<MetricGroup>) async throws {
        try await store.requestReadAuthorization(Registry.types(in: groups).reduce(into: []) { $0.formUnion($1.readTypes) })
    }

    /// Syncs one type. A concurrent call for the same type returns at once; a type this OS does
    /// not know is skipped.
    public func sync(_ type: HealthType) async throws {
        guard let sampleType = type.sampleType, running.insert(type.id).inserted else { return }
        defer { running.remove(type.id) }
        let start = max(store.earliestPermittedSampleDate(), backfillStart)
        while true {
            let before = anchors.anchor(for: type.id)
            let startedAt = rfc3339(Date())
            let page = try await store.anchoredPage(of: sampleType, from: start, anchor: before, limit: Self.pageLimit)
            let body = SamplesPage(
                type: type.id,
                anchor: AnchorInfo(beforeHash: AnchorStore.hash(before), afterHash: AnchorStore.hash(page.anchor), queryStartedAt: startedAt),
                samples: page.samples.map { Mapping.sample($0, unit: type.unit) },
                deleted: page.deleted.map { Deleted(uuid: $0.uuidString) })
            try await sender.send(body, newAnchor: page.anchor)
            if page.samples.count + page.deleted.count < Self.pageLimit { return }
        }
    }

    /// Syncs each type in turn and returns the failures by type id.
    public func syncAll(_ types: [HealthType]) async -> [String: any Error] {
        var failures: [String: any Error] = [:]
        for type in types {
            do { try await sync(type) } catch { failures[type.id] = error }
        }
        return failures
    }

    /// Registers observer queries with background delivery. Each callback syncs its type and
    /// always completes, even on failure, so iOS keeps delivering.
    public func observe(_ types: [HealthType]) async throws {
        for type in types {
            guard let sampleType = type.sampleType else { continue }
            store.observe(sampleType) { done in
                Task {
                    defer { done() }
                    try? await self.sync(type)
                }
            }
            try await store.enableBackgroundDelivery(for: sampleType)
        }
    }

    /// Enabling a type or a server-requested reset: the next sync is a full pull.
    public func resetAnchor(_ type: HealthType) {
        anchors.reset(type.id)
    }
}
