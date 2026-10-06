import XCTest

/// `performAccessibilityAudit()` on every screen (J22.22), on the fake server. Serious issues
/// fail the test; everything else is printed as `a11y:` lines (in the log and the result bundle).
/// `TEST_RUNNER_VITAMUX_A11Y_SURVEY=1` prints everything without failing, for a review pass.
@MainActor
final class AccessibilityAuditUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = true }

    /// Missing or useless labels, wrong traits, and text VoiceOver cannot reach.
    static let serious: XCUIAccessibilityAuditType = [.sufficientElementDescription, .trait, .elementDetection]
    /// Reported, not failing yet: hit regions under 44 pt (range arrows, "Show as table", calendar
    /// days; the design pass J22.24 resizes them, then `.hitRegion` moves to `serious`), and
    /// contrast, clipped text and Dynamic Type, which the simulator's audit over-reports on
    /// system materials and charts. Element detection without an element (text the audit sees in
    /// a chart's axes or behind a sheet but cannot attribute) is reported too.
    static let reported: XCUIAccessibilityAuditType = [.hitRegion, .contrast, .textClipped, .dynamicType]

    private var survey: Bool { ProcessInfo.processInfo.environment["VITAMUX_A11Y_SURVEY"] == "1" }

    // MARK: Screens

    func testSignIn() throws {
        let app = XCUIApplication.launch()
        XCTAssertTrue(app.textFields["serverField"].waitForExistence(timeout: 10))
        try audit(app, "sign-in")
    }

    func testTwoFactorStep() throws {
        let app = XCUIApplication.launch("-uitest-totp")
        app.signIn()
        XCTAssertTrue(app.textFields["codeField"].waitForExistence(timeout: 10))
        try audit(app, "two-factor")
    }

    func testDashboard() throws { try open(nil, "dashboard") }
    func testExplore() throws { try open("vitamux://explore", "explore") }
    func testMetricDetail() throws { try open("vitamux://explore/heart_rate_resting?range=1M", "metric detail") }
    func testDayView() throws { try open("vitamux://explore/heart_rate?range=1D&end=\(ExploreUITests.day(-1))", "day view") }
    func testAllSourcesDay() throws { try open("vitamux://explore/heart_rate/day/\(ExploreUITests.day(-1))", "all-sources day") }
    func testSleep() throws { try open("vitamux://explore/sleep", "sleep") }
    func testBloodPressure() throws { try open("vitamux://explore/blood-pressure", "blood pressure") }
    func testBodyComposition() throws { try open("vitamux://explore/body-composition", "body composition") }
    func testWorkouts() throws { try open("vitamux://explore/workouts", "workouts") }
    func testEvents() throws { try open("vitamux://explore/events?code=ecg_recording", "events") }
    func testECG() throws { try open("vitamux://explore/ecg", "ecg") }
    func testBeats() throws { try open("vitamux://explore/beats?date=\(ExploreUITests.day(-2))", "beats") }
    func testActivityRings() throws { try open("vitamux://explore/activity-rings", "activity rings") }
    func testSources() throws { try open("vitamux://connections", "sources") }
    func testConnectionDetail() throws { try open("vitamux://connections/\(ConnectionDetailUITests.withings)", "connection detail") }
    func testLab() throws { try open("vitamux://lab", "lab") }
    func testLabReview() throws {
        try open("vitamux://lab/documents/doc_00000000000000000000000000000001", "lab review", arguments: "-uitest-lab-review")
    }
    func testLabResults() throws { try open("vitamux://lab/results", "lab results") }
    func testRules() throws { try open("vitamux://rules", "rules") }
    func testRule() throws { try open("vitamux://rules/heart_rate_resting", "rule") }
    func testRuleBuilder() throws { try open("vitamux://rules/new?metric=heart_rate_resting", "rule builder") }
    func testSettings() throws { try open("vitamux://settings", "settings") }
    func testSettingsSources() throws { try open("vitamux://settings/sources", "settings sources") }
    func testSettingsDevices() throws { try open("vitamux://settings/devices", "settings devices") }
    func testSettingsAI() throws { try open("vitamux://settings/ai", "settings ai") }
    func testSettingsAPIKeys() throws { try open("vitamux://settings/api-keys", "settings api keys") }
    func testSettingsSecurity() throws { try open("vitamux://settings/security", "settings security") }
    func testSettingsRetention() throws { try open("vitamux://settings/retention", "settings retention") }
    func testSettingsBackups() throws { try open("vitamux://settings/backups", "settings backups") }
    func testSystemStatus() throws { try open("vitamux://settings/system", "system status") }
    func testAppSettings() throws { try open("vitamux://settings/app", "app settings") }
    func testAppleHealth() throws { try open("vitamux://apple-health", "apple health", arguments: "-uitest-paired") }
    func testAppleHealthSources() throws { try open("vitamux://apple-health/sources", "apple health sources", arguments: "-uitest-paired") }

    func testMore() throws {
        let app = signedIn()
        app.tabBars.buttons["More"].tap()
        settle(app)
        try audit(app, "more")
    }

    func testPointSheet() throws {
        let app = signedIn()
        app.openLink("vitamux://explore/heart_rate_resting?range=1M")
        let row = app.buttons["valueRow-\(ExploreUITests.day(-1))"]
        XCTAssertTrue(app.scrollTo(row).waitForExistence(timeout: 10))
        row.tap()
        XCTAssertTrue(app.staticTexts["pointSource"].waitForExistence(timeout: 10))
        settle(app)
        try audit(app, "point sheet")
    }

    // MARK: Helpers

    private func signedIn(_ arguments: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-uitest"] + arguments
        app.launch()
        app.signInToDashboard()
        return app
    }

    private func open(_ link: String?, _ screen: String, arguments: String...) throws {
        let app = signedIn(arguments)
        if let link { app.openLink(link) }
        settle(app)
        try audit(app, screen)
    }

    /// Waits for spinners to go and the screen to stop moving.
    private func settle(_ app: XCUIApplication) {
        sleep(2)
        let idle = NSPredicate(format: "count == 0")
        _ = XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: idle, object: app.activityIndicators)], timeout: 10)
        sleep(1)
    }

    private func audit(_ app: XCUIApplication, _ screen: String) throws {
        let types: XCUIAccessibilityAuditType = Self.serious.union(Self.reported)
        try app.performAccessibilityAudit(for: types) { issue in
            let element = issue.element.map { "\($0.elementType.rawValue) id=\($0.identifier) label=\($0.label) frame=\($0.frame)" } ?? "none: \(issue.detailedDescription)"
            let serious = Self.serious.contains(issue.auditType) && (issue.auditType != .elementDetection || issue.element != nil)
            print("a11y: [\(screen)] \(serious ? "SERIOUS" : "reported") type=\(issue.auditType.rawValue) \(issue.compactDescription) | \(element)")
            return self.survey || !serious
        }
    }
}
