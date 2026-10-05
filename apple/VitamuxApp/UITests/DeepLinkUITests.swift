import XCTest

/// Every `vitamux://` link in docs/architecture/ios-app-screens.md opens its screen on its tab.
@MainActor
final class DeepLinkUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    /// Link, tab, navigation title, and what the screen shows of the link's query.
    private let links: [(String, String, String, String?)] = [
        ("vitamux://dashboard?date=2026-01-31", "Dashboard", "Dashboard", nil), // the day: DashboardUITests
        ("vitamux://explore", "Explore", "Explore", nil),
        ("vitamux://explore/heart_rate_resting?range=3M", "Explore", "Heart rate resting", nil),
        ("vitamux://explore/heart_rate/day/2026-01-31", "Explore", "Heart rate, 2026-01-31", nil),
        ("vitamux://explore/sleep", "Explore", "Sleep", nil),
        ("vitamux://explore/blood-pressure", "Explore", "Blood pressure", nil),
        ("vitamux://explore/body-composition", "Explore", "Body composition", nil),
        ("vitamux://explore/workouts", "Explore", "Workouts", nil),
        ("vitamux://explore/events?code=sleep_session", "Explore", "Events", nil), // the filter: SpecialisedUITests
        ("vitamux://connections?connected=withings", "Sources", "Sources", nil), // the banner: SourcesUITests
        ("vitamux://connections/conn_00000000000000000000000000000001?tab=backfills", "Sources", "Withings", nil),
        ("vitamux://lab", "Lab", "Lab", nil),
        ("vitamux://lab/documents/doc-synthetic", "Lab", "Review", "doc-synthetic"),
        ("vitamux://lab/results", "Lab", "Results", nil),
        ("vitamux://lab/analytes/ldl", "Lab", "Analyte history", nil), // the view: SpecialisedUITests
        ("vitamux://rules", "More", "Rules", nil),
        ("vitamux://rules/heart_rate", "More", "Rule", "heart_rate"),
        ("vitamux://rules/new", "More", "New rule", nil),
        ("vitamux://settings", "More", "Profile", nil),
        ("vitamux://settings/sources", "More", "Sources", nil),
        ("vitamux://settings/devices", "More", "Devices", nil),
        ("vitamux://settings/ai", "More", "AI providers", nil),
        ("vitamux://settings/api-keys", "More", "API keys", nil),
        ("vitamux://settings/security", "More", "Security", nil),
        ("vitamux://settings/retention", "More", "Retention", nil),
        ("vitamux://settings/backups", "More", "Backups and export", nil),
        ("vitamux://settings/system", "More", "System status", nil),
        ("vitamux://settings/app", "More", "This app", nil),
        ("vitamux://apple-health", "More", "Apple Health", nil),
        // An unknown path opens its tab's root.
        ("vitamux://lab/unknown/path", "Lab", "Lab", nil),
    ]

    func testEveryLinkOpensItsScreen() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        for (link, tab, title, detail) in links {
            app.openLink(link)
            XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: 5), "\(link) shows \(title)")
            XCTAssertTrue(app.tabBars.buttons[tab].isSelected, "\(link) is on \(tab)")
            if let detail {
                XCTAssertEqual(app.staticTexts["placeholderDetail"].label, detail, link)
            }
        }
    }

    func testLinkToAMetricWhileSignedOutOpensAfterSignIn() {
        let app = XCUIApplication.launch()
        XCTAssertTrue(app.textFields["serverField"].waitForExistence(timeout: 10))
        app.openLink("vitamux://explore/heart_rate_resting?range=3M")
        XCTAssertTrue(app.textFields["serverField"].waitForExistence(timeout: 5), "the link waits for sign-in")
        app.signIn()
        XCTAssertTrue(app.navigationBars["Heart rate resting"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.tabBars.buttons["Explore"].isSelected)
    }
}
