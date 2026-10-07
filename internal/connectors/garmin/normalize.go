// Package garmin holds the Garmin Connect connector's server side. The python-garminconnect
// sidecar (E18) stores each endpoint's JSON verbatim as {"unit": {...}, "response": <JSON>};
// this file turns those raw payloads into canonical rows, one normalizer per stream.
package garmin

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

const (
	Provider = "garmin"

	StreamDailySummary      = "garmin.daily_summary"       // get_user_summary
	StreamHeartRate         = "garmin.heart_rate"          // get_heart_rates
	StreamSteps             = "garmin.steps"               // get_steps_data
	StreamStressBodyBattery = "garmin.stress_body_battery" // get_stress_data
	StreamSleep             = "garmin.sleep"               // get_sleep_data
	StreamHRV               = "garmin.hrv"                 // get_hrv_data
	StreamRespiration       = "garmin.respiration"         // get_respiration_data
	StreamSpO2              = "garmin.spo2"                // get_spo2_data
	StreamTraining          = "garmin.training"            // get_max_metrics, get_training_readiness, get_training_status
	StreamFloors            = "garmin.floors"              // get_floors
	StreamHydration         = "garmin.hydration"           // get_hydration_data
	StreamFitnessAge        = "garmin.fitness_age"         // get_fitnessage_data
	StreamBodyComposition   = "garmin.body_composition"    // get_body_composition
	StreamBloodPressure     = "garmin.blood_pressure"      // get_blood_pressure
	StreamActivities        = "garmin.activities"          // get_activities_by_date (JSON) and FIT downloads (binary)
)

// streams maps each stream to its normalizer version and decoder. Bump a stream's version on any
// change to its output; the golden tests enforce it.
var streams = map[string]struct {
	version int
	decode  func(b *builder, resp []byte) error
}{
	StreamDailySummary:      {4, dailySummary},
	StreamHeartRate:         {2, heartRate},
	StreamSteps:             {3, steps},
	StreamStressBodyBattery: {2, stressBodyBattery},
	StreamSleep:             {3, sleep},
	StreamHRV:               {3, hrv},
	StreamRespiration:       {2, respiration},
	StreamSpO2:              {3, spo2},
	StreamTraining:          {5, training},
	StreamFloors:            {1, floors},
	StreamHydration:         {1, hydration},
	StreamFitnessAge:        {1, fitnessAge},
	StreamBodyComposition:   {2, bodyComposition},
	StreamBloodPressure:     {1, bloodPressure},
	StreamActivities:        {4, activity},
}

// Normalizer normalizes one garmin.* stream; its ID is the stream name.
type Normalizer struct{ stream string }

// Normalizers returns one normalizer per Garmin stream, sorted by ID.
func Normalizers() []normalize.Normalizer {
	ns := make([]normalize.Normalizer, 0, len(streams))
	for s := range streams {
		ns = append(ns, Normalizer{s})
	}
	slices.SortFunc(ns, func(a, b normalize.Normalizer) int { return strings.Compare(a.ID(), b.ID()) })
	return ns
}

func (n Normalizer) ID() string                    { return n.stream }
func (n Normalizer) Version() int                  { return streams[n.stream].version }
func (n Normalizer) Accepts(stream, _ string) bool { return stream == n.stream }

// Normalize is pure. A record Garmin reports in both GMT and local time carries that offset, so
// its local date is Garmin's; intraday series carry their day's offset unless it changes within the
// day (DST), and records without local time fall back to the owner's timezone periods.
// An empty response ({}, [] or null: no data for the unit) is no output. FIT files on
// garmin.activities stay raw.
func (n Normalizer) Normalize(_ context.Context, raw normalize.RawPayload, _ normalize.Env) (normalize.Output, error) {
	s, ok := streams[n.stream]
	if !ok {
		return normalize.Output{}, fmt.Errorf("garmin: unknown stream %q", n.stream)
	}
	if n.stream == StreamActivities && !strings.Contains(raw.ContentType, "json") {
		return normalize.Output{}, nil // a FIT download: decoding it is later work
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw.Body, &env); err != nil {
		return normalize.Output{}, errors.New(n.stream + ": undecodable raw envelope")
	}
	resp, ok := env["response"]
	if !ok {
		return normalize.Output{}, drift(n.stream, "response")
	}
	b := &builder{stream: n.stream}
	if empty(resp) {
		return b.out, nil
	}
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	_ = json.Unmarshal(raw.RequestMeta, &req) // optional: only garmin.training reads it
	b.endpoint = req.Endpoint
	var unit struct {
		Date string `json:"date"`
	}
	_ = json.Unmarshal(env["unit"], &unit) // optional: only garmin.fitness_age reads it
	b.date = unit.Date
	if err := s.decode(b, resp); err != nil {
		return normalize.Output{}, err
	}
	return b.out, nil
}

// empty reports an empty day: Garmin answers 204 (stored as {}), null or [].
func empty(resp []byte) bool {
	switch string(bytes.TrimSpace(resp)) {
	case "", "null", "{}", "[]":
		return true
	}
	return false
}

// drift is a response without a required field, or with it in another JSON type. It names the
// field, never a value.
func drift(stream, field string) error {
	return fmt.Errorf("%s: schema drift: field %s missing or retyped", stream, field)
}

// builder collects one payload's output.
type builder struct {
	stream   string
	endpoint string // request_meta endpoint, when stored
	date     string // the unit's calendar date, when it has one
	out      normalize.Output
}

// decode unmarshals resp into v, reporting a retyped field as drift.
func (b *builder) decode(resp []byte, v any) error {
	if err := json.Unmarshal(resp, v); err != nil {
		if te, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
			return drift(b.stream, cmp.Or(te.Field, "response"))
		}
		return errors.New(b.stream + ": undecodable response")
	}
	return nil
}

func (b *builder) device(id *int64) string {
	if id == nil || *id == 0 {
		return ""
	}
	fp := strconv.FormatInt(*id, 10)
	if !slices.ContainsFunc(b.out.Devices, func(d normalize.Device) bool { return d.Fingerprint == fp }) {
		b.out.Devices = append(b.out.Devices, normalize.Device{Fingerprint: fp, Manufacturer: "Garmin"})
	}
	return fp
}

// wearable is the device behind Garmin's wellness data (heart rate, steps, stress, sleep, ...).
// Those responses do not name it, so one stable fingerprint of type watch stands for the
// account's wrist device; device-type rules such as builtin:steps need a type. Scale and
// blood-pressure readings and activities keep the device Garmin names.
func (b *builder) wearable() string {
	const fp = "garmin:wearable"
	if !slices.ContainsFunc(b.out.Devices, func(d normalize.Device) bool { return d.Fingerprint == fp }) {
		b.out.Devices = append(b.out.Devices, normalize.Device{Fingerprint: fp, Type: "watch", Manufacturer: "Garmin"})
	}
	return fp
}

// sample adds a wellness sample, on the device Garmin names or else the wearable.
func (b *builder) sample(metric string, at time.Time, z normalize.Zone, v float64, unit, device string) {
	if device == "" {
		device = b.wearable()
	}
	b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: metric, Kind: catalog.Sample,
		Start: at, Zone: z, Value: v, Unit: unit, Device: device})
}

// daily adds a provider daily value over [start, end), keyed by record type and calendar date.
func (b *builder) daily(metric, record, date string, start, end time.Time, z normalize.Zone, v float64, unit string) {
	b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: metric, Kind: catalog.DailyValue,
		Start: start, End: &end, Zone: z, Value: v, Unit: unit, Device: b.wearable(),
		Key: normalize.Key{RecordType: record, ExternalID: date, Component: metric}})
}

// gtime is a Garmin timestamp string ("2026-06-15T05:00:00.0", activities "2026-06-15 05:00:00"),
// in GMT or local wall time as its field name says. It decodes as that wall time in UTC.
type gtime struct{ time.Time }

func (t *gtime) UnmarshalJSON(data []byte) error {
	var s *string
	if err := json.Unmarshal(data, &s); err != nil || s == nil {
		return err
	}
	p, err := time.Parse("2006-01-02T15:04:05.999999999", strings.Replace(*s, " ", "T", 1))
	if err != nil {
		return errors.New("garmin: bad timestamp")
	}
	t.Time = p
	return nil
}

// zoneAt is the offset of an instant Garmin gives in both GMT and local wall time. Offsets outside
// the real -12..+14 h, or not on a quarter hour, are no offset: some accounts get the local fields
// shifted twice (docs/providers/garmin.md#time), and then the owner's timezone periods decide.
func zoneAt(gmt, local time.Time) normalize.Zone {
	d := local.Sub(gmt)
	if gmt.IsZero() || local.IsZero() || d%(15*time.Minute) != 0 || d < -12*time.Hour || d > 14*time.Hour {
		return normalize.Zone{}
	}
	m := int16(d / time.Minute)
	return normalize.Zone{OffsetMin: &m}
}

// localDay is the UTC span of a calendar date at the zone's offset; ok is false without both.
func localDay(date string, z normalize.Zone) (start, end time.Time, ok bool) {
	d, err := time.Parse(time.DateOnly, date)
	if err != nil || z.OffsetMin == nil {
		return time.Time{}, time.Time{}, false
	}
	start = d.Add(-time.Duration(*z.OffsetMin) * time.Minute)
	return start, start.AddDate(0, 0, 1), true
}

func millis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// series is a Garmin values array: rows of [timestamp ms, value, ...] whose columns a descriptor
// list names. JSON nulls decode as nil.
type series [][]any

// each calls fn with every row's instant and numeric value in column col; rows without either are
// no reading and are skipped.
func (s series) each(tcol, col int, fn func(at time.Time, v float64)) {
	for _, row := range s {
		if len(row) <= max(tcol, col) {
			continue
		}
		ts, ok1 := row[tcol].(float64)
		v, ok2 := row[col].(float64)
		if ok1 && ok2 && !math.IsNaN(v) && !math.IsInf(v, 0) && ts > 0 && ts < 1e15 {
			fn(millis(int64(ts)), v)
		}
	}
}

// descriptor names one column of a values array.
type descriptor struct {
	Key   string `json:"key"`
	Index int    `json:"index"`
}

// columns returns the indexes of keys in ds, or drift naming the list when one is missing.
func (b *builder) columns(list string, ds []descriptor, keys ...string) ([]int, error) {
	idx := make([]int, len(keys))
	for i, k := range keys {
		j := slices.IndexFunc(ds, func(d descriptor) bool { return d.Key == k })
		if j < 0 || ds[j].Index < 0 {
			return nil, drift(b.stream, list+"."+k)
		}
		idx[i] = ds[j].Index
	}
	return idx, nil
}

// dayBounds are the GMT and local bounds of a Garmin wellness day.
type dayBounds struct {
	CalendarDate string `json:"calendarDate"`
	StartGMT     gtime  `json:"startTimestampGMT"`
	EndGMT       gtime  `json:"endTimestampGMT"`
	StartLocal   gtime  `json:"startTimestampLocal"`
	EndLocal     gtime  `json:"endTimestampLocal"`
}

// zone is the offset of the day's intraday series: the day's offset, or none when it changes
// within the day, so a DST day's local dates come from the owner's timezone periods.
func (d dayBounds) zone() normalize.Zone {
	s, e := zoneAt(d.StartGMT.Time, d.StartLocal.Time), zoneAt(d.EndGMT.Time, d.EndLocal.Time)
	if s.OffsetMin == nil || e.OffsetMin == nil || *s.OffsetMin != *e.OffsetMin {
		return normalize.Zone{}
	}
	return s
}

// dailySummary maps the daily totals that have a catalogue code. Resting HR comes from
// garmin.heart_rate, stress and Body Battery levels from their series; the stress, heart-rate and
// SpO2 summaries are derivable and stay raw. A day without wellness data (includesWellnessData
// false, wellnessStartTimeGmt and the totals null) is no output.
func dailySummary(b *builder, resp []byte) error {
	var r struct {
		CalendarDate string   `json:"calendarDate"`
		HasWellness  *bool    `json:"includesWellnessData"`
		StartGMT     gtime    `json:"wellnessStartTimeGmt"`
		StartLocal   gtime    `json:"wellnessStartTimeLocal"`
		EndGMT       gtime    `json:"wellnessEndTimeGmt"`
		TotalSteps   *float64 `json:"totalSteps"`
		Distance     *float64 `json:"totalDistanceMeters"`
		ActiveKcal   *float64 `json:"activeKilocalories"`
		BMRKcal      *float64 `json:"bmrKilocalories"`
		TotalKcal    *float64 `json:"totalKilocalories"`
		Floors       *float64 `json:"floorsAscended"`
		ModerateMin  *float64 `json:"moderateIntensityMinutes"`
		VigorousMin  *float64 `json:"vigorousIntensityMinutes"`
		Sedentary    *float64 `json:"sedentarySeconds"`
		BBCharged    *float64 `json:"bodyBatteryChargedValue"`
		BBDrained    *float64 `json:"bodyBatteryDrainedValue"`
	}
	if err := b.decode(resp, &r); err != nil {
		return err
	}
	switch {
	case r.CalendarDate == "":
		return drift(b.stream, "calendarDate")
	case r.HasWellness != nil && !*r.HasWellness:
		return nil
	case r.StartGMT.IsZero():
		return drift(b.stream, "wellnessStartTimeGmt")
	case !r.EndGMT.After(r.StartGMT.Time):
		return drift(b.stream, "wellnessEndTimeGmt")
	}
	z := zoneAt(r.StartGMT.Time, r.StartLocal.Time)
	minutes := func(v *float64) *float64 { // intensity minutes are stored in seconds
		if v == nil {
			return nil
		}
		return new(*v * 60)
	}
	for _, m := range []struct {
		metric, unit string
		v            *float64
	}{{"steps", "count", r.TotalSteps}, {"distance_walk_run", "m", r.Distance},
		{"active_energy", "kcal", r.ActiveKcal}, {"basal_energy", "kcal", r.BMRKcal}, {"total_energy", "kcal", r.TotalKcal},
		{"floors_climbed", "count", r.Floors},
		{"intensity_moderate_time", "s", minutes(r.ModerateMin)}, {"intensity_vigorous_time", "s", minutes(r.VigorousMin)},
		{"sedentary_time", "s", r.Sedentary},
		{"garmin_body_battery_charged", "index", r.BBCharged}, {"garmin_body_battery_drained", "index", r.BBDrained}} {
		if m.v != nil {
			b.daily(m.metric, "daily_summary", r.CalendarDate, r.StartGMT.Time, r.EndGMT.Time, z, *m.v, m.unit)
		}
	}
	return nil
}

// heartRate maps the intraday series at its native interval and the day's resting HR.
func heartRate(b *builder, resp []byte) error {
	var r struct {
		dayBounds
		Resting     *float64     `json:"restingHeartRate"`
		Descriptors []descriptor `json:"heartRateValueDescriptors"`
		Values      series       `json:"heartRateValues"`
	}
	if err := b.decode(resp, &r); err != nil {
		return err
	}
	if r.Resting == nil && len(r.Values) == 0 {
		return nil
	}
	if r.StartGMT.IsZero() || !r.EndGMT.After(r.StartGMT.Time) {
		return drift(b.stream, "startTimestampGMT")
	}
	z := r.zone()
	if len(r.Values) > 0 {
		c, err := b.columns("heartRateValueDescriptors", r.Descriptors, "timestamp", "heartrate")
		if err != nil {
			return err
		}
		r.Values.each(c[0], c[1], func(at time.Time, v float64) { b.sample("heart_rate", at, z, v, "bpm", "") })
	}
	if r.Resting != nil {
		if r.CalendarDate == "" {
			return drift(b.stream, "calendarDate")
		}
		b.daily("resting_heart_rate", "daily_heart_rate", r.CalendarDate, r.StartGMT.Time, r.EndGMT.Time,
			zoneAt(r.StartGMT.Time, r.StartLocal.Time), *r.Resting, "bpm")
	}
	return nil
}

// steps maps the 15-minute chart as intervals, and its wheelchair pushes. They have no local time, so their local dates come
// from the owner's timezone periods; the day's total is garmin.daily_summary's daily value.
func steps(b *builder, resp []byte) error {
	var r []struct {
		StartGMT gtime    `json:"startGMT"`
		EndGMT   gtime    `json:"endGMT"`
		Steps    *float64 `json:"steps"`
		Pushes   *float64 `json:"pushes"`
	}
	if err := b.decode(resp, &r); err != nil {
		return err
	}
	for _, x := range r {
		switch {
		case x.StartGMT.IsZero() || !x.EndGMT.After(x.StartGMT.Time):
			return drift(b.stream, "[].startGMT")
		case x.Steps == nil:
			return drift(b.stream, "[].steps")
		}
		end := x.EndGMT.Time
		b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: "steps", Kind: catalog.Interval,
			Start: x.StartGMT.Time, End: &end, Value: *x.Steps, Unit: "count", Device: b.wearable()})
		if x.Pushes != nil && *x.Pushes > 0 { // wheelchair pushes: 0 for everyone else
			b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: "wheelchair_pushes", Kind: catalog.Interval,
				Start: x.StartGMT.Time, End: &end, Value: *x.Pushes, Unit: "count", Device: b.wearable()})
		}
	}
	return nil
}

// stressBodyBattery maps the stress and Body Battery series. Negative stress levels are Garmin's
// "no reading" markers (off wrist, too much motion); a Body Battery row without a level has none.
func stressBodyBattery(b *builder, resp []byte) error {
	var r struct {
		dayBounds
		StressDescriptors []descriptor `json:"stressValueDescriptorsDTOList"`
		Stress            series       `json:"stressValuesArray"`
		BBDescriptors     []struct {
			Key   string `json:"bodyBatteryValueDescriptorKey"`
			Index int    `json:"bodyBatteryValueDescriptorIndex"`
		} `json:"bodyBatteryValueDescriptorsDTOList"`
		BodyBattery series `json:"bodyBatteryValuesArray"`
	}
	if err := b.decode(resp, &r); err != nil {
		return err
	}
	if len(r.Stress)+len(r.BodyBattery) == 0 {
		return nil
	}
	if r.StartGMT.IsZero() || !r.EndGMT.After(r.StartGMT.Time) {
		return drift(b.stream, "startTimestampGMT")
	}
	z := r.zone()
	if len(r.Stress) > 0 {
		c, err := b.columns("stressValueDescriptorsDTOList", r.StressDescriptors, "timestamp", "stressLevel")
		if err != nil {
			return err
		}
		r.Stress.each(c[0], c[1], func(at time.Time, v float64) {
			if v >= 0 {
				b.sample("garmin_stress", at, z, v, "index", "")
			}
		})
	}
	if len(r.BodyBattery) > 0 {
		ds := make([]descriptor, len(r.BBDescriptors))
		for i, d := range r.BBDescriptors {
			ds[i] = descriptor{d.Key, d.Index}
		}
		c, err := b.columns("bodyBatteryValueDescriptorsDTOList", ds, "timestamp", "bodyBatteryLevel")
		if err != nil {
			return err
		}
		r.BodyBattery.each(c[0], c[1], func(at time.Time, v float64) { b.sample("garmin_body_battery", at, z, v, "index", "") })
	}
	return nil
}

// hrv maps the overnight average to hrv_rmssd_nightly and the 5-minute readings to hrv_rmssd
// (metric-catalog.md decision 3: different methods, never one code).
func hrv(b *builder, resp []byte) error {
	var r struct {
		Summary *struct {
			CalendarDate string   `json:"calendarDate"`
			LastNightAvg *float64 `json:"lastNightAvg"`
			Baseline     *struct {
				Low   *float64 `json:"balancedLow"`
				High  *float64 `json:"balancedUpper"`
				Floor *float64 `json:"lowUpper"`
			} `json:"baseline"`
		} `json:"hrvSummary"`
		Readings []struct {
			Value *float64 `json:"hrvValue"`
			GMT   gtime    `json:"readingTimeGMT"`
			Local gtime    `json:"readingTimeLocal"`
		} `json:"hrvReadings"`
		StartGMT   gtime `json:"startTimestampGMT"`
		StartLocal gtime `json:"startTimestampLocal"`
	}
	if err := b.decode(resp, &r); err != nil {
		return err
	}
	if r.Summary == nil {
		return drift(b.stream, "hrvSummary")
	}
	for _, x := range r.Readings {
		if x.GMT.IsZero() {
			return drift(b.stream, "hrvReadings.readingTimeGMT")
		}
		if x.Value != nil {
			b.sample("hrv_rmssd", x.GMT.Time, zoneAt(x.GMT.Time, x.Local.Time), *x.Value, "ms", "")
		}
	}
	nights := []struct {
		metric string
		v      *float64
	}{{"hrv_rmssd_nightly", r.Summary.LastNightAvg}}
	if bl := r.Summary.Baseline; bl != nil {
		nights = append(nights, struct {
			metric string
			v      *float64
		}{"garmin_hrv_baseline_low", bl.Low}, struct {
			metric string
			v      *float64
		}{"garmin_hrv_baseline_high", bl.High}, struct {
			metric string
			v      *float64
		}{"garmin_hrv_baseline_floor", bl.Floor})
	}
	for _, n := range nights {
		if n.v == nil {
			continue
		}
		if r.StartGMT.IsZero() || r.StartLocal.IsZero() {
			return drift(b.stream, "startTimestampGMT or startTimestampLocal")
		}
		if err := b.nightly(n.metric, "hrv", r.Summary.CalendarDate,
			zoneAt(r.StartGMT.Time, r.StartLocal.Time), *n.v, "ms"); err != nil {
			return err
		}
	}
	return nil
}

// nightly adds a value of a night's calendar date over that local day. Without a plausible
// offset (local fields shifted twice) the day is unknown, so the value stays raw with a warning.
func (b *builder) nightly(metric, record, date string, z normalize.Zone, v float64, unit string) error {
	if z.OffsetMin == nil {
		b.out.Warn("implausible_local_offset", metric+" "+date)
		return nil
	}
	start, end, ok := localDay(date, z)
	if !ok {
		return drift(b.stream, record+" calendarDate")
	}
	b.daily(metric, record, date, start, end, z, v, unit)
	return nil
}

// respiration maps the breathing-rate series; values <= 0 are Garmin's "no reading" markers.
func respiration(b *builder, resp []byte) error {
	var r struct {
		dayBounds
		Descriptors []descriptor `json:"respirationValueDescriptorsDTOList"`
		Values      series       `json:"respirationValuesArray"`
	}
	if err := b.decode(resp, &r); err != nil {
		return err
	}
	if len(r.Values) == 0 {
		return nil
	}
	c, err := b.columns("respirationValueDescriptorsDTOList", r.Descriptors, "timestamp", "respiration")
	if err != nil {
		return err
	}
	z := r.zone()
	r.Values.each(c[0], c[1], func(at time.Time, v float64) {
		if v > 0 {
			b.sample("respiratory_rate", at, z, v, "breaths/min", "")
		}
	})
	return nil
}

// spo2 maps the single (spot and continuous) readings as [timestamp, value, ...] rows and the
// night's average to spo2_nightly (the one source of that value; the sleep payload's own summary
// stays raw). Per-minute readings come with garmin.sleep; hourly averages are aggregates, not
// samples, and stay in the raw payload.
func spo2(b *builder, resp []byte) error {
	var r struct {
		dayBounds
		Single   series   `json:"spO2SingleValues"`
		AvgSleep *float64 `json:"avgSleepSpO2"`
	}
	if err := b.decode(resp, &r); err != nil {
		return err
	}
	z := r.zone()
	r.Single.each(0, 1, func(at time.Time, v float64) {
		if v > 0 {
			b.sample("spo2", at, z, v, "%", "")
		}
	})
	if r.AvgSleep == nil {
		return nil
	}
	if r.CalendarDate == "" || r.StartGMT.IsZero() || r.StartLocal.IsZero() {
		return drift(b.stream, "calendarDate, startTimestampGMT or startTimestampLocal")
	}
	return b.nightly("spo2_nightly", "spo2", r.CalendarDate, zoneAt(r.StartGMT.Time, r.StartLocal.Time), *r.AvgSleep, "%")
}

// training holds the raw items of a day, told apart by their request endpoint: maxmet/daily
// (VO2max; an array or an object), trainingreadiness (an array of snapshots) and
// trainingstatus/aggregated (an object). Without a stored endpoint, the items' shape decides:
// "generic" is maxmet, "score" is readiness, "mostRecentTrainingStatus" is the status.
func training(b *builder, resp []byte) error {
	var items []json.RawMessage
	if bytes.HasPrefix(bytes.TrimSpace(resp), []byte("{")) {
		items = []json.RawMessage{resp}
	} else if err := b.decode(resp, &items); err != nil {
		return err
	}
	for _, raw := range items {
		var keys map[string]json.RawMessage
		if err := b.decode(raw, &keys); err != nil {
			return err
		}
		_, maxmet := keys["generic"]
		_, ready := keys["score"]
		_, status := keys["mostRecentTrainingStatus"]
		var err error
		switch {
		case strings.Contains(b.endpoint, "/maxmet/"), b.endpoint == "" && maxmet:
			err = maxMetrics(b, raw)
		case strings.Contains(b.endpoint, "/trainingreadiness/"), b.endpoint == "" && ready:
			err = readiness(b, raw)
		case strings.Contains(b.endpoint, "/trainingstatus/"), b.endpoint == "" && status:
			err = trainingStatus(b, raw)
		default:
			err = drift(b.stream, "request endpoint, [].generic, [].score or mostRecentTrainingStatus")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// maxMetrics maps the generic (running) VO2max. Garmin gives the owner's calendar date only, and
// no single instant falls on one date for every offset from -12 to +14 h, so the sample sits at
// 12:00 of that date with a zero offset: its local date is the calendar date for every owner.
// The response names no device, so the sample is the wearable's. The generic (running) VO2max is
// vo2max; the cycling one is another method and keeps its own code.
func maxMetrics(b *builder, raw []byte) error {
	type vo2 struct {
		CalendarDate string   `json:"calendarDate"`
		Precise      *float64 `json:"vo2MaxPreciseValue"`
		Value        *float64 `json:"vo2MaxValue"`
	}
	var r struct {
		Generic *vo2 `json:"generic"`
		Cycling *vo2 `json:"cycling"`
	}
	if err := b.decode(raw, &r); err != nil {
		return err
	}
	for _, m := range []struct {
		metric, component string
		x                 *vo2
	}{{"vo2max", "generic", r.Generic}, {"garmin_vo2max_cycling", "cycling", r.Cycling}} {
		if m.x == nil { // no such VO2max (yet): nothing to map
			continue
		}
		v := cmp.Or(m.x.Precise, m.x.Value)
		if v == nil {
			continue
		}
		d, err := time.Parse(time.DateOnly, m.x.CalendarDate)
		if err != nil {
			return drift(b.stream, "[]."+m.component+".calendarDate")
		}
		b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: m.metric, Kind: catalog.Sample,
			Start: d.Add(12 * time.Hour), Zone: normalize.Zone{OffsetMin: new(int16)}, Value: *v, Unit: "mL/kg/min", Device: b.wearable(),
			Key: normalize.Key{RecordType: "maxmet", ExternalID: m.x.CalendarDate, Component: m.component}})
	}
	return nil
}

// readiness maps one training-readiness snapshot; Garmin updates it during the day. A snapshot
// with a null score has no readiness yet and is skipped.
func readiness(b *builder, raw []byte) error {
	var r struct {
		Timestamp gtime    `json:"timestamp"`
		Local     gtime    `json:"timestampLocal"`
		DeviceID  *int64   `json:"deviceId"`
		Score     *float64 `json:"score"`
		Recovery  *float64 `json:"recoveryTime"`
	}
	if err := b.decode(raw, &r); err != nil {
		return err
	}
	switch {
	case r.Timestamp.IsZero():
		return drift(b.stream, "[].timestamp")
	case r.Score == nil:
		return nil
	}
	z, dev := zoneAt(r.Timestamp.Time, r.Local.Time), b.device(r.DeviceID)
	b.sample("garmin_training_readiness", r.Timestamp.Time, z, *r.Score, "index", dev)
	if r.Recovery != nil {
		b.sample("garmin_recovery_time", r.Timestamp.Time, z, *r.Recovery, "min", dev)
	}
	return nil
}

// trainingStatus maps the acute and chronic training load of each device in the aggregated
// status, and the chronic load's optimal range, as provider daily values of Garmin's calendar
// date. The status itself (a code and a phrase) is a label and stays raw.
func trainingStatus(b *builder, raw []byte) error {
	var r struct {
		Recent *struct {
			Latest map[string]struct {
				CalendarDate string `json:"calendarDate"`
				DeviceID     *int64 `json:"deviceId"`
				Load         *struct {
					Acute   *float64 `json:"dailyTrainingLoadAcute"`
					Chronic *float64 `json:"dailyTrainingLoadChronic"`
					Low     *float64 `json:"minTrainingLoadChronic"`
					High    *float64 `json:"maxTrainingLoadChronic"`
				} `json:"acuteTrainingLoadDTO"`
			} `json:"latestTrainingStatusData"`
		} `json:"mostRecentTrainingStatus"`
	}
	if err := b.decode(raw, &r); err != nil || r.Recent == nil {
		return err // no status (yet): nothing to map
	}
	for _, key := range slices.Sorted(maps.Keys(r.Recent.Latest)) {
		d := r.Recent.Latest[key]
		if d.Load == nil {
			continue
		}
		start, end, z, ok := calendarDay(d.CalendarDate)
		if !ok {
			return drift(b.stream, "mostRecentTrainingStatus.latestTrainingStatusData.calendarDate")
		}
		for _, m := range []struct {
			metric string
			v      *float64
		}{{"garmin_acute_load", d.Load.Acute}, {"garmin_chronic_load", d.Load.Chronic},
			{"garmin_chronic_load_low", d.Load.Low}, {"garmin_chronic_load_high", d.Load.High}} {
			if m.v != nil {
				b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: m.metric, Kind: catalog.DailyValue,
					Start: start, End: &end, Zone: z, Value: *m.v, Unit: "index", Device: cmp.Or(b.device(d.DeviceID), b.wearable()),
					Key: normalize.Key{RecordType: "training_status", ExternalID: d.CalendarDate + ":" + key, Component: m.metric}})
			}
		}
	}
	return nil
}

// calendarDay is Garmin's calendar date as a day at offset 0: the response names no instant or
// offset, so the wall-clock day stands for the owner's own day, whatever their zone.
func calendarDay(date string) (start, end time.Time, z normalize.Zone, ok bool) {
	d, err := time.Parse(time.DateOnly, date)
	return d, d.AddDate(0, 0, 1), normalize.Zone{OffsetMin: new(int16)}, err == nil
}

// floors maps the 15-minute floors chart as intervals of floors ascended and descended. Rows are [start GMT, end GMT, ascended, descended], with the
// columns named by a descriptor list.
func floors(b *builder, resp []byte) error {
	var r struct {
		dayBounds
		Descriptors []descriptor `json:"floorsValueDescriptorDTOList"`
		Values      [][]any      `json:"floorsValuesArray"`
	}
	if err := b.decode(resp, &r); err != nil {
		return err
	}
	if len(r.Values) == 0 {
		return nil
	}
	c, err := b.columns("floorsValueDescriptorDTOList", r.Descriptors, "startTimeGMT", "endTimeGMT", "floorsAscended")
	if err != nil {
		return err
	}
	down, err := b.columns("floorsValueDescriptorDTOList", r.Descriptors, "floorsDescended")
	z := r.zone()
	for _, row := range r.Values {
		if len(row) <= max(c[0], c[1], c[2]) {
			return drift(b.stream, "floorsValuesArray")
		}
		start, end := rowTime(row[c[0]]), rowTime(row[c[1]])
		v, ok := row[c[2]].(float64)
		switch {
		case start.IsZero() || !end.After(start):
			return drift(b.stream, "floorsValuesArray.startTimeGMT")
		case !ok:
			continue // no reading
		}
		b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: "floors_climbed", Kind: catalog.Interval,
			Start: start, End: &end, Zone: z, Value: v, Unit: "count", Device: b.wearable()})
		if d, ok := row[down[0]].(float64); err == nil && len(row) > down[0] && ok {
			b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: "garmin_floors_descended", Kind: catalog.Interval,
				Start: start, End: &end, Zone: z, Value: d, Unit: "count", Device: b.wearable()})
		}
	}
	return nil
}

// rowTime is a Garmin timestamp string inside a values row, or the zero time.
func rowTime(v any) time.Time {
	s, _ := v.(string)
	var t gtime
	if json.Unmarshal([]byte(strconv.Quote(s)), &t) != nil {
		return time.Time{}
	}
	return t.Time
}

// hydration maps the day's logged intake (valueInML) and estimated sweat loss as one interval each
// over the calendar day, the way Garmin reports them: totals without times.
func hydration(b *builder, resp []byte) error {
	var r struct {
		CalendarDate string   `json:"calendarDate"`
		Value        *float64 `json:"valueInML"`
		Sweat        *float64 `json:"sweatLossInML"`
	}
	if err := b.decode(resp, &r); err != nil {
		return err
	}
	start, end, z, ok := calendarDay(r.CalendarDate)
	if !ok && (r.Value != nil || r.Sweat != nil) {
		return drift(b.stream, "calendarDate")
	}
	for _, m := range []struct {
		metric string
		v      *float64
	}{{"diet_water", r.Value}, {"garmin_sweat_loss", r.Sweat}} {
		if m.v != nil {
			b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: m.metric, Kind: catalog.Interval,
				Start: start, End: &end, Zone: z, Value: *m.v, Unit: "mL", Device: b.wearable(),
				Key: normalize.Key{RecordType: "hydration", ExternalID: r.CalendarDate, Component: m.metric}})
		}
	}
	return nil
}

// fitnessAge maps Garmin's fitness age for the requested date, a sample at noon of that date
// at offset 0 (as VO2max, see maxMetrics). A day without a fitness age has none.
func fitnessAge(b *builder, resp []byte) error {
	var r struct {
		FitnessAge *float64 `json:"fitnessAge"`
		Achievable *float64 `json:"achievableFitnessAge"`
	}
	if err := b.decode(resp, &r); err != nil {
		return err
	}
	start, _, z, ok := calendarDay(b.date)
	if !ok && (r.FitnessAge != nil || r.Achievable != nil) {
		return drift(b.stream, "unit.date")
	}
	for _, m := range []struct {
		metric string
		v      *float64
	}{{"garmin_fitness_age", r.FitnessAge}, {"garmin_achievable_fitness_age", r.Achievable}} {
		if m.v != nil {
			b.out.Measurements = append(b.out.Measurements, normalize.Measurement{Metric: m.metric, Kind: catalog.Sample,
				Start: start.Add(12 * time.Hour), Zone: z, Value: *m.v, Unit: "years", Device: b.wearable(),
				Key: normalize.Key{RecordType: "fitness_age", ExternalID: b.date, Component: m.metric}})
		}
	}
	return nil
}
