//go:build integration

package resolve

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// Cache tests (J09.9) on fixturegen slices: invalidation reaches derived codes, followers and the
// next night, pending marks block writes, truncation changes nothing, and Verify finds no diffs.

// cached lists the cached local dates of metric.
func (s *slice) cached(t *testing.T, metric string) []string {
	t.Helper()
	var list string
	if err := s.scan([]any{&list}, `SELECT COALESCE(string_agg(DISTINCT local_date::text, ',' ORDER BY local_date::text), '')
		FROM resolved_cache WHERE metric = $1`, metric); err != nil {
		t.Fatal(err)
	}
	if list == "" {
		return nil
	}
	return strings.Split(list, ",")
}

func (s *slice) mark(t *testing.T, code, date string) {
	t.Helper()
	if err := s.owner(`INSERT INTO resolution_dirty (user_id, metric_id, local_date)
		SELECT $1, id, $3 FROM metric_catalog WHERE code = $2 ON CONFLICT DO NOTHING`, s.user, code, date); err != nil {
		t.Fatal(err)
	}
}

// consume runs the rebuild over every mark, settled or not.
func (s *slice) consume(t *testing.T) {
	t.Helper()
	for {
		n, err := RebuildAggregates(s.ctx, s.d, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if n < rebuildBatch {
			return
		}
	}
}

// same checks that the cached and the live run of metric on the dates agree.
func (s *slice) same(t *testing.T, metric string, kind catalog.Window, from, to string) {
	t.Helper()
	req := Request{UserID: s.user, Metric: metric, Kind: kind, From: date(from), To: date(to), Now: instant(sliceANow)}
	cached, err := Run(s.ctx, s.d, req)
	if err != nil {
		t.Fatal(err)
	}
	req.Live = true
	live, err := Run(s.ctx, s.d, req)
	if err != nil {
		t.Fatal(err)
	}
	if key, ok := sameResults(cached, live); !ok {
		t.Errorf("%s: cached differs from live at window %s", metric, key)
	}
}

func datesFrom(from string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = date(from).AddDate(0, 0, i).Format(dateLayout)
	}
	return out
}

func without(all []string, drop ...string) []string {
	return slices.DeleteFunc(slices.Clone(all), func(d string) bool { return slices.Contains(drop, d) })
}

func TestCacheInvalidation(t *testing.T) {
	s := loadSlice(t, "2025-02-15", 12)
	const from, to = "2025-02-15", "2025-02-26"
	all := datesFrom(from, 12)
	near := datesFrom("2025-02-18", 6) // dates whose results read 02-20: three days before to two after
	warm := func(metrics ...string) {
		for _, m := range metrics {
			s.run(t, m, "", from, to, nil, sliceANow)
		}
	}
	expect := func(metric string, want []string) {
		t.Helper()
		if got := s.cached(t, metric); !slices.Equal(got, want) {
			t.Errorf("%s cached on %v, want %v", metric, got, want)
		}
	}

	// A derived code reads its source metric: a heart_rate mark drops nocturnal RHR around it,
	// and while the mark is pending those dates are not cached again.
	warm("resting_heart_rate_nocturnal")
	expect("resting_heart_rate_nocturnal", all)
	s.mark(t, "heart_rate", "2025-02-20")
	expect("resting_heart_rate_nocturnal", without(all, near...))
	warm("resting_heart_rate_nocturnal")
	expect("resting_heart_rate_nocturnal", without(all, near...))
	s.consume(t)
	warm("resting_heart_rate_nocturnal")
	expect("resting_heart_rate_nocturnal", all)

	// A follower reads its leader.
	warm("weight", "body_fat_ratio")
	s.mark(t, "weight", "2025-02-20")
	expect("body_fat_ratio", without(all, near...))
	s.consume(t)

	// A session with sleep_date D can belong to night D+1 (ADR-0009).
	warm("sleep")
	s.mark(t, "sleep_total", "2025-02-20")
	if got := s.cached(t, "sleep"); slices.Contains(got, "2025-02-21") || slices.Contains(got, "2025-02-20") {
		t.Errorf("sleep still cached on nights 02-20/02-21: %v", got)
	}
	s.consume(t)

	// An override marks its date; the cache then serves the overridden value.
	warm("weight")
	if _, err := NewOverrides(s.d).Create(s.ctx, By{UserID: s.user, Actor: "test"}, NewOverride{
		Metric: "weight", Kind: catalog.WindowLocalDay, Key: "2025-02-20", LocalDate: date("2025-02-20"),
		Action: SetValue, Value: 70, Unit: "kg", Note: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	expect("weight", without(all, near...))
	s.consume(t)
	s.same(t, "weight", "", from, to)

	// Activating a rule drops the metric and its followers.
	warm("weight", "body_fat_ratio")
	b, _ := LookupBuiltin("weight")
	spec, err := json.Marshal(b.Rule)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(s.d).Create(s.ctx, By{UserID: s.user, Actor: "test"}, spec, "copy", true); err != nil {
		t.Fatal(err)
	}
	expect("weight", nil)
	expect("body_fat_ratio", nil)

	// Workouts mark nothing dirty; their trigger drops rules with a workout context.
	warm("distance_walk_run", "steps")
	if err := s.owner(`INSERT INTO workouts (id, user_id, start_at, end_at, local_date, sport, provider_id, connection_id,
		dedupe_key, normalizer_version_id)
		SELECT gen_random_uuid(), user_id, '2025-02-20T10:00:00Z', '2025-02-20T11:00:00Z', '2025-02-20', 'running',
		provider_id, connection_id, substring(sha256('synthetic workout') for 16), normalizer_version_id
		FROM measurements WHERE user_id = $1 LIMIT 1`, s.user); err != nil {
		t.Fatal(err)
	}
	expect("distance_walk_run", without(all, near...)) // the hourly composition reads the days around the workout
	expect("steps", all)

	// A timezone change moves window bounds: the owner's cache is cleared.
	if err := s.owner(`INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES (gen_random_uuid(), $1, 'Europe/Berlin', '2025-02-22T00:00:00Z')`, s.user); err != nil {
		t.Fatal(err)
	}
	expect("steps", nil)

	for _, m := range []string{"resting_heart_rate_nocturnal", "weight", "body_fat_ratio", "sleep", "steps", "distance_walk_run"} {
		s.same(t, m, "", from, to)
	}
}

// TestCacheTruncate checks that truncating both tables changes neither results nor rebuilt
// aggregates, and that the aggregates add up to the rows they summarise. The slice holds the
// spring DST change (a 23-hour day).
func TestCacheTruncate(t *testing.T) {
	s := loadSlice(t, "2025-03-28", 4)
	type req struct {
		metric string
		kind   catalog.Window
	}
	reqs := []req{{"steps", ""}, {"steps", catalog.WindowHour}, {"heart_rate", catalog.WindowHour}, {"sleep", ""},
		{"blood_pressure", ""}, {"blood_pressure", catalog.WindowReading}, {"weight", ""}, {"body_fat_ratio", ""},
		{"resting_heart_rate_nocturnal", ""}, {"resting_heart_rate", ""}, {"height", ""}}
	markAll := func() {
		if err := s.owner(`INSERT INTO resolution_dirty (user_id, metric_id, local_date)
			SELECT DISTINCT user_id, metric_id, local_date FROM measurements ON CONFLICT DO NOTHING`); err != nil {
			t.Fatal(err)
		}
		s.consume(t)
	}
	snapshot := func() (string, []Result) {
		var all []Result
		for _, r := range reqs {
			for range 2 { // a miss, then a hit
				out, err := Run(s.ctx, s.d, Request{UserID: s.user, Metric: r.metric, Kind: r.kind, From: date("2025-03-28"), To: date("2025-03-31"), Now: instant(sliceANow)})
				if err != nil {
					t.Fatal(err)
				}
				if len(all) > 0 && len(out) == 0 && r.metric != "height" {
					t.Errorf("%s %s: no results", r.metric, r.kind)
				}
				all = append(all, out...)
			}
		}
		var agg string
		if err := s.scan([]any{&agg}, `SELECT COALESCE(jsonb_agg(a ORDER BY metric_id, hour_start, source_key)::text, '')
			FROM source_hourly_aggregates a`); err != nil {
			t.Fatal(err)
		}
		return agg, all
	}
	markAll()
	agg1, res1 := snapshot()
	if err := s.owner(`TRUNCATE resolved_cache, source_hourly_aggregates`); err != nil {
		t.Fatal(err)
	}
	markAll()
	agg2, res2 := snapshot()
	if agg1 != agg2 || agg1 == "" {
		t.Errorf("aggregates differ after truncation (%d vs %d bytes)", len(agg1), len(agg2))
	}
	if key, ok := sameResults(res1, res2); !ok {
		t.Errorf("results differ after truncation at window %s", key)
	}

	// Every sample counts once and every interval adds up to its value.
	var samples, rows int64
	var sum, want, lo, hi, wlo, whi float64
	if err := s.scan([]any{&samples, &rows, &lo, &hi, &wlo, &whi, &sum, &want}, `SELECT
		(SELECT sum(samples) FROM source_hourly_aggregates WHERE metric_id = (SELECT id FROM metric_catalog WHERE code = 'heart_rate')),
		(SELECT count(*) FROM measurements WHERE metric_id = (SELECT id FROM metric_catalog WHERE code = 'heart_rate')),
		(SELECT min(min_value) FROM source_hourly_aggregates WHERE metric_id = (SELECT id FROM metric_catalog WHERE code = 'heart_rate')),
		(SELECT max(max_value) FROM source_hourly_aggregates WHERE metric_id = (SELECT id FROM metric_catalog WHERE code = 'heart_rate')),
		(SELECT min(value) FROM measurements WHERE metric_id = (SELECT id FROM metric_catalog WHERE code = 'heart_rate')),
		(SELECT max(value) FROM measurements WHERE metric_id = (SELECT id FROM metric_catalog WHERE code = 'heart_rate')),
		(SELECT sum(interval_sum) FROM source_hourly_aggregates WHERE metric_id = (SELECT id FROM metric_catalog WHERE code = 'steps')),
		(SELECT sum(value) FROM measurements WHERE kind = 'interval' AND metric_id = (SELECT id FROM metric_catalog WHERE code = 'steps'))`); err != nil {
		t.Fatal(err)
	}
	if samples != rows || lo != wlo || hi != whi || math.Abs(sum-want) > 1e-6*want {
		t.Errorf("aggregates: %d samples for %d rows, range %v..%v for %v..%v, steps %v for %v", samples, rows, lo, hi, wlo, whi, sum, want)
	}
	var marks int
	if err := s.scan([]any{&marks}, `SELECT count(*) FROM resolution_dirty`); err != nil || marks != 0 {
		t.Errorf("%d marks left (%v)", marks, err)
	}
}

// TestVerify resolves random (metric, date) pairs of every rule in effect alone, live and from a
// cache filled by ranges; a corrupted row shows up as a diff. The CI run is small; the 1,000
// window run on a fixturegen year is TestResolveBaseline.
func TestVerify(t *testing.T) {
	s := loadSlice(t, "2025-03-28", 4)
	rep, err := Verify(s.ctx, s.d, date("2025-03-28"), date("2025-03-31"), 200, rand.New(rand.NewPCG(9, 12)))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Windows != 200 || len(rep.Diffs) != 0 {
		t.Fatalf("verify: %d windows, diffs %v", rep.Windows, rep.Diffs)
	}
	if err := s.owner(`UPDATE resolved_cache SET results = '[]' WHERE metric IN ('steps', 'sleep', 'weight')`); err != nil {
		t.Fatal(err)
	}
	rep, err = Verify(s.ctx, s.d, date("2025-03-28"), date("2025-03-31"), 200, rand.New(rand.NewPCG(9, 12)))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Diffs) == 0 {
		t.Error("verify missed corrupted cache rows")
	}
}
