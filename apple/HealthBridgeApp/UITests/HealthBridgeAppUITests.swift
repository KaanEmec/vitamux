import XCTest

/// Runs the app with `-uitest`: a fake HealthStore and a fake transport, so there are no
/// HealthKit prompts and no server. Values are synthetic.
@MainActor
final class HealthBridgeAppUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    private func launch(paired: Bool) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-uitest"] + (paired ? ["-uitest-paired"] : [])
        app.launch()
        return app
    }

    func testPairingByManualEntry() {
        let app = launch(paired: false)
        let url = app.textFields["serverURLField"]
        XCTAssertTrue(url.waitForExistence(timeout: 10))
        url.tap()
        url.typeText("https://vitamux.example.test")
        let code = app.textFields["codeField"]
        code.tap()
        code.typeText("SYNTH-1234")
        app.buttons["pairButton"].tap()
        XCTAssertTrue(app.staticTexts["pairedState"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.tabBars.buttons["Status"].exists)
    }

    func testPairingRejectsPlainHTTP() {
        let app = launch(paired: false)
        let url = app.textFields["serverURLField"]
        XCTAssertTrue(url.waitForExistence(timeout: 10))
        url.tap()
        url.typeText("http://vitamux.example.test")
        let code = app.textFields["codeField"]
        code.tap()
        code.typeText("SYNTH-1234")
        app.buttons["pairButton"].tap()
        XCTAssertTrue(app.staticTexts["pairError"].waitForExistence(timeout: 5))
    }

    func testEnablingAGroupShowsRequested() {
        let app = launch(paired: true)
        XCTAssertTrue(app.staticTexts["pairedState"].waitForExistence(timeout: 10))
        let heart = app.switches["group-heart"]
        XCTAssertTrue(heart.waitForExistence(timeout: 5))
        XCTAssertFalse(app.staticTexts["requested-heart"].exists)
        heart.switches.firstMatch.tap()
        XCTAssertTrue(app.staticTexts["requested-heart"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.staticTexts["requested-heart"].label, "Requested")
        XCTAssertFalse(app.staticTexts["Granted"].exists)

        app.tabBars.buttons["Status"].tap()
        XCTAssertTrue(app.staticTexts["Heart Rate"].waitForExistence(timeout: 5))
    }
}
