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
        XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: 10), link)
        return app
    }

    private func any(_ app: XCUIApplication, _ format: String, _ value: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: format, value)).firstMatch
    }

    /// Steps the range picker back `times` ranges.
    private func earlier(_ app: XCUIApplication, times: Int) {
        let button = app.buttons["Earlier"]
        XCTAssertTrue(button.waitForExistence(timeout: 5))
        for _ in 0..<times { button.tap() }
    }

    /// Today in the fake's timezone.
    private var today: String {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "Europe/Berlin")!
        let parts = calendar.dateComponents([.year, .month, .day], from: .now)
        return String(format: "%04d-%02d-%02d", parts.year!, parts.month!, parts.day!)
    }

    private func checkProvenance(_ app: XCUIApplication) {
        let button = app.scrollTo(any(app, "identifier BEGINSWITH %@", "provenance-"))
        XCTAssertTrue(button.waitForExistence(timeout: 5))
        button.tap()
        XCTAssertTrue(app.element("thisVersion").waitForExistence(timeout: 10), "the record's provenance chain opens")
        app.buttons["closeProvenance"].tap()
    }

    // MARK: Sleep

    func testSleepShowsStagesNightsAndAMultiSourceNight() {
        let app = open("vitamux://explore/sleep", title: "Sleep")
        XCTAssertTrue(app.element("stageBars").waitForExistence(timeout: 10))
        XCTAssertTrue(app.element("sleepStats").label.contains("Median bedtime"))
        XCTAssertTrue(app.scrollTo(app.element("lastNight")).exists)

        app.buttons["openLastNight"].tap()
        XCTAssertTrue(app.element("ruleTag-0").waitForExistence(timeout: 10))
        XCTAssertEqual(app.element("ruleTag-0").label, "Selected")
        XCTAssertTrue(app.element("hypnogram-0").exists, "the selected source's stages")
        XCTAssertTrue(app.scrollTo(app.element("hypnogram-1")).exists, "a second source's stages on the same axis")
        XCTAssertEqual(app.element("ruleTag-1").label, "In the rule")
        let excluded = app.scrollTo(app.element("ruleTag-2"))
        XCTAssertTrue(excluded.label.hasPrefix("Excluded"), excluded.label)
        XCTAssertTrue(app.staticTexts["This source reported no sleep stages."].exists)
        app.swipeDown()
        checkProvenance(app)

        // The night before has a nap.
        app.navigationBars.buttons.element(boundBy: 0).tap()
        let second = app.scrollTo(app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'night-'")).element(boundBy: 1))
        XCTAssertTrue(second.waitForExistence(timeout: 5))
        second.tap()
        XCTAssertTrue(app.element("ruleTag-1").waitForExistence(timeout: 10))
        XCTAssertTrue(app.scrollTo(any(app, "identifier BEGINSWITH %@", "nap-")).exists, "the day's nap is listed")
    }

    func testSleepEmptyRange() {
        let app = open("vitamux://explore/sleep", title: "Sleep")
        XCTAssertTrue(app.element("stageBars").waitForExistence(timeout: 10))
        earlier(app, times: 3)
        XCTAssertTrue(app.element("emptyRange").waitForExistence(timeout: 10), "no nights 90 days back")
    }

    // MARK: Blood pressure and body composition

    func testBloodPressureReadingsPartsAndProvenance() {
        let app = open("vitamux://explore/blood-pressure", title: "Blood pressure")
        XCTAssertTrue(app.element("bpChart").waitForExistence(timeout: 10))
        XCTAssertTrue(app.element("bpMean").label.contains("mmHg"))
        let all = app.element("bpCount").label
        app.segmentedControls["timeOfDay"].buttons["Evening"].tap()
        XCTAssertNotEqual(app.element("bpCount").label, all, "only the evening readings")
        app.segmentedControls["timeOfDay"].buttons["All"].tap()
        XCTAssertTrue(app.scrollTo(app.element("pulseChart")).exists)
        XCTAssertTrue(app.scrollTo(any(app, "identifier BEGINSWITH %@", "reading-")).exists)
        checkProvenance(app)
    }

    func testBloodPressureEmptyRange() {
        let app = open("vitamux://explore/blood-pressure", title: "Blood pressure")
        XCTAssertTrue(app.element("bpChart").waitForExistence(timeout: 10))
        earlier(app, times: 1)
        XCTAssertTrue(app.element("emptyRange").waitForExistence(timeout: 10))
    }

    func testBodyCompositionWeightPartsAndWeighIns() {
        let app = open("vitamux://explore/body-composition", title: "Body composition")
        XCTAssertTrue(app.element("weightChart").waitForExistence(timeout: 10))
        XCTAssertTrue(app.element("weightStats").label.contains("Latest"))
        let parts = app.scrollTo(app.element("compositionChart"))
        XCTAssertTrue(parts.exists)
        app.scrollTo(app.buttons["Show as table"].firstMatch)
        XCTAssertTrue(app.scrollTo(any(app, "identifier BEGINSWITH %@", "weighIn-")).exists)
        checkProvenance(app)
    }

    func testBodyCompositionEmptyRange() {
        let app = open("vitamux://explore/body-composition", title: "Body composition")
        XCTAssertTrue(app.element("weightChart").waitForExistence(timeout: 10))
        earlier(app, times: 2)
        XCTAssertTrue(app.element("emptyRange").waitForExistence(timeout: 10))
    }

    // MARK: Workouts and events

    func testWorkoutsCalendarClusterAndPick() {
        let app = open("vitamux://explore/workouts", title: "Workouts")
        XCTAssertTrue(app.element("calendar").waitForExistence(timeout: 10))
        let cluster = any(app, "label CONTAINS %@", "3 sources")
        XCTAssertTrue(cluster.waitForExistence(timeout: 10), "today's run from three sources is one cluster")
        XCTAssertTrue(any(app, "label == %@", "Selected").exists, "the rule's pick is marked")
        XCTAssertTrue(app.scrollTo(any(app, "label == %@", "Not in the rule")).exists)
        app.swipeDown()
        app.swipeDown()

        let day = app.buttons["day-\(today)"]
        XCTAssertTrue(day.exists)
        XCTAssertTrue(day.label.hasSuffix("2 workouts"), day.label)
        day.tap()
        XCTAssertTrue(any(app, "label BEGINSWITH %@", "Showing").waitForExistence(timeout: 5))
        checkProvenance(app)
    }

    func testWorkoutsEmptyMonth() {
        let app = open("vitamux://explore/workouts", title: "Workouts")
        XCTAssertTrue(app.element("calendar").waitForExistence(timeout: 10))
        for _ in 0..<4 { app.buttons["previousMonth"].tap() }
        XCTAssertTrue(app.element("emptyRange").waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["No workouts in this month"].exists)
    }

    func testEventsLanesFilterAndTable() {
        let app = open("vitamux://explore/events", title: "Events")
        let heading = app.element("eventsHeading")
        XCTAssertTrue(heading.waitForExistence(timeout: 10))
        XCTAssertEqual(heading.label, "5 events in 3 types")
        XCTAssertTrue(app.element("eventLanes").exists)

        app.element("eventType").tap()
        let option = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Irregular rhythm'")).firstMatch
        XCTAssertTrue(option.waitForExistence(timeout: 5))
        option.tap()
        XCTAssertTrue(any(app, "label == %@", "2 events in 1 type").waitForExistence(timeout: 10), "one type alone")

        app.scrollTo(app.buttons["Show as table"].firstMatch).tap()
        XCTAssertTrue(app.buttons["Show as chart"].waitForExistence(timeout: 5), "the lanes as a table")
    }

    func testEventsLinkedTypeAndEmptyRange() {
        let app = open("vitamux://explore/events?code=irregular_rhythm", title: "Events")
        XCTAssertTrue(any(app, "label == %@", "2 events in 1 type").waitForExistence(timeout: 10))
        earlier(app, times: 2)
        XCTAssertTrue(app.element("emptyRange").waitForExistence(timeout: 10))
    }

    // MARK: Lab analyte

    func testLabAnalytePrintedUnitsRangeAndLabel() {
        let app = open("vitamux://lab/analytes/ldl_c", title: "Analyte history")
        XCTAssertTrue(app.element("analyteHeader").waitForExistence(timeout: 10))
        XCTAssertTrue(app.element("analyteHeader").label.contains("LDL cholesterol"))
        XCTAssertTrue(app.element("latestResult").label.contains("2.9"))
        XCTAssertTrue(app.element("analyteChart").exists)
        XCTAssertTrue(any(app, "label CONTAINS %@", "The band is the reference range printed on each report.").exists)
        app.buttons["mg/dL"].tap()
        XCTAssertTrue(any(app, "label BEGINSWITH %@", "Plotted in mg/dL, as printed.").waitForExistence(timeout: 5))
        XCTAssertTrue(app.scrollTo(any(app, "label CONTAINS %@", "< 2.6")).exists, "values as printed, with their comparator")

        app.openLink("vitamux://lab/analytes/label:Synthetic%20marker")
        XCTAssertTrue(any(app, "label CONTAINS %@", "Synthetic marker").waitForExistence(timeout: 10), "an unknown analyte by its printed label")
        XCTAssertTrue(app.element("analyteChart").exists)
    }

    func testLabAnalyteWithoutResults() {
        let app = open("vitamux://lab/analytes/unknown_analyte", title: "Analyte history")
        XCTAssertTrue(app.element("emptyRange").waitForExistence(timeout: 10))
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
            XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: 10), link)
            XCTAssertTrue(app.element(ready).waitForExistence(timeout: 10), link)
            review(link)
            if ready == "stageBars" {
                for _ in 0..<4 { app.swipeDown() }
                app.scrollTo(app.buttons["openLastNight"]).tap()
                XCTAssertTrue(app.element("ruleTag-0").waitForExistence(timeout: 10))
                review("the last night")
            }
        }
    }
}
