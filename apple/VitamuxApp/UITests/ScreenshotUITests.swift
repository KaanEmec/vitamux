import XCTest

/// The README's screenshots (docs/images/ios), from the fake server's synthetic data. Runs only
/// when the test runner has `SCREENSHOT_DIR` (pass `TEST_RUNNER_SCREENSHOT_DIR=<dir>` to
/// xcodebuild); `scripts/screenshots.sh` does that and shrinks the PNGs.
@MainActor
final class ScreenshotUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    func testREADMEScreenshots() throws {
        guard let path = ProcessInfo.processInfo.environment["SCREENSHOT_DIR"], !path.isEmpty else {
            throw XCTSkip("SCREENSHOT_DIR is not set")
        }
        let dir = URL(fileURLWithPath: path)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let app = XCUIApplication.launch()

        XCTAssertTrue(app.textFields["serverField"].waitForExistence(timeout: 10))
        try shoot("sign-in", to: dir)

        app.signInToDashboard()
        XCTAssertTrue(app.buttons["card-sleep"].waitForExistence(timeout: 10))
        try shoot("dashboard", to: dir)

        let screens: [(name: String, link: String, title: String)] = [
            ("explore", "vitamux://explore", "Explore"),
            ("metric", "vitamux://explore/resting_heart_rate?range=3M", "Resting heart rate"),
            ("sleep", "vitamux://explore/sleep", "Sleep"),
            ("ecg", "vitamux://explore/ecg/00000000-0000-4000-8000-000000000103", "ECG recording"),
            ("sources", "vitamux://connections", "Sources"),
            ("apple-health", "vitamux://apple-health", "Apple Health"),
            ("lab", "vitamux://lab", "Lab"),
        ]
        for screen in screens {
            app.openLink(screen.link)
            XCTAssertTrue(app.navigationBars[screen.title].waitForExistence(timeout: 10), screen.link)
            settle()
            try shoot(screen.name, to: dir)
        }

        // More, then the light theme on the dashboard's Customize sheet (artboard DashboardEdit).
        app.tabBars.buttons["More"].tap()
        app.tabBars.buttons["More"].tap()
        XCTAssertTrue(app.navigationBars["More"].waitForExistence(timeout: 5))
        settle()
        try shoot("more", to: dir)
        app.buttons["themePicker"].tap()
        app.buttons["Light"].tap()
        app.tabBars.buttons["Dashboard"].tap()
        XCTAssertTrue(app.buttons["card-sleep"].waitForExistence(timeout: 10))
        settle()
        try shoot("dashboard-light", to: dir)
        app.buttons["customizeButton"].tap()
        XCTAssertTrue(app.navigationBars["Customize"].waitForExistence(timeout: 5))
        settle()
        try shoot("customize-light", to: dir)
    }

    /// Lets loads and transitions finish.
    private func settle() {
        _ = XCUIApplication().wait(for: .runningForeground, timeout: 1)
        Thread.sleep(forTimeInterval: 1.5)
    }

    private func shoot(_ name: String, to dir: URL) throws {
        let png = XCUIScreen.main.screenshot().pngRepresentation
        try png.write(to: dir.appendingPathComponent("\(name).png"))
    }
}
