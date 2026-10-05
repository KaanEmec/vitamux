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
    // Apple Watch detail (ADR-0024); each is set only on its own type.
    /// The linked workout of a route or effort score, when HealthKit reports the link.
    public var workoutUUID: String?
    public var ecg: ECG?
    public var beats: Beats?
    public var route: Route?
    public var stateOfMind: StateOfMind?
    public var activitySummary: ActivitySummary?

    public init(uuid: String, start: String, end: String, value: Double? = nil, unit: String? = nil,
                sourceRevision: SourceRevision, device: Device? = nil, metadata: [String: MetadataValue]? = nil,
                wasUserEntered: Bool = false, objects: [Sample]? = nil, workout: Workout? = nil,
                workoutUUID: String? = nil, ecg: ECG? = nil, beats: Beats? = nil, route: Route? = nil,
                stateOfMind: StateOfMind? = nil, activitySummary: ActivitySummary? = nil) {
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
        self.workoutUUID = workoutUUID
        self.ecg = ecg
        self.beats = beats
        self.route = route
        self.stateOfMind = stateOfMind
        self.activitySummary = activitySummary
    }

    enum CodingKeys: String, CodingKey {
        case type, uuid, start, end, value, unit, device, metadata, objects, workout, ecg, beats, route
        case sourceRevision = "source_revision", wasUserEntered = "was_user_entered", workoutUUID = "workout_uuid"
        case stateOfMind = "state_of_mind", activitySummary = "activity_summary"
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
    /// Discrete statistics (e.g. heart rate) keyed by quantity type identifier, in the registry unit.
    public var stats: [String: Stats]?
    /// `HKWorkout.workoutEvents`, in order.
    public var events: [WorkoutEvent]?
    /// `HKWorkout.workoutActivities`, in order.
    public var activities: [WorkoutActivity]?

    public init(activityType: UInt, durationS: Double, totals: [String: Double]? = nil, stats: [String: Stats]? = nil,
                events: [WorkoutEvent]? = nil, activities: [WorkoutActivity]? = nil) {
        self.activityType = activityType
        self.durationS = durationS
        self.totals = totals
        self.stats = stats
        self.events = events
        self.activities = activities
    }

    enum CodingKeys: String, CodingKey {
        case totals, stats, events, activities
        case activityType = "activity_type", durationS = "duration_s"
    }
}

/// Average, minimum and maximum of a discrete quantity.
public struct Stats: Codable, Equatable, Sendable {
    public var avg: Double?
    public var min: Double?
    public var max: Double?

    public init(avg: Double? = nil, min: Double? = nil, max: Double? = nil) {
        self.avg = avg
        self.min = min
        self.max = max
    }
}

/// `HKWorkoutEvent`; `type` is the raw `HKWorkoutEventType`.
public struct WorkoutEvent: Codable, Equatable, Sendable {
    public var type: Int
    public var start: String
    public var end: String
    public var metadata: [String: MetadataValue]?

    public init(type: Int, start: String, end: String, metadata: [String: MetadataValue]? = nil) {
        self.type = type
        self.start = start
        self.end = end
        self.metadata = metadata
    }
}

/// `HKWorkoutActivity`: one leg of a multisport workout, or one interval. Enums are raw values.
public struct WorkoutActivity: Codable, Equatable, Sendable {
    public var uuid: String
    public var activityType: UInt
    public var locationType: Int?
    public var swimmingLocationType: Int?
    public var lapLengthM: Double?
    public var start: String
    public var end: String?
    public var durationS: Double
    public var totals: [String: Double]?
    public var stats: [String: Stats]?
    public var events: [WorkoutEvent]?
    public var metadata: [String: MetadataValue]?

    public init(uuid: String, activityType: UInt, locationType: Int? = nil, swimmingLocationType: Int? = nil,
                lapLengthM: Double? = nil, start: String, end: String? = nil, durationS: Double,
                totals: [String: Double]? = nil, stats: [String: Stats]? = nil, events: [WorkoutEvent]? = nil,
                metadata: [String: MetadataValue]? = nil) {
        self.uuid = uuid
        self.activityType = activityType
        self.locationType = locationType
        self.swimmingLocationType = swimmingLocationType
        self.lapLengthM = lapLengthM
        self.start = start
        self.end = end
        self.durationS = durationS
        self.totals = totals
        self.stats = stats
        self.events = events
        self.metadata = metadata
    }

    enum CodingKeys: String, CodingKey {
        case uuid, start, end, totals, stats, events, metadata
        case activityType = "activity_type", locationType = "location_type", swimmingLocationType = "swimming_location_type"
        case lapLengthM = "lap_length_m", durationS = "duration_s"
    }
}

/// `HKElectrocardiogram` with its voltages. Enums are raw values; `voltages` has `voltageCount` entries in `voltageUnit`.
public struct ECG: Codable, Equatable, Sendable {
    public var classification: Int
    public var symptomsStatus: Int
    public var averageHeartRate: Double?
    public var samplingFrequencyHz: Double?
    public var lead: Int?
    public var voltageCount: Int
    public var voltageUnit: String
    public var voltages: [Double]
    /// Time since the sample start per voltage; only when the spacing is not 1/frequency.
    public var offsetsS: [Double]?

    public init(classification: Int, symptomsStatus: Int, averageHeartRate: Double? = nil, samplingFrequencyHz: Double? = nil,
                lead: Int? = nil, voltageCount: Int, voltageUnit: String = "mcV", voltages: [Double], offsetsS: [Double]? = nil) {
        self.classification = classification
        self.symptomsStatus = symptomsStatus
        self.averageHeartRate = averageHeartRate
        self.samplingFrequencyHz = samplingFrequencyHz
        self.lead = lead
        self.voltageCount = voltageCount
        self.voltageUnit = voltageUnit
        self.voltages = voltages
        self.offsetsS = offsetsS
    }

    enum CodingKeys: String, CodingKey {
        case classification, lead, voltages
        case symptomsStatus = "symptoms_status", averageHeartRate = "average_heart_rate"
        case samplingFrequencyHz = "sampling_frequency_hz", voltageCount = "voltage_count", voltageUnit = "voltage_unit"
        case offsetsS = "offsets_s"
    }
}

/// The beats of an `HKHeartbeatSeriesSample`; both arrays have `count` entries.
public struct Beats: Codable, Equatable, Sendable {
    public var count: Int
    /// Time since the series start per beat, ascending.
    public var offsetsS: [Double]
    public var precededByGap: [Bool]

    public init(offsetsS: [Double], precededByGap: [Bool]) {
        precondition(offsetsS.count == precededByGap.count, "one gap flag per beat")
        self.count = offsetsS.count
        self.offsetsS = offsetsS
        self.precededByGap = precededByGap
    }

    enum CodingKeys: String, CodingKey {
        case count, offsetsS = "offsets_s", precededByGap = "preceded_by_gap"
    }
}

/// The locations of an `HKWorkoutRoute` as parallel arrays of `count` entries. CoreLocation values as
/// given: a negative accuracy, speed or course means invalid.
public struct Route: Codable, Equatable, Sendable {
    public var count: Int
    /// Location timestamp minus the sample start, in seconds.
    public var offsetsS: [Double]
    public var latitude: [Double]
    public var longitude: [Double]
    public var altitudeM: [Double]?
    public var ellipsoidalAltitudeM: [Double]?
    public var horizontalAccuracyM: [Double]?
    public var verticalAccuracyM: [Double]?
    public var speedMps: [Double]?
    public var speedAccuracyMps: [Double]?
    public var courseDeg: [Double]?
    public var courseAccuracyDeg: [Double]?

    public init(count: Int, offsetsS: [Double], latitude: [Double], longitude: [Double], altitudeM: [Double]? = nil,
                ellipsoidalAltitudeM: [Double]? = nil, horizontalAccuracyM: [Double]? = nil, verticalAccuracyM: [Double]? = nil,
                speedMps: [Double]? = nil, speedAccuracyMps: [Double]? = nil, courseDeg: [Double]? = nil,
                courseAccuracyDeg: [Double]? = nil) {
        self.count = count
        self.offsetsS = offsetsS
        self.latitude = latitude
        self.longitude = longitude
        self.altitudeM = altitudeM
        self.ellipsoidalAltitudeM = ellipsoidalAltitudeM
        self.horizontalAccuracyM = horizontalAccuracyM
        self.verticalAccuracyM = verticalAccuracyM
        self.speedMps = speedMps
        self.speedAccuracyMps = speedAccuracyMps
        self.courseDeg = courseDeg
        self.courseAccuracyDeg = courseAccuracyDeg
    }

    enum CodingKeys: String, CodingKey {
        case count, latitude, longitude
        case offsetsS = "offsets_s", altitudeM = "altitude_m", ellipsoidalAltitudeM = "ellipsoidal_altitude_m"
        case horizontalAccuracyM = "horizontal_accuracy_m", verticalAccuracyM = "vertical_accuracy_m"
        case speedMps = "speed_mps", speedAccuracyMps = "speed_accuracy_mps", courseDeg = "course_deg"
        case courseAccuracyDeg = "course_accuracy_deg"
    }
}

/// `HKStateOfMind`; enums are raw values.
public struct StateOfMind: Codable, Equatable, Sendable {
    public var kind: Int
    public var valence: Double
    public var valenceClassification: Int?
    public var labels: [Int]?
    public var associations: [Int]?

    public init(kind: Int, valence: Double, valenceClassification: Int? = nil, labels: [Int]? = nil, associations: [Int]? = nil) {
        self.kind = kind
        self.valence = valence
        self.valenceClassification = valenceClassification
        self.labels = labels
        self.associations = associations
    }

    enum CodingKeys: String, CodingKey {
        case kind, valence, labels, associations, valenceClassification = "valence_classification"
    }
}

/// `HKActivitySummary` of one day. `date` is `YYYY-MM-DD` in the phone's calendar.
public struct ActivitySummary: Codable, Equatable, Sendable {
    public var date: String
    public var moveMode: Int?
    public var paused: Bool?
    public var activeEnergyKcal: Double?
    public var activeEnergyGoalKcal: Double?
    public var moveTimeS: Double?
    public var moveTimeGoalS: Double?
    public var exerciseTimeS: Double?
    public var exerciseTimeGoalS: Double?
    public var standHours: Double?
    public var standHoursGoal: Double?

    public init(date: String, moveMode: Int? = nil, paused: Bool? = nil, activeEnergyKcal: Double? = nil,
                activeEnergyGoalKcal: Double? = nil, moveTimeS: Double? = nil, moveTimeGoalS: Double? = nil,
                exerciseTimeS: Double? = nil, exerciseTimeGoalS: Double? = nil, standHours: Double? = nil,
                standHoursGoal: Double? = nil) {
        self.date = date
        self.moveMode = moveMode
        self.paused = paused
        self.activeEnergyKcal = activeEnergyKcal
        self.activeEnergyGoalKcal = activeEnergyGoalKcal
        self.moveTimeS = moveTimeS
        self.moveTimeGoalS = moveTimeGoalS
        self.exerciseTimeS = exerciseTimeS
        self.exerciseTimeGoalS = exerciseTimeGoalS
        self.standHours = standHours
        self.standHoursGoal = standHoursGoal
    }

    enum CodingKeys: String, CodingKey {
        case date, paused
        case moveMode = "move_mode", activeEnergyKcal = "active_energy_kcal", activeEnergyGoalKcal = "active_energy_goal_kcal"
        case moveTimeS = "move_time_s", moveTimeGoalS = "move_time_goal_s", exerciseTimeS = "exercise_time_s"
        case exerciseTimeGoalS = "exercise_time_goal_s", standHours = "stand_hours", standHoursGoal = "stand_hours_goal"
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
