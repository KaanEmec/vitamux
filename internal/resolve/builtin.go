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
// owner picks a source at onboarding; until then only the all-sources view shows them.
var NoBuiltin = map[string]string{
	"skin_temperature": "the value depends on where the device is worn, so there is no neutral order; the owner picks one source",
}

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
// known_relay_origins seed of migration 00003. J15.1 replaces them with the verified named
// origin sets; a brand without an entry has only its direct group.
var relayOrigins = map[string][]string{
	provGarmin:   {"com.garmin.connect.mobile"},
	provOura:     {"com.ouraring.oura"},
	provWithings: {"com.withings.wiScaleNG"},
}

// biGroup returns one group.
func biGroup(id string, match ...Selector) []Group { return []Group{{ID: id, Match: match}} }

// biDevice is a group of one device type, measured rather than typed in.
func biDevice(deviceType string) []Group {
	return biGroup(deviceType, Selector{DeviceType: deviceType, Entry: EntryDevice})
}

// biBrand is a brand's direct-connector group, followed by its Apple Health relay group when the
// brand's app relays into HealthKit. The direct path wins whenever it has a valid value; the
// relayed copy is used only when it has none (resolution-defaults.md, relayed origins).
func biBrand(provider string) []Group {
	out := biGroup(provider, Selector{Provider: provider})
	if keys := relayOrigins[provider]; len(keys) > 0 {
		var sels []Selector
		for _, k := range keys {
			sels = append(sels, Selector{Provider: provApple, OriginKey: k})
		}
		out = append(out, Group{ID: provider + "_apple", Match: sels})
	}
	return out
}

// biApple is Apple's own data (Watch and iPhone); biAppleWatch the Watch only.
func biApple() []Group {
	return biGroup("apple", Selector{Provider: provApple, OriginKeyPrefix: appleOrigin})
}
func biAppleWatch() []Group {
	return biGroup("apple_watch", Selector{Provider: provApple, OriginKeyPrefix: appleOrigin, DeviceType: "watch"})
}

func biManual() []Group { return biGroup("manual", Selector{Entry: EntryManual}) }

func biLadder(parts ...[]Group) []Group {
	var out []Group
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func biRatio(f float64) *float64 { return &f }

// biV1 wraps a schema-v1 rule as version 1 of its built-in.
func biV1(why string, r Rule) Builtin {
	r.Schema = SchemaV1
	return Builtin{Rule: r, Version: 1, Why: why}
}

func builtins() []Builtin {
	firstAvailable := Strategy{Op: OpFirstAvailable}
	day := RuleWindow{Kind: catalog.WindowLocalDay}
	night := RuleWindow{Kind: catalog.WindowLocalNight}
	latest := RuleWindow{Kind: catalog.WindowLatest}
	stepGates := func() *Quality { return &Quality{MinCoverage: biRatio(0.6), RequireWear: "heart_rate"} }
	hourly := func() *Compose { return &Compose{From: catalog.WindowHour, Op: ComposeFirstAvailable} }
	rmssd := func() []Group {
		return biLadder(biBrand(provOura), biBrand(provWhoop), biBrand(provGarmin), biBrand(provPolar), biBrand(provFitbit), biBrand(provSamsung))
	}
	weightGroups := func() []Group {
		return biLadder(biDevice("scale"), biGroup("scale_apps", Selector{Provider: provApple, Entry: EntryDevice}), biManual())
	}
	spot := func(metric, why string) Builtin { // spot readings any source may take: the newest wins
		return biV1(why, Rule{Metric: metric, Window: latest, Strategy: Strategy{Op: OpLatest},
			Groups: biLadder(biGroup("device", Selector{Entry: EntryDevice}), biManual())})
	}
	withingsOnly := func(metric string) Builtin {
		return biV1("Only Withings reports this measure; other sources join the ladder when they exist.",
			Rule{Metric: metric, Window: latest, Strategy: firstAvailable, Groups: biBrand(provWithings)})
	}

	out := []Builtin{
		biV1("Watch, ring, band, phone; hours resolve separately so a watch left on the charger falls back to the phone for those hours only.",
			Rule{Metric: "steps", Window: day, Strategy: firstAvailable, Quality: stepGates(), Compose: hourly(),
				Groups: biLadder(biDevice("watch"), biDevice("ring"), biDevice("band"), biDevice("phone"))}),
		biV1("Follows the step source; inside a workout, the device that recorded it.",
			Rule{Metric: "distance_walk_run", Window: day, Strategy: firstAvailable, Quality: stepGates(), Compose: hourly(),
				Groups:   biLadder(biDevice("watch"), biDevice("phone"), biDevice("ring")),
				Contexts: map[Context][]string{ContextWorkout: {ContextWorkoutSource}}}),
		biV1("Every device is far off; one worn source per day keeps days comparable.",
			Rule{Metric: "active_energy", Window: day, Strategy: firstAvailable,
				Quality: &Quality{MinCoverage: biRatio(0.8), RequireWear: "heart_rate"},
				Groups:  biLadder(biDevice("watch"), biDevice("band"), biDevice("ring"), biDevice("phone"))}),
		biV1("Chest straps are ECG-class, then wrist devices by independent validation; inside workouts the recording device follows the straps.",
			Rule{Metric: "heart_rate", Window: RuleWindow{Kind: catalog.WindowBucket, Size: "5m"}, Strategy: firstAvailable,
				Quality: &Quality{PlausibleRange: []float64{25, 230}, ExcludeFlags: []string{"manual_entry"}},
				Groups: biLadder(biDevice("chest_strap"), biDevice("arm_band"), biAppleWatch(), biBrand(provGarmin), biBrand(provFitbit),
					biBrand(provSamsung), biBrand(provWhoop), biBrand(provPolar), biBrand(provXiaomi), biBrand(provAmazfit), biBrand(provOura)),
				Contexts: map[Context][]string{ContextWorkout: {"chest_strap", "arm_band", ContextWorkoutSource}}}),
		biV1("Selection only (definitions differ); ranked by nightly error against a chest strap.",
			Rule{Metric: "resting_heart_rate", Window: day, Strategy: firstAvailable, Quality: &Quality{MaxStaleness: "36h"},
				Groups: biLadder(biBrand(provOura), biBrand(provWhoop), biBrand(provPolar), biApple(), biBrand(provGarmin), biBrand(provFitbit), biBrand(provSamsung))}),
		biV1("Spot SDNN is its own method and comes from Apple only; never mixed with RMSSD.",
			Rule{Metric: "hrv_sdnn", Window: day, Strategy: Strategy{Op: OpSingleSource}, Groups: biApple()}),
		biV1("For sources that send 5-minute samples; never combined with hrv_rmssd_nightly.",
			Rule{Metric: "hrv_rmssd", Window: night, Strategy: firstAvailable, Groups: rmssd()}),
		biV1("Selection only (overnight windows differ); ranked by agreement with ECG.",
			Rule{Metric: "hrv_rmssd_nightly", Window: day, Strategy: firstAvailable, Quality: &Quality{MaxStaleness: "36h"}, Groups: rmssd()}),
		biV1("Ranked by published error against lab tests; older than 30 days counts as stale.",
			Rule{Metric: "vo2max", Window: latest, Strategy: firstAvailable, Quality: &Quality{MaxStaleness: "30d"},
				Groups: biLadder(biBrand(provGarmin), biApple(), biBrand(provPolar), biBrand(provSamsung))}),
		withingsOnly("pulse_wave_velocity"),
		withingsOnly("vascular_age"),
		biV1("Validated cuffs first, then watches with a micro-cuff, then manual entries; one reading never mixes sources, and a day is the mean of its readings.",
			Rule{Metric: FamilyBloodPressure, Window: day, Strategy: firstAvailable, WithinSource: &WithinSource{Statistic: StatMean},
				Groups: biLadder(biDevice("bp_monitor"), biGroup("watch_cuff", Selector{DeviceType: "watch", Entry: EntryDevice}), biManual())}),
		biV1("Nightly mean, ranked by published error against reference oximetry; ring values are a trend only.",
			Rule{Metric: "spo2", Window: night, Strategy: firstAvailable,
				Groups: biLadder(biApple(), biBrand(provSamsung), biBrand(provWithings), biBrand(provGarmin), biBrand(provFitbit), biBrand(provOura), biBrand(provWhoop))}),
		biV1("All published evidence is vendor-funded; low confidence across the board.",
			Rule{Metric: "respiratory_rate", Window: night, Strategy: firstAvailable,
				Groups: biLadder(biBrand(provSamsung), biBrand(provOura), biBrand(provWhoop), biApple(), biBrand(provFitbit), biBrand(provGarmin))}),
		spot("body_temperature", "Spot readings: the newest measured value wins, else the newest manual entry."),
		biV1("Scales agree closely; the latest reading of the day, from a scale before scale apps and manual entries.",
			Rule{Metric: "weight", Window: day, Strategy: firstAvailable, WithinSource: &WithinSource{Statistic: StatLatest}, Groups: weightGroups()}),
		spot("height", "The newest value from any source."),
	}
	for _, m := range catalog.Metrics() {
		if m.Group == "body_composition" && m.Code != "weight" {
			out = append(out, biV1("Same scale as that day's weight (each vendor's body model differs).",
				Rule{Metric: m.Code, Window: day, Strategy: firstAvailable, Follow: "weight", Groups: weightGroups()}))
		}
	}
	return append(out, biV1("One night comes from one source, ranked by independent four-stage agreement with PSG; a device on the charger fails the coverage gate.",
		Rule{Metric: FamilySleep, Window: night, Strategy: Strategy{Op: OpEventPriority},
			Quality: &Quality{MaxStaleness: "36h", Sleep: &SleepQuality{MatchOverlap: biRatio(0.5), MinEpisodeCoverage: biRatio(0.7)}},
			Groups: biLadder(biBrand(provOura), biAppleWatch(), biBrand(provFitbit),
				// The Withings normalizer reports its under-mattress sensor as sleep_monitor.
				biGroup("under_mattress", Selector{DeviceType: "under_mattress"}, Selector{DeviceType: "sleep_monitor"}),
				biBrand(provSamsung), biBrand(provWhoop), biBrand(provGarmin), biBrand(provPolar), biBrand(provXiaomi), biBrand(provAmazfit))}))
}
