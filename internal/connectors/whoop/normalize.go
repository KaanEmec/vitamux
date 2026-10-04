// Package whoop holds the core side of the WHOOP connector (E19): the normalizers of the raw
// payloads the whoop sidecar stores (docs/providers/whoop.md). Each raw body is
// {"unit": {...}, "response": <WHOOP's response body verbatim>}. whoop.strain_deep_dive is
// raw only by design: its normalizer accepts it and writes nothing. whoop.journal is not synced.
package whoop

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

const (
	Provider             = "whoop"
	StreamHeartRate      = "whoop.heart_rate"
	StreamCycles         = "whoop.cycles"
	StreamSleep          = "whoop.sleep"
	StreamWorkouts       = "whoop.workouts"
	StreamStrainDeepDive = "whoop.strain_deep_dive"
)

// versions is each stream normalizer's Version(): bump only the stream whose output changes.
var versions = map[string]int{StreamHeartRate: 1, StreamCycles: 2, StreamSleep: 2, StreamWorkouts: 1, StreamStrainDeepDive: 1}

// strap is the device of every WHOOP record: the private API names no device of its own.
var strap = normalize.Device{Fingerprint: "whoop:strap", Type: "band", Manufacturer: "WHOOP"}

// Normalizer normalizes one WHOOP stream; its ID is the stream name.
type Normalizer struct{ Stream string }

// Normalizers returns one normalizer per stored stream, for normalize.NewRegistry.
func Normalizers() []normalize.Normalizer {
	return []normalize.Normalizer{Normalizer{StreamHeartRate}, Normalizer{StreamCycles}, Normalizer{StreamSleep},
		Normalizer{StreamWorkouts}, Normalizer{StreamStrainDeepDive}}
}

func (n Normalizer) ID() string                    { return n.Stream }
func (n Normalizer) Version() int                  { return versions[n.Stream] }
func (n Normalizer) Accepts(stream, _ string) bool { return stream == n.Stream }

// builder collects one payload's output; errors name fields, never values.
type builder struct {
	stream string
	out    normalize.Output
}

func (b *builder) drift(format string, args ...any) error {
	return fmt.Errorf("%s: %s (shape drift)", b.stream, fmt.Sprintf(format, args...))
}

// decode unmarshals v, reporting a retyped field by its path and type only.
func (b *builder) decode(raw []byte, v any) error {
	err := json.Unmarshal(raw, v)
	var te *json.UnmarshalTypeError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &te):
		return b.drift("field %q is not %s", te.Field, te.Type)
	}
	return b.drift("undecodable response")
}

func (b *builder) warn(code, detail string) {
	b.out.Warnings = append(b.out.Warnings, normalize.Warning{Code: code, Detail: detail})
}

// sleepUnit is the unit of a whoop.sleep raw: the sleep's activity id, plus its nap flag and
// offset, which the sidecar copies from the cycle's sleeps[] because the stage events lack them.
type sleepUnit struct {
	ID             string `json:"id"`
	IsNap          *bool  `json:"is_nap"`
	TimezoneOffset string `json:"timezone_offset"`
}

// Normalize is pure. Heart rate carries no WHOOP timezone_offset, so its local dates come from
// the owner's timezone periods; everything else carries WHOOP's offset.
func (n Normalizer) Normalize(_ context.Context, raw normalize.RawPayload, _ normalize.Env) (normalize.Output, error) {
	b := &builder{stream: n.Stream}
	var body struct {
		Unit     sleepUnit       `json:"unit"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(raw.Body, &body); err != nil || isNull(body.Response) {
		return normalize.Output{}, b.drift("payload without a unit and a response")
	}
	var err error
	switch n.Stream {
	case StreamHeartRate:
		err = b.heartRate(body.Response)
	case StreamCycles:
		err = b.cycles(body.Response)
	case StreamSleep:
		err = b.sleep(body.Unit, body.Response)
	case StreamWorkouts:
		err = b.workouts(body.Response)
	case StreamStrainDeepDive: // raw only: an untyped app screen, kept for reprocessing
	default:
		err = b.drift("stream is not normalized")
	}
	if err != nil {
		return normalize.Output{}, err
	}
	if len(b.out.Measurements)+len(b.out.Sleep)+len(b.out.Workouts) > 0 {
		b.out.Devices = []normalize.Device{strap}
	}
	return b.out, nil
}

func isNull(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || string(raw) == "null"
}

// heartRate maps a metrics-service body {"values": [{time, data}]} to samples at their native step.
func (b *builder) heartRate(resp json.RawMessage) error {
	var r struct {
		Values *[]struct {
			Time *int64   `json:"time"` // Unix ms
			Data *float64 `json:"data"`
		} `json:"values"`
	}
	if err := b.decode(resp, &r); err != nil {
		return err
	}
	if r.Values == nil {
		return b.drift("response without values")
	}
	for _, v := range *r.Values {
		if v.Time == nil || v.Data == nil {
			return b.drift("value without time or data")
		}
		t := time.UnixMilli(*v.Time).UTC()
		if t.Year() < 2000 || t.Year() > 9999 {
			return b.drift("value time out of range")
		}
		b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: "heart_rate", Kind: catalog.Sample,
			Start: t, Value: *v.Data, Unit: "bpm", Device: strap.Fingerprint})
	}
	return nil
}

// cycleRecord is one item of the cycles BFF's records (docs/providers/whoop.md#content-of-records).
// Fields not read here (cycle days and during, SpO2, skin temperature, sleep need and debt,
// workouts, v2_activities) stay raw.
type cycleRecord struct {
	Cycle *struct {
		ID             json.RawMessage `json:"id"`
		TimezoneOffset string          `json:"timezone_offset"`
		DayStrain      *float64        `json:"day_strain"`
	} `json:"cycle"`
	Recovery *struct {
		RecoveryScore    *float64 `json:"recovery_score"`
		RestingHeartRate *float64 `json:"resting_heart_rate"`
		HRVRMSSD         *float64 `json:"hrv_rmssd"` // seconds
	} `json:"recovery"`
	Sleeps []cycleSleep `json:"sleeps"`
}

// cycleSleep is a sleep summary inside a cycle, naps included.
type cycleSleep struct {
	ActivityID      string   `json:"activity_id"`
	During          string   `json:"during"`
	TimezoneOffset  string   `json:"timezone_offset"`
	IsNap           bool     `json:"is_nap"`
	Significant     bool     `json:"significant"`
	Score           *float64 `json:"score"` // sleep performance, %
	RespiratoryRate *float64 `json:"respiratory_rate"`
}

// rank orders main-sleep candidates: a non-nap sleep, else a significant one, else none.
func (s cycleSleep) rank() int {
	switch {
	case !s.IsNap:
		return 2
	case s.Significant:
		return 1
	}
	return 0
}

// cycles maps a cycles body {"records": [...]}.
func (b *builder) cycles(resp json.RawMessage) error {
	var r struct {
		Records *[]cycleRecord `json:"records"`
	}
	if err := b.decode(resp, &r); err != nil {
		return err
	}
	if r.Records == nil {
		return b.drift("response without records")
	}
	for _, c := range *r.Records {
		if err := b.cycle(c); err != nil {
			return err
		}
	}
	return nil
}

// cycle maps one record. Sleep sessions belong to whoop.sleep, which has their stages and is
// keyed by the same activity_id; the cycle adds each sleep's respiratory rate and its main
// sleep's performance. The main sleep is the longest of the highest rank (non-nap, else
// significant). A cycle runs from one sleep to the next, so it is not a local day: day strain,
// recovery, resting HR and nightly RMSSD are daily values at the main sleep's wake-up, so they
// share its sleep_date (ADR-0009). Without a main sleep they stay raw.
func (b *builder) cycle(c cycleRecord) error {
	if c.Cycle == nil {
		return b.drift("record without cycle")
	}
	id := externalID(c.Cycle.ID)
	if id == "" {
		return b.drift("cycle without id")
	}
	main, spans := -1, make([][2]time.Time, len(c.Sleeps))
	for i, s := range c.Sleeps {
		start, end, ok := during(s.During)
		switch {
		case s.ActivityID == "":
			return b.drift("sleep without activity_id")
		case !ok || !end.After(start):
			return b.drift("sleep without a valid during")
		}
		spans[i] = [2]time.Time{start, end}
		if r := s.rank(); r > 0 && (main < 0 || r > c.Sleeps[main].rank() ||
			r == c.Sleeps[main].rank() && end.Sub(start) > spans[main][1].Sub(spans[main][0])) {
			main = i
		}
	}
	var wake time.Time
	var wakeZone normalize.Zone
	for i, s := range c.Sleeps {
		zone, err := b.zone(cmp.Or(s.TimezoneOffset, c.Cycle.TimezoneOffset))
		if err != nil {
			return err
		}
		start, end := spans[i][0], spans[i][1]
		key := func(metric string) normalize.Key {
			return normalize.Key{RecordType: "sleep", ExternalID: s.ActivityID, Component: metric}
		}
		if s.RespiratoryRate != nil {
			b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: "respiratory_rate",
				Kind: catalog.Sample, Start: start.Add(end.Sub(start) / 2), Zone: zone, Value: *s.RespiratoryRate,
				Unit: "breaths/min", Device: strap.Fingerprint, Key: key("respiratory_rate")})
		}
		if i != main {
			continue
		}
		wake, wakeZone = end, zone
		if s.Score != nil {
			b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: "whoop_sleep_performance",
				Kind: catalog.Sample, Start: end, Zone: zone, Value: *s.Score, Unit: "%", Device: strap.Fingerprint,
				Key: key("whoop_sleep_performance")})
		}
	}
	if main < 0 {
		if c.Cycle.DayStrain != nil || c.Recovery != nil {
			b.warn("cycle_without_main_sleep", id)
		}
		return nil
	}
	add := func(metric string, v *float64, unit string) {
		if v != nil {
			b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: metric, Kind: catalog.DailyValue,
				Start: wake, End: &wake, Zone: wakeZone, Value: *v, Unit: unit, Device: strap.Fingerprint,
				Key: normalize.Key{RecordType: "cycle", ExternalID: id, Component: metric}})
		}
	}
	add("whoop_strain", c.Cycle.DayStrain, "index")
	if r := c.Recovery; r != nil {
		add("whoop_recovery", r.RecoveryScore, "%")
		add("resting_heart_rate", r.RestingHeartRate, "bpm")
		add("hrv_rmssd_nightly", r.HRVRMSSD, "s") // WHOOP sends seconds; the writer stores ms
	}
	return nil
}

// sleepStages maps sleep-event types, compared in lower case, as @dofek/whoop reads them;
// no_data is a gap.
var sleepStages = map[string]string{"awake": "awake", "light": "light", "deep": "deep", "slow_wave": "deep", "rem": "rem", "no_data": ""}

// sleep maps one sleep-events body, an array of stage events [{during, type}], to the session of
// the unit's activity id, bounded by its events. It is the only writer of WHOOP sleep sessions;
// totals are summed from the stages. An unknown type is a warning and a gap. A raw stored by
// sidecar 0.2.2 or older has no is_nap in its unit and stays raw until the sleep is fetched again.
func (b *builder) sleep(u sleepUnit, resp json.RawMessage) error {
	if u.ID == "" {
		return b.drift("sleep without unit id")
	}
	var events []struct {
		During *string `json:"during"`
		Type   *string `json:"type"`
	}
	if err := b.decode(resp, &events); err != nil {
		return err
	}
	if u.IsNap == nil {
		b.warn("sleep_unit_without_is_nap", "fetched by an older sidecar: sync whoop.sleep again")
		return nil
	}
	zone, err := b.zone(u.TimezoneOffset)
	if err != nil {
		return err
	}
	sess := normalize.SleepSession{Zone: zone, Nap: *u.IsNap, Device: strap.Fingerprint,
		Key: normalize.Key{RecordType: "sleep", ExternalID: u.ID}}
	warned := map[string]bool{}
	for _, e := range events {
		if e.During == nil || e.Type == nil {
			return b.drift("stage event without during or type")
		}
		a, z, ok := during(*e.During)
		if !ok || !z.After(a) {
			return b.drift("stage event without a valid during")
		}
		if sess.Start.IsZero() || a.Before(sess.Start) {
			sess.Start = a
		}
		if z.After(sess.End) {
			sess.End = z
		}
		kind, known := sleepStages[strings.ToLower(*e.Type)]
		switch {
		case !known && !warned[*e.Type]:
			warned[*e.Type] = true
			b.warn("unknown_stage", *e.Type)
		case kind != "":
			sess.Stages = append(sess.Stages, normalize.SleepStage{Stage: kind, Start: a, End: z})
		}
	}
	if len(events) > 0 { // none yet: not scored
		b.out.Sleep = append(b.out.Sleep, sess)
	}
	return nil
}

// zone is WHOOP's timezone_offset as a record zone; empty means none.
func (b *builder) zone(offset string) (normalize.Zone, error) {
	if offset == "" {
		return normalize.Zone{}, nil
	}
	off, err := b.offset(offset)
	return normalize.Zone{OffsetMin: &off}, err
}

// offset parses WHOOP's timezone_offset ("-05:00", "-0500" or "Z").
func (b *builder) offset(s string) (int16, error) {
	if s == "Z" {
		return 0, nil
	}
	if len(s) == 6 && s[3] == ':' {
		s = s[:3] + s[4:]
	}
	if len(s) != 5 || (s[0] != '+' && s[0] != '-') {
		return 0, b.drift("timezone_offset is not ±hh:mm")
	}
	h, err1 := strconv.ParseUint(s[1:3], 10, 8) // unsigned: no sign inside
	m, err2 := strconv.ParseUint(s[3:], 10, 8)
	if err1 != nil || err2 != nil || h > 18 || m > 59 {
		return 0, b.drift("timezone_offset is not ±hh:mm")
	}
	off := int16(h*60 + m)
	if s[0] == '-' {
		off = -off
	}
	return off, nil
}

// during parses WHOOP's Postgres range "['start','end')".
func during(s string) (start, end time.Time, ok bool) {
	if len(s) < 2 || !strings.ContainsRune("[(", rune(s[0])) || !strings.ContainsRune(")]", rune(s[len(s)-1])) {
		return start, end, false
	}
	a, c, found := strings.Cut(s[1:len(s)-1], ",")
	start, err1 := time.Parse(time.RFC3339Nano, strings.Trim(a, `'" `))
	end, err2 := time.Parse(time.RFC3339Nano, strings.Trim(c, `'" `))
	return start.UTC(), end.UTC(), found && err1 == nil && err2 == nil
}

// externalID reads an id that WHOOP sends as a number (v1) or a string (v2 UUID).
func externalID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// sports maps WHOOP sport_name values to the canonical sports (HealthKit activity names in snake case).
var sports = map[string]string{
	"running": "running", "cycling": "cycling", "spin": "cycling", "indoor-cycling": "cycling", "mountain-biking": "cycling",
	"walking": "walking", "hiking": "hiking", "rucking": "hiking", "swimming": "swimming", "rowing": "rowing",
	"yoga": "yoga", "hot-yoga": "yoga", "pilates": "pilates", "barre": "barre", "stretching": "flexibility",
	"meditation": "mind_and_body", "weightlifting": "traditional_strength_training",
	"strength-trainer": "traditional_strength_training", "powerlifting": "traditional_strength_training",
	"functional-fitness": "functional_strength_training", "hiit": "high_intensity_interval_training",
	"elliptical": "elliptical", "stairmaster": "stair_climbing", "boxing": "boxing", "kickboxing": "kickboxing",
	"martial-arts": "martial_arts", "tennis": "tennis", "pickleball": "pickleball", "squash": "squash",
	"badminton": "badminton", "table-tennis": "table_tennis", "soccer": "soccer", "basketball": "basketball",
	"volleyball": "volleyball", "golf": "golf", "dance": "dance", "climbing": "climbing", "rock-climbing": "climbing",
	"skiing": "downhill_skiing", "cross-country-skiing": "cross_country_skiing", "snowboarding": "snowboarding",
	"jump-rope": "jump_rope", "triathlon": "swim_bike_run", "surfing": "surfing_sports",
}

// workouts maps one developer-API workout record (the sidecar slices each record of a page into
// its own raw). Strain, HR zones and percent recorded have no place in the workout model and stay
// raw. A weightlifting detail is a separate payload, and a workout's sets would have to come from
// the payload that writes the workout, so the detail is only checked for shape and stays raw.
func (b *builder) workouts(resp json.RawMessage) error {
	var detail struct {
		ActivityID    *string            `json:"activity_id"`
		WorkoutGroups *[]json.RawMessage `json:"workout_groups"`
	}
	if err := b.decode(resp, &detail); err != nil {
		return err
	}
	if detail.ActivityID == nil && detail.WorkoutGroups == nil {
		return b.workout(resp)
	}
	if detail.ActivityID == nil || *detail.ActivityID == "" || detail.WorkoutGroups == nil {
		return b.drift("weightlifting detail without activity_id or workout_groups")
	}
	return nil
}

func (b *builder) workout(raw json.RawMessage) error {
	var w struct {
		ID             string     `json:"id"`
		Start          *time.Time `json:"start"`
		End            *time.Time `json:"end"`
		TimezoneOffset string     `json:"timezone_offset"`
		SportName      string     `json:"sport_name"`
		SportID        *int       `json:"sport_id"`
		Score          *struct {
			AverageHeartRate *float64 `json:"average_heart_rate"`
			MaxHeartRate     *float64 `json:"max_heart_rate"`
			Kilojoule        *float64 `json:"kilojoule"`
			DistanceMeter    *float64 `json:"distance_meter"`
		} `json:"score"`
	}
	if err := b.decode(raw, &w); err != nil {
		return err
	}
	switch {
	case w.ID == "":
		return b.drift("workout without id")
	case w.Start == nil || w.End == nil || !w.End.After(*w.Start):
		return b.drift("workout without start and a later end")
	}
	zone, err := b.zone(w.TimezoneOffset)
	if err != nil {
		return err
	}
	provider := w.SportName
	if provider == "" && w.SportID != nil {
		provider = "sport_id:" + strconv.Itoa(*w.SportID)
	}
	sport, ok := sports[strings.ToLower(w.SportName)]
	if !ok {
		sport = "other"
		b.warn("unmapped_sport", provider)
	}
	x := normalize.Workout{Start: w.Start.UTC(), End: w.End.UTC(), Zone: zone, Sport: sport, ProviderSport: provider,
		Device: strap.Fingerprint, Key: normalize.Key{RecordType: "workout", ExternalID: w.ID}}
	if sc := w.Score; sc != nil {
		x.AvgHRBpm, x.MaxHRBpm, x.DistanceM = sc.AverageHeartRate, sc.MaxHeartRate, sc.DistanceMeter
		if sc.Kilojoule != nil {
			kcal := *sc.Kilojoule / 4.184
			x.EnergyKcal = &kcal
		}
	}
	b.out.Workouts = append(b.out.Workouts, x)
	return nil
}
