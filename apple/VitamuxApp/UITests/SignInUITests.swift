import XCTest

/// The server step and sign-in on the fake server.
@MainActor
final class SignInUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    func testFirstRunReachesTheDashboard() {
        let app = XCUIApplication.launch()
        XCTAssertFalse(app.element("sessionExpired").exists)
        app.signInToDashboard()
        XCTAssertTrue(app.navigationBars["Dashboard"].exists)
    }

    func testPlainHTTPIsRefused() {
        let app = XCUIApplication.launch()
        app.signIn(server: "http://vitamux.example.org")
        let problem = app.element("serverProblem")
        XCTAssertTrue(problem.waitForExistence(timeout: Wait.ui))
        XCTAssertTrue(problem.label.contains("https://"), problem.label)
    }

    func testUnreachableServer() {
        let app = XCUIApplication.launch()
        app.signIn(server: "offline.vitamux.test")
        let problem = app.element("serverProblem")
        XCTAssertTrue(problem.waitForExistence(timeout: Wait.server))
        XCTAssertTrue(problem.label.contains("Can't reach the server"), problem.label)
    }

    func testSomethingElseAtTheAddress() {
        let app = XCUIApplication.launch()
        app.signIn(server: "other.vitamux.test")
        let problem = app.element("serverProblem")
        XCTAssertTrue(problem.waitForExistence(timeout: Wait.server))
        XCTAssertTrue(problem.label.contains("not a Vitamux server"), problem.label)
    }

    func testServerFromBeforeTheHandshakeIsTooOld() {
        let app = XCUIApplication.launch()
        app.signIn(server: "old.vitamux.test")
        let problem = app.element("serverProblem")
        XCTAssertTrue(problem.waitForExistence(timeout: Wait.server))
        XCTAssertTrue(problem.label.contains("needs an update"), problem.label)
    }

    func testTwoFactorCode() {
        let app = XCUIApplication.launch("-uitest-totp")
        app.signIn()
        let code = app.textFields["codeField"]
        XCTAssertTrue(code.waitForExistence(timeout: Wait.server), "totp_required asks for the code")
        code.tap()
        code.typeText(Fake.totp)
        app.buttons["signInButton"].tap()
        XCTAssertTrue(app.tabBars.buttons["Dashboard"].waitForExistence(timeout: Wait.server))
    }

    func testRecoveryCode() {
        let app = XCUIApplication.launch("-uitest-totp")
        app.signIn()
        XCTAssertTrue(app.textFields["codeField"].waitForExistence(timeout: Wait.server))
        app.buttons["recoveryToggle"].tap()
        let code = app.textFields["codeField"]
        code.tap()
        code.typeText(Fake.recovery)
        app.buttons["signInButton"].tap()
        XCTAssertTrue(app.tabBars.buttons["Dashboard"].waitForExistence(timeout: Wait.server))
    }

    func testWrongPasswordThenRateLimit() {
        let app = XCUIApplication.launch()
        app.signIn(password: "wrong-synthetic")
        let problem = app.element("signInProblem")
        XCTAssertTrue(problem.waitForExistence(timeout: Wait.server))
        XCTAssertTrue(problem.label.contains("invalid username, password or code"), problem.label)
        // The fake locks after three failures; the fourth attempt is told to wait.
        for _ in 0..<3 {
            app.buttons["signInButton"].tap()
        }
        XCTAssertTrue(app.element("rateLimit").waitForExistence(timeout: Wait.server))
        XCTAssertFalse(app.buttons["signInButton"].isEnabled, "sign-in waits for Retry-After")
    }

    func testSessionExpiryMidUse() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.openLink("vitamux://uitest/expire-sessions")
        // The next request answers 401: back to sign-in, keeping the server.
        app.buttons["searchButton"].firstMatch.tap()
        XCTAssertTrue(app.element("sessionExpired").waitForExistence(timeout: Wait.server))
        XCTAssertEqual(app.textFields["serverField"].value as? String, "https://\(Fake.server)")
    }
}
