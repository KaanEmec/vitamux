import XCTest

/// Sources, the OAuth return banners, Connect a source (E20 setup states, the Withings app wizard,
/// the sidecar card, prompt steps with an MFA code) and reauthorization, on the fake server
/// (FakeServer+Sources.swift). Seed: Withings healthy, Garmin Connect needing reauthorization,
/// WHOOP's sidecar off. Values are synthetic.
@MainActor
final class SourcesUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    static let login = (email: "synthetic@example.test", password: "synthetic-pass", code: "654321")

    private func openSources(_ arguments: String...) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-uitest"] + arguments
        app.launch()
        app.signInToDashboard()
        app.tabBars.buttons["Sources"].tap()
        XCTAssertTrue(app.buttons["connection-withings"].waitForExistence(timeout: 10))
        return app
    }

    private func waitForLabel(_ element: XCUIElement, containing text: String, file: StaticString = #filePath, line: UInt = #line) {
        let matched = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label CONTAINS %@", text), object: element)
        XCTAssertEqual(XCTWaiter.wait(for: [matched], timeout: 15), .completed, "\(element) says \(element.label), not \(text)", file: file, line: line)
    }

    /// Answers a prompt step: types each value into its field and continues.
    private func answer(_ app: XCUIApplication, _ values: [(String, String)]) {
        for (name, value) in values {
            let field = app.descendants(matching: .any)["promptField-\(name)"].firstMatch
            XCTAssertTrue(field.waitForExistence(timeout: 10), "the prompt asks for \(name)")
            field.tap()
            field.typeText(value)
        }
        app.buttons["promptContinue"].tap()
    }

    func testListShowsSummaryCardsBackfillsAndRuns() {
        let app = openSources()
        XCTAssertEqual(app.staticTexts["sourcesSummary"].label, "2 sources · 1 healthy · 1 needs attention")
        XCTAssertTrue(app.buttons["thisIPhone"].exists)
        XCTAssertTrue(app.element("runStrip").exists)
        XCTAssertTrue(app.buttons["syncNow-withings"].exists)
        XCTAssertTrue(app.buttons["reauthorize-garmin"].exists, "Garmin's fix-it action")
        XCTAssertFalse(app.buttons["syncNow-garmin"].exists, "a card that needs reauthorization offers only that")

        app.buttons["syncNow-withings"].tap()
        let queued = app.element("syncQueued")
        XCTAssertTrue(queued.waitForExistence(timeout: 10))
        XCTAssertTrue(queued.label.contains("withings.measures (queued)"), queued.label)

        let running = app.scrollTo(app.buttons["runningBackfill-withings.measures"])
        XCTAssertTrue(running.exists, "the running backfill is listed")
        XCTAssertTrue(app.scrollTo(app.staticTexts["Recent sync runs"]).exists)
        running.tap()
        XCTAssertTrue(app.navigationBars["Withings"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["tab-backfills"].isSelected, "the backfill opens its connection's Backfills tab")
    }

    func testEveryReturnBanner() {
        let app = openSources()
        let errors = [
            ("invalid_state", "The authorization link expired or was already used. Start again."),
            ("denied", "Access was not granted at the provider."),
            ("account_mismatch", "You signed in to a different account than this connection uses. Nothing changed."),
            ("exchange_failed", "The provider did not accept the authorization. Try again later."),
            ("unavailable", "The provider or Vitamux could not complete the connection right now. Try again later."),
            ("something_new", "the provider answered something_new."),
        ]
        for (code, text) in errors {
            app.openLink("vitamux://connections?auth_error=\(code)&provider=withings")
            let banner = app.element("bannerAuthError")
            XCTAssertTrue(banner.waitForExistence(timeout: 10), code)
            waitForLabel(banner, containing: "Connecting Withings failed: \(text)")
            XCTAssertEqual(app.buttons["bannerRetry"].label, "Try again", "Withings has no app credentials of its own here")
        }
        app.buttons["bannerRetry"].tap()
        XCTAssertTrue(app.buttons["provider-withings"].waitForExistence(timeout: 10), "Try again opens Connect a source")
        app.buttons["Cancel"].tap()

        app.openLink("vitamux://connections?connected=withings")
        let connected = app.element("bannerConnected")
        XCTAssertTrue(connected.waitForExistence(timeout: 10))
        XCTAssertTrue(connected.label.contains("Withings is connected. A first sync has been queued."))
        app.buttons["bannerDismiss"].tap()
        XCTAssertTrue(connected.waitForNonExistence(timeout: 10))

        app.openLink("vitamux://connections?removed=garmin")
        XCTAssertTrue(app.element("bannerRemoved").waitForExistence(timeout: 10))
        XCTAssertTrue(app.element("bannerRemoved").label.contains("The Garmin Connect connection was removed."))
    }

    /// Deleting Withings with its data leaves it without app credentials: the wizard, a refused
    /// secret, the right one, then OAuth through the auth browser back to the app.
    func testConnectWithingsThroughTheAppWizard() {
        let app = openSources()
        app.buttons["connection-withings"].tap()
        app.buttons["tab-settings"].tap()
        app.scrollTo(app.buttons["removeConnection"]).tap()
        app.buttons["delete-delete"].tap()
        app.switches["confirmDelete"].switches.firstMatch.tap()
        app.buttons["submitDelete"].tap()
        let removed = app.element("bannerRemoved")
        XCTAssertTrue(removed.waitForExistence(timeout: 10))
        XCTAssertTrue(removed.label.contains("The Withings connection was removed."))
        XCTAssertFalse(app.buttons["connection-withings"].exists)

        app.scrollTo(app.buttons["connectSource"]).tap()
        let state = app.element("setupState-withings")
        XCTAssertTrue(state.waitForExistence(timeout: 10))
        XCTAssertTrue(state.label.contains("Needs its app credentials"))
        XCTAssertTrue(app.element("setupState-whoop").label.contains("Not available: its sidecar is not running"))
        XCTAssertTrue(app.element("setupState-garmin").label.contains("Connected"))
        XCTAssertEqual(app.scrollTo(app.buttons["connectNext"]).label, "Set up Withings")
        app.buttons["connectNext"].tap()

        XCTAssertTrue(app.staticTexts["wizardStep"].waitForExistence(timeout: 5))
        XCTAssertEqual(app.staticTexts["wizardStep"].label, "Step 1 of 3 · Create the app")
        XCTAssertEqual(app.staticTexts["value-Callback URL"].label, "https://fake.vitamux.test/oauth/withings/callback")
        app.scrollTo(app.buttons["wizardNext"]).tap()

        let id = app.textFields["clientIdField"]
        XCTAssertTrue(id.waitForExistence(timeout: 5))
        id.tap()
        id.typeText("synthetic-client-id")
        let secret = app.secureTextFields["clientSecretField"]
        secret.tap()
        secret.typeText("synthetic-wrong-secret")
        app.buttons["saveAppCredentials"].tap()
        let check = app.element("appCheck")
        XCTAssertTrue(check.waitForExistence(timeout: 10))
        XCTAssertTrue(check.label.contains("refused"), check.label)
        XCTAssertEqual(secret.value as? String, "Client secret", "the secret is cleared once sent")

        app.scrollTo(secret).tap()
        secret.typeText("synthetic-client-secret\n") // Return saves too
        XCTAssertTrue(app.buttons["wizardConnect"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.staticTexts["wizardStep"].label, "Step 3 of 3 · Connect your account")
        XCTAssertTrue(app.element("appCheck").label.contains("accepted"))
        app.buttons["wizardConnect"].tap()

        let connected = app.element("bannerConnected")
        XCTAssertTrue(connected.waitForExistence(timeout: 10), "the auth browser returns to vitamux://connections?connected=withings")
        XCTAssertTrue(connected.label.contains("Withings is connected."))
        XCTAssertTrue(app.buttons["connection-withings"].waitForExistence(timeout: 10))

        // A refused exchange with the owner's own app points at its setup.
        app.openLink("vitamux://connections?auth_error=exchange_failed&provider=withings")
        let review = app.buttons["bannerRetry"]
        XCTAssertTrue(review.waitForExistence(timeout: 10))
        waitForLabel(review, containing: "Review the app setup")
        review.tap()
        XCTAssertTrue(app.staticTexts["wizardStep"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.staticTexts["wizardStep"].label, "Step 1 of 3 · Create the app")
    }

    func testAppCredentialsManagedByTheEnvironment() {
        let app = openSources("-uitest-withings-env")
        app.openLink("vitamux://connections/conn_00000000000000000000000000000001?tab=settings")
        app.scrollTo(app.buttons["removeConnection"]).tap()
        app.buttons["delete-delete"].tap()
        app.switches["confirmDelete"].switches.firstMatch.tap()
        app.buttons["submitDelete"].tap()
        XCTAssertTrue(app.element("bannerRemoved").waitForExistence(timeout: 10))

        app.scrollTo(app.buttons["connectSource"]).tap()
        let state = app.element("setupState-withings")
        XCTAssertTrue(state.waitForExistence(timeout: 10))
        XCTAssertTrue(state.label.contains("Ready to connect"), state.label)
        XCTAssertEqual(app.scrollTo(app.buttons["connectNext"]).label, "Continue to Withings")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'It uses the app credentials set by the environment.'")).firstMatch.exists)
        app.buttons["connectNext"].tap()
        XCTAssertTrue(app.element("bannerConnected").waitForExistence(timeout: 10))

        app.openLink("vitamux://connections?auth_error=exchange_failed&provider=withings")
        XCTAssertTrue(app.buttons["bannerRetry"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.buttons["bannerRetry"].label, "Try again", "credentials of the environment are not the app's to review")
    }

    /// WHOOP's sidecar answers after Check again; its sign-in asks for a code, a wrong code ends
    /// the flow, and the second try connects it, paused like every new unofficial connection.
    func testSidecarCheckAgainThenPromptStepsWithAnMFACode() {
        let app = openSources()
        app.scrollTo(app.buttons["connectSource"]).tap()
        let whoop = app.buttons["provider-whoop"]
        XCTAssertTrue(whoop.waitForExistence(timeout: 10))
        XCTAssertFalse(whoop.isEnabled, "a provider whose sidecar is off cannot be chosen")
        XCTAssertEqual(app.staticTexts["value-Add this line to .env"].label, "COMPOSE_PROFILES=whoop")

        app.scrollTo(app.buttons["checkAgain-whoop"]).tap()
        waitForLabel(app.staticTexts["checked-whoop"], containing: "still does not answer")
        app.buttons["checkAgain-whoop"].tap()
        waitForLabel(app.element("setupState-whoop"), containing: "Ready to connect")
        app.scrollTo(whoop).tap()
        XCTAssertEqual(app.scrollTo(app.buttons["connectNext"]).label, "Continue to WHOOP")
        app.scrollTo(app.buttons["connectNext"]).tap()

        answer(app, [("email", Self.login.email), ("password", Self.login.password)])
        answer(app, [("code", "111111")])
        let problem = app.element("problem")
        XCTAssertTrue(problem.waitForExistence(timeout: 10))
        XCTAssertTrue(problem.label.contains("WHOOP did not accept the verification code"), problem.label)
        app.buttons["promptRestart"].tap()

        XCTAssertTrue(app.buttons["provider-whoop"].waitForExistence(timeout: 10), "start again goes back to the choice")
        app.scrollTo(app.buttons["connectNext"]).tap()
        answer(app, [("email", Self.login.email), ("password", Self.login.password)])
        XCTAssertTrue(app.descendants(matching: .any)["promptField-code"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.descendants(matching: .any)["promptField-email"].exists, false, "each step clears the last one's fields")
        answer(app, [("code", Self.login.code)])

        XCTAssertTrue(app.navigationBars["WHOOP"].waitForExistence(timeout: 10), "a finished sign-in opens the connection")
        waitForLabel(app.element("detailHealth"), containing: "Paused")
    }

    func testReauthorizeGarminWithItsPrompts() {
        let app = openSources()
        app.buttons["reauthorize-garmin"].tap()
        XCTAssertTrue(app.element("promptMessage").waitForExistence(timeout: 10))
        answer(app, [("email", Self.login.email), ("password", Self.login.password)])
        answer(app, [("code", Self.login.code)])
        XCTAssertTrue(app.buttons["promptContinue"].waitForNonExistence(timeout: 10), "the sheet closes once reauthorized")
        waitForLabel(app.buttons["connection-garmin"], containing: "Healthy")
        XCTAssertTrue(app.buttons["syncNow-garmin"].waitForExistence(timeout: 5))
    }

    func testReauthorizeWithingsReturnsThroughTheAuthBrowser() {
        let app = openSources("-uitest-withings-env")
        app.openLink("vitamux://connections/conn_00000000000000000000000000000001")
        let reauthorize = app.buttons["reauthorize-withings"]
        XCTAssertTrue(reauthorize.waitForExistence(timeout: 10))
        reauthorize.tap()
        XCTAssertTrue(app.element("bannerConnected").waitForExistence(timeout: 10), "the return opens Sources with its banner")
        XCTAssertTrue(app.navigationBars["Sources"].exists)
    }
}
