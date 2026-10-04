package applehealth

import (
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

// quantities is the HK column of docs/metrics.md for the type registry v1. Percent types arrive
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
}

// event maps one HealthKit category type to a catalogue event code; levels maps the raw
// category value to the level word (none: the type's only value is notApplicable).
type event struct {
	code   string
	levels map[int]string
}

var events = map[string]event{
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
