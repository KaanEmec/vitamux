package resolve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

func TestBuiltinsValid(t *testing.T) {
	all := Builtins()
	rules := make([]*Rule, len(all))
	for i := range all {
		b := &all[i]
		rules[i] = &b.Rule
		if err := Validate(&b.Rule); err != nil {
			t.Errorf("%s: %v", b.Ref(), err)
		}
		if b.Version < 1 || b.Why == "" {
			t.Errorf("%s: needs a version and a reason", b.Ref())
		}
		// The spec copied on first edit must parse back to the same rule.
		spec, err := json.Marshal(b.Rule)
		if err != nil {
			t.Fatal(err)
		}
		back, err := ParseRule(spec)
		if err != nil || !reflect.DeepEqual(*back, b.Rule) {
			t.Errorf("%s: does not round-trip through ParseRule (%v)", b.Ref(), err)
		}
	}
	if err := ValidateSet(rules); err != nil {
		t.Errorf("built-ins are not valid together: %v", err)
	}
}

func TestEveryMetricHasBuiltinOrIsExempt(t *testing.T) {
	for _, m := range catalog.Metrics() {
		code := m.Code
		switch {
		case m.Agg == catalog.SleepDerived:
			code = FamilySleep
		case m.Group == "bp_reading":
			code = FamilyBloodPressure
		}
		_, has := LookupBuiltin(code)
		_, exempt := NoBuiltin[m.Code]
		if has == exempt {
			t.Errorf("%s: needs exactly one of a built-in or a NoBuiltin entry (built-in %v, exempt %v)", m.Code, has, exempt)
		}
	}
	for code := range NoBuiltin {
		if _, ok := catalog.Lookup(code); !ok {
			t.Errorf("NoBuiltin names %s, which is not in the catalogue", code)
		}
	}
}

func TestBuiltinDeviceTypesAreInVocabulary(t *testing.T) {
	for _, b := range Builtins() {
		for _, g := range b.Rule.Groups {
			for _, s := range g.Match {
				if s.DeviceType != "" && !slices.Contains(DeviceTypes, s.DeviceType) {
					t.Errorf("%s: group %s matches device type %q, which is not in DeviceTypes", b.Ref(), g.ID, s.DeviceType)
				}
			}
		}
	}
}

func TestBuiltinGroupIDsAreConsistent(t *testing.T) {
	seen := map[string][]Selector{}
	for _, b := range Builtins() {
		for _, g := range b.Rule.Groups {
			if prev, ok := seen[g.ID]; ok && !reflect.DeepEqual(prev, g.Match) {
				t.Errorf("%s: group %s differs from its use in another built-in", b.Ref(), g.ID)
			}
			seen[g.ID] = g.Match
		}
	}
}

func TestBuiltinShape(t *testing.T) {
	refs := map[string]bool{}
	for _, b := range Builtins() {
		if refs[b.Ref()] {
			t.Errorf("duplicate built-in %s", b.Ref())
		}
		refs[b.Ref()] = true
	}
	hr, _ := LookupBuiltin("heart_rate")
	if hr.Ref() != "builtin:heart_rate:3" || hr.Rule.Groups[0].ID != "chest_strap" || hr.Rule.Groups[3].ID != "whoop" || hr.Rule.Groups[5].ID != "garmin" || hr.Rule.Contexts[ContextWorkout] == nil {
		t.Errorf("heart_rate: %+v", hr.Rule)
	}
	for _, code := range []string{"resting_heart_rate", "hrv_rmssd_nightly"} {
		b, _ := LookupBuiltin(code)
		if b.Rule.Strategy.Op.pooling() || b.Rule.Groups[0].ID != "oura" {
			t.Errorf("%s: selection-only, Oura first: %+v", code, b.Rule.Strategy)
		}
	}
	garmin := biBrand(provGarmin)
	if len(garmin) != 2 || garmin[1].ID != "garmin_apple" || garmin[1].Match[0].Provider != provApple {
		t.Errorf("a relaying brand is its direct group, then its Apple Health group: %+v", garmin)
	}
	watch := biAppleDevice("apple_watch", "Watch")[0].Match[0]
	if watch.DeviceManufacturer != appleInc || watch.DeviceModel != "Watch" || watch.DeviceType != "" {
		t.Errorf("apple_watch is Apple's manufacturer and model, not a device type: %+v", watch)
	}
	steps, _ := LookupBuiltin("steps")
	if i, p := groupIndex(steps.Rule, "iphone"), groupIndex(steps.Rule, "phone"); i < 0 || i > p {
		t.Errorf("steps: iphone (%d) comes before phone (%d)", i, p)
	}
	for _, code := range []string{"steps", "distance_walk_run", "active_energy", "resting_heart_rate_nocturnal", FamilySleep} {
		b, _ := LookupBuiltin(code)
		if q := b.Rule.Quality; q != nil && (q.MinCoverage != nil || q.RequireWear != "" || q.Sleep != nil && q.Sleep.MinEpisodeCoverage != nil) {
			t.Errorf("%s: gates are opt-in: %+v", code, q)
		}
	}
	if rhr, _ := LookupBuiltin("resting_heart_rate_nocturnal"); groupIndex(rhr.Rule, "whoop") != 3 || groupIndex(rhr.Rule, "whoop") > groupIndex(rhr.Rule, "garmin") {
		t.Errorf("resting_heart_rate_nocturnal shares the heart-rate order: %+v", rhr.Rule.Groups)
	}
	if groupIndex(hr.Rule, "whoop_apple") < 0 {
		t.Error("heart_rate: WHOOP relayed through Apple Health has its group")
	}
	if g := biBrand(provGarmin)[1].Match; g[len(g)-1].DeviceManufacturer != "Garmin" {
		t.Errorf("garmin_apple also takes Garmin devices in Apple Health: %+v", g)
	}
	fat, _ := LookupBuiltin("body_fat_ratio")
	if fat.Rule.Follow != "weight" {
		t.Error("body composition follows weight")
	}
	if _, ok := LookupBuiltin("sleep_deep"); ok {
		t.Error("sleep codes have no per-code built-in")
	}
	a, b := Builtins(), Builtins()
	a[0].Rule.Groups[0].ID = "changed"
	if b[0].Rule.Groups[0].ID == "changed" {
		t.Error("Builtins must return fresh values")
	}
}

// TestDefaultsDocUpToDate is the drift check for the generated doc.
func TestDefaultsDocUpToDate(t *testing.T) {
	got, err := os.ReadFile(filepath.Join("..", "..", DefaultsDocPath))
	if err != nil {
		t.Fatalf("%v (run: go run ./internal/resolve/gen)", err)
	}
	if string(got) != DefaultsDoc() {
		t.Errorf("%s is stale; run: go run ./internal/resolve/gen", DefaultsDocPath)
	}
	if !strings.Contains(string(got), "`builtin:sleep:3`") {
		t.Error("doc lists the sleep family built-in")
	}
}

func groupIndex(r Rule, id string) int {
	return slices.IndexFunc(r.Groups, func(g Group) bool { return g.ID == id })
}
