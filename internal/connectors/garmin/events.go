package garmin

import (
	"cmp"
	"encoding/json"
	"strconv"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// sleepLevels maps a sleepLevels activityLevel to sleep_stages.stage. Devices without REM report
// 2 and 3 as awake (remSleepData false); -1 is unmeasurable and is no stage.
var sleepLevels = map[float64]string{0: "deep", 1: "light", 2: "rem", 3: "awake"}

// sleep maps the night (dailySleepDTO with its sleepLevels), the naps of the day and the
// provider's sleep score. Vitals inside the response are left to their own streams.
func sleep(b *builder, resp []byte) error {
	var r struct {
		DTO *struct {
			ID           *int64 `json:"id"`
			CalendarDate string `json:"calendarDate"`
			StartGMT     *int64 `json:"sleepStartTimestampGMT"`
			EndGMT       *int64 `json:"sleepEndTimestampGMT"`
			StartLocal   *int64 `json:"sleepStartTimestampLocal"`
			EndLocal     *int64 `json:"sleepEndTimestampLocal"`
			Asleep       *int32 `json:"sleepTimeSeconds"`
			Deep         *int32 `json:"deepSleepSeconds"`
			Light        *int32 `json:"lightSleepSeconds"`
			REM          *int32 `json:"remSleepSeconds"`
			Awake        *int32 `json:"awakeSleepSeconds"`
			Scores       *struct {
				Overall *struct {
					Value *float64 `json:"value"`
				} `json:"overall"`
			} `json:"sleepScores"`
		} `json:"dailySleepDTO"`
		REMData *bool `json:"remSleepData"`
		Levels  []struct {
			StartGMT gtime    `json:"startGMT"`
			EndGMT   gtime    `json:"endGMT"`
			Level    *float64 `json:"activityLevel"`
		} `json:"sleepLevels"`
		Naps []struct {
			StartGMT gtime  `json:"napStartTimestampGMT"`
			EndGMT   gtime  `json:"napEndTimestampGMT"`
			Seconds  *int32 `json:"napTimeSec"`
		} `json:"dailyNapDTOS"`
	}
	if err := b.decode(resp, &r); err != nil {
		return err
	}
	d := r.DTO
	if d == nil {
		return drift(b.stream, "dailySleepDTO")
	}
	var z normalize.Zone
	if d.ID != nil { // a day without a night has no id
		if d.StartGMT == nil || d.EndGMT == nil || d.StartLocal == nil || d.EndLocal == nil {
			return drift(b.stream, "dailySleepDTO.sleep{Start,End}Timestamp{GMT,Local}")
		}
		start, end := millis(*d.StartGMT), millis(*d.EndGMT)
		z = zoneAt(end, millis(*d.EndLocal)) // sleep_date is the local wake date
		s := normalize.SleepSession{Start: start, End: end, Zone: z, Device: b.wearable(),
			Totals: &normalize.SleepTotals{Asleep: d.Asleep, Deep: d.Deep, Light: d.Light, REM: d.REM, Awake: d.Awake},
			Key:    normalize.Key{RecordType: "sleep", ExternalID: strconv.FormatInt(*d.ID, 10)}}
		rem := r.REMData == nil || *r.REMData
		for _, l := range r.Levels {
			if l.StartGMT.IsZero() || l.EndGMT.IsZero() || l.Level == nil {
				return drift(b.stream, "sleepLevels.startGMT, endGMT or activityLevel")
			}
			stage, ok := sleepLevels[*l.Level]
			switch {
			case *l.Level == -1 || !l.EndGMT.After(l.StartGMT.Time):
				continue
			case !ok:
				b.warn("unknown_sleep_level", strconv.FormatFloat(*l.Level, 'f', -1, 64))
				continue
			case !rem && stage == "rem":
				stage = "awake"
			}
			s.Stages = append(s.Stages, normalize.SleepStage{Stage: stage, Start: l.StartGMT.Time, End: l.EndGMT.Time})
		}
		if end.After(start) {
			b.out.Sleep = append(b.out.Sleep, s)
		} else {
			b.warn("empty_sleep_window", d.CalendarDate)
		}
		if d.Scores != nil && d.Scores.Overall != nil && d.Scores.Overall.Value != nil {
			if err := b.nightly("garmin_sleep_score", "sleep", d.CalendarDate, z, *d.Scores.Overall.Value, "index"); err != nil {
				return err
			}
		}
	}
	for _, n := range r.Naps {
		if n.StartGMT.IsZero() || n.EndGMT.IsZero() {
			return drift(b.stream, "dailyNapDTOS.napStartTimestampGMT or napEndTimestampGMT")
		}
		if !n.EndGMT.After(n.StartGMT.Time) {
			continue
		}
		s := normalize.SleepSession{Start: n.StartGMT.Time, End: n.EndGMT.Time, Zone: z, Nap: true, Device: b.wearable(),
			Key: normalize.Key{RecordType: "nap", ExternalID: strconv.FormatInt(n.StartGMT.UnixMilli(), 10)}}
		if n.Seconds != nil {
			s.Totals = &normalize.SleepTotals{Asleep: n.Seconds}
		}
		b.out.Sleep = append(b.out.Sleep, s)
	}
	return nil
}

// bodyComposition maps each weigh-in of dateWeightList to a body_composition group. Garmin
// reports masses in grams; bodyWater is a percentage, which no catalogue code holds yet.
func bodyComposition(b *builder, resp []byte) error {
	var r struct {
		List *[]struct {
			SamplePK    *int64   `json:"samplePk"`
			GMT         *int64   `json:"timestampGMT"`
			Local       *int64   `json:"date"`
			SourceType  string   `json:"sourceType"`
			Weight      *float64 `json:"weight"`
			BMI         *float64 `json:"bmi"`
			BodyFat     *float64 `json:"bodyFat"`
			BoneMass    *float64 `json:"boneMass"`
			MuscleMass  *float64 `json:"muscleMass"`
			VisceralFat *float64 `json:"visceralFat"`
		} `json:"dateWeightList"`
	}
	if err := b.decode(resp, &r); err != nil {
		return err
	}
	if r.List == nil {
		return drift(b.stream, "dateWeightList")
	}
	for _, w := range *r.List {
		if w.SamplePK == nil || w.GMT == nil {
			return drift(b.stream, "dateWeightList.samplePk or timestampGMT")
		}
		at := millis(*w.GMT)
		var z normalize.Zone
		if w.Local != nil {
			z = zoneAt(at, millis(*w.Local))
		}
		flags := manual(w.SourceType)
		var comps []normalize.Measurement
		for _, c := range []struct {
			metric, unit string
			v            *float64
		}{{"weight", "g", w.Weight}, {"bmi", "kg/m²", w.BMI}, {"body_fat_ratio", "%", w.BodyFat},
			{"bone_mass", "g", w.BoneMass}, {"muscle_mass", "g", w.MuscleMass}, {"visceral_fat_index", "index", w.VisceralFat}} {
			if c.v != nil {
				comps = append(comps, normalize.Measurement{Metric: c.metric, Kind: catalog.Sample, Start: at, Zone: z,
					Value: *c.v, Unit: c.unit, Flags: flags})
			}
		}
		if len(comps) > 0 {
			b.out.Groups = append(b.out.Groups, normalize.Group{Kind: "body_composition", MeasuredAt: at, Zone: z,
				Context: sourceContext(w.SourceType, false), Components: comps,
				Key: normalize.Key{RecordType: "weight", ExternalID: strconv.FormatInt(*w.SamplePK, 10)}})
		}
	}
	return nil
}

// bloodPressure maps each reading of measurementSummaries to a bp_reading group, keyed by the
// reading's version (Garmin's id for it). Notes are free text and stay in the raw payload.
func bloodPressure(b *builder, resp []byte) error {
	var r struct {
		Summaries *[]struct {
			Measurements []struct {
				Version   *int64   `json:"version"`
				Systolic  *float64 `json:"systolic"`
				Diastolic *float64 `json:"diastolic"`
				Pulse     *float64 `json:"pulse"`
				Source    string   `json:"sourceType"`
				Multi     bool     `json:"multiMeasurement"`
				GMT       gtime    `json:"measurementTimestampGMT"`
				Local     gtime    `json:"measurementTimestampLocal"`
			} `json:"measurements"`
		} `json:"measurementSummaries"`
	}
	if err := b.decode(resp, &r); err != nil {
		return err
	}
	if r.Summaries == nil {
		return drift(b.stream, "measurementSummaries")
	}
	for _, s := range *r.Summaries {
		for _, m := range s.Measurements {
			switch {
			case m.Version == nil:
				return drift(b.stream, "measurements.version")
			case m.GMT.IsZero():
				return drift(b.stream, "measurements.measurementTimestampGMT")
			case m.Systolic == nil || m.Diastolic == nil:
				return drift(b.stream, "measurements.systolic or diastolic")
			}
			at, z, flags := m.GMT.Time, zoneAt(m.GMT.Time, m.Local.Time), manual(m.Source)
			comps := []normalize.Measurement{
				{Metric: "bp_systolic", Kind: catalog.Sample, Start: at, Zone: z, Value: *m.Systolic, Unit: "mmHg", Flags: flags},
				{Metric: "bp_diastolic", Kind: catalog.Sample, Start: at, Zone: z, Value: *m.Diastolic, Unit: "mmHg", Flags: flags},
			}
			if m.Pulse != nil {
				comps = append(comps, normalize.Measurement{Metric: "bp_pulse", Kind: catalog.Sample, Start: at, Zone: z,
					Value: *m.Pulse, Unit: "bpm", Flags: flags})
			}
			b.out.Groups = append(b.out.Groups, normalize.Group{Kind: "bp_reading", MeasuredAt: at, Zone: z,
				Context: sourceContext(m.Source, m.Multi), Components: comps,
				Key: normalize.Key{RecordType: "bp", ExternalID: strconv.FormatInt(*m.Version, 10)}})
		}
	}
	return nil
}

func manual(sourceType string) normalize.Flags {
	if sourceType == "MANUAL" {
		return normalize.FlagManualEntry
	}
	return 0
}

// sourceContext is a group's context: Garmin's source type and whether the reading is part of an
// averaged session.
func sourceContext(sourceType string, multi bool) json.RawMessage {
	c := map[string]any{}
	if sourceType != "" {
		c["source_type"] = sourceType
	}
	if multi {
		c["multi_measurement"] = true
	}
	if len(c) == 0 {
		return nil
	}
	out, _ := json.Marshal(c)
	return out
}

// maxActivitySecs bounds an activity's duration (a week); longer ones are refused, not stored.
const maxActivitySecs = 7 * 24 * 3600

// activity maps one activity summary to a workout. Laps need the activity's details or FIT file,
// which this version does not decode.
func activity(b *builder, resp []byte) error {
	var a struct {
		ID         *int64 `json:"activityId"`
		StartGMT   gtime  `json:"startTimeGMT"`
		StartLocal gtime  `json:"startTimeLocal"`
		Type       *struct {
			Key string `json:"typeKey"`
		} `json:"activityType"`
		Duration *float64 `json:"duration"`
		Elapsed  *float64 `json:"elapsedDuration"`
		Distance *float64 `json:"distance"`
		Calories *float64 `json:"calories"`
		AvgHR    *float64 `json:"averageHR"`
		MaxHR    *float64 `json:"maxHR"`
		DeviceID *int64   `json:"deviceId"`
	}
	if err := b.decode(resp, &a); err != nil {
		return err
	}
	switch {
	case a.ID == nil:
		return drift(b.stream, "activityId")
	case a.StartGMT.IsZero():
		return drift(b.stream, "startTimeGMT")
	case a.Type == nil || a.Type.Key == "":
		return drift(b.stream, "activityType.typeKey")
	case a.Duration == nil:
		return drift(b.stream, "duration")
	}
	secs := *a.Duration
	if a.Elapsed != nil && *a.Elapsed > secs {
		secs = *a.Elapsed
	}
	end := a.StartGMT.Add(time.Duration(min(secs, maxActivitySecs) * float64(time.Second))).Truncate(time.Millisecond)
	if !end.After(a.StartGMT.Time) || secs > maxActivitySecs {
		b.warn("bad_activity_duration", strconv.FormatInt(*a.ID, 10))
		return nil
	}
	w := normalize.Workout{Start: a.StartGMT.Time, End: end, Zone: zoneAt(a.StartGMT.Time, a.StartLocal.Time),
		Sport: cmp.Or(sports[a.Type.Key], "other"), ProviderSport: a.Type.Key, DistanceM: positive(a.Distance), EnergyKcal: positive(a.Calories),
		AvgHRBpm: positive(a.AvgHR), MaxHRBpm: positive(a.MaxHR), Device: b.device(a.DeviceID),
		Key: normalize.Key{RecordType: "activity", ExternalID: strconv.FormatInt(*a.ID, 10)}}
	b.out.Workouts = append(b.out.Workouts, w)
	return nil
}

// positive drops Garmin's 0 for "not measured" (a strength session's distance, an HR-less activity).
func positive(v *float64) *float64 {
	if v == nil || *v <= 0 {
		return nil
	}
	return v
}

// sports maps Garmin activity typeKeys to the canonical sport (the snake-case names the Apple
// Health normalizer uses). Unlisted keys are "other"; the typeKey is always kept as provider sport.
var sports = map[string]string{
	"running": "running", "trail_running": "running", "treadmill_running": "running", "track_running": "running",
	"indoor_running": "running", "street_running": "running", "virtual_run": "running", "ultra_run": "running",
	"cycling": "cycling", "road_biking": "cycling", "mountain_biking": "cycling", "gravel_cycling": "cycling",
	"indoor_cycling": "cycling", "virtual_ride": "cycling", "cyclocross": "cycling", "e_bike_fitness": "cycling",
	"walking": "walking", "casual_walking": "walking", "speed_walking": "walking", "indoor_walking": "walking",
	"hiking": "hiking", "lap_swimming": "swimming", "open_water_swimming": "swimming", "swimming": "swimming",
	"strength_training": "traditional_strength_training", "hiit": "high_intensity_interval_training",
	"indoor_cardio": "mixed_cardio", "yoga": "yoga", "pilates": "pilates", "elliptical": "elliptical",
	"indoor_rowing": "rowing", "rowing": "rowing", "stair_climbing": "stair_climbing",
	"cross_country_skiing_ws": "cross_country_skiing", "skate_skiing_ws": "cross_country_skiing",
	"resort_skiing": "downhill_skiing", "resort_snowboarding": "snowboarding",
	"tennis": "tennis", "soccer": "soccer", "golf": "golf", "multi_sport": "swim_bike_run",
}
