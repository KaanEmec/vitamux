//go:build integration

package resolve

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/db/fixtureload"
)

// Scenario suite (J09.8): the edge cases of docs/architecture/resolution.md#edge-cases on
// tools/fixturegen data, loaded with fixtureload and resolved through Run, so the loader is
// exercised end to end. Each test generates the slice that holds its scenario's dates
// (fixtures/README.md maps them). Cases the generator cannot produce run in memory:
// 10 (no heart rate variability) in result_test.go, and the extension scenarios with a ring or
// chest strap in extensions_test.go and TestExplanationExtensions.

// slice is one generated and loaded fixturegen slice.
type slice struct {
	ctx   context.Context
	d     *db.DB
	owner func(stmt string, args ...any) error              // runs a statement as the schema owner
	scan  func(dest []any, query string, args ...any) error // scans one row of a query as the schema owner
	user  uuid.UUID
}

func loadSlice(t *testing.T, start string, days int) *slice {
	t.Helper()
	return loadDir(t, generate(t, t.TempDir(), start, days, "-hr-step", "60"))
}

// generate runs fixturegen for the days from start into dir and returns dir.
func generate(t testing.TB, dir, start string, days int, flags ...string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	args := append([]string{"run", "./tools/fixturegen", "-out", dir, "-start", start, "-days", strconv.Itoa(days)}, flags...)
	cmd := exec.CommandContext(t.Context(), "go", args...)
	cmd.Dir = filepath.Join(filepath.Dir(file), "..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixturegen: %v\n%s", err, out)
	}
	return dir
}

// loadDir loads a fixturegen dataset into a fresh database with the persona's timezone periods.
func loadDir(t testing.TB, dir string) *slice {
	t.Helper()
	u, app := dbtest.Migrated(t)
	stats, err := fixtureload.Load(t.Context(), app, dir)
	if err != nil {
		t.Fatal(err)
	}
	owner := dbtest.Pool(t, u, db.OwnerRole)
	s := &slice{ctx: t.Context(), d: db.New(app), user: stats.UserID,
		owner: func(stmt string, args ...any) error {
			_, err := owner.Exec(t.Context(), stmt, args...)
			return err
		},
		scan: func(dest []any, query string, args ...any) error {
			return owner.QueryRow(t.Context(), query, args...).Scan(dest...)
		}}
	// The persona's timezone periods (fixtures/README.md): Berlin, New York for the trip, Berlin.
	for _, p := range []struct{ from, tz string }{
		{"2024-01-01T00:00:00Z", "Europe/Berlin"},
		{"2025-05-11T22:00:00Z", "America/New_York"},
		{"2025-05-22T04:00:00Z", "Europe/Berlin"},
	} {
		if _, err := app.Exec(t.Context(), `INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES ($1, $2, $3, $4)`,
			uuid.New(), s.user, p.tz, p.from); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// run resolves metric on the dates with the rule (nil: the rule in effect) at now.
func (s *slice) run(t *testing.T, metric string, kind catalog.Window, from, to string, rule *Rule, now string) []Result {
	t.Helper()
	req := Request{UserID: s.user, Metric: metric, Kind: kind, From: date(from), To: date(to), Now: instant(now)}
	if rule != nil {
		v := testVersion(rule)
		req.Rule = &v
	}
	out, err := Run(s.ctx, s.d, req)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// parseRule parses and validates a rule spec.
func parseRule(t *testing.T, spec string) *Rule {
	t.Helper()
	r, err := ParseRule([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func byKey(rs []Result) map[string]Result {
	out := map[string]Result{}
	for _, r := range rs {
		out[r.Window.Key] = r
	}
	return out
}

func hasWarning(r Result, c Warning) bool {
	return slices.ContainsFunc(r.Warnings, func(w WindowWarning) bool { return w.Code == c })
}

const sliceANow = "2025-03-01T00:00:00Z" // after slice A: every window is closed

// Slice A, 2025-02-15..19: Garmin dead on 02-17 and 02-18, a manual heart rate on 02-17, weigh-ins.
func TestScenariosSliceA(t *testing.T) {
	s := loadSlice(t, "2025-02-15", 5)

	// 1. A daily total and the intervals of one source are never added; hours ignore the total.
	garminSteps := parseRule(t, `{"schema":"vitamux.rule/1","metric":"steps","window":{"kind":"local_day"},
		"groups":[{"id":"garmin","match":[{"provider":"garmin"}]}],"strategy":{"op":"first_available"}}`)
	day := s.run(t, "steps", catalog.WindowLocalDay, "2025-02-16", "2025-02-16", garminSteps, sliceANow)[0]
	g := input(day, "garmin")
	if g.Basis != BasisDailyValue || g.Count != 1 {
		t.Errorf("1: day basis %s from %d rows", g.Basis, g.Count)
	}
	hours := s.run(t, "steps", catalog.WindowHour, "2025-02-16", "2025-02-16", garminSteps, sliceANow)
	sum := 0.0
	for _, h := range hours {
		if b := input(h, "garmin").Basis; b == BasisDailyValue {
			t.Errorf("1: hour %s used the daily total", h.Window.Key)
		}
		if h.Value != nil {
			sum += *h.Value
		}
	}
	if len(hours) != 24 || !near(sum, *day.Value) { // the generator's total is the interval sum
		t.Errorf("1: %d hours sum to %v, day %v", len(hours), sum, *day.Value)
	}
	golden(t, "scenario-01-daily-total", day, hours[10])

	// 2. A provider relayed through Apple Health while also direct: the origin is relayed and
	// the rule excludes it; under the built-in it is only the direct group's fallback.
	hrRule := parseRule(t, `{"schema":"vitamux.rule/1","metric":"heart_rate","window":{"kind":"hour"},
		"groups":[{"id":"garmin","match":[{"provider":"garmin"}]},{"id":"apple_watch","match":[{"provider":"apple_health","device_type":"watch","relayed":false}]}],
		"exclude":[{"provider":"apple_health","relayed":true}],"strategy":{"op":"first_available"},"quality":{"exclude_flags":["manual_entry"]}}`)
	h := byKey(s.run(t, "heart_rate", catalog.WindowHour, "2025-02-16", "2025-02-16", hrRule, sliceANow))["2025-02-16T09:00:00Z"]
	ex := h.Inputs[2]
	if ex.Status != StatusExcluded || !ex.Sources[0].Relayed || ex.Sources[0].OriginKey != "com.garmin.connect.mobile" || ex.Reason != "exclude: provider=apple_health relayed=true" {
		t.Errorf("2: excluded entry %+v", ex)
	}
	builtin := byKey(s.run(t, "heart_rate", catalog.WindowHour, "2025-02-16", "2025-02-16", nil, sliceANow))["2025-02-16T09:00:00Z"]
	if in := input(builtin, "garmin_apple"); in.Status == StatusUsed || in.Count == 0 {
		t.Errorf("2: built-in relay group %+v", in)
	}
	golden(t, "scenario-02-relayed", h, builtin)

	// 3. A cross-source sum needs the acknowledgement and repeats its warning in every result.
	sumRule := parseRule(t, `{"schema":"vitamux.rule/1","metric":"steps","window":{"kind":"local_day"},
		"groups":[{"id":"apple_watch","match":[{"provider":"apple_health","device_type":"watch","relayed":false}]},{"id":"iphone","match":[{"device_type":"phone"}]}],
		"strategy":{"op":"sum_across_sources"},"acknowledged_warnings":["cross_source_sum_duplicate_risk"]}`)
	sums := s.run(t, "steps", catalog.WindowLocalDay, "2025-02-15", "2025-02-19", sumRule, sliceANow)
	for _, r := range sums {
		if r.Status != ResultNoData && !hasWarning(r, WarnCrossSourceSum) {
			t.Errorf("3: %s without the sum warning", r.Window.Key)
		}
	}
	sumRule.AcknowledgedWarnings = nil
	v := testVersion(sumRule)
	if _, err := Run(s.ctx, s.d, Request{UserID: s.user, Metric: "steps", From: date("2025-02-16"), To: date("2025-02-16"), Rule: &v}); !errors.Is(err, ErrUnacknowledgedSum) {
		t.Errorf("3: unacknowledged sum: %v", err)
	}
	golden(t, "scenario-03-sum", sums[1])

	// 5. Intervals crossing a window boundary are pro-rated: the hours add up to the day.
	appleSteps := parseRule(t, `{"schema":"vitamux.rule/1","metric":"steps","window":{"kind":"hour"},
		"groups":[{"id":"apple_watch","match":[{"provider":"apple_health","device_type":"watch","relayed":false}]}],"strategy":{"op":"first_available"}}`)
	ah := s.run(t, "steps", catalog.WindowHour, "2025-02-16", "2025-02-16", appleSteps, sliceANow)
	ad := s.run(t, "steps", catalog.WindowLocalDay, "2025-02-16", "2025-02-16", appleSteps, sliceANow)[0]
	sum, prorated := 0.0, 0
	for _, r := range ah {
		if r.Value != nil {
			sum += *r.Value
		}
		if input(r, "apple_watch").Prorated {
			prorated++
		}
	}
	if prorated == 0 || !near(sum, *ad.Value) {
		t.Errorf("5: %d pro-rated hours, hours %v, day %v", prorated, sum, *ad.Value)
	}
	golden(t, "scenario-05-prorated", ah[9])

	// 9. Fallback is per window: the dead Garmin days fall back, the others stay direct.
	ladder := parseRule(t, `{"schema":"vitamux.rule/1","metric":"steps","window":{"kind":"local_day"},
		"groups":[{"id":"garmin","match":[{"provider":"garmin"}]},{"id":"apple_watch","match":[{"provider":"apple_health","device_type":"watch","relayed":false}]}],
		"strategy":{"op":"first_available"}}`)
	week := s.run(t, "steps", catalog.WindowLocalDay, "2025-02-15", "2025-02-19", ladder, sliceANow)
	for _, r := range week {
		want := "garmin"
		if r.Window.Key == "2025-02-17" || r.Window.Key == "2025-02-18" {
			want = "apple_watch"
		}
		if r.Selected != want {
			t.Errorf("9: %s selected %q, want %s", r.Window.Key, r.Selected, want)
		}
	}
	if got := SourcesUsed(week); !slices.Equal(got, []string{"garmin", "apple_watch"}) {
		t.Errorf("9: sources used %v", got)
	}
	golden(t, "scenario-09-per-window", week...)

	// 11. Manual entries are flagged and never reach heart_rate under the built-in.
	noon := byKey(s.run(t, "heart_rate", catalog.WindowHour, "2025-02-17", "2025-02-17", nil, sliceANow))
	var manual *ResultInput
	for _, r := range noon {
		for i, in := range r.Inputs {
			if len(in.Sources) > 0 && in.Sources[0].Manual {
				manual = &r.Inputs[i]
				golden(t, "scenario-11-manual", r)
			}
		}
	}
	if manual == nil || manual.Status != StatusNotInRule || manual.Selected {
		t.Errorf("11: manual entry %+v", manual)
	}

	// 12. Late or corrected data: a correction supersedes the row, the next resolution reads
	// the new value, and computed_at tells when it was computed.
	before := s.run(t, "steps", catalog.WindowLocalDay, "2025-02-16", "2025-02-16", garminSteps, sliceANow)[0]
	if err := s.owner(`WITH old AS (
		  UPDATE measurements SET superseded_at = now() WHERE user_id = $1 AND kind = 'daily_value' AND local_date = '2025-02-16'
		    AND metric_id = (SELECT id FROM metric_catalog WHERE code = 'steps') AND superseded_at IS NULL RETURNING *)
		INSERT INTO measurements (user_id, metric_id, kind, start_at, end_at, tz_offset_min, local_date, value, provider_id, connection_id,
		  device_id, external_id, dedupe_key, raw_payload_id, normalizer_version_id)
		SELECT user_id, metric_id, kind, start_at, end_at, tz_offset_min, local_date, value + 700, provider_id, connection_id,
		  device_id, external_id, dedupe_key, raw_payload_id, normalizer_version_id FROM old`, s.user); err != nil {
		t.Fatal(err)
	}
	after := s.run(t, "steps", catalog.WindowLocalDay, "2025-02-16", "2025-02-16", garminSteps, "2025-03-02T06:00:00Z")[0]
	if *after.Value != *before.Value+700 || !after.ComputedAt.Equal(instant("2025-03-02T06:00:00Z")) || slices.Equal(after.Inputs[0].RecordRefs, before.Inputs[0].RecordRefs) {
		t.Errorf("12: before %v after %v at %v", *before.Value, *after.Value, after.ComputedAt)
	}

	// 14. A duplicate weigh-in via the relay: excluded by a relayed exclusion, else the scale
	// group holds both and the reading order decides; values never mix within a reading.
	weightRule := parseRule(t, `{"schema":"vitamux.rule/1","metric":"weight","window":{"kind":"local_day"},
		"groups":[{"id":"scale","match":[{"device_type":"scale","entry":"device"}]},{"id":"manual","match":[{"entry":"manual"}]}],
		"exclude":[{"relayed":true}],"strategy":{"op":"first_available"}}`)
	var dup []Result
	for _, r := range s.run(t, "weight", catalog.WindowLocalDay, "2025-02-15", "2025-02-19", weightRule, sliceANow) {
		if len(r.Inputs) > 2 && r.Inputs[2].Status == StatusExcluded {
			dup = append(dup, r)
			break
		}
	}
	if len(dup) == 0 {
		t.Fatal("14: no relayed weigh-in in the slice")
	}
	plain := s.run(t, "weight", catalog.WindowLocalDay, dup[0].Window.Key, dup[0].Window.Key, nil, sliceANow)[0]
	if *plain.Value != *dup[0].Value || input(plain, "scale").Count != 1 {
		t.Errorf("14: built-in %s vs excluded %s", resultText(plain), resultText(dup[0]))
	}
	golden(t, "scenario-14-duplicate-weigh-in", dup[0], plain)

	// E5 through the loader: body composition follows the group weight selected that day.
	fat := s.run(t, "body_fat_ratio", catalog.WindowLocalDay, dup[0].Window.Key, dup[0].Window.Key, nil, sliceANow)[0]
	if fat.Follow != "weight" || fat.FollowGroup != "scale" || fat.Selected != "scale" {
		t.Errorf("follow: %s", resultText(fat))
	}
	golden(t, "loader-follow", fat)

	// The all-sources drilldown through the loader.
	req := Request{UserID: s.user, Metric: "steps", From: date("2025-02-16"), To: date("2025-02-16"), Now: instant(sliceANow), Sources: true}
	gv := testVersion(ladder)
	req.Rule = &gv
	drill, err := Run(s.ctx, s.d, req)
	if err != nil {
		t.Fatal(err)
	}
	var garmin *SourceView
	for i, v := range drill[0].Sources {
		if v.Group == "garmin" {
			garmin = &drill[0].Sources[i]
		}
	}
	if len(drill[0].Sources) != 4 || garmin == nil || garmin.Values["daily_value"] != *before.Value+700 || !near(garmin.Values["interval_sum"], *before.Value) {
		t.Errorf("drilldown: %+v", drill[0].Sources)
	}

	// 15. Today is partial; max_staleness lets a stale preferred source fall back.
	today := s.run(t, "steps", catalog.WindowLocalDay, "2025-02-19", "2025-02-19", ladder, "2025-02-19T14:00:00Z")[0]
	if !today.Partial {
		t.Error("15: today not partial")
	}
	stale := parseRule(t, `{"schema":"vitamux.rule/1","metric":"heart_rate","window":{"kind":"latest"},
		"groups":[{"id":"garmin","match":[{"provider":"garmin"}]},{"id":"apple_watch","match":[{"provider":"apple_health","device_type":"watch","relayed":false}]}],
		"strategy":{"op":"first_available"},"quality":{"max_staleness":"1h"}}`)
	latest := s.run(t, "heart_rate", catalog.WindowLatest, "2025-02-16", "2025-02-17", stale, sliceANow)
	if latest[0].Selected != "garmin" || latest[1].Selected != "apple_watch" || input(latest[1], "garmin").Status != StatusStale {
		t.Errorf("15: latest %s %s", resultText(latest[0]), resultText(latest[1]))
	}
	golden(t, "scenario-15-today-and-stale", today, latest[1])
}

// Slice 2025-06-02..05: the Garmin watch is off 13:00-21:00 on 06-03 and 06-04 (partial wear).
func TestScenariosPartialWear(t *testing.T) {
	s := loadSlice(t, "2025-06-02", 4)
	const now = "2025-06-10T00:00:00Z"

	// 8. Partial wear below coverage is below_quality, so first_available falls through for
	// that window only.
	hr := parseRule(t, `{"schema":"vitamux.rule/1","metric":"heart_rate","window":{"kind":"local_day"},
		"groups":[{"id":"garmin","match":[{"provider":"garmin"}]},{"id":"apple_watch","match":[{"provider":"apple_health","device_type":"watch","relayed":false}]}],
		"strategy":{"op":"first_available"},"quality":{"min_coverage":0.8}}`)
	days := s.run(t, "heart_rate", catalog.WindowLocalDay, "2025-06-02", "2025-06-05", hr, now)
	for i, want := range []string{"garmin", "apple_watch", "apple_watch", "garmin"} {
		if days[i].Selected != want || want != "garmin" && input(days[i], "garmin").Reason != ReasonCoverage {
			t.Errorf("8: %s", resultText(days[i]))
		}
	}
	golden(t, "scenario-08-partial-wear", days...)

	// 4. A day built from hourly picks (E9) warns when it exceeds every single source. The
	// Garmin watch reports steps, so its worn hours without steps are a measured 0 (J24.3).
	steps := parseRule(t, `{"schema":"vitamux.rule/1","metric":"steps","window":{"kind":"local_day"},
		"groups":[{"id":"garmin","match":[{"provider":"garmin"}]},{"id":"iphone","match":[{"device_type":"phone"}]}],
		"strategy":{"op":"first_available"},"compose":{"from":"hour","op":"first_available"},
		"quality":{"min_coverage":0.6,"require_wear":"heart_rate"}}`)
	composed := s.run(t, "steps", catalog.WindowLocalDay, "2025-06-03", "2025-06-03", steps, now)[0]
	if len(composed.Hours) != 24 || !hasWarning(composed, WarnCompositeExceeds) {
		t.Errorf("4: %s", resultText(composed))
	}
	golden(t, "scenario-04-composite", composed)
}

// Nights of March: Garmin splits 03-12 around a 75-minute wake (two episodes); DST on 03-30.
func TestScenariosMarch(t *testing.T) {
	s := loadSlice(t, "2025-03-11", 21)
	const now = "2025-04-15T00:00:00Z"

	// 7. A night split by a long wake: two episodes; the main one is the longer.
	garminSleep := parseRule(t, `{"schema":"vitamux.rule/1","metric":"sleep","window":{"kind":"local_night"},
		"groups":[{"id":"garmin","match":[{"provider":"garmin"}]}],"strategy":{"op":"event_priority"}}`)
	eps := s.run(t, "sleep_total", catalog.WindowSleepEpisode, "2025-03-12", "2025-03-12", garminSleep, now)
	if len(eps) != 2 {
		t.Fatalf("7: %d episodes on the split night", len(eps))
	}
	golden(t, "scenario-07-split-night-long", eps...)

	// 6. Missing sleep stages give no_stage_data, not 0: find the first night whose selected
	// Apple Watch session has no stages (built-in sleep rule).
	var noStage []Result
	for _, r := range s.run(t, "sleep_deep", catalog.WindowLocalNight, "2025-03-11", "2025-03-31", nil, now) {
		if r.Selected == "apple_watch" && r.Status == ResultNoData {
			noStage = append(noStage, r)
			break
		}
	}
	if len(noStage) == 0 {
		t.Fatal("6: no unstaged Apple Watch night selected in March")
	}
	night := noStage[0].Window.Key
	if in := input(noStage[0], "apple_watch"); in.Status != StatusNoStageData || in.Value != nil {
		t.Errorf("6: %+v", in)
	}
	family := s.run(t, "sleep", catalog.WindowLocalNight, night, night, nil, now)[0]
	if family.Missing["sleep_deep"] != StatusNoStageData || family.Components["sleep_total"] == 0 {
		t.Errorf("6: family %s", resultText(family))
	}
	golden(t, "scenario-06-no-stage-data", noStage[0], family)

	// A force_source override on the sleep family takes the Garmin night instead.
	ov := NewOverrides(s.d)
	if _, err := ov.Create(s.ctx, By{UserID: s.user, Actor: "api_key:test"}, NewOverride{Metric: FamilySleep, Kind: catalog.WindowLocalNight,
		Key: night, LocalDate: date(night), Action: ForceSource, Group: "garmin"}); err != nil {
		t.Fatal(err)
	}
	forced := s.run(t, "sleep_deep", catalog.WindowLocalNight, night, night, nil, now)[0]
	if forced.Status != ResultOverridden || forced.Selected != "garmin" || forced.Value == nil || forced.Computed == nil {
		t.Errorf("forced: %s", resultText(forced))
	}
	golden(t, "loader-sleep-override", forced)

	// A night-window metric narrows to the main episode (E2 through the loader), and the sleep
	// drilldown lists every source of the episode.
	rhr := s.run(t, "resting_heart_rate_nocturnal", catalog.WindowLocalNight, "2025-03-13", "2025-03-13", nil, now)[0]
	if rhr.Status == ResultNoData || rhr.Window.End.Sub(rhr.Window.Start) > 12*time.Hour {
		t.Errorf("nocturnal: %s %v", resultText(rhr), rhr.Window)
	}
	golden(t, "loader-nocturnal", rhr)
	drill, err := Run(s.ctx, s.d, Request{UserID: s.user, Metric: "sleep", From: date("2025-03-13"), To: date("2025-03-13"), Now: instant(now), Sources: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(drill) != 1 || len(drill[0].Sources) == 0 || drill[0].Sources[0].Values["sessions"] == 0 {
		t.Errorf("sleep drilldown: %+v", drill)
	}

	// 13. DST: 2025-03-30 has 23 hours; the day window spans them by the wall clock.
	hours := s.run(t, "heart_rate", catalog.WindowHour, "2025-03-30", "2025-03-30", nil, now)
	day := s.run(t, "steps", catalog.WindowLocalDay, "2025-03-30", "2025-03-30", nil, now)[0]
	if len(hours) != 23 || day.Window.End.Sub(day.Window.Start) != 23*time.Hour {
		t.Errorf("13: %d hours, day of %v", len(hours), day.Window.End.Sub(day.Window.Start))
	}
	golden(t, "scenario-13-dst", hours[2], day)
}

// 7 (continued). Garmin wakes only 35 minutes on 2025-04-08: the fragments merge.
func TestScenarioSplitNightShort(t *testing.T) {
	s := loadSlice(t, "2025-04-07", 2)
	garminSleep := parseRule(t, `{"schema":"vitamux.rule/1","metric":"sleep","window":{"kind":"local_night"},
		"groups":[{"id":"garmin","match":[{"provider":"garmin"}]}],"strategy":{"op":"event_priority"}}`)
	eps := s.run(t, "sleep_total", catalog.WindowSleepEpisode, "2025-04-08", "2025-04-08", garminSleep, "2025-04-15T00:00:00Z")
	if len(eps) != 1 || len(input(eps[0], "garmin").Sessions) != 2 {
		t.Fatalf("7: %d episodes", len(eps))
	}
	golden(t, "scenario-07-split-night-short", eps...)
}

// 13 (continued). Travel: the first trip day is a New York day by its stored local dates.
func TestScenarioTravel(t *testing.T) {
	s := loadSlice(t, "2025-05-11", 3)
	days := s.run(t, "steps", catalog.WindowLocalDay, "2025-05-11", "2025-05-13", nil, "2025-06-01T00:00:00Z")
	ny, _ := time.LoadLocation("America/New_York")
	if w := days[1].Window; !w.Start.Equal(time.Date(2025, 5, 12, 0, 0, 0, 0, ny)) || days[1].Status == ResultNoData {
		t.Errorf("13: travel day %s from %v", resultText(days[1]), w.Start)
	}
	golden(t, "scenario-13-travel", days...)
}

// cachedResolve is the hook for the J09.9 property "cached = live": the cache read-through,
// which TestPropertyCachedEqualsLive checks returns exactly what a Live run does.
var cachedResolve = Run

func TestPropertyCachedEqualsLive(t *testing.T) {
	s := loadSlice(t, "2025-02-15", 3)
	for _, metric := range []string{"steps", "heart_rate", "sleep", "blood_pressure", "weight"} {
		req := Request{UserID: s.user, Metric: metric, From: date("2025-02-15"), To: date("2025-02-17"), Now: instant(sliceANow)}
		lreq := req
		lreq.Live = true
		live, err := Run(s.ctx, s.d, lreq)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 { // a miss, then a hit
			cached, err := cachedResolve(s.ctx, s.d, req)
			if err != nil {
				t.Fatal(err)
			}
			if len(cached) != len(live) {
				t.Fatalf("%s: %d cached, %d live", metric, len(cached), len(live))
			}
			for i := range live {
				if a, b := resultText(cached[i]), resultText(live[i]); a != b {
					t.Errorf("%s window %d:\n cached %s live   %s", metric, i, a, b)
				}
			}
		}
	}
}
