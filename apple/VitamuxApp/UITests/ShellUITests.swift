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
            XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: 5), tab)
        }
    }

    func testSyncStatusShowsAttentionAndOpensSources() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        let status = app.buttons["syncStatus"].firstMatch
        XCTAssertTrue(status.waitForExistence(timeout: 10))
        XCTAssertTrue(status.label.contains("1 source needs attention"), status.label)
        status.tap()
        XCTAssertTrue(app.navigationBars["Sources"].waitForExistence(timeout: 5))
    }

    func testSearchFindsAMetricAndItsRule() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.buttons["searchButton"].firstMatch.tap()
        let field = app.searchFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["result-Go to-Dashboard"].waitForExistence(timeout: 5), "sections show without a query")
        field.typeText("resting")
        XCTAssertTrue(app.buttons["result-Rules-heart_rate_resting"].waitForExistence(timeout: 10))
        app.buttons["result-Metrics-heart_rate_resting"].tap()
        XCTAssertTrue(app.navigationBars["Heart rate resting"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.tabBars.buttons["Explore"].isSelected)
    }

    func testSearchFindsAConnection() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        XCTAssertTrue(app.buttons["syncStatus"].firstMatch.waitForExistence(timeout: 10))
        app.buttons["searchButton"].firstMatch.tap()
        app.searchFields.firstMatch.typeText("withings")
        let result = app.buttons["result-Connections-withings"]
        XCTAssertTrue(result.waitForExistence(timeout: 5))
        result.tap()
        XCTAssertTrue(app.navigationBars["Withings"].waitForExistence(timeout: 10))
    }

    func testThemePreference() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.tabBars.buttons["More"].tap()
        app.buttons["themePicker"].tap()
        app.buttons["Dark"].tap()
        XCTAssertTrue(app.buttons["themePicker"].label.contains("Dark"), app.buttons["themePicker"].label)
    }

    func testSignOut() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.tabBars.buttons["More"].tap()
        XCTAssertEqual(app.scrollTo(app.staticTexts["signedInServer"]).label, Fake.server)
        app.buttons["signOutButton"].tap()
        XCTAssertTrue(app.buttons["confirmSignOut"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.switches["unpairToggle"].exists, "nothing to unpair without a paired iPhone")
        app.buttons["confirmSignOut"].tap()
        XCTAssertTrue(app.element("signedOut").waitForExistence(timeout: 10))
        // The session was revoked: signing in again starts a new one.
        app.signInToDashboard()
    }

    func testSignOutAlsoUnpairsThisIPhone() {
        let app = XCUIApplication.launch("-uitest-paired")
        app.signInToDashboard()
        app.tabBars.buttons["More"].tap()
        app.scrollTo(app.buttons["signOutButton"]).tap()
        let toggle = app.switches["unpairToggle"]
        XCTAssertTrue(toggle.waitForExistence(timeout: 5))
        toggle.switches.firstMatch.tap()
        app.buttons["confirmSignOut"].tap()
        XCTAssertTrue(app.element("signedOut").waitForExistence(timeout: 10))

        app.signInToDashboard()
        app.tabBars.buttons["More"].tap()
        app.scrollTo(app.buttons["signOutButton"]).tap()
        XCTAssertTrue(app.buttons["confirmSignOut"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.switches["unpairToggle"].exists, "the device token is gone")
    }

    func testSignOutKeepsThePairingByDefault() {
        let app = XCUIApplication.launch("-uitest-paired")
        app.signInToDashboard()
        app.tabBars.buttons["More"].tap()
        app.scrollTo(app.buttons["signOutButton"]).tap()
        XCTAssertTrue(app.switches["unpairToggle"].waitForExistence(timeout: 5))
        app.buttons["confirmSignOut"].tap()
        XCTAssertTrue(app.element("signedOut").waitForExistence(timeout: 10))

        app.signInToDashboard()
        app.tabBars.buttons["More"].tap()
        app.scrollTo(app.buttons["signOutButton"]).tap()
        XCTAssertTrue(app.switches["unpairToggle"].waitForExistence(timeout: 5), "Apple Health stays paired")
    }
}
