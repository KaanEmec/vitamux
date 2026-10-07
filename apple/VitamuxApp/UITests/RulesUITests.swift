import XCTest

/// Rules, the rule lens and the builder on the fake server (FakeServer+Rules.swift). Scenario:
/// resting heart rate tries WHOOP, Garmin, then Apple Watch, and excludes Apple Health relays;
/// the preview changes three days of any draft that differs from the rule in effect.
@MainActor
final class RulesUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    private func waitForLabel(_ element: XCUIElement, _ label: String, file: StaticString = #filePath, line: UInt = #line) {
        let matched = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label ==[c] %@", label), object: element)
        XCTAssertEqual(XCTWaiter.wait(for: [matched], timeout: 10), .completed, "\(element) is \(element.label), not \(label)", file: file, line: line)
    }

    private func signedIn() -> XCUIApplication {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        return app
    }

    /// Opens the builder for `metric` at its sources step.
    private func builder(_ app: XCUIApplication, metric: String) {
        app.openLink("vitamux://rules/new?metric=\(metric)")
        XCTAssertTrue(app.navigationBars["Rule builder"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.textFields["groupID-0"].waitForExistence(timeout: 10), "a metric opens on its sources")
    }

    private func step(_ app: XCUIApplication, _ n: Int) {
        app.buttons["builderStep-\(n)"].tap()
    }

    func testCatalogueListsEveryMetricWithItsRule() {
        let app = signedIn()
        app.openLink("vitamux://rules")
        let sentence = app.staticTexts["ruleSentence-resting_heart_rate"]
        XCTAssertTrue(sentence.waitForExistence(timeout: 10))
        XCTAssertEqual(sentence.label, "For each day, use the first source in order with data; if none has, the day has no value. 1 excluded source is never used.")
        XCTAssertTrue(app.staticTexts["Built-in default"].firstMatch.exists)
        app.assertNoProblem("catalogue")

        app.buttons["Default 1"].tap()
        XCTAssertTrue(app.staticTexts["ruleSentence-spo2"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.staticTexts["ruleSentence-resting_heart_rate"].exists, "the filter hides built-ins")
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'All '")).firstMatch.tap()

        app.buttons["ruleEntry-resting_heart_rate"].tap()
        XCTAssertTrue(app.navigationBars["resting_heart_rate"].waitForExistence(timeout: 10))
        waitForLabel(app.staticTexts["inEffect"], "IN EFFECT: BUILT-IN DEFAULT")
        XCTAssertTrue(app.element("inEffectGroup-0").label.contains("whoop"), app.element("inEffectGroup-0").label)
    }

    func testLensReorderPreviewSaveAndActivateThenRevert() {
        let app = signedIn()
        app.openLink("vitamux://explore/resting_heart_rate?range=1M")
        XCTAssertTrue(app.navigationBars["Resting heart rate"].waitForExistence(timeout: 10))
        app.scrollTo(app.buttons["editRule"]).tap()
        XCTAssertTrue(app.navigationBars["How it's calculated"].waitForExistence(timeout: 10))
        let sentence = app.staticTexts["lensSentence"]
        XCTAssertTrue(sentence.waitForExistence(timeout: 10))
        XCTAssertTrue(sentence.label.hasPrefix("For each day, use the first source in order with data"))
        XCTAssertTrue(app.staticTexts["previewIdle"].exists, "nothing to preview before a change")

        app.scrollTo(app.buttons["moveUp-apple_watch"]).tap()
        waitForLabel(app.staticTexts["lensGroup-1"], "Apple Watch")
        waitForLabel(app.scrollTo(app.staticTexts["lensGroup-2"]), "garmin")

        let summary = app.staticTexts["previewSummary"]
        for _ in 0..<4 where !summary.exists { app.swipeDown() }
        XCTAssertTrue(summary.waitForExistence(timeout: 10), "the draft is previewed")
        XCTAssertTrue(summary.label.hasPrefix("3 of "), summary.label)
        XCTAssertTrue(summary.label.hasSuffix("no new gaps"), summary.label)

        app.scrollTo(app.buttons["saveActivate"]).tap()
        let message = app.staticTexts["lensMessage"]
        XCTAssertTrue(app.scrollTo(message).waitForExistence(timeout: 10))
        XCTAssertEqual(message.label, "Version 2 is now active.")
        XCTAssertFalse(app.element("draftBadge").exists, "the saved rule is the rule in effect")

        app.scrollTo(app.buttons["revert"]).tap()
        waitForLabel(message, "Reverted: version 1 is active again.")
        for _ in 0..<6 where !app.staticTexts["lensGroup-1"].isHittable { app.swipeDown() }
        waitForLabel(app.staticTexts["lensGroup-1"], "garmin")
    }

    func testAnUnacknowledgedSumCannotBeSaved() {
        let app = signedIn()
        builder(app, metric: "steps")
        step(app, 2)
        app.scrollTo(app.buttons["op-sum_across_sources"]).tap()
        step(app, 4)
        let save = app.buttons["saveRule"]
        XCTAssertTrue(save.waitForExistence(timeout: 5))
        save.tap()

        let error = app.staticTexts["ackError"]
        XCTAssertTrue(app.scrollTo(error).waitForExistence(timeout: 10), "the strategy step shows the missing acknowledgement")
        XCTAssertEqual(error.label, "Confirm that you understand the duplicate risk before saving a sum.")
        XCTAssertTrue(app.buttons["builderStep-2"].isSelected)
        XCTAssertFalse(app.navigationBars["steps"].exists, "nothing was saved")

        app.scrollTo(app.switches["ackToggle"]).switches.firstMatch.tap()
        XCTAssertFalse(app.staticTexts["ackError"].exists)
        step(app, 4)
        app.buttons["saveRule"].tap()
        XCTAssertTrue(app.navigationBars["steps"].waitForExistence(timeout: 10), "the saved rule opens")
        XCTAssertEqual(app.staticTexts["ruleNotice"].label, "Saved version 2.")
        XCTAssertTrue(app.staticTexts["inEffectSentence"].label.contains("add the sources together"))
    }

    func testAFieldErrorPointsToItsControl() {
        let app = signedIn()
        builder(app, metric: "resting_heart_rate")
        app.textFields["groupID-0"].replaceText("Bad Id")
        step(app, 4)
        app.buttons["saveRule"].tap()

        let error = app.staticTexts["groupIDError-0"]
        XCTAssertTrue(error.waitForExistence(timeout: 10), "the error returns to the sources step")
        XCTAssertEqual(error.label, "must start with a lowercase letter and use only a-z, 0-9 and _")
        XCTAssertTrue(app.buttons["builderStep-1"].isSelected)
        XCTAssertTrue(app.textFields["groupID-0"].isHittable, "the control is on screen")
    }

    func testActivatingAnOldVersion() {
        let app = signedIn()
        builder(app, metric: "heart_rate")
        step(app, 2)
        app.scrollTo(app.buttons["op-maximum_across_sources"]).tap()
        step(app, 4)
        app.buttons["saveRule"].tap()
        XCTAssertTrue(app.navigationBars["heart_rate"].waitForExistence(timeout: 10))
        waitForLabel(app.staticTexts["inEffect"], "IN EFFECT: VERSION 2")

        app.scrollTo(app.buttons["activate-1"]).tap()
        let activated = app.staticTexts["Version 1 is now active."]
        for _ in 0..<6 where !activated.exists { app.swipeDown() }
        XCTAssertTrue(activated.waitForExistence(timeout: 10))
        waitForLabel(app.staticTexts["inEffect"], "IN EFFECT: VERSION 1")
        XCTAssertTrue(app.scrollTo(app.element("diff-strategy.op")).exists, "the two versions are compared field by field")
    }
}
