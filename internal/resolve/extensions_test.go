package resolve

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// Scenario data shaped like tools/fixturegen output: one person in Europe/Berlin, devices
// identified by their device row, synthetic values only.
var (
	exWatch  = Source{Provider: "apple_health", OriginKey: "com.apple.health.synthetic-watch", DeviceType: "watch", DeviceID: uuid.MustParse("00000000-0000-4000-8000-000000000001")}
	exPhone  = Source{Provider: "apple_health", OriginKey: "com.apple.health.synthetic-phone", DeviceType: "phone", DeviceID: uuid.MustParse("00000000-0000-4000-8000-000000000002")}
	exRing   = Source{Provider: "oura", DeviceType: "ring", DeviceID: uuid.MustParse("00000000-0000-4000-8000-000000000003")}
	exStrap  = Source{Provider: "apple_health", OriginKey: "com.apple.health.synthetic-watch", DeviceType: "chest_strap", DeviceID: uuid.MustParse("00000000-0000-4000-8000-000000000004")}
	exGarmin = Source{Provider: "garmin", DeviceType: "watch", DeviceID: uuid.MustParse("00000000-0000-4000-8000-000000000005")}
)

var exLoc = func() *time.Location {
	l, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		panic(err)
	}
	return l
}()

// exT is a local Berlin time on a date; "24:00" is the next midnight.
func exT(day, hhmm string) time.Time {
	d := date(day)
	h, _ := strconv.Atoi(hhmm[:2])
	m, _ := strconv.Atoi(hhmm[3:])
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, exLoc).Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
}

func exLocalDate(t time.Time) time.Time { l := t.In(exLoc); return midnightUTC(l) }

func exSample(src Source, at time.Time, v float64) Input {
	return Input{ID: agID(), Source: src, Kind: catalog.Sample, Start: at, LocalDate: exLocalDate(at), Value: v}
}

func exInterval(src Source, start time.Time, d time.Duration, v float64) Input {
	return Input{ID: agID(), Source: src, Kind: catalog.Interval, Start: start, End: start.Add(d), LocalDate: exLocalDate(start), Value: v}
}

func exDaily(src Source, day string, v float64) Input {
	return Input{ID: agID(), Source: src, Kind: catalog.DailyValue, Start: exT(day, "00:00"), LocalDate: date(day), Value: v}
}

// exEvery calls f for each step from start (inclusive) to end (exclusive).
func exEvery(start, end time.Time, step time.Duration, f func(time.Time)) {
	for t := start; t.Before(end); t = t.Add(step) {
		f(t)
	}
}

func exRule(t *testing.T, file string) *Rule {
	t.Helper()
	data, err := os.ReadFile("testdata/valid/" + file)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseRule(data)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// exSummary is the explanation snapshot of a result: the plain fields J09.8 renders.
func exSummary(res WindowResult) string {
	num := func(v float64) string { return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64) }
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", res.Status, num(res.Value))
	for _, kv := range [][2]string{{"sel", res.Selected}, {"ctx", string(res.Context)}, {"ws", res.WorkoutGroup}, {"follow", res.FollowGroup}} {
		if kv[1] != "" {
			fmt.Fprintf(&b, " %s=%s", kv[0], kv[1])
		}
	}
	b.WriteString(" |")
	for _, g := range res.Groups {
		if g.ID == "" {
			continue
		}
		fmt.Fprintf(&b, " %s:%s", g.ID, g.Status)
		if g.Reason != "" {
			b.WriteString(":" + g.Reason)
		}
	}
	for _, w := range res.Warnings {
		b.WriteString(" !" + string(w.Code))
		if w.Group != "" {
			b.WriteString("(" + w.Group + ")")
		}
	}
	return b.String()
}

func exGroup(res WindowResult, id string) GroupValue {
	for _, g := range res.Groups {
		if g.ID == id {
			return g
		}
	}
	return GroupValue{}
}

// exByStart resolves the windows and indexes the results by local "HH:MM" start.
func exByStart(t *testing.T, r *Rule, ws []Window, s Series, opt Options) map[string]WindowResult {
	t.Helper()
	res, err := r.ResolveWindows(ws, s, opt)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]WindowResult{}
	for _, x := range res {
		out[x.Window.Start.In(exLoc).Format("15:04")] = x
	}
	return out
}

func exCheck(t *testing.T, name string, got WindowResult, want string) {
	t.Helper()
	if s := exSummary(got); s != want {
		t.Errorf("%s:\n got  %s\n want %s", name, s, want)
	}
}

// E1: a chest strap overrides the watch inside a workout only, and @workout_source puts the
// recording device ahead of the default ladder where the strap has a gap.
func TestScenarioChestStrapInWorkout(t *testing.T) {
	const day = "2026-06-15"
	r := exRule(t, "e1-workout-hr-contexts.json")
	var hr []Input
	exEvery(exT(day, "09:55"), exT(day, "11:30"), time.Minute, func(m time.Time) { hr = append(hr, exSample(exWatch, m, 100)) })
	exEvery(exT(day, "10:00"), exT(day, "11:20"), time.Minute, func(m time.Time) {
		if m.Before(exT(day, "10:30")) || !m.Before(exT(day, "10:40")) { // the strap drops out 10:30-10:40
			hr = append(hr, exSample(exStrap, m, 130))
		}
	})
	exEvery(exT(day, "10:00"), exT(day, "11:00"), time.Minute, func(m time.Time) { hr = append(hr, exSample(exGarmin, m, 125)) })
	run := WorkoutInput{ID: uuid.New(), Source: exGarmin, Start: exT(day, "10:00"), End: exT(day, "11:00"), Sport: "running"}

	buckets, err := Buckets(date(day), 5*time.Minute, berlin)
	if err != nil {
		t.Fatal(err)
	}
	got := exByStart(t, r, buckets, Series{"heart_rate": hr}, Options{Events: ContextEvents{Workouts: ClusterWorkouts([]WorkoutInput{run})}})
	exCheck(t, "10:00 in workout", got["10:00"],
		"direct 130 sel=chest_strap ctx=workout ws=garmin | chest_strap:selected garmin:fallback_unused apple_watch:fallback_unused ring:no_data:no_inputs")
	exCheck(t, "10:30 strap gap", got["10:30"],
		"fallback 125 sel=garmin ctx=workout ws=garmin | chest_strap:no_data:no_inputs garmin:selected apple_watch:fallback_unused ring:no_data:no_inputs !preferred_source_unavailable(chest_strap)")
	exCheck(t, "10:55 last workout bucket", got["10:55"],
		"direct 130 sel=chest_strap ctx=workout ws=garmin | chest_strap:selected garmin:fallback_unused apple_watch:fallback_unused ring:no_data:no_inputs")
	exCheck(t, "11:05 after the workout", got["11:05"],
		"direct 100 sel=apple_watch | apple_watch:selected garmin:no_data:no_inputs chest_strap:fallback_unused ring:no_data:no_inputs")
	// Contexts never move rows: the strap's rows stay in chest_strap everywhere.
	if g := exGroup(got["11:05"], "chest_strap"); g.Count != 5 || g.Value != 130 {
		t.Errorf("strap outside the workout: %+v", g)
	}
}

// exNight aligns the night of 2026-06-15: ring 23:00-07:00 and watch 23:10-06:50.
func exNight(t *testing.T) SleepAlignment {
	t.Helper()
	sr := &Rule{Schema: SchemaV1, Metric: FamilySleep, Window: RuleWindow{Kind: catalog.WindowLocalNight}, Strategy: Strategy{Op: OpEventPriority},
		Groups: []Group{{ID: "ring", Match: []Selector{{DeviceType: "ring"}}}, {ID: "watch", Match: []Selector{{DeviceType: "watch"}}}}}
	in := []SleepInput{
		{ID: uuid.New(), Source: exRing, Start: exT("2026-06-14", "23:00"), End: exT("2026-06-15", "07:00")},
		{ID: uuid.New(), Source: exWatch, Start: exT("2026-06-14", "23:10"), End: exT("2026-06-15", "06:50")},
	}
	a, err := sr.AlignSleep(date("2026-06-15"), in, berlin)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := a.Main(); !ok || !e.Start.Equal(in[0].Start) || !e.End.Equal(in[0].End) {
		t.Fatalf("main episode: %+v", a.Episodes)
	}
	return a
}

// E1: the ring leads heart rate inside the aligned sleep episode only.
func TestScenarioRingLeadsInSleep(t *testing.T) {
	const day = "2026-06-15"
	r := exRule(t, "e1-workout-hr-contexts.json")
	var hr []Input
	exEvery(exT(day, "06:00"), exT(day, "08:00"), 5*time.Minute, func(b time.Time) { hr = append(hr, exSample(exRing, b.Add(2*time.Minute), 55)) })
	exEvery(exT(day, "06:00"), exT(day, "08:00"), time.Minute, func(m time.Time) { hr = append(hr, exSample(exWatch, m, 60)) })
	buckets, _ := Buckets(date(day), 5*time.Minute, berlin)
	got := exByStart(t, r, buckets, Series{"heart_rate": hr}, Options{Events: ContextEvents{Sleep: exNight(t).Episodes}})
	exCheck(t, "06:55 in sleep", got["06:55"],
		"direct 55 sel=ring ctx=sleep | ring:selected apple_watch:fallback_unused garmin:no_data:no_inputs chest_strap:no_data:no_inputs")
	exCheck(t, "07:00 awake", got["07:00"],
		"direct 60 sel=apple_watch | apple_watch:selected garmin:no_data:no_inputs chest_strap:no_data:no_inputs ring:fallback_unused")
}

func exNocturnalRule() *Rule {
	return &Rule{Schema: SchemaV1, Metric: "resting_heart_rate_nocturnal", Window: RuleWindow{Kind: catalog.WindowLocalNight},
		Groups:       []Group{{ID: "apple_watch", Match: []Selector{{DeviceType: "watch"}}}, {ID: "ring", Match: []Selector{{DeviceType: "ring"}}}},
		WithinSource: &WithinSource{Statistic: StatMinRollingMean, Span: "30m"},
		Strategy:     Strategy{Op: OpFirstAvailable}, Quality: &Quality{MinCoverage: new(0.7)},
		Contexts: map[Context][]string{ContextSleep: {"ring"}}}
}

// E2: nocturnal resting HR is the lowest 30-minute mean of the main episode, read from the
// heart_rate series; the ring leads through the sleep context.
func TestScenarioNocturnalRestingHR(t *testing.T) {
	r := exNocturnalRule()
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	ep, _ := exNight(t).Main()
	night, err := LocalNight(date("2026-06-15"), DefaultNightAnchor, berlin)
	if err != nil {
		t.Fatal(err)
	}
	w := night.WithEpisode(ep)
	var ring, watch []Input
	k := 0
	exEvery(ep.Start, ep.End, 5*time.Minute, func(b time.Time) {
		v := 58.0
		switch l := b.In(exLoc).Format("15:04"); {
		case l >= "03:00" && l < "03:30":
			v = 49
		case l == "04:00": // one low bucket: a plain min would take it, a 30-min mean does not
			v = 45
		}
		ring = append(ring, exSample(exRing, b.Add(time.Minute), v))
		if k%6 != 5 { // the watch misses every sixth bucket: 83 % coverage, but no full 30 minutes
			watch = append(watch, exSample(exWatch, b.Add(time.Minute), 52))
		}
		k++
	})
	res, err := r.ResolveWindow(w, Series{"heart_rate": append(ring, watch...)}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	exCheck(t, "ring and gappy watch", res, "direct 49 sel=ring ctx=sleep | ring:selected apple_watch:below_quality:no_full_span")
	g := exGroup(res, "ring")
	if g.Basis != BasisMinRollingMean || g.Coverage != 1 || !g.SpanStart.Equal(exT("2026-06-15", "03:00")) || !g.SpanEnd.Equal(exT("2026-06-15", "03:30")) {
		t.Errorf("ring span: %+v", g)
	}

	// A dense watch that covers only 03:00-05:00 has full spans but fails min_coverage 0.7.
	var short []Input
	exEvery(exT("2026-06-15", "03:00"), exT("2026-06-15", "05:00"), 5*time.Minute, func(b time.Time) { short = append(short, exSample(exWatch, b, 50)) })
	res, _ = r.ResolveWindow(w, Series{"heart_rate": short}, Options{})
	exCheck(t, "short watch only", res, "no_data 0 ctx=sleep | ring:no_data:no_inputs apple_watch:below_quality:coverage")

	// A derived code needs its statistic, at resolve time and on save.
	r.WithinSource = nil
	if _, err := r.ResolveWindow(w, Series{}, Options{}); err == nil {
		t.Error("derived code without a statistic resolved")
	}
	if err := Validate(r); err == nil || !strings.Contains(err.Error(), "/within_source/statistic") {
		t.Errorf("validate: %v", err)
	}
}

// E2: spo2_night_min (built-in) is the lowest 5-minute SpO2 mean of the episode.
func TestScenarioSpO2NightMin(t *testing.T) {
	b, ok := LookupBuiltin("spo2_night_min")
	if !ok {
		t.Fatal("no built-in for spo2_night_min")
	}
	ep, _ := exNight(t).Main()
	night, _ := LocalNight(date("2026-06-15"), DefaultNightAnchor, berlin)
	var in []Input
	exEvery(ep.Start, ep.End, 5*time.Minute, func(t time.Time) { in = append(in, exSample(exWatch, t.Add(time.Minute), 95)) })
	in = append(in, exSample(exWatch, exT("2026-06-15", "02:11"), 88), exSample(exWatch, exT("2026-06-15", "02:12"), 90))
	res, err := b.Rule.ResolveWindow(night.WithEpisode(ep), Series{"spo2": in}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	g := exGroup(res, "apple")
	if res.Status != ResultDirect || !agNear(res.Value, 91) || g.Basis != BasisMin || !g.SpanStart.Equal(exT("2026-06-15", "02:10")) {
		t.Errorf("spo2 night min: %s %+v", exSummary(res), g)
	}
}

// E3 + E9: hourly steps from watch, ring and phone. The watch is off 12:00-15:00 (one stray
// interval while on the table), worn only 20 minutes of 17:00, and worn without steps in the
// evening; the ring counts 80 % of the watch and is off from 20:00; the phone never reports
// heart rate, so it is exempt from the wear gate. Watch and ring report steps (J24.3), so their
// worn hours without steps stay a measured 0 rather than falling through.
func TestScenarioHourlyStepsWithWear(t *testing.T) {
	const day = "2026-06-15"
	r := exRule(t, "e9-steps-compose.json")
	at := func(hhmm string) time.Time { return exT(day, hhmm) }
	watchOff := func(b time.Time) bool {
		return !b.Before(at("12:00")) && b.Before(at("15:00")) || !b.Before(at("17:00")) && b.Before(at("17:40"))
	}
	var hr, steps []Input
	exEvery(at("07:00"), at("23:00"), 5*time.Minute, func(b time.Time) {
		if !watchOff(b) {
			hr = append(hr, exSample(exWatch, b.Add(2*time.Minute), 70))
		}
	})
	exEvery(at("00:00"), at("20:00"), 5*time.Minute, func(b time.Time) { hr = append(hr, exSample(exRing, b.Add(3*time.Minute), 65)) })
	exEvery(at("08:00"), at("20:00"), 5*time.Minute, func(b time.Time) {
		if !watchOff(b) {
			steps = append(steps, exInterval(exWatch, b, 5*time.Minute, 50))
		}
		steps = append(steps, exInterval(exRing, b, 5*time.Minute, 40))
	})
	steps = append(steps, exInterval(exWatch, at("13:00"), 5*time.Minute, 7)) // bumped on the table
	exEvery(at("10:00"), at("18:00"), time.Hour, func(h time.Time) { steps = append(steps, exInterval(exPhone, h, time.Hour, 500)) })
	steps = append(steps, exInterval(exPhone, at("20:00"), time.Hour, 200))

	w, err := LocalDay(date(day), berlin)
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.ResolveWindow(w, Series{"steps": steps, "heart_rate": hr}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hours) != 24 {
		t.Fatalf("%d hours", len(res.Hours))
	}
	for h, want := range map[int]string{
		3:  "fallback 0 sel=ring | watch:no_data:no_inputs ring:selected phone:no_data:no_inputs !preferred_source_unavailable(watch)",
		7:  "direct 0 sel=watch | watch:selected ring:fallback_unused phone:no_data:no_inputs",
		9:  "direct 600 sel=watch | watch:selected ring:fallback_unused phone:no_data:no_inputs",
		13: "fallback 480 sel=ring | watch:below_quality:not_worn ring:selected phone:fallback_unused !preferred_source_unavailable(watch)",
		17: "fallback 480 sel=ring | watch:below_quality:coverage ring:selected phone:fallback_unused !preferred_source_unavailable(watch)",
		20: "direct 0 sel=watch | watch:selected ring:no_data:no_inputs phone:fallback_unused",
		23: "no_data 0 | watch:no_data:no_inputs ring:no_data:no_inputs phone:no_data:no_inputs",
	} {
		exCheck(t, fmt.Sprintf("hour %02d", h), res.Hours[h], want)
	}
	if g := exGroup(res.Hours[13], "watch"); g.Gated != 1 || g.Count != 0 {
		t.Errorf("hour 13 watch: %+v", g)
	}
	if g := exGroup(res.Hours[17], "watch"); g.WornBuckets != 4 || !agNear(g.Coverage, 4.0/12) || g.Value != 200 {
		t.Errorf("hour 17 watch: %+v", g)
	}
	// 2400 + 1440 (ring) + 1200 + 480 (ring) + 1200; the ring alone has 5760, the watch 5000.
	exCheck(t, "day", res,
		"calculated 6720 | watch:selected ring:selected phone:fallback_unused !preferred_source_unavailable(watch) !composite_exceeds_any_source")
	if w, rg, ph := exGroup(res, "watch"), exGroup(res, "ring"), exGroup(res, "phone"); w.Value != 5000 || rg.Value != 5760 || ph.Value != 4200 ||
		!ph.WearExempt || w.WearExempt || w.WornBuckets != 148 {
		t.Errorf("day groups: watch %+v ring %+v phone %+v", w, rg, ph)
	}

	// Without any watch heart rate in the series the watch counts as never reporting wear:
	// exempt, so the stray interval is taken at face value.
	var ringHR []Input
	for _, x := range hr {
		if x.Source == exRing {
			ringHR = append(ringHR, x)
		}
	}
	res, _ = r.ResolveWindow(w, Series{"steps": steps, "heart_rate": ringHR}, Options{})
	exCheck(t, "exempt watch hour 13", res.Hours[13], "direct 7 sel=watch | watch:selected ring:fallback_unused phone:fallback_unused")
}

// J24.3: a band worn all day that reports heart rate but no steps (or only a daily total) never
// stands for steps with a measured 0, and its daily total is never added to hours.
func TestScenarioNoPhantomValues(t *testing.T) {
	const day = "2026-06-15"
	r := exRule(t, "e9-steps-compose.json")
	r.Groups[1] = Group{ID: "band", Match: []Selector{{DeviceType: "band"}}}
	band := Source{Provider: "whoop", DeviceType: "band", DeviceID: uuid.MustParse("00000000-0000-4000-8000-000000000006")}
	at := func(hhmm string) time.Time { return exT(day, hhmm) }
	var hr, phone []Input
	exEvery(at("00:00"), at("24:00"), 5*time.Minute, func(b time.Time) { hr = append(hr, exSample(band, b.Add(time.Minute), 60)) })
	exEvery(at("10:00"), at("18:00"), time.Hour, func(h time.Time) { phone = append(phone, exInterval(exPhone, h, time.Hour, 500)) })
	w, err := LocalDay(date(day), berlin)
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(s Series) WindowResult {
		t.Helper()
		res, err := r.ResolveWindow(w, s, Options{})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	explanation := func(res WindowResult) string {
		return BuildResult("steps", testVersion(r), Resolved{WindowResult: res}, time.Time{}).Explanation
	}

	// Worn band, no steps: the band is no_data (not_reported), so the phone's hours make the day.
	res := resolve(Series{"steps": phone, "heart_rate": hr})
	exCheck(t, "silent band hour 03", res.Hours[3], "no_data 0 | watch:no_data:no_inputs band:no_data:not_reported phone:no_data:no_inputs")
	exCheck(t, "silent band day", res,
		"fallback 4000 sel=phone | watch:no_data:no_inputs band:no_data:not_reported phone:selected !preferred_source_unavailable(watch) !preferred_source_unavailable(band)")
	if x := explanation(res); !strings.Contains(x, "band does not report steps") {
		t.Errorf("silent band explanation: %s", x)
	}
	// Steps from the band within the lookback (Series[Reporting]) make its worn hours a measured 0.
	res = resolve(Series{"steps": phone, "heart_rate": hr, Reporting: {{Source: band, Start: at("00:00").AddDate(0, 0, -10)}}})
	exCheck(t, "reporting band hour 03", res.Hours[3], "fallback 0 sel=band | watch:no_data:no_inputs band:selected phone:no_data:no_inputs !preferred_source_unavailable(watch)")

	// Daily-only band above the phone: the day is the band's daily total, not hours of zeros.
	daily := append([]Input{exDaily(band, day, 9000)}, phone...)
	res = resolve(Series{"steps": daily, "heart_rate": hr})
	exCheck(t, "daily band day", res,
		"fallback 9000 sel=band | watch:no_data:no_inputs band:selected phone:fallback_unused !preferred_source_unavailable(watch)")
	if g := exGroup(res, "band"); g.Basis != BasisDailyValue || len(res.Hours) != 0 {
		t.Errorf("daily band: %+v, %d hours", g, len(res.Hours))
	}

	// Watch until noon (steps 08-12) above the daily-only band: the watch's hours, then the
	// phone's; the band's total is never added.
	var watch []Input
	exEvery(at("00:00"), at("12:00"), 5*time.Minute, func(b time.Time) {
		hr = append(hr, exSample(exWatch, b.Add(2*time.Minute), 70))
		if !b.Before(at("08:00")) {
			watch = append(watch, exInterval(exWatch, b, 5*time.Minute, 50))
		}
	})
	res = resolve(Series{"steps": append(watch, daily...), "heart_rate": hr})
	exCheck(t, "half day hour 03", res.Hours[3], "direct 0 sel=watch | watch:selected band:no_data:no_inputs phone:no_data:no_inputs")
	exCheck(t, "half day hour 12", res.Hours[12],
		"fallback 500 sel=phone | watch:no_data:no_inputs band:no_data:no_inputs phone:selected !preferred_source_unavailable(watch) !preferred_source_unavailable(band)")
	// 2400 (watch 08-12) + 3000 (phone 12-18).
	exCheck(t, "half day", res, "calculated 5400 | watch:selected band:no_data:only_daily_total phone:selected !preferred_source_unavailable(watch) !preferred_source_unavailable(band) !composite_exceeds_any_source")
	if x := explanation(res); !strings.Contains(x, "band sent only a daily total, which cannot fill hours") {
		t.Errorf("half day explanation: %s", x)
	}
}

// E9 with op max: each hour takes the largest group and names it.
func TestComposeMax(t *testing.T) {
	const day = "2026-06-15"
	r := exRule(t, "e9-steps-compose.json")
	r.Quality, r.Compose.Op = nil, ComposeMax
	steps := []Input{
		exInterval(exWatch, exT(day, "09:00"), time.Hour, 600), exInterval(exPhone, exT(day, "09:00"), time.Hour, 700),
		exInterval(exWatch, exT(day, "10:00"), time.Hour, 800), exInterval(exPhone, exT(day, "10:00"), time.Hour, 300),
	}
	w, _ := LocalDay(date(day), berlin)
	res, err := r.ResolveWindow(w, Series{"steps": steps}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	exCheck(t, "hour 09", res.Hours[9], "calculated 700 sel=phone | watch:fallback_unused ring:no_data:no_inputs phone:selected")
	exCheck(t, "day", res, "calculated 1500 | watch:selected ring:no_data:no_inputs phone:selected !composite_exceeds_any_source")
}

// E5: basal energy follows active energy's source, with a fallback day. basal_energy enters
// the catalogue with E15/J08.6, so distance_walk_run (also additive, daily values) stands in
// as the follower; the mechanics do not depend on the code.
func TestScenarioFollowLeader(t *testing.T) {
	groups := func(ids ...string) []Group {
		var out []Group
		for _, id := range ids {
			out = append(out, Group{ID: id, Match: []Selector{{DeviceType: id}}})
		}
		return out
	}
	day := RuleWindow{Kind: catalog.WindowLocalDay}
	leader := &Rule{Schema: SchemaV1, Metric: "active_energy", Window: day, Strategy: Strategy{Op: OpFirstAvailable}, Groups: groups("watch", "ring", "phone")}
	follower := &Rule{Schema: SchemaV1, Metric: "distance_walk_run", Window: day, Strategy: Strategy{Op: OpFirstAvailable},
		Groups: groups("ring", "watch", "phone"), Follow: "active_energy"}
	if err := ValidateSet([]*Rule{leader, follower}); err != nil {
		t.Fatal(err)
	}
	days := []string{"2026-06-15", "2026-06-16", "2026-06-17"}
	var ws []Window
	for _, d := range days {
		w, _ := LocalDay(date(d), berlin)
		ws = append(ws, w)
	}
	active := Series{"active_energy": {exDaily(exWatch, days[0], 500), exDaily(exRing, days[0], 450), exDaily(exWatch, days[1], 520)}}
	basal := Series{"distance_walk_run": {exDaily(exRing, days[0], 1500), exDaily(exWatch, days[0], 1600),
		exDaily(exRing, days[1], 1550), exDaily(exRing, days[2], 1580)}}
	lead, err := leader.ResolveWindows(ws, active, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := follower.ResolveWindows(ws, basal, Options{Leader: LeaderSelections(lead)})
	if err != nil {
		t.Fatal(err)
	}
	exCheck(t, "day 1", got[0], "direct 1600 sel=watch follow=watch | ring:fallback_unused watch:selected phone:no_data:no_inputs")
	exCheck(t, "day 2", got[1], "fallback 1550 sel=ring follow=watch | ring:selected watch:no_data:no_inputs phone:no_data:no_inputs !follow_unavailable(watch)")
	exCheck(t, "day 3", got[2], "fallback 1580 sel=ring | ring:selected watch:no_data:no_inputs phone:no_data:no_inputs !follow_unavailable")
}

func TestContextAt(t *testing.T) {
	r := exRule(t, "e1-workout-hr-contexts.json")
	run := ClusterWorkouts([]WorkoutInput{{ID: uuid.New(), Source: exGarmin, Start: instant("2026-06-15T10:00:00Z"), End: instant("2026-06-15T10:20:00Z"), Sport: "running"}})
	ev := ContextEvents{Workouts: run}
	for _, tc := range []struct {
		start string
		want  Context
	}{
		{"2026-06-15T09:40:00Z", ""},             // 10 of 30 minutes in the workout
		{"2026-06-15T09:45:00Z", ContextWorkout}, // 15 of 30: half counts
		{"2026-06-15T10:00:00Z", ContextWorkout},
	} {
		s := instant(tc.start)
		ctx, g := r.ContextAt(Window{Kind: catalog.WindowBucket, Start: s, End: s.Add(30 * time.Minute)}, ev)
		if ctx != tc.want || (ctx == ContextWorkout && g != 1) {
			t.Errorf("%s: %q %d", tc.start, ctx, g)
		}
	}
	if ctx, _ := r.ContextAt(Window{Kind: catalog.WindowLocalNight}, ContextEvents{}); ctx != ContextSleep {
		t.Errorf("local_night: %q", ctx)
	}
	// An explicit context overrides the events; @workout_source is then unknown and skipped.
	w := Window{Kind: catalog.WindowBucket, Start: instant("2026-06-15T12:00:00Z"), End: instant("2026-06-15T12:05:00Z")}
	gvs := make([]GroupValue, len(r.Groups))
	for i, g := range r.Groups {
		gvs[i] = GroupValue{Group: i, ID: g.ID, Status: StatusNoData}
	}
	res, err := r.Select(w, gvs, Options{Context: ContextWorkout})
	if err != nil || res.Context != ContextWorkout || res.WorkoutGroup != "" || res.Groups[0].ID != "chest_strap" {
		t.Errorf("explicit context: %v %+v", err, res)
	}
}
