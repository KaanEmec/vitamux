import XCTest

/// Apple Health › Sources (`vitamux://apple-health/sources`, J22.25) for the paired iPhone against
/// the fake server's source-filter scenario (`FakeServer+SourceFilter.swift`): a synthetic band
/// ignored by default because its provider is connected directly, the Apple Watch and a synthetic
/// scale taken. Take, ignore, per type, the explanation line and Explore's ignored sources.
@MainActor
final class AppleHealthSourcesUITests: XCTestCase {
    let band = "com.example.synthetic.band", scale = "com.example.synthetic.scale"

    override func setUp() async throws { continueAfterFailure = false }

    /// Signs in with this iPhone paired and opens `link`, waiting for its screen.
    private func open(_ link: String = "vitamux://apple-health/sources", title: String = "Sources") -> XCUIApplication {
        let app = XCUIApplication.launch("-uitest-paired")
        app.signInToDashboard()
        app.openLink(link)
        XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: Wait.ui), link)
        return app
    }

    private func wait(_ element: XCUIElement, label: String, timeout: TimeInterval = Wait.server) {
        let expectation = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label == %@", label), object: element)
        XCTAssertEqual(XCTWaiter.wait(for: [expectation], timeout: timeout), .completed, "\(element) reads \(label)")
    }

    func testDefaultIgnoredRowExplainsWhyAndTakeMakesItYourChoice() {
        let app = open()
        let reason = app.staticTexts["reason-\(band)"]
        XCTAssertTrue(app.scrollTo(reason).waitForExistence(timeout: Wait.server))
        XCTAssertEqual(reason.label, "You get Synthetic Band Cloud directly; its copy in Apple Health would count twice.")
        XCTAssertEqual(app.staticTexts["choice-\(band)"].label, "Default")
        XCTAssertTrue(app.segmentedControls["mode-\(band)"].buttons["Ignore"].isSelected)
        XCTAssertFalse(app.element("reason-\(scale)").exists, "a row taken by default needs no explanation")

        app.segmentedControls["mode-\(band)"].buttons["Take"].tap()
        wait(app.staticTexts["choice-\(band)"], label: "Your choice")
        XCTAssertTrue(app.segmentedControls["mode-\(band)"].buttons["Take"].isSelected)
        XCTAssertFalse(reason.exists, "an explicit choice is not explained by the default")

        app.buttons["useDefault-\(band)"].tap()
        wait(app.staticTexts["choice-\(band)"], label: "Default")
        XCTAssertTrue(app.staticTexts["reason-\(band)"].waitForExistence(timeout: Wait.server))
    }

    func testIgnoreAnotherApp() {
        let app = open()
        let control = app.scrollTo(app.segmentedControls["mode-\(scale)"])
        XCTAssertTrue(control.waitForExistence(timeout: Wait.server))
        XCTAssertTrue(control.buttons["Take"].isSelected)
        XCTAssertEqual(app.staticTexts["classification-\(scale)"].label, "Direct")
        control.buttons["Ignore"].tap()
        wait(app.staticTexts["choice-\(scale)"], label: "Your choice")
        XCTAssertTrue(control.buttons["Ignore"].isSelected)
    }

    func testChooseTypesTakesOnlyTheChosenOnes() {
        let app = open()
        let choose = app.scrollTo(app.buttons["chooseTypes-\(band)"])
        XCTAssertTrue(choose.waitForExistence(timeout: Wait.server))
        choose.tap()
        let heartRate = app.switches["type-HKQuantityTypeIdentifierHeartRate"]
        XCTAssertTrue(heartRate.waitForExistence(timeout: Wait.ui))
        XCTAssertEqual(heartRate.value as? String, "0", "ignored: nothing taken yet")
        heartRate.switches.firstMatch.tap()
        app.buttons["saveTypes"].tap()

        wait(app.staticTexts["choice-\(band)"], label: "Your choice")
        let perType = app.staticTexts["perType-\(band)"]
        XCTAssertTrue(perType.waitForExistence(timeout: Wait.server))
        XCTAssertEqual(perType.label, "Takes Heart rate only")
        XCTAssertFalse(app.segmentedControls["mode-\(band)"].buttons["Take"].isSelected)
        XCTAssertFalse(app.segmentedControls["mode-\(band)"].buttons["Ignore"].isSelected)
    }

    func testAppleHealthRowSummarisesAndOpensTheScreen() {
        let app = open("vitamux://apple-health", title: "Apple Health")
        let row = app.scrollTo(app.buttons["appleHealthSources"])
        XCTAssertTrue(row.waitForExistence(timeout: Wait.server))
        let summary = app.staticTexts["sourcesSummary"]
        XCTAssertTrue(summary.waitForExistence(timeout: Wait.server))
        XCTAssertEqual(summary.label, "1 ignored")
        row.tap()
        XCTAssertTrue(app.navigationBars["Sources"].waitForExistence(timeout: Wait.ui))
        XCTAssertTrue(app.element("origin-\(band)").waitForExistence(timeout: Wait.server))
    }

    func testShowInExploreOpensExploreFilteredToTheApp() {
        let app = open()
        let link = app.scrollTo(app.buttons["explore-\(band)"])
        XCTAssertTrue(link.waitForExistence(timeout: Wait.server))
        link.tap()
        XCTAssertTrue(app.navigationBars["Explore"].waitForExistence(timeout: Wait.ui))
        XCTAssertTrue(app.element("originFilter").waitForExistence(timeout: Wait.server), "the origin filter is set")
    }

    func testExploreShowsIgnoredSourcesWhenAsked() {
        let app = open("vitamux://explore", title: "Explore")
        let ignored = app.element("ignored-heart_rate-\(band)")
        XCTAssertFalse(ignored.exists, "hidden by default")
        app.scrollTo(app.switches["showIgnoredToggle"]).switches.firstMatch.tap()
        XCTAssertTrue(ignored.waitForExistence(timeout: Wait.server))
        XCTAssertTrue(ignored.label.contains("42 records held raw, not used"))
        XCTAssertTrue(ignored.label.contains("Synthetic Band"))
    }
}
