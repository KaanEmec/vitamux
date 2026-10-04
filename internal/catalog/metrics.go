package catalog

// Section names, in docs/metrics.md order.
const (
	secActivity    = "Activity"
	secHeart       = "Heart and circulation"
	secBP          = "Blood pressure"
	secRespiration = "Respiration and oxygen"
	secTemperature = "Temperature"
	secBody        = "Body composition"
	secGlucose     = "Glucose and metabolism"
	secNutrition   = "Nutrition and intake"
	secMobility    = "Mobility"
	secSleep       = "Sleep"
	secDerived     = "Derived"
)

// sections is the docs/metrics.md order; seed order is append-only, so the doc groups by section.
var sections = []string{secActivity, secHeart, secBP, secRespiration, secTemperature, secBody, secGlucose,
	secNutrition, secMobility, secSleep, secDerived}

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
}
