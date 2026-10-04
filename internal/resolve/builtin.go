package resolve

import (
	"fmt"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

//go:generate go run ./gen

// Built-in default rules (J09.2) are the suggested ladders of
// docs/architecture/resolution-defaults.md in rule form. A built-in applies while the owner has
// no rule of their own for the metric; the first edit copies it into the owner's version 1
// (Store.Create), after which it is an ordinary rule. Nothing requires a built-in to exist.
//
// A change to a built-in adds a new Version instead of editing the old one (review policy).
// docs/resolution-defaults.md is generated from this file: go run ./internal/resolve/gen.

// Builtin is one built-in rule version, referenced as builtin:<metric>:<version>.
type Builtin struct {
	Rule    Rule
	Version int
	Why     string // one line for the generated doc
}

// Ref returns the built-in's rule reference.
func (b Builtin) Ref() string { return BuiltinRef(b.Rule.Metric, b.Version) }

// BuiltinRef formats a built-in rule reference.
func BuiltinRef(metric string, version int) string {
	return fmt.Sprintf("builtin:%s:%d", metric, version)
}

// NoBuiltin lists the catalogue codes that ship without a built-in, with the reason. The
// default rule (default.go) resolves them.
var NoBuiltin = func() map[string]string {
	m := map[string]string{
		"skin_temperature": "the value depends on where the device is worn, so there is no neutral order: uses the default rule",
	}
	// The Apple Health bridge codes (J15.2) have no researched ladder yet.
	for _, code := range []string{"distance_cycling", "distance_swimming", "distance_wheelchair", "floors_climbed",
		"basal_energy", "exercise_time", "stand_time", "stand_hours", "walking_heart_rate", "breathing_disturbances",
		"wrist_temperature_sleeping", "basal_body_temperature", "waist_circumference", "blood_glucose", "diet_energy",
		"diet_protein", "diet_carbohydrate", "diet_fat_total", "diet_fat_saturated", "diet_fat_monounsaturated",
		"diet_fat_polyunsaturated", "diet_fiber", "diet_sugar", "diet_cholesterol", "diet_water", "diet_caffeine",
		"walking_steadiness", "walking_asymmetry", "walking_double_support", "walking_step_length"} {
		m[code] = "added with the Apple Health bridge (J15.2); no researched ladder: uses the default rule"
	}
	// Garmin and WHOOP scores (J18.4, J19.4) are provider-scoped: one source each, nothing to order.
	for _, code := range []string{"garmin_stress", "garmin_body_battery", "garmin_training_readiness", "garmin_sleep_score",
		"whoop_recovery", "whoop_strain", "whoop_sleep_performance", "whoop_sleep_need", "whoop_sleep_debt",
		"whoop_sleep_consistency", "whoop_sleep_disturbances", "whoop_max_heart_rate", "garmin_body_battery_charged",
		"garmin_body_battery_drained", "garmin_fitness_age", "garmin_acute_load", "garmin_chronic_load", "withings_sleep_score",
		"withings_breathing_quality", "withings_nerve_health_score", "withings_nerve_response_score", "withings_metabolic_age",
		"withings_esc"} {
		m[code] = "a provider-scoped score with a single source, so there is nothing to order: uses the default rule, which takes that provider alone"
	}
	// The catalogue seed of J25.1 (E25) adds the codes the mapping corrections and the Apple Health type registry need.
	for _, code := range []string{"elevation_gain", "intensity_light_time", "intensity_moderate_time", "intensity_vigorous_time",
		"sedentary_time", "distance_rowing", "distance_paddle", "distance_skating", "distance_xc_ski", "distance_downhill_snow",
		"move_time", "daylight_time", "wheelchair_pushes", "swim_strokes", "speed_walking", "speed_running", "speed_cycling",
		"speed_rowing", "speed_paddle", "cadence_cycling", "power_running", "power_cycling", "ftp_cycling", "running_stride_length",
		"running_vertical_oscillation", "running_ground_contact_time", "physical_effort", "sleeping_heart_rate",
		"heart_rate_recovery_1min", "afib_burden", "perfusion_index", "ecg_qrs", "ecg_pr", "ecg_qt", "ecg_qtc", "spo2_nightly",
		"respiratory_rate_nightly", "apnea_hypopnea_index", "fev1", "fvc", "peak_expiratory_flow", "inhaler_uses",
		"skin_temperature_nightly", "sleep_temperature_deviation", "insulin_basal", "insulin_bolus", "blood_alcohol",
		"alcoholic_drinks", "stair_ascent_speed", "stair_descent_speed", "six_minute_walk_distance", "falls",
		"environment_audio_exposure", "headphone_audio_exposure", "environment_sound_reduction", "uv_exposure",
		"water_temperature", "underwater_depth", "electrodermal_activity", "sleep_awakenings", "sleep_snoring_time",
		"sleep_snoring_episodes"} {
		m[code] = "added with the mapping corrections (J25.1); no researched ladder: uses the default rule"
	}
	// The WHOOP workout, sleep-need and elevation codes of J25.4: provider-scoped with one source each, or added without a ladder.
	for _, code := range []string{"whoop_workout_strain", "whoop_hr_zone_0_time", "whoop_hr_zone_1_time", "whoop_hr_zone_2_time",
		"whoop_hr_zone_3_time", "whoop_hr_zone_4_time", "whoop_hr_zone_5_time", "whoop_sleep_debt_post", "whoop_sleep_need_habitual",
		"whoop_sleep_need_from_strain", "whoop_sleep_nap_credit", "whoop_sleep_cycles"} {
		m[code] = "a provider-scoped value with a single source, so there is nothing to order: uses the default rule, which takes that provider alone"
	}
	m["elevation_change"] = "added with the WHOOP mappings (J25.4); no researched ladder: uses the default rule"
	// The Garmin values of J25.5 are provider-scoped: one source each, nothing to order.
	for _, code := range []string{"garmin_hr_zone_1_time", "garmin_hr_zone_2_time", "garmin_hr_zone_3_time", "garmin_hr_zone_4_time",
		"garmin_hr_zone_5_time", "garmin_training_effect_aerobic", "garmin_training_effect_anaerobic", "garmin_activity_training_load",
		"garmin_activity_moving_time", "garmin_floors_descended", "garmin_chronic_load_low", "garmin_chronic_load_high",
		"garmin_recovery_time", "garmin_vo2max_cycling", "garmin_hrv_baseline_low", "garmin_hrv_baseline_high",
		"garmin_hrv_baseline_floor", "garmin_achievable_fitness_age",
		"garmin_sweat_loss", "garmin_sleep_movement"} {
		m[code] = "a provider-scoped value with a single source, so there is nothing to order: uses the default rule, which takes that provider alone"
	}
	return m
}()

// Builtins returns the latest version of every built-in, in catalogue order. Each call builds
// fresh values, so callers may modify the result.
func Builtins() []Builtin { return builtins() }

// LookupBuiltin returns the latest built-in for a metric code or rule family.
func LookupBuiltin(metric string) (Builtin, bool) {
	for _, b := range builtins() {
		if b.Rule.Metric == metric {
			return b, true
		}
	}
	return Builtin{}, false
}

// Provider codes of connectors that do not exist yet. A group naming one stays empty until
// the connector lands, and the ladder starts at the next group.
const (
	provApple    = "apple_health"
	provGarmin   = "garmin"
	provOura     = "oura"
	provWithings = "withings"
	provWhoop    = "whoop"
	provPolar    = "polar"
	provFitbit   = "fitbit"
	provSamsung  = "samsung"
	provXiaomi   = "xiaomi"
	provAmazfit  = "amazfit"
)

// appleOrigin prefixes the origin keys of Apple's own apps and devices in HealthKit.
const appleOrigin = "com.apple.health"

// relayOrigins are the Apple Health bundle ids of brand apps that relay into HealthKit: the
// known_relay_origins seed of migrations 00003 and 00026. J15.1 replaces them with the verified
// named origin sets; a brand without an entry has only its direct group.
var relayOrigins = map[string][]string{
	provGarmin:   {"com.garmin.connect.mobile"},
	provOura:     {"com.ouraring.oura"},
	provWithings: {"com.withings.wiScaleNG"},
	provWhoop:    {"com.whoop.iphone"},
	provPolar:    {"fi.polar.polarflow"},
	provFitbit:   {"com.fitbit.FitbitMobile"},
}

// relayManufacturers are the HealthKit device manufacturer strings verified for a brand. Its
// relay group also takes the brand's devices that another app writes into Apple Health.
var relayManufacturers = map[string]string{
	provGarmin:   "Garmin",
	provWithings: "Withings",
}

// appleInc is the HealthKit manufacturer of Apple's own devices.
const appleInc = "Apple Inc."

// biGroup returns one group.
func biGroup(id string, match ...Selector) []Group { return []Group{{ID: id, Match: match}} }

// biDevice is a group of one device type, measured rather than typed in.
func biDevice(deviceType string) []Group {
	return biGroup(deviceType, Selector{DeviceType: deviceType, Entry: EntryDevice})
}

// biWorn is a device-type group of directly synced wearables, followed by <type>_relayed with
// the copies a brand app relays into Apple Health (a Garmin watch through Garmin Connect). The
// direct device wins whenever it has a valid value, so a watch that is both connected and relayed
// is not counted twice: in one group the per-bucket max took the larger of two copies with
// different interval shapes, which inflated the day.
func biWorn(deviceType string) []Group {
	direct, relayed := false, true
	return []Group{
		{ID: deviceType, Match: []Selector{{DeviceType: deviceType, Entry: EntryDevice, Relayed: &direct}}},
		{ID: deviceType + "_relayed", Match: []Selector{{DeviceType: deviceType, Entry: EntryDevice, Relayed: &relayed}}},
	}
}

// biBrand is a brand's direct-connector group, followed by its Apple Health relay group when the
// brand's app relays into HealthKit. The direct path wins whenever it has a valid value; the
// relayed copy is used only when it has none (resolution-defaults.md, relayed origins).
func biBrand(provider string) []Group {
	out := biDirect(provider)
	if keys := relayOrigins[provider]; len(keys) > 0 {
		var sels []Selector
		for _, k := range keys {
			sels = append(sels, Selector{Provider: provApple, OriginKey: k})
		}
		if m := relayManufacturers[provider]; m != "" {
			sels = append(sels, Selector{Provider: provApple, DeviceManufacturer: m})
		}
		out = append(out, Group{ID: provider + "_apple", Match: sels})
	}
	return out
}

// biDirect is a brand's direct-connector group alone, for metrics its app does not relay.
func biDirect(provider string) []Group { return biGroup(provider, Selector{Provider: provider}) }

// biApple is Apple's own data (Watch and iPhone); biAppleDevice one Apple device model, so
// "watch" means an Apple Watch whatever type the owner gave it.
func biApple() []Group {
	return biGroup("apple", Selector{Provider: provApple, OriginKeyPrefix: appleOrigin})
}
func biAppleDevice(id, model string) []Group {
	return biGroup(id, Selector{Provider: provApple, OriginKeyPrefix: appleOrigin, DeviceManufacturer: appleInc, DeviceModel: model})
}

func biManual() []Group { return biGroup("manual", Selector{Entry: EntryManual}) }

func biLadder(parts ...[]Group) []Group {
	var out []Group
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// biV1 wraps a schema-v1 rule as version 1 of its built-in.
func biV1(why string, r Rule) Builtin { return biV(1, why, r) }

// biV wraps a schema-v1 rule as version n of its built-in. Version history:
//   - steps, distance_walk_run, active_energy v2: relayed wearables in their own groups (biWorn);
//     v3: the iPhone before other phones; v4: no coverage or wear gates (opt-in, J24.2).
//   - heart_rate v3: WHOOP directly above Garmin (owner decision, J24.4).
//   - resting_heart_rate_nocturnal v3: no coverage gate, and the v3 heart-rate order.
//   - sleep v3: no episode coverage gate.
//   - every other n=2: Apple devices by manufacturer and model (biAppleDevice), relay groups for
//     WHOOP, Polar and Fitbit, and manufacturer selectors in the Garmin and Withings relay groups.
func biV(n int, why string, r Rule) Builtin {
	r.Schema = SchemaV1
	return Builtin{Rule: r, Version: n, Why: why}
}

func builtins() []Builtin {
	firstAvailable := Strategy{Op: OpFirstAvailable}
	day := RuleWindow{Kind: catalog.WindowLocalDay}
	night := RuleWindow{Kind: catalog.WindowLocalNight}
	latest := RuleWindow{Kind: catalog.WindowLatest}
	hourly := func() *Compose { return &Compose{From: catalog.WindowHour, Op: ComposeFirstAvailable} }
	iphone := func() []Group { return biAppleDevice("iphone", "iPhone") }
	// Only Oura and Garmin relay HRV into Apple Health; the others keep their direct group alone.
	rmssd := func() []Group {
		return biLadder(biBrand(provOura), biDirect(provWhoop), biBrand(provGarmin), biDirect(provPolar), biDirect(provFitbit), biDirect(provSamsung))
	}
	hrGroups := func() []Group {
		return biLadder(biDevice("chest_strap"), biDevice("arm_band"), biAppleDevice("apple_watch", "Watch"), biBrand(provWhoop), biBrand(provGarmin), biBrand(provFitbit),
			biBrand(provSamsung), biBrand(provPolar), biBrand(provXiaomi), biBrand(provAmazfit), biBrand(provOura))
	}
	hrQuality := func() *Quality {
		return &Quality{PlausibleRange: []float64{25, 230}, ExcludeFlags: []string{"manual_entry"}}
	}
	spo2Groups := func() []Group {
		return biLadder(biApple(), biBrand(provSamsung), biBrand(provWithings), biBrand(provGarmin), biBrand(provFitbit), biBrand(provOura), biBrand(provWhoop))
	}
	weightGroups := func() []Group {
		return biLadder(biDevice("scale"), biGroup("scale_apps", Selector{Provider: provApple, Entry: EntryDevice}), biManual())
	}
	spot := func(metric, why string) Builtin { // spot readings any source may take: the newest wins
		return biV1(why, Rule{Metric: metric, Window: latest, Strategy: Strategy{Op: OpLatest},
			Groups: biLadder(biGroup("device", Selector{Entry: EntryDevice}), biManual())})
	}
	withingsOnly := func(metric string) Builtin { // v2: the shared withings_apple group changed
		return biV(2, "Only Withings reports this measure; other sources join the ladder when they exist.",
			Rule{Metric: metric, Window: latest, Strategy: firstAvailable, Groups: biBrand(provWithings)})
	}

	out := []Builtin{
		biV(4, "Watch, ring, band, iPhone, other phones, each direct before relayed; hours resolve separately so a watch left on the charger falls back to the phone for those hours only. Wear and coverage gates are opt-in.",
			Rule{Metric: "steps", Window: day, Strategy: firstAvailable, Compose: hourly(),
				Groups: biLadder(biWorn("watch"), biWorn("ring"), biWorn("band"), iphone(), biDevice("phone"))}),
		biV(4, "Follows the step source; inside a workout, the device that recorded it.",
			Rule{Metric: "distance_walk_run", Window: day, Strategy: firstAvailable, Compose: hourly(),
				Groups:   biLadder(biWorn("watch"), iphone(), biDevice("phone"), biWorn("ring")),
				Contexts: map[Context][]string{ContextWorkout: {ContextWorkoutSource}}}),
		biV(4, "Every device is far off; one source per day keeps days comparable.",
			Rule{Metric: "active_energy", Window: day, Strategy: firstAvailable,
				Groups: biLadder(biWorn("watch"), biWorn("band"), biWorn("ring"), iphone(), biDevice("phone"))}),
		biV1("Follows the active energy source; only reported totals are stored, never active plus basal.",
			Rule{Metric: "total_energy", Window: day, Strategy: firstAvailable, Follow: "active_energy",
				Groups: biLadder(biWorn("watch"), biWorn("band"), biWorn("ring"), iphone(), biDevice("phone"))}),
		biV(3, "Chest straps are ECG-class, then WHOOP (owner decision) and wrist devices by independent validation; inside workouts the recording device follows the straps.",
			Rule{Metric: "heart_rate", Window: RuleWindow{Kind: catalog.WindowBucket, Size: "5m"}, Strategy: firstAvailable,
				Quality: hrQuality(), Groups: hrGroups(),
				Contexts: map[Context][]string{ContextWorkout: {"chest_strap", "arm_band", ContextWorkoutSource}}}),
		biV(2, "Selection only (definitions differ); ranked by nightly error against a chest strap.",
			Rule{Metric: "resting_heart_rate", Window: day, Strategy: firstAvailable, Quality: &Quality{MaxStaleness: "36h"},
				Groups: biLadder(biBrand(provOura), biBrand(provWhoop), biBrand(provPolar), biApple(), biBrand(provGarmin), biBrand(provFitbit), biBrand(provSamsung))}),
		biV1("Spot SDNN is its own method and comes from Apple only; never mixed with RMSSD.",
			Rule{Metric: "hrv_sdnn", Window: day, Strategy: Strategy{Op: OpSingleSource}, Groups: biApple()}),
		biV(2, "For sources that send 5-minute samples; never combined with hrv_rmssd_nightly.",
			Rule{Metric: "hrv_rmssd", Window: night, Strategy: firstAvailable, Groups: rmssd()}),
		biV(2, "Selection only (overnight windows differ); ranked by agreement with ECG.",
			Rule{Metric: "hrv_rmssd_nightly", Window: day, Strategy: firstAvailable, Quality: &Quality{MaxStaleness: "36h"}, Groups: rmssd()}),
		biV(2, "Ranked by published error against lab tests; older than 30 days counts as stale.",
			Rule{Metric: "vo2max", Window: latest, Strategy: firstAvailable, Quality: &Quality{MaxStaleness: "30d"},
				Groups: biLadder(biBrand(provGarmin), biApple(), biBrand(provPolar), biBrand(provSamsung))}),
		withingsOnly("pulse_wave_velocity"),
		withingsOnly("vascular_age"),
		biV1("Validated cuffs first, then watches with a micro-cuff, then manual entries; one reading never mixes sources, and a day is the mean of its readings.",
			Rule{Metric: FamilyBloodPressure, Window: day, Strategy: firstAvailable, WithinSource: &WithinSource{Statistic: StatMean},
				Groups: biLadder(biDevice("bp_monitor"), biGroup("watch_cuff", Selector{DeviceType: "watch", Entry: EntryDevice}), biManual())}),
		biV(2, "Nightly mean, ranked by published error against reference oximetry; ring values are a trend only.",
			Rule{Metric: "spo2", Window: night, Strategy: firstAvailable, Groups: spo2Groups()}),
		biV(2, "All published evidence is vendor-funded; low confidence across the board.",
			Rule{Metric: "respiratory_rate", Window: night, Strategy: firstAvailable,
				Groups: biLadder(biBrand(provSamsung), biBrand(provOura), biBrand(provWhoop), biApple(), biBrand(provFitbit), biBrand(provGarmin))}),
		spot("body_temperature", "Spot readings: the newest measured value wins, else the newest manual entry."),
		biV1("Scales agree closely; the latest reading of the day, from a scale before scale apps and manual entries.",
			Rule{Metric: "weight", Window: day, Strategy: firstAvailable, WithinSource: &WithinSource{Statistic: StatLatest}, Groups: weightGroups()}),
		spot("height", "The newest value from any source."),
		biV(3, "One definition across brands: the lowest 30-minute mean of heart rate in the main sleep episode, from the heart-rate ladder; a coverage gate is opt-in.",
			Rule{Metric: "resting_heart_rate_nocturnal", Window: night, Strategy: firstAvailable,
				WithinSource: &WithinSource{Statistic: StatMinRollingMean, Span: "30m"},
				Quality:      &Quality{PlausibleRange: []float64{25, 230}, ExcludeFlags: []string{"manual_entry"}},
				Groups:       hrGroups()}),
		biV(2, "The lowest 5-minute SpO2 mean in the main sleep episode, from the SpO2 ladder; a failed reading is never 0 %.",
			Rule{Metric: "spo2_night_min", Window: night, Strategy: firstAvailable, WithinSource: &WithinSource{Statistic: StatMin}, Groups: spo2Groups()}),
	}
	for _, m := range catalog.Metrics() {
		if m.Group == "body_composition" && m.Code != "weight" {
			out = append(out, biV1("Same scale as that day's weight (each vendor's body model differs).",
				Rule{Metric: m.Code, Window: day, Strategy: firstAvailable, Follow: "weight", Groups: weightGroups()}))
		}
	}
	return append(out, biV(3, "One night comes from one source, ranked by independent four-stage agreement with PSG; an episode coverage gate is opt-in.",
		Rule{Metric: FamilySleep, Window: night, Strategy: Strategy{Op: OpEventPriority},
			Quality: &Quality{MaxStaleness: "36h", Sleep: &SleepQuality{MatchOverlap: new(0.5)}},
			Groups: biLadder(biBrand(provOura), biAppleDevice("apple_watch", "Watch"), biBrand(provFitbit),
				// withings.measures v1 reported its under-mattress sensor as sleep_monitor.
				biGroup("under_mattress", Selector{DeviceType: "under_mattress"}, Selector{DeviceType: "sleep_monitor"}),
				biBrand(provSamsung), biBrand(provWhoop), biBrand(provGarmin), biBrand(provPolar), biBrand(provXiaomi), biBrand(provAmazfit))}))
}
