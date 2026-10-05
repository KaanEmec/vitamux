package catalog

import "slices"

// Event is a health_events code (docs/architecture/metric-catalog.md#events): a typed event with
// an optional level or value, listed for rules and APIs but never resolved like a metric.
type Event struct {
	Code   string
	Levels []string // allowed level words; none means the event carries no level
	HK     string   // HealthKit identifier without prefix; documentation only
	// File is the format of the blob document an event row references (health_events.file_blob_sha256).
	File string
}

// Formats of the event blob documents (ADR-0024), served by GET /events/{id}/waveform and
// GET /workouts/{id}/route.
const (
	FileWaveform = "vitamux.waveform/1"
	FileRoute    = "vitamux.route/1"
)

// events is every implemented event code, in docs order.
var events = slices.Concat(baseEvents, symptomEvents())

var baseEvents = []Event{
	{Code: "high_heart_rate_alert", HK: "HighHeartRateEvent"},
	{Code: "low_heart_rate_alert", HK: "LowHeartRateEvent"},
	{Code: "low_cardio_fitness_alert", HK: "LowCardioFitnessEvent"},
	{Code: "hypertension_alert", HK: "HypertensionEvent"},
	{Code: "sleep_apnea_alert", HK: "SleepApneaEvent"},
	{Code: "walking_steadiness_alert", HK: "AppleWalkingSteadinessEvent",
		Levels: []string{"initial_low", "initial_very_low", "repeat_low", "repeat_very_low"}},
	{Code: "environment_audio_alert", HK: "AudioExposureEvent", Levels: []string{"momentary_limit"}},
	{Code: "headphone_audio_alert", HK: "HeadphoneAudioExposureEvent", Levels: []string{"seven_day_limit"}},
	{Code: "afib_ecg_result", Levels: AfibCategories},
	{Code: "afib_ppg_result", Levels: AfibCategories},

	// Apple Watch (E22, J22.17; ADR-0024).
	{Code: "ecg_recording", HK: "Electrocardiogram (data type)", File: FileWaveform, Levels: []string{"not_set", "sinus_rhythm",
		"atrial_fibrillation", "inconclusive_low_heart_rate", "inconclusive_high_heart_rate", "inconclusive_poor_reading",
		"inconclusive_other", "unrecognized"}},
	{Code: "irregular_rhythm_alert", HK: "IrregularHeartRhythmEvent"},
	{Code: "workout_route", HK: "WorkoutRoute (series type)", File: FileRoute},
	{Code: "handwashing", HK: "HandwashingEvent"},
	{Code: "mindful_session", HK: "MindfulSession"},
	{Code: "state_of_mind", HK: "StateOfMind (data type)", Levels: []string{"momentary_emotion", "daily_mood"}},
	{Code: "menstrual_flow", HK: "MenstrualFlow", Levels: VaginalBleeding},
	{Code: "bleeding_after_pregnancy", HK: "BleedingAfterPregnancy", Levels: VaginalBleeding},
	{Code: "bleeding_during_pregnancy", HK: "BleedingDuringPregnancy", Levels: VaginalBleeding},
	{Code: "bleeding_after_menopause", HK: "BleedingAfterMenopause", Levels: VaginalBleeding},
	{Code: "intermenstrual_bleeding", HK: "IntermenstrualBleeding"},
	{Code: "sexual_activity", HK: "SexualActivity"},
	{Code: "pregnancy", HK: "Pregnancy"},
	{Code: "lactation", HK: "Lactation"},
	{Code: "cervical_mucus", HK: "CervicalMucusQuality", Levels: []string{"dry", "sticky", "creamy", "watery", "egg_white"}},
	{Code: "ovulation_test", HK: "OvulationTestResult", Levels: []string{"negative", "lh_surge", "indeterminate", "estrogen_surge"}},
	{Code: "pregnancy_test", HK: "PregnancyTestResult", Levels: TestResults},
	{Code: "progesterone_test", HK: "ProgesteroneTestResult", Levels: TestResults},
	{Code: "contraceptive", HK: "Contraceptive", Levels: []string{"unspecified", "implant", "injection", "intrauterine_device",
		"intravaginal_ring", "oral", "patch"}},
	{Code: "menopausal_state", HK: "MenopausalState", Levels: []string{"menopause", "perimenopause", "none"}},
	{Code: "irregular_cycles_alert", HK: "IrregularMenstrualCycles"},
	{Code: "infrequent_cycles_alert", HK: "InfrequentMenstrualCycles"},
	{Code: "prolonged_periods_alert", HK: "ProlongedMenstrualPeriods"},
	{Code: "persistent_intermenstrual_bleeding_alert", HK: "PersistentIntermenstrualBleeding"},
}

// Level words of the Apple Watch event families (ADR-0024; HKCategoryValues.h).
var (
	// VaginalBleeding: HKCategoryValueVaginalBleeding 1 to 5.
	VaginalBleeding = []string{"unspecified", "light", "medium", "heavy", "none"}
	// TestResults: HKCategoryValuePregnancyTestResult and ProgesteroneTestResult 1 to 3.
	TestResults = []string{"negative", "positive", "indeterminate"}
	// Severity: HKCategoryValueSeverity 0 to 4.
	Severity = []string{"unspecified", "not_present", "mild", "moderate", "severe"}
	// Presence: HKCategoryValuePresence 0 present, 1 not present.
	Presence = []string{"present", "not_present"}
	// AppetiteChanges: HKCategoryValueAppetiteChanges 0 to 3.
	AppetiteChanges = []string{"unspecified", "no_change", "decreased", "increased"}
)

// Symptoms are the HealthKit symptom categories (HK id → symptom_<name>), in docs order.
var Symptoms = []struct{ HK, Name string }{
	{"AbdominalCramps", "abdominal_cramps"}, {"Acne", "acne"}, {"AppetiteChanges", "appetite_changes"},
	{"BladderIncontinence", "bladder_incontinence"}, {"Bloating", "bloating"}, {"BreastPain", "breast_pain"},
	{"ChestTightnessOrPain", "chest_tightness_or_pain"}, {"Chills", "chills"}, {"Constipation", "constipation"},
	{"Coughing", "coughing"}, {"Diarrhea", "diarrhea"}, {"Dizziness", "dizziness"}, {"DrySkin", "dry_skin"},
	{"Fainting", "fainting"}, {"Fatigue", "fatigue"}, {"Fever", "fever"}, {"GeneralizedBodyAche", "generalized_body_ache"},
	{"HairLoss", "hair_loss"}, {"Headache", "headache"}, {"Heartburn", "heartburn"}, {"HotFlashes", "hot_flashes"},
	{"LossOfSmell", "loss_of_smell"}, {"LossOfTaste", "loss_of_taste"}, {"LowerBackPain", "lower_back_pain"},
	{"MemoryLapse", "memory_lapse"}, {"MoodChanges", "mood_changes"}, {"Nausea", "nausea"}, {"NightSweats", "night_sweats"},
	{"PelvicPain", "pelvic_pain"}, {"RapidPoundingOrFlutteringHeartbeat", "rapid_pounding_or_fluttering_heartbeat"},
	{"RunnyNose", "runny_nose"}, {"ShortnessOfBreath", "shortness_of_breath"}, {"SinusCongestion", "sinus_congestion"},
	{"SkippedHeartbeat", "skipped_heartbeat"}, {"SleepChanges", "sleep_changes"}, {"SoreThroat", "sore_throat"},
	{"VaginalDryness", "vaginal_dryness"}, {"Vomiting", "vomiting"}, {"Wheezing", "wheezing"},
}

// SymptomLevels returns the level words of a symptom: presence for mood and sleep changes, the
// appetite scale for appetite changes, severity for the rest.
func SymptomLevels(name string) []string {
	switch name {
	case "mood_changes", "sleep_changes":
		return Presence
	case "appetite_changes":
		return AppetiteChanges
	}
	return Severity
}

func symptomEvents() []Event {
	out := make([]Event, len(Symptoms))
	for i, s := range Symptoms {
		out[i] = Event{Code: "symptom_" + s.Name, HK: s.HK, Levels: SymptomLevels(s.Name)}
	}
	return out
}

// AfibCategories are the words of the Withings atrial fibrillation categories 0 to 13, the levels
// of afib_ecg_result and afib_ppg_result.
var AfibCategories = []string{"negative", "positive", "inconclusive", "no_signal", "other", "noise", "low_heart_rate",
	"high_heart_rate", "inconclusive_us", "negative_normal_hr", "negative_high_hr", "positive_normal_hr", "positive_high_hr", "no_diagnosis"}

// Events returns every event in docs order.
func Events() []Event { return slices.Clone(events) }

// LookupEvent finds an event by code.
func LookupEvent(code string) (Event, bool) {
	i := slices.IndexFunc(events, func(e Event) bool { return e.Code == code })
	if i < 0 {
		return Event{}, false
	}
	return events[i], true
}

// AllowsLevel reports whether level ("" for none) is valid for the event.
func (e Event) AllowsLevel(level string) bool { return level == "" || slices.Contains(e.Levels, level) }
