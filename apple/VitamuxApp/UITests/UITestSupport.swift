import XCTest

/// Launch and sign-in helpers. Every test runs the app with `-uitest`: the kit's fake server
/// (`FakeServer.uiTestServers`), an in-memory session and cleared preferences. Values are synthetic.
enum Fake {
    static let server = "fake.vitamux.test"
    static let username = "owner"
    static let password = "synthetic-password"
    static let totp = "123456"
    static let recovery = "synthetic-recovery-1"

    /// A local date `offset` days from today in the fixtures' timezone (the app runs in it under
    /// `-uitest`, and the stack smoke's synthetic week is written in it), as links write it.
    static func day(_ offset: Int = 0) -> String {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "Europe/Berlin")!
        let date = calendar.date(byAdding: .day, value: offset, to: .now)!
        let parts = calendar.dateComponents([.year, .month, .day], from: date)
        return String(format: "%04d-%02d-%02d", parts.year!, parts.month!, parts.day!)
    }
}

extension XCUIApplication {
    static func launch(_ arguments: String...) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-uitest"] + arguments
        app.launch()
        return app
    }

    /// Fills the sign-in screen and taps Sign in.
    func signIn(server: String = Fake.server, username: String = Fake.username, password: String = Fake.password) {
        let field = textFields["serverField"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        field.replaceText(server)
        textFields["usernameField"].replaceText(username)
        let secure = secureTextFields["passwordField"]
        secure.tap()
        secure.typeText(password)
        buttons["signInButton"].tap()
    }

    /// Signs in to the fake and waits for the tab bar.
    func signInToDashboard() {
        signIn()
        XCTAssertTrue(tabBars.buttons["Dashboard"].waitForExistence(timeout: 10), "the shell shows after sign-in")
    }

    func element(_ identifier: String) -> XCUIElement {
        descendants(matching: .any)[identifier].firstMatch
    }

    /// Opens a `vitamux://` link in the running app, as a widget or notification does.
    /// (`XCUIApplication.open` relaunches the app, which ends the fake server's session.)
    func openLink(_ link: String) {
        XCUIDevice.shared.system.open(URL(string: link)!)
    }

    /// Swipes up until `element` can be tapped (lists load rows lazily).
    @discardableResult
    func scrollTo(_ element: XCUIElement) -> XCUIElement {
        for _ in 0..<6 where !(element.exists && element.isHittable) {
            swipeUp()
        }
        return element
    }
}

extension XCUIElement {
    /// Clears the field and types `text`.
    func replaceText(_ text: String) {
        // Tap at the trailing edge, so the cursor sits after the current text.
        coordinate(withNormalizedOffset: CGVector(dx: 0.98, dy: 0.5)).tap()
        // An empty field reports its placeholder as its value, so delete by length regardless.
        let current = (value as? String) ?? ""
        typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: current.count))
        typeText(text)
    }
}
