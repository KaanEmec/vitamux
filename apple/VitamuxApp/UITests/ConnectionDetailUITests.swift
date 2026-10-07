import XCTest

/// The connection detail's tabs and actions on the fake server (FakeServer+Sources.swift):
/// overview, streams, devices, backfills, history and settings.
@MainActor
final class ConnectionDetailUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    static let withings = "conn_00000000000000000000000000000001"
    static let garmin = "conn_00000000000000000000000000000002"

    private func open(_ id: String, tab: String? = nil) -> XCUIApplication {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.openLink("vitamux://connections/\(id)" + (tab.map { "?tab=\($0)" } ?? ""))
        XCTAssertTrue(app.element("detailHealth").waitForExistence(timeout: 10))
        return app
    }

    /// Swipes down until `element` shows (notices sit at the top of a tab).
    private func scrollBack(_ app: XCUIApplication, to element: XCUIElement) -> XCUIElement {
        for _ in 0..<6 where !(element.exists && element.isHittable) { app.swipeDown() }
        return element
    }

    private func waitForLabel(_ element: XCUIElement, containing text: String, timeout: TimeInterval = 15, file: StaticString = #filePath, line: UInt = #line) {
        let matched = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label CONTAINS %@", text), object: element)
        XCTAssertEqual(XCTWaiter.wait(for: [matched], timeout: timeout), .completed, "\(element) says \(element.label), not \(text)", file: file, line: line)
    }

    func testOverviewShowsTheTimelineAndFacts() {
        let app = open(Self.garmin)
        XCTAssertTrue(app.navigationBars["Garmin Connect"].exists)
        XCTAssertTrue(app.buttons["tab-overview"].isSelected)
        XCTAssertTrue(app.element("reauthNotice").exists)
        let strip = app.element("runStrip")
        XCTAssertTrue(strip.waitForExistence(timeout: 10))
        waitForLabel(strip, containing: "2 days with a failed run")
        XCTAssertTrue(app.scrollTo(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS 'garminconnect 0.3.17'")).firstMatch).exists, "the wrapped upstream")
        app.scrollTo(app.buttons["allRuns"]).tap()
        XCTAssertTrue(app.buttons["tab-history"].isSelected)
    }

    func testSyncNowAndPauseAndResume() {
        let app = open(Self.withings, tab: "settings")
        app.buttons["syncNow-withings"].tap()
        XCTAssertTrue(app.element("syncQueued").waitForExistence(timeout: 10))

        app.scrollTo(app.buttons["pauseSyncing"]).tap()
        waitForLabel(app.element("detailHealth"), containing: "Paused")
        XCTAssertTrue(app.element("settingsMessage").label.contains("Syncing is paused."))
        XCTAssertFalse(app.buttons["syncNow-withings"].isEnabled, "a paused connection does not sync")
        app.scrollTo(app.buttons["resumeSyncing"]).tap()
        waitForLabel(app.element("detailHealth"), containing: "Healthy")
        XCTAssertTrue(app.buttons["syncNow-withings"].isEnabled)
    }

    func testSchedulesSaveOnChange() {
        let app = open(Self.withings, tab: "settings")
        let interval = app.scrollTo(app.buttons["interval-withings.measures-incremental"])
        XCTAssertTrue(interval.waitForExistence(timeout: 10))
        interval.tap()
        app.buttons["every 6 h"].firstMatch.tap()
        waitForLabel(app.element("settingsMessage"), containing: "Schedule withings.measures (incremental) saved.")
        waitForLabel(interval, containing: "every 6 h")

        let enabled = app.scrollTo(app.switches["enabled-withings.measures-correction"])
        XCTAssertEqual(enabled.value as? String, "1")
        enabled.switches.firstMatch.tap()
        let off = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == '0'"), object: enabled)
        XCTAssertEqual(XCTWaiter.wait(for: [off], timeout: 10), .completed)
    }

    func testDisconnectKeepingTheData() {
        let app = open(Self.garmin, tab: "settings")
        app.scrollTo(app.buttons["removeConnection"]).tap()
        let submit = app.buttons["submitDelete"]
        XCTAssertTrue(submit.waitForExistence(timeout: 5))
        XCTAssertEqual(submit.label, "Disconnect and keep data")
        submit.tap()
        waitForLabel(scrollBack(app, to: app.element("detailHealth")), containing: "Disabled")
        XCTAssertTrue(app.element("settingsMessage").label.contains("Disconnected. The data stays"))
    }

    func testDeletingNeedsItsConfirmation() {
        let app = open(Self.garmin, tab: "settings")
        app.scrollTo(app.buttons["removeConnection"]).tap()
        let deleteData = app.buttons["delete-delete"]
        XCTAssertTrue(deleteData.waitForExistence(timeout: 10), "the removal sheet offers deleting the data")
        deleteData.tap()
        XCTAssertFalse(app.buttons["submitDelete"].isEnabled, "deleting the data asks for a confirmation")
        app.switches["confirmDelete"].switches.firstMatch.tap()
        app.buttons["submitDelete"].tap()
        XCTAssertTrue(app.element("bannerRemoved").waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["connection-garmin"].exists)
    }

    func testStreamsResetACursor() {
        let app = open(Self.withings, tab: "streams")
        let cursor = app.element("cursor-withings.sleep")
        XCTAssertTrue(app.scrollTo(cursor).waitForExistence(timeout: 10))
        XCTAssertTrue(cursor.label.contains("Saved"), cursor.label)
        app.scrollTo(app.buttons["resetCursor-withings.sleep"]).tap()
        let confirm = app.buttons["confirmResetCursor"].firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5))
        confirm.tap()
        waitForLabel(app.element("cursor-withings.sleep"), containing: "None (starts from the initial window)")
    }

    func testDevicesTypeNameAndMerge() {
        let app = open(Self.withings, tab: "devices")
        let type = app.scrollTo(app.buttons["deviceType-synthetic-scale-1b"])
        XCTAssertTrue(type.waitForExistence(timeout: 10))
        type.tap()
        app.buttons["scale"].firstMatch.tap()
        waitForLabel(scrollBack(app, to: app.element("deviceSaved")), containing: "is a scale. Resolved values are being recomputed.")

        let name = app.scrollTo(app.textFields["deviceName-synthetic-scale-1b"])
        name.tap()
        name.typeText("Hall scale\n")
        waitForLabel(scrollBack(app, to: app.element("deviceSaved")), containing: "Named Hall scale.")

        app.scrollTo(app.buttons["merge-synthetic-scale-1b"]).tap()
        let merge = app.buttons["mergeDevices"]
        XCTAssertTrue(merge.waitForExistence(timeout: 5))
        XCTAssertFalse(merge.isEnabled, "a merge needs its confirmation")
        app.switches["confirmMerge"].switches.firstMatch.tap()
        merge.tap()
        waitForLabel(scrollBack(app, to: app.element("deviceSaved")), containing: "Hall scale merged into Synthetic Scale: 4 readings moved.")
        XCTAssertFalse(app.buttons["merge-synthetic-scale-1b"].exists)
        XCTAssertTrue(app.scrollTo(app.staticTexts["Merged devices"]).exists)
    }

    func testBackfillsPollAndStart() {
        let app = open(Self.withings, tab: "backfills")
        let status = app.element("backfillStatus-withings.measures")
        XCTAssertTrue(status.waitForExistence(timeout: 10))
        waitForLabel(status, containing: "done", timeout: 30)
        waitForLabel(app.element("backfillProgress-withings.measures"), containing: "8 of 8 units done")

        app.buttons["newBackfill"].tap()
        let start = app.buttons["startBackfill"]
        XCTAssertTrue(start.waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label CONTAINS 'fetched in units of 30 days'")).firstMatch.exists)
        start.tap()
        waitForLabel(app.element("backfillMessage"), containing: "Backfill of withings.measures started.")

        // The new one runs (a unit at a time); cancelling keeps what it fetched.
        let cancel = app.buttons["cancelBackfill-withings.measures"]
        XCTAssertTrue(app.scrollTo(cancel).waitForExistence(timeout: 10))
        cancel.tap()
        waitForLabel(scrollBack(app, to: app.element("backfillMessage")), containing: "Backfill of withings.measures cancelled; data fetched so far stays.")
    }

    func testBackfillUnitsRetry() {
        let app = open(Self.garmin, tab: "backfills")
        let failed = app.element("backfillStatus-garmin.daily_summary")
        XCTAssertTrue(failed.waitForExistence(timeout: 10))
        waitForLabel(failed, containing: "failed")
        app.buttons["units-garmin.daily_summary"].tap()
        let retry = app.scrollTo(app.buttons["retryUnit"])
        XCTAssertTrue(retry.waitForExistence(timeout: 10))
        retry.tap()
        waitForLabel(scrollBack(app, to: app.element("backfillMessage")), containing: "Failed units of garmin.daily_summary queued again.")
        waitForLabel(app.element("backfillStatus-garmin.daily_summary"), containing: "done", timeout: 30)
    }

    func testHistoryLoadsOlderRuns() {
        let app = open(Self.withings, tab: "history")
        let older = app.scrollTo(app.buttons["loadOlderRuns"])
        for _ in 0..<20 where !(older.exists && older.isHittable) { app.swipeUp() }
        XCTAssertTrue(older.waitForExistence(timeout: 10), "60 runs come 50 at a time")
        older.tap()
        XCTAssertTrue(older.waitForNonExistence(timeout: 10), "the last page has no more")
    }
}
