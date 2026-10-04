import Foundation

/// Body of one `healthkit.samples.v1` item: one anchored-query page of one type.
/// Contract: schemas/healthkit-samples.v1.json.
public struct SamplesPage: Codable, Equatable, Sendable {
    public var type: String
    public var anchor: AnchorInfo
    public var samples: [Sample]
    public var deleted: [Deleted]

    public init(type: String, anchor: AnchorInfo, samples: [Sample], deleted: [Deleted]) {
        self.type = type
        self.anchor = anchor
        self.samples = samples
        self.deleted = deleted
    }
}

public struct AnchorInfo: Codable, Equatable, Sendable {
    public var beforeHash: String
    public var afterHash: String
    public var queryStartedAt: String

    public init(beforeHash: String, afterHash: String, queryStartedAt: String) {
        self.beforeHash = beforeHash
        self.afterHash = afterHash
        self.queryStartedAt = queryStartedAt
    }

    enum CodingKeys: String, CodingKey {
        case beforeHash = "before_hash", afterHash = "after_hash", queryStartedAt = "query_started_at"
    }
}

public struct Sample: Codable, Equatable, Sendable {
    /// HealthKit type identifier; set only on correlation members, whose type the page does not give.
    public var type: String?
    public var uuid: String
    public var start: String
    public var end: String
    /// Quantity value in `unit`, or the raw category value (then `unit` is nil).
    public var value: Double?
    public var unit: String?
    public var sourceRevision: SourceRevision
    public var device: Device?
    public var metadata: [String: MetadataValue]?
    public var wasUserEntered: Bool
    /// Members of a correlation (blood pressure).
    public var objects: [Sample]?
    public var workout: Workout?

    public init(uuid: String, start: String, end: String, value: Double? = nil, unit: String? = nil,
                sourceRevision: SourceRevision, device: Device? = nil, metadata: [String: MetadataValue]? = nil,
                wasUserEntered: Bool = false, objects: [Sample]? = nil, workout: Workout? = nil) {
        self.uuid = uuid
        self.start = start
        self.end = end
        self.value = value
        self.unit = unit
        self.sourceRevision = sourceRevision
        self.device = device
        self.metadata = metadata
        self.wasUserEntered = wasUserEntered
        self.objects = objects
        self.workout = workout
    }

    enum CodingKeys: String, CodingKey {
        case type, uuid, start, end, value, unit, device, metadata, objects, workout
        case sourceRevision = "source_revision", wasUserEntered = "was_user_entered"
    }
}

public struct SourceRevision: Codable, Equatable, Sendable {
    public var bundleId: String
    public var name: String
    public var version: String?
    public var productType: String?
    public var osVersion: String?

    public init(bundleId: String, name: String, version: String? = nil, productType: String? = nil, osVersion: String? = nil) {
        self.bundleId = bundleId
        self.name = name
        self.version = version
        self.productType = productType
        self.osVersion = osVersion
    }

    enum CodingKeys: String, CodingKey {
        case name, version
        case bundleId = "bundle_id", productType = "product_type", osVersion = "os_version"
    }
}

public struct Device: Codable, Equatable, Sendable {
    public var name: String?
    public var manufacturer: String?
    public var model: String?
    public var hardwareVersion: String?
    public var softwareVersion: String?

    public init(name: String?, manufacturer: String?, model: String?, hardwareVersion: String?, softwareVersion: String?) {
        self.name = name
        self.manufacturer = manufacturer
        self.model = model
        self.hardwareVersion = hardwareVersion
        self.softwareVersion = softwareVersion
    }

    enum CodingKeys: String, CodingKey {
        case name, manufacturer, model
        case hardwareVersion = "hardware_version", softwareVersion = "software_version"
    }
}

public struct Workout: Codable, Equatable, Sendable {
    /// `HKWorkoutActivityType` raw value.
    public var activityType: UInt
    public var durationS: Double
    /// Sums keyed by quantity type identifier, in the registry unit of that type.
    public var totals: [String: Double]?

    public init(activityType: UInt, durationS: Double, totals: [String: Double]? = nil) {
        self.activityType = activityType
        self.durationS = durationS
        self.totals = totals
    }

    enum CodingKeys: String, CodingKey {
        case totals
        case activityType = "activity_type", durationS = "duration_s"
    }
}

public struct Deleted: Codable, Equatable, Sendable {
    public var uuid: String
    public init(uuid: String) { self.uuid = uuid }
}

/// A HealthKit metadata value. HealthKit metadata is flat: strings, numbers, dates and quantities.
public enum MetadataValue: Codable, Equatable, Sendable {
    case string(String)
    case number(Double)

    public init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if let n = try? c.decode(Double.self) { self = .number(n) } else { self = .string(try c.decode(String.self)) }
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .string(let s): try c.encode(s)
        case .number(let n): try c.encode(n)
        }
    }
}

/// Body of `POST /api/ingest/v1/batches` (schemas/ingest-batch.v1.json).
struct IngestBatch: Encodable {
    let schema = "vitamux.ingest.batch/1"
    let connectionId: String
    let client: Client
    let items: [Item]

    struct Client: Encodable {
        let kind = "device"
        let name = "healthbridge-ios"
        let version: String
    }

    struct Item: Encodable {
        let stream = "healthkit.samples.v1"
        let externalKey: String
        let fetchedAt: String
        let contentType = "application/json"
        let body: SamplesPage

        enum CodingKeys: String, CodingKey {
            case stream, body
            case externalKey = "external_key", fetchedAt = "fetched_at", contentType = "content_type"
        }
    }

    enum CodingKeys: String, CodingKey {
        case schema, client, items
        case connectionId = "connection_id"
    }
}

/// RFC 3339 timestamp with fractional seconds and a numeric offset in `timeZone`.
public func rfc3339(_ date: Date, in timeZone: TimeZone = .current) -> String {
    date.formatted(Date.ISO8601FormatStyle(timeZoneSeparator: .colon, includingFractionalSeconds: true, timeZone: timeZone))
}
