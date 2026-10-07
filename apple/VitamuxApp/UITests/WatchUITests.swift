import XCTest

/// The Apple Watch views (J22.18) on the fake server (FakeServer+Watch.swift): ECG, beat-to-beat,
/// activity rings, State of Mind and a workout's route and segments, each with data and empty;
/// the Explore and Events entries that open them; the sensitive groups and the Apple Watch card on
/// the Apple Health screen; and a copy review. Values are synthetic.
@MainActor
final class WatchUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    /// The fixture's ECG recordings: today's sinus rhythm with a waveform, 4 days back without one.
    private let latestECG = "00000000-0000-4000-8000-000000000103"
    private let poorECG = "00000000-0000-4000-8000-000000000102"

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

    private func earlier(_ app: XCUIApplication, times: Int) {
        let button = app.buttons["Earlier"]
        XCTAssertTrue(button.waitForExistence(timeout: 5))
        for _ in 0..<times { button.tap() }
    }

    // MARK: ECG

    func testECGRecordingShowsAppleLabelStripAndTable() {
        let app = open("vitamux://explore/ecg", title: "ECG")
        XCTAssertTrue(app.element("ecgCount").waitForExistence(timeout: 10))
        XCTAssertEqual(app.element("ecgCount").label, "3 recordings")
        app.buttons["ecg-\(latestECG)"].tap()
        XCTAssertTrue(app.navigationBars["ECG recording"].waitForExistence(timeout: 10))
        let label = app.element("ecgClassification")
        XCTAssertTrue(label.waitForExistence(timeout: 10))
        XCTAssertTrue(label.label.hasPrefix("Sinus rhythm"), label.label)
        XCTAssertTrue(any(app, "label CONTAINS %@", "Classification recorded by Apple’s ECG app, shown as recorded").exists)
        XCTAssertTrue(app.element("ecgHeartRate").label.hasSuffix("64 bpm"), app.element("ecgHeartRate").label)
        let strip = app.element("ecgStrip")
        XCTAssertTrue(strip.waitForExistence(timeout: 10))
        XCTAssertTrue((strip.value as? String ?? "").hasPrefix("30 s at 512 Hz"), "\(strip.value ?? "")")
        strip.swipeLeft()
        XCTAssertTrue(app.scrollTo(any(app, "label CONTAINS %@", "25 mm/s and 10 mm/mV")).exists, "the paper scale is named")
        app.scrollTo(app.switches["ecgTableToggle"]).switches.firstMatch.tap()
        XCTAssertTrue(app.scrollTo(app.element("ecgSecond-0")).waitForExistence(timeout: 5), "the strip as a table")
    }

    func testECGWithoutWaveformAndEmptyRange() {
        let app = open("vitamux://explore/ecg/\(poorECG)", title: "ECG recording")
        XCTAssertTrue(app.element("noWaveform").waitForExistence(timeout: 10))
        XCTAssertTrue(app.element("ecgClassification").label.hasPrefix("Inconclusive: poor reading"))
        XCTAssertTrue(app.element("ecgHeartRate").label.hasSuffix("Not recorded"), app.element("ecgHeartRate").label)

        app.openLink("vitamux://explore/ecg")
        XCTAssertTrue(app.element("ecgCount").waitForExistence(timeout: 10))
        earlier(app, times: 1)
        XCTAssertTrue(app.element("emptyRange").waitForExistence(timeout: 10), "no recordings a year back")
    }

    // MARK: Beat-to-beat

    func testBeatsOpenFromAnHRVReading() {
        let app = open("vitamux://explore/hrv_rmssd_nightly", title: "Hrv rmssd nightly")
        let row = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'valueRow-'")).firstMatch
        XCTAssertTrue(app.scrollTo(row).waitForExistence(timeout: 10))
        row.tap()
        let link = app.buttons["openBeats"]
        XCTAssertTrue(app.scrollTo(link).waitForExistence(timeout: 10), "the point sheet links the day's beats")
        link.tap()
        XCTAssertTrue(app.navigationBars["Beat-to-beat"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.element("beatsChart-0").waitForExistence(timeout: 10))
    }

    func testBeatsSeriesPerDayAndEmptyDay() {
        let app = open("vitamux://explore/beats?date=\(Fake.day(-1))", title: "Beat-to-beat")
        XCTAssertTrue(app.element("beatsChart-0").waitForExistence(timeout: 10))
        XCTAssertTrue(app.scrollTo(app.element("beatsChart-1")).exists, "two series on the day")
        let count = app.scrollTo(app.element("beatsCount-1"))
        XCTAssertTrue(count.exists && count.label.hasPrefix("89 intervals"), count.debugDescription)

        app.openLink("vitamux://explore/beats?date=\(Fake.day(-2))")
        XCTAssertTrue(app.element("beatsChart-0").waitForExistence(timeout: 10))
        for _ in 0..<4 { app.swipeDown() }
        app.buttons["earlierDay"].tap()
        XCTAssertTrue(app.element("emptyRange").waitForExistence(timeout: 10), "no beats on the gap day")
    }

    // MARK: Activity rings

    func testActivityRingsAgainstApplesGoals() {
        let app = open("vitamux://explore/activity-rings", title: "Activity rings")
        XCTAssertTrue(app.element("ring-move-\(Fake.day(-1))").waitForExistence(timeout: 10))
        XCTAssertTrue(any(app, "value ENDSWITH %@", "of 500 kcal, Apple’s goal").exists, "the value against Apple's goal")
        XCTAssertTrue(app.scrollTo(app.element("ring-exercise-\(Fake.day(-1))")).exists)
        XCTAssertTrue(app.scrollTo(any(app, "label BEGINSWITH %@", "Apple’s rings were paused on this day.")).exists, "the paused day says so")

        for _ in 0..<6 where !app.buttons["1M"].isHittable { app.swipeDown() }
        app.buttons["1M"].tap()
        earlier(app, times: 2)
        XCTAssertTrue(app.element("emptyRange").waitForExistence(timeout: 10), "no summaries 60 days back")
    }

    // MARK: State of Mind

    func testStateOfMindEntriesAsLoggedAndEmptyRange() {
        let app = open("vitamux://explore/state-of-mind", title: "State of Mind")
        XCTAssertTrue(app.element("mindCount").waitForExistence(timeout: 10))
        XCTAssertEqual(app.element("mindCount").label, "5 entries")
        XCTAssertTrue(app.element("valenceChart").exists)
        XCTAssertTrue(app.scrollTo(any(app, "label CONTAINS %@", "Slightly pleasant")).exists, "Apple's word, as logged")
        XCTAssertTrue(any(app, "label CONTAINS %@", "Labels: Calm, Content").exists)
        XCTAssertTrue(app.scrollTo(any(app, "label CONTAINS %@", "Mindful session")).exists)

        for _ in 0..<6 { app.swipeDown() }
        earlier(app, times: 1)
        XCTAssertTrue(app.element("emptyRange").waitForExistence(timeout: 10))
    }

    // MARK: Workout route and segments

    func testWorkoutRouteLapsAndAWorkoutWithoutRoute() {
        let app = open("vitamux://explore/workouts", title: "Workouts")
        let run = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'workoutDetail-913'")).firstMatch
        XCTAssertTrue(app.scrollTo(run).waitForExistence(timeout: 10))
        run.tap()
        XCTAssertTrue(app.navigationBars["Running"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.element("routeMap").waitForExistence(timeout: 10), "the route on the map")
        XCTAssertTrue(app.scrollTo(any(app, "label CONTAINS %@", "locations as recorded. The map loads Apple map tiles")).exists, "the map's tiles are named")
        XCTAssertTrue(app.scrollTo(app.element("segmentLanes")).exists, "laps, pauses and markers on the workout's axis")
        // The lanes name each segment's kind: five laps, a pause and a marker.
        for (kind, count) in [("Laps", 5), ("Pauses", 1), ("Markers", 1)] {
            let marks = app.descendants(matching: .any).matching(NSPredicate(format: "label ENDSWITH %@", ", \(kind)"))
            XCTAssertEqual(marks.count, count, kind)
        }

        // Today's evening ride, by its link (`914` + days since 1970 of the fake's today).
        let parts = Fake.day(0).split(separator: "-").map { Int($0)! }
        var utc = Calendar(identifier: .gregorian)
        utc.timeZone = .gmt
        let days = Int(utc.date(from: DateComponents(year: parts[0], month: parts[1], day: parts[2]))!.timeIntervalSince1970 / 86_400)
        app.openLink("vitamux://explore/workouts/914\(days)")
        XCTAssertTrue(app.navigationBars["Cycling"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.element("noRoute").waitForExistence(timeout: 10), "no route recorded")
        XCTAssertTrue(app.scrollTo(app.element("segmentLanes")).exists)
        let activities = app.descendants(matching: .any).matching(NSPredicate(format: "label ENDSWITH %@", ", Activities"))
        XCTAssertEqual(activities.count, 2, "the multisport workout's two activities")
    }

    // MARK: Where the views open from

    func testExploreAndEventsOpenTheWatchViews() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.tabBars.buttons["Explore"].tap()
        for (code, title) in [("rr_interval", "Beat-to-beat"), ("ecg_recording", "ECG"), ("state_of_mind", "State of Mind")] {
            let row = app.scrollTo(app.buttons["exploreRow-\(code)"])
            XCTAssertTrue(row.waitForExistence(timeout: 10), code)
            row.tap()
            XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: 10), code)
            app.navigationBars.buttons.element(boundBy: 0).tap()
            XCTAssertTrue(app.navigationBars["Explore"].waitForExistence(timeout: 5))
        }

        app.openLink("vitamux://explore/stand_hours")
        XCTAssertTrue(app.navigationBars["Stand hours"].waitForExistence(timeout: 10))
        app.scrollTo(app.buttons["ringsLink"]).tap()
        XCTAssertTrue(app.navigationBars["Activity rings"].waitForExistence(timeout: 10))

        app.openLink("vitamux://explore/events?code=ecg_recording")
        XCTAssertTrue(any(app, "label == %@", "3 events in 1 type").waitForExistence(timeout: 10), "ECG recordings are a lane too")
        app.buttons["eventTypeView"].tap()
        XCTAssertTrue(app.navigationBars["ECG"].waitForExistence(timeout: 10))
    }

    // MARK: Apple Health

    private func openAppleHealth() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-uitest", "-uitest-paired"]
        app.launch()
        app.signInToDashboard()
        app.openLink("vitamux://apple-health")
        XCTAssertTrue(app.navigationBars["Apple Health"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.element("pairedState").waitForExistence(timeout: 10))
        return app
    }

    func testSensitiveGroupsAskFirstAndRoutesNeedWorkouts() {
        let app = openAppleHealth()
        let ecg = app.scrollTo(app.switches["group-ecg"])
        XCTAssertTrue(ecg.waitForExistence(timeout: 5))
        ecg.switches.firstMatch.tap()
        XCTAssertTrue(app.buttons["confirmOptIn"].waitForExistence(timeout: 5), "turning a sensitive group on asks first")
        XCTAssertTrue(any(app, "label CONTAINS %@", "without extra encryption by the app").exists)
        app.buttons["cancelOptIn"].tap()
        XCTAssertFalse(app.staticTexts["requested-ecg"].waitForExistence(timeout: 2), "cancel leaves it off")
        app.scrollTo(app.switches["group-ecg"]).switches.firstMatch.tap()
        app.buttons["confirmOptIn"].tap()
        XCTAssertTrue(app.staticTexts["requested-ecg"].waitForExistence(timeout: 10))

        let routes = app.scrollTo(app.switches["group-routes"])
        XCTAssertFalse(routes.isEnabled, "routes need workouts")
        XCTAssertTrue(app.staticTexts["requires-routes"].exists)
        for _ in 0..<6 where !app.switches["group-workouts"].isHittable { app.swipeDown() }
        app.switches["group-workouts"].switches.firstMatch.tap()
        XCTAssertTrue(app.staticTexts["requested-workouts"].waitForExistence(timeout: 10), "a non-sensitive group turns on at once")
        let enabled = app.scrollTo(app.switches["group-routes"])
        XCTAssertTrue(enabled.isEnabled)
        enabled.switches.firstMatch.tap()
        XCTAssertTrue(app.buttons["confirmOptIn"].waitForExistence(timeout: 5))
        XCTAssertTrue(any(app, "label CONTAINS %@", "loads Apple map tiles").exists, "the routes sheet names the map tiles")
        app.buttons["confirmOptIn"].tap()
        XCTAssertTrue(app.staticTexts["requested-routes"].waitForExistence(timeout: 10))
    }

    func testWatchCardListsGroupsAndWatchTypes() {
        let app = openAppleHealth()
        let groups = app.scrollTo(app.staticTexts["watchGroups"])
        XCTAssertTrue(groups.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(groups.label.hasSuffix("None"), groups.label)
        XCTAssertTrue(app.scrollTo(app.element("watchType-rr_interval")).waitForExistence(timeout: 10), "a type the Watch contributed")
        XCTAssertTrue(app.element("watchType-ecg_recording").exists)
    }

    // MARK: Copy review

    /// Nothing rates an ECG, rhythm, cycle or mood value. Apple's own ECG labels are shown as
    /// recorded, so they are the only exception.
    func testNoWatchViewRatesAValue() throws {
        let judgement = try NSRegularExpression(pattern: #"\b(good|bad|poor|excellent|great|healthy|unhealthy|normal|abnormal|optimal|ideal|elevated|dangerous|concerning|warning|worrying|irregular heartbeat detected|(high|low) risk)\b"#, options: .caseInsensitive)
        let appleLabels = ["Inconclusive: poor reading"]
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        let views = [
            ("vitamux://explore/ecg", "ECG", "ecgCount"), ("vitamux://explore/ecg/\(poorECG)", "ECG recording", "noWaveform"),
            ("vitamux://explore/ecg/\(latestECG)", "ECG recording", "ecgStrip"), ("vitamux://explore/beats?date=\(Fake.day(-1))", "Beat-to-beat", "beatsChart-0"),
            ("vitamux://explore/activity-rings", "Activity rings", "ring-move-\(Fake.day(-1))"), ("vitamux://explore/state-of-mind", "State of Mind", "mindCount"),
        ]
        for (link, title, ready) in views {
            app.openLink(link)
            XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: 10), link)
            XCTAssertTrue(app.element(ready).waitForExistence(timeout: 10), link)
            for _ in 0..<3 {
                for text in app.staticTexts.allElementsBoundByIndex.map(\.label) {
                    var checked = text
                    for label in appleLabels { checked = checked.replacingOccurrences(of: label, with: "") }
                    let range = NSRange(checked.startIndex..., in: checked)
                    XCTAssertNil(judgement.firstMatch(in: checked, range: range), "\(link) shows “\(text)”")
                }
                app.swipeUp()
            }
        }
    }
}
