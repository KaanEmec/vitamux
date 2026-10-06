import CryptoKit
import XCTest

/// The real-stack smoke (J22.22, `make test-ios-stack`): the app as shipped (no `-uitest`, the
/// Keychain and the network) against a real `vitamux serve` on a throwaway database with a
/// synthetic slice, two-factor sign-in and a paused fake Withings connection
/// (scripts/ios-stack.sh). The server reaches the app as its stored server address, passed as a
/// launch argument (`-server`, read through the argument domain), so the server step starts
/// filled in. Skipped unless the script passes the stack as `TEST_RUNNER_VITAMUX_STACK_*`.
@MainActor
final class StackSmokeUITests: XCTestCase {
    private struct Stack {
        let url, user, password, totpSecret, withings, value, note: String

        init?(_ env: [String: String]) {
            guard let url = env["VITAMUX_STACK_URL"], let user = env["VITAMUX_STACK_USER"], let password = env["VITAMUX_STACK_PASSWORD"],
                  let totp = env["VITAMUX_STACK_TOTP_SECRET"], let withings = env["VITAMUX_STACK_WITHINGS"],
                  let value = env["VITAMUX_STACK_VALUE"], let note = env["VITAMUX_STACK_NOTE"] else { return nil }
            (self.url, self.user, self.password, totpSecret, self.withings, self.value, self.note) = (url, user, password, totp, withings, value, note)
        }
    }

    override func setUp() async throws { continueAfterFailure = false }

    func testServerSignInDashboardMetricOverrideWithingsSignOut() throws {
        guard let stack = Stack(ProcessInfo.processInfo.environment) else { throw XCTSkip("run via scripts/ios-stack.sh") }
        let app = XCUIApplication()
        // The app's stored server (UserDefaults "server": a ServerProfile as JSON) from the launch arguments.
        let profile = try JSONSerialization.data(withJSONObject: ["baseURL": stack.url])
        app.launchArguments = ["-server", "<" + profile.map { String(format: "%02x", $0) }.joined() + ">"]
        app.launch()

        // Server step: the address from the launch argument; then password and the TOTP code.
        let server = app.textFields["serverField"]
        XCTAssertTrue(server.waitForExistence(timeout: 15))
        XCTAssertEqual(server.value as? String, stack.url, "the launch argument points the app at the stack")
        app.signIn(server: stack.url, username: stack.user, password: stack.password)
        let code = app.textFields["codeField"]
        XCTAssertTrue(code.waitForExistence(timeout: 15), "the owner has two-factor on")
        code.tap()
        code.typeText(try totp(stack.totpSecret))
        app.buttons["signInButton"].tap()
        XCTAssertTrue(app.tabBars.buttons["Dashboard"].waitForExistence(timeout: 15), "signed in")

        // Dashboard: cards from the synthetic slice.
        let card = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'card-'")).firstMatch
        XCTAssertTrue(card.waitForExistence(timeout: 20), "the dashboard shows the slice's cards")
        app.assertNoProblem("dashboard")

        // A metric: resting heart rate over a month, with yesterday's value (the real catalogue's code; the fake calls it heart_rate_resting).
        app.openLink("vitamux://explore/resting_heart_rate?range=1M")
        let row = app.buttons["valueRow-\(ExploreUITests.day(-1))"]
        // The list loads its rows lazily, after the values arrive: scroll again until the row shows.
        for _ in 0 ..< 4 where !row.exists { _ = app.scrollTo(row).waitForExistence(timeout: 5) }
        XCTAssertTrue(row.exists, "yesterday has a resolved value")
        app.assertNoProblem("metric")

        // An override: set yesterday's value, with a note.
        row.tap()
        XCTAssertTrue(app.staticTexts["pointSource"].waitForExistence(timeout: 15))
        app.scrollTo(app.buttons["setValue"]).tap()
        let value = app.textFields["valueField"]
        XCTAssertTrue(value.waitForExistence(timeout: 5))
        value.tap()
        // Typed as the simulator's locale writes decimals (the field parses locale-aware).
        value.typeText(stack.value.replacingOccurrences(of: ".", with: Locale.current.decimalSeparator ?? "."))
        let note = app.textFields["noteField"]
        note.tap()
        note.typeText(stack.note)
        let save = app.buttons["saveOverride"]
        XCTAssertTrue(save.isEnabled)
        save.tap()
        XCTAssertTrue(save.waitForNonExistence(timeout: 15), "the server saved the override")
        // Back to the top of the sheet, where the point's status is.
        let status = app.element("pointStatus")
        for _ in 0 ..< 4 where !status.exists { app.collectionViews.firstMatch.swipeDown() }
        let overridden = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label CONTAINS 'Overridden'"), object: status)
        XCTAssertEqual(XCTWaiter.wait(for: [overridden], timeout: 15), .completed, "the point is overridden: \(status.label)")
        app.buttons["closePoint"].tap()

        // The fake Withings connection: on the Sources tab and in its detail, paused.
        app.tabBars.buttons["Sources"].tap()
        XCTAssertTrue(app.element("connection-withings").waitForExistence(timeout: 15), "the Withings connection is listed")
        app.openLink("vitamux://connections/\(stack.withings)")
        let health = app.element("detailHealth")
        XCTAssertTrue(health.waitForExistence(timeout: 15))
        XCTAssertTrue(health.label.contains("Paused"), health.label)

        // Sign-out revokes the session on the server.
        app.tabBars.buttons["More"].tap()
        app.scrollTo(app.buttons["signOutButton"]).tap()
        XCTAssertTrue(app.buttons["confirmSignOut"].waitForExistence(timeout: 5))
        app.buttons["confirmSignOut"].tap()
        XCTAssertTrue(app.element("signedOut").waitForExistence(timeout: 15))
    }

    /// RFC 6238 with the server's parameters (SHA-1, 6 digits, 30 s), from a base32 secret; waits
    /// out the last 3 s of a step so the code is still valid when it arrives.
    private func totp(_ secret: String) throws -> String {
        if 30 - Int(Date.now.timeIntervalSince1970) % 30 < 3 { sleep(3) }
        let alphabet = Array("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567")
        var bits = 0, buffer = 0
        var key = [UInt8]()
        for char in secret.uppercased() where char != "=" {
            let index = try XCTUnwrap(alphabet.firstIndex(of: char), "base32")
            buffer = buffer << 5 | index
            bits += 5
            if bits >= 8 {
                bits -= 8
                key.append(UInt8(buffer >> bits & 0xFF))
            }
        }
        let counter = withUnsafeBytes(of: UInt64(Date.now.timeIntervalSince1970 / 30).bigEndian) { Data($0) }
        let mac = Array(HMAC<Insecure.SHA1>.authenticationCode(for: counter, using: SymmetricKey(data: key)))
        let offset = Int(mac[19] & 0x0F)
        let number = (UInt32(mac[offset] & 0x7F) << 24 | UInt32(mac[offset + 1]) << 16 | UInt32(mac[offset + 2]) << 8 | UInt32(mac[offset + 3])) % 1_000_000
        return String(format: "%06u", number)
    }
}
