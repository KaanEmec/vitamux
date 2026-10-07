import Foundation
import HealthBridgeCore

/// The repository root, from this file's path (apple/HealthBridgeKit/Tests/HealthBridgeHealthKitTests).
let repoRoot = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
    .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()

func example(_ name: String) throws -> [String: Any] {
    let url = repoRoot.appending(path: "schemas/examples/healthkit-samples.v1/\(name).json")
    return try JSONSerialization.jsonObject(with: Data(contentsOf: url)) as! [String: Any]
}

func jsonObject(_ value: some Encodable) throws -> Any {
    try JSONSerialization.jsonObject(with: JSONEncoder().encode(value))
}

/// A small checker for the subset of JSON Schema that schemas/healthkit-samples.v1.json uses (`$ref`,
/// `type`, `required`, `properties`, `additionalProperties`, `items`, `maxItems`, bounds, `pattern`,
/// `format`). It is stricter than the schema in one way: an object key the schema does not declare is
/// an error unless the schema allows additional properties, so a misspelt payload field fails.
struct SchemaCheck {
    let root: [String: Any]

    init() throws {
        let url = repoRoot.appending(path: "schemas/healthkit-samples.v1.json")
        root = try JSONSerialization.jsonObject(with: Data(contentsOf: url)) as! [String: Any]
    }

    func errors(_ page: SamplesPage) throws -> [String] { errors(try jsonObject(page), root, "$") }

    func errors(_ value: Any, _ schema: [String: Any], _ path: String) -> [String] {
        if let ref = schema["$ref"] as? String {
            let name = ref.replacingOccurrences(of: "#/$defs/", with: "")
            return errors(value, (root["$defs"] as! [String: Any])[name] as! [String: Any], path)
        }
        var out: [String] = []
        if let type = schema["type"] {
            let types = (type as? [String]) ?? [type as! String]
            if !types.contains(where: { Self.matches(value, $0) }) { return ["\(path): not \(types)"] }
        }
        if let object = value as? [String: Any] {
            for key in (schema["required"] as? [String]) ?? [] where object[key] == nil { out.append("\(path).\(key): missing") }
            let properties = (schema["properties"] as? [String: Any]) ?? [:]
            for (key, child) in object {
                if let sub = properties[key] as? [String: Any] {
                    out += errors(child, sub, "\(path).\(key)")
                } else if let extra = schema["additionalProperties"] as? [String: Any] {
                    out += errors(child, extra, "\(path).\(key)")
                } else if schema["additionalProperties"] == nil {
                    out.append("\(path).\(key): not in the schema")
                }
            }
        }
        if let array = value as? [Any] {
            if let max = schema["maxItems"] as? Int, array.count > max { out.append("\(path): more than \(max) items") }
            if let items = schema["items"] as? [String: Any] {
                for (i, item) in array.enumerated() { out += errors(item, items, "\(path)[\(i)]") }
            }
        }
        if let n = value as? NSNumber, !Self.isBool(n) {
            let d = n.doubleValue
            if let min = schema["minimum"] as? Double, d < min { out.append("\(path): below \(min)") }
            if let max = schema["maximum"] as? Double, d > max { out.append("\(path): above \(max)") }
            if let min = schema["exclusiveMinimum"] as? Double, d <= min { out.append("\(path): not above \(min)") }
        }
        if let s = value as? String {
            if let pattern = schema["pattern"] as? String, s.range(of: pattern, options: .regularExpression) == nil {
                out.append("\(path): \(s) does not match \(pattern)")
            }
            switch schema["format"] as? String {
            case "date-time":
                let fractional = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
                if (try? Date(s, strategy: fractional)) == nil, (try? Date(s, strategy: .iso8601)) == nil { out.append("\(path): not a date-time") }
            case "date":
                if s.range(of: #"^\d{4}-\d{2}-\d{2}$"#, options: .regularExpression) == nil { out.append("\(path): not a date") }
            default: break
            }
        }
        return out
    }

    static func isBool(_ n: NSNumber) -> Bool { CFGetTypeID(n) == CFBooleanGetTypeID() }

    static func matches(_ value: Any, _ type: String) -> Bool {
        switch type {
        case "object": return value is [String: Any]
        case "array": return value is [Any]
        case "string": return value is String
        case "boolean": return (value as? NSNumber).map(isBool) ?? false
        case "number": return (value as? NSNumber).map { !isBool($0) } ?? false
        case "integer": return (value as? NSNumber).map { !isBool($0) && $0.doubleValue.rounded() == $0.doubleValue } ?? false
        default: return false
        }
    }
}

/// `value` without the keys in `drop`, recursively, as sorted-key JSON for comparison.
func canonical(_ value: Any, dropping drop: Set<String> = []) throws -> String {
    func strip(_ v: Any) -> Any {
        if let d = v as? [String: Any] { return d.filter { !drop.contains($0.key) }.mapValues(strip) }
        if let a = v as? [Any] { return a.map(strip) }
        return v
    }
    let data = try JSONSerialization.data(withJSONObject: strip(value), options: [.sortedKeys, .withoutEscapingSlashes])
    // The kit always writes milliseconds; the examples mostly omit zero ones. Both are RFC 3339.
    return String(decoding: data, as: UTF8.self).replacingOccurrences(of: #"\.000(?=[+-]\d\d:\d\d")"#, with: "", options: .regularExpression)
}
