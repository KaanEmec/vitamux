import XCTest

/// The Day view of metric detail (J22.26) on the fake server's intraday fixtures
/// (FakeServer+Intraday.swift): the Day tab, zoom levels, overlays, the bucket sheet, the table
/// fallback and the drill from a weekly chart.
@MainActor
final class DayViewUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    private func openDay(_ app: XCUIApplication, _ code: String, _ date: String = Fake.day(-1)) {
        app.openLink("vitamux://explore/\(code)?range=1D&end=\(date)")
        XCTAssertTrue(app.staticTexts["dayStep"].waitForExistence(timeout: 10), "\(code) opens the Day view")
    }

    private func waitForStep(_ app: XCUIApplication, _ label: String, file: StaticString = #filePath, line: UInt = #line) {
        let step = app.staticTexts["dayStep"]
        let matched = NSPredicate(format: "label == %@", label)
        XCTAssertEqual(XCTWaiter.wait(for: [expectation(for: matched, evaluatedWith: step)], timeout: 10), .completed, "step is \(label), not \(step.label)", file: file, line: line)
    }

    /// The plot: the chart element the Day chart labels with its title.
    private func plot(_ app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS ' on ' AND (label CONTAINS 'buckets' OR label CONTAINS 'readings')")).firstMatch
    }

    func testOnlyMetricsWithIntradayHaveADayTab() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.openLink("vitamux://explore/resting_heart_rate?range=1D")
        XCTAssertTrue(app.buttons["3M"].waitForExistence(timeout: 10))
        let fellBack = NSPredicate(format: "isSelected == true")
        XCTAssertEqual(XCTWaiter.wait(for: [expectation(for: fellBack, evaluatedWith: app.buttons["3M"])], timeout: 10), .completed, "a Day link falls back to the default range")
        XCTAssertFalse(app.buttons["1D"].exists, "a daily summary has no Day view")
        XCTAssertFalse(app.staticTexts["dayStep"].exists)
        for code in ["heart_rate", "steps", "spo2"] {
            // Through the tab root, so each link opens a fresh detail screen.
            app.openLink("vitamux://explore")
            XCTAssertTrue(app.navigationBars["Explore"].waitForExistence(timeout: 10))
            app.openLink("vitamux://explore/\(code)")
            XCTAssertTrue(app.buttons["1D"].waitForExistence(timeout: 10), code)
            app.buttons["1D"].tap()
            XCTAssertTrue(app.staticTexts["dayStep"].waitForExistence(timeout: 10), code)
            XCTAssertTrue(plot(app).waitForExistence(timeout: 10), "\(code) draws its day")
            app.assertNoProblem("\(code) day")
        }
    }

    func testHeartRateZoomsFromMinutesToRawReadings() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        openDay(app, "heart_rate")
        waitForStep(app, "1-minute buckets")
        XCTAssertTrue(plot(app).waitForExistence(timeout: 10))
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH 'Night '")).firstMatch.exists, "the night is shaded")

        app.buttons["dayZoomIn"].tap()
        waitForStep(app, "30-second buckets")
        app.buttons["dayZoomIn"].tap()
        waitForStep(app, "Raw readings")
        XCTAssertFalse(app.buttons["dayZoomIn"].isEnabled, "raw is the finest step")
        // The chart keeps the 30-second layer until every raw page has arrived (14,400 rows on a
        // hosted runner take a while): wait for the loading indicator to clear before tapping.
        XCTAssertTrue(app.activityIndicators.firstMatch.waitForNonExistence(timeout: 120), "the raw layer finished loading")
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS 'raw readings'")).firstMatch.waitForExistence(timeout: 10))
        sleep(1)
        plot(app).tap()
        XCTAssertTrue(app.staticTexts["bucketValue"].waitForExistence(timeout: 10), "a tap opens the bucket")
        XCTAssertTrue(app.staticTexts["bucketSpan"].label.contains("30-second bucket"))
        XCTAssertTrue(app.staticTexts["bucketSource"].label.hasPrefix("From "))
        XCTAssertTrue(app.staticTexts["bucketExplanation"].label.contains("readings"))
        // The readings follow the bucket's summary: at the half-height detent the list has not
        // laid their rows out (on a hosted runner the "Readings" header sits on the sheet's edge),
        // so raise the sheet to full height first.
        app.staticTexts["bucketExplanation"].swipeUp()
        XCTAssertTrue(app.descendants(matching: .any)["reading"].firstMatch.waitForExistence(timeout: 15), "raw zoom lists the readings with time, device and origin")
        app.buttons["closeBucket"].tap()

        app.buttons["dayZoomOut"].tap()
        waitForStep(app, "30-second buckets")
        app.buttons["dayZoomOut"].tap()
        waitForStep(app, "1-minute buckets")
        app.assertNoProblem("zoom")
    }

    func testTodayShowsNightWorkoutsAndNow() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        openDay(app, "heart_rate", Fake.day(0))
        let any = app.descendants(matching: .any)
        XCTAssertTrue(any.matching(NSPredicate(format: "label BEGINSWITH 'Now '")).firstMatch.waitForExistence(timeout: 10), "a now line on today")
        XCTAssertTrue(any.matching(NSPredicate(format: "label BEGINSWITH 'Running '")).firstMatch.exists, "today's run is a band")
        XCTAssertTrue(any.matching(NSPredicate(format: "label BEGINSWITH 'Night '")).firstMatch.exists)
        // The date stepper: the day before has no now line.
        app.buttons["Earlier"].tap()
        XCTAssertTrue(any.matching(NSPredicate(format: "label BEGINSWITH 'Now '")).firstMatch.waitForNonExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["dayStep"].exists)
    }

    func testStepsBarsZoomByPinchAndShowAsTable() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        openDay(app, "steps")
        waitForStep(app, "30-minute buckets")
        let chart = plot(app)
        XCTAssertTrue(chart.waitForExistence(timeout: 10))
        chart.pinch(withScale: 6, velocity: 1)
        let finer = NSPredicate(format: "label == '15-minute buckets' OR label == '5-minute buckets'")
        XCTAssertEqual(XCTWaiter.wait(for: [expectation(for: finer, evaluatedWith: app.staticTexts["dayStep"])], timeout: 10), .completed, "a pinch zooms in")
        for _ in 0 ..< 3 where app.staticTexts["dayStep"].label != "30-minute buckets" {
            app.buttons["dayZoomOut"].tap()
            sleep(1)
        }
        waitForStep(app, "30-minute buckets")

        app.buttons["Show as table"].firstMatch.tap()
        XCTAssertTrue(app.buttons["Show as chart"].waitForExistence(timeout: 5), "the buckets as a table")
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS 'table'")).firstMatch.exists)
        app.buttons["Show as chart"].tap()
        XCTAssertTrue(plot(app).waitForExistence(timeout: 5))
    }

    func testWeeklyChartDrillsIntoTheDay() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.openLink("vitamux://explore/heart_rate?range=1W")
        let chart = app.descendants(matching: .any).matching(NSPredicate(format: "label == 'Heart rate, resolved per day'")).firstMatch
        XCTAssertTrue(chart.waitForExistence(timeout: 10))
        chart.tap()
        XCTAssertTrue(app.staticTexts["dayStep"].waitForExistence(timeout: 10), "a tapped day opens its Day view")
        XCTAssertTrue(app.buttons["1D"].isSelected)
    }

    func testSleepRelatedMetricOpensOnTheNight() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        openDay(app, "spo2")
        XCTAssertTrue(app.staticTexts["dayNight"].waitForExistence(timeout: 10), "SpO2 opens centred on the night")
        waitForStep(app, "5-minute buckets")
        XCTAssertTrue(plot(app).waitForExistence(timeout: 10))
        app.assertNoProblem("spo2 day")
    }
}
