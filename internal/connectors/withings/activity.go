package withings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

const (
	activityFields = "steps,distance,elevation,soft,moderate,intense,active,calories,totalcalories," +
		"hr_average,hr_min,hr_max,hr_zone_0,hr_zone_1,hr_zone_2,hr_zone_3"
	intradayFields = "steps,elevation,calories,distance,stroke,pool_lap,duration,heart_rate,spo2_auto,rr,rmssd,sdnn1,hrv_quality,core_body_temperature"

	intradayFirst   = 7 * 24 * time.Hour // the first intraday sync; older days are a backfill
	intradayOverlap = 6 * time.Hour      // re-fetched each run: devices upload late
	intradaySlice   = 24 * time.Hour     // getintradayactivity answers at most 24 h
)

// fetchActivity gets one getactivity page and puts one raw record per day and device
// (docs/providers/withings.md#activity-intraday-and-sleep).
func (w *Connector) fetchActivity(ctx context.Context, c connectors.Conn, cred connectors.Credentials, u connectors.WorkUnit, cur cursor, out *connectors.RawSink) (connectors.FetchResult, error) {
	form := url.Values{"action": {"getactivity"}, "data_fields": {activityFields}}
	incremental := lastupdateOrDates(form, u, cur)
	body, req, err := w.call(ctx, c, cred, measureV2Path, form)
	if err != nil {
		return connectors.FetchResult{}, err
	}
	var b struct {
		Activities *[]json.RawMessage `json:"activities"`
		More       flexInt            `json:"more"`
		Offset     flexInt            `json:"offset"`
	}
	if json.Unmarshal(body, &b) != nil || b.Activities == nil || (b.More != 0 && int64(b.Offset) <= cur.Offset) {
		return connectors.FetchResult{}, drift(out, req, body)
	}
	var hw time.Time
	for _, raw := range *b.Activities {
		var a struct {
			Date         string  `json:"date"`
			Brand        flexInt `json:"brand"`
			DeviceID     string  `json:"deviceid"`
			HashDeviceID string  `json:"hash_deviceid"`
		}
		if json.Unmarshal(raw, &a) != nil {
			return connectors.FetchResult{}, drift(out, req, body)
		}
		d, err := time.Parse(time.DateOnly, a.Date)
		if err != nil {
			return connectors.FetchResult{}, drift(out, req, body)
		}
		dev := a.HashDeviceID
		if dev == "" {
			dev = a.DeviceID
		}
		out.Put(ingest.RawItem{ExternalKey: "activity:" + a.Date + ":" + strconv.FormatInt(int64(a.Brand), 10) + ":" + dev,
			ContentType: "application/json", Body: wrap("activity", raw), Request: req})
		if d.After(hw) {
			hw = d
		}
	}
	return cur.next(incremental, b.More != 0, int64(b.Offset), hw), nil
}

// lastupdateOrDates sets lastupdate for an incremental unit, else the unit's dates, and the
// offset; it reports whether the unit is incremental.
func lastupdateOrDates(form url.Values, u connectors.WorkUnit, cur cursor) bool {
	incremental := u.From.IsZero() && u.To.IsZero()
	if incremental {
		form.Set("lastupdate", strconv.FormatInt(cur.LastUpdate, 10))
	} else {
		form.Set("startdateymd", u.From.UTC().Format(time.DateOnly))
		form.Set("enddateymd", u.To.UTC().Format(time.DateOnly))
	}
	if cur.Offset > 0 {
		form.Set("offset", strconv.FormatInt(cur.Offset, 10))
	}
	return incremental
}

// wrap is the raw body {"<name>": record}.
func wrap(name string, record json.RawMessage) []byte {
	b := append([]byte(`{"`+name+`":`), record...)
	return append(b, '}')
}

// intradayUnit widens [from, to) to whole hours, so every hour record is fetched complete.
func intradayUnit(from, to time.Time) connectors.WorkUnit {
	from, to = from.UTC().Truncate(time.Hour), to.UTC().Add(time.Hour-time.Nanosecond).Truncate(time.Hour)
	if !to.After(from) {
		from = to.Add(-time.Hour)
	}
	return connectors.WorkUnit{From: from, To: to}
}

// fetchIntraday gets the next slice of at most 24 h of the unit and puts one raw record per
// hour, {"entries": [{"timestamp": <unix>, "data": entry}, ...]} in time order. Entries outside the slice are left
// to the slice they belong to, so an hour record is always whole. After the last slice the
// cursor starts the next run intradayOverlap before the unit's end.
func (w *Connector) fetchIntraday(ctx context.Context, c connectors.Conn, cred connectors.Credentials, u connectors.WorkUnit, cur cursor, out *connectors.RawSink) (connectors.FetchResult, error) {
	start := u.From
	if cur.Start > 0 {
		start = time.Unix(cur.Start, 0).UTC()
	}
	end := start.Add(intradaySlice)
	if end.After(u.To) {
		end = u.To
	}
	form := url.Values{"action": {"getintradayactivity"}, "data_fields": {intradayFields},
		"startdate": {strconv.FormatInt(start.Unix(), 10)}, "enddate": {strconv.FormatInt(end.Unix(), 10)}}
	body, req, err := w.call(ctx, c, cred, measureV2Path, form)
	if err != nil {
		return connectors.FetchResult{}, err
	}
	var b struct {
		Series json.RawMessage `json:"series"`
	}
	series := map[string]json.RawMessage{}
	if json.Unmarshal(body, &b) != nil || len(b.Series) == 0 ||
		(!bytes.Equal(b.Series, []byte("[]")) && json.Unmarshal(b.Series, &series) != nil) { // empty is []
		return connectors.FetchResult{}, drift(out, req, body)
	}
	hours := map[int64][]int64{}
	for k := range series {
		ts, err := strconv.ParseInt(k, 10, 64)
		if err != nil {
			return connectors.FetchResult{}, drift(out, req, body)
		}
		if ts >= start.Unix() && ts < end.Unix() {
			hours[ts-ts%3600] = append(hours[ts-ts%3600], ts)
		}
	}
	var hw time.Time
	for _, h := range slices.Sorted(maps.Keys(hours)) {
		tss := hours[h]
		slices.Sort(tss)
		rec := []byte(`{"entries":[`)
		for i, ts := range tss {
			if i > 0 {
				rec = append(rec, ',')
			}
			rec = strconv.AppendInt(append(rec, `{"timestamp":`...), ts, 10)
			rec = append(append(append(rec, `,"data":`...), series[strconv.FormatInt(ts, 10)]...), '}')
		}
		out.Put(ingest.RawItem{ExternalKey: "intraday:" + strconv.FormatInt(h, 10), ContentType: "application/json",
			Body: append(rec, "]}"...), Request: req})
		hw = time.Unix(tss[len(tss)-1], 0).UTC()
	}
	res := connectors.FetchResult{HighWatermark: hw, Done: !end.Before(u.To)}
	next := end
	if res.Done {
		next = u.To.Add(-intradayOverlap)
	}
	res.NextCursor, _ = json.Marshal(cursor{Start: next.Unix()})
	return res, nil
}

// activityMetrics maps getactivity fields to daily values; elevation is floors climbed and
// calories are active calories (docs/providers/withings.md#activity-intraday-and-sleep).
var activityMetrics = []struct{ field, metric, unit string }{
	{"steps", "steps", "count"}, {"distance", "distance_walk_run", "m"}, {"elevation", "floors_climbed", "count"},
	{"calories", "active_energy", "kcal"}, {"totalcalories", "total_energy", "kcal"},
	{"soft", "intensity_light_time", "s"}, {"moderate", "intensity_moderate_time", "s"}, {"intense", "intensity_vigorous_time", "s"},
}

// normalizeActivity turns one day of one device into daily values over the local day of the
// record's timezone. Brand 18 rows are relays from other apps.
func normalizeActivity(body []byte, out *normalize.Output) error {
	var rec struct {
		Activity *struct {
			Date     string  `json:"date"`
			Timezone string  `json:"timezone"`
			Brand    flexInt `json:"brand"`
			deviceFields
		} `json:"activity"`
	}
	var values struct {
		Activity map[string]any `json:"activity"`
	}
	if json.Unmarshal(body, &rec) != nil || rec.Activity == nil || json.Unmarshal(body, &values) != nil {
		return errors.New("withings: undecodable activity record")
	}
	a := rec.Activity
	start, end, zone, err := localDay(a.Date, a.Timezone)
	if err != nil {
		return err
	}
	dev, origin, _ := a.source(out)
	if a.Brand == 18 {
		origin = "relay:brand:18"
		addOrigin(out, normalize.Origin{Key: origin, Name: "Other app"})
	}
	ext := a.Date + ":" + strconv.FormatInt(int64(a.Brand), 10) + ":" + dev
	for _, m := range activityMetrics {
		if v, ok := values.Activity[m.field].(float64); ok {
			out.Measurements = append(out.Measurements, normalize.Measurement{Metric: m.metric, Kind: catalog.DailyValue,
				Start: start, End: &end, Zone: zone, Value: v, Unit: m.unit, Device: dev, Origin: origin,
				Key: normalize.Key{RecordType: "activity", ExternalID: ext, Component: m.metric}})
		}
	}
	return nil
}

// localDay is the span of a calendar date in an IANA zone.
func localDay(date, tz string) (start, end time.Time, z normalize.Zone, err error) {
	d, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return start, end, z, errors.New("withings: record without a date")
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" || tz == "Local" {
		return start, end, z, errors.New("withings: record without a known timezone")
	}
	start = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	return start.UTC(), start.AddDate(0, 0, 1).UTC(), normalize.Zone{TZ: tz}, nil
}

// intradayValues maps getintradayactivity fields: interval fields cover [t, t+duration), the
// others are samples at t. RMSSD and SDNN are Withings' few-second and 1-minute windows; the
// core body temperature is an estimate, never body_temperature.
var intradayValues = []struct {
	field, metric, unit string
	interval            bool
}{
	{"steps", "steps", "count", true}, {"elevation", "floors_climbed", "count", true},
	{"calories", "active_energy", "kcal", true}, {"distance", "distance_walk_run", "m", true},
	{"stroke", "swim_strokes", "count", true}, {"pool_lap", "swim_laps", "count", true}, {"heart_rate", "heart_rate", "bpm", false},
	{"spo2_auto", "spo2", "%", false}, {"rr", "respiratory_rate", "breaths/min", false},
	{"rmssd", "hrv_rmssd", "ms", false}, {"sdnn1", "hrv_sdnn", "ms", false},
	{"core_body_temperature", "core_body_temperature_estimated", "°C", false},
}

// normalizeIntraday turns one hour record into intervals and samples, keyed by timestamp.
func normalizeIntraday(body []byte, out *normalize.Output) error {
	var rec struct {
		Entries *[]struct {
			Timestamp int64           `json:"timestamp"`
			Data      json.RawMessage `json:"data"`
		} `json:"entries"`
	}
	if json.Unmarshal(body, &rec) != nil || rec.Entries == nil {
		return errors.New("withings: undecodable intraday record")
	}
	for _, x := range *rec.Entries {
		ts := x.Timestamp
		if ts <= 0 || ts > maxMeasureDate {
			return errors.New("withings: intraday timestamp outside 1970..9999")
		}
		var e struct {
			deviceFields
			Duration *float64 `json:"duration"`
		}
		var vals map[string]any
		if json.Unmarshal(x.Data, &e) != nil || json.Unmarshal(x.Data, &vals) != nil {
			return errors.New("withings: undecodable intraday entry")
		}
		dev, origin, _ := e.source(out)
		at := time.Unix(ts, 0).UTC()
		for _, f := range intradayValues {
			v, ok := vals[f.field].(float64)
			if !ok {
				continue
			}
			m := normalize.Measurement{Metric: f.metric, Kind: catalog.Sample, Start: at, Value: v, Unit: f.unit,
				Device: dev, Origin: origin, Key: normalize.Key{RecordType: "intraday", ExternalID: strconv.FormatInt(ts, 10), Component: f.metric}}
			if f.interval {
				if e.Duration == nil || *e.Duration <= 0 {
					out.Warn("intraday_without_duration", f.field)
					continue
				}
				end := at.Add(time.Duration(*e.Duration) * time.Second)
				m.Kind, m.End = catalog.Interval, &end
			}
			out.Measurements = append(out.Measurements, m)
		}
	}
	return nil
}
