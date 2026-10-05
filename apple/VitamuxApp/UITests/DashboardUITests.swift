import XCTest

/// The dashboard on the fake server (`FakeServer+Dashboard.swift`): the default layout, edit and
/// save, a past day, an empty install, and a layout the panel saved.
@MainActor
final class DashboardUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    func testDefaultLayout() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        let sleep = app.card("sleep")
        XCTAssertTrue(sleep.waitForExistence(timeout: 10))
        XCTAssertEqual(app.staticTexts["dayTitle"].label, "Today")
        XCTAssertTrue(app.staticTexts["dayLabel"].label.contains("Resolved values for Europe/Berlin"), app.staticTexts["dayLabel"].label)
        XCTAssertFalse(app.buttons["nextDay"].isEnabled, "no future days")

        let shown = ["sleep", "resting_heart_rate", "hrv_rmssd_nightly", "steps", "vo2max", "weight", "blood_pressure", "spo2",
                     "respiratory_rate", "active_energy", "total_energy"]
        for code in shown { XCTAssertTrue(app.card(code).exists, code) }
        XCTAssertFalse(app.card("hrv_sdnn").exists, "a card without data hides outside edit mode")
        app.assertOrder(shown)
        XCTAssertEqual(app.card("vo2max").frame.minY, app.card("weight").frame.minY, accuracy: 1, "S cards share a row")
        XCTAssertGreaterThan(app.card("steps").frame.width, app.card("weight").frame.width * 1.5, "M takes the whole width")

        XCTAssertTrue(app.card("steps").label.contains("Partial"), app.card("steps").label)
        XCTAssertTrue(app.card("resting_heart_rate").label.contains("vs 30-day mean"), app.card("resting_heart_rate").label)
        XCTAssertTrue(app.card("spo2").label.contains("Fallback"), app.card("spo2").label)
        XCTAssertTrue(app.card("blood_pressure").label.contains("pulse 58 bpm"), app.card("blood_pressure").label)
        XCTAssertTrue(app.card("sleep").label.contains("asleep"), app.card("sleep").label)

        XCTAssertTrue(app.staticTexts["Garmin Connect needs reauthorization."].exists)
        XCTAssertEqual(app.texts(beginningWith: "Job sync failed permanently").count, 1, "only this week's failed jobs")
        XCTAssertEqual(app.texts(beginningWith: "The last backup is from").count, 1)
        XCTAssertTrue(app.buttons["source-withings"].exists)
        XCTAssertTrue(app.buttons["source-garmin"].exists)

        app.card("resting_heart_rate").tap()
        XCTAssertTrue(app.navigationBars["Resting heart rate"].waitForExistence(timeout: 5), "a card opens Explore")
        XCTAssertTrue(app.tabBars.buttons["Explore"].isSelected)
    }

    func testEditSaveAndReload() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        XCTAssertTrue(app.card("steps").waitForExistence(timeout: 10))
        app.buttons["customizeButton"].tap()
        XCTAssertTrue(app.navigationBars["Customize"].waitForExistence(timeout: 5))

        for _ in 0..<2 {
            app.buttons["move-steps"].tap()
            app.buttons["Move up"].tap()
        }
        app.scrollTo(app.segmentedControls["size-weight"]).buttons["L"].tap()
        app.scrollTo(app.buttons["hide-vo2max"]).tap()
        XCTAssertTrue(app.scrollTo(app.buttons["show-vo2max"]).exists, "hidden cards wait below")
        app.scrollTo(app.buttons["addMetric"]).tap()
        let search = app.searchFields.firstMatch
        XCTAssertTrue(search.waitForExistence(timeout: 5))
        search.tap()
        search.typeText("sleep")
        XCTAssertTrue(app.buttons["pin-sleep"].waitForExistence(timeout: 5), "sleep stages are one entry")
        XCTAssertFalse(app.buttons["pin-sleep_deep"].exists)
        XCTAssertEqual(app.buttons["pin-sleep"].label, "Unpin Sleep", "on the layout")
        search.buttons["Clear text"].tap()
        search.typeText("body")
        app.buttons["pin-body_fat_ratio"].tap()
        XCTAssertEqual(app.buttons["pin-body_fat_ratio"].label, "Unpin Body fat ratio")
        if app.buttons["Close"].exists { app.buttons["Close"].tap() } // ends the search
        XCTAssertTrue(app.buttons["addMetricDone"].waitForExistence(timeout: 5))
        app.buttons["addMetricDone"].tap()
        app.buttons["editSave"].tap()
        XCTAssertTrue(app.navigationBars["Customize"].waitForNonExistence(timeout: 10))

        let saved = ["sleep", "resting_heart_rate", "steps", "hrv_rmssd_nightly", "weight", "blood_pressure"]
        app.assertOrder(saved)
        XCTAssertFalse(app.card("vo2max").exists)

        // Reload from the server: the session ends, the next request asks to sign in again, and a
        // fresh dashboard reads the saved layout.
        app.openLink("vitamux://uitest/expire-sessions")
        app.buttons["previousDay"].tap()
        app.signIn()
        XCTAssertTrue(app.card("steps").waitForExistence(timeout: 10))
        app.assertOrder(saved)
        XCTAssertFalse(app.card("vo2max").exists)
        XCTAssertGreaterThan(app.card("weight").frame.width, app.card("blood_pressure").frame.width * 1.5, "weight is L")

        app.buttons["customizeButton"].tap()
        XCTAssertTrue(app.scrollTo(app.element("edit-body_fat_ratio")).exists, "the pinned metric was saved")
        XCTAssertTrue(app.scrollTo(app.segmentedControls["size-weight"]).buttons["L"].isSelected)
        app.buttons["editCancel"].tap()
        XCTAssertTrue(app.navigationBars["Customize"].waitForNonExistence(timeout: 5))
    }

    func testResetAndCancel() {
        let app = XCUIApplication.launch("-uitest-panel-layout")
        app.signInToDashboard()
        XCTAssertTrue(app.card("steps").waitForExistence(timeout: 10))
        app.buttons["customizeButton"].tap()
        app.scrollTo(app.buttons["resetLayout"]).tap()
        app.buttons["editCancel"].tap()
        XCTAssertTrue(app.navigationBars["Customize"].waitForNonExistence(timeout: 5))
        XCTAssertFalse(app.card("resting_heart_rate").exists, "cancel keeps the saved layout")

        app.buttons["customizeButton"].tap()
        app.scrollTo(app.buttons["resetLayout"]).tap()
        app.buttons["editSave"].tap()
        XCTAssertTrue(app.navigationBars["Customize"].waitForNonExistence(timeout: 10))
        XCTAssertTrue(app.card("resting_heart_rate").waitForExistence(timeout: 10))
        app.assertOrder(["sleep", "resting_heart_rate", "steps", "vo2max"])
    }

    func testAPastDay() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        let steps = app.card("steps")
        XCTAssertTrue(steps.waitForExistence(timeout: 10))
        XCTAssertTrue(steps.label.contains("Partial"))

        app.buttons["previousDay"].tap()
        app.waitForLabel(of: app.staticTexts["dayTitle"], "Yesterday")
        XCTAssertTrue(app.buttons["todayButton"].exists)
        XCTAssertTrue(app.buttons["nextDay"].isEnabled)
        XCTAssertTrue(app.card("steps").waitForExistence(timeout: 10))
        XCTAssertFalse(app.card("steps").label.contains("Partial"), "a past day is final")

        app.buttons["nextDay"].tap()
        app.waitForLabel(of: app.staticTexts["dayTitle"], "Today")
        XCTAssertFalse(app.buttons["todayButton"].exists)

        app.openLink("vitamux://dashboard?date=2026-01-31")
        app.waitForLabel(of: app.staticTexts["dayTitle"], "Saturday")
        XCTAssertEqual(app.staticTexts["dayLabel"].value as? String, "2026-01-31")
        XCTAssertTrue(app.card("weight").waitForExistence(timeout: 10))
        app.buttons["todayButton"].tap()
        app.waitForLabel(of: app.staticTexts["dayTitle"], "Today")
    }

    func testAnEmptyInstall() {
        let app = XCUIApplication.launch("-uitest-empty-install")
        app.signInToDashboard()
        XCTAssertTrue(app.staticTexts["No data yet"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["Nothing needs your attention."].exists)
        XCTAssertTrue(app.buttons["connectSource"].exists, "the sources section offers to connect one")
        XCTAssertFalse(app.card("sleep").exists)
        app.buttons["emptyCardsAction"].tap()
        XCTAssertTrue(app.navigationBars["Sources"].waitForExistence(timeout: 5))
    }

    func testALayoutSavedInThePanel() {
        let app = XCUIApplication.launch("-uitest-panel-layout")
        app.signInToDashboard()
        XCTAssertTrue(app.card("steps").waitForExistence(timeout: 10))
        app.assertOrder(["steps", "weight", "sleep", "vo2max"])
        XCTAssertEqual(app.card("weight").frame.minY, app.card("blood_pressure").frame.minY, accuracy: 1)
        XCTAssertGreaterThan(app.card("sleep").frame.width, app.card("weight").frame.width * 1.5, "sleep is M")
        XCTAssertFalse(app.card("resting_heart_rate").exists, "hidden in the panel")
        XCTAssertFalse(app.card("hrv_rmssd_nightly").exists, "not on the panel's layout")
        XCTAssertTrue(app.staticTexts["Garmin Connect needs reauthorization."].exists)
        XCTAssertEqual(app.texts(beginningWith: "The last backup is from").count, 0, "dismissed in the panel")
    }
}

private extension XCUIApplication {
    func card(_ code: String) -> XCUIElement {
        buttons["card-\(code)"]
    }

    func texts(beginningWith prefix: String) -> XCUIElementQuery {
        staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", prefix))
    }

    /// The cards appear top to bottom (and left to right in a row) in this order.
    func assertOrder(_ codes: [String], file: StaticString = #filePath, line: UInt = #line) {
        let frames = codes.map { card($0).frame }
        for (index, pair) in zip(frames, frames.dropFirst()).enumerated() {
            let (a, b) = pair
            XCTAssertTrue(a.minY < b.minY - 1 || (abs(a.minY - b.minY) <= 1 && a.minX < b.minX),
                          "\(codes[index]) comes before \(codes[index + 1])", file: file, line: line)
        }
    }

    func waitForLabel(of element: XCUIElement, _ label: String, file: StaticString = #filePath, line: UInt = #line) {
        let match = NSPredicate(format: "label == %@", label)
        let found = XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: match, object: element)], timeout: 10)
        XCTAssertEqual(found, .completed, "\(element.label) is \(label)", file: file, line: line)
    }
}
