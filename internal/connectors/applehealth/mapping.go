package applehealth

import (
	"maps"
	"strconv"
	"strings"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

const (
	hkQuantity = "HKQuantityTypeIdentifier"
	hkCategory = "HKCategoryTypeIdentifier"

	typeBloodPressure = "HKCorrelationTypeIdentifierBloodPressure"
	typeWorkout       = "HKWorkoutTypeIdentifier"
	typeSleep         = hkCategory + "SleepAnalysis"
	typeStandHour     = hkCategory + "AppleStandHour"
	typeSystolic      = hkQuantity + "BloodPressureSystolic"
	typeDiastolic     = hkQuantity + "BloodPressureDiastolic"
	typeHeartRate     = hkQuantity + "HeartRate"
	typeInsulin       = hkQuantity + "InsulinDelivery"

	// Apple Watch types of the type registry v2 (ADR-0024), as HealthBridgeKit sends them.
	// Electrocardiogram and activity summary have no identifier constant (the kit sends what
	// HealthKit reports), and the State of Mind constant's value has no "Identifier".
	typeHeartbeat       = "HKDataTypeIdentifierHeartbeatSeries"
	typeECG             = "HKDataTypeIdentifierElectrocardiogram"
	typeRoute           = "HKWorkoutRouteTypeIdentifier"
	typeStateOfMind     = "HKDataTypeStateOfMind"
	typeActivitySummary = "HKActivitySummaryTypeIdentifier"
)

// quantity maps one HealthKit quantity type: the catalogue code, the kind a sample becomes, the
// HKUnit string the type registry reads it in (apple/HealthBridgeKit Registry.swift) and the
// catalogue unit that value is in. A sample in any other unit is refused, never reinterpreted.
type quantity struct {
	metric       string
	kind         catalog.Kind
	hkUnit, unit string
}

func q(metric string, kind catalog.Kind, hkUnit, unit string) quantity {
	return quantity{metric, kind, hkUnit, unit}
}

// quantities is the HK column of docs/metrics.md for the type registry v1 (insulin: typeInsulin). Percent types arrive
// as fractions; daily values are HealthKit's own per-day or per-night summaries.
var quantities = map[string]quantity{
	hkQuantity + "StepCount":                      q("steps", catalog.Interval, "count", "count"),
	hkQuantity + "DistanceWalkingRunning":         q("distance_walk_run", catalog.Interval, "m", "m"),
	hkQuantity + "DistanceCycling":                q("distance_cycling", catalog.Interval, "m", "m"),
	hkQuantity + "DistanceSwimming":               q("distance_swimming", catalog.Interval, "m", "m"),
	hkQuantity + "DistanceWheelchair":             q("distance_wheelchair", catalog.Interval, "m", "m"),
	hkQuantity + "FlightsClimbed":                 q("floors_climbed", catalog.Interval, "count", "count"),
	hkQuantity + "ActiveEnergyBurned":             q("active_energy", catalog.Interval, "kcal", "kcal"),
	hkQuantity + "BasalEnergyBurned":              q("basal_energy", catalog.Interval, "kcal", "kcal"),
	hkQuantity + "AppleExerciseTime":              q("exercise_time", catalog.Interval, "s", "s"),
	hkQuantity + "AppleStandTime":                 q("stand_time", catalog.Interval, "s", "s"),
	hkQuantity + "AppleWalkingSteadiness":         q("walking_steadiness", catalog.Sample, "%", "fraction"),
	hkQuantity + "WalkingAsymmetryPercentage":     q("walking_asymmetry", catalog.Sample, "%", "fraction"),
	hkQuantity + "WalkingDoubleSupportPercentage": q("walking_double_support", catalog.Sample, "%", "fraction"),
	hkQuantity + "WalkingStepLength":              q("walking_step_length", catalog.Sample, "m", "m"),

	hkQuantity + "HeartRate":                          q("heart_rate", catalog.Sample, "count/min", "bpm"),
	hkQuantity + "RestingHeartRate":                   q("resting_heart_rate", catalog.DailyValue, "count/min", "bpm"),
	hkQuantity + "WalkingHeartRateAverage":            q("walking_heart_rate", catalog.DailyValue, "count/min", "bpm"),
	hkQuantity + "HeartRateVariabilitySDNN":           q("hrv_sdnn", catalog.Sample, "ms", "ms"),
	hkQuantity + "VO2Max":                             q("vo2max", catalog.Sample, "ml/kg*min", "mL/kg/min"),
	hkQuantity + "OxygenSaturation":                   q("spo2", catalog.Sample, "%", "fraction"),
	hkQuantity + "RespiratoryRate":                    q("respiratory_rate", catalog.Sample, "count/min", "breaths/min"),
	hkQuantity + "BodyTemperature":                    q("body_temperature", catalog.Sample, "degC", "°C"),
	hkQuantity + "BasalBodyTemperature":               q("basal_body_temperature", catalog.Sample, "degC", "°C"),
	hkQuantity + "BloodGlucose":                       q("blood_glucose", catalog.Sample, "mg/dL", "mg/dL glucose"),
	hkQuantity + "BodyMass":                           q("weight", catalog.Sample, "kg", "kg"),
	hkQuantity + "Height":                             q("height", catalog.Sample, "m", "m"),
	hkQuantity + "BodyFatPercentage":                  q("body_fat_ratio", catalog.Sample, "%", "fraction"),
	hkQuantity + "BodyMassIndex":                      q("bmi", catalog.Sample, "count", "kg/m²"),
	hkQuantity + "LeanBodyMass":                       q("lean_body_mass", catalog.Sample, "kg", "kg"),
	hkQuantity + "WaistCircumference":                 q("waist_circumference", catalog.Sample, "m", "m"),
	hkQuantity + "AppleSleepingBreathingDisturbances": q("breathing_disturbances", catalog.DailyValue, "count", "events/h"),
	hkQuantity + "AppleSleepingWristTemperature":      q("wrist_temperature_sleeping", catalog.DailyValue, "degC", "°C"),

	hkQuantity + "DietaryEnergyConsumed":     q("diet_energy", catalog.Interval, "kcal", "kcal"),
	hkQuantity + "DietaryProtein":            q("diet_protein", catalog.Interval, "g", "g"),
	hkQuantity + "DietaryCarbohydrates":      q("diet_carbohydrate", catalog.Interval, "g", "g"),
	hkQuantity + "DietaryFatTotal":           q("diet_fat_total", catalog.Interval, "g", "g"),
	hkQuantity + "DietaryFatSaturated":       q("diet_fat_saturated", catalog.Interval, "g", "g"),
	hkQuantity + "DietaryFatMonounsaturated": q("diet_fat_monounsaturated", catalog.Interval, "g", "g"),
	hkQuantity + "DietaryFatPolyunsaturated": q("diet_fat_polyunsaturated", catalog.Interval, "g", "g"),
	hkQuantity + "DietaryFiber":              q("diet_fiber", catalog.Interval, "g", "g"),
	hkQuantity + "DietarySugar":              q("diet_sugar", catalog.Interval, "g", "g"),
	hkQuantity + "DietaryCholesterol":        q("diet_cholesterol", catalog.Interval, "mg", "mg"),
	hkQuantity + "DietaryWater":              q("diet_water", catalog.Interval, "mL", "mL"),
	hkQuantity + "DietaryCaffeine":           q("diet_caffeine", catalog.Interval, "mg", "mg"),

	// E25 (J25.7): the types the catalogue gained with the mapping seed.
	hkQuantity + "DistanceRowing":                  q("distance_rowing", catalog.Interval, "m", "m"),
	hkQuantity + "DistancePaddleSports":            q("distance_paddle", catalog.Interval, "m", "m"),
	hkQuantity + "DistanceSkatingSports":           q("distance_skating", catalog.Interval, "m", "m"),
	hkQuantity + "DistanceCrossCountrySkiing":      q("distance_xc_ski", catalog.Interval, "m", "m"),
	hkQuantity + "DistanceDownhillSnowSports":      q("distance_downhill_snow", catalog.Interval, "m", "m"),
	hkQuantity + "AppleMoveTime":                   q("move_time", catalog.Interval, "s", "s"),
	hkQuantity + "TimeInDaylight":                  q("daylight_time", catalog.Interval, "s", "s"),
	hkQuantity + "PushCount":                       q("wheelchair_pushes", catalog.Interval, "count", "count"),
	hkQuantity + "SwimmingStrokeCount":             q("swim_strokes", catalog.Interval, "count", "count"),
	hkQuantity + "WalkingSpeed":                    q("speed_walking", catalog.Sample, "m/s", "m/s"),
	hkQuantity + "RunningSpeed":                    q("speed_running", catalog.Sample, "m/s", "m/s"),
	hkQuantity + "CyclingSpeed":                    q("speed_cycling", catalog.Sample, "m/s", "m/s"),
	hkQuantity + "RowingSpeed":                     q("speed_rowing", catalog.Sample, "m/s", "m/s"),
	hkQuantity + "PaddleSportsSpeed":               q("speed_paddle", catalog.Sample, "m/s", "m/s"),
	hkQuantity + "CyclingCadence":                  q("cadence_cycling", catalog.Sample, "count/min", "rpm"),
	hkQuantity + "RunningPower":                    q("power_running", catalog.Sample, "W", "W"),
	hkQuantity + "CyclingPower":                    q("power_cycling", catalog.Sample, "W", "W"),
	hkQuantity + "CyclingFunctionalThresholdPower": q("ftp_cycling", catalog.Sample, "W", "W"),
	hkQuantity + "RunningStrideLength":             q("running_stride_length", catalog.Sample, "m", "m"),
	hkQuantity + "RunningVerticalOscillation":      q("running_vertical_oscillation", catalog.Sample, "m", "m"),
	hkQuantity + "RunningGroundContactTime":        q("running_ground_contact_time", catalog.Sample, "s", "s"),
	hkQuantity + "PhysicalEffort":                  q("physical_effort", catalog.Sample, "kcal/(kg*hr)", "kcal/kg/h"),
	hkQuantity + "HeartRateRecoveryOneMinute":      q("heart_rate_recovery_1min", catalog.Sample, "count/min", "bpm"),
	hkQuantity + "AtrialFibrillationBurden":        q("afib_burden", catalog.DailyValue, "%", "fraction"),
	hkQuantity + "PeripheralPerfusionIndex":        q("perfusion_index", catalog.Sample, "%", "fraction"),
	hkQuantity + "ForcedExpiratoryVolume1":         q("fev1", catalog.Sample, "L", "L"),
	hkQuantity + "ForcedVitalCapacity":             q("fvc", catalog.Sample, "L", "L"),
	hkQuantity + "PeakExpiratoryFlowRate":          q("peak_expiratory_flow", catalog.Sample, "L/min", "L/min"),
	hkQuantity + "InhalerUsage":                    q("inhaler_uses", catalog.Interval, "count", "count"),
	hkQuantity + "BloodAlcoholContent":             q("blood_alcohol", catalog.Sample, "%", "fraction"),
	hkQuantity + "NumberOfAlcoholicBeverages":      q("alcoholic_drinks", catalog.Interval, "count", "count"),
	hkQuantity + "StairAscentSpeed":                q("stair_ascent_speed", catalog.Sample, "m/s", "m/s"),
	hkQuantity + "StairDescentSpeed":               q("stair_descent_speed", catalog.Sample, "m/s", "m/s"),
	hkQuantity + "SixMinuteWalkTestDistance":       q("six_minute_walk_distance", catalog.Sample, "m", "m"),
	hkQuantity + "NumberOfTimesFallen":             q("falls", catalog.Interval, "count", "count"),
	hkQuantity + "EnvironmentalAudioExposure":      q("environment_audio_exposure", catalog.Sample, "dBASPL", "dBA"),
	hkQuantity + "HeadphoneAudioExposure":          q("headphone_audio_exposure", catalog.Sample, "dBASPL", "dBA"),
	hkQuantity + "EnvironmentalSoundReduction":     q("environment_sound_reduction", catalog.Sample, "dBASPL", "dB"),
	hkQuantity + "UVExposure":                      q("uv_exposure", catalog.Sample, "count", "index"),
	hkQuantity + "WaterTemperature":                q("water_temperature", catalog.Sample, "degC", "°C"),
	hkQuantity + "UnderwaterDepth":                 q("underwater_depth", catalog.Sample, "m", "m"),
	hkQuantity + "ElectrodermalActivity":           q("electrodermal_activity", catalog.Sample, "mcS", "µS"),

	// Registry v2 (ADR-0024). An effort score links its workout through context.workout_uuid.
	hkQuantity + "HeartRateVariabilityRMSSD":   q("hrv_rmssd", catalog.Sample, "ms", "ms"),
	hkQuantity + "CrossCountrySkiingSpeed":     q("speed_xc_ski", catalog.Sample, "m/s", "m/s"),
	hkQuantity + "WorkoutEffortScore":          q("apple_workout_effort", catalog.Interval, "appleEffortScore", "index"),
	hkQuantity + "EstimatedWorkoutEffortScore": q("apple_workout_effort_estimated", catalog.Interval, "appleEffortScore", "index"),
}

// insulinReasonKey is the metadata key of an insulin dose's HKInsulinDeliveryReason; insulinReasons
// maps its value to the dose's code. A dose without a known reason is refused.
const insulinReasonKey = "HKInsulinDeliveryReason"

var insulinReasons = map[int]string{1: "insulin_basal", 2: "insulin_bolus"}

// event maps one HealthKit category type to a catalogue event code; levels maps the raw
// category value to the level word (none: the type's only value is notApplicable).
type event struct {
	code   string
	levels map[int]string
}

var events = func() map[string]event {
	m := map[string]event{
		hkCategory + "IrregularHeartRhythmEvent": {code: "irregular_rhythm_alert"},
		hkCategory + "HandwashingEvent":          {code: "handwashing"},
		hkCategory + "MindfulSession":            {code: "mindful_session"},
		// Cycle tracking (HKCategoryValues.h): HKCategoryValueVaginalBleeding 1 to 5 and the others below.
		hkCategory + "MenstrualFlow":                    {code: "menstrual_flow", levels: levelsFrom(1, catalog.VaginalBleeding)},
		hkCategory + "BleedingAfterPregnancy":           {code: "bleeding_after_pregnancy", levels: levelsFrom(1, catalog.VaginalBleeding)},
		hkCategory + "BleedingDuringPregnancy":          {code: "bleeding_during_pregnancy", levels: levelsFrom(1, catalog.VaginalBleeding)},
		hkCategory + "BleedingAfterMenopause":           {code: "bleeding_after_menopause", levels: levelsFrom(1, catalog.VaginalBleeding)},
		hkCategory + "IntermenstrualBleeding":           {code: "intermenstrual_bleeding"},
		hkCategory + "SexualActivity":                   {code: "sexual_activity"},
		hkCategory + "Pregnancy":                        {code: "pregnancy"},
		hkCategory + "Lactation":                        {code: "lactation"},
		hkCategory + "CervicalMucusQuality":             {code: "cervical_mucus", levels: levelsFrom(1, []string{"dry", "sticky", "creamy", "watery", "egg_white"})},
		hkCategory + "OvulationTestResult":              {code: "ovulation_test", levels: levelsFrom(1, []string{"negative", "lh_surge", "indeterminate", "estrogen_surge"})},
		hkCategory + "PregnancyTestResult":              {code: "pregnancy_test", levels: levelsFrom(1, catalog.TestResults)},
		hkCategory + "ProgesteroneTestResult":           {code: "progesterone_test", levels: levelsFrom(1, catalog.TestResults)},
		hkCategory + "Contraceptive":                    {code: "contraceptive", levels: levelsFrom(1, []string{"unspecified", "implant", "injection", "intrauterine_device", "intravaginal_ring", "oral", "patch"})},
		hkCategory + "MenopausalState":                  {code: "menopausal_state", levels: levelsFrom(1, []string{"menopause", "perimenopause", "none"})},
		hkCategory + "IrregularMenstrualCycles":         {code: "irregular_cycles_alert"},
		hkCategory + "InfrequentMenstrualCycles":        {code: "infrequent_cycles_alert"},
		hkCategory + "ProlongedMenstrualPeriods":        {code: "prolonged_periods_alert"},
		hkCategory + "PersistentIntermenstrualBleeding": {code: "persistent_intermenstrual_bleeding_alert"},
	}
	// Symptoms: HKCategoryValueSeverity, Presence and AppetiteChanges all start at 0.
	for _, s := range catalog.Symptoms {
		m[hkCategory+s.HK] = event{code: "symptom_" + s.Name, levels: levelsFrom(0, catalog.SymptomLevels(s.Name))}
	}
	maps.Copy(m, v1Events)
	return m
}()

// levelsFrom maps consecutive raw category values from first to the level words.
func levelsFrom(first int, words []string) map[int]string {
	m := make(map[int]string, len(words))
	for i, w := range words {
		m[first+i] = w
	}
	return m
}

// v1Events are the categories of the type registry v1.
var v1Events = map[string]event{
	hkCategory + "HighHeartRateEvent":    {code: "high_heart_rate_alert"},
	hkCategory + "LowHeartRateEvent":     {code: "low_heart_rate_alert"},
	hkCategory + "LowCardioFitnessEvent": {code: "low_cardio_fitness_alert"},
	hkCategory + "HypertensionEvent":     {code: "hypertension_alert"},
	hkCategory + "SleepApneaEvent":       {code: "sleep_apnea_alert"},
	// HKCategoryValueAppleWalkingSteadinessEvent.
	hkCategory + "AppleWalkingSteadinessEvent": {code: "walking_steadiness_alert",
		levels: map[int]string{1: "initial_low", 2: "initial_very_low", 3: "repeat_low", 4: "repeat_very_low"}},
	// The raw value of .environmentalAudioExposureEvent is the older AudioExposureEvent identifier.
	hkCategory + "AudioExposureEvent":          {code: "environment_audio_alert", levels: map[int]string{1: "momentary_limit"}},
	hkCategory + "HeadphoneAudioExposureEvent": {code: "headphone_audio_alert", levels: map[int]string{1: "seven_day_limit"}},
}

// sleepStages maps HKCategoryValueSleepAnalysis to sleep_stages.stage.
var sleepStages = map[int]string{0: "in_bed", 1: "asleep_unspecified", 2: "awake", 3: "light", 4: "deep", 5: "rem"}

// standHour maps HKCategoryValueAppleStandHour (0 stood, 1 idle) to the stand_hours value.
var standHour = map[int]float64{0: 1, 1: 0}

// sports maps HKWorkoutActivityType raw values to their case names. The canonical sport is the
// case name in snake case; 3000 (other) and unknown values are "other".
var sports = map[int]string{
	1: "americanFootball", 2: "archery", 3: "australianFootball", 4: "badminton", 5: "baseball", 6: "basketball",
	7: "bowling", 8: "boxing", 9: "climbing", 10: "cricket", 11: "crossTraining", 12: "curling", 13: "cycling",
	14: "dance", 15: "danceInspiredTraining", 16: "elliptical", 17: "equestrianSports", 18: "fencing", 19: "fishing",
	20: "functionalStrengthTraining", 21: "golf", 22: "gymnastics", 23: "handball", 24: "hiking", 25: "hockey",
	26: "hunting", 27: "lacrosse", 28: "martialArts", 29: "mindAndBody", 30: "mixedMetabolicCardioTraining",
	31: "paddleSports", 32: "play", 33: "preparationAndRecovery", 34: "racquetball", 35: "rowing", 36: "rugby",
	37: "running", 38: "sailing", 39: "skatingSports", 40: "snowSports", 41: "soccer", 42: "softball", 43: "squash",
	44: "stairClimbing", 45: "surfingSports", 46: "swimming", 47: "tableTennis", 48: "tennis", 49: "trackAndField",
	50: "traditionalStrengthTraining", 51: "volleyball", 52: "walking", 53: "waterFitness", 54: "waterPolo",
	55: "waterSports", 56: "wrestling", 57: "yoga", 58: "barre", 59: "coreTraining", 60: "crossCountrySkiing",
	61: "downhillSkiing", 62: "flexibility", 63: "highIntensityIntervalTraining", 64: "jumpRope", 65: "kickboxing",
	66: "pilates", 67: "snowboarding", 68: "stairs", 69: "stepTraining", 70: "wheelchairWalkPace",
	71: "wheelchairRunPace", 72: "taiChi", 73: "mixedCardio", 74: "handCycling", 75: "discSports",
	76: "fitnessGaming", 77: "cardioDance", 78: "socialDance", 79: "pickleball", 80: "cooldown", 82: "swimBikeRun",
	83: "transition", 84: "underwaterDiving", 3000: "other",
}

// sport returns the canonical and provider sport of an activity type; ok is false when unknown.
func sport(activityType int) (canonical, provider string, ok bool) {
	name, ok := sports[activityType]
	if !ok {
		return "other", "hk_activity_" + strconv.Itoa(activityType), false
	}
	var b strings.Builder
	for _, r := range name {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String(), name, true
}

// workoutDistances are the workout totals that are a distance (m); one is set per workout.
var workoutDistances = []string{hkQuantity + "DistanceWalkingRunning", hkQuantity + "DistanceCycling",
	hkQuantity + "DistanceSwimming", hkQuantity + "DistanceWheelchair"}

// deviceTypes are matched in order against the words of an HKDevice's name, model and
// manufacturer; the first type with a matching word or phrase wins. Vocabulary:
// docs/architecture/data-model.md (devices.device_type).
var deviceTypes = []struct {
	typ   string
	words []string
}{
	{"chest_strap", []string{"h7", "h9", "h10", "hrm", "hrm pro", "hrm dual", "tickr", "chest strap"}},
	{"arm_band", []string{"verity sense", "oh1", "armband", "arm band"}},
	{"ring", []string{"ring", "oura", "ringconn", "ultrahuman"}},
	{"scale", []string{"scale", "body cardio", "body comp", "body scan", "bodyscan", "body smart"}},
	{"bp_monitor", []string{"bpm", "blood pressure", "bp monitor", "omron"}},
	{"cgm", []string{"cgm", "dexcom", "libre"}},
	{"glucose_meter", []string{"glucometer", "glucose meter", "contour", "accu chek"}},
	{"band", []string{"band", "whoop"}},
	{"watch", []string{"watch", "forerunner", "fenix", "epix", "venu", "vivoactive", "instinct", "enduro", "vantage", "pacer", "suunto", "coros"}},
	{"phone", []string{"iphone", "phone"}},
}

// deviceType derives devices.device_type; "" when nothing matches. Apple hardware ids
// (Watch7,1, iPhone17,1) decide first.
func deviceType(d hkDevice) string {
	hw := strings.ToLower(d.HardwareVersion)
	switch {
	case strings.HasPrefix(hw, "watch"):
		return "watch"
	case strings.HasPrefix(hw, "iphone"):
		return "phone"
	case strings.HasPrefix(hw, "ipad"):
		return "other"
	}
	text := " " + strings.Join(strings.FieldsFunc(strings.ToLower(d.Name+" "+d.Model+" "+d.Manufacturer), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}), " ") + " "
	for _, t := range deviceTypes {
		for _, w := range t.words {
			if strings.Contains(text, " "+w+" ") {
				return t.typ
			}
		}
	}
	return ""
}
