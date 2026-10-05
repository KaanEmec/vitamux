import Foundation
import OpenAPIRuntime

/// A JSON value whose object members keep their order, so a rule spec serialises exactly as the
/// panel's `JSON.stringify` does (`text`). Rule specs travel as `OpenAPIObjectContainer`, which
/// has no order; `init(_:)` sorts their keys and `container` converts back, with whole numbers
/// as integers so the stored `jsonb` matches the panel's.
public indirect enum JSONValue: Hashable, Sendable {
    case null
    case bool(Bool)
    case number(Double)
    case string(String)
    case array([JSONValue])
    case object([Member])

    public struct Member: Hashable, Sendable {
        public var key: String
        public var value: JSONValue

        public init(_ key: String, _ value: JSONValue) {
            self.key = key
            self.value = value
        }
    }

    /// An object from members in order; nil values are left out, as `JSON.stringify` drops `undefined`.
    public static func ordered(_ members: KeyValuePairs<String, JSONValue?>) -> JSONValue {
        .object(members.compactMap { key, value in value.map { Member(key, $0) } })
    }

    public subscript(key: String) -> JSONValue? {
        if case .object(let members) = self { members.first { $0.key == key }?.value } else { nil }
    }

    public var string: String? {
        if case .string(let s) = self { s } else { nil }
    }

    public var number: Double? {
        if case .number(let n) = self { n } else { nil }
    }

    public var bool: Bool? {
        if case .bool(let b) = self { b } else { nil }
    }

    public var array: [JSONValue]? {
        if case .array(let a) = self { a } else { nil }
    }

    public var members: [Member]? {
        if case .object(let m) = self { m } else { nil }
    }

    /// The same value with every object's keys sorted, to compare values whatever their order.
    public var sorted: JSONValue {
        switch self {
        case .array(let items): .array(items.map(\.sorted))
        case .object(let members): .object(members.map { Member($0.key, $0.value.sorted) }.sorted { $0.key < $1.key })
        default: self
        }
    }

    // MARK: Text, as JSON.stringify writes it

    /// Compact JSON text, byte for byte what the panel's `JSON.stringify` writes for the same value.
    public var text: String {
        var out = ""
        write(to: &out)
        return out
    }

    private func write(to out: inout String) {
        switch self {
        case .null: out += "null"
        case .bool(let b): out += b ? "true" : "false"
        case .number(let n): out += Self.numberText(n)
        case .string(let s): Self.quote(s, into: &out)
        case .array(let items):
            out += "["
            for (i, item) in items.enumerated() {
                if i > 0 { out += "," }
                item.write(to: &out)
            }
            out += "]"
        case .object(let members):
            out += "{"
            for (i, member) in members.enumerated() {
                if i > 0 { out += "," }
                Self.quote(member.key, into: &out)
                out += ":"
                member.value.write(to: &out)
            }
            out += "}"
        }
    }

    /// A number as JavaScript prints it: whole numbers without a fraction, others in their
    /// shortest form, exponents without padding (`1e-7`).
    public static func numberText(_ n: Double) -> String {
        guard n.isFinite else { return "null" }
        if n == n.rounded(), abs(n) < 1e21 {
            if abs(n) < 9e15 { return String(Int64(n)) }
            return String(format: "%.0f", n)
        }
        var text = "\(n)"
        if text.hasSuffix(".0") { text.removeLast(2) }
        return text.replacingOccurrences(of: "e-0", with: "e-").replacingOccurrences(of: "e+0", with: "e+")
    }

    private static func quote(_ s: String, into out: inout String) {
        out += "\""
        for scalar in s.unicodeScalars {
            switch scalar {
            case "\"": out += "\\\""
            case "\\": out += "\\\\"
            case "\u{08}": out += "\\b"
            case "\u{0C}": out += "\\f"
            case "\n": out += "\\n"
            case "\r": out += "\\r"
            case "\t": out += "\\t"
            case _ where scalar.value < 0x20: out += String(format: "\\u%04x", scalar.value)
            default: out.unicodeScalars.append(scalar)
            }
        }
        out += "\""
    }

    // MARK: Parsing, keeping member order

    public struct ParseError: Error, Equatable {
        public var offset: Int
    }

    /// Parses JSON text and keeps each object's member order.
    public init(parsing data: Data) throws {
        var parser = Parser(bytes: Array(data))
        self = try parser.value()
        parser.skipSpace()
        guard parser.index == parser.bytes.count else { throw ParseError(offset: parser.index) }
    }

    private struct Parser {
        let bytes: [UInt8]
        var index = 0

        mutating func skipSpace() {
            while index < bytes.count, [0x20, 0x0A, 0x0D, 0x09].contains(bytes[index]) { index += 1 }
        }

        mutating func value() throws -> JSONValue {
            skipSpace()
            guard index < bytes.count else { throw ParseError(offset: index) }
            switch bytes[index] {
            case UInt8(ascii: "{"):
                index += 1
                var members: [Member] = []
                skipSpace()
                if consume("}") { return .object(members) }
                repeat {
                    skipSpace()
                    let key = try string()
                    skipSpace()
                    try expect(":")
                    members.append(Member(key, try value()))
                    skipSpace()
                } while consume(",")
                try expect("}")
                return .object(members)
            case UInt8(ascii: "["):
                index += 1
                var items: [JSONValue] = []
                skipSpace()
                if consume("]") { return .array(items) }
                repeat { items.append(try value()); skipSpace() } while consume(",")
                try expect("]")
                return .array(items)
            case UInt8(ascii: "\""):
                return .string(try string())
            case UInt8(ascii: "t"): try literal("true"); return .bool(true)
            case UInt8(ascii: "f"): try literal("false"); return .bool(false)
            case UInt8(ascii: "n"): try literal("null"); return .null
            default:
                let start = index
                while index < bytes.count, "+-0123456789.eE".utf8.contains(bytes[index]) { index += 1 }
                guard let n = Double(String(decoding: bytes[start..<index], as: UTF8.self)) else { throw ParseError(offset: start) }
                return .number(n)
            }
        }

        mutating func string() throws -> String {
            try expect("\"")
            var scalars = String.UnicodeScalarView()
            var raw: [UInt8] = []
            func flush() {
                scalars.append(contentsOf: String(decoding: raw, as: UTF8.self).unicodeScalars)
                raw.removeAll()
            }
            while index < bytes.count {
                let byte = bytes[index]
                index += 1
                switch byte {
                case UInt8(ascii: "\""):
                    flush()
                    return String(scalars)
                case UInt8(ascii: "\\"):
                    flush()
                    guard index < bytes.count else { throw ParseError(offset: index) }
                    let escape = bytes[index]
                    index += 1
                    switch escape {
                    case UInt8(ascii: "b"): scalars.append("\u{08}")
                    case UInt8(ascii: "f"): scalars.append("\u{0C}")
                    case UInt8(ascii: "n"): scalars.append("\n")
                    case UInt8(ascii: "r"): scalars.append("\r")
                    case UInt8(ascii: "t"): scalars.append("\t")
                    case UInt8(ascii: "u"):
                        var code = try hex4()
                        if (0xD800..<0xDC00).contains(code), consume("\\"), consume("u") {
                            let low = try hex4()
                            code = 0x10000 + ((code - 0xD800) << 10) + (low - 0xDC00)
                        }
                        scalars.append(Unicode.Scalar(code) ?? "\u{FFFD}")
                    default: scalars.append(Unicode.Scalar(escape))
                    }
                default:
                    raw.append(byte)
                }
            }
            throw ParseError(offset: index)
        }

        mutating func hex4() throws -> UInt32 {
            guard index + 4 <= bytes.count, let code = UInt32(String(decoding: bytes[index..<index + 4], as: UTF8.self), radix: 16) else {
                throw ParseError(offset: index)
            }
            index += 4
            return code
        }

        mutating func literal(_ word: String) throws {
            for byte in word.utf8 {
                guard index < bytes.count, bytes[index] == byte else { throw ParseError(offset: index) }
                index += 1
            }
        }

        func peek(_ c: Unicode.Scalar) -> Bool {
            index < bytes.count && bytes[index] == UInt8(ascii: c)
        }

        mutating func consume(_ c: Unicode.Scalar) -> Bool {
            guard peek(c) else { return false }
            index += 1
            return true
        }

        mutating func expect(_ c: Unicode.Scalar) throws {
            guard consume(c) else { throw ParseError(offset: index) }
        }
    }
}

// MARK: OpenAPI containers

extension JSONValue {
    /// A value from a generated container's untyped payload; object keys are sorted.
    public init(any value: (any Sendable)?) {
        switch value {
        case nil: self = .null
        case let b as Bool: self = .bool(b)
        case let i as Int: self = .number(Double(i))
        case let i as Int64: self = .number(Double(i))
        case let d as Double: self = .number(d)
        case let s as String: self = .string(s)
        case let a as [(any Sendable)?]: self = .array(a.map(JSONValue.init(any:)))
        case let o as [String: (any Sendable)?]:
            self = .object(o.keys.sorted().map { Member($0, JSONValue(any: o[$0] ?? nil)) })
        default: self = .null
        }
    }

    public init(_ container: OpenAPIObjectContainer) {
        self.init(any: container.value)
    }

    public init(_ container: OpenAPIValueContainer?) {
        self.init(any: container?.value)
    }

    /// The payload a generated container takes: whole numbers as `Int`, so `25` stays `25`.
    public var payload: (any Sendable)? {
        switch self {
        case .null: nil
        case .bool(let b): b
        case .number(let n): n == n.rounded() && abs(n) < 9e15 ? Int(n) : n
        case .string(let s): s
        case .array(let items): items.map(\.payload)
        case .object(let members): Dictionary(members.map { ($0.key, $0.value.payload) }) { _, last in last }
        }
    }

    /// The object as the generated client's `spec` body.
    public var container: OpenAPIObjectContainer {
        get throws {
            try OpenAPIObjectContainer(unvalidatedValue: (payload as? [String: (any Sendable)?]) ?? [:])
        }
    }
}
