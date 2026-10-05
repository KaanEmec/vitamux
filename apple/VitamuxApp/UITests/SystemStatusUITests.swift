import XCTest

/// The worked example's UI test: open the screen by its deep link, check what it shows.
/// J22.4 adds the `-uitest` launch argument that swaps in the fake server; this screen needs none.
@MainActor
final class SystemStatusUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    func testDeepLinkOpensSystemStatus() {
        let app = XCUIApplication()
        app.launch()
        app.open(URL(string: "vitamux://settings/system")!)
        XCTAssertTrue(app.navigationBars["System status"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.descendants(matching: .any)["appVersion"].waitForExistence(timeout: 5))
    }
}
