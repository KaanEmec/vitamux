import XCTest

/// Settings parity (J22.13) on the fake server (FakeServer+Settings.swift): every page, and each
/// destructive action's confirmation. Values and secrets are synthetic.
@MainActor
final class SettingsUITests: XCTestCase {
    override func setUp() async throws { continueAfterFailure = false }

    private func open(_ page: String, title: String) -> XCUIApplication {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.openLink("vitamux://settings\(page.isEmpty ? "" : "/\(page)")")
        XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: 10))
        return app
    }

    // MARK: Profile

    func testTimezonePeriodsAddChangeAndRemove() {
        let app = open("", title: "Profile")
        XCTAssertEqual(app.element("accountUsername").label.contains(Fake.username), true)
        XCTAssertTrue(app.buttons["period-Europe/Berlin"].waitForExistence(timeout: 10))

        app.scrollTo(app.buttons["addPeriod"]).tap()
        let zone = app.textFields["periodZone"]
        XCTAssertTrue(zone.waitForExistence(timeout: 5))
        zone.replaceText("Tokyo")
        let suggestion = app.buttons["zoneSuggestion-Asia/Tokyo"]
        XCTAssertTrue(suggestion.waitForExistence(timeout: 5), "zones are suggested while typing")
        suggestion.tap()
        app.buttons["savePeriod"].tap()
        XCTAssertTrue(app.buttons["period-Asia/Tokyo"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.element("settingsNotice").exists)

        // Change it, then remove it from its sheet after confirming.
        app.buttons["period-Asia/Tokyo"].tap()
        zone.replaceText("Asia/Seoul")
        app.buttons["savePeriod"].tap()
        XCTAssertTrue(app.buttons["period-Asia/Seoul"].waitForExistence(timeout: 10))
        app.buttons["period-Asia/Seoul"].tap()
        app.buttons["removePeriod"].tap()
        let confirm = app.dialogButton("Remove Asia/Seoul")
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), "removing asks first")
        confirm.tap()
        XCTAssertTrue(app.buttons["period-Asia/Seoul"].waitForNonExistence(timeout: 10))
    }

    func testRemovePeriodBySwipeAsksFirstAndWithingsSaves() {
        let app = open("", title: "Profile")
        let row = app.buttons["period-America/New_York"]
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        row.swipeLeft()
        app.buttons["Remove"].tap()
        let confirm = app.dialogButton("Remove America/New_York")
        XCTAssertTrue(confirm.waitForExistence(timeout: 5))
        confirm.tap()
        XCTAssertTrue(row.waitForNonExistence(timeout: 10))

        let toggle = app.scrollTo(app.switches["withingsToggle"])
        toggle.switches.firstMatch.tap()
        XCTAssertTrue(app.staticTexts["Saved."].waitForExistence(timeout: 10))
        XCTAssertEqual(toggle.value as? String, "1")
    }

    // MARK: Devices

    func testPairingCodeResyncRevokeAndOrigins() {
        let app = open("devices", title: "Devices")
        app.buttons["createPairingCode"].tap()
        XCTAssertTrue(app.element("pairingQR").waitForExistence(timeout: 10), "a QR for another phone")
        XCTAssertTrue(app.staticTexts["pairingCode"].label.hasPrefix("SYN"))
        XCTAssertTrue(app.staticTexts["pairingCountdown"].label.hasPrefix("Expires in"))

        let resync = app.scrollTo(app.buttons["resync-Synthetic iPhone"])
        XCTAssertTrue(resync.exists)
        resync.tap()
        XCTAssertTrue(app.buttons["requestResync"].waitForExistence(timeout: 5))
        app.buttons["requestResync"].tap()
        let requested = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'Resync requested'")).firstMatch
        XCTAssertTrue(requested.waitForExistence(timeout: 10), "the device shows the request")

        let revoke = app.scrollTo(app.buttons["revoke-Synthetic old iPhone"])
        revoke.tap()
        let confirm = app.dialogButton("Revoke Synthetic old iPhone")
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), "revoking asks first")
        confirm.tap()
        XCTAssertTrue(app.scrollTo(app.element("revoked-Synthetic old iPhone")).waitForExistence(timeout: 10))

        let relays = app.scrollTo(app.buttons["relays-com.example.connect"])
        relays.tap()
        app.buttons["Nothing (direct)"].firstMatch.tap()
        let state = app.staticTexts["originState-com.example.connect"]
        XCTAssertTrue(state.waitForExistence(timeout: 10))
        let direct = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label BEGINSWITH 'Direct'"), object: state)
        XCTAssertEqual(XCTWaiter.wait(for: [direct], timeout: 10), .completed)
        XCTAssertTrue(app.scrollTo(app.element("sourceFilterPlaceholder")).exists, "the J22.25 source filter is a placeholder")
    }

    // MARK: AI providers

    func testAIProviderToggleSaves() {
        let app = open("ai", title: "AI providers")
        let gemini = app.switches["ai-gemini"]
        XCTAssertTrue(gemini.waitForExistence(timeout: 10))
        XCTAssertEqual(gemini.value as? String, "0")
        gemini.switches.firstMatch.tap()
        XCTAssertTrue(app.staticTexts["Saved."].waitForExistence(timeout: 10))
        XCTAssertEqual(gemini.value as? String, "1")
    }

    // MARK: API keys

    func testAPIKeySecretIsShownOnceAndRevokeAsks() {
        let app = open("api-keys", title: "API keys")
        XCTAssertTrue(app.element("apiKey-synthetic script").waitForExistence(timeout: 10))
        app.buttons["createAPIKey"].tap()
        let name = app.textFields["keyName"]
        XCTAssertTrue(name.waitForExistence(timeout: 5))
        name.tap()
        name.typeText("test key")
        app.buttons["saveAPIKey"].tap()
        let secret = app.staticTexts["secretValue"]
        XCTAssertTrue(secret.waitForExistence(timeout: 10), "the secret shows once")
        app.buttons["copySecret"].tap()
        XCTAssertTrue(app.buttons["Copied"].waitForExistence(timeout: 5))
        app.buttons["savedSecret"].tap()
        XCTAssertTrue(secret.waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.element("apiKey-test key").waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts["secretValue"].exists, "the secret is gone once saved")

        app.element("apiKey-test key").swipeLeft()
        app.buttons["Revoke"].tap()
        let confirm = app.dialogButton("Revoke key")
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), "revoking asks first")
        confirm.tap()
        let row = app.element("apiKey-test key")
        let revoked = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label CONTAINS 'Revoked'"), object: row)
        XCTAssertEqual(XCTWaiter.wait(for: [revoked], timeout: 10), .completed)
    }

    // MARK: Security

    func testPasswordChangeAsksAndChecksTheCurrentPassword() {
        let app = open("security", title: "Security")
        let current = app.secureTextFields["currentPassword"]
        XCTAssertTrue(current.waitForExistence(timeout: 10))
        current.tap()
        current.typeText("wrong-password")
        let new = app.secureTextFields["newPassword"]
        new.tap()
        new.typeText("synthetic-new-password")
        app.buttons["changePassword"].tap()
        app.dialogButton("Change and sign out others").tap()
        XCTAssertTrue(app.staticTexts["does not match"].waitForExistence(timeout: 10))

        current.tap()
        current.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 20) + Fake.password)
        app.buttons["changePassword"].tap()
        app.dialogButton("Change and sign out others").tap()
        XCTAssertTrue(app.staticTexts["Password changed. Every other session was signed out."].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["endOtherSessions"].isEnabled, "no other session is left")
    }

    func testTOTPEnrolRecoveryCodesOnceAndTurnOff() {
        let app = open("security", title: "Security")
        app.scrollTo(app.buttons["enrollTOTP"]).tap()
        XCTAssertTrue(app.staticTexts["secretValue"].waitForExistence(timeout: 10), "the setup key")
        XCTAssertTrue(app.links["otpauthLink"].exists || app.buttons["otpauthLink"].exists, "a link to an authenticator app")
        let code = app.textFields["totpCode"]
        code.tap()
        code.typeText(Fake.totp)
        app.buttons["confirmTOTP"].tap()
        XCTAssertTrue(app.staticTexts["synthetic-recovery-a"].waitForExistence(timeout: 10), "recovery codes show once")
        app.buttons["savedRecoveryCodes"].tap()
        XCTAssertTrue(app.staticTexts["synthetic-recovery-a"].waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Two-factor authentication is on."].waitForExistence(timeout: 10))

        app.buttons["disableTOTP"].tap()
        let password = app.secureTextFields["disablePassword"]
        XCTAssertTrue(password.waitForExistence(timeout: 5))
        password.tap()
        password.typeText(Fake.password)
        let totp = app.textFields["disableCode"]
        totp.tap()
        totp.typeText(Fake.totp)
        app.buttons["confirmDisableTOTP"].tap()
        XCTAssertTrue(app.staticTexts["Two-factor authentication is off."].waitForExistence(timeout: 10))
    }

    func testEndOneSessionThenAllOthersAsks() {
        let app = open("security", title: "Security")
        let one = app.scrollTo(app.buttons["endSession-00000000-0000-4000-8000-000000000050"])
        XCTAssertTrue(one.waitForExistence(timeout: 10))
        one.tap()
        let confirm = app.dialogButton("Sign out session")
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), "ending a session asks first")
        confirm.tap()
        XCTAssertTrue(app.staticTexts["Session signed out."].waitForExistence(timeout: 10))
        app.scrollTo(app.buttons["endOtherSessions"]).tap()
        app.dialogButton("Sign out all others").tap()
        XCTAssertTrue(app.staticTexts["Other sessions signed out."].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["This iPhone"].exists, "this app's session stays")
    }

    // MARK: Sources

    func testSourceOrderAndAppCredentials() {
        let app = open("sources", title: "Sources")
        let down = app.buttons["orderDown-whoop"]
        XCTAssertTrue(down.waitForExistence(timeout: 20))
        down.tap()
        XCTAssertFalse(app.buttons["orderUp-withings"].isEnabled, "Withings moves first")
        XCTAssertTrue(app.buttons["orderUp-whoop"].isEnabled)
        app.buttons["saveOrder"].tap()
        XCTAssertTrue(app.staticTexts["Saved."].waitForExistence(timeout: 10))

        app.scrollTo(app.buttons["editCredentials-withings"]).tap()
        let secret = app.secureTextFields["clientSecret"]
        XCTAssertTrue(secret.waitForExistence(timeout: 5))
        secret.tap()
        secret.typeText("synthetic-secret")
        app.buttons["saveCredentials"].tap()
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'Saved the Withings app credentials.'")).firstMatch.waitForExistence(timeout: 10))

        app.scrollTo(app.buttons["removeCredentials-withings"]).tap()
        app.dialogButton("Remove app credentials").tap()
        let anyway = app.dialogButton("Remove anyway")
        XCTAssertTrue(anyway.waitForExistence(timeout: 10), "in use: asks a second time")
        anyway.tap()
        XCTAssertTrue(app.staticTexts["Removed the Withings app credentials."].waitForExistence(timeout: 10))
    }

    func testSidecarSecretOnceAndRemoveInUseAsksTwice() {
        let app = open("sources", title: "Sources")
        app.scrollTo(app.buttons["addSidecar"]).tap()
        let name = app.textFields["sidecarName"]
        XCTAssertTrue(name.waitForExistence(timeout: 5))
        name.tap()
        name.typeText("synthetic_scale")
        let url = app.textFields["sidecarURL"]
        url.tap()
        url.typeText("http://scale-sidecar:8080")
        app.buttons["saveSidecar"].tap()
        XCTAssertTrue(app.staticTexts["secretValue"].waitForExistence(timeout: 10), "the shared secret shows once")
        app.buttons["savedSecret"].tap()
        XCTAssertTrue(app.staticTexts["secretValue"].waitForNonExistence(timeout: 5))

        app.scrollTo(app.buttons["removeSidecar-synthetic_ring"]).tap()
        app.dialogButton("Remove sidecar").tap()
        let anyway = app.dialogButton("Remove anyway")
        XCTAssertTrue(anyway.waitForExistence(timeout: 10), "connections exist: asks a second time")
        anyway.tap()
        XCTAssertTrue(app.staticTexts["Removed the sidecar “synthetic_ring”."].waitForExistence(timeout: 10))
    }

    // MARK: Retention

    func testRetentionValidatesAndSavesAfterConfirming() {
        let app = open("retention", title: "Retention")
        let idempotency = app.scrollTo(app.textFields["idempotencyDays"])
        XCTAssertTrue(idempotency.waitForExistence(timeout: 10))
        idempotency.retype("3")
        XCTAssertEqual(idempotency.value as? String, "3")
        app.scrollTo(app.buttons["saveRetention"]).tap()
        XCTAssertFalse(app.dialogButton("Save and prune").waitForExistence(timeout: 2), "checked before sending")
        let message = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS 'whole number from 7 to 36500'")).firstMatch
        XCTAssertTrue(message.waitForExistence(timeout: 5), "the field says why")

        app.scrollTo(app.textFields["idempotencyDays"]).retype("45")
        app.scrollTo(app.textFields["rawDays-withings"]).retype("120")
        app.scrollTo(app.buttons["saveRetention"]).tap()
        let confirm = app.dialogButton("Save and prune")
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), "pruning asks first")
        confirm.tap()
        XCTAssertTrue(confirm.waitForNonExistence(timeout: 5))
        // The notice heads the form.
        for _ in 0..<4 { app.swipeDown() }
        XCTAssertTrue(app.staticTexts["Saved."].waitForExistence(timeout: 10))
    }

    // MARK: Backups and export

    func testExportPollsDownloadsAndShares() {
        let app = open("backups", title: "Backups and export")
        let backup = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'Last backup'")).firstMatch
        XCTAssertTrue(backup.waitForExistence(timeout: 10), "the last backup")
        app.scrollTo(app.switches["exportRaw"]).switches.firstMatch.tap()
        app.scrollTo(app.buttons["startExport"]).tap()
        let download = app.scrollTo(app.buttons["downloadExport"])
        XCTAssertTrue(download.waitForExistence(timeout: 15), "ready after polling")
        download.tap()
        XCTAssertTrue(app.scrollTo(app.buttons["shareExport"]).waitForExistence(timeout: 10), "handed to the share sheet")
        XCTAssertTrue(app.buttons["saveExportToFiles"].exists, "or Files")
    }

    // MARK: System status

    func testSystemStatusShowsServerStorageAndHealth() {
        let app = open("system", title: "System status")
        XCTAssertTrue(app.element("appVersion").waitForExistence(timeout: 10))
        XCTAssertTrue(app.element("serverVersion").waitForExistence(timeout: 10))
        XCTAssertTrue(app.element("apiVersion").exists)
        XCTAssertTrue(app.scrollTo(app.element("databaseSize")).exists)
        XCTAssertTrue(app.scrollTo(app.element("noDegraded")).exists)
        XCTAssertTrue(app.scrollTo(app.element("noFailing")).exists)
    }

    // MARK: This app and More

    func testAppPreferencesAndMoreValues() {
        let app = XCUIApplication.launch()
        app.signInToDashboard()
        app.tabBars.buttons["More"].tap()
        let devices = app.buttons["more-devices"]
        XCTAssertTrue(devices.waitForExistence(timeout: 10))
        let two = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label CONTAINS '2 devices'"), object: devices)
        XCTAssertEqual(XCTWaiter.wait(for: [two], timeout: 10), .completed)
        XCTAssertTrue(app.buttons["more-security"].label.contains("Two-factor off"))
        XCTAssertTrue(app.buttons["more-system"].label.contains("Healthy"))
        XCTAssertTrue(app.scrollTo(app.buttons["notificationsRow"]).label.contains("5 of 5 on"))

        app.buttons["notificationsRow"].tap()
        XCTAssertTrue(app.navigationBars["This app"].waitForExistence(timeout: 5))
        app.scrollTo(app.switches["notifications.staleBackup"]).switches.firstMatch.tap()
        XCTAssertEqual(app.switches["notifications.staleBackup"].value as? String, "0")
        let redact = app.scrollTo(app.switches["redactWidgets"])
        XCTAssertEqual(redact.value as? String, "1", "widgets hide values while locked by default")
        app.scrollTo(app.buttons["clearCache"]).tap()
        let clear = app.dialogButton("Clear the cache")
        XCTAssertTrue(clear.waitForExistence(timeout: 5), "clearing asks first")
        clear.tap()
        XCTAssertTrue(clear.waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.scrollTo(app.buttons["appSignOut"]).exists)

        app.navigationBars.buttons.element(boundBy: 0).tap()
        let row = app.scrollTo(app.buttons["notificationsRow"])
        let four = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label CONTAINS '4 of 5 on'"), object: row)
        XCTAssertEqual(XCTWaiter.wait(for: [four], timeout: 5), .completed)
    }
}

extension XCUIApplication {
    /// A confirmation dialog's button: iOS lists it more than once, so take the first.
    func dialogButton(_ label: String) -> XCUIElement {
        buttons.matching(NSPredicate(format: "label == %@", label)).firstMatch
    }
}

extension XCUIElement {
    /// Replaces a short right-aligned number: a double tap on it selects it, typing replaces it.
    func retype(_ text: String) {
        coordinate(withNormalizedOffset: CGVector(dx: 0.97, dy: 0.5)).doubleTap()
        typeText(text)
    }
}
