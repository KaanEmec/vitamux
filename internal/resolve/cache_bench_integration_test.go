//go:build integration

package resolve

import (
	"math/rand/v2"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// Resolution benchmark (J09.9, docs/benchmarks/baseline.md#resolution-and-cache): ten metrics
// with day or night windows, each resolved over a date range, cold (empty resolved_cache) and
// warm (every date cached), one request at a time and as a dashboard of ten parallel requests.
// TestResolveSmoke runs in CI on a short slice with generous limits; TestResolveBaseline is the
// recorded run on a fixturegen year:
//
//	VITAMUX_RESOLVE_BASELINE=1 go test -tags integration -run TestResolveBaseline -v -timeout 60m ./internal/resolve/
//
// (VITAMUX_VOLUME_DATASET=fixtures/generated reuses a generated year.)
var benchMetrics = []string{"steps", "distance_walk_run", "active_energy", "resting_heart_rate", "resting_heart_rate_nocturnal",
	"weight", "body_fat_ratio", "blood_pressure", "sleep", "spo2"}

type benchResult struct {
	cold, warm         []time.Duration // per request
	coldDash, warmDash []time.Duration // ten parallel requests, wall time
}

func pct(ds []time.Duration, p float64) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	i := int(float64(len(s))*p+0.999999) - 1
	return s[max(0, min(i, len(s)-1))]
}

func benchResolve(t *testing.T, s *slice, metrics []string, kind catalog.Window, from, to string, rounds int) benchResult {
	t.Helper()
	run := func(m string) time.Duration {
		start := time.Now()
		if _, err := Run(s.ctx, s.d, Request{UserID: s.user, Metric: m, Kind: kind, From: date(from), To: date(to)}); err != nil {
			t.Error(m, err)
		}
		return time.Since(start)
	}
	dash := func() time.Duration {
		start := time.Now()
		var wg sync.WaitGroup
		for _, m := range metrics {
			wg.Go(func() { run(m) })
		}
		wg.Wait()
		return time.Since(start)
	}
	truncate := func() {
		if err := s.owner(`TRUNCATE resolved_cache`); err != nil {
			t.Fatal(err)
		}
	}
	var r benchResult
	for range rounds {
		truncate()
		for _, m := range metrics {
			r.cold = append(r.cold, run(m))
		}
		for _, m := range metrics {
			r.warm = append(r.warm, run(m))
		}
		truncate()
		r.coldDash = append(r.coldDash, dash())
		r.warmDash = append(r.warmDash, dash())
	}
	return r
}

func (r benchResult) log(t *testing.T, label string) {
	t.Helper()
	t.Logf("%s: per request cold p50 %v p95 %v, warm p50 %v p95 %v; dashboard cold p50 %v p95 %v, warm p50 %v p95 %v",
		label, pct(r.cold, 0.5), pct(r.cold, 0.95), pct(r.warm, 0.5), pct(r.warm, 0.95),
		pct(r.coldDash, 0.5), pct(r.coldDash, 0.95), pct(r.warmDash, 0.5), pct(r.warmDash, 0.95))
}

// TestResolveSmoke: 14 days of the ten metrics on a 6 s heart rate slice. Limits are about ten
// times the measured values, to catch order-of-magnitude regressions only.
func TestResolveSmoke(t *testing.T) {
	s := loadDir(t, generate(t, t.TempDir(), "2025-03-01", 15))
	r := benchResolve(t, s, benchMetrics, "", "2025-03-02", "2025-03-15", 2)
	r.log(t, "14 days")
	if c, w := pct(r.cold, 0.95), pct(r.warm, 0.95); c > 5*time.Second || w > time.Second {
		t.Errorf("p95 cold %v (limit 5 s), warm %v (limit 1 s)", c, w)
	}
}

// TestResolveBaseline is the recorded benchmark: a fixturegen year, 90 days of the ten metrics
// (and heart_rate by local day as the densest case), then Verify on 1,000 random windows.
func TestResolveBaseline(t *testing.T) {
	if os.Getenv("VITAMUX_RESOLVE_BASELINE") == "" {
		t.Skip("set VITAMUX_RESOLVE_BASELINE=1 (about 10 min)")
	}
	dir := os.Getenv("VITAMUX_VOLUME_DATASET")
	if dir == "" {
		dir = generate(t, t.TempDir(), "2025-01-01", 365)
	}
	start := time.Now()
	s := loadDir(t, dir)
	if err := s.owner(`VACUUM (ANALYZE) measurements`); err != nil {
		t.Fatal(err)
	}
	t.Logf("loaded in %v", time.Since(start).Round(time.Second))

	const from, to, rounds = "2025-03-02", "2025-05-30", 5
	r := benchResolve(t, s, benchMetrics, "", from, to, rounds)
	r.log(t, "90 days x 10 metrics")
	for i, m := range benchMetrics {
		var cold, warm []time.Duration
		for k := range rounds {
			cold, warm = append(cold, r.cold[k*len(benchMetrics)+i]), append(warm, r.warm[k*len(benchMetrics)+i])
		}
		t.Logf("  %-30s cold p50 %v max %v, warm p50 %v max %v", m, pct(cold, 0.5), pct(cold, 1), pct(warm, 0.5), pct(warm, 1))
	}
	hr := benchResolve(t, s, []string{"heart_rate"}, catalog.WindowLocalDay, from, to, rounds)
	hr.log(t, "90 days heart_rate local_day")

	start = time.Now()
	rep, err := Verify(s.ctx, s.d, date("2025-01-01"), date("2025-12-31"), 1000, rand.New(rand.NewPCG(42, 0)))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("verify: %d windows, %d diffs in %v", rep.Windows, len(rep.Diffs), time.Since(start).Round(time.Second))
	for _, d := range rep.Diffs {
		t.Error(d)
	}
	if p := pct(r.warm, 0.95); p > 500*time.Millisecond {
		t.Errorf("warm p95 %v, target 500 ms", p)
	}
	if p := pct(r.cold, 0.95); p > 2*time.Second {
		t.Errorf("cold p95 %v, target 2 s", p)
	}
}
