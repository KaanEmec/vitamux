package resolve

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

const ovMetric = "heart_rate"

// ovFixture: a heart_rate hour rule (garmin, whoop, apple; first_available) with one hour of
// samples per group, plus the next hour for garmin only.
type ovFixture struct {
	r      *Rule
	w, w2  Window
	s      Series
	garmin Input
}

func newOvFixture() ovFixture {
	f := ovFixture{r: stHR(OpFirstAvailable), w: agHourWindow("2026-06-15T10:00:00Z"), w2: agHourWindow("2026-06-15T11:00:00Z")}
	f.garmin = agSample(agGarmin, f.w.Start.Add(time.Minute), 60)
	f.s = Series{ovMetric: {
		f.garmin, agSample(agWhoop, f.w.Start.Add(2*time.Minute), 70), agSample(agWatch, f.w.Start.Add(3*time.Minute), 80),
		agSample(agGarmin, f.w2.Start.Add(time.Minute), 65),
	}}
	return f
}

// scopeOf is the scope of window w of the rule metric.
func scopeOf(metric string, w Window) Scope {
	return Scope{Metric: metric, Kind: w.Kind, Key: w.Key, LocalDate: w.Date}
}

func ovMake(w Window, a OverrideAction, edit func(*Override)) Override {
	o := Override{ID: uuid.New(), Scope: scopeOf(ovMetric, w), Action: a, CreatedBy: "owner", CreatedAt: time.Unix(1_700_000_000, 0)}
	edit(&o)
	return o
}

func ovResolve(t *testing.T, f ovFixture, w Window, ovs ...Override) Resolved {
	t.Helper()
	res, err := f.r.ResolveWindowOverridden(w, f.s, Options{}, ovs)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestOverrideExcludeInput(t *testing.T) {
	f := newOvFixture()
	before := ovResolve(t, f, f.w)
	if before.Status != ResultDirect || before.Selected != "garmin" || before.Computed != nil {
		t.Fatalf("baseline: %s %q computed=%v", before.Status, before.Selected, before.Computed)
	}
	ov := ovMake(f.w, ExcludeInput, func(o *Override) { o.InputID = f.garmin.ID })
	res := ovResolve(t, f, f.w, ov)
	if res.Status != ResultOverridden || res.Selected != "whoop" || !agNear(res.Value, 70) {
		t.Errorf("excluded: %s %q %v, want overridden whoop 70", res.Status, res.Selected, res.Value)
	}
	if len(res.Overrides) != 1 || res.Overrides[0].ID != ov.ID || res.Computed == nil || res.Computed.Selected != "garmin" || !agNear(res.Computed.Value, 60) {
		t.Errorf("applied %v, computed %+v", res.Overrides, res.Computed)
	}
	// Only its window: the next hour has no override and keeps the computed result.
	if other := ovResolve(t, f, f.w2, ov); other.Status != ResultDirect || other.Overrides != nil || other.Computed != nil {
		t.Errorf("other window changed: %+v", other)
	}
	// The input series is not modified.
	if n := len(f.s[ovMetric]); n != 4 {
		t.Errorf("series has %d rows, want 4", n)
	}
	// An id that is not in the window changes nothing and is reported.
	miss := ovResolve(t, f, f.w, ovMake(f.w, ExcludeInput, func(o *Override) { o.InputID = 999_999 }))
	if miss.Status != ResultDirect || len(miss.Overrides) != 0 || len(miss.Ignored) != 1 {
		t.Errorf("missing input: %s applied %d ignored %d", miss.Status, len(miss.Overrides), len(miss.Ignored))
	}
	// Excluding the only input leaves no data: the status stays no_data.
	only := ovResolve(t, f, f.w2, ovMake(f.w2, ExcludeInput, func(o *Override) { o.InputID = f.s[ovMetric][3].ID }))
	if only.Status != ResultNoData || len(only.Overrides) != 1 {
		t.Errorf("only input excluded: %s applied %d", only.Status, len(only.Overrides))
	}
}

func TestOverrideForceSource(t *testing.T) {
	f := newOvFixture()
	ov := ovMake(f.w, ForceSource, func(o *Override) { o.Group = "apple" })
	res := ovResolve(t, f, f.w, ov)
	if res.Status != ResultOverridden || res.Selected != "apple" || !agNear(res.Value, 80) {
		t.Fatalf("forced: %s %q %v", res.Status, res.Selected, res.Value)
	}
	st := stStatuses(res.WindowResult)
	if st["apple"] != StatusSelected || st["garmin"] != StatusUnused || st["whoop"] != StatusUnused {
		t.Errorf("group statuses %v", st)
	}
	for _, g := range res.Groups {
		if g.ID == "garmin" && g.Reason != ReasonOverridden {
			t.Errorf("garmin reason %q", g.Reason)
		}
	}
	if res.Computed == nil || res.Computed.Selected != "garmin" || stStatuses(*res.Computed)["garmin"] != StatusSelected {
		t.Errorf("computed result was altered: %+v", res.Computed)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %v", res.Warnings)
	}
	// A group without inputs cannot be forced: nothing changes and the override is ignored.
	none := ovResolve(t, f, f.w2, ovMake(f.w2, ForceSource, func(o *Override) { o.Group = "whoop" }))
	if none.Status != ResultDirect || len(none.Ignored) != 1 || len(none.Overrides) != 0 {
		t.Errorf("force without data: %s ignored %d", none.Status, len(none.Ignored))
	}
	// An unknown group is ignored too.
	if bad := ovResolve(t, f, f.w, ovMake(f.w, ForceSource, func(o *Override) { o.Group = "nope" })); len(bad.Ignored) != 1 || bad.Status != ResultDirect {
		t.Errorf("unknown group: %s ignored %d", bad.Status, len(bad.Ignored))
	}
}

func TestOverrideSetValue(t *testing.T) {
	f := newOvFixture()
	set := ovMake(f.w, SetValue, func(o *Override) { o.Value, o.Unit, o.Note = 66, "bpm", "chest strap reading" })
	force := ovMake(f.w, ForceSource, func(o *Override) { o.Group = "apple" })
	res := ovResolve(t, f, f.w, set, force)
	if res.Status != ResultOverridden || !agNear(res.Value, 66) || res.Selected != "" {
		t.Fatalf("set_value: %s %v %q", res.Status, res.Value, res.Selected)
	}
	// set_value wins over force_source; the computed groups stay visible.
	if len(res.Overrides) != 1 || res.Overrides[0].ID != set.ID || len(res.Ignored) != 1 || res.Ignored[0].ID != force.ID {
		t.Errorf("applied %v ignored %v", res.Overrides, res.Ignored)
	}
	if st := stStatuses(res.WindowResult); st["garmin"] != StatusSelected || st["apple"] != StatusUnused {
		t.Errorf("groups %v", st)
	}
	if res.Computed == nil || !agNear(res.Computed.Value, 60) {
		t.Errorf("computed %+v", res.Computed)
	}
	// A window with no data takes the value too: the owner supplied it.
	empty := agHourWindow("2026-06-15T15:00:00Z")
	nd := ovResolve(t, f, empty, ovMake(empty, SetValue, func(o *Override) { o.Value, o.Unit, o.Note = 66, "bpm", "n" }))
	if nd.Status != ResultOverridden || !agNear(nd.Value, 66) {
		t.Errorf("no-data window: %s %v", nd.Status, nd.Value)
	}
}

func TestOverrideRevokeRestoresComputed(t *testing.T) {
	f := newOvFixture()
	want := ovResolve(t, f, f.w).WindowResult
	ex := ovMake(f.w, ExcludeInput, func(o *Override) { o.InputID = f.garmin.ID })
	set := ovMake(f.w, SetValue, func(o *Override) { o.Value, o.Unit, o.Note = 66, "bpm", "n" })
	if got := ovResolve(t, f, f.w, ex, set); got.Status != ResultOverridden {
		t.Fatalf("active overrides: %s", got.Status)
	}
	now := time.Now()
	ex.RevokedAt, set.RevokedAt = &now, &now
	got := ovResolve(t, f, f.w, ex, set)
	if !reflect.DeepEqual(got.WindowResult, want) || got.Computed != nil || got.Overrides != nil {
		t.Errorf("revoked overrides changed the result: %+v", got)
	}
}

func TestOverrideScopeAndSeries(t *testing.T) {
	f := newOvFixture()
	// Other metric and other window kind do not match; the series carries the selection over.
	other := ovMake(f.w, SetValue, func(o *Override) { o.Metric, o.Value, o.Unit, o.Note = "steps", 1, "count", "n" })
	day := ovMake(f.w, SetValue, func(o *Override) { o.Kind, o.Value, o.Unit, o.Note = catalog.WindowLocalDay, 1, "bpm", "n" })
	if res := ovResolve(t, f, f.w, other, day); res.Computed != nil || res.Status != ResultDirect {
		t.Errorf("foreign overrides matched: %+v", res)
	}
	ex := ovMake(f.w, ExcludeInput, func(o *Override) { o.InputID = f.garmin.ID })
	out, err := f.r.ResolveWindowsOverridden([]Window{f.w, f.w2}, f.s, Options{}, []Override{ex})
	if err != nil || len(out) != 2 || out[0].Status != ResultOverridden || out[1].Status != ResultDirect {
		t.Fatalf("series: %v %+v", err, out)
	}
}

// Excluding one component of a blood pressure reading drops the whole reading, so the rest of
// the window never mixes components of different readings.
func TestOverrideExcludeDropsWholeReading(t *testing.T) {
	day := midnightUTC(instant("2026-06-15T00:00:00Z"))
	mk := func(v float64, group int64) Input {
		in := agSample(agCuff, day.Add(8*time.Hour), v)
		in.GroupID = group
		return in
	}
	sys1, dia1, sys2, dia2 := mk(120, 1), mk(80, 1), mk(150, 2), mk(95, 2)
	s := Series{"systolic_bp": {sys1, sys2}, "diastolic_bp": {dia1, dia2}}
	got, found := dropInputs(s, []int64{sys2.ID})
	if !found[sys2.ID] || len(got["systolic_bp"]) != 1 || len(got["diastolic_bp"]) != 1 || got["diastolic_bp"][0].ID != dia1.ID {
		t.Errorf("reading not dropped whole: %+v", got)
	}
	if len(s["diastolic_bp"]) != 2 || len(s["systolic_bp"]) != 2 {
		t.Error("dropInputs modified its input")
	}
}

func TestValidateOverride(t *testing.T) {
	w := agHourWindow("2026-06-15T10:00:00Z")
	ok := NewOverride{Scope: scopeOf(ovMetric, w), Action: SetValue, Value: 66, Unit: "bpm", Note: "n"}
	if err := validateOverride(ok); err != nil {
		t.Fatal(err)
	}
	day := midnightUTC(w.Start)
	for name, edit := range map[string]func(*NewOverride){
		"unknown metric":      func(n *NewOverride) { n.Metric = "nope" },
		"window not allowed":  func(n *NewOverride) { n.Kind = catalog.WindowReading },
		"no key":              func(n *NewOverride) { n.Key = "" },
		"no date":             func(n *NewOverride) { n.LocalDate = time.Time{} },
		"day key not date":    func(n *NewOverride) { n.Kind, n.Key, n.LocalDate = catalog.WindowLocalDay, "2026-06-16", day },
		"unknown action":      func(n *NewOverride) { n.Action = "delete" },
		"out of range":        func(n *NewOverride) { n.Value = 900 },
		"not canonical unit":  func(n *NewOverride) { n.Unit = "bps" },
		"no note":             func(n *NewOverride) { n.Note = "" },
		"exclude without id":  func(n *NewOverride) { n.Action = ExcludeInput },
		"force without group": func(n *NewOverride) { n.Action = ForceSource },
		"family set_value":    func(n *NewOverride) { n.Metric = FamilyBloodPressure },
	} {
		n := ok
		edit(&n)
		if err := validateOverride(n); !errors.Is(err, ErrInvalidOverride) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
