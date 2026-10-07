import XCTest

/// Every `vitamux://` link in docs/architecture/ios-app-screens.md opens its screen on its tab.
@MainActor
final class DeepLinkUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    /// Link, tab, and navigation title.
    private let links: [(String, String, String)] = [
        ("vitamux://dashboard?date=2026-01-31", "Dashboard", "Dashboard"), // the day: DashboardUITests
        ("vitamux://explore", "Explore", "Explore"),
        ("vitamux://explore/resting_heart_rate?range=3M", "Explore", "Resting heart rate"),
        ("vitamux://explore/heart_rate/day/2026-01-31", "Explore", "Heart rate, 2026-01-31"),
        ("vitamux://explore/sleep", "Explore", "Sleep"),
        ("vitamux://explore/blood-pressure", "Explore", "Blood pressure"),
        ("vitamux://explore/body-composition", "Explore", "Body composition"),
        ("vitamux://explore/workouts", "Explore", "Workouts"),
        ("vitamux://explore/events?code=sleep_session", "Explore", "Events"), // the filter: SpecialisedUITests
        // Apple Watch views: WatchUITests
        ("vitamux://explore/ecg", "Explore", "ECG"),
        ("vitamux://explore/ecg/00000000-0000-4000-8000-000000000103", "Explore", "ECG recording"),
        ("vitamux://explore/beats?date=2026-01-31", "Explore", "Beat-to-beat"),
        ("vitamux://explore/activity-rings", "Explore", "Activity rings"),
        ("vitamux://explore/state-of-mind", "Explore", "State of Mind"),
        ("vitamux://explore/workouts/unknown-workout", "Explore", "Workout"),
        ("vitamux://connections?connected=withings", "Sources", "Sources"), // the banner: SourcesUITests
        ("vitamux://connections/conn_00000000000000000000000000000001?tab=backfills", "Sources", "Withings"),
        ("vitamux://lab", "Lab", "Lab"),
        ("vitamux://lab/documents/doc_00000000000000000000000000000001", "Lab", "Review"), // the review: LabUITests
        ("vitamux://lab/results", "Lab", "Results"), // the results: LabUITests
        ("vitamux://lab/analytes/ldl", "Lab", "Analyte history"), // the view: SpecialisedUITests
        ("vitamux://rules", "More", "Rules"),
        ("vitamux://rules/heart_rate", "More", "heart_rate"), // the page: RulesUITests
        ("vitamux://rules/new?metric=steps", "More", "Rule builder"),
        ("vitamux://settings", "More", "Profile"),
        ("vitamux://settings/sources", "More", "Sources"),
        ("vitamux://settings/devices", "More", "Devices"),
        ("vitamux://settings/ai", "More", "AI providers"),
        ("vitamux://settings/api-keys", "More", "API keys"),
        ("vitamux://settings/security", "More", "Security"),
        ("vitamux://settings/retention", "More", "Retention"),
        ("vitamux://settings/backups", "More", "Backups and export"),
        ("vitamux://settings/system", "More", "System status"),
        ("vitamux://settings/app", "More", "This app"),
        ("vitamux://apple-health", "More", "Apple Health"),
        // An unknown path opens its tab's root.
        ("vitamux://lab/unknown/path", "Lab", "Lab"),
    ]

    func testEveryLinkOpensItsScreen() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        for (link, tab, title) in links {
            app.openLink(link)
            XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: Wait.server), "\(link) shows \(title)")
            XCTAssertTrue(app.tabBars.buttons[tab].isSelected, "\(link) is on \(tab)")
        }
    }

    func testLinkToAMetricWhileSignedOutOpensAfterSignIn() {
        let app = XCUIApplication.launch()
        XCTAssertTrue(app.textFields["serverField"].waitForExistence(timeout: 10))
        app.openLink("vitamux://explore/resting_heart_rate?range=3M")
        XCTAssertTrue(app.textFields["serverField"].waitForExistence(timeout: Wait.ui), "the link waits for sign-in")
        app.signIn()
        XCTAssertTrue(app.navigationBars["Resting heart rate"].waitForExistence(timeout: Wait.server))
        XCTAssertTrue(app.tabBars.buttons["Explore"].isSelected)
    }
}
