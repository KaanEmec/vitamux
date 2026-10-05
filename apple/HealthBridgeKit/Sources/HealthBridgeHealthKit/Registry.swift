import HealthBridgeCore
import HealthKit

/// The metric groups the user enables; authorization is requested per group.
public enum MetricGroup: String, CaseIterable, Sendable {
    case heart, activity, body, sleep, workouts, vitals, nutrition
    /// Registry v2 groups (ADR-0024). Each is opt-in: off on install and after an upgrade, and nothing
    /// in it is requested or read before the owner turns it on.
    case mind, cycle, symptoms, ecg, beats, routes

    /// Off until the owner turns it on, after a plain explanation of what is read.
    public var isOptIn: Bool {
        switch self {
        case .heart, .activity, .body, .sleep, .workouts, .vitals, .nutrition: false
        case .mind, .cycle, .symptoms, .ecg, .beats, .routes: true
        }
    }

    /// The group that must be on for this one to make sense: routes need workouts. Turning a group on
    /// never turns another one on.
    public var requires: MetricGroup? { self == .routes ? .workouts : nil }
}

/// One synced HealthKit type. `unit` is the HKUnit string quantities are read in; the server
/// converts to canonical units. `pageLimit` is the anchored-query limit (ADR-0024 page sizes).
public struct HealthType: Hashable, Sendable {
    public let id: String
    public let group: MetricGroup
    public let unit: String?
    public let pageLimit: Int

    init(id: String, group: MetricGroup, unit: String?, pageLimit: Int = Batcher.maxSamples) {
        self.id = id
        self.group = group
        self.unit = unit
        self.pageLimit = pageLimit
    }

    /// The type on this OS, or nil when this OS does not know it (newer identifiers) or it is not a
    /// sample type (the activity summary).
    public var sampleType: HKSampleType? {
        switch id {
        case HKWorkoutTypeIdentifier: return HKObjectType.workoutType()
        case HKWorkoutRouteTypeIdentifier: return HKSeriesType.workoutRoute()
        case HKDataTypeIdentifierHeartbeatSeries: return HKSeriesType.heartbeat()
        case Registry.electrocardiogramID: return HKObjectType.electrocardiogramType()
        case Registry.stateOfMindID:
            if #available(iOS 18.0, macOS 15.0, *) { return HKObjectType.stateOfMindType() }
            return nil
        case Registry.activitySummaryID: return nil
        default: break
        }
        if id.hasPrefix("HKQuantity") { return HKObjectType.quantityType(forIdentifier: .init(rawValue: id)) }
        if id.hasPrefix("HKCategory") { return HKObjectType.categoryType(forIdentifier: .init(rawValue: id)) }
        return HKObjectType.correlationType(forIdentifier: .init(rawValue: id))
    }

    /// The daily activity summary: read by date, without anchor, UUID or deletions.
    public var isActivitySummary: Bool { id == Registry.activitySummaryID }

    /// Types to request read access for. A correlation is authorized through its members.
    var readTypes: Set<HKObjectType> {
        if isActivitySummary { return [HKObjectType.activitySummaryType()] }
        guard id == HKCorrelationTypeIdentifier.bloodPressure.rawValue else { return Set([sampleType].compactMap { $0 }) }
        return Set(Registry.bloodPressureMembers.keys.compactMap { HKObjectType.quantityType(forIdentifier: .init(rawValue: $0)) })
    }

    /// The extra read a page of this type needs before upload.
    var detail: Detail? {
        switch id {
        case Registry.electrocardiogramID: .ecg
        case HKDataTypeIdentifierHeartbeatSeries: .beats
        case HKWorkoutRouteTypeIdentifier: .route
        case Registry.effortScoreIDs.0, Registry.effortScoreIDs.1: .workoutLink
        default: nil
        }
    }

    enum Detail { case ecg, beats, route, workoutLink }
}

/// Type registry v1: the `v1`, `E15` and `E25` rows of docs/architecture/metric-catalog.md that have an HK id.
/// Type registry v2 (ADR-0024): v1 plus the Apple Watch types v1 lacks.
public enum Registry {
    public static let v1: [HealthType] = [
        q("StepCount", .activity, "count"), q("DistanceWalkingRunning", .activity, "m"), q("DistanceCycling", .activity, "m"),
        q("DistanceSwimming", .activity, "m"), q("DistanceWheelchair", .activity, "m"), q("FlightsClimbed", .activity, "count"),
        q("ActiveEnergyBurned", .activity, "kcal"), q("BasalEnergyBurned", .activity, "kcal"),
        q("AppleExerciseTime", .activity, "s"), q("AppleStandTime", .activity, "s"), c("AppleStandHour", .activity),
        q("AppleWalkingSteadiness", .activity, "%"), q("WalkingAsymmetryPercentage", .activity, "%"),
        q("WalkingDoubleSupportPercentage", .activity, "%"), q("WalkingStepLength", .activity, "m"),
        c("AppleWalkingSteadinessEvent", .activity),
        q("DistanceRowing", .activity, "m"), q("DistancePaddleSports", .activity, "m"), q("DistanceSkatingSports", .activity, "m"),
        q("DistanceCrossCountrySkiing", .activity, "m"), q("DistanceDownhillSnowSports", .activity, "m"),
        q("AppleMoveTime", .activity, "s"), q("TimeInDaylight", .activity, "s"), q("PushCount", .activity, "count"),
        q("SwimmingStrokeCount", .activity, "count"), q("WalkingSpeed", .activity, "m/s"), q("RunningSpeed", .activity, "m/s"),
        q("CyclingSpeed", .activity, "m/s"), q("RowingSpeed", .activity, "m/s"), q("PaddleSportsSpeed", .activity, "m/s"),
        q("CyclingCadence", .activity, "count/min"), q("RunningPower", .activity, "W"), q("CyclingPower", .activity, "W"),
        q("CyclingFunctionalThresholdPower", .activity, "W"), q("RunningStrideLength", .activity, "m"),
        q("RunningVerticalOscillation", .activity, "m"), q("RunningGroundContactTime", .activity, "s"),
        q("PhysicalEffort", .activity, "kcal/(kg*hr)"), q("StairAscentSpeed", .activity, "m/s"),
        q("StairDescentSpeed", .activity, "m/s"), q("SixMinuteWalkTestDistance", .activity, "m"),
        q("NumberOfTimesFallen", .activity, "count"),

        q("HeartRate", .heart, "count/min"), q("RestingHeartRate", .heart, "count/min"),
        q("WalkingHeartRateAverage", .heart, "count/min"), q("HeartRateVariabilitySDNN", .heart, "ms"),
        q("VO2Max", .heart, "ml/kg*min"), c("HighHeartRateEvent", .heart), c("LowHeartRateEvent", .heart),
        c("LowCardioFitnessEvent", .heart), c("HypertensionEvent", .heart),
        HealthType(id: HKCorrelationTypeIdentifier.bloodPressure.rawValue, group: .heart, unit: nil),
        q("HeartRateRecoveryOneMinute", .heart, "count/min"), q("AtrialFibrillationBurden", .heart, "%"),
        q("PeripheralPerfusionIndex", .heart, "%"),

        q("OxygenSaturation", .vitals, "%"), q("RespiratoryRate", .vitals, "count/min"), q("BodyTemperature", .vitals, "degC"),
        q("BasalBodyTemperature", .vitals, "degC"), q("BloodGlucose", .vitals, "mg/dL"),
        // The raw value of `.environmentalAudioExposureEvent` is the older "HKCategoryTypeIdentifierAudioExposureEvent".
        HealthType(id: HKCategoryTypeIdentifier.environmentalAudioExposureEvent.rawValue, group: .vitals, unit: nil),
        c("HeadphoneAudioExposureEvent", .vitals),
        q("ForcedExpiratoryVolume1", .vitals, "L"), q("ForcedVitalCapacity", .vitals, "L"),
        q("PeakExpiratoryFlowRate", .vitals, "L/min"), q("InhalerUsage", .vitals, "count"), q("InsulinDelivery", .vitals, "IU"),
        q("BloodAlcoholContent", .vitals, "%"), q("EnvironmentalAudioExposure", .vitals, "dBASPL"),
        q("HeadphoneAudioExposure", .vitals, "dBASPL"), q("EnvironmentalSoundReduction", .vitals, "dBASPL"),
        q("UVExposure", .vitals, "count"), q("WaterTemperature", .vitals, "degC"), q("UnderwaterDepth", .vitals, "m"),
        q("ElectrodermalActivity", .vitals, "mcS"),

        q("BodyMass", .body, "kg"), q("Height", .body, "m"), q("BodyFatPercentage", .body, "%"), q("BodyMassIndex", .body, "count"),
        q("LeanBodyMass", .body, "kg"), q("WaistCircumference", .body, "m"),

        c("SleepAnalysis", .sleep), q("AppleSleepingBreathingDisturbances", .sleep, "count"),
        q("AppleSleepingWristTemperature", .sleep, "degC"), c("SleepApneaEvent", .sleep),

        HealthType(id: HKWorkoutTypeIdentifier, group: .workouts, unit: nil),

        q("DietaryEnergyConsumed", .nutrition, "kcal"), q("DietaryProtein", .nutrition, "g"), q("DietaryCarbohydrates", .nutrition, "g"),
        q("DietaryFatTotal", .nutrition, "g"), q("DietaryFatSaturated", .nutrition, "g"), q("DietaryFatMonounsaturated", .nutrition, "g"),
        q("DietaryFatPolyunsaturated", .nutrition, "g"), q("DietaryFiber", .nutrition, "g"), q("DietarySugar", .nutrition, "g"),
        q("DietaryCholesterol", .nutrition, "mg"), q("DietaryWater", .nutrition, "mL"), q("DietaryCaffeine", .nutrition, "mg"),
        q("NumberOfAlcoholicBeverages", .nutrition, "count"),
    ]

    /// Correlation members are read in these units.
    static let bloodPressureMembers = [
        HKQuantityTypeIdentifier.bloodPressureSystolic.rawValue: "mmHg",
        HKQuantityTypeIdentifier.bloodPressureDiastolic.rawValue: "mmHg",
    ]

    /// Identifiers HealthKit reports at run time; the SDK has no constant for the first two, and the
    /// State of Mind constant's value is `HKDataTypeStateOfMind` (no "Identifier").
    public static let electrocardiogramID = HKObjectType.electrocardiogramType().identifier
    public static let activitySummaryID = HKObjectType.activitySummaryType().identifier
    public static let stateOfMindID = "HKDataTypeStateOfMind" // HKDataTypeIdentifierStateOfMind, iOS 18
    static let effortScoreIDs = ("HKQuantityTypeIdentifierWorkoutEffortScore", "HKQuantityTypeIdentifierEstimatedWorkoutEffortScore")

    /// Anchored-query limits of the types with a per-sample detail read (ADR-0024).
    public static let ecgPageLimit = 5, beatsPageLimit = 100, routePageLimit = 1

    /// Type registry v2: every v1 type keeps its id, group and unit (so its anchor), plus the rows below.
    public static let v2: [HealthType] = v1 + [
        c("IrregularHeartRhythmEvent", .heart), q("HeartRateVariabilityRMSSD", .heart, "ms"),

        q("CrossCountrySkiingSpeed", .activity, "m/s"),
        HealthType(id: activitySummaryID, group: .activity, unit: nil),

        HealthType(id: effortScoreIDs.0, group: .workouts, unit: "appleEffortScore"),
        HealthType(id: effortScoreIDs.1, group: .workouts, unit: "appleEffortScore"),

        c("HandwashingEvent", .vitals),

        c("MindfulSession", .mind), HealthType(id: stateOfMindID, group: .mind, unit: nil),
    ] + cycle.map { c($0, .cycle) } + symptoms.map { c($0, .symptoms) } + [
        HealthType(id: electrocardiogramID, group: .ecg, unit: nil, pageLimit: ecgPageLimit),
        HealthType(id: HKDataTypeIdentifierHeartbeatSeries, group: .beats, unit: nil, pageLimit: beatsPageLimit),
        HealthType(id: HKWorkoutRouteTypeIdentifier, group: .routes, unit: nil, pageLimit: routePageLimit),
    ]

    /// The 18 cycle-tracking categories.
    static let cycle = [
        "MenstrualFlow", "BleedingAfterPregnancy", "BleedingDuringPregnancy", "BleedingAfterMenopause",
        "IntermenstrualBleeding", "SexualActivity", "Pregnancy", "Lactation", "CervicalMucusQuality", "OvulationTestResult",
        "PregnancyTestResult", "ProgesteroneTestResult", "Contraceptive", "MenopausalState", "IrregularMenstrualCycles",
        "InfrequentMenstrualCycles", "ProlongedMenstrualPeriods", "PersistentIntermenstrualBleeding",
    ]

    /// The 39 symptom categories.
    static let symptoms = [
        "AbdominalCramps", "Acne", "AppetiteChanges", "BladderIncontinence", "Bloating", "BreastPain", "ChestTightnessOrPain",
        "Chills", "Constipation", "Coughing", "Diarrhea", "Dizziness", "DrySkin", "Fainting", "Fatigue", "Fever",
        "GeneralizedBodyAche", "HairLoss", "Headache", "Heartburn", "HotFlashes", "LossOfSmell", "LossOfTaste", "LowerBackPain",
        "MemoryLapse", "MoodChanges", "Nausea", "NightSweats", "PelvicPain", "RapidPoundingOrFlutteringHeartbeat", "RunnyNose",
        "ShortnessOfBreath", "SinusCongestion", "SkippedHeartbeat", "SleepChanges", "SoreThroat", "VaginalDryness", "Vomiting",
        "Wheezing",
    ]

    /// The unit a quantity type is read in, for correlation members, workout totals and statistics.
    static func unit(for id: String) -> String? {
        bloodPressureMembers[id] ?? v2.first { $0.id == id }?.unit
    }

    /// The registry v2 types of `groups`, in registry order.
    public static func types(in groups: Set<MetricGroup>) -> [HealthType] { v2.filter { groups.contains($0.group) } }

    static func q(_ name: String, _ group: MetricGroup, _ unit: String) -> HealthType {
        HealthType(id: "HKQuantityTypeIdentifier" + name, group: group, unit: unit)
    }

    static func c(_ name: String, _ group: MetricGroup) -> HealthType {
        HealthType(id: "HKCategoryTypeIdentifier" + name, group: group, unit: nil)
    }
}
