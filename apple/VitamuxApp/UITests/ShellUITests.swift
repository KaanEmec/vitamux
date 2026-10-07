import XCTest

/// Tabs, search, sync status, theme and sign-out on the fake server.
@MainActor
final class ShellUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    func testEveryTabIsReachable() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        for (tab, title) in [("Explore", "Explore"), ("Sources", "Sources"), ("Lab", "Lab"), ("More", "More"), ("Dashboard", "Dashboard")] {
            app.tabBars.buttons[tab].tap()
            XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: Wait.ui), tab)
        }
    }

    func testSyncStatusShowsAttentionAndOpensSources() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        let status = app.buttons["syncStatus"].firstMatch
        XCTAssertTrue(status.waitForExistence(timeout: Wait.server))
        let attention = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label CONTAINS '1 source needs attention'"), object: status)
        XCTAssertEqual(XCTWaiter.wait(for: [attention], timeout: Wait.server), .completed, status.label)
        status.tap()
        XCTAssertTrue(app.navigationBars["Sources"].waitForExistence(timeout: Wait.ui))
    }

    func testSearchFindsAMetricAndItsRule() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.buttons["searchButton"].firstMatch.tap()
        let field = app.searchFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: Wait.ui))
        XCTAssertTrue(app.buttons["result-Go to-Dashboard"].waitForExistence(timeout: Wait.ui), "sections show without a query")
        field.typeText("resting")
        XCTAssertTrue(app.buttons["result-Rules-resting_heart_rate"].waitForExistence(timeout: Wait.server))
        app.buttons["result-Metrics-resting_heart_rate"].tapWhenReady(timeout: Wait.server)
        XCTAssertTrue(app.navigationBars["Resting heart rate"].waitForExistence(timeout: Wait.ui))
        XCTAssertTrue(app.tabBars.buttons["Explore"].isSelected)
    }

    func testSearchFindsAConnection() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        XCTAssertTrue(app.buttons["syncStatus"].firstMatch.waitForExistence(timeout: Wait.server))
        app.buttons["searchButton"].firstMatch.tap()
        let field = app.searchFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: Wait.ui))
        field.typeText("withings")
        let result = app.buttons["result-Connections-withings"]
        XCTAssertTrue(result.waitForExistence(timeout: Wait.server))
        result.tap()
        XCTAssertTrue(app.navigationBars["Withings"].waitForExistence(timeout: Wait.server))
    }

    func testThemePreference() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.tabBars.buttons["More"].tap()
        app.buttons["themePicker"].tap()
        app.buttons["Dark"].tapWhenReady()
        XCTAssertTrue(app.buttons["themePicker"].label.contains("Dark"), app.buttons["themePicker"].label)
    }

    func testSignOut() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.tabBars.buttons["More"].tap()
        XCTAssertEqual(app.scrollTo(app.staticTexts["signedInServer"]).label, Fake.server)
        app.buttons["signOutButton"].tap()
        XCTAssertTrue(app.buttons["confirmSignOut"].waitForExistence(timeout: Wait.ui))
        XCTAssertFalse(app.switches["unpairToggle"].exists, "nothing to unpair without a paired iPhone")
        app.buttons["confirmSignOut"].tap()
        XCTAssertTrue(app.element("signedOut").waitForExistence(timeout: Wait.server))
        // The session was revoked: signing in again starts a new one.
        app.signInToDashboard()
    }

    func testSignOutAlsoUnpairsThisIPhone() {
        let app = XCUIApplication.launch("-uitest-paired")
        app.signInToDashboard()
        app.tabBars.buttons["More"].tap()
        app.scrollTo(app.buttons["signOutButton"]).tap()
        let toggle = app.switches["unpairToggle"]
        XCTAssertTrue(toggle.waitForExistence(timeout: Wait.ui))
        toggle.switches.firstMatch.tap()
        app.buttons["confirmSignOut"].tap()
        XCTAssertTrue(app.element("signedOut").waitForExistence(timeout: Wait.server))

        app.signInToDashboard()
        app.tabBars.buttons["More"].tap()
        app.scrollTo(app.buttons["signOutButton"]).tap()
        XCTAssertTrue(app.buttons["confirmSignOut"].waitForExistence(timeout: Wait.ui))
        XCTAssertFalse(app.switches["unpairToggle"].exists, "the device token is gone")
    }

    func testSignOutKeepsThePairingByDefault() {
        let app = XCUIApplication.launch("-uitest-paired")
        app.signInToDashboard()
        app.tabBars.buttons["More"].tap()
        app.scrollTo(app.buttons["signOutButton"]).tap()
        XCTAssertTrue(app.switches["unpairToggle"].waitForExistence(timeout: Wait.ui))
        app.buttons["confirmSignOut"].tap()
        XCTAssertTrue(app.element("signedOut").waitForExistence(timeout: Wait.server))

        app.signInToDashboard()
        app.tabBars.buttons["More"].tap()
        app.scrollTo(app.buttons["signOutButton"]).tap()
        XCTAssertTrue(app.switches["unpairToggle"].waitForExistence(timeout: Wait.ui), "Apple Health stays paired")
    }
}
