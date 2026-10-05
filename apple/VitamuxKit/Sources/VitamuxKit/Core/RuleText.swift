import Foundation

// A rule in plain words, the twin of web/src/lib/rules/sentence.ts, chips.ts and preview.ts: what
// the rule lens, the catalogue and the builder show instead of the JSON. Neutral and descriptive:
// it says what the rule does, never whether a source or a value is better.

public enum RuleText {
    /// What one window is called: "night", "5-minute bucket".
    public static func windowNoun(_ window: JSONValue?) -> String {
        let kind = window?["kind"]?.string ?? ""
        if kind == "bucket" {
            // parseInt of the size: its leading digits ("30s" reads as 30, as in the panel).
            let digits = (window?["size"]?.string ?? "").prefix { $0.isASCII && $0.isNumber }
            let n = Int(digits) ?? 0
            return "\(n == 0 ? "?" : String(n))-minute bucket"
        }
        let nouns = ["hour": "hour", "local_day": "day", "local_night": "night", "sleep_episode": "sleep episode", "reading": "reading", "latest": "latest value"]
        return nouns[kind] ?? kind
    }

    /// "For each night, use the first source in order with data …" (`ruleSentence`).
    public static func sentence(_ r: JSONValue) -> String {
        let noun = windowNoun(r["window"])
        let lead = r["window"]?["kind"]?.string == "latest" ? "For the latest value" : "For each \(noun)"
        let covered: String
        if let cov = r["quality"]?["min_coverage"]?.number, cov > 0 {
            covered = " with at least \(Int((cov * 100).rounded(.toNearestOrAwayFromZero)))% coverage"
        } else {
            covered = ""
        }
        let rawOp = r["strategy"]?["op"]?.string ?? ""
        let op = RuleOp(rawValue: rawOp)
        let atLeast: String
        if op?.pooling == true, let min = r["strategy"]?["min_sources"]?.number, min > 1 {
            atLeast = ", when at least \(JSONValue.numberText(min)) have data"
        } else {
            atLeast = ""
        }
        let none = "; if none has, the \(noun) has no value"
        let first = r["groups"]?.array?.first?["id"]?.string ?? "one source"
        let body: String
        switch op {
        case .singleSource: body = "use only \(first)\(covered); without it, the \(noun) has no value"
        case .firstAvailable: body = "use the first source in order \(covered.isEmpty ? "with data" : String(covered.dropFirst()))\(none)"
        case .meanAcrossSources: body = "average the sources\(covered)\(atLeast)"
        case .minimumAcrossSources: body = "take the lowest of the sources\(covered)\(atLeast)"
        case .maximumAcrossSources: body = "take the highest of the sources\(covered)\(atLeast)"
        case .sumAcrossSources: body = "add the sources together\(covered)\(atLeast); the same activity can be counted twice"
        case .latest: body = "use the newest value of the sources\(covered); ties go by order"
        case .earliest: body = "use the oldest value of the sources\(covered); ties go by order"
        case .eventPriority: body = "use the whole event from the first source in order that recorded one\(covered)"
        case nil: body = rawOp
        }
        var out = ["\(lead), \(body)."]
        if r["compose"] != nil { out.append("Each hour is picked first, then the hours are added up.") }
        if let follow = r["follow"]?.string, !follow.isEmpty { out.append("It uses the source that \(follow) selected when it can.") }
        let excluded = r["exclude"]?.array?.count ?? 0
        if excluded > 0 { out.append("\(excluded) excluded \(excluded == 1 ? "source is" : "sources are") never used.") }
        return out.joined(separator: " ")
    }

    /// The built-in group ids by name: `apple_watch` is "Apple Watch", `garmin_apple` "Garmin via
    /// Apple Health". Other ids stay as written.
    public static func groupLabel(_ id: String) -> String {
        if id == "apple_watch" { return "Apple Watch" }
        if id == "iphone" { return "iPhone" }
        guard id.hasSuffix("_apple") else { return id }
        let brand = String(id.dropLast(6))
        guard !brand.isEmpty, brand.allSatisfy({ $0.isASCII && ($0.isLowercase || $0.isNumber) }) else { return id }
        let name = brand == "whoop" ? "WHOOP" : brand.prefix(1).uppercased() + brand.dropFirst()
        return "\(name) via Apple Health"
    }

    /// A company name without its legal suffix: "Apple Inc." is "Apple".
    public static func brandName(_ manufacturer: String) -> String {
        let suffix = /(?:[\s,]+(?:inc|ltd|llc|gmbh|corp|corporation|co|limited)\.?)+$/.ignoresCase()
        let trimmed = manufacturer.replacing(suffix, with: "").trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? manufacturer : trimmed
    }

    /// One readable line per selector: "provider apple_health, device type watch, not relayed". A
    /// selector that is one of the named `choices` reads as its label.
    public static func selector(_ s: RuleSelector, choices: [RuleChoice] = []) -> String {
        if let named = choices.first(where: { $0.selector.key == s.key }) { return named.label }
        return s.conditions.map { c in
            switch (c.field, c.value) {
            case (.relayed, .flag(let on)): on ? "relayed" : "not relayed"
            case (.deviceManufacturer, let v): "brand \(brandName(v.text))"
            case (let field, let v): "\(field.label.lowercased()) \(v.text)"
            }
        }.joined(separator: ", ")
    }

    /// The provider a group names, for its colour; else the group id.
    public static func provider(of group: RuleForm.Group) -> String {
        for s in group.match {
            if case .text(let p)? = s[.provider], !p.isEmpty { return p }
        }
        return group.name
    }
}

/// A one-click selector: a named source or device, an origin app, a device type.
public struct RuleChoice: Hashable, Sendable {
    public var label: String
    public var selector: RuleSelector

    public init(label: String, selector: RuleSelector) {
        self.label = label
        self.selector = selector
    }

    /// Transport providers (those with origins) get relayed and direct chips, and their device
    /// types the direct ones; then each origin app (`selectorChips`).
    public static func chips(origins: [Components.Schemas.DataOrigin], devices: [Components.Schemas.SourceDevice]) -> [RuleChoice] {
        var out = Ordered()
        let add = { (s: RuleSelector, out: inout Ordered) in out.set(RuleText.selector(s), RuleChoice(label: RuleText.selector(s), selector: s)) }
        var transports: [String] = []
        for o in origins where !transports.contains(o.provider) { transports.append(o.provider) }
        for p in transports {
            add(RuleSelector([.provider: .text(p), .relayed: .flag(false)]), &out)
            add(RuleSelector([.provider: .text(p), .relayed: .flag(true)]), &out)
        }
        for d in devices {
            guard let type = d.deviceType, d.mergedInto == nil else { continue } // a merged device's records carry its target's type
            add(transports.contains(d.provider)
                ? RuleSelector([.provider: .text(d.provider), .deviceType: .text(type), .relayed: .flag(false)])
                : RuleSelector([.provider: .text(d.provider), .deviceType: .text(type)]), &out)
        }
        for o in origins { add(RuleSelector([.provider: .text(o.provider), .originKey: .text(o.originKey)]), &out) }
        return out.values
    }

    /// Named choices for "Choose a source or device", from the owner's own devices and providers
    /// (`sourceChoices`): whole sources, a brand, a model, or one device the owner named. Merged
    /// devices are left out; their records live on the target.
    public static func sources(
        devices: [Components.Schemas.SourceDevice], providers: [Components.Schemas.Provider], name fallback: @escaping (String) -> String = SourceStyle.label
    ) -> [RuleChoice] {
        let live = devices.filter { $0.mergedInto == nil }
        var out = Ordered()
        let add = { (label: String, s: RuleSelector, out: inout Ordered) in out.set(s.key, RuleChoice(label: label, selector: s)) }
        let name = { (code: String) in providers.first { $0.code == code }?.name ?? fallback(code) }
        var codes: [String] = []
        for code in live.map(\.provider) + providers.filter({ $0.connections > 0 }).map(\.code) where !codes.contains(code) { codes.append(code) }
        for code in codes.sorted(by: { name($0).localizedCompare(name($1)) == .orderedAscending }) {
            add("\(name(code)) (all data)", RuleSelector([.provider: .text(code)]), &out)
        }
        let brands = Set(live.compactMap(\.manufacturer)).sorted()
        for m in brands {
            let models = Set(live.filter { $0.manufacturer == m }.compactMap(\.model)).sorted()
            if isApple(m) {
                for model in models {
                    add(deviceLabel(manufacturer: m, model: model),
                        RuleSelector([.provider: .text("apple_health"), .deviceManufacturer: .text(m), .deviceModel: .text(model)]), &out)
                }
                continue
            }
            add("\(RuleText.brandName(m)) (any device)", RuleSelector([.deviceManufacturer: .text(m)]), &out)
            for model in models {
                add(deviceLabel(manufacturer: m, model: model), RuleSelector([.deviceManufacturer: .text(m), .deviceModel: .text(model)]), &out)
            }
        }
        for d in live { if let label = d.name, !label.isEmpty { add(label, RuleSelector([.deviceID: .text(d.id)]), &out) } }
        return out.values
    }

    /// A device's name as people say it: "Apple Watch", "iPhone", "Garmin Forerunner" (no doubled brand).
    public static func deviceLabel(manufacturer: String, model: String) -> String {
        let brand = RuleText.brandName(manufacturer)
        if model.lowercased().hasPrefix(brand.lowercased()) { return model }
        if isApple(manufacturer), model.lowercased().hasPrefix("iphone") || model.lowercased().hasPrefix("ipad") || model.lowercased().hasPrefix("ipod") {
            return model
        }
        return "\(brand) \(model)"
    }

    private static func isApple(_ manufacturer: String) -> Bool {
        manufacturer.firstMatch(of: /^apple\b/.ignoresCase()) != nil
    }

    /// A Map's order: the first insertion keeps its place, the last value wins.
    private struct Ordered {
        private var keys: [String] = []
        private var byKey: [String: RuleChoice] = [:]

        mutating func set(_ key: String, _ value: RuleChoice) {
            if byKey[key] == nil { keys.append(key) }
            byKey[key] = value
        }

        var values: [RuleChoice] { keys.compactMap { byKey[$0] } }
    }
}

/// Reading a draft preview (`POST /resolution/preview`): which days change and by how much.
public enum RulePreview {
    public typealias Day = Components.Schemas.PreviewDay
    public typealias Resolved = Components.Schemas.ResolvedValue

    public struct Summary: Hashable, Sendable {
        /// The local dates whose value, status or source changes.
        public var changed: [String]
        /// The shift of the mean (numbers only).
        public var shift: Double?
        /// Days that lose their value.
        public var newGaps: Int
        public var unit: String
    }

    /// The group the value came from, or "".
    public static func selectedGroup(_ r: Resolved) -> String {
        (r.inputs?.first { $0.selected == true }?.group) ?? ""
    }

    public static func number(_ r: Resolved) -> Double? {
        JSONValue(r.value).number
    }

    /// The draft gives another value, status or source than the rule in effect.
    public static func changed(_ d: Day) -> Bool {
        JSONValue(d.draft.value) != JSONValue(d.active.value) || d.draft.status != d.active.status || selectedGroup(d.draft) != selectedGroup(d.active)
    }

    public static func summary(_ days: [Day]) -> Summary {
        let mean = { (xs: [Double]) in xs.isEmpty ? nil : xs.reduce(0, +) / Double(xs.count) }
        let draft = mean(days.compactMap { number($0.draft) })
        let active = mean(days.compactMap { number($0.active) })
        return Summary(
            changed: days.filter(changed).map(\.localDate),
            shift: draft.flatMap { d in active.map { d - $0 } },
            newGaps: days.filter { JSONValue($0.active.value) != .null && JSONValue($0.draft.value) == .null }.count,
            unit: days.lazy.compactMap(\.draft.unit).first ?? ""
        )
    }
}
