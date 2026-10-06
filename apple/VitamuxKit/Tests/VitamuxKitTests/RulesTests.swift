import Foundation
import Testing
@testable import VitamuxKit

/// The repository root, from this file.
private let root = URL(filePath: #filePath).deletingLastPathComponent().appending(path: "../../../..").standardized

private func json(_ path: String) throws -> JSONValue {
    try JSONValue(parsing: Data(contentsOf: root.appending(path: path)))
}

/// A fixture form (the panel's `Form` without group keys) as a `RuleForm`.
private func form(_ f: JSONValue) -> RuleForm {
    let s = { (key: String) in f[key]?.string ?? "" }
    let list = { (key: String) in (f[key]?.array ?? []).compactMap(\.string) }
    var out = RuleForm(
        metric: s("metric"), windowKind: s("windowKind"), bucketSize: s("bucketSize"),
        groups: (f["groups"]?.array ?? []).map { g in
            RuleForm.Group(name: g["id"]?.string ?? "", match: (g["match"]?.array ?? []).map(RuleSelector.init(json:)))
        },
        exclude: (f["exclude"]?.array ?? []).map(RuleSelector.init(json:)),
        op: RuleOp(rawValue: s("op"))!
    )
    out.minSources = s("minSources")
    out.onInsufficient = s("onInsufficient")
    out.intraGroup = s("intraGroup")
    out.dailyValuePolicy = s("dailyValuePolicy")
    out.statistic = s("statistic")
    out.span = s("span")
    out.minCoverage = s("minCoverage")
    out.rangeLow = s("rangeLow")
    out.rangeHigh = s("rangeHigh")
    out.excludeFlags = list("excludeFlags")
    out.maxStaleness = s("maxStaleness")
    out.requireWear = s("requireWear")
    out.matchOverlap = s("matchOverlap")
    out.minEpisodeCoverage = s("minEpisodeCoverage")
    out.includeNaps = s("includeNaps")
    out.nightAnchor = s("nightAnchor")
    out.follow = s("follow")
    out.compose = s("compose")
    out.acknowledged = list("acknowledged")
    out.contexts = f["contexts"]
    return out
}

/// Two forms with the same inputs, whatever their group keys.
private func same(_ a: RuleForm, _ b: RuleForm) -> Bool {
    var a = a
    a.groups = zip(a.groups, b.groups).map { RuleForm.Group(name: $0.name, match: $0.match, key: $1.key) } + a.groups.dropFirst(b.groups.count)
    return a == b
}

/// fixtures/rule-model.json, shared with web/e2e/rule-model.spec.ts.
struct RuleModelTests {
    let cases: [JSONValue]
    let schema: JSONValue

    init() throws {
        let fixture = try json("fixtures/rule-model.json")
        #expect(fixture["synthetic"] == .bool(true))
        cases = fixture["cases"]?.array ?? []
        schema = try json("schemas/resolution-rule.v1.json")
    }

    @Test func `every case saves the panel's exact JSON and sentence`() throws {
        #expect(cases.count > 20)
        for c in cases {
            let note = c["note"]?.string ?? ""
            let f = form(try #require(c["form"]))
            #expect(f.spec.text == c["spec"]?.string, "\(note)")
            #expect(RuleText.sentence(f.spec) == c["sentence"]?.string, "\(note)")
            if let rule = c["rule"] {
                #expect(same(RuleForm(spec: rule), f), "fromSpec: \(note)")
            }
        }
        let ops = Set(cases.compactMap { try? JSONValue(parsing: Data(($0["spec"]?.string ?? "").utf8))["strategy"]?["op"]?.string })
        #expect(ops == Set(RuleOp.allCases.map(\.rawValue)))
    }

    @Test func `the rules the panel can save are valid against the schema`() throws {
        // Left for the server to name: empty ids and selectors, text for a number, half a range.
        let invalid = ["an empty rule", "inputs are trimmed, empty fields left out, bad numbers kept as text", "a span without min_rolling_mean is left out; naps off"]
        for c in cases {
            let note = c["note"]?.string ?? ""
            let spec = try JSONValue(parsing: Data((c["spec"]?.string ?? "").utf8))
            let problems = SchemaCheck(root: schema).problems(spec)
            if invalid.contains(note) {
                #expect(!problems.isEmpty, "\(note) is rejected")
            } else {
                #expect(problems.isEmpty, "\(note): \(problems)")
            }
        }
    }

    @Test func `every valid example survives a round trip unchanged`() throws {
        let dir = root.appending(path: "internal/resolve/testdata/valid")
        let names = try FileManager.default.contentsOfDirectory(atPath: dir.path()).filter { $0.hasSuffix(".json") }
        #expect(names.count >= 10)
        for name in names {
            let rule = try JSONValue(parsing: Data(contentsOf: dir.appending(path: name)))
            #expect(RuleForm(spec: rule).spec.sorted.text == Self.dropEmptyArrays(rule).sorted.text, "\(name)")
        }
    }

    /// `acknowledged_warnings: []` means the same as leaving it out.
    static func dropEmptyArrays(_ v: JSONValue) -> JSONValue {
        switch v {
        case .object(let members): .object(members.filter { $0.value != .array([]) }.map { .init($0.key, dropEmptyArrays($0.value)) })
        case .array(let items): .array(items.map(dropEmptyArrays))
        default: v
        }
    }

    @Test func `a whole-number spec value reaches the server as an integer`() throws {
        var f = RuleForm.blank(metric: "steps")
        f.groups = [.init(name: "watch", match: [RuleSelector([.deviceType: .text("watch")])])]
        f.op = .meanAcrossSources
        f.minSources = "2"
        f.minCoverage = "0.5"
        let container = try f.spec.container
        let strategy = try #require(container.value["strategy"] as? [String: (any Sendable)?])
        #expect(strategy["min_sources"] as? Int == 2)
        let quality = try #require(container.value["quality"] as? [String: (any Sendable)?])
        #expect(quality["min_coverage"] as? Double == 0.5)
        #expect(JSONValue(container).sorted == f.spec.sorted)
    }

    @Test func `only a sum needs the acknowledgement`() {
        var f = RuleForm.blank(metric: "steps")
        #expect(!f.needsSumAck)
        f.op = .sumAcrossSources
        #expect(f.needsSumAck && !f.isSumAcknowledged)
        f.acknowledged = [RuleSpec.sumWarning]
        #expect(f.isSumAcknowledged)
        f.op = .firstAvailable
        f.intraGroup = "sum"
        #expect(f.needsSumAck)
    }

    @Test func `the diff lists changed fields in the first spec's order`() throws {
        let a = try JSONValue(parsing: Data(#"{"metric":"x","groups":[{"id":"a"},{"id":"b"}],"strategy":{"op":"first_available"}}"#.utf8))
        let b = try JSONValue(parsing: Data(#"{"metric":"x","groups":[{"id":"b"},{"id":"a"}],"strategy":{"op":"mean_across_sources","min_sources":2}}"#.utf8))
        let changes = RuleSpec.diff(a, b)
        #expect(changes.map(\.path) == ["groups[0].id", "groups[1].id", "strategy.op", "strategy.min_sources"])
        #expect(changes[2].before == .string("first_available") && changes[2].after == .string("mean_across_sources"))
        #expect(changes[3].before == nil)
        #expect(RuleSpec.diff(a, a).isEmpty)
    }

    @Test func `names read as people say them`() {
        #expect(RuleText.groupLabel("apple_watch") == "Apple Watch")
        #expect(RuleText.groupLabel("garmin_apple") == "Garmin via Apple Health")
        #expect(RuleText.groupLabel("whoop_apple") == "WHOOP via Apple Health")
        #expect(RuleText.groupLabel("chest_strap") == "chest_strap")
        #expect(RuleText.brandName("Apple Inc.") == "Apple")
        #expect(RuleText.brandName("Example Devices GmbH") == "Example Devices")
        let s = RuleSelector([.provider: .text("apple_health"), .deviceType: .text("watch"), .relayed: .flag(false)])
        #expect(RuleText.selector(s) == "provider apple_health, device type watch, not relayed")
        #expect(RuleText.selector(RuleSelector([.deviceManufacturer: .text("Garmin Ltd.")])) == "brand Garmin")
        #expect(RuleText.selector(s, choices: [RuleChoice(label: "Apple Watch", selector: s)]) == "Apple Watch")
        #expect(RuleChoice.deviceLabel(manufacturer: "Apple Inc.", model: "iPhone") == "iPhone")
        #expect(RuleChoice.deviceLabel(manufacturer: "Apple Inc.", model: "Watch") == "Apple Watch")
        #expect(RuleChoice.deviceLabel(manufacturer: "Garmin", model: "Garmin Runner") == "Garmin Runner")
    }

    @Test func `the parser keeps member order and the writer matches JSON.stringify`() throws {
        let text = #"{"b":1,"a":[true,null,0.5,-2,"x\"\né"],"c":{}}"#
        let value = try JSONValue(parsing: Data(text.utf8))
        #expect(value.members?.map(\.key) == ["b", "a", "c"])
        #expect(value.text == #"{"b":1,"a":[true,null,0.5,-2,"x\"\né"],"c":{}}"#)
        #expect(JSONValue.numberText(1e-7) == "1e-7")
        #expect(JSONValue.numberText(230) == "230")
        #expect(throws: JSONValue.ParseError.self) { try JSONValue(parsing: Data("{".utf8)) }
    }
}

/// The subset of JSON Schema (2020-12) that schemas/resolution-rule.v1.json uses.
private struct SchemaCheck {
    let root: JSONValue

    func problems(_ value: JSONValue) -> [String] {
        var out: [String] = []
        check(value, root, "", &out)
        return out
    }

    private func resolve(_ schema: JSONValue) -> JSONValue {
        guard let ref = schema["$ref"]?.string, ref.hasPrefix("#/$defs/") else { return schema }
        return root["$defs"]?[String(ref.dropFirst(8))] ?? schema
    }

    private func valid(_ value: JSONValue, _ schema: JSONValue) -> Bool {
        var out: [String] = []
        check(value, schema, "", &out)
        return out.isEmpty
    }

    private func check(_ value: JSONValue, _ raw: JSONValue, _ path: String, _ out: inout [String]) {
        let schema = resolve(raw)
        func fail(_ why: String) { out.append("\(path.isEmpty ? "/" : path): \(why)") }
        if let type = schema["type"]?.string {
            let ok = switch (type, value) {
            case ("object", .object), ("array", .array), ("string", .string), ("boolean", .bool): true
            case ("number", .number): true
            case ("integer", .number(let n)): n == n.rounded()
            default: false
            }
            if !ok { return fail("not \(type)") }
        }
        if let c = schema["const"], c != value { fail("not \(c.text)") }
        if let options = schema["enum"]?.array, !options.contains(value) { fail("\(value.text) not allowed") }
        if case .string(let s) = value {
            if let p = schema["pattern"]?.string, ((try? Regex(p)).flatMap { try? $0.firstMatch(in: s) }) == nil { fail("\(s) does not match \(p)") }
            if let n = schema["minLength"]?.number, Double(s.count) < n { fail("too short") }
            if let n = schema["maxLength"]?.number, Double(s.count) > n { fail("too long") }
        }
        if case .number(let n) = value {
            if let m = schema["minimum"]?.number, n < m { fail("below \(m)") }
            if let m = schema["maximum"]?.number, n > m { fail("above \(m)") }
            if let m = schema["exclusiveMinimum"]?.number, n <= m { fail("not above \(m)") }
        }
        if case .array(let items) = value {
            if let n = schema["minItems"]?.number, Double(items.count) < n { fail("too few items") }
            if let n = schema["maxItems"]?.number, Double(items.count) > n { fail("too many items") }
            if schema["uniqueItems"] == .bool(true), Set(items).count != items.count { fail("items repeat") }
            if let item = schema["items"] { for (i, x) in items.enumerated() { check(x, item, "\(path)/\(i)", &out) } }
            if let contains = schema["contains"], !items.contains(where: { valid($0, contains) }) { fail("contains nothing required") }
        }
        if case .object(let members) = value {
            let props = schema["properties"]?.members ?? []
            for m in members {
                if let p = props.first(where: { $0.key == m.key }) {
                    check(m.value, p.value, "\(path)/\(m.key)", &out)
                } else if schema["additionalProperties"] == .bool(false) {
                    fail("unknown field \(m.key)")
                }
            }
            for key in schema["required"]?.array?.compactMap(\.string) ?? [] where !members.contains(where: { $0.key == key }) {
                fail("missing \(key)")
            }
            if let n = schema["minProperties"]?.number, Double(members.count) < n { fail("too few fields") }
        }
        if let anyOf = schema["anyOf"]?.array, !anyOf.contains(where: { valid(value, $0) }) { fail("matches no option") }
        if let not = schema["not"], valid(value, not) { fail("matches a forbidden shape") }
        for sub in schema["allOf"]?.array ?? [] { check(value, sub, path, &out) }
        if let condition = schema["if"] {
            if valid(value, condition) {
                if let then = schema["then"] { check(value, then, path, &out) }
            } else if let otherwise = schema["else"] {
                check(value, otherwise, path, &out)
            }
        }
    }
}

/// The rules endpoints of the fake server (FakeServer+Rules.swift), through the generated client.
struct RulesFakeTests {
    let fake = FakeServer()
    let sessions = SessionStore.inMemory()
    let client: Client

    init() async throws {
        client = fake.client(sessions: sessions)
        let body = Components.Schemas.LoginRequest(username: FakeServer.Owner.username, password: FakeServer.Owner.password, client: .app, deviceName: "Test iPhone")
        guard case .AppSession(let session) = try await client.login(body: .json(body)).ok.body.json else {
            Issue.record("expected an app session")
            return
        }
        try sessions.save(session, for: fake.profile)
    }

    private func save(_ form: RuleForm, activate: Bool = false) async throws -> Components.Schemas.RuleVersion {
        try await client.createRuleVersion(path: .init(metric: form.metric), body: .json(.init(spec: try form.spec.container, activate: activate))).created.body.json
    }

    private func heartRateResting() async throws -> RuleForm {
        let versions = try await client.listRuleVersions(path: .init(metric: "heart_rate_resting")).ok.body.json.versions
        return RuleForm(spec: JSONValue(try #require(versions.first { $0.active }).spec))
    }

    @Test func `the catalogue lists built-in and default rules`() async throws {
        let rules = try await client.listRules().ok.body.json.rules
        #expect(rules.map(\.metric).contains("heart_rate_resting"))
        let spo2 = try #require(rules.first { $0.metric == "spo2" })
        #expect(spo2._default && spo2.builtin)
        #expect(rules.allSatisfy { $0.active })
    }

    @Test func `the first save copies the built-in, and activation switches back`() async throws {
        var form = try await heartRateResting()
        form.groups.swapAt(1, 2)
        let saved = try await save(form, activate: true)
        #expect(saved.version == 2 && saved.active)
        var versions = try await client.listRuleVersions(path: .init(metric: "heart_rate_resting")).ok.body.json.versions
        #expect(versions.map(\.version) == [2, 1])
        #expect(versions[1].basedOn == "builtin:heart_rate_resting:1")
        // The spec comes back exactly as the phone wrote it.
        #expect(JSONValue(versions[0].spec).sorted == form.spec.sorted)

        let back = try await client.activateRule(path: .init(metric: "heart_rate_resting"), body: .json(.init(version: 1))).ok.body.json
        #expect(back.version == 1 && back.active)
        versions = try await client.listRuleVersions(path: .init(metric: "heart_rate_resting")).ok.body.json.versions
        #expect(versions.first { $0.active }?.version == 1)
    }

    @Test func `an unacknowledged sum and a bad group id are refused with pointers`() async throws {
        var form = RuleForm.blank(metric: "steps")
        form.groups = [.init(name: "watch", match: [RuleSelector([.deviceType: .text("watch")])])]
        form.op = .sumAcrossSources
        let sum = await #expect(throws: (any Error).self) { try await save(form) }.map(Problem.init)
        #expect(sum?.status == 409 && sum?.code == "rule_warning_unacknowledged")
        form.acknowledged = [RuleSpec.sumWarning]
        form.groups[0].name = "Bad Id"
        let bad = await #expect(throws: (any Error).self) { try await save(form) }.map(Problem.init)
        #expect(bad?.detail(for: "/spec/groups/0/id") != nil)
        form.groups[0].name = "watch"
        #expect(try await save(form).version == 2)
    }

    @Test func `the preview marks the days a draft changes`() async throws {
        var form = try await heartRateResting()
        let request = { (spec: JSONValue) async throws in
            try await client.previewResolution(body: .json(.init(spec: try spec.container, startDate: "2026-01-01", endDate: "2026-01-14"))).ok.body.json
        }
        let same = try await request(form.spec)
        #expect(same.days.count == 14 && same.days.allSatisfy { !RulePreview.changed($0) })
        form.groups.swapAt(0, 2)
        let draft = try await request(form.spec)
        let summary = RulePreview.summary(draft.days)
        #expect(summary.changed == ["2026-01-03", "2026-01-06", "2026-01-10"])
        #expect(summary.newGaps == 0 && summary.unit == "bpm")
        #expect(abs((summary.shift ?? 0) - 1.5 * 3 / 14) < 1e-9)
        #expect(RulePreview.selectedGroup(draft.days[2].draft) == "apple_watch")
    }

    @Test func `one-click selectors come from origins and devices`() async throws {
        let origins = try await client.listOrigins().ok.body.json.origins
        let devices = try await client.listSourceDevices().ok.body.json.devices
        let providers = try await client.listProviders().ok.body.json.providers
        let chips = RuleChoice.chips(origins: origins, devices: devices).map(\.label)
        #expect(chips.prefix(2) == ["provider apple_health, not relayed", "provider apple_health, relayed"])
        #expect(chips.contains("provider apple_health, origin app id com.example.connect"))
        let choices = RuleChoice.sources(devices: devices, providers: providers).map(\.label)
        #expect(choices.contains("Apple Watch") && choices.contains("iPhone") && choices.contains("Garmin (any device)"))
        #expect(choices.contains("Training watch"))
    }
}
