package catalog

// Section names, in docs/metrics.md order.
const (
	secActivity    = "Activity"
	secHeart       = "Heart and circulation"
	secBP          = "Blood pressure"
	secRespiration = "Respiration and oxygen"
	secTemperature = "Temperature"
	secBody        = "Body composition"
	secUrine       = "Urine"
	secGlucose     = "Glucose and metabolism"
	secNutrition   = "Nutrition and intake"
	secMobility    = "Mobility"
	secEnvironment = "Environment and hearing"
	secSleep       = "Sleep"
	secDerived     = "Derived"
)

// sections is the docs/metrics.md order; seed order is append-only, so the doc groups by section.
var sections = []string{secActivity, secHeart, secBP, secRespiration, secTemperature, secBody, secUrine, secGlucose,
	secNutrition, secMobility, secEnvironment, secSleep, secDerived}

var (
	sample        = []Kind{Sample}
	interval      = []Kind{Interval}
	sampleDaily   = []Kind{Sample, DailyValue}
	intervalDaily = []Kind{Interval, DailyValue}
	daily         = []Kind{DailyValue}
)

const (
	groupBP   = "bp_reading"
	groupBody = "body_composition"
)

// metrics is every implemented code of docs/architecture/metric-catalog.md. The slice order is
// the seed order, so changing it changes ids in a fresh database: append, never reorder, and
// give appended codes the marker of their own seed migration (Since).
var metrics = []Metric{
	{Code: "steps", Section: secActivity, Unit: "count", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 200000, HK: "StepCount"},
	{Code: "distance_walk_run", Section: secActivity, Unit: "m", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 300000, HK: "DistanceWalkingRunning"},
	{Code: "active_energy", Section: secActivity, Unit: "kcal", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 20000, HK: "ActiveEnergyBurned"},

	{Code: "heart_rate", Section: secHeart, Unit: "bpm", Kinds: sample, Agg: Intensive, Min: 20, Max: 250, HK: "HeartRate", Withings: "11 (outside BP)"},
	{Code: "resting_heart_rate", Section: secHeart, Unit: "bpm", Kinds: sampleDaily, Agg: DailySummary, Min: 20, Max: 150, SelectionOnly: true, HK: "RestingHeartRate"},
	{Code: "hrv_sdnn", Section: secHeart, Unit: "ms", Kinds: sample, Agg: Intensive, Min: 1, Max: 500, HK: "HeartRateVariabilitySDNN"},
	{Code: "hrv_rmssd", Section: secHeart, Unit: "ms", Kinds: sample, Agg: Intensive, Min: 1, Max: 500},
	{Code: "hrv_rmssd_nightly", Section: secHeart, Unit: "ms", Kinds: daily, Agg: DailySummary, Min: 1, Max: 500, SelectionOnly: true},
	{Code: "vo2max", Section: secHeart, Unit: "mL/kg/min", Kinds: sample, Agg: Latest, Min: 10, Max: 100, HK: "VO2Max", Withings: "123"},
	{Code: "pulse_wave_velocity", Section: secHeart, Unit: "m/s", Kinds: sample, Agg: Latest, Min: 2, Max: 30, Withings: "91"},
	{Code: "vascular_age", Section: secHeart, Unit: "years", Kinds: sample, Agg: Latest, Min: 10, Max: 120, Withings: "155"},

	{Code: "bp_systolic", Section: secBP, Unit: "mmHg", Kinds: sample, Agg: Latest, Min: 40, Max: 300, Group: groupBP, HK: "BloodPressureSystolic", Withings: "10"},
	{Code: "bp_diastolic", Section: secBP, Unit: "mmHg", Kinds: sample, Agg: Latest, Min: 20, Max: 200, Group: groupBP, HK: "BloodPressureDiastolic", Withings: "9"},
	{Code: "bp_pulse", Section: secBP, Unit: "bpm", Kinds: sample, Agg: Latest, Min: 20, Max: 250, Group: groupBP, HK: "HeartRate (in the correlation)", Withings: "11"},

	{Code: "spo2", Section: secRespiration, Unit: "%", Kinds: sample, Agg: Intensive, Min: 50, Max: 100, HK: "OxygenSaturation", Withings: "54"},
	{Code: "respiratory_rate", Section: secRespiration, Unit: "breaths/min", Kinds: sample, Agg: Intensive, Min: 4, Max: 60, HK: "RespiratoryRate"},

	{Code: "body_temperature", Section: secTemperature, Unit: "°C", Kinds: sample, Agg: Latest, Min: 30, Max: 45, HK: "BodyTemperature", Withings: "71, 12"},
	{Code: "skin_temperature", Section: secTemperature, Unit: "°C", Kinds: sample, Agg: Intensive, Min: 20, Max: 45, Withings: "73"},

	{Code: "weight", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 2, Max: 500, Group: groupBody, HK: "BodyMass", Withings: "1"},
	{Code: "height", Section: secBody, Unit: "m", Kinds: sample, Agg: Latest, Min: 0.3, Max: 2.8, HK: "Height", Withings: "4"},
	{Code: "body_fat_ratio", Section: secBody, Unit: "%", Kinds: sample, Agg: Latest, Min: 1, Max: 80, Group: groupBody, HK: "BodyFatPercentage", Withings: "6"},
	{Code: "fat_mass", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.1, Max: 300, Group: groupBody, Withings: "8"},
	{Code: "fat_free_mass", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 1, Max: 300, Group: groupBody, Withings: "5"},
	{Code: "muscle_mass", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 1, Max: 200, Group: groupBody, Withings: "76"},
	{Code: "bone_mass", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.1, Max: 10, Group: groupBody, Withings: "88"},
	{Code: "hydration", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 1, Max: 300, Group: groupBody, Withings: "77"},
	{Code: "extracellular_water", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 1, Max: 150, Group: groupBody, Withings: "168"},
	{Code: "intracellular_water", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 1, Max: 150, Group: groupBody, Withings: "169"},
	{Code: "visceral_fat_index", Section: secBody, Unit: "index", Kinds: sample, Agg: Latest, Min: 0, Max: 60, Group: groupBody, Withings: "170"},
	{Code: "basal_metabolic_rate", Section: secBody, Unit: "kcal/day", Kinds: sample, Agg: Latest, Min: 300, Max: 10000, Group: groupBody, Withings: "226"},

	// Derived from sleep sessions and stages (tables sleep_sessions, sleep_stages), never stored as measurements.
	{Code: "sleep_total", Section: secSleep, Unit: "s", Agg: SleepDerived, Min: 0, Max: 86400},
	{Code: "sleep_in_bed", Section: secSleep, Unit: "s", Agg: SleepDerived, Min: 0, Max: 86400},
	{Code: "sleep_awake", Section: secSleep, Unit: "s", Agg: SleepDerived, Min: 0, Max: 86400},
	{Code: "sleep_light", Section: secSleep, Unit: "s", Agg: SleepDerived, Min: 0, Max: 86400},
	{Code: "sleep_deep", Section: secSleep, Unit: "s", Agg: SleepDerived, Min: 0, Max: 86400},
	{Code: "sleep_rem", Section: secSleep, Unit: "s", Agg: SleepDerived, Min: 0, Max: 86400},
	{Code: "sleep_unspecified", Section: secSleep, Unit: "s", Agg: SleepDerived, Min: 0, Max: 86400},
	{Code: "sleep_latency", Section: secSleep, Unit: "s", Agg: SleepDerived, Min: 0, Max: 86400},
	{Code: "sleep_waso", Section: secSleep, Unit: "s", Agg: SleepDerived, Min: 0, Max: 86400},
	{Code: "sleep_efficiency", Section: secSleep, Unit: "%", Agg: SleepDerived, Min: 0, Max: 100},

	// Derived codes (E2, J09.10): computed from the source metric's series, never stored as measurements.
	{Code: "resting_heart_rate_nocturnal", Section: secDerived, Unit: "bpm", Agg: Intensive, Min: 20, Max: 150, DerivedFrom: "heart_rate", Since: SeedDerived},
	{Code: "spo2_night_min", Section: secDerived, Unit: "%", Agg: Intensive, Min: 50, Max: 100, DerivedFrom: "spo2", Since: SeedDerived},

	// Apple Health bridge (E15, J15.2): the codes the HealthKit type registry v1 sends.
	{Code: "distance_cycling", Section: secActivity, Unit: "m", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 1000000, HK: "DistanceCycling", Since: SeedHealthKit},
	{Code: "distance_swimming", Section: secActivity, Unit: "m", Kinds: interval, Agg: Additive, Min: 0, Max: 100000, HK: "DistanceSwimming", Since: SeedHealthKit},
	{Code: "distance_wheelchair", Section: secActivity, Unit: "m", Kinds: interval, Agg: Additive, Min: 0, Max: 300000, HK: "DistanceWheelchair", Since: SeedHealthKit},
	{Code: "floors_climbed", Section: secActivity, Unit: "count", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 3000, HK: "FlightsClimbed", Since: SeedHealthKit},
	{Code: "basal_energy", Section: secActivity, Unit: "kcal", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 10000, HK: "BasalEnergyBurned", Since: SeedHealthKit},
	{Code: "exercise_time", Section: secActivity, Unit: "s", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 86400, HK: "AppleExerciseTime", Since: SeedHealthKit},
	{Code: "stand_time", Section: secActivity, Unit: "s", Kinds: interval, Agg: Additive, Min: 0, Max: 86400, HK: "AppleStandTime", Since: SeedHealthKit},
	// One interval per hour: 1 when the hour counted as stood, 0 when idle.
	{Code: "stand_hours", Section: secActivity, Unit: "count", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 24, HK: "AppleStandHour (category)", Since: SeedHealthKit},
	{Code: "walking_heart_rate", Section: secHeart, Unit: "bpm", Kinds: daily, Agg: DailySummary, Min: 20, Max: 250, HK: "WalkingHeartRateAverage", Since: SeedHealthKit},
	{Code: "breathing_disturbances", Section: secRespiration, Unit: "events/h", Kinds: daily, Agg: DailySummary, Min: 0, Max: 150, HK: "AppleSleepingBreathingDisturbances", Since: SeedHealthKit},
	{Code: "wrist_temperature_sleeping", Section: secTemperature, Unit: "°C", Kinds: daily, Agg: DailySummary, Min: 25, Max: 45, HK: "AppleSleepingWristTemperature", Since: SeedHealthKit},
	{Code: "basal_body_temperature", Section: secTemperature, Unit: "°C", Kinds: sample, Agg: Latest, Min: 30, Max: 45, HK: "BasalBodyTemperature", Since: SeedHealthKit},
	{Code: "bmi", Section: secBody, Unit: "kg/m²", Kinds: sample, Agg: Latest, Min: 8, Max: 100, Group: groupBody, HK: "BodyMassIndex", Since: SeedHealthKit},
	{Code: "lean_body_mass", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 1, Max: 300, Group: groupBody, HK: "LeanBodyMass", Since: SeedHealthKit},
	{Code: "waist_circumference", Section: secBody, Unit: "m", Kinds: sample, Agg: Latest, Min: 0.3, Max: 3, HK: "WaistCircumference", Since: SeedHealthKit},
	{Code: "blood_glucose", Section: secGlucose, Unit: "mmol/L", Kinds: sample, Agg: Intensive, Min: 0.5, Max: 50, HK: "BloodGlucose", Since: SeedHealthKit},
	{Code: "diet_energy", Section: secNutrition, Unit: "kcal", Kinds: interval, Agg: Additive, Min: 0, Max: 20000, HK: "DietaryEnergyConsumed", Since: SeedHealthKit},
	{Code: "diet_protein", Section: secNutrition, Unit: "g", Kinds: interval, Agg: Additive, Min: 0, Max: 2000, HK: "DietaryProtein", Since: SeedHealthKit},
	{Code: "diet_carbohydrate", Section: secNutrition, Unit: "g", Kinds: interval, Agg: Additive, Min: 0, Max: 3000, HK: "DietaryCarbohydrates", Since: SeedHealthKit},
	{Code: "diet_fat_total", Section: secNutrition, Unit: "g", Kinds: interval, Agg: Additive, Min: 0, Max: 2000, HK: "DietaryFatTotal", Since: SeedHealthKit},
	{Code: "diet_fat_saturated", Section: secNutrition, Unit: "g", Kinds: interval, Agg: Additive, Min: 0, Max: 1000, HK: "DietaryFatSaturated", Since: SeedHealthKit},
	{Code: "diet_fat_monounsaturated", Section: secNutrition, Unit: "g", Kinds: interval, Agg: Additive, Min: 0, Max: 1000, HK: "DietaryFatMonounsaturated", Since: SeedHealthKit},
	{Code: "diet_fat_polyunsaturated", Section: secNutrition, Unit: "g", Kinds: interval, Agg: Additive, Min: 0, Max: 1000, HK: "DietaryFatPolyunsaturated", Since: SeedHealthKit},
	{Code: "diet_fiber", Section: secNutrition, Unit: "g", Kinds: interval, Agg: Additive, Min: 0, Max: 1000, HK: "DietaryFiber", Since: SeedHealthKit},
	{Code: "diet_sugar", Section: secNutrition, Unit: "g", Kinds: interval, Agg: Additive, Min: 0, Max: 2000, HK: "DietarySugar", Since: SeedHealthKit},
	{Code: "diet_cholesterol", Section: secNutrition, Unit: "mg", Kinds: interval, Agg: Additive, Min: 0, Max: 20000, HK: "DietaryCholesterol", Since: SeedHealthKit},
	{Code: "diet_water", Section: secNutrition, Unit: "mL", Kinds: interval, Agg: Additive, Min: 0, Max: 20000, HK: "DietaryWater", Since: SeedHealthKit},
	{Code: "diet_caffeine", Section: secNutrition, Unit: "mg", Kinds: interval, Agg: Additive, Min: 0, Max: 5000, HK: "DietaryCaffeine", Since: SeedHealthKit},
	{Code: "walking_steadiness", Section: secMobility, Unit: "%", Kinds: sample, Agg: Latest, Min: 0, Max: 100, HK: "AppleWalkingSteadiness", Since: SeedHealthKit},
	{Code: "walking_asymmetry", Section: secMobility, Unit: "%", Kinds: sample, Agg: Intensive, Min: 0, Max: 100, HK: "WalkingAsymmetryPercentage", Since: SeedHealthKit},
	{Code: "walking_double_support", Section: secMobility, Unit: "%", Kinds: sample, Agg: Intensive, Min: 0, Max: 100, HK: "WalkingDoubleSupportPercentage", Since: SeedHealthKit},
	{Code: "walking_step_length", Section: secMobility, Unit: "m", Kinds: sample, Agg: Intensive, Min: 0.1, Max: 3, HK: "WalkingStepLength", Since: SeedHealthKit},

	// Garmin Connect (E18, J18.4): provider-scoped scores (metric-catalog.md#provider-namespaced-scores), 0-100.
	{Code: "garmin_stress", Section: secHeart, Unit: "index", Kinds: sample, Agg: Intensive, Min: 0, Max: 100, ProviderScoped: true, Since: SeedSidecars},
	{Code: "garmin_body_battery", Section: secHeart, Unit: "index", Kinds: sample, Agg: Intensive, Min: 0, Max: 100, ProviderScoped: true, Since: SeedSidecars},
	// Snapshots: Garmin updates the score during the day, so the day's value is the latest.
	{Code: "garmin_training_readiness", Section: secHeart, Unit: "index", Kinds: sample, Agg: DailySummary, Min: 0, Max: 100, ProviderScoped: true, Since: SeedSidecars},
	{Code: "garmin_sleep_score", Section: secSleep, Unit: "index", Kinds: daily, Agg: DailySummary, Min: 0, Max: 100, ProviderScoped: true, Since: SeedSidecars},

	// WHOOP (E19, J19.4): provider-scoped scores. Recovery and day strain are daily values at the
	// cycle's wake-up; sleep performance is a sample at the sleep's wake-up.
	{Code: "whoop_recovery", Section: secHeart, Unit: "%", Kinds: daily, Agg: DailySummary, Min: 0, Max: 100, ProviderScoped: true, Since: SeedSidecars},
	{Code: "whoop_strain", Section: secActivity, Unit: "index", Kinds: daily, Agg: DailySummary, Min: 0, Max: 21, ProviderScoped: true, Since: SeedSidecars},
	{Code: "whoop_sleep_performance", Section: secSleep, Unit: "%", Kinds: sample, Agg: DailySummary, Min: 0, Max: 100, ProviderScoped: true, Since: SeedSidecars},

	// Mapping completeness (E25, J25.1): the codes the corrected mappings, the Apple Health type registry and the sidecar
	// connectors need, so later jobs only use them.
	{Code: "distance_rowing", Section: secActivity, Unit: "m", Kinds: interval, Agg: Additive, Min: 0, Max: 100000, HK: "DistanceRowing", Since: SeedMappings},
	{Code: "distance_paddle", Section: secActivity, Unit: "m", Kinds: interval, Agg: Additive, Min: 0, Max: 100000, HK: "DistancePaddleSports", Since: SeedMappings},
	{Code: "distance_skating", Section: secActivity, Unit: "m", Kinds: interval, Agg: Additive, Min: 0, Max: 200000, HK: "DistanceSkatingSports", Since: SeedMappings},
	{Code: "distance_xc_ski", Section: secActivity, Unit: "m", Kinds: interval, Agg: Additive, Min: 0, Max: 200000, HK: "DistanceCrossCountrySkiing", Since: SeedMappings},
	{Code: "distance_downhill_snow", Section: secActivity, Unit: "m", Kinds: interval, Agg: Additive, Min: 0, Max: 300000, HK: "DistanceDownhillSnowSports", Since: SeedMappings},
	{Code: "elevation_gain", Section: secActivity, Unit: "m", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 20000, Since: SeedMappings},
	{Code: "total_energy", Section: secActivity, Unit: "kcal", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 30000, Since: SeedMappings},
	{Code: "intensity_light_time", Section: secActivity, Unit: "s", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 86400, SelectionOnly: true, Since: SeedMappings},
	{Code: "intensity_moderate_time", Section: secActivity, Unit: "s", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 86400, SelectionOnly: true, Since: SeedMappings},
	{Code: "intensity_vigorous_time", Section: secActivity, Unit: "s", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 86400, SelectionOnly: true, Since: SeedMappings},
	{Code: "sedentary_time", Section: secActivity, Unit: "s", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 86400, SelectionOnly: true, Since: SeedMappings},
	{Code: "move_time", Section: secActivity, Unit: "s", Kinds: interval, Agg: Additive, Min: 0, Max: 86400, HK: "AppleMoveTime", Since: SeedMappings},
	{Code: "daylight_time", Section: secActivity, Unit: "s", Kinds: interval, Agg: Additive, Min: 0, Max: 86400, HK: "TimeInDaylight", Since: SeedMappings},
	{Code: "wheelchair_pushes", Section: secActivity, Unit: "count", Kinds: interval, Agg: Additive, Min: 0, Max: 50000, HK: "PushCount", Since: SeedMappings},
	{Code: "swim_strokes", Section: secActivity, Unit: "count", Kinds: interval, Agg: Additive, Min: 0, Max: 100000, HK: "SwimmingStrokeCount", Since: SeedMappings},
	{Code: "speed_walking", Section: secActivity, Unit: "m/s", Kinds: sample, Agg: Intensive, Min: 0, Max: 10, HK: "WalkingSpeed", Since: SeedMappings},
	{Code: "speed_running", Section: secActivity, Unit: "m/s", Kinds: sample, Agg: Intensive, Min: 0, Max: 20, HK: "RunningSpeed", Since: SeedMappings},
	{Code: "speed_cycling", Section: secActivity, Unit: "m/s", Kinds: sample, Agg: Intensive, Min: 0, Max: 40, HK: "CyclingSpeed", Since: SeedMappings},
	{Code: "speed_rowing", Section: secActivity, Unit: "m/s", Kinds: sample, Agg: Intensive, Min: 0, Max: 15, HK: "RowingSpeed", Since: SeedMappings},
	{Code: "speed_paddle", Section: secActivity, Unit: "m/s", Kinds: sample, Agg: Intensive, Min: 0, Max: 15, HK: "PaddleSportsSpeed", Since: SeedMappings},
	{Code: "cadence_cycling", Section: secActivity, Unit: "rpm", Kinds: sample, Agg: Intensive, Min: 0, Max: 300, HK: "CyclingCadence", Since: SeedMappings},
	{Code: "power_running", Section: secActivity, Unit: "W", Kinds: sample, Agg: Intensive, Min: 0, Max: 2000, HK: "RunningPower", Since: SeedMappings},
	{Code: "power_cycling", Section: secActivity, Unit: "W", Kinds: sample, Agg: Intensive, Min: 0, Max: 3000, HK: "CyclingPower", Since: SeedMappings},
	{Code: "ftp_cycling", Section: secActivity, Unit: "W", Kinds: sample, Agg: Latest, Min: 0, Max: 1000, HK: "CyclingFunctionalThresholdPower", Since: SeedMappings},
	{Code: "running_stride_length", Section: secActivity, Unit: "m", Kinds: sample, Agg: Intensive, Min: 0.2, Max: 3, HK: "RunningStrideLength", Since: SeedMappings},
	{Code: "running_vertical_oscillation", Section: secActivity, Unit: "m", Kinds: sample, Agg: Intensive, Min: 0.01, Max: 0.5, HK: "RunningVerticalOscillation", Since: SeedMappings},
	{Code: "running_ground_contact_time", Section: secActivity, Unit: "s", Kinds: sample, Agg: Intensive, Min: 0.05, Max: 1, HK: "RunningGroundContactTime", Since: SeedMappings},
	{Code: "physical_effort", Section: secActivity, Unit: "kcal/kg/h", Kinds: sample, Agg: Intensive, Min: 0, Max: 100, HK: "PhysicalEffort", Since: SeedMappings},
	{Code: "garmin_acute_load", Section: secActivity, Unit: "index", Kinds: daily, Agg: DailySummary, Min: 0, Max: 10000, ProviderScoped: true, Since: SeedMappings},
	{Code: "garmin_chronic_load", Section: secActivity, Unit: "index", Kinds: daily, Agg: DailySummary, Min: 0, Max: 10000, ProviderScoped: true, Since: SeedMappings},
	{Code: "sleeping_heart_rate", Section: secHeart, Unit: "bpm", Kinds: daily, Agg: DailySummary, Min: 20, Max: 150, Since: SeedMappings},
	{Code: "heart_rate_recovery_1min", Section: secHeart, Unit: "bpm", Kinds: sample, Agg: Latest, Min: 0, Max: 150, HK: "HeartRateRecoveryOneMinute", Since: SeedMappings},
	{Code: "afib_burden", Section: secHeart, Unit: "%", Kinds: daily, Agg: DailySummary, Min: 0, Max: 100, HK: "AtrialFibrillationBurden", Since: SeedMappings},
	{Code: "perfusion_index", Section: secHeart, Unit: "%", Kinds: sample, Agg: Intensive, Min: 0, Max: 20, HK: "PeripheralPerfusionIndex", Since: SeedMappings},
	{Code: "ecg_qrs", Section: secHeart, Unit: "s", Kinds: sample, Agg: Latest, Min: 0.02, Max: 0.3, Withings: "135", Since: SeedMappings},
	{Code: "ecg_pr", Section: secHeart, Unit: "s", Kinds: sample, Agg: Latest, Min: 0.05, Max: 0.6, Withings: "136", Since: SeedMappings},
	{Code: "ecg_qt", Section: secHeart, Unit: "s", Kinds: sample, Agg: Latest, Min: 0.2, Max: 0.8, Withings: "137", Since: SeedMappings},
	{Code: "ecg_qtc", Section: secHeart, Unit: "s", Kinds: sample, Agg: Latest, Min: 0.2, Max: 0.8, Withings: "138", Since: SeedMappings},
	{Code: "whoop_max_heart_rate", Section: secHeart, Unit: "bpm", Kinds: sample, Agg: Latest, Min: 100, Max: 250, ProviderScoped: true, Since: SeedMappings},
	{Code: "garmin_body_battery_charged", Section: secHeart, Unit: "index", Kinds: daily, Agg: DailySummary, Min: 0, Max: 200, ProviderScoped: true, Since: SeedMappings},
	{Code: "garmin_body_battery_drained", Section: secHeart, Unit: "index", Kinds: daily, Agg: DailySummary, Min: 0, Max: 200, ProviderScoped: true, Since: SeedMappings},
	{Code: "spo2_nightly", Section: secRespiration, Unit: "%", Kinds: daily, Agg: DailySummary, Min: 50, Max: 100, SelectionOnly: true, Since: SeedMappings},
	{Code: "respiratory_rate_nightly", Section: secRespiration, Unit: "breaths/min", Kinds: daily, Agg: DailySummary, Min: 4, Max: 60, SelectionOnly: true, Since: SeedMappings},
	{Code: "apnea_hypopnea_index", Section: secRespiration, Unit: "events/h", Kinds: daily, Agg: DailySummary, Min: 0, Max: 150, Since: SeedMappings},
	{Code: "fev1", Section: secRespiration, Unit: "L", Kinds: sample, Agg: Latest, Min: 0, Max: 10, HK: "ForcedExpiratoryVolume1", Since: SeedMappings},
	{Code: "fvc", Section: secRespiration, Unit: "L", Kinds: sample, Agg: Latest, Min: 0, Max: 10, HK: "ForcedVitalCapacity", Since: SeedMappings},
	{Code: "peak_expiratory_flow", Section: secRespiration, Unit: "L/min", Kinds: sample, Agg: Latest, Min: 0, Max: 1000, HK: "PeakExpiratoryFlowRate", Since: SeedMappings},
	{Code: "inhaler_uses", Section: secRespiration, Unit: "count", Kinds: interval, Agg: Additive, Min: 0, Max: 200, HK: "InhalerUsage", Since: SeedMappings},
	{Code: "withings_breathing_quality", Section: secRespiration, Unit: "index", Kinds: daily, Agg: DailySummary, Min: 0, Max: 100, ProviderScoped: true, Since: SeedMappings},
	{Code: "skin_temperature_nightly", Section: secTemperature, Unit: "°C", Kinds: daily, Agg: DailySummary, Min: 20, Max: 45, SelectionOnly: true, Since: SeedMappings},
	{Code: "sleep_temperature_deviation", Section: secTemperature, Unit: "°C", Kinds: daily, Agg: DailySummary, Min: -10, Max: 10, SelectionOnly: true, Since: SeedMappings},
	{Code: "fat_free_mass_trunk", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.01, Max: 150, Group: groupBody, Withings: "173", Since: SeedMappings},
	{Code: "fat_free_mass_left_arm", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.01, Max: 150, Group: groupBody, Withings: "173", Since: SeedMappings},
	{Code: "fat_free_mass_right_arm", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.01, Max: 150, Group: groupBody, Withings: "173", Since: SeedMappings},
	{Code: "fat_free_mass_left_leg", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.01, Max: 150, Group: groupBody, Withings: "173", Since: SeedMappings},
	{Code: "fat_free_mass_right_leg", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.01, Max: 150, Group: groupBody, Withings: "173", Since: SeedMappings},
	{Code: "fat_mass_trunk", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.01, Max: 100, Group: groupBody, Withings: "174", Since: SeedMappings},
	{Code: "fat_mass_left_arm", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.01, Max: 100, Group: groupBody, Withings: "174", Since: SeedMappings},
	{Code: "fat_mass_right_arm", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.01, Max: 100, Group: groupBody, Withings: "174", Since: SeedMappings},
	{Code: "fat_mass_left_leg", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.01, Max: 100, Group: groupBody, Withings: "174", Since: SeedMappings},
	{Code: "fat_mass_right_leg", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.01, Max: 100, Group: groupBody, Withings: "174", Since: SeedMappings},
	{Code: "muscle_mass_trunk", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.01, Max: 100, Group: groupBody, Withings: "175", Since: SeedMappings},
	{Code: "muscle_mass_left_arm", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.01, Max: 100, Group: groupBody, Withings: "175", Since: SeedMappings},
	{Code: "muscle_mass_right_arm", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.01, Max: 100, Group: groupBody, Withings: "175", Since: SeedMappings},
	{Code: "muscle_mass_left_leg", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.01, Max: 100, Group: groupBody, Withings: "175", Since: SeedMappings},
	{Code: "muscle_mass_right_leg", Section: secBody, Unit: "kg", Kinds: sample, Agg: Latest, Min: 0.01, Max: 100, Group: groupBody, Withings: "175", Since: SeedMappings},
	{Code: "body_water_ratio", Section: secBody, Unit: "%", Kinds: sample, Agg: Latest, Min: 10, Max: 90, Group: groupBody, Since: SeedMappings},
	{Code: "withings_nerve_health_score", Section: secBody, Unit: "index", Kinds: sample, Agg: Latest, Min: 0, Max: 100, ProviderScoped: true, Withings: "167", Since: SeedMappings},
	{Code: "withings_nerve_response_score", Section: secBody, Unit: "index", Kinds: sample, Agg: Latest, Min: 0, Max: 100, ProviderScoped: true, Withings: "196", Since: SeedMappings},
	{Code: "withings_esc", Section: secBody, Unit: "µS", Kinds: sample, Agg: Latest, Min: 0, Max: 200, ProviderScoped: true, Withings: "229", Since: SeedMappings},
	{Code: "withings_metabolic_age", Section: secBody, Unit: "years", Kinds: sample, Agg: Latest, Min: 10, Max: 120, ProviderScoped: true, Withings: "227", Since: SeedMappings},
	{Code: "garmin_fitness_age", Section: secBody, Unit: "years", Kinds: sample, Agg: Latest, Min: 10, Max: 120, ProviderScoped: true, Since: SeedMappings},
	{Code: "insulin_basal", Section: secGlucose, Unit: "IU", Kinds: interval, Agg: Additive, Min: 0, Max: 500, HK: "InsulinDelivery", Since: SeedMappings},
	{Code: "insulin_bolus", Section: secGlucose, Unit: "IU", Kinds: interval, Agg: Additive, Min: 0, Max: 500, HK: "InsulinDelivery", Since: SeedMappings},
	{Code: "blood_alcohol", Section: secGlucose, Unit: "%", Kinds: sample, Agg: Latest, Min: 0, Max: 100, HK: "BloodAlcoholContent", Since: SeedMappings},
	{Code: "alcoholic_drinks", Section: secGlucose, Unit: "count", Kinds: interval, Agg: Additive, Min: 0, Max: 200, HK: "NumberOfAlcoholicBeverages", Since: SeedMappings},
	{Code: "stair_ascent_speed", Section: secMobility, Unit: "m/s", Kinds: sample, Agg: Intensive, Min: 0, Max: 5, HK: "StairAscentSpeed", Since: SeedMappings},
	{Code: "stair_descent_speed", Section: secMobility, Unit: "m/s", Kinds: sample, Agg: Intensive, Min: 0, Max: 5, HK: "StairDescentSpeed", Since: SeedMappings},
	{Code: "six_minute_walk_distance", Section: secMobility, Unit: "m", Kinds: sample, Agg: Latest, Min: 0, Max: 1500, HK: "SixMinuteWalkTestDistance", Since: SeedMappings},
	{Code: "falls", Section: secMobility, Unit: "count", Kinds: interval, Agg: Additive, Min: 0, Max: 100, HK: "NumberOfTimesFallen", Since: SeedMappings},
	{Code: "environment_audio_exposure", Section: secEnvironment, Unit: "dBA", Kinds: sample, Agg: Intensive, Min: 0, Max: 200, HK: "EnvironmentalAudioExposure", Since: SeedMappings},
	{Code: "headphone_audio_exposure", Section: secEnvironment, Unit: "dBA", Kinds: sample, Agg: Intensive, Min: 0, Max: 200, HK: "HeadphoneAudioExposure", Since: SeedMappings},
	{Code: "environment_sound_reduction", Section: secEnvironment, Unit: "dB", Kinds: sample, Agg: Intensive, Min: 0, Max: 100, HK: "EnvironmentalSoundReduction", Since: SeedMappings},
	{Code: "uv_exposure", Section: secEnvironment, Unit: "index", Kinds: sample, Agg: Intensive, Min: 0, Max: 20, HK: "UVExposure", Since: SeedMappings},
	{Code: "water_temperature", Section: secEnvironment, Unit: "°C", Kinds: sample, Agg: Latest, Min: -2, Max: 50, HK: "WaterTemperature", Since: SeedMappings},
	{Code: "underwater_depth", Section: secEnvironment, Unit: "m", Kinds: sample, Agg: Latest, Min: 0, Max: 200, HK: "UnderwaterDepth", Since: SeedMappings},
	{Code: "electrodermal_activity", Section: secEnvironment, Unit: "µS", Kinds: sample, Agg: Intensive, Min: 0, Max: 200, HK: "ElectrodermalActivity", Since: SeedMappings},
	{Code: "sleep_awakenings", Section: secSleep, Unit: "count", Kinds: daily, Agg: DailySummary, Min: 0, Max: 200, Since: SeedMappings},
	{Code: "sleep_snoring_time", Section: secSleep, Unit: "s", Kinds: daily, Agg: DailySummary, Min: 0, Max: 86400, Since: SeedMappings},
	{Code: "sleep_snoring_episodes", Section: secSleep, Unit: "count", Kinds: daily, Agg: DailySummary, Min: 0, Max: 500, Since: SeedMappings},
	{Code: "whoop_sleep_need", Section: secSleep, Unit: "s", Kinds: daily, Agg: DailySummary, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedMappings},
	{Code: "whoop_sleep_debt", Section: secSleep, Unit: "s", Kinds: daily, Agg: DailySummary, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedMappings},
	{Code: "whoop_sleep_consistency", Section: secSleep, Unit: "%", Kinds: daily, Agg: DailySummary, Min: 0, Max: 100, ProviderScoped: true, Since: SeedMappings},
	{Code: "whoop_sleep_disturbances", Section: secSleep, Unit: "count", Kinds: daily, Agg: DailySummary, Min: 0, Max: 500, ProviderScoped: true, Since: SeedMappings},
	{Code: "withings_sleep_score", Section: secSleep, Unit: "index", Kinds: daily, Agg: DailySummary, Min: 0, Max: 100, ProviderScoped: true, Since: SeedMappings},

	// WHOOP leftovers (J25.4): a workout's strain and its time in each heart-rate zone are intervals over the workout; the
	// sleep-need parts and the debt left after sleep are daily values at the main sleep's wake-up (whoop_sleep_debt is the
	// debt inside the night's need).
	{Code: "whoop_workout_strain", Section: secActivity, Unit: "index", Kinds: interval, Agg: Latest, Min: 0, Max: 21, ProviderScoped: true, Since: SeedWhoop},
	{Code: "whoop_hr_zone_0_time", Section: secActivity, Unit: "s", Kinds: interval, Agg: Additive, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedWhoop},
	{Code: "whoop_hr_zone_1_time", Section: secActivity, Unit: "s", Kinds: interval, Agg: Additive, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedWhoop},
	{Code: "whoop_hr_zone_2_time", Section: secActivity, Unit: "s", Kinds: interval, Agg: Additive, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedWhoop},
	{Code: "whoop_hr_zone_3_time", Section: secActivity, Unit: "s", Kinds: interval, Agg: Additive, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedWhoop},
	{Code: "whoop_hr_zone_4_time", Section: secActivity, Unit: "s", Kinds: interval, Agg: Additive, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedWhoop},
	{Code: "whoop_hr_zone_5_time", Section: secActivity, Unit: "s", Kinds: interval, Agg: Additive, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedWhoop},
	{Code: "elevation_change", Section: secActivity, Unit: "m", Kinds: intervalDaily, Agg: Additive, Min: -20000, Max: 20000, Since: SeedWhoop},
	{Code: "whoop_sleep_debt_post", Section: secSleep, Unit: "s", Kinds: daily, Agg: DailySummary, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedWhoop},
	{Code: "whoop_sleep_need_habitual", Section: secSleep, Unit: "s", Kinds: daily, Agg: DailySummary, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedWhoop},
	{Code: "whoop_sleep_need_from_strain", Section: secSleep, Unit: "s", Kinds: daily, Agg: DailySummary, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedWhoop},
	{Code: "whoop_sleep_nap_credit", Section: secSleep, Unit: "s", Kinds: daily, Agg: DailySummary, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedWhoop},
	{Code: "whoop_sleep_cycles", Section: secSleep, Unit: "count", Kinds: daily, Agg: DailySummary, Min: 0, Max: 20, ProviderScoped: true, Since: SeedWhoop},
	{Code: "garmin_hr_zone_1_time", Section: secActivity, Unit: "s", Kinds: interval, Agg: Additive, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_hr_zone_2_time", Section: secActivity, Unit: "s", Kinds: interval, Agg: Additive, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_hr_zone_3_time", Section: secActivity, Unit: "s", Kinds: interval, Agg: Additive, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_hr_zone_4_time", Section: secActivity, Unit: "s", Kinds: interval, Agg: Additive, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_hr_zone_5_time", Section: secActivity, Unit: "s", Kinds: interval, Agg: Additive, Min: 0, Max: 86400, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_training_effect_aerobic", Section: secActivity, Unit: "index", Kinds: sample, Agg: Latest, Min: 0, Max: 5, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_training_effect_anaerobic", Section: secActivity, Unit: "index", Kinds: sample, Agg: Latest, Min: 0, Max: 5, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_activity_training_load", Section: secActivity, Unit: "index", Kinds: interval, Agg: Additive, Min: 0, Max: 5000, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_activity_moving_time", Section: secActivity, Unit: "s", Kinds: interval, Agg: Additive, Min: 0, Max: 604800, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_floors_descended", Section: secActivity, Unit: "count", Kinds: interval, Agg: Additive, Min: 0, Max: 3000, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_chronic_load_low", Section: secActivity, Unit: "index", Kinds: daily, Agg: DailySummary, Min: 0, Max: 10000, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_chronic_load_high", Section: secActivity, Unit: "index", Kinds: daily, Agg: DailySummary, Min: 0, Max: 10000, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_recovery_time", Section: secActivity, Unit: "min", Kinds: sample, Agg: Latest, Min: 0, Max: 10000, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_vo2max_cycling", Section: secHeart, Unit: "mL/kg/min", Kinds: sample, Agg: Latest, Min: 10, Max: 100, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_hrv_baseline_low", Section: secHeart, Unit: "ms", Kinds: daily, Agg: DailySummary, Min: 1, Max: 500, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_hrv_baseline_high", Section: secHeart, Unit: "ms", Kinds: daily, Agg: DailySummary, Min: 1, Max: 500, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_hrv_baseline_floor", Section: secHeart, Unit: "ms", Kinds: daily, Agg: DailySummary, Min: 1, Max: 500, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_metabolic_age", Section: secBody, Unit: "years", Kinds: sample, Agg: Latest, Min: 10, Max: 120, Group: groupBody, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_physique_rating", Section: secBody, Unit: "index", Kinds: sample, Agg: Latest, Min: 1, Max: 9, Group: groupBody, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_achievable_fitness_age", Section: secBody, Unit: "years", Kinds: sample, Agg: Latest, Min: 10, Max: 120, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_sweat_loss", Section: secNutrition, Unit: "mL", Kinds: interval, Agg: Additive, Min: 0, Max: 20000, ProviderScoped: true, Since: SeedGarmin},
	{Code: "garmin_sleep_movement", Section: secSleep, Unit: "index", Kinds: sample, Agg: Intensive, Min: 0, Max: 100, ProviderScoped: true, Since: SeedGarmin},

	// Withings (J25.2, J25.3): the U-Scan urine analytes and the core body temperature estimate.
	{Code: "urine_ph", Section: secUrine, Unit: "pH", Kinds: sample, Agg: Latest, Min: 3, Max: 10, Withings: "147", Since: SeedWithings},
	{Code: "urine_specific_gravity", Section: secUrine, Unit: "ratio", Kinds: sample, Agg: Latest, Min: 1, Max: 1.1, Withings: "148", Since: SeedWithings},
	{Code: "urine_nitrites", Section: secUrine, Unit: "µmol/L", Kinds: sample, Agg: Latest, Min: 0, Max: 5000, Withings: "151", Since: SeedWithings},
	{Code: "urine_ketones", Section: secUrine, Unit: "mmol/L", Kinds: sample, Agg: Latest, Min: 0, Max: 50, Withings: "204", Since: SeedWithings},
	{Code: "urine_vitamin_c", Section: secUrine, Unit: "mmol/L", Kinds: sample, Agg: Latest, Min: 0, Max: 50, Withings: "205", Since: SeedWithings},
	{Code: "urine_calcium", Section: secUrine, Unit: "mmol/L", Kinds: sample, Agg: Latest, Min: 0, Max: 50, Withings: "248", Since: SeedWithings},
	{Code: "urine_creatinine", Section: secUrine, Unit: "mmol/L", Kinds: sample, Agg: Latest, Min: 0, Max: 100, Withings: "249", Since: SeedWithings},
	{Code: "urine_calcium_creatinine_ratio", Section: secUrine, Unit: "mmol/mmol", Kinds: sample, Agg: Latest, Min: 0, Max: 10, Withings: "251", Since: SeedWithings},
	{Code: "core_body_temperature_estimated", Section: secTemperature, Unit: "°C", Kinds: sample, Agg: Intensive, Min: 30, Max: 45, Since: SeedWithings},
}
