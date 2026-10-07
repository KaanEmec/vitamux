import XCTest

/// Explore, metric detail and the all-sources day on the fake server (FakeServer+Explore.swift).
@MainActor
final class ExploreUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    /// The fake's catalogue (`GET /metrics`): every code must open a view without an error.
    static let catalogue = [
        ("heart_rate", "Heart rate"), ("resting_heart_rate", "Resting heart rate"), ("steps", "Steps"), ("body_mass", "Body mass"),
        ("spo2", "Spo2"), ("hrv_rmssd_nightly", "Hrv rmssd nightly"), ("bp_systolic", "Bp systolic"), ("sleep_total", "Sleep total"),
        ("vo2max", "Vo2max"),
    ]

    func testExploreListsSectionsFiltersAndEmptyMetrics() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.tabBars.buttons["Explore"].tap()
        XCTAssertTrue(app.buttons["exploreRow-resting_heart_rate"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.element("aggregatesPending").exists, "the catching-up note shows while aggregates rebuild")

        app.buttons["sourceFilter-withings"].tap()
        XCTAssertTrue(app.buttons["exploreRow-body_mass"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["exploreRow-resting_heart_rate"].exists, "Withings has no resting heart rate")
        app.buttons["sourceFilter-all"].tap()
        XCTAssertTrue(app.buttons["exploreRow-resting_heart_rate"].waitForExistence(timeout: 5))

        XCTAssertFalse(app.buttons["exploreRow-vo2max"].exists)
        app.scrollTo(app.switches["showEmptyToggle"]).switches.firstMatch.tap()
        XCTAssertTrue(app.scrollTo(app.buttons["exploreRow-vo2max"]).waitForExistence(timeout: 10), "catalogue metrics without data are listed")
    }

    func testSearchMatchesNamesAndAnalytes() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.tabBars.buttons["Explore"].tap()
        XCTAssertTrue(app.buttons["exploreRow-resting_heart_rate"].waitForExistence(timeout: 10))
        let field = app.searchFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5), app.debugDescription)
        field.tap()
        field.typeText("ldl")
        XCTAssertTrue(app.buttons["exploreRow-ldl_c"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["exploreRow-resting_heart_rate"].exists)
    }

    func testPinAndUnpin() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.tabBars.buttons["Explore"].tap()
        let row = app.scrollTo(app.buttons["exploreRow-body_mass"])
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        row.swipeLeft()
        let pin = app.buttons["pin-body_mass"]
        XCTAssertTrue(pin.waitForExistence(timeout: 5))
        XCTAssertEqual(pin.label, "Pin")
        pin.tap()
        row.swipeLeft()
        XCTAssertTrue(pin.waitForExistence(timeout: 5))
        XCTAssertEqual(pin.label, "Unpin", "body_mass is on the dashboard now")
        pin.tap()
        row.swipeLeft()
        XCTAssertEqual(app.buttons["pin-body_mass"].label, "Pin")
    }

    func testSpecialisedItemsOpenTheirViews() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.tabBars.buttons["Explore"].tap()
        // In list order, so each row is further down than the last.
        for (code, title) in [("workouts", "Workouts"), ("bp_reading", "Blood pressure"), ("sleep", "Sleep"), ("irregular_rhythm", "Events"), ("ldl_c", "Analyte history")] {
            let row = app.scrollTo(app.buttons["exploreRow-\(code)"])
            XCTAssertTrue(row.waitForExistence(timeout: 10), code)
            row.tap()
            XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: 5), code)
            app.navigationBars.buttons.element(boundBy: 0).tap()
            XCTAssertTrue(app.navigationBars["Explore"].waitForExistence(timeout: 5))
        }
    }

    func testEveryCatalogueMetricOpensWithoutError() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        let yesterday = Fake.day(-1)
        for (code, title) in Self.catalogue {
            app.openLink("vitamux://explore/\(code)")
            XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: 10), code)
            let plotted = app.element("metricChart").waitForExistence(timeout: 10) || app.element("noValues").exists
            XCTAssertTrue(plotted, "\(code) draws its chart")
            app.assertNoProblem(code)

            app.openLink("vitamux://explore/\(code)/day/\(yesterday)")
            XCTAssertTrue(app.staticTexts["pointValue"].waitForExistence(timeout: 10), "\(code) day shows its resolved value")
            app.assertNoProblem("\(code) day")
        }
    }

    func testRollupDrillDown() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.openLink("vitamux://explore/resting_heart_rate?range=All")
        XCTAssertTrue(app.navigationBars["Resting heart rate"].waitForExistence(timeout: 10))
        let rollup = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'rollupRow-'")).firstMatch
        XCTAssertTrue(app.scrollTo(rollup).waitForExistence(timeout: 10), "All plots rollups")
        rollup.tap()
        let day = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'valueRow-'")).firstMatch
        XCTAssertTrue(day.waitForExistence(timeout: 10), "the rollup opens its days")
        for _ in 0..<8 where !app.buttons["1W"].exists { app.swipeDown() }
        let picked = app.buttons["1W"].isSelected || app.buttons["1M"].isSelected
        XCTAssertTrue(picked, "the range is the rollup's week or month")
    }

    func testSourceAndCoverageToggles() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.openLink("vitamux://explore/resting_heart_rate?range=1M")
        let garmin = app.element("toggleSource-garmin")
        XCTAssertTrue(garmin.waitForExistence(timeout: 10))
        garmin.tap()
        XCTAssertEqual(garmin.value as? String, "1", "Garmin's own series is on")
        app.element("toggleCoverage").tap()
        XCTAssertTrue(app.element("coverageStrip").waitForExistence(timeout: 10))
        app.assertNoProblem("toggles")
        app.scrollTo(app.buttons["editRule"]).tap()
        XCTAssertTrue(app.navigationBars["How it's calculated"].waitForExistence(timeout: 5), "Edit rule opens the rule lens (J22.10)")
    }
}

extension XCUIApplication {
    /// No failed load on screen: the titles `Problem` gives a decoding or transport failure and
    /// the fake's problem titles.
    func assertNoProblem(_ context: String, file: StaticString = #filePath, line: UInt = #line) {
        for title in ["Unexpected response", "Something went wrong", "Can't reach the server", "Not found", "Validation failed"] {
            XCTAssertFalse(staticTexts[title].exists, "\(context): \(title)", file: file, line: line)
        }
    }
}
