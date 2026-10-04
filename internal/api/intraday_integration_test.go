//go:build integration

package api

import (
	"math"
	"net/http"
	"net/url"
	"testing"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
)

// Intraday views (J26.1, J26.2) on the synthetic dataset, whose heart rate comes every minute.

func span(from, to string) string {
	return "&start=" + url.QueryEscape(from) + "&end=" + url.QueryEscape(to)
}

var intradayDay = span("2025-09-14T00:00:00+02:00", "2025-09-15T00:00:00+02:00")

func TestIntradayResolvedSeries(t *testing.T) {
	e := newResolvedEnv(t, "2025-09-13", 3)
	var m oapi.Metric
	e.call("GET /api/v1/metrics/{code}", "/api/v1/metrics/heart_rate", "", http.StatusOK, &m)
	if m.Intraday == nil || m.Intraday.Default != "1m" || m.Intraday.Finest != "raw" {
		t.Errorf("heart_rate intraday: %+v", m.Intraday)
	}
	m = oapi.Metric{}
	e.call("GET /api/v1/metrics/{code}", "/api/v1/metrics/weight", "", http.StatusOK, &m)
	if m.Intraday != nil {
		t.Errorf("weight has no day view: %+v", m.Intraday)
	}

	for size, want := range map[string]int{"30s": 2880, "1m": 1440, "5m": 288} {
		var s oapi.ResolvedSeries
		e.call(series, "/api/v1/resolved/series?metric=heart_rate&limit=5000&window="+size+intradayDay, "", http.StatusOK, &s)
		if len(s.Points) != want || s.HasMore || *s.Window.Size != size {
			t.Fatalf("%s: %d points, want %d", size, len(s.Points), want)
		}
		valued := 0
		for _, p := range s.Points {
			v, ok := p.Value.(float64)
			if !ok {
				if p.N != nil || p.Min != nil {
					t.Fatalf("%s: band without a value: %+v", size, p)
				}
				continue
			}
			valued++
			if p.N == nil || *p.N < 1 || p.Min == nil || p.Max == nil || *p.Min > v || *p.Max < v {
				t.Fatalf("%s: point %+v", size, p)
			}
		}
		if valued < want/3 {
			t.Errorf("%s: %d points with a value", size, valued)
		}
	}
	// Additive buckets carry n but no band.
	var s oapi.ResolvedSeries
	e.call(series, "/api/v1/resolved/series?metric=steps&window=30m"+intradayDay, "", http.StatusOK, &s)
	for _, p := range s.Points {
		if p.Min != nil || p.Value != nil && p.N == nil {
			t.Fatalf("steps 30m point %+v", p)
		}
	}

	// Spans: a day for 30s and 1m (25 hours across DST), a week for 5m and longer.
	e.call(series, "/api/v1/resolved/series?metric=heart_rate&window=1m"+span("2025-09-14T00:00:00+02:00", "2025-09-15T01:00:00+02:00"), "", http.StatusOK, nil)
	e.call(series, "/api/v1/resolved/series?metric=heart_rate&window=5m"+span("2025-09-08T00:00:00+02:00", "2025-09-15T00:00:00+02:00"), "", http.StatusOK, nil)
	for _, target := range []string{
		"/api/v1/resolved/series?metric=heart_rate&window=30s" + span("2025-09-13T00:00:00+02:00", "2025-09-15T00:00:00+02:00"),
		"/api/v1/resolved/series?metric=heart_rate&window=1m" + span("2025-09-14T00:00:00+02:00", "2025-09-15T01:00:01+02:00"),
		"/api/v1/resolved/series?metric=heart_rate&window=15m" + span("2025-09-07T00:00:00+02:00", "2025-09-15T00:00:00+02:00"),
		"/api/v1/resolved/series?metric=heart_rate&window=10s" + intradayDay,
	} {
		e.call(series, target, "", http.StatusUnprocessableEntity, nil)
	}
}

func TestIntradaySourceSeries(t *testing.T) {
	e := newResolvedEnv(t, "2025-09-13", 3)
	e.exec(`INSERT INTO resolution_dirty (user_id, metric_id, local_date)
		SELECT DISTINCT user_id, metric_id, local_date FROM measurements ON CONFLICT DO NOTHING`)
	rebuild(t, e.d)

	// Every grain answers; the samples per source are the same at each one.
	samples := map[string]int{}
	for _, grain := range []string{"30s", "1m", "5m", "15m", "30m"} {
		var s oapi.SourceSeries
		e.call(srcSeries, "/api/v1/sources/series?metric=heart_rate&grain="+grain+intradayDay, "", http.StatusOK, &s)
		if string(s.Grain) != grain || s.Aggregation != "intensive" || len(s.Sources) < 2 {
			t.Fatalf("%s: %+v", grain, s)
		}
		minute := false
		for _, src := range s.Sources {
			n := 0
			for _, p := range src.Points {
				if p.Start == nil || p.LocalDate.String() != "2025-09-14" || p.Mean == nil || *p.Min > *p.Mean || *p.Max < *p.Mean || p.Sum != nil {
					t.Fatalf("%s %s: point %+v", grain, src.Provider, p)
				}
				n += p.N
			}
			key := sourceKey(t, src)
			if want, ok := samples[key]; ok && n != want {
				t.Errorf("%s %s: %d samples, %d at 30s", grain, src.Provider, n, want)
			}
			samples[key] = n
			minute = minute || src.SpacingS != nil && *src.SpacingS == 60
		}
		if !minute {
			t.Errorf("%s: no source reports its 60-second spacing", grain)
		}
	}

	// Steps by 30 minutes add up to the hourly aggregates.
	var half, hour oapi.SourceSeries
	e.call(srcSeries, "/api/v1/sources/series?metric=steps&grain=30m"+intradayDay, "", http.StatusOK, &half)
	e.call(srcSeries, "/api/v1/sources/series?metric=steps&grain=hour"+intradayDay, "", http.StatusOK, &hour)
	totals := map[string]float64{}
	for _, src := range hour.Sources {
		for _, p := range src.Points {
			totals[sourceKey(t, src)] += *p.Sum
		}
	}
	for _, src := range half.Sources {
		var sum float64
		for _, p := range src.Points {
			if p.Sum == nil || p.Mean != nil || p.Start.Minute()%30 != 0 {
				t.Fatalf("steps 30m point %+v", p)
			}
			sum += *p.Sum
		}
		if want := totals[sourceKey(t, src)]; math.Abs(sum-want) > 1e-3 {
			t.Errorf("%s: 30-minute steps %v, hours %v", src.Provider, sum, want)
		}
	}

	// Raw pages through every row once.
	total := 0
	for _, n := range samples {
		total += n
	}
	seen := map[string]bool{}
	target := "/api/v1/sources/series?metric=heart_rate&grain=raw&limit=2000" + intradayDay
	for pages := 0; ; pages++ {
		var s oapi.SourceSeries
		e.call(srcSeries, target, "", http.StatusOK, &s)
		rows := 0
		for _, src := range s.Sources {
			for _, p := range src.Points {
				k := sourceKey(t, src) + p.Start.String()
				if p.N != 1 || p.Value == nil || seen[k] {
					t.Fatalf("raw point %+v (repeated %v)", p, seen[k])
				}
				seen[k] = true
				rows++
			}
		}
		if rows > 2000 || s.HasMore == nil || pages > 10 {
			t.Fatalf("raw page of %d rows, has_more %v", rows, s.HasMore)
		}
		if !*s.HasMore {
			break
		}
		target = "/api/v1/sources/series?metric=heart_rate&grain=raw&limit=2000" + intradayDay + "&cursor=" + url.QueryEscape(*s.NextCursor)
	}
	if len(seen) != total {
		t.Errorf("raw: %d rows, %d samples in buckets", len(seen), total)
	}

	for _, target := range []string{
		"/api/v1/sources/series?metric=heart_rate&grain=1m" + span("2025-09-14T00:00:00+02:00", "2025-09-15T01:00:01+02:00"),
		"/api/v1/sources/series?metric=heart_rate&grain=raw&limit=2001" + intradayDay,
		"/api/v1/sources/series?metric=heart_rate&grain=10s" + intradayDay,
	} {
		e.call(srcSeries, target, "", http.StatusUnprocessableEntity, nil)
	}
}
