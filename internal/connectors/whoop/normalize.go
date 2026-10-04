// Package whoop holds the core side of the WHOOP connector (E19): the normalizers of the raw
// payloads the whoop sidecar stores (docs/providers/whoop.md). Each raw body is
// {"unit": {...}, "response": <WHOOP's response body verbatim>}. whoop.strain_deep_dive and
// whoop.journal stay raw: no normalizer accepts them until their shapes are verified.
package whoop

import (
	"bytes"
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
	Provider        = "whoop"
	StreamHeartRate = "whoop.heart_rate"
	StreamSteps     = "whoop.steps"
	StreamCycles    = "whoop.cycles"
	StreamSleep     = "whoop.sleep"
	StreamWorkouts  = "whoop.workouts"

	stepsStep = 5 * time.Minute // the getSteps step the sidecar requests
)

// versions is each stream normalizer's Version(): bump only the stream whose output changes.
var versions = map[string]int{StreamHeartRate: 1, StreamSteps: 1, StreamCycles: 1, StreamSleep: 1, StreamWorkouts: 1}

// strap is the device of every WHOOP record: the private API names no device of its own.
var strap = normalize.Device{Fingerprint: "whoop:strap", Type: "band", Manufacturer: "WHOOP"}

// Normalizer normalizes one WHOOP stream; its ID is the stream name.
type Normalizer struct{ Stream string }

// Normalizers returns one normalizer per normalized stream, for normalize.NewRegistry.
func Normalizers() []normalize.Normalizer {
	return []normalize.Normalizer{Normalizer{StreamHeartRate}, Normalizer{StreamSteps}, Normalizer{StreamCycles},
		Normalizer{StreamSleep}, Normalizer{StreamWorkouts}}
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

// Normalize is pure. Records without a WHOOP timezone_offset (HR, steps, the cycle's inline
// sleeps) get their local dates from the owner's timezone periods.
func (n Normalizer) Normalize(_ context.Context, raw normalize.RawPayload, _ normalize.Env) (normalize.Output, error) {
	b := &builder{stream: n.Stream}
	var body struct {
		Unit struct {
			ID string `json:"id"`
		} `json:"unit"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(raw.Body, &body); err != nil || isNull(body.Response) {
		return normalize.Output{}, b.drift("payload without a unit and a response")
	}
	var err error
	switch n.Stream {
	case StreamHeartRate:
		err = b.series(body.Response, "heart_rate")
	case StreamSteps:
		err = b.series(body.Response, "steps")
	case StreamCycles:
		err = b.cycles(body.Response)
	case StreamSleep:
		err = b.sleep(body.Unit.ID, body.Response)
	case StreamWorkouts:
		err = b.workouts(body.Response)
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

// series maps a metrics-service body {"values": [{time, data}]}: heart rate as samples at their
// native step, steps as counts over [time, time+step).
func (b *builder) series(resp json.RawMessage, metric string) error {
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
		m := normalize.Measurement{Metric: metric, Kind: catalog.Sample, Start: t, Value: *v.Data, Unit: "bpm", Device: strap.Fingerprint}
		if metric == "steps" {
			end := t.Add(stepsStep)
			m.Kind, m.End, m.Unit = catalog.Interval, &end, "count"
		}
		b.out.Measurements = append(b.out.Measurements, m)
	}
	return nil
}

// rawCycle is one cycles-BFF item (docs/providers/whoop.md#content-of-records). The inner cycle
// object is unverified; day strain is read from its score, a bare number (as in the BFF workout
// records) or an object with strain (as in WHOOP's Cycle record).
type rawCycle struct {
	ID    json.RawMessage `json:"id"`
	Days  []string        `json:"days"`
	Cycle struct {
		Score json.RawMessage `json:"score"`
	} `json:"cycle"`
	Sleep *struct {
		ID json.RawMessage `json:"id"`
	} `json:"sleep"`
	Sleeps   []inlineSleep `json:"sleeps"`
	Recovery *struct {
		SleepID          json.RawMessage `json:"sleep_id"`
		ScoreState       string          `json:"score_state"`
		Score            json.RawMessage `json:"score"`
		RecoveryScore    *float64        `json:"recovery_score"`
		RestingHeartRate *float64        `json:"resting_heart_rate"`
		HRVRMSSD         *float64        `json:"hrv_rmssd"` // seconds
	} `json:"recovery"`
}

// inlineSleep is a sleep summary inside a cycle; durations are in ms. Its id is unverified and
// read only if present.
type inlineSleep struct {
	ID              json.RawMessage `json:"id"`
	During          string          `json:"during"`
	State           *string         `json:"state"`
	Wake            *float64        `json:"wake_duration"`
	Light           *float64        `json:"light_sleep_duration"`
	Deep            *float64        `json:"slow_wave_sleep_duration"`
	REM             *float64        `json:"rem_sleep_duration"`
	RespiratoryRate *float64        `json:"respiratory_rate"`
	Significant     *bool           `json:"significant"` // false for a nap
}

func (s inlineSleep) nap() bool      { return s.Significant != nil && !*s.Significant }
func (s inlineSleep) complete() bool { return s.State == nil || *s.State == "complete" }

// cycles maps a cycles body (an array, or one wrapped in cycles, records, data or results).
func (b *builder) cycles(resp json.RawMessage) error {
	if !bytes.HasPrefix(bytes.TrimSpace(resp), []byte("[")) {
		var w map[string]json.RawMessage
		if err := b.decode(resp, &w); err != nil {
			return err
		}
		resp = nil
		for _, k := range []string{"cycles", "records", "data", "results"} {
			if resp = w[k]; resp != nil {
				break
			}
		}
		if resp == nil {
			return b.drift("response without a cycle list")
		}
	}
	var list []rawCycle
	if err := b.decode(resp, &list); err != nil {
		return err
	}
	for _, c := range list {
		if err := b.cycle(c); err != nil {
			return err
		}
	}
	return nil
}

// cycle maps one cycle. Its inline sleeps give each sleep's respiratory rate, and the sessions
// of naps and of a main sleep whose stages whoop.sleep does not own (ownsStages). The main sleep
// is keyed by mainSleepID, any other sleep by its own id or else by the cycle and its ordinal in
// start order, so a rescore that moves its bounds supersedes it instead of adding a second one. A cycle runs
// from one wake-up to the next, so it is not a local day: day strain, recovery, resting HR and
// nightly RMSSD are daily values at the wake-up of the cycle's main sleep, so they share that
// sleep's sleep_date (ADR-0009). SpO2 and skin temperature are overnight values without a
// matching code and stay raw.
func (b *builder) cycle(c rawCycle) error {
	id := externalID(c.ID)
	if id == "" && len(c.Days) > 0 {
		id = c.Days[0]
	}
	if id == "" {
		return b.drift("cycle without id or days")
	}
	mainID := c.mainSleepID()
	// The main sleep is the complete non-nap sleep that ends last; its wake-up dates the cycle.
	main, spans := -1, make([][2]time.Time, len(c.Sleeps))
	for i, s := range c.Sleeps {
		start, end, ok := during(s.During)
		if !ok || !end.After(start) {
			return b.drift("inline sleep without a valid during")
		}
		spans[i] = [2]time.Time{start, end}
		if !s.nap() && s.complete() && (main < 0 || end.After(spans[main][1])) {
			main = i
		}
	}
	for i, s := range c.Sleeps {
		if !s.complete() {
			continue
		}
		start, end := spans[i][0], spans[i][1]
		key := normalize.Key{RecordType: "sleep", ExternalID: externalID(s.ID)}
		if i == main && mainID != "" {
			key.ExternalID = mainID
		}
		if key.ExternalID == "" {
			ord := 0
			for j, sp := range spans {
				if sp[0].Before(start) || (sp[0].Equal(start) && j < i) {
					ord++
				}
			}
			key = normalize.Key{RecordType: "cycle_sleep", ExternalID: id + "/" + strconv.Itoa(ord)}
		}
		if i != main || !ownsStages(mainID) {
			b.out.Sleep = append(b.out.Sleep, normalize.SleepSession{Start: start, End: end, Nap: s.nap(),
				Totals: inlineTotals(s), Device: strap.Fingerprint, Key: key})
		}
		if s.RespiratoryRate != nil {
			key.Component = "respiratory_rate"
			b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: "respiratory_rate",
				Kind: catalog.Sample, Start: start.Add(end.Sub(start) / 2), Value: *s.RespiratoryRate,
				Unit: "breaths/min", Device: strap.Fingerprint, Key: key})
		}
	}
	strain, err := b.strain(c.Cycle.Score)
	if err != nil {
		return err
	}
	var wake time.Time
	if main >= 0 {
		wake = spans[main][1]
	}
	add := func(metric string, v *float64, unit string) {
		if v != nil {
			b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: metric, Kind: catalog.DailyValue,
				Start: wake, End: &wake, Value: *v, Unit: unit, Device: strap.Fingerprint,
				Key: normalize.Key{RecordType: "cycle", ExternalID: id, Component: metric}})
		}
	}
	r := c.Recovery
	scored := r != nil && !isNull(r.Score) && (r.ScoreState == "" || r.ScoreState == "SCORED")
	flat := r != nil && r.RestingHeartRate != nil
	if main < 0 {
		if strain != nil || scored || flat {
			b.warn("cycle_without_main_sleep", id)
		}
		return nil
	}
	add("whoop_strain", strain, "index")
	switch {
	case scored: // nested score, RMSSD in ms
		var s struct {
			RecoveryScore    *float64 `json:"recovery_score"`
			RestingHeartRate *float64 `json:"resting_heart_rate"`
			HRVRMSSDMilli    *float64 `json:"hrv_rmssd_milli"`
		}
		if err := b.decode(r.Score, &s); err != nil {
			return err
		}
		add("whoop_recovery", s.RecoveryScore, "%")
		add("resting_heart_rate", s.RestingHeartRate, "bpm")
		add("hrv_rmssd_nightly", s.HRVRMSSDMilli, "ms")
	case flat: // flat BFF fields, RMSSD in s; a resting HR means it is scored
		add("whoop_recovery", r.RecoveryScore, "%")
		add("resting_heart_rate", r.RestingHeartRate, "bpm")
		add("hrv_rmssd_nightly", r.HRVRMSSD, "s")
	}
	return nil
}

// strain reads day strain from the inner cycle's score.
func (b *builder) strain(score json.RawMessage) (*float64, error) {
	if isNull(score) {
		return nil, nil
	}
	var f float64
	if json.Unmarshal(score, &f) == nil {
		return &f, nil
	}
	var s struct {
		Strain *float64 `json:"strain"`
	}
	if err := b.decode(score, &s); err != nil {
		return nil, err
	}
	return s.Strain, nil
}

func inlineTotals(s inlineSleep) *normalize.SleepTotals {
	t := &normalize.SleepTotals{Awake: secs(s.Wake), Light: secs(s.Light), Deep: secs(s.Deep), REM: secs(s.REM)}
	if t.Light != nil && t.Deep != nil && t.REM != nil {
		asleep := *t.Light + *t.Deep + *t.REM
		t.Asleep = &asleep
	}
	return t
}

func secs(ms *float64) *int32 {
	if ms == nil || *ms < 0 || *ms > 7*24*3600*1000 {
		return nil
	}
	v := int32(*ms / 1000)
	return &v
}

// mainSleepID is the one id of a cycle's main sleep: recovery.sleep_id, else sleep.id. When both
// are set and differ, sleep.id is ignored here and in the sidecar, which fetches whoop.sleep for
// this id only, so the episode is never written under both.
func (c rawCycle) mainSleepID() string {
	id := ""
	if c.Recovery != nil {
		id = externalID(c.Recovery.SleepID)
	}
	if id == "" && c.Sleep != nil {
		id = externalID(c.Sleep.ID)
	}
	return id
}

// ownsStages reports whether whoop.sleep writes the session of this sleep id. The sidecar fetches
// stages for the cycle's mainSleepID and for v2 sleep activities (UUIDs: naps, or a second id of
// the main sleep). Only numeric ids are owned there, so no episode is written twice; cycles writes
// the rest, with WHOOP's nap flag and stage totals but without stages.
func ownsStages(id string) bool {
	_, err := strconv.ParseUint(id, 10, 64)
	return err == nil
}

// sleepStages maps WHOOP sleep-event stages; no_data is a gap.
var sleepStages = map[string]string{"awake": "awake", "light": "light", "deep": "deep", "slow_wave": "deep", "rem": "rem"}

// sleep maps one sleep-events body (unit {"id"}) to the session with its stages, bounded by the
// record's start and end (or during) or else by its stages. Sleep ids that cycles owns stay raw.
// The older summary shape, if WHOOP sends it, adds stage totals, the offset and the sleep
// performance of a main sleep at its wake-up; respiration comes from cycles only. A rescored
// sleep keeps its id, so its rows are superseded.
func (b *builder) sleep(id string, resp json.RawMessage) error {
	var s struct {
		Start          *time.Time `json:"start"`
		End            *time.Time `json:"end"`
		During         string     `json:"during"`
		TimezoneOffset string     `json:"timezone_offset"`
		Nap            bool       `json:"nap"`
		Score          *struct {
			StageSummary *struct {
				Awake *float64 `json:"total_awake_time_milli"`
				Light *float64 `json:"total_light_sleep_time_milli"`
				Deep  *float64 `json:"total_slow_wave_sleep_time_milli"`
				REM   *float64 `json:"total_rem_sleep_time_milli"`
			} `json:"stage_summary"`
			SleepPerformance *float64 `json:"sleep_performance_percentage"`
		} `json:"score"`
		Stages []struct { // nil when absent
			Stage  string `json:"stage"`
			During string `json:"during"`
		} `json:"stages"`
	}
	if id == "" {
		return b.drift("sleep without unit id")
	}
	if err := b.decode(resp, &s); err != nil {
		return err
	}
	if !ownsStages(id) {
		return nil
	}
	start, end, bounded := during(s.During)
	if s.Start != nil && s.End != nil {
		start, end, bounded = s.Start.UTC(), s.End.UTC(), true
	}
	if s.Stages == nil && !bounded {
		return b.drift("sleep without stages or bounds")
	}
	var zone normalize.Zone
	if s.TimezoneOffset != "" {
		off, err := b.offset(s.TimezoneOffset)
		if err != nil {
			return err
		}
		zone.OffsetMin = &off
	}
	sess := normalize.SleepSession{Zone: zone, Nap: s.Nap, Device: strap.Fingerprint,
		Key: normalize.Key{RecordType: "sleep", ExternalID: id}}
	for _, st := range s.Stages {
		a, z, ok := during(st.During)
		if !ok || !z.After(a) {
			return b.drift("sleep stage without a valid during")
		}
		kind, known := sleepStages[st.Stage]
		switch {
		case st.Stage == "no_data":
		case !known:
			b.warn("unknown_stage", st.Stage)
		default:
			sess.Stages = append(sess.Stages, normalize.SleepStage{Stage: kind, Start: a, End: z})
			if sess.Start.IsZero() || a.Before(sess.Start) {
				sess.Start = a
			}
			if z.After(sess.End) {
				sess.End = z
			}
		}
	}
	if bounded {
		sess.Start, sess.End = start, end
	}
	if !sess.End.After(sess.Start) {
		if bounded {
			return b.drift("sleep ends before it starts")
		}
		return nil // not scored yet: no stages
	}
	if s.Score != nil && s.Score.StageSummary != nil {
		ss := s.Score.StageSummary
		sess.Totals = inlineTotals(inlineSleep{Wake: ss.Awake, Light: ss.Light, Deep: ss.Deep, REM: ss.REM})
	}
	b.out.Sleep = append(b.out.Sleep, sess)
	if s.Score != nil && s.Score.SleepPerformance != nil && !s.Nap {
		b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: "whoop_sleep_performance",
			Kind: catalog.Sample, Start: sess.End, Zone: zone, Value: *s.Score.SleepPerformance, Unit: "%",
			Device: strap.Fingerprint, Key: normalize.Key{RecordType: "sleep", ExternalID: id, Component: "whoop_sleep_performance"}})
	}
	return nil
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
	var zone normalize.Zone
	if w.TimezoneOffset != "" {
		off, err := b.offset(w.TimezoneOffset)
		if err != nil {
			return err
		}
		zone.OffsetMin = &off
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
