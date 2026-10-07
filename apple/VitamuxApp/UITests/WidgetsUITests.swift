import XCTest

/// The app's side of the widgets (J22.20): a dashboard load hands today's cards to the widgets,
/// Settings › This app says from when, and a widget's links open the card's Explore screen. The
/// widgets themselves are rendered by `VitamuxWidgetsTests`; the lock screen needs a device.
@MainActor
final class WidgetsUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    func testDashboardHandsValuesToWidgets() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        XCTAssertTrue(app.buttons["card-steps"].waitForExistence(timeout: Wait.server))

        app.openLink("vitamux://settings/app")
        XCTAssertTrue(app.navigationBars["This app"].waitForExistence(timeout: Wait.ui))
        let asOf = app.element("widgetsAsOf")
        XCTAssertTrue(app.scrollTo(asOf).waitForExistence(timeout: Wait.ui))
        let asOfText = "\(asOf.label) \(asOf.value as? String ?? "")"
        XCTAssertTrue(asOfText.contains("Values from"), asOfText)
        XCTAssertFalse(asOfText.contains("Not yet"), "the dashboard wrote the widgets' values: \(asOfText)")
        let redact = app.scrollTo(app.switches["redactWidgets"])
        XCTAssertEqual(redact.value as? String, "1", "values are hidden while locked by default")
        redact.switches.firstMatch.tap()
        XCTAssertEqual(app.switches["redactWidgets"].value as? String, "0")
    }

    /// The links the widgets put on their cards (`WidgetSnapshot.Card.link`).
    func testWidgetLinksOpenExplore() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        for (link, title) in [
            ("vitamux://explore/resting_heart_rate", "Resting heart rate"),
            ("vitamux://explore/sleep", "Sleep"),
            ("vitamux://explore/blood-pressure", "Blood pressure"),
        ] {
            app.openLink(link)
            XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: Wait.ui), link)
            XCTAssertTrue(app.tabBars.buttons["Explore"].isSelected, link)
        }
    }

    /// A signed-out widget opens the dashboard, which waits for sign-in.
    func testSignedOutWidgetOpensSignIn() {
        let app = XCUIApplication.launch()
        app.openLink("vitamux://dashboard")
        XCTAssertTrue(app.textFields["serverField"].waitForExistence(timeout: 10))
        app.signInToDashboard()
        XCTAssertTrue(app.buttons["card-steps"].waitForExistence(timeout: Wait.server))
    }
}
