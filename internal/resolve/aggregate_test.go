package resolve

import (
	"errors"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

var (
	agGarmin = Source{Provider: "garmin", DeviceType: "watch"}
	agWhoop  = Source{Provider: "whoop", DeviceType: "band"}
	agWatch  = Source{Provider: "apple_health", OriginKey: "com.apple.health.watch", DeviceType: "watch"}
	agPhone  = Source{Provider: "apple_health", OriginKey: "com.apple.health.phone", DeviceType: "phone"}
	agCuff   = Source{Provider: "withings", DeviceType: "bp_monitor"}
)

var agSeq int64

func agID() int64 { agSeq++; return agSeq }

func agSample(src Source, at time.Time, v float64) Input {
	return Input{ID: agID(), Source: src, Kind: catalog.Sample, Start: at, LocalDate: midnightUTC(at), Value: v}
}

func agInterval(src Source, start, end time.Time, v float64) Input {
	return Input{ID: agID(), Source: src, Kind: catalog.Interval, Start: start, End: end, LocalDate: midnightUTC(start), Value: v}
}

func agDaily(src Source, day time.Time, v float64) Input {
	return Input{ID: agID(), Source: src, Kind: catalog.DailyValue, Start: day, LocalDate: midnightUTC(day), Value: v}
}

// agRule builds a rule without validation; groups are "<id>" -> provider-level selectors.
func agRule(metric string, kind catalog.Window, op Op, groups ...Group) *Rule {
	return &Rule{Schema: SchemaV1, Metric: metric, Window: RuleWindow{Kind: kind}, Groups: groups, Strategy: Strategy{Op: op}}
}

func agGroup(id string, sels ...Selector) Group { return Group{ID: id, Match: sels} }

func agHourWindow(start string) Window {
	s := instant(start)
	return Window{Kind: catalog.WindowHour, Start: s, End: s.Add(time.Hour), Date: midnightUTC(s), Key: s.Format(time.RFC3339)}
}

func agAggregate(t *testing.T, r *Rule, w Window, in ...Input) GroupValue {
	t.Helper()
	gv, err := r.Aggregate(w, 0, Series{r.Metric: in}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return gv
}

func agNear(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestIntensiveBucketMeans(t *testing.T) {
	r := agRule("heart_rate", catalog.WindowHour, OpFirstAvailable, agGroup("g", Selector{Provider: "garmin"}))
	w := agHourWindow("2026-06-15T10:00:00Z")
	var in []Input
	// Bucket 10:00: 50 samples at 60; bucket 10:05: one sample at 90. Equal bucket weights: 75.
	for i := range 50 {
		in = append(in, agSample(agGarmin, w.Start.Add(time.Duration(i)*time.Second), 60))
	}
	in = append(in, agSample(agGarmin, w.Start.Add(6*time.Minute), 90))
	gv := agAggregate(t, r, w, in...)
	if !agNear(gv.Value, 75) || gv.Buckets != 2 || gv.Count != 51 || gv.Basis != BasisBucketMeans || gv.Status != StatusValid {
		t.Fatalf("got %+v", gv)
	}
	if !agNear(gv.Coverage, 2.0/12) {
		t.Errorf("coverage %v, want 2/12", gv.Coverage)
	}
	// An open window counts elapsed buckets only: at 10:10 two of two buckets are covered.
	gv, _ = r.Aggregate(w, 0, Series{"heart_rate": in}, w.Start.Add(10*time.Minute))
	if gv.Coverage != 1 {
		t.Errorf("open-window coverage %v, want 1", gv.Coverage)
	}
}

// Property: duplicating the samples of a bucket N times never changes the window value.
func TestIntensiveDensityInvariance(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	r := agRule("heart_rate", catalog.WindowHour, OpFirstAvailable, agGroup("apple", Selector{Provider: "apple_health"}))
	w := agHourWindow("2026-06-15T10:00:00Z")
	for iter := range 200 {
		var in []Input
		for range 1 + rng.IntN(80) {
			src := agWatch
			if rng.IntN(3) == 0 {
				src = agPhone
			}
			at := w.Start.Add(time.Duration(rng.Int64N(int64(time.Hour))))
			in = append(in, agSample(src, at, 40+rng.Float64()*120))
		}
		base := agAggregate(t, r, w, in...)
		bucket := in[rng.IntN(len(in))].Start.Truncate(5 * time.Minute)
		n := 2 + rng.IntN(20)
		dup := append([]Input(nil), in...)
		for _, x := range in {
			if x.Start.Truncate(5 * time.Minute).Equal(bucket) {
				for range n - 1 {
					dup = append(dup, x)
				}
			}
		}
		rng.Shuffle(len(dup), func(i, j int) { dup[i], dup[j] = dup[j], dup[i] })
		got := agAggregate(t, r, w, dup...)
		if !agNear(base.Value, got.Value) || base.Coverage != got.Coverage {
			t.Fatalf("iteration %d: %v (coverage %v) became %v (%v) after duplicating bucket %v x%d",
				iter, base.Value, base.Coverage, got.Value, got.Coverage, bucket, n)
		}
	}
}

func TestIntensiveIntraGroup(t *testing.T) {
	w := agHourWindow("2026-06-15T10:00:00Z")
	in := []Input{
		agSample(agWatch, w.Start.Add(time.Minute), 60), agSample(agWatch, w.Start.Add(2*time.Minute), 62),
		agSample(agPhone, w.Start.Add(3*time.Minute), 70),
	}
	for _, tc := range []struct {
		intra IntraGroup
		want  float64
	}{{"", 65.5}, {IntraAuto, 65.5}, {IntraMean, 65.5}, {IntraMax, 70}} {
		r := agRule("heart_rate", catalog.WindowHour, OpFirstAvailable, agGroup("apple", Selector{Provider: "apple_health"}))
		r.WithinSource = &WithinSource{IntraGroup: tc.intra}
		if gv := agAggregate(t, r, w, in...); !agNear(gv.Value, tc.want) || len(gv.Sources) != 2 {
			t.Errorf("intra %q: %v from %d sources, want %v", tc.intra, gv.Value, len(gv.Sources), tc.want)
		}
	}
}

func TestAdditiveProrating(t *testing.T) {
	r := agRule("steps", catalog.WindowHour, OpFirstAvailable, agGroup("g", Selector{Provider: "garmin"}))
	iv := agInterval(agGarmin, instant("2026-06-15T10:50:00Z"), instant("2026-06-15T11:10:00Z"), 200)
	for _, tc := range []struct {
		start string
		want  float64
	}{{"2026-06-15T10:00:00Z", 100}, {"2026-06-15T11:00:00Z", 100}} {
		gv := agAggregate(t, r, agHourWindow(tc.start), iv)
		if !agNear(gv.Value, tc.want) || !gv.Prorated || gv.Basis != BasisIntervals {
			t.Errorf("%s: %+v, want %v prorated", tc.start, gv, tc.want)
		}
	}
	// A 5-minute bucket window takes a quarter of a 20-minute interval.
	s := instant("2026-06-15T11:00:00Z")
	bw := Window{Kind: catalog.WindowBucket, Start: s, End: s.Add(5 * time.Minute)}
	if gv := agAggregate(t, r, bw, iv); !agNear(gv.Value, 50) {
		t.Errorf("bucket: %v, want 50", gv.Value)
	}
	// local_day takes the whole row by its stored local_date: the day total keeps all 200.
	day, err := LocalDay(date("2026-06-15"), berlin)
	if err != nil {
		t.Fatal(err)
	}
	r.Window.Kind = catalog.WindowLocalDay
	if gv := agAggregate(t, r, day, iv); !agNear(gv.Value, 200) || gv.Prorated {
		t.Errorf("local_day: %+v, want 200 unprorated", gv)
	}
}

func TestDailyTotalNeverSummedWithIntervals(t *testing.T) {
	day, err := LocalDay(date("2026-06-15"), berlin)
	if err != nil {
		t.Fatal(err)
	}
	in := []Input{
		agDaily(agGarmin, date("2026-06-15"), 8000),
		agInterval(agGarmin, instant("2026-06-15T08:00:00Z"), instant("2026-06-15T09:00:00Z"), 3000),
		agInterval(agGarmin, instant("2026-06-15T12:00:00Z"), instant("2026-06-15T13:00:00Z"), 4000),
	}
	for _, tc := range []struct {
		policy DailyValuePolicy
		want   float64
		basis  Basis
	}{{"", 8000, BasisDailyValue}, {PreferReported, 8000, BasisDailyValue}, {IntervalsOnly, 7000, BasisIntervals}} {
		r := agRule("steps", catalog.WindowLocalDay, OpFirstAvailable, agGroup("g", Selector{Provider: "garmin"}))
		r.WithinSource = &WithinSource{DailyValuePolicy: tc.policy}
		gv := agAggregate(t, r, day, in...)
		if !agNear(gv.Value, tc.want) || gv.Basis != tc.basis {
			t.Errorf("policy %q: %v (%s), want %v (%s)", tc.policy, gv.Value, gv.Basis, tc.want, tc.basis)
		}
	}
	// Hour windows never see the daily value (Window.Includes), so only intervals count.
	r := agRule("steps", catalog.WindowHour, OpFirstAvailable, agGroup("g", Selector{Provider: "garmin"}))
	res, err := r.ResolveWindow(agHourWindow("2026-06-15T08:00:00Z"), Series{"steps": in}, Options{})
	if err != nil || !agNear(res.Value, 3000) {
		t.Errorf("hour: %v %v, want 3000", res.Value, err)
	}
	// intervals_only with nothing but a daily total is no_data with a reason.
	r = agRule("steps", catalog.WindowLocalDay, OpFirstAvailable, agGroup("g", Selector{Provider: "garmin"}))
	r.WithinSource = &WithinSource{DailyValuePolicy: IntervalsOnly}
	if gv := agAggregate(t, r, day, in[0]); gv.Status != StatusNoData || gv.Reason != ReasonOnlyDailyTotal {
		t.Errorf("intervals_only, daily only: %+v", gv)
	}
}

func TestAdditiveIntraGroup(t *testing.T) {
	w := agHourWindow("2026-06-15T10:00:00Z")
	// Watch and phone both count the same walk; auto takes the per-bucket max, not the sum.
	in := []Input{
		agInterval(agWatch, instant("2026-06-15T10:00:00Z"), instant("2026-06-15T10:10:00Z"), 1000),
		agInterval(agPhone, instant("2026-06-15T10:00:00Z"), instant("2026-06-15T10:05:00Z"), 400),
		agInterval(agPhone, instant("2026-06-15T10:05:00Z"), instant("2026-06-15T10:10:00Z"), 700),
	}
	for _, tc := range []struct {
		intra IntraGroup
		want  float64
		warn  bool
	}{{IntraAuto, 500 + 700, false}, {IntraMax, 1200, false}, {IntraMean, 450 + 600, false}, {IntraSum, 2100, true}} {
		r := agRule("steps", catalog.WindowHour, OpFirstAvailable, agGroup("apple", Selector{Provider: "apple_health"}))
		r.WithinSource = &WithinSource{IntraGroup: tc.intra}
		r.AcknowledgedWarnings = []Warning{WarnCrossSourceSum}
		gv := agAggregate(t, r, w, in...)
		if !agNear(gv.Value, tc.want) || (len(gv.Warnings) > 0) != tc.warn {
			t.Errorf("intra %s: %v warnings %v, want %v", tc.intra, gv.Value, gv.Warnings, tc.want)
		}
	}
	r := agRule("steps", catalog.WindowHour, OpFirstAvailable, agGroup("apple", Selector{Provider: "apple_health"}))
	r.WithinSource = &WithinSource{IntraGroup: IntraSum}
	if _, err := r.Aggregate(w, 0, Series{"steps": in}, time.Time{}); !errors.Is(err, ErrUnacknowledgedSum) {
		t.Errorf("unacknowledged intra_group sum: %v", err)
	}
}

func TestLatestStatistic(t *testing.T) {
	day, _ := LocalDay(date("2026-06-15"), berlin)
	in := []Input{
		agSample(agCuff, instant("2026-06-15T05:00:00Z"), 80.4),
		agSample(agCuff, instant("2026-06-15T18:00:00Z"), 81.0),
		agSample(agCuff, instant("2026-06-15T12:00:00Z"), 80.6),
	}
	r := agRule("weight", catalog.WindowLocalDay, OpFirstAvailable, agGroup("w", Selector{Provider: "withings"}))
	if gv := agAggregate(t, r, day, in...); gv.Value != 81.0 || gv.Basis != BasisLatest || gv.Count != 1 {
		t.Errorf("latest: %+v", gv)
	}
	r.WithinSource = &WithinSource{Statistic: StatMean}
	if gv := agAggregate(t, r, day, in...); !agNear(gv.Value, 80.66666666666667) || gv.Basis != BasisMean || gv.Count != 3 {
		t.Errorf("mean: %+v", gv)
	}
	// A latest window takes the most recent row at or before as_of.
	r.Window.Kind = catalog.WindowLatest
	if gv := agAggregate(t, r, LatestWindow(instant("2026-06-15T13:00:00Z")), in...); gv.Value != 80.6 {
		t.Errorf("latest window: %+v", gv)
	}
}

func TestBloodPressureReadingsStayCoherent(t *testing.T) {
	day, _ := LocalDay(date("2026-06-15"), berlin)
	morning, evening := instant("2026-06-15T06:00:00Z"), instant("2026-06-15T19:00:00Z")
	read := func(group int64, at time.Time, v float64) Input {
		x := agSample(agCuff, at, v)
		x.GroupID = group
		return x
	}
	s := Series{
		"bp_systolic":  {read(1, morning, 120), read(2, evening, 130)},
		"bp_diastolic": {read(1, morning, 80), read(2, evening, 86)},
		"bp_pulse":     {read(1, morning, 60)}, // the evening reading has no pulse
	}
	r := agRule(FamilyBloodPressure, catalog.WindowLocalDay, OpFirstAvailable, agGroup("cuff", Selector{Provider: "withings"}))
	gv, err := r.Aggregate(day, 0, s, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(gv.Components) != 2 || gv.Components["bp_systolic"] != 130 || gv.Components["bp_diastolic"] != 86 {
		t.Errorf("latest reading: %v (the morning pulse must not join the evening reading)", gv.Components)
	}
	r.WithinSource = &WithinSource{Statistic: StatMean}
	gv, _ = r.Aggregate(day, 0, s, time.Time{})
	if gv.Components["bp_systolic"] != 125 || gv.Components["bp_diastolic"] != 83 || gv.Components["bp_pulse"] != 60 {
		t.Errorf("mean of readings: %v", gv.Components)
	}
	// One reading per result on reading windows.
	r.WithinSource = nil
	ws := ReadingWindows(s["bp_systolic"])
	gv, _ = r.Aggregate(ws[0], 0, Series{"bp_systolic": s["bp_systolic"][:1], "bp_diastolic": s["bp_diastolic"][:1], "bp_pulse": s["bp_pulse"]}, time.Time{})
	if gv.Components["bp_systolic"] != 120 || gv.Components["bp_pulse"] != 60 {
		t.Errorf("reading window: %v", gv.Components)
	}
}

func TestDailySummary(t *testing.T) {
	day, _ := LocalDay(date("2026-06-15"), berlin)
	r := agRule("resting_heart_rate", catalog.WindowLocalDay, OpFirstAvailable, agGroup("g", Selector{Provider: "garmin"}))
	sample := agSample(agGarmin, instant("2026-06-15T20:00:00Z"), 55)
	daily := agDaily(agGarmin, date("2026-06-15"), 52)
	if gv := agAggregate(t, r, day, sample, daily); gv.Value != 52 || gv.Basis != BasisDailyValue {
		t.Errorf("daily value first: %+v", gv)
	}
	if gv := agAggregate(t, r, day, sample); gv.Value != 55 || gv.Basis != BasisLatest {
		t.Errorf("latest sample fallback: %+v", gv)
	}
}

func TestAggregatePendingParts(t *testing.T) {
	day, _ := LocalDay(date("2026-06-15"), berlin)
	r := agRule(FamilySleep, catalog.WindowLocalNight, OpEventPriority, agGroup("g", Selector{Provider: "garmin"}))
	if _, err := r.Aggregate(day, 0, Series{}, time.Time{}); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("sleep family: %v", err)
	}
}
