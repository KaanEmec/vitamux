import CoreGraphics
import Foundation
import SwiftUI
import Testing
@testable import VitamuxKit

/// The cases of fixtures/chart-grammar.json, shared with web/e2e/chart-grammar.spec.ts.
private struct GrammarFixture: Decodable {
    struct Case: Decodable {
        struct Expect: Decodable {
            var view: String
            var day: Components.Schemas.Intraday?
        }

        var note: String
        var metric: Components.Schemas.Metric
        var expect: Expect
    }

    var synthetic: Bool
    var cases: [Case]

    static func load() throws -> GrammarFixture {
        let url = URL(filePath: #filePath).deletingLastPathComponent().appending(path: "../../../../fixtures/chart-grammar.json").standardized
        return try JSONDecoder().decode(GrammarFixture.self, from: Data(contentsOf: url))
    }
}

struct ChartGrammarTests {
    @Test func `the shared fixture picks every view and day bucket`() throws {
        let fixture = try GrammarFixture.load()
        #expect(fixture.synthetic)
        for c in fixture.cases {
            let spec = chartFor(c.metric)
            #expect(spec.view.rawValue == c.expect.view, "\(c.metric.code): \(c.note)")
            #expect(spec.day == c.expect.day, "\(c.metric.code): \(c.note)")
        }
        // Every view, each intraday default, and metrics without intraday are covered.
        #expect(Set(fixture.cases.map(\.expect.view)) == Set(ChartView.allCases.map(\.rawValue)))
        #expect(Set(fixture.cases.compactMap(\.expect.day?._default)) == Set(Components.Schemas.Intraday.DefaultPayload.allCases))
        #expect(fixture.cases.contains { $0.expect.day == nil && $0.metric.aggregation == .intensive })
    }
}

struct DecimationTests {
    private let day = ChartSamples.intraday()
    private var domain: ClosedRange<Date> { day.first!.x ... day.last!.x }

    @Test func `a 14,400-point day keeps two points per column`() {
        #expect(day.count == 14_400)
        let drawn = decimate(day, over: domain, columns: 1_170)
        #expect(drawn.count <= 2 * 1_170 + 2)
        #expect(drawn.count > 1_170)
        #expect(zip(drawn, drawn.dropFirst()).allSatisfy { $0.x <= $1.x })
    }

    @Test func `extremes survive and the gap stays a gap`() {
        let drawn = decimate(day, over: domain, columns: 300)
        let values = day.compactMap(\.y)
        #expect(drawn.map(\.y).min() == values.min())
        #expect(drawn.map(\.y).max() == values.max())
        // The 13:00–14:00 gap splits the line into two segments and nothing is drawn inside it.
        #expect(Set(drawn.map(\.segment)) == [0, 1])
        let gap = ChartSamples.dayStart.addingTimeInterval(13 * 3_600) ..< ChartSamples.dayStart.addingTimeInterval(14 * 3_600)
        #expect(!drawn.contains { gap.contains($0.x) })
    }

    @Test func `short series are drawn as they are`() {
        let daily = ChartSamples.daily()
        let drawn = decimate(daily, over: timeDomain(daily.map(\.x)), columns: 64)
        #expect(drawn.count == daily.compactMap(\.y).count)
        #expect(Set(drawn.map(\.segment)).count == 2)
    }

    @Test func `step rows hold each reading until the next`() {
        let rows = stepRows([DrawnPoint(x: .init(timeIntervalSince1970: 0), y: 1, segment: 0), DrawnPoint(x: .init(timeIntervalSince1970: 10), y: 2, segment: 0), DrawnPoint(x: .init(timeIntervalSince1970: 20), y: 3, segment: 1)])
        #expect(rows.map(\.y) == [1, 1, 2, 3])
    }
}

struct ChartModelTests {
    @Test func `partial wins over direct and calculated, not over overridden`() {
        #expect(DataStatus(status: "direct", partial: true) == .partial)
        #expect(DataStatus(status: "calculated", partial: true) == .partial)
        #expect(DataStatus(status: "overridden", partial: true) == .overridden)
        #expect(DataStatus(status: "something_new") == .noData)
    }

    @Test func `holes run from the value before to the value after`() {
        let points = ChartSamples.daily()
        let gaps = holes(in: points)
        #expect(gaps.count == 1)
        #expect(gaps[0].lowerBound == points[14].x && gaps[0].upperBound == points[16].x)
    }

    @Test func `range presets end on the end date`() {
        let end = LocalDate("2026-10-04")!
        #expect(ChartRange.week.start(endingOn: end) == LocalDate("2026-09-28"))
        #expect(ChartRange.all.start(endingOn: end) == nil)
    }

    @Test func `All reads weekly rollups when the data starts within two years`() {
        let end = LocalDate("2026-10-04")!
        #expect(TrendRollup.query(endingOn: end).grain == .month)
        let bucket = { (start: String, n: Int) in
            Components.Schemas.Rollup(startDate: start, endDate: start, days: 30, n: n, coverage: Double(n) / 30, mean: n > 0 ? 60 : nil, min: n > 0 ? 50 : nil, max: n > 0 ? 70 : nil)
        }
        let trend = Components.Schemas.ResolvedTrend(metric: "heart_rate", grain: .month, timezone: "Europe/Berlin", startDate: "2016-10-07", endDate: "2026-10-04", buckets: [bucket("2025-01-01", 0), bucket("2025-02-01", 20), bucket("2025-03-01", 30)])
        #expect(TrendRollup.refinement(of: trend, endingOn: end)?.start == LocalDate("2025-02-01"))
        let chart = TrendRollup.chart(trend, timeZone: ChartSamples.timeZone)
        #expect(chart?.series.label == "Monthly mean")
        #expect(chart?.series.points.count == 2)
        #expect(chart?.bin == .month)
    }

    @Test func `grammar hues name the metric`() {
        #expect(MetricHue.of(code: "heart_rate", section: "Heart and circulation") == .heartRate)
        #expect(MetricHue.of(code: "hrv_rmssd", section: "Heart and circulation") == .hrv)
        #expect(MetricHue.of(code: "active_energy", section: "Activity") == .activeEnergy)
        #expect(MetricHue.of(code: "bp_systolic", section: "Blood pressure") == .bloodPressure)
    }
}

/// Every view renders on synthetic data in light and dark and at the default and largest
/// Dynamic Type sizes (a smoke test: something non-blank is drawn and nothing traps).
@MainActor
struct ChartRenderingTests {
    @Test(arguments: [ColorScheme.light, .dark], [DynamicTypeSize.large, .accessibility5])
    func `every view renders in both schemes and at the largest type size`(scheme: ColorScheme, size: DynamicTypeSize) throws {
        #expect(ChartSamples.gallery.count == 11)
        for (name, view) in ChartSamples.gallery {
            let renderer = ImageRenderer(content: view
                .padding()
                .frame(width: 390)
                .background(scheme == .dark ? Color.black : Color.white)
                .environment(\.colorScheme, scheme)
                .environment(\.dynamicTypeSize, size))
            renderer.scale = 1
            let image = try #require(renderer.cgImage, "\(name) \(scheme) \(size)")
            #expect(image.width == 390)
            #expect(colours(in: image) > 2, "\(name) \(scheme) \(size) drew nothing")
        }
    }

    /// Distinct colours on a coarse grid of the image.
    private func colours(in image: CGImage) -> Int {
        let width = image.width
        let height = image.height
        var pixels = [UInt8](repeating: 0, count: width * height * 4)
        let drawn = pixels.withUnsafeMutableBytes { buffer -> Bool in
            guard let context = CGContext(data: buffer.baseAddress, width: width, height: height, bitsPerComponent: 8, bytesPerRow: width * 4,
                                          space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return false }
            context.draw(image, in: CGRect(x: 0, y: 0, width: width, height: height))
            return true
        }
        guard drawn else { return 0 }
        var seen = Set<UInt32>()
        for y in stride(from: 0, to: height, by: 3) {
            for x in stride(from: 0, to: width, by: 3) {
                let i = (y * width + x) * 4
                seen.insert(UInt32(pixels[i]) << 16 | UInt32(pixels[i + 1]) << 8 | UInt32(pixels[i + 2]))
            }
        }
        return seen.count
    }
}
