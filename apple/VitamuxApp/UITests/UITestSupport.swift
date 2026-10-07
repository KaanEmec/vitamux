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

/// How long a test waits for what a slow machine shows late. GitHub's hosted macOS runners are two
/// to three times slower than a laptop, so nothing that appears after a load, a presentation or a
/// request may be read or tapped without waiting for it. A wait ends as soon as the element shows,
/// so a generous bound costs nothing when the app is fast.
enum Wait {
    /// A sheet, dialog, menu, pushed screen or a link's screen being presented.
    static let ui: TimeInterval = 10
    /// What the fake server provides: a list's rows, a loaded value, the answer to an action.
    static let server: TimeInterval = 30
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
        XCTAssertTrue(field.waitForExistence(timeout: Wait.server))
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
        XCTAssertTrue(tabBars.buttons["Dashboard"].waitForExistence(timeout: Wait.server), "the shell shows after sign-in")
    }

    func element(_ identifier: String) -> XCUIElement {
        descendants(matching: .any)[identifier].firstMatch
    }

    /// Opens a `vitamux://` link in the running app, as a widget or notification does.
    /// (`XCUIApplication.open` relaunches the app, which ends the fake server's session.)
    func openLink(_ link: String) {
        XCUIDevice.shared.system.open(URL(string: link)!)
    }

    /// Swipes up until `element` can be tapped (lists load rows lazily). The screen's spinners clear
    /// first (up to `timeout`): swipes would run past a list that has no rows yet, and a section
    /// that finishes loading above `element` moves it after it was found.
    @discardableResult
    func scrollTo(_ element: XCUIElement, timeout: TimeInterval = Wait.server) -> XCUIElement {
        _ = element.waitForExistence(timeout: 1)
        _ = activityIndicators.firstMatch.waitForNonExistence(timeout: timeout)
        for _ in 0..<6 where !(element.exists && element.isHittable) {
            swipeUp()
        }
        return element
    }

    /// A confirmation dialog's button: iOS lists it more than once, so take the first.
    func dialogButton(_ label: String) -> XCUIElement {
        buttons.matching(NSPredicate(format: "label == %@", label)).firstMatch
    }
}

extension XCUIElement {
    /// Waits for the element (a sheet's or dialog's control, a menu item, a row a request brings),
    /// then taps it: a bare `tap()` fails at once on an element a slow machine has not shown yet.
    func tapWhenReady(timeout: TimeInterval = Wait.ui, file: StaticString = #filePath, line: UInt = #line) {
        XCTAssertTrue(waitForExistence(timeout: timeout), "\(self) shows", file: file, line: line)
        tap()
    }

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
