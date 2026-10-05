import XCTest

/// App lock and the app-switcher cover. UI tests have no Face ID or passcode: under `-uitest`
/// the lock opens on the Unlock button.
@MainActor
final class AppLockUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    func testLockAfterLeavingTheAppAndUnlock() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.openLink("vitamux://settings/app")
        let toggle = app.switches["appLockToggle"]
        XCTAssertTrue(toggle.waitForExistence(timeout: 5))
        toggle.switches.firstMatch.tap()
        XCTAssertEqual(toggle.value as? String, "1")

        XCUIDevice.shared.press(.home)
        app.activate()
        XCTAssertTrue(app.buttons["unlockButton"].waitForExistence(timeout: 10), "locked after leaving the app")
        XCTAssertFalse(app.tabBars.buttons["Dashboard"].exists, "nothing shows behind the lock")

        // A link while locked opens after unlocking.
        app.openLink("vitamux://lab/results")
        app.buttons["unlockButton"].tap()
        XCTAssertTrue(app.navigationBars["Results"].waitForExistence(timeout: 5))
    }

    func testNoLockWhenOff() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        XCUIDevice.shared.press(.home)
        app.activate()
        XCTAssertTrue(app.tabBars.buttons["Dashboard"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["unlockButton"].exists)
    }
}
