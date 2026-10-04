//go:build integration

package api

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
)

// Intraday series budget (J26.4, docs/resource-budget.md#intraday-series): a day of WHOOP-like
// 6-second heart rate (14,400 rows) plus a second source resolves to 1-minute buckets in under
// 300 ms. Timings are logged (run with -v); only the budget itself fails the test.
func TestIntradaySeriesBudget(t *testing.T) {
	e := newResolvedEnvStep(t, "2025-09-14", 1, 6)
	e.exec(`INSERT INTO resolution_dirty (user_id, metric_id, local_date)
		SELECT DISTINCT user_id, metric_id, local_date FROM measurements ON CONFLICT DO NOTHING`)
	rebuild(t, e.d)
	e.exec(`ANALYZE`)

	timed := func(name, pattern, target string, v any) time.Duration {
		var all []time.Duration
		for range 7 {
			start := time.Now()
			e.call(pattern, target, "", http.StatusOK, v)
			all = append(all, time.Since(start))
		}
		slices.Sort(all)
		t.Logf("%-32s median %6.1f ms, max %6.1f ms", name, ms(all[len(all)/2]), ms(all[len(all)-1]))
		return all[len(all)/2]
	}

	for _, w := range []string{"1m", "30s"} {
		var s oapi.ResolvedSeries
		d := timed("resolved heart_rate "+w, series, "/api/v1/resolved/series?metric=heart_rate&limit=5000&window="+w+intradayDay, &s)
		if w == "1m" && d > 300*time.Millisecond {
			t.Errorf("resolved 1m: median %v, budget 300 ms", d)
		}
	}
	for _, g := range []string{"30s", "1m"} {
		var s oapi.SourceSeries
		timed("sources heart_rate "+g, srcSeries, "/api/v1/sources/series?metric=heart_rate&grain="+g+intradayDay, &s)
	}
	var s oapi.SourceSeries
	timed("sources heart_rate raw (2000)", srcSeries, "/api/v1/sources/series?metric=heart_rate&grain=raw&limit=2000"+intradayDay, &s)
	n := 0
	for _, src := range s.Sources {
		n += len(src.Points)
	}
	if n > 2000 {
		t.Errorf("raw page of %d points, limit 2000", n)
	}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
