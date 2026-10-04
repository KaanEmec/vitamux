package catalog

import "slices"

// Event is a health_events code (docs/architecture/metric-catalog.md#events): a typed event with
// an optional level or value, listed for rules and APIs but never resolved like a metric.
type Event struct {
	Code   string
	Levels []string // allowed level words; none means the event carries no level
	HK     string   // HealthKit identifier without prefix; documentation only
}

// events is every implemented event code, in docs order.
var events = []Event{
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
