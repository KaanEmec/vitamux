import Foundation
import os

/// Performance marks (J22.22): intervals in Instruments' Points of Interest, read on the simulator
/// by `scripts/perf-signposts.sh`. `dashboardReady` spans a dashboard load until every card has
/// its summary; `chartRender` spans a Day-view layer from arriving in the model to its frame being
/// committed. Names and point counts only, never values.
enum Signposts {
    static let signposter = OSSignposter(subsystem: "org.vitamux.app", category: .pointsOfInterest)
    private static var chart: OSSignpostIntervalState?

    /// A chart layer of `points` loaded points is about to be drawn.
    static func chartWillRender(points: Int) {
        if let chart { signposter.endInterval("chartRender", chart, "superseded") }
        chart = signposter.beginInterval("chartRender", id: signposter.makeSignpostID(), "points \(points)")
    }

    /// Called as the chart appears or changes layer; the frame being built is committed before
    /// the main queue's next turn, where the interval ends.
    static func chartDidRender() {
        guard let state = chart else { return }
        chart = nil
        DispatchQueue.main.async { signposter.endInterval("chartRender", state) }
    }
}
