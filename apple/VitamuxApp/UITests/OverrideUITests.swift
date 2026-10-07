import XCTest

/// The point sheet's override actions, provenance, and revoke on the all-sources day, on the fake
/// server. Scenario: every resting heart-rate day falls back from WHOOP (degraded) to Garmin.
@MainActor
final class OverrideUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    /// Opens resting heart rate on 1M and yesterday's point sheet (yesterday has data in any
    /// timezone).
    private func openPoint() -> XCUIApplication {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.openLink("vitamux://explore/resting_heart_rate?range=1M")
        XCTAssertTrue(app.navigationBars["Resting heart rate"].waitForExistence(timeout: Wait.ui))
        let row = app.buttons["valueRow-\(Fake.day(-1))"]
        XCTAssertTrue(app.scrollTo(row).waitForExistence(timeout: Wait.server))
        row.tap()
        XCTAssertTrue(app.staticTexts["pointSource"].waitForExistence(timeout: Wait.server))
        XCTAssertEqual(app.staticTexts["pointSource"].label, "from Garmin Connect")
        return app
    }

    private func saveOverride(_ app: XCUIApplication) {
        let save = app.buttons["saveOverride"]
        XCTAssertTrue(save.waitForExistence(timeout: Wait.ui))
        XCTAssertTrue(save.isEnabled)
        save.tap()
        XCTAssertTrue(save.waitForNonExistence(timeout: Wait.server), "the override sheet closes once saved")
    }

    private func waitForLabel(_ element: XCUIElement, _ label: String, file: StaticString = #filePath, line: UInt = #line) {
        let matched = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label == %@", label), object: element)
        XCTAssertEqual(XCTWaiter.wait(for: [matched], timeout: Wait.server), .completed, "\(element) is \(element.label), not \(label)", file: file, line: line)
    }

    func testExcludeAnInputFallsBackToTheNextSource() {
        let app = openPoint()
        let exclude = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'exclude-'")).firstMatch
        XCTAssertTrue(app.scrollTo(exclude).waitForExistence(timeout: Wait.server))
        exclude.tap()
        let record = app.textFields["recordField"]
        XCTAssertTrue(record.waitForExistence(timeout: Wait.ui))
        XCTAssertFalse((record.value as? String ?? "").isEmpty, "the record id is filled in")
        saveOverride(app)
        waitForLabel(app.staticTexts["pointSource"], "from Apple Watch")
    }

    func testForceASource() {
        let app = openPoint()
        app.scrollTo(app.buttons["forceSource"]).tap()
        let watch = app.buttons["Apple Watch"].firstMatch
        XCTAssertTrue(watch.waitForExistence(timeout: Wait.ui))
        watch.tap()
        saveOverride(app)
        waitForLabel(app.staticTexts["pointSource"], "from Apple Watch")
    }

    func testSetAValue() {
        let app = openPoint()
        app.scrollTo(app.buttons["setValue"]).tap()
        let value = app.textFields["valueField"]
        XCTAssertTrue(value.waitForExistence(timeout: Wait.ui))
        value.tap()
        value.typeText("49")
        XCTAssertEqual(app.textFields["unitField"].value as? String, "bpm", "the unit is the metric's")
        XCTAssertFalse(app.buttons["saveOverride"].isEnabled, "a value needs a note")
        let note = app.textFields["noteField"]
        note.tap()
        note.typeText("synthetic note")
        saveOverride(app)
        waitForLabel(app.staticTexts["pointValue"], "49 bpm")
        XCTAssertTrue(app.element("pointStatus").label.contains("Overridden"))
    }

    func testProvenanceShowsTheChain() {
        let app = openPoint()
        let trace = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'provenance-'")).firstMatch
        XCTAssertTrue(app.scrollTo(trace).waitForExistence(timeout: Wait.server))
        trace.tap()
        XCTAssertTrue(app.element("thisVersion").waitForExistence(timeout: Wait.server))
        let normalizer = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "garmin.daily_summary@3 (abcdef01)")).firstMatch
        XCTAssertTrue(normalizer.waitForExistence(timeout: Wait.server), "the normalizer and its commit")
        XCTAssertTrue(app.staticTexts["Earlier version #9182000"].exists || app.otherElements["version-9182000"].exists, "the version it replaced")
        app.buttons["closeProvenance"].tap()
        XCTAssertTrue(app.staticTexts["pointSource"].waitForExistence(timeout: Wait.ui))
    }

    func testRevokeOnTheAllSourcesDay() {
        let app = openPoint()
        app.scrollTo(app.buttons["setValue"]).tap()
        let value = app.textFields["valueField"]
        XCTAssertTrue(value.waitForExistence(timeout: Wait.ui))
        value.tap()
        value.typeText("49")
        app.textFields["noteField"].tap()
        app.textFields["noteField"].typeText("synthetic note")
        saveOverride(app)
        waitForLabel(app.staticTexts["pointValue"], "49 bpm")

        app.scrollTo(app.buttons["allSources"]).tap()
        XCTAssertTrue(app.element("pointValue").waitForExistence(timeout: Wait.server))
        waitForLabel(app.staticTexts["pointValue"], "49 bpm")
        XCTAssertTrue(app.scrollTo(app.element("sourceStatus-apple_health-excluded")).waitForExistence(timeout: Wait.server), "the relayed copy is marked excluded")
        XCTAssertTrue(app.element("sourceStatus-manual-not_in_rule").exists, "manual entries are outside the rule")

        let revoke = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'revoke-'")).firstMatch
        XCTAssertTrue(app.scrollTo(revoke).waitForExistence(timeout: Wait.server))
        revoke.tap()
        let state = app.staticTexts.matching(NSPredicate(format: "identifier BEGINSWITH 'overrideState-'")).firstMatch
        let revoked = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label BEGINSWITH 'Revoked'"), object: state)
        XCTAssertEqual(XCTWaiter.wait(for: [revoked], timeout: Wait.server), .completed)
        app.swipeDown()
        app.swipeDown()
        waitForLabel(app.staticTexts["pointSource"], "from Garmin Connect")
    }

    func testAllSourcesDayOverlaysEverySource() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.openLink("vitamux://explore/heart_rate/day/\(Fake.day(-1))")
        XCTAssertTrue(app.element("dayChart").waitForExistence(timeout: Wait.server), "every source's readings over the day")
        let trace = app.buttons["trace-garmin-used"]
        XCTAssertTrue(app.scrollTo(trace).waitForExistence(timeout: Wait.server))
        trace.tap()
        XCTAssertTrue(app.element("thisVersion").waitForExistence(timeout: Wait.server))
    }
}
