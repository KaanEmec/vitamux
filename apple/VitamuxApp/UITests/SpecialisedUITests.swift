import XCTest

/// Sleep, blood pressure, body composition, workouts, events and the lab analyte view on the fake
/// server (FakeServer+Specialised.swift): each with data, with an empty range, and the multi-source
/// night. Values are synthetic.
@MainActor
final class SpecialisedUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    /// Signs in and opens `link`, waiting for `title`.
    private func open(_ link: String, title: String) -> XCUIApplication {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.openLink(link)
        XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: Wait.ui), link)
        return app
    }

    private func any(_ app: XCUIApplication, _ format: String, _ value: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: format, value)).firstMatch
    }

    /// Steps the range picker back `times` ranges.
    private func earlier(_ app: XCUIApplication, times: Int) {
        let button = app.buttons["Earlier"]
        XCTAssertTrue(button.waitForExistence(timeout: Wait.ui))
        for _ in 0..<times { button.tap() }
    }

    private func checkProvenance(_ app: XCUIApplication) {
        // The first match can already sit under the navigation bar (an earlier scroll passed it),
        // so tap the first one that is actually on screen.
        let buttons = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH %@", "provenance-"))
        app.scrollTo(buttons.firstMatch)
        let button = buttons.allElementsBoundByIndex.first(where: \.isHittable) ?? buttons.firstMatch
        XCTAssertTrue(button.waitForExistence(timeout: Wait.server))
        button.tap()
        // The sheet presents, then loads the chain from the server: a hosted runner takes a while
        // for both.
        XCTAssertTrue(app.buttons["closeProvenance"].waitForExistence(timeout: Wait.ui), "the provenance sheet opens")
        XCTAssertTrue(app.element("thisVersion").waitForExistence(timeout: Wait.server), "the record's provenance chain opens")
        app.buttons["closeProvenance"].tap()
    }

    // MARK: Sleep

    func testSleepShowsStagesNightsAndAMultiSourceNight() {
        let app = open("vitamux://explore/sleep", title: "Sleep")
        XCTAssertTrue(app.element("stageBars").waitForExistence(timeout: Wait.server))
        XCTAssertTrue(app.element("sleepStats").label.contains("Median bedtime"))
        XCTAssertTrue(app.scrollTo(app.element("lastNight")).waitForExistence(timeout: Wait.server))

        app.buttons["openLastNight"].tap()
        XCTAssertTrue(app.element("ruleTag-0").waitForExistence(timeout: Wait.server))
        XCTAssertEqual(app.element("ruleTag-0").label, "Selected")
        XCTAssertTrue(app.element("hypnogram-0").waitForExistence(timeout: Wait.server), "the selected source's stages")
        XCTAssertTrue(app.scrollTo(app.element("hypnogram-1")).waitForExistence(timeout: Wait.server), "a second source's stages on the same axis")
        XCTAssertEqual(app.element("ruleTag-1").label, "In the rule")
        let excluded = app.scrollTo(app.element("ruleTag-2"))
        XCTAssertTrue(excluded.waitForExistence(timeout: Wait.server))
        XCTAssertTrue(excluded.label.hasPrefix("Excluded"), excluded.label)
        XCTAssertTrue(app.staticTexts["This source reported no sleep stages."].waitForExistence(timeout: Wait.ui))
        app.swipeDown()
        checkProvenance(app)

        // The night before has a nap.
        app.navigationBars.buttons.element(boundBy: 0).tap()
        let second = app.scrollTo(app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'night-'")).element(boundBy: 1))
        XCTAssertTrue(second.waitForExistence(timeout: Wait.server))
        second.tap()
        XCTAssertTrue(app.element("ruleTag-1").waitForExistence(timeout: Wait.server))
        XCTAssertTrue(app.scrollTo(any(app, "identifier BEGINSWITH %@", "nap-")).waitForExistence(timeout: Wait.server), "the day's nap is listed")
    }

    func testSleepEmptyRange() {
        let app = open("vitamux://explore/sleep", title: "Sleep")
        XCTAssertTrue(app.element("stageBars").waitForExistence(timeout: Wait.server))
        earlier(app, times: 3)
        XCTAssertTrue(app.element("emptyRange").waitForExistence(timeout: Wait.server), "no nights 90 days back")
    }

    // MARK: Blood pressure and body composition

    func testBloodPressureReadingsPartsAndProvenance() {
        let app = open("vitamux://explore/blood-pressure", title: "Blood pressure")
        XCTAssertTrue(app.element("bpChart").waitForExistence(timeout: Wait.server))
        XCTAssertTrue(app.element("bpMean").label.contains("mmHg"))
        let all = app.element("bpCount").label
        app.segmentedControls["timeOfDay"].buttons["Evening"].tap()
        XCTAssertNotEqual(app.element("bpCount").label, all, "only the evening readings")
        app.segmentedControls["timeOfDay"].buttons["All"].tap()
        XCTAssertTrue(app.scrollTo(app.element("pulseChart")).waitForExistence(timeout: Wait.server))
        XCTAssertTrue(app.scrollTo(any(app, "identifier BEGINSWITH %@", "reading-")).waitForExistence(timeout: Wait.server))
        checkProvenance(app)
    }

    func testBloodPressureEmptyRange() {
        let app = open("vitamux://explore/blood-pressure", title: "Blood pressure")
        XCTAssertTrue(app.element("bpChart").waitForExistence(timeout: Wait.server))
        earlier(app, times: 1)
        XCTAssertTrue(app.element("emptyRange").waitForExistence(timeout: Wait.server))
    }

    func testBodyCompositionWeightPartsAndWeighIns() {
        let app = open("vitamux://explore/body-composition", title: "Body composition")
        XCTAssertTrue(app.element("weightChart").waitForExistence(timeout: Wait.server))
        XCTAssertTrue(app.element("weightStats").label.contains("Latest"))
        let parts = app.scrollTo(app.element("compositionChart"))
        XCTAssertTrue(parts.waitForExistence(timeout: Wait.server))
        app.scrollTo(app.buttons["Show as table"].firstMatch)
        XCTAssertTrue(app.scrollTo(any(app, "identifier BEGINSWITH %@", "weighIn-")).waitForExistence(timeout: Wait.server))
        checkProvenance(app)
    }

    func testBodyCompositionEmptyRange() {
        let app = open("vitamux://explore/body-composition", title: "Body composition")
        XCTAssertTrue(app.element("weightChart").waitForExistence(timeout: Wait.server))
        earlier(app, times: 2)
        XCTAssertTrue(app.element("emptyRange").waitForExistence(timeout: Wait.server))
    }

    // MARK: Workouts and events

    func testWorkoutsCalendarClusterAndPick() {
        let app = open("vitamux://explore/workouts", title: "Workouts")
        XCTAssertTrue(app.element("calendar").waitForExistence(timeout: Wait.server))
        let cluster = any(app, "label CONTAINS %@", "3 sources")
        XCTAssertTrue(cluster.waitForExistence(timeout: Wait.server), "today's run from three sources is one cluster")
        XCTAssertTrue(any(app, "label == %@", "Selected").exists, "the rule's pick is marked")
        XCTAssertTrue(app.scrollTo(any(app, "label == %@", "Not in the rule")).waitForExistence(timeout: Wait.server))
        app.swipeDown()
        app.swipeDown()

        let day = app.buttons["day-\(Fake.day())"]
        XCTAssertTrue(day.waitForExistence(timeout: Wait.ui))
        XCTAssertTrue(day.label.hasSuffix("2 workouts"), day.label)
        day.tap()
        XCTAssertTrue(any(app, "label BEGINSWITH %@", "Showing").waitForExistence(timeout: Wait.server))
        checkProvenance(app)
    }

    func testWorkoutsEmptyMonth() {
        let app = open("vitamux://explore/workouts", title: "Workouts")
        XCTAssertTrue(app.element("calendar").waitForExistence(timeout: Wait.server))
        for _ in 0..<4 { app.buttons["previousMonth"].tap() }
        XCTAssertTrue(app.element("emptyRange").waitForExistence(timeout: Wait.server))
        XCTAssertTrue(app.staticTexts["No workouts in this month"].exists)
    }

    func testEventsLanesFilterAndTable() {
        let app = open("vitamux://explore/events", title: "Events")
        let heading = app.element("eventsHeading")
        XCTAssertTrue(heading.waitForExistence(timeout: Wait.server))
        XCTAssertEqual(heading.label, "5 events in 3 types")
        XCTAssertTrue(app.element("eventLanes").exists)

        app.element("eventType").tap()
        let option = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Irregular rhythm'")).firstMatch
        XCTAssertTrue(option.waitForExistence(timeout: Wait.ui))
        option.tap()
        XCTAssertTrue(any(app, "label == %@", "2 events in 1 type").waitForExistence(timeout: Wait.server), "one type alone")

        app.scrollTo(app.buttons["Show as table"].firstMatch).tap()
        XCTAssertTrue(app.buttons["Show as chart"].waitForExistence(timeout: Wait.ui), "the lanes as a table")
    }

    func testEventsLinkedTypeAndEmptyRange() {
        let app = open("vitamux://explore/events?code=irregular_rhythm", title: "Events")
        XCTAssertTrue(any(app, "label == %@", "2 events in 1 type").waitForExistence(timeout: Wait.server))
        earlier(app, times: 2)
        XCTAssertTrue(app.element("emptyRange").waitForExistence(timeout: Wait.server))
    }

    // MARK: Lab analyte

    func testLabAnalytePrintedUnitsRangeAndLabel() {
        let app = open("vitamux://lab/analytes/ldl_c", title: "Analyte history")
        XCTAssertTrue(app.element("analyteHeader").waitForExistence(timeout: Wait.server))
        XCTAssertTrue(app.element("analyteHeader").label.contains("LDL cholesterol"))
        XCTAssertTrue(app.element("latestResult").label.contains("2.9"))
        XCTAssertTrue(app.element("analyteChart").exists)
        XCTAssertTrue(any(app, "label CONTAINS %@", "The band is the reference range printed on each report.").exists)
        app.buttons["mg/dL"].tap()
        XCTAssertTrue(any(app, "label BEGINSWITH %@", "Plotted in mg/dL, as printed.").waitForExistence(timeout: Wait.ui))
        XCTAssertTrue(app.scrollTo(any(app, "label CONTAINS %@", "< 2.6")).waitForExistence(timeout: Wait.server), "values as printed, with their comparator")

        app.openLink("vitamux://lab/analytes/label:Synthetic%20marker")
        XCTAssertTrue(any(app, "label CONTAINS %@", "Synthetic marker").waitForExistence(timeout: Wait.server), "an unknown analyte by its printed label")
        XCTAssertTrue(app.element("analyteChart").waitForExistence(timeout: Wait.server))
    }

    func testLabAnalyteWithoutResults() {
        let app = open("vitamux://lab/analytes/unknown_analyte", title: "Analyte history")
        XCTAssertTrue(app.element("emptyRange").waitForExistence(timeout: Wait.server))
    }

    // MARK: Copy review

    /// The views never rate a value (CLAUDE.md hard rules), the twin of the panel's lint list in
    /// web/e2e/views.spec.ts. Lab values, ranges and flags are shown as printed.
    func testNoViewRatesAValue() throws {
        let judgement = try NSRegularExpression(pattern: #"\b(good|bad|poor|excellent|great|healthy|unhealthy|normal|abnormal|optimal|ideal|elevated|dangerous|concerning|warning|(high|low) risk)\b"#, options: .caseInsensitive)
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        let views = [
            ("vitamux://explore/sleep", "Sleep", "stageBars"), ("vitamux://explore/blood-pressure", "Blood pressure", "bpChart"),
            ("vitamux://explore/body-composition", "Body composition", "weightChart"), ("vitamux://explore/workouts", "Workouts", "calendar"),
            ("vitamux://explore/events", "Events", "eventsHeading"), ("vitamux://lab/analytes/ldl_c", "Analyte history", "analyteHeader"),
        ]
        let review = { (link: String) in
            for _ in 0..<3 {
                for text in app.staticTexts.allElementsBoundByIndex.map(\.label) {
                    let range = NSRange(text.startIndex..., in: text)
                    XCTAssertNil(judgement.firstMatch(in: text, range: range), "\(link) shows “\(text)”")
                }
                app.swipeUp()
            }
        }
        for (link, title, ready) in views {
            app.openLink(link)
            XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: Wait.ui), link)
            XCTAssertTrue(app.element(ready).waitForExistence(timeout: Wait.server), link)
            review(link)
            if ready == "stageBars" {
                for _ in 0..<4 { app.swipeDown() }
                app.scrollTo(app.buttons["openLastNight"]).tap()
                XCTAssertTrue(app.element("ruleTag-0").waitForExistence(timeout: Wait.server))
                review("the last night")
            }
        }
    }
}
