import Foundation

// The Swift twin of web/src/lib/rules/rule.ts: the typed rule of schemas/resolution-rule.v1.json
// as the builder's form, and the helpers the rules screens share
// (docs/architecture/resolution.md#rule-specification). `RuleForm.spec` writes exactly the JSON
// the panel's `toSpec` writes for the same form (fixtures/rule-model.json). The server validates
// every rule; nothing here repeats the catalogue checks.

/// A strategy (`strategy.op`) with its plain-language card (frontend.md#rule-builder).
public enum RuleOp: String, CaseIterable, Sendable {
    case firstAvailable = "first_available"
    case singleSource = "single_source"
    case meanAcrossSources = "mean_across_sources"
    case minimumAcrossSources = "minimum_across_sources"
    case maximumAcrossSources = "maximum_across_sources"
    case sumAcrossSources = "sum_across_sources"
    case latest
    case earliest
    case eventPriority = "event_priority"

    public var label: String {
        switch self {
        case .firstAvailable: "Use the first source with data"
        case .singleSource: "Use one source only"
        case .meanAcrossSources: "Average the sources"
        case .minimumAcrossSources: "Take the lowest"
        case .maximumAcrossSources: "Take the highest"
        case .sumAcrossSources: "Add the sources together"
        case .latest: "Use the newest value"
        case .earliest: "Use the oldest value"
        case .eventPriority: "Use the whole event from the first source"
        }
    }

    public var hint: String {
        switch self {
        case .firstAvailable: "Groups are tried in order, per window."
        case .singleSource: "Exactly one group; no fallback."
        case .meanAcrossSources: "Mean of the valid groups."
        case .minimumAcrossSources: "The lowest valid group."
        case .maximumAcrossSources: "The highest valid group."
        case .sumAcrossSources: "Sum of the valid groups. The same activity can be counted twice."
        case .latest, .earliest: "Ties go by group order."
        case .eventPriority: "Sleep or workouts, never spliced."
        }
    }

    /// The rule lens's strategy pills.
    public var short: String {
        switch self {
        case .firstAvailable: "First available"
        case .singleSource: "One source"
        case .meanAcrossSources: "Mean"
        case .minimumAcrossSources: "Lowest"
        case .maximumAcrossSources: "Highest"
        case .sumAcrossSources: "Sum"
        case .latest: "Latest"
        case .earliest: "Earliest"
        case .eventPriority: "Whole event"
        }
    }

    /// Combines several groups' values, so `min_sources` and `on_insufficient` apply.
    public var pooling: Bool {
        switch self {
        case .meanAcrossSources, .minimumAcrossSources, .maximumAcrossSources, .sumAcrossSources: true
        default: false
        }
    }
}

/// The window kinds and bucket sizes a rule names.
public enum RuleWindow {
    public static let kinds: [(kind: String, label: String)] = [
        ("bucket", "Fixed buckets (minutes)"),
        ("hour", "Local hour"),
        ("local_day", "Local day"),
        ("local_night", "Night (main sleep episode)"),
        ("sleep_episode", "Every sleep episode"),
        ("latest", "Latest value"),
        ("reading", "Each reading"),
    ]
    public static let bucketSizes = ["30s", "1m", "5m", "15m", "30m"]

    public static func label(kind: String) -> String {
        kinds.first { $0.kind == kind }?.label ?? kind
    }

    /// "5m buckets", or the kind's label.
    public static func label(_ window: JSONValue?) -> String {
        let kind = window?["kind"]?.string ?? ""
        if kind == "bucket", let size = window?["size"]?.string, !size.isEmpty { return "\(size) buckets" }
        return label(kind: kind)
    }
}

/// Values shared by the rule screens.
public enum RuleSpec {
    public static let schema = "vitamux.rule/1"
    /// The acknowledgement a sum needs before it can be saved.
    public static let sumWarning = "cross_source_sum_duplicate_risk"
    public static let qualityFlags = [
        "manual_entry", "motion_context", "implausible", "relayed", "migrated_without_raw", "prorated_source", "calibrating",
    ]
}

/// One selector: conditions that are ANDed, in the order they were written.
public struct RuleSelector: Hashable, Sendable {
    public enum Field: String, CaseIterable, Sendable {
        case provider
        case deviceType = "device_type"
        case relayed
        case originKey = "origin_key"
        case originKeyPrefix = "origin_key_prefix"
        case originName = "origin_name"
        case deviceManufacturer = "device_manufacturer"
        case deviceModel = "device_model"
        case deviceID = "device_id"
        case connectionID = "connection_id"
        case entry

        public var label: String {
            switch self {
            case .provider: "Provider"
            case .deviceType: "Device type"
            case .relayed: "Relayed"
            case .originKey: "Origin app id"
            case .originKeyPrefix: "Origin app id starts with"
            case .originName: "Origin app name"
            case .deviceManufacturer: "Brand"
            case .deviceModel: "Device model"
            case .deviceID: "Device id"
            case .connectionID: "Connection id"
            case .entry: "Entry"
            }
        }
    }

    public enum Value: Hashable, Sendable {
        case text(String)
        case flag(Bool)

        var json: JSONValue {
            switch self {
            case .text(let s): .string(s)
            case .flag(let b): .bool(b)
            }
        }

        var text: String {
            switch self {
            case .text(let s): s
            case .flag(let b): b ? "true" : "false"
            }
        }
    }

    public struct Condition: Hashable, Sendable {
        public var field: Field
        public var value: Value

        public init(_ field: Field, _ value: Value) {
            self.field = field
            self.value = value
        }
    }

    public var conditions: [Condition]

    public init(_ conditions: [Condition] = []) {
        self.conditions = conditions
    }

    public init(_ pairs: KeyValuePairs<Field, Value>) {
        conditions = pairs.map { Condition($0.key, $0.value) }
    }

    /// A selector from its JSON; unknown fields are left out (the schema rejects them).
    public init(json: JSONValue) {
        conditions = (json.members ?? []).compactMap { member in
            guard let field = Field(rawValue: member.key) else { return nil }
            switch member.value {
            case .bool(let b): return Condition(field, .flag(b))
            case .string(let s): return Condition(field, .text(s))
            case .number(let n): return Condition(field, .text(JSONValue.numberText(n)))
            default: return nil
            }
        }
    }

    public subscript(field: Field) -> Value? {
        conditions.first { $0.field == field }?.value
    }

    /// The JSON, without conditions whose text is empty.
    public var cleaned: JSONValue {
        .object(conditions.filter { $0.value != .text("") }.map { JSONValue.Member($0.field.rawValue, $0.value.json) })
    }

    var json: JSONValue {
        .object(conditions.map { JSONValue.Member($0.field.rawValue, $0.value.json) })
    }

    /// A stable text for the selector, to tell whether two are the same (`selectorKey`).
    public var key: String {
        JSONValue.array(conditions.sorted { $0.field.rawValue < $1.field.rawValue }.map {
            .array([.string($0.field.rawValue), $0.value.json])
        }).text
    }
}

/// The builder's form: text for every input, converted by `spec`. Unknown extensions
/// (`contexts`) pass through.
public struct RuleForm: Hashable, Sendable {
    public struct Group: Hashable, Sendable, Identifiable {
        /// Stable identity while the group is moved or renamed.
        public let key: UUID
        /// The group id in the rule (`garmin`, `apple_watch`).
        public var name: String
        public var match: [RuleSelector]

        public var id: UUID { key }

        public init(name: String, match: [RuleSelector], key: UUID = UUID()) {
            self.key = key
            self.name = name
            self.match = match
        }
    }

    public var metric: String
    public var windowKind: String
    public var bucketSize: String
    public var groups: [Group]
    public var exclude: [RuleSelector]
    public var op: RuleOp
    public var minSources = ""
    public var onInsufficient = ""
    public var intraGroup = ""
    public var dailyValuePolicy = ""
    public var statistic = ""
    public var span = ""
    public var minCoverage = ""
    public var rangeLow = ""
    public var rangeHigh = ""
    public var excludeFlags: [String] = []
    public var maxStaleness = ""
    public var requireWear = ""
    public var matchOverlap = ""
    public var minEpisodeCoverage = ""
    public var includeNaps = ""
    public var nightAnchor = ""
    public var follow = ""
    public var compose = ""
    public var acknowledged: [String] = []
    public var contexts: JSONValue?

    /// An empty rule for `metric`: one group with an empty provider.
    public static func blank(metric: String) -> RuleForm {
        RuleForm(metric: metric, windowKind: "local_day", bucketSize: "5m",
                 groups: [Group(name: "", match: [RuleSelector([.provider: .text("")])])], exclude: [], op: .firstAvailable)
    }

    public init(metric: String, windowKind: String, bucketSize: String, groups: [Group], exclude: [RuleSelector], op: RuleOp) {
        self.metric = metric
        self.windowKind = windowKind
        self.bucketSize = bucketSize
        self.groups = groups
        self.exclude = exclude
        self.op = op
    }

    /// The form for a rule spec (`fromSpec`).
    public init(spec r: JSONValue) {
        let q = r["quality"]
        let ws = r["within_source"]
        let sleep = q?["sleep"]
        let str = { (v: JSONValue?) in Self.text(v) }
        metric = r["metric"]?.string ?? ""
        windowKind = r["window"]?["kind"]?.string ?? ""
        bucketSize = r["window"]?["size"]?.string ?? "5m"
        groups = (r["groups"]?.array ?? []).map { g in
            Group(name: g["id"]?.string ?? "", match: (g["match"]?.array ?? []).map(RuleSelector.init(json:)))
        }
        exclude = (r["exclude"]?.array ?? []).map(RuleSelector.init(json:))
        op = r["strategy"]?["op"]?.string.flatMap(RuleOp.init(rawValue:)) ?? .firstAvailable
        minSources = str(r["strategy"]?["min_sources"])
        onInsufficient = str(r["strategy"]?["on_insufficient"])
        intraGroup = str(ws?["intra_group"])
        dailyValuePolicy = str(ws?["daily_value_policy"])
        statistic = str(ws?["statistic"])
        span = str(ws?["span"])
        minCoverage = str(q?["min_coverage"])
        rangeLow = str(q?["plausible_range"]?.array?.first)
        rangeHigh = str(q?["plausible_range"]?.array.flatMap { $0.count > 1 ? $0[1] : nil })
        excludeFlags = (q?["exclude_flags"]?.array ?? []).compactMap(\.string)
        maxStaleness = str(q?["max_staleness"])
        requireWear = str(q?["require_wear"])
        matchOverlap = str(sleep?["match_overlap"])
        minEpisodeCoverage = str(sleep?["min_episode_coverage"])
        includeNaps = str(sleep?["include_naps"])
        nightAnchor = str(sleep?["night_anchor"])
        follow = str(r["follow"])
        compose = str(r["compose"]?["op"])
        acknowledged = (r["acknowledged_warnings"]?.array ?? []).compactMap(\.string)
        contexts = r["contexts"]
    }

    /// `String(v)` of the panel: empty for a missing value or null.
    private static func text(_ v: JSONValue?) -> String {
        switch v {
        case nil, .null?: ""
        case .string(let s)?: s
        case .number(let n)?: JSONValue.numberText(n)
        case .bool(let b)?: b ? "true" : "false"
        case let other?: other.text
        }
    }

    /// The form sums across or within sources, so it needs the duplicate-risk acknowledgement.
    public var needsSumAck: Bool {
        op == .sumAcrossSources || intraGroup == "sum"
    }

    public var isSumAcknowledged: Bool {
        acknowledged.contains(RuleSpec.sumWarning)
    }

    /// The typed rule JSON for the form (`toSpec`): empty inputs are left out, numbers that are not
    /// numbers stay text so the server names the field. The server validates the rest.
    public var spec: JSONValue {
        let low = Self.number(rangeLow)
        let high = Self.number(rangeHigh)
        let trim = { (s: String) in s.trimmingCharacters(in: .whitespacesAndNewlines) }
        let sleep = Self.compact([
            ("match_overlap", Self.number(matchOverlap)),
            ("min_episode_coverage", Self.number(minEpisodeCoverage)),
            ("include_naps", includeNaps.isEmpty ? nil : .bool(includeNaps == "true")),
            ("night_anchor", .string(trim(nightAnchor))),
        ])
        let followed = trim(follow)
        return .ordered([
            "schema": .string(RuleSpec.schema),
            "metric": .string(metric),
            "window": windowKind == "bucket"
                ? .ordered(["kind": .string("bucket"), "size": .string(bucketSize)])
                : .ordered(["kind": .string(windowKind)]),
            "groups": .array(groups.map { .ordered(["id": .string(trim($0.name)), "match": .array($0.match.map(\.cleaned))]) }),
            "exclude": exclude.isEmpty ? nil : .array(exclude.map(\.cleaned)),
            "within_source": Self.compact([
                ("intra_group", .string(intraGroup)),
                ("daily_value_policy", .string(dailyValuePolicy)),
                ("statistic", .string(statistic)),
                ("span", statistic == "min_rolling_mean" ? .string(trim(span)) : nil),
            ]),
            "strategy": Self.compact([
                ("op", .string(op.rawValue)),
                ("min_sources", op.pooling ? Self.number(minSources) : nil),
                ("on_insufficient", op.pooling ? .string(onInsufficient) : nil),
            ]),
            "quality": Self.compact([
                ("min_coverage", Self.number(minCoverage)),
                // An input left empty is `undefined` in the panel's array, which JSON writes as null.
                ("plausible_range", low == nil && high == nil ? nil : .array([low ?? .null, high ?? .null])),
                ("exclude_flags", excludeFlags.isEmpty ? nil : .array(excludeFlags.map(JSONValue.string))),
                ("max_staleness", .string(trim(maxStaleness))),
                ("require_wear", .string(trim(requireWear))),
                ("sleep", sleep),
            ]),
            "contexts": contexts,
            "follow": followed.isEmpty ? nil : .string(followed),
            "compose": compose.isEmpty ? nil : .ordered(["from": .string("hour"), "op": .string(compose)]),
            "acknowledged_warnings": acknowledged.isEmpty ? nil : .array(acknowledged.map(JSONValue.string)),
        ])
    }

    /// A number when the text is one; otherwise the trimmed text; nil when empty.
    static func number(_ s: String) -> JSONValue? {
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty { return nil }
        if let n = Double(t), n.isFinite { return .number(n) }
        return .string(t)
    }

    /// The members that are set and not empty text; nil when none is.
    private static func compact(_ members: [(String, JSONValue?)]) -> JSONValue? {
        let kept = members.compactMap { key, value -> JSONValue.Member? in
            guard let value, value != .string("") else { return nil }
            return JSONValue.Member(key, value)
        }
        return kept.isEmpty ? nil : .object(kept)
    }
}

/// One field that differs between two rule specs.
public struct RuleChange: Hashable, Sendable {
    public var path: String
    public var before: JSONValue?
    public var after: JSONValue?
}

extension RuleSpec {
    /// Field-level differences between two rule specs, in the first spec's field order (`diffSpecs`).
    public static func diff(_ a: JSONValue?, _ b: JSONValue?) -> [RuleChange] {
        var fa: [(String, JSONValue)] = []
        var fb: [(String, JSONValue)] = []
        if let a { flatten(a, "", into: &fa) }
        if let b { flatten(b, "", into: &fb) }
        let before = Dictionary(fa) { first, _ in first }
        let after = Dictionary(fb) { first, _ in first }
        let paths = fa.map(\.0) + fb.map(\.0).filter { before[$0] == nil }
        return paths.compactMap { path in
            let x = before[path], y = after[path]
            guard x?.text != y?.text else { return nil }
            return RuleChange(path: path.hasPrefix(".") ? String(path.dropFirst()) : path, before: x, after: y)
        }
    }

    private static func flatten(_ v: JSONValue, _ path: String, into out: inout [(String, JSONValue)]) {
        switch v {
        case .array(let items) where !items.isEmpty:
            for (i, item) in items.enumerated() { flatten(item, "\(path)[\(i)]", into: &out) }
        case .object(let members) where !members.isEmpty:
            for m in members { flatten(m.value, "\(path).\(m.key)", into: &out) }
        default:
            out.append((path, v))
        }
    }
}
