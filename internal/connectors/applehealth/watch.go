package applehealth

import (
	"cmp"
	"encoding/json"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// The Apple Watch types of the type registry v2 (docs/adr/0024-watch-data.md): beat-to-beat
// series, ECG recordings and workout routes (with their detail as blob documents), State of
// Mind, activity summaries and workout events and activities. No new tables: beats are
// rr_interval samples, the rest health_events, daily values and workout segments.

const (
	metricRR = "rr_interval"

	// activitySummaryBundle is the fixed source marker of activity summaries, which HealthKit
	// reports without a source; the summaries are Apple's own (native).
	activitySummaryBundle = "vitamux.activity-summary"
)

type stat struct {
	Avg *float64 `json:"avg"`
	Min *float64 `json:"min"`
	Max *float64 `json:"max"`
}

type workoutEvent struct {
	Type     int                        `json:"type"`
	Start    time.Time                  `json:"start"`
	End      time.Time                  `json:"end"`
	Metadata map[string]json.RawMessage `json:"metadata"`
}

type workoutActivity struct {
	UUID                 string                     `json:"uuid"`
	ActivityType         int                        `json:"activity_type"`
	LocationType         *int                       `json:"location_type"`
	SwimmingLocationType *int                       `json:"swimming_location_type"`
	LapLengthM           *float64                   `json:"lap_length_m"`
	Start                time.Time                  `json:"start"`
	End                  *time.Time                 `json:"end"`
	DurationS            float64                    `json:"duration_s"`
	Totals               map[string]float64         `json:"totals"`
	Stats                map[string]stat            `json:"stats"`
	Events               []workoutEvent             `json:"events"`
	Metadata             map[string]json.RawMessage `json:"metadata"`
}

type ecgInfo struct {
	Classification      int       `json:"classification"`
	SymptomsStatus      int       `json:"symptoms_status"`
	AverageHeartRate    *float64  `json:"average_heart_rate"`
	SamplingFrequencyHz *float64  `json:"sampling_frequency_hz"`
	Lead                *int      `json:"lead"`
	VoltageCount        int       `json:"voltage_count"`
	VoltageUnit         string    `json:"voltage_unit"`
	Voltages            []float64 `json:"voltages"`
	OffsetsS            []float64 `json:"offsets_s"`
}

type beatsInfo struct {
	Count         int       `json:"count"`
	OffsetsS      []float64 `json:"offsets_s"`
	PrecededByGap []bool    `json:"preceded_by_gap"`
}

// routeInfo is the payload's route: CoreLocation values as given, in parallel arrays.
type routeInfo struct {
	Count                int       `json:"count"`
	OffsetsS             []float64 `json:"offsets_s"`
	Latitude             []float64 `json:"latitude"`
	Longitude            []float64 `json:"longitude"`
	AltitudeM            []float64 `json:"altitude_m,omitempty"`
	EllipsoidalAltitudeM []float64 `json:"ellipsoidal_altitude_m,omitempty"`
	HorizontalAccuracyM  []float64 `json:"horizontal_accuracy_m,omitempty"`
	VerticalAccuracyM    []float64 `json:"vertical_accuracy_m,omitempty"`
	SpeedMps             []float64 `json:"speed_mps,omitempty"`
	SpeedAccuracyMps     []float64 `json:"speed_accuracy_mps,omitempty"`
	CourseDeg            []float64 `json:"course_deg,omitempty"`
	CourseAccuracyDeg    []float64 `json:"course_accuracy_deg,omitempty"`
}

type stateOfMind struct {
	Kind                  int     `json:"kind"`
	Valence               float64 `json:"valence"`
	ValenceClassification *int    `json:"valence_classification"`
	Labels                []int   `json:"labels"`
	Associations          []int   `json:"associations"`
}

type activitySummary struct {
	Date                 string   `json:"date"`
	MoveMode             *int     `json:"move_mode"`
	Paused               *bool    `json:"paused"`
	ActiveEnergyKcal     *float64 `json:"active_energy_kcal"`
	ActiveEnergyGoalKcal *float64 `json:"active_energy_goal_kcal"`
	MoveTimeS            *float64 `json:"move_time_s"`
	MoveTimeGoalS        *float64 `json:"move_time_goal_s"`
	ExerciseTimeS        *float64 `json:"exercise_time_s"`
	ExerciseTimeGoalS    *float64 `json:"exercise_time_goal_s"`
	StandHours           *float64 `json:"stand_hours"`
	StandHoursGoal       *float64 `json:"stand_hours_goal"`
}

// Raw enum values to words (HealthKit SDK headers; ADR-0024). An unknown value keeps its number.
var (
	ecgClassifications = map[int]string{0: "not_set", 1: "sinus_rhythm", 2: "atrial_fibrillation",
		3: "inconclusive_low_heart_rate", 4: "inconclusive_high_heart_rate", 5: "inconclusive_poor_reading",
		6: "inconclusive_other", 100: "unrecognized"}
	ecgSymptoms   = map[int]string{0: "not_set", 1: "none", 2: "present"}
	ecgLeads      = map[int]string{1: "apple_watch_similar_to_lead_i"}
	moodKinds     = map[int]string{1: "momentary_emotion", 2: "daily_mood"}
	moodValences  = levelsFrom(1, []string{"very_unpleasant", "unpleasant", "slightly_unpleasant", "neutral", "slightly_pleasant", "pleasant", "very_pleasant"})
	moodLabels    = levelsFrom(1, []string{"amazed", "amused", "angry", "anxious", "ashamed", "brave", "calm", "content", "disappointed", "discouraged", "disgusted", "embarrassed", "excited", "frustrated", "grateful", "guilty", "happy", "hopeless", "irritated", "jealous", "joyful", "lonely", "passionate", "peaceful", "proud", "relieved", "sad", "scared", "stressed", "surprised", "worried", "annoyed", "confident", "drained", "hopeful", "indifferent", "overwhelmed", "satisfied"})
	moodAssocs    = levelsFrom(1, []string{"community", "current_events", "dating", "education", "family", "fitness", "friends", "health", "hobbies", "identity", "money", "partner", "self_care", "spirituality", "tasks", "travel", "work", "weather"})
	moveModes     = map[int]string{1: "active_energy", 2: "move_time"}
	locationTypes = map[int]string{1: "unknown", 2: "indoor", 3: "outdoor"}
	swimLocations = map[int]string{0: "unknown", 1: "pool", 2: "open_water"}
)

// HKWorkoutEventType raw values.
const (
	evPause = iota + 1
	evResume
	evLap
	evMarker
	evMotionPaused
	evMotionResumed
	evSegment
	evPauseOrResumeRequest
)

// word maps a raw enum value; ok is false for an unknown value, which keeps its number.
func word(m map[int]string, v int) (string, bool) {
	if w, ok := m[v]; ok {
		return w, true
	}
	return "unknown_" + strconv.Itoa(v), false
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil { // only maps and slices of strings, numbers and valid JSON reach here
		return nil
	}
	return b
}

func finitePtr(f *float64) *float64 {
	if f == nil || !finite(*f) {
		return nil
	}
	v := *f
	return &v
}

// startOffset is the zone of the offset the start timestamp carries.
func startOffset(s sample) normalize.Zone {
	_, off := s.Start.Zone()
	m := int16(off / 60) //nolint:gosec // a UTC offset is within ±18 h
	return normalize.Zone{OffsetMin: &m}
}

func offset(start time.Time, s float64) time.Time {
	return start.Add(time.Duration(math.Round(s * float64(time.Second))))
}

// beats maps a heartbeat series to rr_interval samples (s): one per beat after the first at the
// beat's time, skipping beats preceded by a gap, keyed <uuid>#<beat index>.
func (b *builder) beats(s sample) {
	if !b.valid(s) {
		return
	}
	bt := s.Beats
	if bt == nil || bt.Count != len(bt.OffsetsS) || bt.Count != len(bt.PrecededByGap) {
		b.warn("bad_beats", s.UUID)
		return
	}
	id := strings.ToUpper(s.UUID)
	zone, fl := b.zone(s), flags(s)
	device, origin := b.source(s)
	for i := 1; i < bt.Count; i++ {
		rr := bt.OffsetsS[i] - bt.OffsetsS[i-1]
		if bt.PrecededByGap[i] {
			continue
		}
		if !finite(rr) || rr <= 0 {
			b.warn("bad_beats", s.UUID)
			continue
		}
		b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: metricRR, Kind: catalog.Sample,
			Start: offset(s.Start, bt.OffsetsS[i]), Zone: zone, Value: rr, Unit: "s", Flags: fl, Device: device, Origin: origin,
			Key: normalize.Key{RecordType: b.typ, ExternalID: id + "#" + strconv.Itoa(i)}})
	}
}

// file adds a blob document and returns its hash.
func (b *builder) file(doc any) []byte {
	f, err := normalize.NewFile(doc)
	if err != nil {
		return nil
	}
	if !slices.ContainsFunc(b.out.Files, func(x normalize.File) bool { return string(x.SHA256) == string(f.SHA256) }) {
		b.out.Files = append(b.out.Files, f)
	}
	return f.SHA256
}

// newEvent is an event of the sample's time, zone, flags, source and key.
func (b *builder) newEvent(s sample, code string) normalize.Event {
	e := normalize.Event{Code: code, Start: s.Start, Zone: b.zone(s), Flags: flags(s), Key: b.key(s)}
	if s.End.After(s.Start) {
		e.End = &s.End
	}
	e.Device, e.Origin = b.source(s)
	return e
}

// waveformDoc is vitamux.waveform/1.
type waveformDoc struct {
	Format              string    `json:"format"`
	Start               time.Time `json:"start"`
	SamplingFrequencyHz *float64  `json:"sampling_frequency_hz,omitempty"`
	Unit                string    `json:"unit"`
	Lead                string    `json:"lead,omitempty"`
	Values              []float64 `json:"values"`
	OffsetsS            []float64 `json:"offsets_s,omitempty"`
}

// ecg maps an electrocardiogram to an ecg_recording event: Apple's classification as level, the
// average heart rate as value, and its voltages as a waveform document.
func (b *builder) ecg(s sample) {
	if !b.valid(s) {
		return
	}
	g := s.ECG
	if g == nil || g.VoltageUnit != "mcV" || g.VoltageCount != len(g.Voltages) ||
		(len(g.OffsetsS) > 0 && len(g.OffsetsS) != len(g.Voltages)) {
		b.warn("bad_ecg", s.UUID)
		return
	}
	e := b.newEvent(s, "ecg_recording")
	level, ok := word(ecgClassifications, g.Classification)
	if !ok {
		b.warn("unknown_ecg_classification", s.UUID)
		level = ""
	}
	e.Level, e.Value = level, finitePtr(g.AverageHeartRate)
	symptoms, _ := word(ecgSymptoms, g.SymptomsStatus)
	ctx := map[string]any{"symptoms_status": symptoms, "voltage_count": g.VoltageCount}
	doc := waveformDoc{Format: catalog.FileWaveform, Start: s.Start, SamplingFrequencyHz: finitePtr(g.SamplingFrequencyHz),
		Unit: "µV", Values: g.Voltages, OffsetsS: g.OffsetsS}
	if doc.SamplingFrequencyHz != nil {
		ctx["sampling_frequency_hz"] = *doc.SamplingFrequencyHz
	}
	if g.Lead != nil {
		doc.Lead, _ = word(ecgLeads, *g.Lead)
		ctx["lead"] = doc.Lead
	}
	if v, ok := s.Metadata["HKMetadataKeyAppleECGAlgorithmVersion"]; ok {
		ctx["algorithm_version"] = v
	}
	if doc.Values == nil {
		doc.Values = []float64{}
	}
	e.Context, e.FileSHA256 = mustJSON(ctx), b.file(doc)
	b.out.Events = append(b.out.Events, e)
}

// routeDoc is vitamux.route/1: the payload's route plus its start.
type routeDoc struct {
	Format string    `json:"format"`
	Start  time.Time `json:"start"`
	routeInfo
}

// route maps a workout route to a workout_route event with a route document. The workout finds
// it by context.workout_uuid, else by time (GET /workouts/{id}/route), whatever the arrival order.
func (b *builder) route(s sample) {
	if !b.valid(s) {
		return
	}
	r := s.Route
	if r == nil || r.Count == 0 {
		b.warn("bad_route", s.UUID)
		return
	}
	for _, a := range [][]float64{r.OffsetsS, r.Latitude, r.Longitude} {
		if len(a) != r.Count {
			b.warn("bad_route", s.UUID)
			return
		}
	}
	for _, a := range [][]float64{r.AltitudeM, r.EllipsoidalAltitudeM, r.HorizontalAccuracyM, r.VerticalAccuracyM,
		r.SpeedMps, r.SpeedAccuracyMps, r.CourseDeg, r.CourseAccuracyDeg} {
		if a != nil && len(a) != r.Count {
			b.warn("bad_route", s.UUID)
			return
		}
	}
	e := b.newEvent(s, "workout_route")
	ctx := map[string]any{"point_count": r.Count}
	if s.WorkoutUUID != "" {
		ctx["workout_uuid"] = strings.ToUpper(s.WorkoutUUID)
	}
	e.Context, e.FileSHA256 = mustJSON(ctx), b.file(routeDoc{Format: catalog.FileRoute, Start: s.Start, routeInfo: *r})
	b.out.Events = append(b.out.Events, e)
}

// stateOfMind maps HKStateOfMind to a state_of_mind event: the kind as level, the valence as
// value, and the classification, labels and associations as words in context.
func (b *builder) stateOfMind(s sample) {
	if !b.valid(s) {
		return
	}
	m := s.StateOfMind
	if m == nil {
		b.warn("bad_state_of_mind", s.UUID)
		return
	}
	kind, ok := moodKinds[m.Kind]
	if !ok || !finite(m.Valence) || m.Valence < -1 || m.Valence > 1 {
		b.warn("bad_state_of_mind", s.UUID)
		return
	}
	e := b.newEvent(s, "state_of_mind")
	v := m.Valence
	e.Level, e.Value = kind, &v
	ctx := map[string]any{}
	if m.ValenceClassification != nil {
		ctx["valence_classification"] = b.words(moodValences, []int{*m.ValenceClassification}, s.UUID)[0]
	}
	if len(m.Labels) > 0 {
		ctx["labels"] = b.words(moodLabels, m.Labels, s.UUID)
	}
	if len(m.Associations) > 0 {
		ctx["associations"] = b.words(moodAssocs, m.Associations, s.UUID)
	}
	if len(ctx) > 0 {
		e.Context = mustJSON(ctx)
	}
	b.out.Events = append(b.out.Events, e)
}

// words maps raw values, warning once per sample about unknown ones.
func (b *builder) words(m map[int]string, vs []int, uuid string) []string {
	out := make([]string, len(vs))
	warned := false
	for i, v := range vs {
		var ok bool
		if out[i], ok = word(m, v); !ok && !warned {
			b.warn("unknown_enum_value", uuid)
			warned = true
		}
	}
	return out
}

// activitySummary maps one day's rings to daily values of active_energy, move_time,
// exercise_time and stand_hours on the summary's date, each with its goal, the move mode and the
// paused flag in context, keyed by the day's UUID and the code: a changed day supersedes.
func (b *builder) activitySummary(s sample) {
	if !b.valid(s) {
		return
	}
	a := s.ActivitySummary
	if a == nil || !s.End.After(s.Start) {
		b.warn("bad_activity_summary", s.UUID)
		return
	}
	// start is the local midnight of the summary's day, so its own offset gives local_date = date.
	zone := startOffset(s)
	if s.Start.Format(time.DateOnly) != a.Date {
		b.warn("bad_activity_summary", s.UUID)
		return
	}
	common := map[string]any{}
	if a.MoveMode != nil {
		common["move_mode"], _ = word(moveModes, *a.MoveMode)
	}
	if a.Paused != nil {
		common["paused"] = *a.Paused
	}
	device, origin := b.source(s)
	for _, r := range []struct {
		code, unit  string
		value, goal *float64
	}{
		{"active_energy", "kcal", a.ActiveEnergyKcal, a.ActiveEnergyGoalKcal},
		{"move_time", "s", a.MoveTimeS, a.MoveTimeGoalS},
		{"exercise_time", "s", a.ExerciseTimeS, a.ExerciseTimeGoalS},
		{"stand_hours", "count", a.StandHours, a.StandHoursGoal},
	} {
		if r.value == nil || !finite(*r.value) {
			continue
		}
		ctx := maps.Clone(common)
		if g := finitePtr(r.goal); g != nil {
			ctx["goal"] = *g
		}
		m := normalize.Measurement{Metric: r.code, Kind: catalog.DailyValue, Start: s.Start, End: &s.End, Zone: zone,
			Value: *r.value, Unit: r.unit, Device: device, Origin: origin,
			Key: normalize.Key{RecordType: b.typ, ExternalID: strings.ToUpper(s.UUID), Component: r.code}}
		if len(ctx) > 0 {
			m.Context = mustJSON(ctx)
		}
		b.out.Measurements = append(b.out.Measurements, m)
	}
}

// segments maps a workout's events and activities to workout_segments, ordered by start: laps
// (lap), segments (interval), the activities of a multisport workout (activity, totals and stats
// in data), pause to resume
// and motion-pause pairs (pause), markers and pause or resume requests (marker).
func (b *builder) segments(s sample) []normalize.Segment {
	w := s.Workout
	segs := b.eventSegments(s, w.Events, "")
	for _, a := range w.Activities {
		if a.Start.IsZero() || (a.End != nil && a.End.Before(a.Start)) {
			b.warn("bad_workout_activity", s.UUID)
			continue
		}
		sp, provider, known := sport(a.ActivityType)
		if !known {
			b.warn("unknown_activity_type", provider)
		}
		data := map[string]any{"uuid": strings.ToUpper(a.UUID), "sport": sp, "provider_sport": provider, "duration_s": a.DurationS}
		if a.LocationType != nil {
			data["location"], _ = word(locationTypes, *a.LocationType)
		}
		if a.SwimmingLocationType != nil {
			data["swimming_location"], _ = word(swimLocations, *a.SwimmingLocationType)
		}
		if v := finitePtr(a.LapLengthM); v != nil {
			data["lap_length_m"] = *v
		}
		for k, v := range map[string]any{"totals": a.Totals, "stats": a.Stats, "metadata": a.Metadata} {
			if !emptyMap(v) {
				data[k] = v
			}
		}
		// HealthKit reports a single-activity workout as one activity: that segment would only
		// repeat the workout, so only a multisport workout stores its activities.
		if len(w.Activities) > 1 {
			segs = append(segs, normalize.Segment{Kind: "activity", Start: a.Start, End: a.End, Data: mustJSON(data)})
		}
		segs = append(segs, b.eventSegments(s, a.Events, strings.ToUpper(a.UUID))...)
	}
	slices.SortStableFunc(segs, func(x, y normalize.Segment) int { return cmp.Compare(x.Start.UnixNano(), y.Start.UnixNano()) })
	return segs
}

func emptyMap(v any) bool {
	switch m := v.(type) {
	case map[string]float64:
		return len(m) == 0
	case map[string]stat:
		return len(m) == 0
	case map[string]json.RawMessage:
		return len(m) == 0
	}
	return true
}

// eventSegments maps HKWorkoutEvents in order; activity names the activity they belong to.
func (b *builder) eventSegments(s sample, evs []workoutEvent, activity string) []normalize.Segment {
	var out []normalize.Segment
	open := map[int]time.Time{} // pause kind (evPause, evMotionPaused) -> start of the open pause
	data := func(m map[string]any, ev workoutEvent) json.RawMessage {
		if activity != "" {
			m["activity_uuid"] = activity
		}
		if len(ev.Metadata) > 0 {
			m["metadata"] = ev.Metadata
		}
		if len(m) == 0 {
			return nil
		}
		return mustJSON(m)
	}
	pause := func(start, end time.Time, typ string, ev workoutEvent) {
		out = append(out, normalize.Segment{Kind: "pause", Start: start, End: &end, Data: data(map[string]any{"type": typ}, ev)})
	}
	for _, ev := range evs {
		if ev.Start.IsZero() || ev.End.Before(ev.Start) {
			b.warn("bad_workout_event", s.UUID)
			continue
		}
		var end *time.Time
		if ev.End.After(ev.Start) {
			end = &ev.End
		}
		switch ev.Type {
		case evLap:
			out = append(out, normalize.Segment{Kind: "lap", Start: ev.Start, End: end, Data: data(map[string]any{}, ev)})
		case evSegment:
			out = append(out, normalize.Segment{Kind: "interval", Start: ev.Start, End: end, Data: data(map[string]any{}, ev)})
		case evMarker:
			out = append(out, normalize.Segment{Kind: "marker", Start: ev.Start, End: end, Data: data(map[string]any{"type": "marker"}, ev)})
		case evPauseOrResumeRequest:
			out = append(out, normalize.Segment{Kind: "marker", Start: ev.Start, End: end,
				Data: data(map[string]any{"type": "pause_or_resume_request"}, ev)})
		case evPause, evMotionPaused:
			if _, ok := open[ev.Type]; !ok {
				open[ev.Type] = ev.Start
			}
		case evResume, evMotionResumed:
			from := ev.Type - 1 // evResume pairs with evPause, evMotionResumed with evMotionPaused
			if start, ok := open[from]; ok && !ev.Start.Before(start) {
				pause(start, ev.Start, pauseType(from), ev)
				delete(open, from)
			}
		default:
			b.warn("unknown_workout_event", s.UUID)
		}
	}
	// A pause the workout ended in lasts until its end.
	for _, typ := range []int{evPause, evMotionPaused} {
		if start, ok := open[typ]; ok && !s.End.Before(start) {
			pause(start, s.End, pauseType(typ), workoutEvent{})
		}
	}
	return out
}

func pauseType(t int) string {
	if t == evMotionPaused {
		return "motion_pause"
	}
	return "pause"
}
