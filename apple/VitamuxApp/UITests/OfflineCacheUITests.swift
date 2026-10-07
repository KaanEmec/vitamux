import XCTest

/// The offline read cache (J22.19) with the fake server's failure mode: `vitamux://uitest/offline`
/// fails every request as without a network, `vitamux://uitest/online` brings it back.
@MainActor
final class OfflineCacheUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    func testCachedDashboardAfterAnOutage() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        XCTAssertTrue(app.card("steps").waitForExistence(timeout: Wait.server))
        XCTAssertFalse(app.element("offlineBanner").exists, "no banner while the server answers")

        // Yesterday, online: stored on the way.
        app.buttons["previousDay"].tap()
        app.waitForDay("Yesterday")
        XCTAssertTrue(app.card("steps").waitForExistence(timeout: Wait.server))
        app.buttons["nextDay"].tap()
        app.waitForDay("Today")

        // The outage: yesterday comes from the cache, marked in the shell.
        app.openLink("vitamux://uitest/offline")
        app.buttons["previousDay"].tap()
        app.waitForDay("Yesterday")
        let banner = app.element("offlineBanner")
        XCTAssertTrue(banner.waitForExistence(timeout: Wait.server), "the shell says the data is from the cache")
        XCTAssertTrue(banner.label.contains("Offline, showing data from"), banner.label)
        XCTAssertTrue(banner.label.contains("Changes need the network"), banner.label)
        XCTAssertTrue(app.card("steps").waitForExistence(timeout: Wait.server), "the cached day shows its cards")
        XCTAssertFalse(app.card("steps").label.contains("Partial"), "the cached past day, not today")

        // Back online: the next answer from the server ends the banner.
        app.openLink("vitamux://uitest/online")
        app.buttons["nextDay"].tap()
        app.waitForDay("Today")
        XCTAssertTrue(banner.waitForNonExistence(timeout: Wait.server))
        XCTAssertTrue(app.card("steps").waitForExistence(timeout: Wait.server))
    }

    func testPullToRefreshOfflineAsksTheServer() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        XCTAssertTrue(app.card("steps").waitForExistence(timeout: Wait.server))

        app.openLink("vitamux://uitest/offline")
        let top = app.staticTexts["dayTitle"]
        top.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
            .press(forDuration: 0.1, thenDragTo: top.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 12)))
        XCTAssertTrue(app.element("offlineBanner").waitForExistence(timeout: Wait.server), "the failure mode shows the banner")
        XCTAssertTrue(app.staticTexts["Can't reach the server"].firstMatch.waitForExistence(timeout: Wait.server),
                      "a pull to refresh never answers from the cache")
    }

    func testClearCache() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        XCTAssertTrue(app.card("steps").waitForExistence(timeout: Wait.server))
        app.buttons["previousDay"].tap()
        app.waitForDay("Yesterday")
        XCTAssertTrue(app.card("steps").waitForExistence(timeout: Wait.server))
        app.buttons["nextDay"].tap()
        app.waitForDay("Today")

        app.openLink("vitamux://settings/app")
        XCTAssertTrue(app.navigationBars["This app"].waitForExistence(timeout: Wait.ui))
        let size = app.element("cacheSize")
        XCTAssertTrue(app.scrollTo(size).waitForExistence(timeout: Wait.ui))
        XCTAssertFalse(size.text.contains("Empty"), "the dashboard was stored: \(size.text)")
        app.scrollTo(app.buttons["clearCache"]).tap()
        let confirm = app.dialogButton("Clear the cache")
        XCTAssertTrue(confirm.waitForExistence(timeout: Wait.ui))
        confirm.tap()
        XCTAssertTrue(confirm.waitForNonExistence(timeout: Wait.ui))
        let empty = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label CONTAINS 'Empty' OR value CONTAINS 'Empty'"), object: size)
        XCTAssertEqual(XCTWaiter.wait(for: [empty], timeout: Wait.ui), .completed, size.text)
        XCTAssertFalse(app.buttons["clearCache"].isEnabled)

        // Yesterday was stored before; without the server nothing is left to show.
        app.openLink("vitamux://uitest/offline")
        app.tabBars.buttons["Dashboard"].tap()
        app.buttons["previousDay"].tap()
        app.waitForDay("Yesterday")
        XCTAssertTrue(app.staticTexts["Can't reach the server"].firstMatch.waitForExistence(timeout: Wait.server))
    }
}

private extension XCUIApplication {
    func card(_ code: String) -> XCUIElement {
        buttons["card-\(code)"]
    }

    func waitForDay(_ title: String, file: StaticString = #filePath, line: UInt = #line) {
        let element = staticTexts["dayTitle"]
        let match = NSPredicate(format: "label == %@", title)
        let found = XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: match, object: element)], timeout: Wait.ui)
        XCTAssertEqual(found, .completed, "\(element.label) is \(title)", file: file, line: line)
    }
}

private extension XCUIElement {
    /// Label and value together (a `LabeledContent` reads as both).
    var text: String {
        "\(label) \(value as? String ?? "")"
    }
}
