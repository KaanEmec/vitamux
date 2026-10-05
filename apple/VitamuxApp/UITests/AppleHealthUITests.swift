import XCTest

/// Apple Health (`vitamux://apple-health`) against the fake HealthStore and the fake server
/// (`FakeServer+AppleHealth.swift`): one-tap and manual pairing, groups, Sync now, a revoked
/// token, a server-requested reset and unpair. Moved from Bridge's UI tests. Values are synthetic.
@MainActor
final class AppleHealthUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    /// Signs in and opens the Apple Health screen.
    private func open(_ arguments: String...) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-uitest"] + arguments
        app.launch()
        app.signInToDashboard()
        app.openLink("vitamux://apple-health")
        XCTAssertTrue(app.navigationBars["Apple Health"].waitForExistence(timeout: 5))
        return app
    }

    /// Waits until the element's label (a `LabeledContent` reads "Title, value") ends in `value`.
    private func wait(_ element: XCUIElement, value: String, timeout: TimeInterval = 10) {
        let expectation = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label ENDSWITH %@", " " + value), object: element)
        XCTAssertEqual(XCTWaiter.wait(for: [expectation], timeout: timeout), .completed, "\(element) shows \(value)")
    }

    /// Swipes down, then up, until `element` can be tapped.
    @discardableResult
    private func reveal(_ element: XCUIElement, in app: XCUIApplication) -> XCUIElement {
        for _ in 0..<6 where !(element.exists && element.isHittable) { app.swipeDown() }
        return app.scrollTo(element)
    }

    private func enableHeart(_ app: XCUIApplication) {
        let heart = reveal(app.switches["group-heart"], in: app)
        XCTAssertTrue(heart.waitForExistence(timeout: 5))
        XCTAssertFalse(app.staticTexts["requested-heart"].exists)
        heart.switches.firstMatch.tap()
        XCTAssertTrue(app.staticTexts["requested-heart"].waitForExistence(timeout: 10))
    }

    func testOneTapPairing() {
        let app = open()
        let pair = app.buttons["pairHereButton"]
        XCTAssertTrue(pair.waitForExistence(timeout: 5))
        pair.tap()
        XCTAssertTrue(app.element("pairedState").waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["pairedServer"].label.hasSuffix("fake.vitamux.test"), "paired with the signed-in server")
        XCTAssertTrue(app.scrollTo(app.staticTexts["serverTypes"]).waitForExistence(timeout: 10), "the server lists the new device")
    }

    func testPairingByManualEntry() {
        let app = open()
        let url = app.scrollTo(app.textFields["serverURLField"])
        XCTAssertTrue(url.waitForExistence(timeout: 5))
        url.tap()
        url.typeText("https://fake.vitamux.test")
        let code = app.textFields["codeField"]
        code.tap()
        code.typeText("SYNT-H234")
        app.buttons["pairButton"].tap()
        XCTAssertTrue(app.element("pairedState").waitForExistence(timeout: 10))
    }

    func testPairingRejectsPlainHTTP() {
        let app = open()
        let url = app.scrollTo(app.textFields["serverURLField"])
        XCTAssertTrue(url.waitForExistence(timeout: 5))
        url.tap()
        url.typeText("http://fake.vitamux.test")
        let code = app.textFields["codeField"]
        code.tap()
        code.typeText("SYNT-H234")
        app.buttons["pairButton"].tap()
        XCTAssertTrue(app.staticTexts["Check the server address"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.element("pairedState").exists)
    }

    func testEnablingAGroupShowsRequested() {
        let app = open("-uitest-paired")
        XCTAssertTrue(app.element("pairedState").waitForExistence(timeout: 10))
        enableHeart(app)
        XCTAssertEqual(app.staticTexts["requested-heart"].label, "Requested")
        XCTAssertFalse(app.staticTexts["Granted"].exists)

        app.scrollTo(app.buttons["types-heart"]).tap()
        XCTAssertTrue(app.staticTexts["Heart Rate"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["anchor-HKQuantityTypeIdentifierHeartRate"].label.hasPrefix("Anchor "), "pulled in full")
        XCTAssertTrue(app.staticTexts["possiblyDenied-HKQuantityTypeIdentifierRestingHeartRate"].exists, "the server's hint")
    }

    func testSyncNowSendsTheCheckpoint() {
        let app = open("-uitest-paired")
        enableHeart(app)
        let types = app.scrollTo(app.staticTexts["serverTypes"])
        XCTAssertTrue(types.waitForExistence(timeout: 10))
        XCTAssertFalse(types.label.hasSuffix(" 0"), "the heartbeat listed the Heart anchors")

        // Off keeps what the server has; the next sync reports no anchors.
        reveal(app.switches["group-heart"], in: app).switches.firstMatch.tap()
        XCTAssertFalse(app.staticTexts["requested-heart"].waitForExistence(timeout: 2))
        reveal(app.buttons["syncNowButton"], in: app).tap()
        wait(app.scrollTo(app.staticTexts["serverTypes"]), value: "0")
        XCTAssertTrue(reveal(app.staticTexts["syncSummary"], in: app).label.hasPrefix("Last sync"))
    }

    func testARevokedTokenStopsSyncAndPairsAgain() {
        let app = open("-uitest-paired", "-uitest-revoked")
        XCTAssertTrue(app.element("revokedState").waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["syncNowButton"].exists, "sync stopped")
        app.buttons["repairButton"].tap()
        XCTAssertTrue(app.element("pairedState").waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["syncNowButton"].exists)
    }

    func testAServerRequestedResetIsApplied() {
        let app = open("-uitest-paired", "-uitest-anchor-reset")
        XCTAssertTrue(app.staticTexts["serverResetApplied"].waitForExistence(timeout: 10))
    }

    func testUnpairRevokesAndForgets() {
        let app = open("-uitest-paired")
        XCTAssertTrue(app.element("pairedState").waitForExistence(timeout: 10))
        app.scrollTo(app.buttons["unpairButton"]).tap()
        let confirm = app.buttons["confirmUnpair"].firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5))
        confirm.tap()
        XCTAssertTrue(app.buttons["pairHereButton"].waitForExistence(timeout: 10), "not paired any more")

        app.tabBars.buttons["More"].tap()
        app.scrollTo(app.buttons["signOutButton"]).tap()
        XCTAssertTrue(app.buttons["confirmSignOut"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.switches["unpairToggle"].exists, "the device token is gone")
    }

    func testSignOutLeavesSyncRunning() {
        let app = open("-uitest-paired")
        enableHeart(app)
        app.tabBars.buttons["More"].tap()
        app.scrollTo(app.buttons["signOutButton"]).tap()
        app.buttons["confirmSignOut"].tap()
        XCTAssertTrue(app.element("signedOut").waitForExistence(timeout: 10))

        app.signInToDashboard()
        app.openLink("vitamux://apple-health")
        XCTAssertTrue(app.element("pairedState").waitForExistence(timeout: 10))
        XCTAssertTrue(app.scrollTo(app.staticTexts["requested-heart"]).exists, "groups stay on")
    }
}
