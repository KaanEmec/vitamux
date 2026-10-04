package catalog

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestMetricsWellFormed(t *testing.T) {
	codeRE := regexp.MustCompile(`^[a-z][a-z0-9_]*$`) // the metric_catalog CHECK
	seen := map[string]bool{}
	for _, m := range metrics {
		if !codeRE.MatchString(m.Code) || seen[m.Code] {
			t.Errorf("%s: bad or duplicate code", m.Code)
		}
		seen[m.Code] = true
		if _, ok := LookupUnit(m.Unit); !ok {
			t.Errorf("%s: unknown unit %q", m.Code, m.Unit)
		}
		if !(m.Min < m.Max) {
			t.Errorf("%s: plausible range %v..%v", m.Code, m.Min, m.Max)
		}
		if !slices.Contains(sections, m.Section) || len(m.Windows()) == 0 || len(m.Strategies()) == 0 {
			t.Errorf("%s: missing section, windows or strategies", m.Code)
		}
		switch m.Agg {
		case SleepDerived:
			if len(m.Kinds) != 0 {
				t.Errorf("%s: derived from sleep sessions, so it has no measurement kinds", m.Code)
			}
		case Additive:
			if !slices.Contains(m.Kinds, Interval) {
				t.Errorf("%s: additive metrics need interval rows", m.Code)
			}
		case DailySummary:
			if !slices.Contains(m.Kinds, DailyValue) && !slices.Contains(m.Kinds, Sample) {
				t.Errorf("%s: daily_summary needs daily values or samples", m.Code)
			}
		case Intensive, Latest:
		}
		if m.Agg != SleepDerived && m.DerivedFrom == "" && len(m.Kinds) == 0 {
			t.Errorf("%s: no kinds", m.Code)
		}
		if m.DerivedFrom != "" {
			src, ok := Lookup(m.DerivedFrom)
			if !ok || src.DerivedFrom != "" || src.Unit != m.Unit || src.Agg != m.Agg || len(m.Kinds) != 0 {
				t.Errorf("%s: a derived code needs a stored source metric with its unit and aggregation, and no kinds", m.Code)
			}
		}
		if m.Group != "" && m.Group != groupBP && m.Group != groupBody {
			t.Errorf("%s: group %q is not a measurement_groups.kind", m.Code, m.Group)
		}
	}
}

func TestSDNNAndRMSSDAreDistinct(t *testing.T) {
	sdnn, ok1 := Lookup("hrv_sdnn")
	rmssd, ok2 := Lookup("hrv_rmssd")
	if !ok1 || !ok2 || sdnn.Code == rmssd.Code {
		t.Fatal("hrv_sdnn and hrv_rmssd must both exist as separate codes")
	}
	for _, p := range [][2]string{{"hrv_sdnn", "hrv_rmssd"}, {"hrv_rmssd", "hrv_rmssd_nightly"}, {"hrv_sdnn", "hrv_rmssd_nightly"}} {
		if Combinable(p[0], p[1]) || Combinable(p[1], p[0]) {
			t.Errorf("%s and %s must not share a rule", p[0], p[1])
		}
	}
	if !Combinable("hrv_sdnn", "hrv_sdnn") || Combinable("nope", "nope") {
		t.Error("a known code combines with itself; an unknown code with nothing")
	}
}

func TestStrategiesAndWindows(t *testing.T) {
	steps, _ := Lookup("steps")
	hr, _ := Lookup("heart_rate")
	bp, _ := Lookup("bp_systolic")
	sleep, _ := Lookup("sleep_deep")
	score := Metric{Code: "oura_readiness", Agg: DailySummary, ProviderScoped: true}

	if !steps.AllowsStrategy(Sum) || hr.AllowsStrategy(Sum) || bp.AllowsStrategy(Sum) {
		t.Error("sum is for additive metrics only")
	}
	if !hr.AllowsStrategy(Mean) || !hr.AllowsStrategy(FirstAvailable) || hr.AllowsStrategy(EventPriority) {
		t.Errorf("heart_rate strategies: %v", hr.Strategies())
	}
	if !sleep.AllowsStrategy(EventPriority) || !sleep.AllowsWindow(WindowLocalNight) || sleep.AllowsWindow(WindowHour) {
		t.Error("sleep metrics use night and episode windows")
	}
	if score.Poolable() || score.AllowsStrategy(Mean) || score.AllowsStrategy(Min) || score.AllowsStrategy(Max) || !score.AllowsStrategy(FirstAvailable) {
		t.Errorf("provider-scoped strategies: %v", score.Strategies())
	}
	for _, code := range []string{"resting_heart_rate", "hrv_rmssd_nightly"} {
		if m, _ := Lookup(code); m.Poolable() || m.AllowsStrategy(Mean) || !m.AllowsStrategy(FirstAvailable) {
			t.Errorf("%s is selection-only: %v", code, m.Strategies())
		}
	}
	if !bp.AllowsWindow(WindowReading) || hr.AllowsWindow(WindowReading) {
		t.Error("only group metrics have the reading window")
	}
	if hr.BaseBucket().Minutes() != 5 || bp.BaseBucket() != 0 {
		t.Error("base bucket: 5 minutes for intensive, none for latest")
	}
}

// TestIntraday checks the J22.26 ladder: every stored intensive and additive code has a day
// view, dense series start at 1 minute, and codes measured once a day or night have none.
func TestIntraday(t *testing.T) {
	for _, code := range dense {
		if m, ok := Lookup(code); !ok || m.Agg != Intensive {
			t.Errorf("dense code %s is not an intensive catalogue code", code)
		}
	}
	for _, m := range metrics {
		in, ok := m.Intraday()
		stored := (m.Agg == Intensive || m.Agg == Additive) && m.DerivedFrom == ""
		switch {
		case !stored || strings.HasSuffix(m.Code, "_nightly"):
			if ok {
				t.Errorf("%s (%s): measured once a day or night, so no intraday view: %v", m.Code, m.Agg, in)
			}
		case m.Agg == Additive && in != Intraday{"30m", "1m"}:
			t.Errorf("%s: additive ladder %v, want 30m → 1m", m.Code, in)
		case m.Agg == Intensive && (in.Finest != "raw" || in.Default != "1m" && in.Default != "5m"):
			t.Errorf("%s: intensive ladder %v, want 1m or 5m → raw", m.Code, in)
		}
	}
	want := map[string]Intraday{
		"heart_rate": {"1m", "raw"}, "spo2": {"5m", "raw"}, "respiratory_rate": {"5m", "raw"},
		"hrv_rmssd": {"5m", "raw"}, "garmin_stress": {"5m", "raw"}, "garmin_body_battery": {"5m", "raw"},
		"skin_temperature": {"5m", "raw"}, "walking_step_length": {"5m", "raw"}, "steps": {"30m", "1m"},
		"resting_heart_rate": {}, "weight": {}, "sleep_deep": {}, "spo2_nightly": {}, "spo2_night_min": {},
	}
	for code, w := range want {
		m, _ := Lookup(code)
		if in, ok := m.Intraday(); in != w || ok != (w != Intraday{}) {
			t.Errorf("%s: intraday %v, want %v", code, in, w)
		}
	}
}

// wantBase lists, for every unit that is not its own base, one value converted to the base unit.
// TestEveryConversionIsTested fails when a unit is added without an entry here.
var wantBase = map[string]struct{ in, want float64 }{
	"ms":            {1500, 1.5},
	"min":           {2, 120},
	"h":             {1.5, 5400},
	"km":            {2.5, 2500},
	"cm":            {172, 1.72},
	"mi":            {1, 1609.344},
	"ft":            {6, 1.8288},
	"in":            {10, 0.254},
	"kJ":            {4.184, 1},
	"g":             {2500, 2.5},
	"lb":            {154.3235835, 70},
	"oz":            {16, 0.45359237},
	"st":            {11, 69.85323498},
	"km/h":          {36, 10},
	"mph":           {10, 4.4704},
	"kPa":           {13.3322387415, 100},
	"fraction":      {0.185, 18.5},
	"°F":            {98.6, 37},
	"mg/dL glucose": {90, 4.99567},
	"mg":            {2500, 0.0025},
	"mL":            {250, 0.25},
	"µmol/L":        {12, 0.012},
}

func close(a, b float64) bool { return math.Abs(a-b) <= 1e-6*math.Max(1, math.Abs(b)) }

func TestEveryConversionIsTested(t *testing.T) {
	for _, u := range units {
		if u.Factor == 1 && u.Offset == 0 {
			if u.Base != u.Code {
				t.Errorf("%s: identity conversion to another base", u.Code)
			}
			continue
		}
		tc, ok := wantBase[u.Code]
		if !ok {
			t.Errorf("%s has a non-identity conversion and no test case", u.Code)
			continue
		}
		got, err := Convert(tc.in, u.Code, u.Base)
		if err != nil || !close(got, tc.want) {
			t.Errorf("%s: %v -> %v, want %v (err %v)", u.Code, tc.in, got, tc.want, err)
		}
		// Round trip through the base unit.
		back, err := Convert(got, u.Base, u.Code)
		if err != nil || !close(back, tc.in) {
			t.Errorf("%s: round trip %v -> %v", u.Code, tc.in, back)
		}
	}
	for code := range wantBase {
		if _, ok := LookupUnit(code); !ok {
			t.Errorf("test case for unknown unit %s", code)
		}
	}
}

func TestConvertBetweenNonBaseUnits(t *testing.T) {
	cases := []struct {
		v        float64
		from, to string
		want     float64
	}{
		{1500, "ms", "s", 1.5},
		{1.5, "s", "ms", 1500},
		{5, "h", "min", 300},
		{212, "°F", "°C", 100},
		{0, "°C", "°F", 32},
		{-40, "°F", "°C", -40},
		{1, "mi", "km", 1.609344},
		{0.5, "fraction", "%", 50},
		{97, "%", "fraction", 0.97},
		{70, "kg", "kg", 70},
	}
	for _, c := range cases {
		got, err := Convert(c.v, c.from, c.to)
		if err != nil || !close(got, c.want) {
			t.Errorf("%v %s -> %s = %v (err %v), want %v", c.v, c.from, c.to, got, err, c.want)
		}
	}
	for _, bad := range [][2]string{{"kg", "m"}, {"nope", "kg"}, {"kg", "nope"}, {"nope", "nope"}} {
		if _, err := Convert(1, bad[0], bad[1]); err == nil {
			t.Errorf("Convert %s -> %s should fail", bad[0], bad[1])
		}
	}
}

func TestToCanonical(t *testing.T) {
	v, converted, err := ToCanonical("weight", 154.3235835, "lb")
	if err != nil || !converted || !close(v, 70) {
		t.Errorf("weight lb: %v %v %v", v, converted, err)
	}
	v, converted, err = ToCanonical("hrv_sdnn", 42, "ms")
	if err != nil || converted || v != 42 {
		t.Errorf("hrv_sdnn in ms is canonical: %v %v %v", v, converted, err)
	}
	if v, _, err = ToCanonical("hrv_sdnn", 0.042, "s"); err != nil || !close(v, 42) {
		t.Errorf("hrv_sdnn in s: %v %v", v, err)
	}
	if v, _, err = ToCanonical("spo2", 0.97, "fraction"); err != nil || !close(v, 97) {
		t.Errorf("spo2 fraction: %v %v", v, err)
	}
	if _, _, err = ToCanonical("weight", 1, "m"); err == nil {
		t.Error("weight in metres must fail")
	}
	if _, _, err = ToCanonical("nope", 1, "kg"); err == nil {
		t.Error("unknown metric must fail")
	}
}

func TestEventsWellFormed(t *testing.T) {
	codeRE := regexp.MustCompile(`^[a-z][a-z0-9_]*$`) // the health_events CHECK
	seen := map[string]bool{}
	for _, e := range events {
		if !codeRE.MatchString(e.Code) || seen[e.Code] {
			t.Errorf("%s: bad or duplicate event code", e.Code)
		}
		if _, clash := Lookup(e.Code); clash {
			t.Errorf("%s is both a metric and an event", e.Code)
		}
		seen[e.Code] = true
	}
	if e, ok := LookupEvent("walking_steadiness_alert"); !ok || !e.AllowsLevel("repeat_low") || !e.AllowsLevel("") || e.AllowsLevel("low") {
		t.Error("walking_steadiness_alert levels")
	}
	if _, ok := LookupEvent("heart_rate"); ok {
		t.Error("a metric is not an event")
	}
}

// TestGeneratedFilesUpToDate is the drift check: regenerate in memory and compare with the committed files.
func TestGeneratedFilesUpToDate(t *testing.T) {
	root := filepath.Join("..", "..")
	files := SeedFiles()
	files[DocPath] = MetricsDoc()
	for path, want := range files {
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("%s: %v (run: go run ./internal/catalog/gen)", path, err)
		}
		if string(got) != want {
			t.Errorf("%s is stale; run: go run ./internal/catalog/gen", path)
		}
	}
}
