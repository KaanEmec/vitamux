import XCTest

/// Lab documents, review and results on the fake server (FakeServer+Lab.swift). The system Files
/// picker and the document camera are not drivable here: under `-uitest` "Choose from Files" picks
/// the synthetic fixture PDF and "Scan with camera" returns a synthetic page image, and everything
/// after them (size check, PDF from the scan, multipart upload) runs as on a device. Synthetic only.
@MainActor
final class LabUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    private func openLab(_ arguments: String...) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-uitest"] + arguments
        app.launch()
        app.signInToDashboard()
        app.openLink("vitamux://lab")
        XCTAssertTrue(app.navigationBars["Lab"].waitForExistence(timeout: Wait.ui))
        return app
    }

    private func add(_ app: XCUIApplication, _ item: String) {
        app.buttons["addDocument"].tap()
        let button = app.buttons[item]
        XCTAssertTrue(button.waitForExistence(timeout: Wait.ui), item)
        button.tap()
    }

    private func notice(_ app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any)["labNotice"].firstMatch
    }

    private func beginning(_ app: XCUIApplication, _ prefix: String) -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH %@", prefix)).firstMatch
    }

    /// The screen's list: the review's own (the PDF view above it scrolls too), else the first.
    private func scrollList(_ app: XCUIApplication) -> XCUIElement {
        let review = app.collectionViews["reviewList"]
        return review.exists ? review : app.collectionViews.firstMatch
    }

    /// Waits for `element`, then looks for it down and up the list (lists create rows lazily). The
    /// documents list reloads after an upload, which a hosted runner answers slowly: wait for the
    /// server's rows before swiping.
    private func appear(_ app: XCUIApplication, _ element: XCUIElement, timeout: TimeInterval = Wait.server) -> Bool {
        if element.waitForExistence(timeout: timeout) { return true }
        let list = scrollList(app)
        for _ in 0..<4 {
            list.swipeUp()
            if element.waitForExistence(timeout: 1) { return true }
        }
        for _ in 0..<8 {
            list.swipeDown()
            if element.exists { return true }
        }
        return false
    }

    /// Adds the fixture PDF from "Files", starts the built-in extractor and waits for its rows.
    private func reviewNewDocument(_ app: XCUIApplication) {
        add(app, "Choose from Files")
        XCTAssertTrue(notice(app).waitForExistence(timeout: Wait.server))
        let extract = beginning(app, "extract-")
        XCTAssertTrue(appear(app, extract))
        extract.tap()
        let start = app.buttons["startExtraction"]
        XCTAssertTrue(start.waitForExistence(timeout: Wait.ui))
        // Enabled once the providers have loaded from the server.
        let enabled = XCTNSPredicateExpectation(predicate: NSPredicate(format: "isEnabled == true"), object: start)
        XCTAssertEqual(XCTWaiter.wait(for: [enabled], timeout: Wait.server), .completed, "the built-in extractor needs no consent")
        start.tap()
        XCTAssertTrue(app.navigationBars["Review"].waitForExistence(timeout: Wait.server))
        XCTAssertTrue(app.buttons["confirmResults"].waitForExistence(timeout: Wait.server), "the run finishes on the next 2 s poll")
        XCTAssertTrue(appear(app, app.buttons["row-0"]))
    }

    // MARK: Intake

    func testUploadFromFilesAndAgainSaysAlreadyStored() {
        let app = openLab()
        add(app, "Choose from Files")
        XCTAssertTrue(notice(app).waitForExistence(timeout: Wait.server))
        XCTAssertEqual(notice(app).label, "Stored synthetic-report.pdf.")
        XCTAssertTrue(beginning(app, "extract-").waitForExistence(timeout: Wait.server), "a new document offers Extract")

        add(app, "Choose from Files")
        let again = NSPredicate(format: "label CONTAINS 'already stored'")
        expectation(for: again, evaluatedWith: notice(app))
        waitForExpectations(timeout: Wait.server)
        XCTAssertEqual(app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'extract-'")).count, 1, "stored once")
    }

    func testAFileOverTheLimitIsRefusedBeforeUpload() {
        let app = openLab("-uitest-large-pdf")
        add(app, "Choose from Files")
        let problem = app.descendants(matching: .any)["labProblem"].firstMatch
        XCTAssertTrue(problem.waitForExistence(timeout: Wait.ui))
        XCTAssertTrue(problem.label.contains("larger than 20 MiB"), problem.label)
    }

    func testScanIsSavedAsOnePDF() {
        let app = openLab()
        add(app, "Scan with camera")
        XCTAssertTrue(notice(app).waitForExistence(timeout: Wait.server))
        XCTAssertTrue(notice(app).label.hasPrefix("Stored Scan "), notice(app).label)
        XCTAssertTrue(beginning(app, "extract-").waitForExistence(timeout: Wait.server), "the reloaded list offers Extract")
    }

    // MARK: Extract

    func testExternalProviderNeedsConsentNamingTheModel() {
        let app = openLab()
        add(app, "Choose from Files")
        let extract = beginning(app, "extract-")
        XCTAssertTrue(notice(app).waitForExistence(timeout: Wait.server))
        XCTAssertTrue(appear(app, extract))
        extract.tap()

        let gemini = app.buttons["extractor-gemini"]
        XCTAssertTrue(gemini.waitForExistence(timeout: Wait.server))
        XCTAssertFalse(app.buttons["extractor-openai"].isEnabled, "a disabled provider cannot be picked")
        gemini.tap()
        let start = app.buttons["startExtraction"]
        XCTAssertEqual(start.label, "Send and extract")
        XCTAssertFalse(start.isEnabled, "nothing is sent without consent")
        let consent = app.switches["consentToggle"]
        XCTAssertTrue(consent.waitForExistence(timeout: Wait.ui))
        XCTAssertTrue(consent.label.contains("gemini-synthetic-model"), consent.label)
        consent.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap()
        XCTAssertTrue(start.isEnabled)
        start.tap()

        XCTAssertTrue(app.buttons["confirmResults"].waitForExistence(timeout: Wait.server), "the run succeeded")
        let readBy = app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'Read by Google Gemini (gemini-synthetic-model)'")).firstMatch
        XCTAssertTrue(readBy.waitForExistence(timeout: Wait.server))
    }

    // MARK: Review

    func testReviewAcceptEditRejectConfirmAndUnconfirm() {
        let app = openLab()
        reviewNewDocument(app)
        XCTAssertTrue(app.element("pdfPage").waitForExistence(timeout: Wait.server))
        XCTAssertTrue(app.buttons["confirmResults"].label.contains("4 rows still need review"))

        // Accept as read: the PDF outlines the row in view above the row sheet, and the next row
        // waiting for review is selected.
        app.buttons["row-0"].tap()
        let accept = app.buttons["acceptRow"]
        XCTAssertTrue(accept.waitForExistence(timeout: Wait.ui), "the row sheet opens")
        XCTAssertEqual(app.element("pdfPage").value as? String, "Row 1 (Glucose) outlined")
        XCTAssertEqual(accept.label, "Accept as read")
        accept.tap()
        expectation(for: NSPredicate(format: "label CONTAINS 'Accepted'"), evaluatedWith: app.buttons["row-0"])
        waitForExpectations(timeout: Wait.server)
        expectation(for: NSPredicate(format: "value == 'Row 2 (Creatinine) outlined'"), evaluatedWith: app.element("pdfPage"))
        waitForExpectations(timeout: Wait.server)

        // Edit: only the changed field is sent, and the row reads Edited.
        let unit = app.textFields["field-unit_text"]
        XCTAssertTrue(unit.waitForExistence(timeout: Wait.server))
        unit.replaceText("umol/l")
        XCTAssertEqual(accept.label, "Save and accept")
        accept.tap()
        expectation(for: NSPredicate(format: "label CONTAINS 'Edited'"), evaluatedWith: app.buttons["row-1"])
        waitForExpectations(timeout: Wait.server)

        // Reject the unreadable row.
        expectation(for: NSPredicate(format: "value == 'Row 3 (#######) outlined'"), evaluatedWith: app.element("pdfPage"))
        waitForExpectations(timeout: Wait.server)
        app.buttons["rejectRow"].tap()
        expectation(for: NSPredicate(format: "label CONTAINS 'Rejected'"), evaluatedWith: app.buttons["row-2"])
        waitForExpectations(timeout: Wait.server)

        // Confirm with an unreviewed row: it is listed with a way to it.
        XCTAssertTrue(app.buttons["closeRow"].waitForExistence(timeout: Wait.server), "row 4 is up next")
        app.buttons["closeRow"].tap()
        let confirm = app.buttons["confirmResults"]
        XCTAssertTrue(confirm.waitForExistence(timeout: Wait.ui))
        XCTAssertTrue(confirm.label.contains("1 row still needs review"), confirm.label)
        confirm.tap()
        XCTAssertTrue(app.element("notReady").waitForExistence(timeout: Wait.server))
        let goTo = app.buttons["goToRow-3"]
        XCTAssertTrue(appear(app, goTo))
        goTo.tap()
        XCTAssertTrue(accept.waitForExistence(timeout: Wait.ui))
        XCTAssertEqual(app.element("pdfPage").value as? String, "Row 4 (HbA1c) outlined")
        let collected = app.textFields["field-collected_at"]
        XCTAssertTrue(collected.waitForExistence(timeout: Wait.ui))
        // Lower in the medium sheet than the bottom bar: drag the sheet up first.
        app.collectionViews.matching(NSPredicate(format: "identifier != 'reviewList'")).firstMatch.swipeUp()
        collected.replaceText("2026-09-28")
        accept.tap()
        expectation(for: NSPredicate(format: "label CONTAINS 'Edited'"), evaluatedWith: app.buttons["row-3"])
        waitForExpectations(timeout: Wait.server)

        // Confirm, unconfirm: every row is reviewed, so the sheet has closed.
        XCTAssertTrue(confirm.waitForExistence(timeout: Wait.ui))
        XCTAssertEqual(confirm.label, "Confirm results")
        confirm.tap()
        expectation(for: NSPredicate(format: "label == 'Confirmed 3 results.'"), evaluatedWith: notice(app))
        waitForExpectations(timeout: Wait.server)
        XCTAssertTrue(app.buttons["confirmAgain"].waitForExistence(timeout: Wait.server))
        app.buttons["unconfirm"].tapWhenReady(timeout: Wait.server)
        expectation(for: NSPredicate(format: "label BEGINSWITH 'Unconfirmed'"), evaluatedWith: notice(app))
        waitForExpectations(timeout: Wait.server)
        XCTAssertTrue(app.buttons["confirmResults"].waitForExistence(timeout: Wait.server))
    }

    func testAConfirmedDocumentOpensFromItsLink() {
        let app = openLab()
        app.openLink("vitamux://lab/documents/doc_00000000000000000000000000000001")
        XCTAssertTrue(app.navigationBars["Review"].waitForExistence(timeout: Wait.ui))
        XCTAssertTrue(app.buttons["confirmAgain"].waitForExistence(timeout: Wait.server))
        XCTAssertTrue(app.element("pdfPage").waitForExistence(timeout: Wait.server))
        XCTAssertTrue(app.element("documentStatus").label.contains("Confirmed"))
    }

    // MARK: Delete

    func testDeleteKeepingItsResults() {
        let app = openLab()
        let delete = app.buttons["delete-doc_00000000000000000000000000000001"]
        XCTAssertTrue(delete.waitForExistence(timeout: Wait.server))
        delete.tap()
        // The dialog's buttons are matched by label: the action sheet lists each one twice.
        let keep = app.buttons["Delete, keep its results"].firstMatch
        XCTAssertTrue(keep.waitForExistence(timeout: Wait.ui))
        XCTAssertTrue(app.buttons["Delete with its results"].firstMatch.exists)
        keep.tap()
        expectation(for: NSPredicate(format: "label == 'Deleted synthetic-report-2025.pdf; its confirmed results are kept.'"), evaluatedWith: notice(app))
        waitForExpectations(timeout: Wait.server)
        XCTAssertFalse(app.buttons["delete-doc_00000000000000000000000000000001"].exists, "a deleted document has no actions")
    }

    // MARK: Results

    func testResultsByAnalyteWithHistoryAndTheAnalyteView() {
        let app = openLab()
        app.buttons["openResults"].tap()
        XCTAssertTrue(app.navigationBars["Results"].waitForExistence(timeout: Wait.ui))
        let group = app.element("group-ldl_c")
        XCTAssertTrue(group.waitForExistence(timeout: Wait.server))
        XCTAssertTrue(group.label.contains("4 results"), group.label)
        XCTAssertTrue(app.scrollTo(app.element("group-label:Synthetic marker")).waitForExistence(timeout: Wait.server), "an unknown analyte by its printed label")

        app.swipeDown()
        app.swipeDown()
        let result = beginning(app, "result-")
        XCTAssertTrue(result.waitForExistence(timeout: Wait.server))
        result.tap()
        XCTAssertTrue(app.element("revision-1").waitForExistence(timeout: Wait.server))
        app.buttons["closeHistory"].tap()

        app.scrollTo(app.buttons["trend-ldl_c"]).tap()
        XCTAssertTrue(app.navigationBars["Analyte history"].waitForExistence(timeout: Wait.ui))
    }

    // MARK: Copy review

    /// The lab screens never rate a value (CLAUDE.md hard rules; the panel's list in
    /// web/e2e/views.spec.ts): values, ranges and flags are shown as printed.
    func testNoLabScreenRatesAValue() throws {
        let judgement = try NSRegularExpression(pattern: #"\b(good|bad|poor|excellent|great|healthy|unhealthy|normal|abnormal|optimal|ideal|elevated|dangerous|concerning|warning|(high|low) risk)\b"#, options: .caseInsensitive)
        let app = openLab()
        let check = { (screen: String) in
            for _ in 0..<3 {
                for text in app.staticTexts.allElementsBoundByIndex.map(\.label) {
                    XCTAssertNil(judgement.firstMatch(in: text, range: NSRange(text.startIndex..., in: text)), "\(screen) shows “\(text)”")
                }
                self.scrollList(app).swipeUp()
            }
        }
        check("Lab")
        reviewNewDocument(app)
        app.buttons["row-1"].tap()
        XCTAssertTrue(app.buttons["acceptRow"].waitForExistence(timeout: Wait.ui))
        check("Review")
        app.buttons["closeRow"].tap()
        app.openLink("vitamux://lab/results")
        XCTAssertTrue(app.navigationBars["Results"].waitForExistence(timeout: Wait.ui))
        XCTAssertTrue(app.element("group-ldl_c").waitForExistence(timeout: Wait.server))
        check("Results")
    }
}
