import XCTest

/// The upgrade in place: run by `scripts/upgrade-from-bridge.sh` after a Bridge build paired this
/// simulator (Bridge's `-uitest -uitest-paired`); skipped in a normal run. The app reads Bridge's
/// device token (the unpair toggle shows), and unpairing clears it; the script checks the anchors.
@MainActor
final class UpgradeFromBridgeUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    func testBridgePairingSurvivesTheUpgrade() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["VITAMUX_UPGRADE_CHECK"] == "1", "run by scripts/upgrade-from-bridge.sh")
        let app = XCUIApplication.launch("-uitest-keep-device")
        app.signInToDashboard()
        app.tabBars.buttons["More"].tap()
        app.scrollTo(app.buttons["signOutButton"]).tap()
        let toggle = app.switches["unpairToggle"]
        XCTAssertTrue(toggle.waitForExistence(timeout: Wait.ui), "the device token Bridge saved is still there")
        if ProcessInfo.processInfo.environment["VITAMUX_UPGRADE_UNPAIR"] == "1" {
            toggle.switches.firstMatch.tap()
            app.buttons["confirmSignOut"].tap()
            XCTAssertTrue(app.element("signedOut").waitForExistence(timeout: Wait.server))
        }
    }
}
