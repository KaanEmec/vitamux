import HealthKit

/// The metric groups the user enables; authorization is requested per group.
public enum MetricGroup: String, CaseIterable, Sendable {
    case heart, activity, body, sleep, workouts, vitals, nutrition
}

/// One synced HealthKit type. `unit` is the HKUnit string quantities are read in; the server
/// converts to canonical units.
public struct HealthType: Hashable, Sendable {
    public let id: String
    public let group: MetricGroup
    public let unit: String?

    /// The type on this OS, or nil when this OS does not know it (newer identifiers).
    public var sampleType: HKSampleType? {
        if id == HKWorkoutTypeIdentifier { return HKObjectType.workoutType() }
        if id.hasPrefix("HKQuantity") { return HKObjectType.quantityType(forIdentifier: .init(rawValue: id)) }
        if id.hasPrefix("HKCategory") { return HKObjectType.categoryType(forIdentifier: .init(rawValue: id)) }
        return HKObjectType.correlationType(forIdentifier: .init(rawValue: id))
    }

    /// Types to request read access for. A correlation is authorized through its members.
    var readTypes: Set<HKObjectType> {
        guard id == HKCorrelationTypeIdentifier.bloodPressure.rawValue else { return Set([sampleType].compactMap { $0 }) }
        return Set(Registry.bloodPressureMembers.keys.compactMap { HKObjectType.quantityType(forIdentifier: .init(rawValue: $0)) })
    }
}

/// Type registry v1: the `v1`, `E15` and `E25` rows of docs/architecture/metric-catalog.md that have an HK id.
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

    /// The unit a quantity type is read in, for correlation members and workout totals.
    static func unit(for id: String) -> String? {
        bloodPressureMembers[id] ?? v1.first { $0.id == id }?.unit
    }

    public static func types(in groups: Set<MetricGroup>) -> [HealthType] { v1.filter { groups.contains($0.group) } }

    static func q(_ name: String, _ group: MetricGroup, _ unit: String) -> HealthType {
        HealthType(id: "HKQuantityTypeIdentifier" + name, group: group, unit: unit)
    }

    static func c(_ name: String, _ group: MetricGroup) -> HealthType {
        HealthType(id: "HKCategoryTypeIdentifier" + name, group: group, unit: nil)
    }
}
