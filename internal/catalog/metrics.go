package catalog

// Section names, in docs/metrics.md order.
const (
	secActivity    = "Activity"
	secHeart       = "Heart and circulation"
	secBP          = "Blood pressure"
	secRespiration = "Respiration and oxygen"
	secTemperature = "Temperature"
	secBody        = "Body composition"
	secSleep       = "Sleep"
)

var (
	sample        = []Kind{Sample}
	sampleDaily   = []Kind{Sample, DailyValue}
	intervalDaily = []Kind{Interval, DailyValue}
	daily         = []Kind{DailyValue}
)

const (
	groupBP   = "bp_reading"
	groupBody = "body_composition"
)

// metrics is every v1 code of docs/architecture/metric-catalog.md. The slice order is the
// seed order, so changing it changes ids in a fresh database: append, never reorder.
var metrics = []Metric{
	{Code: "steps", Section: secActivity, Unit: "count", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 200000, HK: "StepCount"},
	{Code: "distance_walk_run", Section: secActivity, Unit: "m", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 300000, HK: "DistanceWalkingRunning"},
	{Code: "active_energy", Section: secActivity, Unit: "kcal", Kinds: intervalDaily, Agg: Additive, Min: 0, Max: 20000, HK: "ActiveEnergyBurned"},

	{Code: "heart_rate", Section: secHeart, Unit: "bpm", Kinds: sample, Agg: Intensive, Min: 20, Max: 250, HK: "HeartRate", Withings: "11 (outside BP)"},
	{Code: "resting_heart_rate", Section: secHeart, Unit: "bpm", Kinds: sampleDaily, Agg: DailySummary, Min: 20, Max: 150, HK: "RestingHeartRate"},
	{Code: "hrv_sdnn", Section: secHeart, Unit: "ms", Kinds: sample, Agg: Intensive, Min: 1, Max: 500, HK: "HeartRateVariabilitySDNN"},
	{Code: "hrv_rmssd", Section: secHeart, Unit: "ms", Kinds: sample, Agg: Intensive, Min: 1, Max: 500},
	{Code: "hrv_rmssd_nightly", Section: secHeart, Unit: "ms", Kinds: daily, Agg: DailySummary, Min: 1, Max: 500},
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
}
