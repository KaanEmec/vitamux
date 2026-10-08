package withings

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

const sleepSummaryFields = "breathing_disturbances_intensity,deepsleepduration,durationtosleep,durationtowakeup," +
	"hr_average,hr_max,hr_min,lightsleepduration,remsleepduration,rr_average,rr_max,rr_min,sleep_score,snoring," +
	"snoringepisodecount,wakeupcount,wakeupduration,sleep_efficiency,sleep_latency,total_sleep_time,total_timeinbed," +
	"wakeup_latency,waso,apnea_hypopnea_index,out_of_bed_count,nb_rem_episodes,night_events"

// fetchSleep gets one getsummary page and, for each night, its states (sleep get over the
// night, at most 24 h), and puts one raw record per night: {"summary": …, "states": […]}.
func (w *Connector) fetchSleep(ctx context.Context, c connectors.Conn, cred connectors.Credentials, u connectors.WorkUnit, cur cursor, out *connectors.RawSink) (connectors.FetchResult, error) {
	form := url.Values{"action": {"getsummary"}, "data_fields": {sleepSummaryFields}}
	incremental := lastupdateOrDates(form, u, cur)
	body, req, err := w.call(ctx, c, cred, sleepPath, form)
	if err != nil {
		return connectors.FetchResult{}, err
	}
	var b struct {
		Series *[]json.RawMessage `json:"series"`
		More   flexInt            `json:"more"`
		Offset flexInt            `json:"offset"`
	}
	if json.Unmarshal(body, &b) != nil || b.Series == nil || (b.More != 0 && int64(b.Offset) <= cur.Offset) {
		return connectors.FetchResult{}, drift(out, req, body)
	}
	var hw time.Time
	for _, raw := range *b.Series {
		var s struct {
			ID        *int64 `json:"id"`
			StartDate *int64 `json:"startdate"`
			EndDate   *int64 `json:"enddate"`
		}
		if json.Unmarshal(raw, &s) != nil || s.ID == nil || s.StartDate == nil || s.EndDate == nil || *s.EndDate < *s.StartDate {
			return connectors.FetchResult{}, drift(out, req, body)
		}
		end := min(*s.EndDate, *s.StartDate+int64(intradaySlice/time.Second))
		states, sreq, err := w.call(ctx, c, cred, sleepPath, url.Values{"action": {"get"},
			"startdate": {strconv.FormatInt(*s.StartDate, 10)}, "enddate": {strconv.FormatInt(end, 10)}})
		if err != nil {
			return connectors.FetchResult{}, err
		}
		var g struct {
			Series *[]json.RawMessage `json:"series"`
		}
		if json.Unmarshal(states, &g) != nil || g.Series == nil {
			return connectors.FetchResult{}, drift(out, sreq, states)
		}
		rec, _ := json.Marshal(struct {
			Summary json.RawMessage   `json:"summary"`
			States  []json.RawMessage `json:"states"`
		}{raw, *g.Series})
		out.Put(ingest.RawItem{ExternalKey: "sleep:" + strconv.FormatInt(*s.ID, 10), ContentType: "application/json", Body: rec, Request: req})
		if t := time.Unix(*s.EndDate, 0).UTC(); t.After(hw) {
			hw = t
		}
	}
	return cur.next(incremental, b.More != 0, int64(b.Offset), hw), nil
}

// sleepStages maps Withings sleep states; 4 (manual) and 5 are sleep of no known stage.
var sleepStages = map[int]string{0: "awake", 1: "light", 2: "deep", 3: "rem", 4: "asleep_unspecified", 5: "asleep_unspecified", 15: "out_of_bed"}

// sleepValues are getsummary values stored as daily values of the night's date. Efficiency and
// WASO stay raw (the engine derives them from the session); hr_min is not resting heart rate.
var sleepValues = []struct{ field, metric, unit string }{
	{"wakeupcount", "sleep_awakenings", "count"}, {"snoring", "sleep_snoring_time", "s"},
	{"snoringepisodecount", "sleep_snoring_episodes", "count"}, {"apnea_hypopnea_index", "apnea_hypopnea_index", "events/h"},
	{"sleep_score", "withings_sleep_score", "index"}, {"breathing_disturbances_intensity", "withings_breathing_quality", "index"},
	{"hr_average", "sleeping_heart_rate", "bpm"}, {"rr_average", "respiratory_rate_nightly", "breaths/min"},
}

// normalizeSleep turns one night into a sleep session with stages and provider totals, and
// the summary values into daily values.
func normalizeSleep(body []byte, out *normalize.Output) error {
	var rec struct {
		Summary *struct {
			ID        int64  `json:"id"`
			Timezone  string `json:"timezone"`
			Date      string `json:"date"`
			StartDate int64  `json:"startdate"`
			EndDate   int64  `json:"enddate"`
			deviceFields
			Data map[string]any `json:"data"`
		} `json:"summary"`
		States []struct {
			StartDate int64 `json:"startdate"`
			EndDate   int64 `json:"enddate"`
			State     int   `json:"state"`
			deviceFields
		} `json:"states"`
	}
	if json.Unmarshal(body, &rec) != nil || rec.Summary == nil {
		return errors.New("withings: undecodable sleep record")
	}
	s := rec.Summary
	if s.ID == 0 || s.StartDate <= 0 || s.EndDate <= s.StartDate || s.EndDate > maxMeasureDate {
		return errors.New("withings: sleep without id or a span in 1970..9999")
	}
	start, end, zone, err := localDay(s.Date, s.Timezone)
	if err != nil {
		return err
	}
	dev, origin, _ := s.source(out)
	ext := strconv.FormatInt(s.ID, 10)
	sess := normalize.SleepSession{Start: time.Unix(s.StartDate, 0).UTC(), End: time.Unix(s.EndDate, 0).UTC(), Zone: zone,
		Device: dev, Origin: origin, Key: normalize.Key{RecordType: "sleep", ExternalID: ext}}
	for _, st := range rec.States {
		if id := st.modelID(); id != 0 && s.modelID() != 0 && id != s.modelID() {
			continue // another device's night in the same window
		}
		stage, ok := sleepStages[st.State]
		if !ok {
			out.Warn("unknown_sleep_state", "state "+strconv.Itoa(st.State))
			continue
		}
		if st.EndDate > st.StartDate && st.StartDate > 0 && st.EndDate <= maxMeasureDate {
			sess.Stages = append(sess.Stages, normalize.SleepStage{Stage: stage, Start: time.Unix(st.StartDate, 0).UTC(), End: time.Unix(st.EndDate, 0).UTC()})
		}
	}
	secs := func(field string) *int32 {
		if v, ok := s.Data[field].(float64); ok && v >= 0 && v <= 86400 {
			n := int32(v)
			return &n
		}
		return nil
	}
	sess.Totals = &normalize.SleepTotals{Asleep: secs("total_sleep_time"), Deep: secs("deepsleepduration"), Light: secs("lightsleepduration"),
		REM: secs("remsleepduration"), Awake: secs("wakeupduration"), Latency: secs("sleep_latency")}
	if *sess.Totals == (normalize.SleepTotals{}) {
		sess.Totals = nil
	}
	out.Sleep = append(out.Sleep, sess)
	for _, m := range sleepValues {
		if v, ok := s.Data[m.field].(float64); ok {
			out.Measurements = append(out.Measurements, normalize.Measurement{Metric: m.metric, Kind: catalog.DailyValue,
				Start: start, End: &end, Zone: zone, Value: v, Unit: m.unit, Device: dev, Origin: origin,
				Key: normalize.Key{RecordType: "sleep", ExternalID: ext, Component: m.metric}})
		}
	}
	return nil
}
