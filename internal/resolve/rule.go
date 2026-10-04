package resolve

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// SchemaV1 identifies rule schema v1 (schemas/resolution-rule.v1.json, ADR-0008).
const SchemaV1 = "vitamux.rule/1"

// Rule families resolve several catalogue codes with one rule, so the codes always come from
// the same event or reading (docs/architecture/resolution.md#group-coherent-selection).
const (
	FamilySleep         = "sleep"          // every sleep-derived code
	FamilyBloodPressure = "blood_pressure" // bp_reading components
)

// Rule is one versioned resolution rule. The JSON shape is schemas/resolution-rule.v1.json;
// ParseRule decodes and validates it. Optional parts are nil or zero when absent.
type Rule struct {
	Schema       string        `json:"schema"`
	Metric       string        `json:"metric"` // catalogue code or a Family*
	Window       RuleWindow    `json:"window"`
	Groups       []Group       `json:"groups"` // ordered; an input joins the first group it matches
	Exclude      []Selector    `json:"exclude,omitempty"`
	WithinSource *WithinSource `json:"within_source,omitempty"`
	Strategy     Strategy      `json:"strategy"`
	Quality      *Quality      `json:"quality,omitempty"`
	// Contexts (E1) puts the listed group ids first inside aligned workouts or the sleep episode.
	// The list may also name ContextWorkoutSource. Group membership never changes.
	Contexts map[Context][]string `json:"contexts,omitempty"`
	// Follow (E5) uses the group (by id) the leader metric selected for the same window.
	Follow               string    `json:"follow,omitempty"`
	Compose              *Compose  `json:"compose,omitempty"` // E9
	AcknowledgedWarnings []Warning `json:"acknowledged_warnings,omitempty"`
}

// RuleWindow is the rule's default window. Size is set for bucket windows only.
type RuleWindow struct {
	Kind catalog.Window `json:"kind"`
	Size Duration       `json:"size,omitempty"`
}

// Group is one source group: an input belongs to it when any selector matches.
type Group struct {
	ID    string     `json:"id"`
	Match []Selector `json:"match"`
}

// Selector fields are ANDed; unset fields match anything. See Selector.Matches.
type Selector struct {
	Provider        string `json:"provider,omitempty"`
	ConnectionID    string `json:"connection_id,omitempty"`
	OriginKey       string `json:"origin_key,omitempty"`
	OriginKeyPrefix string `json:"origin_key_prefix,omitempty"`
	OriginName      string `json:"origin_name,omitempty"`
	Relayed         *bool  `json:"relayed,omitempty"`
	DeviceType      string `json:"device_type,omitempty"`
	DeviceModel     string `json:"device_model,omitempty"`
	// DeviceManufacturer is the brand as devices.manufacturer stores it; it matches case-insensitively.
	DeviceManufacturer string `json:"device_manufacturer,omitempty"`
	DeviceID           string `json:"device_id,omitempty"`
	Entry              Entry  `json:"entry,omitempty"`
}

// DeviceTypes is the devices.device_type vocabulary that device_type selectors and the built-in
// rules match (data-model.md#tables); the owner picks from it when typing a device.
var DeviceTypes = []string{"watch", "band", "ring", "phone", "chest_strap", "arm_band", "scale", "bp_monitor",
	"thermometer", "under_mattress", "sleep_monitor", "cgm", "glucose_meter", "other"}

// Entry says whether a row was measured by a device or typed in by a person.
type Entry string

const (
	EntryDevice Entry = "device"
	EntryManual Entry = "manual"
)

// Op is a cross-source strategy (docs/architecture/resolution.md#strategies).
type Op string

const (
	OpSingleSource   Op = "single_source"
	OpFirstAvailable Op = "first_available"
	OpMean           Op = "mean_across_sources"
	OpMin            Op = "minimum_across_sources"
	OpMax            Op = "maximum_across_sources"
	OpSum            Op = "sum_across_sources"
	OpLatest         Op = "latest"
	OpEarliest       Op = "earliest"
	OpEventPriority  Op = "event_priority"
)

// Strategy is the cross-source step. MinSources 0 means 1; OnInsufficient "" means use_available.
// Both apply to the pooling ops (mean, minimum, maximum, sum) only.
type Strategy struct {
	Op             Op           `json:"op"`
	MinSources     int          `json:"min_sources,omitempty"`
	OnInsufficient Insufficient `json:"on_insufficient,omitempty"`
}

// Insufficient is what a pooling op does below min_sources.
type Insufficient string

const (
	UseAvailable Insufficient = "use_available" // compute over the valid groups, warn insufficient_sources
	NoValue      Insufficient = "no_value"
)

// WithinSource tunes the per-group step. Empty values mean the defaults noted on each type.
type WithinSource struct {
	IntraGroup       IntraGroup       `json:"intra_group,omitempty"`
	DailyValuePolicy DailyValuePolicy `json:"daily_value_policy,omitempty"`
	Statistic        Statistic        `json:"statistic,omitempty"` // E2; empty = the aggregation's default
	Span             Duration         `json:"span,omitempty"`      // min_rolling_mean only
}

// IntraGroup combines several sub-sources of one group per bucket. auto (default) is the mean
// for intensive metrics and the max for additive ones; sum must be acknowledged.
type IntraGroup string

const (
	IntraAuto IntraGroup = "auto"
	IntraMean IntraGroup = "mean"
	IntraMax  IntraGroup = "max"
	IntraSum  IntraGroup = "sum"
)

// DailyValuePolicy picks between a provider daily total and the interval sum on local_day
// (additive metrics). prefer_reported is the default. The two are never added.
type DailyValuePolicy string

const (
	PreferReported DailyValuePolicy = "prefer_reported"
	IntervalsOnly  DailyValuePolicy = "intervals_only"
)

// Statistic is the within-source window statistic (E2 and grouped latest metrics).
type Statistic string

const (
	StatMinRollingMean Statistic = "min_rolling_mean" // lowest mean over Span of bucket means
	StatMin            Statistic = "min"              // lowest bucket mean
	StatLatest         Statistic = "latest"           // latest reading of the day (default for latest metrics)
	StatMean           Statistic = "mean"             // mean of the day's readings, per component
)

// Quality holds the gates. Pointers distinguish "absent" from an explicit value.
type Quality struct {
	MinCoverage    *float64      `json:"min_coverage,omitempty"`
	PlausibleRange []float64     `json:"plausible_range,omitempty"` // [low, high] in the canonical unit
	ExcludeFlags   []string      `json:"exclude_flags,omitempty"`   // measurements.quality_flags names
	MaxStaleness   Duration      `json:"max_staleness,omitempty"`
	RequireWear    string        `json:"require_wear,omitempty"` // E3: the wear metric, e.g. heart_rate
	Sleep          *SleepQuality `json:"sleep,omitempty"`
}

// SleepQuality configures episode alignment. Absent values take the documented defaults.
type SleepQuality struct {
	MatchOverlap       *float64 `json:"match_overlap,omitempty"`        // default 0.5
	MinEpisodeCoverage *float64 `json:"min_episode_coverage,omitempty"` // opt-in; unset = no gate
	IncludeNaps        bool     `json:"include_naps,omitempty"`
	NightAnchor        string   `json:"night_anchor,omitempty"` // local "HH:MM", 12:00-23:59; default 18:00 (ADR-0009)
}

// Context is an E1 context.
type Context string

const (
	ContextWorkout Context = "workout"
	ContextSleep   Context = "sleep"
)

// ContextWorkoutSource in contexts.workout stands for the group that holds the source which
// recorded the workout.
const ContextWorkoutSource = "@workout_source"

// Compose (E9) resolves each local hour, then sums the hours into the local_day value.
type Compose struct {
	From catalog.Window `json:"from"` // hour
	Op   ComposeOp      `json:"op"`
}

// ComposeOp picks the value of one hour.
type ComposeOp string

const (
	ComposeFirstAvailable ComposeOp = "first_available"
	ComposeMax            ComposeOp = "max"
)

// Duration is a rule duration: a positive integer and one unit of s, m, h or d ("5m", "36h",
// "30d"). It stays a string so rules round-trip byte for byte.
type Duration string

// Std returns d as a time.Duration, or 0 when d is empty or malformed.
func (d Duration) Std() time.Duration {
	s := string(d)
	if len(s) < 2 || len(s) > 7 || s[0] == '0' {
		return 0
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n <= 0 {
		return 0
	}
	unit := map[byte]time.Duration{'s': time.Second, 'm': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour}[s[len(s)-1]]
	return time.Duration(n) * unit
}

// DefaultNightAnchor is the local time that separates one night from the next (ADR-0009).
const DefaultNightAnchor = 18 * time.Hour

// NightAnchor returns the rule's night anchor as an offset from local midnight.
func (r *Rule) NightAnchor() time.Duration {
	if r.Quality == nil || r.Quality.Sleep == nil || r.Quality.Sleep.NightAnchor == "" {
		return DefaultNightAnchor
	}
	a, _ := parseAnchor(r.Quality.Sleep.NightAnchor)
	return a
}

// parseAnchor reads "HH:MM" within 12:00-23:59.
func parseAnchor(s string) (time.Duration, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil || len(s) != 5 || t.Hour() < 12 {
		return DefaultNightAnchor, false
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, true
}

// ParseRule decodes a rule strictly (unknown fields are errors) and validates it against the
// catalogue. Errors are *ValidationError.
func ParseRule(data []byte) (*Rule, error) {
	var r Rule
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	err := dec.Decode(&r)
	if err == nil {
		if _, extra := dec.Token(); !errors.Is(extra, io.EOF) {
			err = errors.New("trailing data after the JSON value")
		}
	}
	if err != nil {
		return nil, decodeError(err)
	}
	return &r, Validate(&r)
}

func decodeError(err error) error {
	ptr, detail := "", "invalid JSON"
	var typeErr *json.UnmarshalTypeError
	var syntaxErr *json.SyntaxError
	switch {
	case errors.As(err, &typeErr):
		ptr, detail = "/"+strings.ReplaceAll(typeErr.Field, ".", "/"), "must be "+typeErr.Type.Kind().String()
	case errors.As(err, &syntaxErr):
		detail = "invalid JSON at offset " + strconv.FormatInt(syntaxErr.Offset, 10)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		detail = strings.TrimPrefix(err.Error(), "json: ")
	case strings.HasPrefix(err.Error(), "trailing"):
		detail = err.Error()
	}
	return &ValidationError{Errors: []FieldError{{Pointer: ptr, Detail: detail}}}
}
