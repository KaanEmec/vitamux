import Foundation
import XCTest
@testable import HealthBridgeCore

/// The same rules as the server's internal/sourcefilter; synthetic bundle ids only.
final class SourceFilterTests: XCTestCase {
    let band = "com.example.synthetic.band", scale = "com.example.synthetic.scale"
    let watch = "com.apple.health.00000000-0000-4000-8000-0000000000aa"
    let steps = "HKQuantityTypeIdentifierStepCount"

    var filter: SourceFilter {
        SourceFilter(version: 3, origins: [
            .init(bundleID: scale, name: "Synthetic Scale", mode: .ignore),
            .init(bundleID: "com.example.synthetic.ring", mode: .perType, types: [hrType]),
        ], defaultIgnore: [.init(originPattern: "com.example.synthetic.band%", provider: "synthetic_band", providerName: "Synthetic Band Cloud")])
    }

    func testExplicitIgnore() {
        let d = filter.decision(for: scale)
        XCTAssertEqual(d.mode, .ignore)
        XCTAssertTrue(d.explicit)
        XCTAssertEqual(d.defaultMode, .take)
        XCTAssertNil(d.reason)
        XCTAssertFalse(filter.takes(scale, type: hrType))
    }

    func testPerTypeTakesOnlyListedTypes() {
        XCTAssertTrue(filter.takes("com.example.synthetic.ring", type: hrType))
        XCTAssertFalse(filter.takes("com.example.synthetic.ring", type: steps))
        XCTAssertEqual(filter.decision(for: "com.example.synthetic.ring").types, [hrType])
    }

    func testDefaultIgnorePatternWithReason() {
        let d = filter.decision(for: band)
        XCTAssertEqual(d, SourceDecision(mode: .ignore, defaultMode: .ignore, reason: .directConnection, provider: "synthetic_band",
                                         providerName: "Synthetic Band Cloud"))
        XCTAssertFalse(filter.takes(band, type: hrType))
    }

    func testWatchExtensionFollowsParent() {
        // The parent's default pattern applies to its Watch extension.
        XCTAssertEqual(filter.decision(for: band + ".watchkitapp").mode, .ignore)
        XCTAssertEqual(filter.decision(for: band + ".watchkitapp").reason, .directConnection)
        // So does the parent's explicit choice, and the extension's own choice wins.
        XCTAssertEqual(filter.decision(for: scale + ".watchkitapp.watchkitextension").mode, .ignore)
        XCTAssertTrue(filter.decision(for: scale + ".watchkitapp.watchkitextension").explicit)
        var own = filter
        own.origins.append(.init(bundleID: scale + ".watchkitapp", mode: .take))
        XCTAssertEqual(own.decision(for: scale + ".watchkitapp").mode, .take)
    }

    func testNativeTakenDespiteMatchingPattern() {
        let everything = SourceFilter(version: 1, defaultIgnore: [.init(originPattern: "%", provider: "p", providerName: "P")])
        XCTAssertEqual(everything.decision(for: watch), SourceDecision(defaultMode: .take, reason: .native))
        XCTAssertEqual(everything.decision(for: band).mode, .ignore)
        // An explicit choice still wins over the native default.
        let ignored = SourceFilter(version: 2, origins: [.init(bundleID: watch, mode: .ignore)])
        XCTAssertEqual(ignored.decision(for: watch).mode, .ignore)
        XCTAssertEqual(ignored.decision(for: watch).defaultMode, .take)
    }

    func testUnknownSourceIsTaken() {
        let d = filter.decision(for: "org.example.never.seen")
        XCTAssertEqual(d, SourceDecision(defaultMode: .take))
        XCTAssertTrue(filter.takes("org.example.never.seen", type: steps))
        XCTAssertTrue(SourceFilter(version: 1).takes(band, type: hrType))
    }

    func testParent() {
        XCTAssertEqual(SourceFilter.parent(of: "a.b.watchkitapp.watchkitextension"), "a.b")
        XCTAssertEqual(SourceFilter.parent(of: "a.b.watchkitextension"), "a.b")
        XCTAssertEqual(SourceFilter.parent(of: "a.b.watchkitapp"), "a.b")
        XCTAssertEqual(SourceFilter.parent(of: "a.b.watchextension"), "a.b")
        XCTAssertEqual(SourceFilter.parent(of: "a.b.watchapp"), "a.b")
        XCTAssertNil(SourceFilter.parent(of: "a.b"))
        XCTAssertNil(SourceFilter.parent(of: ".watchapp"))
    }

    func testLike() {
        XCTAssertTrue(SourceFilter.like("com.example.%", "com.example.band"))
        XCTAssertTrue(SourceFilter.like("%band", "com.example.band"))
        XCTAssertTrue(SourceFilter.like("com._xample.band", "com.example.band"))
        XCTAssertFalse(SourceFilter.like("com._xample.band", "com.xample.band"))
        XCTAssertTrue(SourceFilter.like("100\\%", "100%"))
        XCTAssertFalse(SourceFilter.like("100\\%", "1000"))
        XCTAssertTrue(SourceFilter.like("a\\_b", "a_b"))
        XCTAssertFalse(SourceFilter.like("a\\_b", "axb"))
        XCTAssertFalse(SourceFilter.like("com.example", "com.example.band"))
        XCTAssertTrue(SourceFilter.like("%%", ""))
        XCTAssertFalse(SourceFilter.like("_", ""))
    }

    func testDeviceSelfDecodesWithAndWithoutSourceFilter() throws {
        let old = #"{"device_id":"d","connection_id":"c","name":"Synthetic iPhone","anchor_resets":[]}"#
        XCTAssertNil(try JSONDecoder().decode(DeviceSelf.self, from: Data(old.utf8)).sourceFilter)

        let new = #"""
        {"device_id":"d","connection_id":"c","name":"Synthetic iPhone","anchor_resets":[],
         "source_filter":{"version":4,"origins":[{"bundle_id":"com.example.synthetic.scale","name":"Synthetic Scale","mode":"ignore"},
           {"bundle_id":"com.example.synthetic.ring","mode":"per_type","types":["HKQuantityTypeIdentifierHeartRate"]}],
          "default_ignore":[{"origin_pattern":"com.example.synthetic.band%","provider":"synthetic_band","provider_name":"Synthetic Band Cloud"}]}}
        """#
        let me = try JSONDecoder().decode(DeviceSelf.self, from: Data(new.utf8))
        XCTAssertEqual(me.sourceFilter, SourceFilter(version: 4, origins: filter.origins, defaultIgnore: filter.defaultIgnore))
    }

    func testAnchorStoreKeepsFilterAndExclusionsUnderAnchorPrefix() throws {
        let anchors = freshAnchors()
        XCTAssertNil(anchors.sourceFilter)
        anchors.sourceFilter = filter
        anchors.setExcluded([band, scale], for: hrType)
        XCTAssertEqual(anchors.sourceFilter, filter)
        XCTAssertEqual(anchors.excluded(for: hrType), [band, scale])
        XCTAssertEqual(anchors.excluded(for: steps), [])

        let keys = anchors.defaults.dictionaryRepresentation().keys.filter { $0.hasPrefix("anchor.") }
        XCTAssertEqual(Set(keys), ["anchor.filter", "anchor.excluded." + hrType])
        anchors.setExcluded([], for: hrType)
        anchors.sourceFilter = nil
        XCTAssertNil(anchors.defaults.object(forKey: "anchor.filter"))
        XCTAssertNil(anchors.defaults.object(forKey: "anchor.excluded." + hrType))
    }

    func testReportSourcesBodyShape() async throws {
        let server = FakeServer(statuses: [204])
        let last = ISO8601DateFormatter().date(from: "2026-09-30T06:00:00Z")!
        try await server.client().reportSources([
            DiscoveredSource(bundleID: band, name: "Synthetic Band", types: [.init(type: hrType, lastSampleAt: last), .init(type: steps)]),
            DiscoveredSource(bundleID: watch, name: nil, types: []),
        ], credentials: credentials)

        let request = try XCTUnwrap(server.requests.first)
        XCTAssertEqual(request.httpMethod, "PUT")
        XCTAssertEqual(request.url?.path, "/api/ingest/v1/devices/self/sources")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer \(credentials.token)")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Content-Type"), "application/json")
        let body = try XCTUnwrap(JSONSerialization.jsonObject(with: request.httpBody!) as? [String: Any])
        let sources = try XCTUnwrap(body["sources"] as? [[String: Any]])
        XCTAssertEqual(sources.count, 2)
        XCTAssertEqual(sources[0]["bundle_id"] as? String, band)
        XCTAssertEqual(sources[0]["name"] as? String, "Synthetic Band")
        let types = try XCTUnwrap(sources[0]["types"] as? [[String: String]])
        XCTAssertEqual(types[0]["type"], hrType)
        XCTAssertEqual(types[0]["last_sample_at"].flatMap { try? Date($0, strategy: .iso8601) }, last)
        XCTAssertEqual(types[1], ["type": steps])
        XCTAssertNil(sources[1]["name"])
        XCTAssertEqual((sources[1]["types"] as? [Any])?.count, 0)
    }

    func testReportSourcesRejectedThrows() async throws {
        let server = FakeServer(statuses: [422])
        do {
            try await server.client().reportSources([], credentials: credentials)
            XCTFail("422 did not throw")
        } catch {
            XCTAssertEqual(error as? BridgeError, .rejected(status: 422))
        }
    }
}
