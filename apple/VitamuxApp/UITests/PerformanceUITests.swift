import XCTest

/// Drives the screens behind the performance marks (J22.22) on the fake server: the dashboard's
/// load, then heart rate's Day view on a 14,400-row day (the watch every 6 s,
/// FakeServer+Intraday.swift) with both sources drawn, at every zoom rung. The app emits
/// `dashboardReady` and `chartRender` signposts; `scripts/perf-signposts.sh` reads them from the
/// simulator's log and checks the budgets, so this test only asserts that each screen showed.
@MainActor
final class PerformanceUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    /// Runs twice: a cold launch and a warm one.
    func testDashboardAndHeartRateDay() {
        for _ in 0 ..< 2 {
            let app = XCUIApplication.launch()
            app.signInToDashboard()
            XCTAssertTrue(app.buttons["card-steps"].waitForExistence(timeout: 10), "the dashboard is ready")

            app.openLink("vitamux://explore/heart_rate?range=1D&end=\(Fake.day(-1))")
            XCTAssertTrue(app.staticTexts["dayStep"].waitForExistence(timeout: 10), "heart rate opens the Day view")
            waitForStep(app, "1-minute buckets")
            // The source toggles sit below the chart. One swipe each way: a scroll loop's repeated
            // queries of this many chart elements flood the log and push out the signposts.
            app.swipeUp()
            for provider in ["apple_health", "garmin"] {
                let toggle = app.descendants(matching: .any)["toggleSource-\(provider)"].firstMatch
                XCTAssertTrue(toggle.waitForExistence(timeout: 10), provider)
                toggle.tap()
            }
            app.swipeDown()
            app.buttons["dayZoomIn"].tap()
            waitForStep(app, "30-second buckets")
            app.buttons["dayZoomIn"].tap()
            waitForStep(app, "Raw readings")
            let raw = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS 'raw readings'")).firstMatch
            XCTAssertTrue(raw.waitForExistence(timeout: 10))
            app.buttons["dayZoomOut"].tap()
            app.buttons["dayZoomOut"].tap()
            waitForStep(app, "1-minute buckets")
            app.terminate()
        }
    }

    private func waitForStep(_ app: XCUIApplication, _ label: String, file: StaticString = #filePath, line: UInt = #line) {
        let step = app.staticTexts["dayStep"]
        let matched = NSPredicate(format: "label == %@", label)
        XCTAssertEqual(XCTWaiter.wait(for: [expectation(for: matched, evaluatedWith: step)], timeout: 10), .completed, "step is \(label), not \(step.label)", file: file, line: line)
    }
}
