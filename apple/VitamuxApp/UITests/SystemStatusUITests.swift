import XCTest

/// The worked example's UI test: sign in to the fake server (`-uitest`), open the screen by its
/// deep link, check what it shows by accessibility identifier.
@MainActor
final class SystemStatusUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    func testDeepLinkOpensSystemStatus() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.openLink("vitamux://settings/system")
        XCTAssertTrue(app.navigationBars["System status"].waitForExistence(timeout: Wait.ui))
        XCTAssertTrue(app.element("appVersion").waitForExistence(timeout: Wait.ui))
    }
}
