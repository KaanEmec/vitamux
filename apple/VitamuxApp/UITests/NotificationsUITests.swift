import XCTest

/// Local notifications (J22.21) on the fake server. The simulator's notification centre cannot be
/// inspected, so under `-uitest` the app records what it would post and lists it in Settings ›
/// This app; tapping an entry follows its link as the system's tap does. Real delivery, the
/// system tap and the background refresh are checked on a device (J22.23). Values are synthetic.
@MainActor
final class NotificationsUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    /// Signs in with `arguments` and opens Settings › This app.
    private func launch(_ arguments: String...) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-uitest"] + arguments
        app.launch()
        app.signInToDashboard()
        openAppSettings(app)
        return app
    }

    private func openAppSettings(_ app: XCUIApplication) {
        app.openLink("vitamux://settings/app")
        XCTAssertTrue(app.navigationBars["This app"].waitForExistence(timeout: Wait.ui))
    }

    private func waitForPosted(_ count: Int, in app: XCUIApplication, file: StaticString = #filePath, line: UInt = #line) {
        let label = reveal(app.staticTexts["notificationsPosted"], in: app)
        let posted = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label == %@", "\(count) posted"), object: label)
        XCTAssertEqual(XCTWaiter.wait(for: [posted], timeout: Wait.server), .completed, "\(count) posted", file: file, line: line)
    }

    /// Scrolls the form until `element` can be tapped: down the form first, then back up.
    @discardableResult
    private func reveal(_ element: XCUIElement, in app: XCUIApplication) -> XCUIElement {
        app.scrollTo(element)
        for _ in 0..<6 where !(element.exists && element.isHittable) {
            app.swipeDown()
        }
        return element
    }

    /// Allows notifications from the permission row: it is not asked at launch, and nothing is
    /// posted before it is allowed.
    private func allow(_ app: XCUIApplication) {
        XCTAssertEqual(app.scrollTo(app.staticTexts["notificationsPosted"]).label, "0 posted", "nothing is posted before it is allowed")
        let allow = reveal(app.buttons["allowNotifications"], in: app)
        XCTAssertTrue(allow.waitForExistence(timeout: Wait.ui), "the permission waits to be asked in context")
        allow.tap()
        XCTAssertTrue(app.element("notificationPermission").waitForExistence(timeout: Wait.ui))
    }

    /// Category, the screen its tap opens, and that screen's tab.
    private let links: [(String, String, String)] = [
        ("notifications.connectionAttention", "Garmin Connect", "Sources"), // needs reauthorization
        ("notifications.failedJob", "Garmin Connect", "Sources"), // its history
        ("notifications.labReady", "Review", "Lab"),
        ("notifications.staleBackup", "Backups and export", "More"),
        ("notifications.uploadStalled", "Apple Health", "More"), // the device token was revoked
    ]

    func testEachCategoryOpensTheScreenThatFixesIt() {
        let app = launch("-uitest-paired", "-uitest-revoked", "-uitest-lab-review")
        allow(app)
        waitForPosted(links.count, in: app)
        for (category, title, tab) in links {
            openAppSettings(app)
            let notice = reveal(app.buttons["notice-\(category)"], in: app)
            XCTAssertTrue(notice.waitForExistence(timeout: Wait.ui), category)
            notice.tap()
            XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: Wait.server), "\(category) opens \(title)")
            XCTAssertTrue(app.tabBars.buttons[tab].isSelected, "\(category) is on \(tab)")
        }
    }

    func testAConditionNotifiesOnceAndACategoryTurnedOffIsWithdrawn() {
        let app = launch()
        allow(app)
        // Garmin needs reauthorization, a job failed permanently, the backup is ten days old.
        waitForPosted(3, in: app)
        reveal(app.buttons["checkNotifications"], in: app).tap()
        let settle = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label != '3 posted'"),
                                               object: app.staticTexts["notificationsPosted"])
        XCTAssertEqual(XCTWaiter.wait(for: [settle], timeout: 3), .timedOut, "the same conditions are not posted again")

        reveal(app.switches["notifications.staleBackup"], in: app).switches.firstMatch.tap()
        XCTAssertTrue(reveal(app.buttons["notice-notifications.connectionAttention"], in: app).exists)
        XCTAssertTrue(app.buttons["notice-notifications.staleBackup"].waitForNonExistence(timeout: Wait.ui), "turned off, withdrawn")
        waitForPosted(3, in: app)

        reveal(app.switches["notifications.staleBackup"], in: app).switches.firstMatch.tap()
        waitForPosted(4, in: app)
        XCTAssertTrue(reveal(app.buttons["notice-notifications.staleBackup"], in: app).exists, "turned on, the current condition notifies")
    }
}
