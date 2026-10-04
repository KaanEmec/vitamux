package resolve

import (
	"reflect"
	"strings"
	"testing"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// defaults builds the default rules activeSet adds for an owner without rules of their own.
func defaults(t *testing.T, priority []string) map[string]Version {
	t.Helper()
	energy, _ := LookupBuiltin("active_energy")
	out := map[string]Version{}
	for _, m := range catalog.Metrics() {
		if _, ok := LookupBuiltin(m.Code); ok || RuleMetric(m.Code) != m.Code {
			continue
		}
		var leader *Rule
		if defaultFollows[m.Code] == "active_energy" {
			leader = &energy.Rule
		}
		out[m.Code] = defaultRule(m, priority, leader)
	}
	return out
}

func TestDefaultRulesValid(t *testing.T) {
	ds := defaults(t, []string{"whoop", "garmin", "manual", "apple_health"})
	if len(ds) != len(NoBuiltin) {
		t.Errorf("%d default rules for %d codes without a built-in", len(ds), len(NoBuiltin))
	}
	var rules []*Rule
	for _, b := range Builtins() {
		rules = append(rules, &b.Rule)
	}
	for code, v := range ds {
		if _, ok := NoBuiltin[code]; !ok {
			t.Errorf("%s: a default rule for a code with a built-in", code)
		}
		if err := Validate(v.Rule); err != nil {
			t.Errorf("%s: %v", v.Ref, err)
		}
		if back, err := ParseRule(v.Spec); err != nil || !reflect.DeepEqual(*back, *v.Rule) {
			t.Errorf("%s: spec does not round-trip (%v)", v.Ref, err)
		}
		if !v.Builtin || !v.Default || !v.Active || !strings.HasPrefix(v.Ref, "default:"+code+":") {
			t.Errorf("%s: %+v", code, v)
		}
		rules = append(rules, v.Rule)
	}
	if err := ValidateSet(rules); err != nil {
		t.Errorf("built-ins and default rules are not valid together: %v", err)
	}
}

func TestDefaultRuleLadder(t *testing.T) {
	ds := defaults(t, []string{"whoop", "manual", "garmin"})
	var ids []string
	for _, g := range ds["floors_climbed"].Rule.Groups {
		ids = append(ids, g.ID)
	}
	want := "whoop whoop_apple garmin garmin_apple watch watch_relayed band band_relayed ring ring_relayed chest_strap arm_band phone device manual"
	if got := strings.Join(ids, " "); got != want {
		t.Errorf("owner's providers (relays included, manual provider skipped), then the generic ladder:\n got %s\nwant %s", got, want)
	}
	r := ds["floors_climbed"].Rule
	if r.Strategy.Op != OpFirstAvailable || r.Quality != nil || r.Window.Kind != catalog.WindowLocalDay {
		t.Errorf("first_available, no gates, local_day: %+v", r)
	}
	night := catalog.Metric{Code: "night_only", Agg: catalog.Intensive, DerivedFrom: "heart_rate"}
	if k := defaultRule(night, nil, nil).Rule.Window.Kind; k != catalog.WindowLocalNight {
		t.Errorf("a night-only code uses local_night, got %s", k)
	}
	for _, code := range []string{"garmin_stress", "whoop_recovery"} {
		g := ds[code].Rule.Groups
		if provider, _, _ := strings.Cut(code, "_"); len(g) != 1 || g[0].ID != provider || g[0].Match[0].Provider != provider {
			t.Errorf("%s: a provider-scoped code has its provider alone: %+v", code, g)
		}
	}
	energy, _ := LookupBuiltin("active_energy")
	if b := ds["basal_energy"].Rule; b.Follow != "active_energy" || b.Window != energy.Rule.Window || !reflect.DeepEqual(b.Groups, energy.Rule.Groups) {
		t.Errorf("basal_energy follows active_energy with its window and groups: %+v", b)
	}
}

func TestDefaultRefFollowsPriority(t *testing.T) {
	a, b, again := defaults(t, []string{"garmin", "whoop"}), defaults(t, []string{"whoop", "garmin"}), defaults(t, []string{"garmin", "whoop"})
	if a["floors_climbed"].Ref == b["floors_climbed"].Ref {
		t.Error("a new source order is a new ref")
	}
	if a["floors_climbed"].Ref != again["floors_climbed"].Ref {
		t.Error("the same source order is the same ref")
	}
	if a["garmin_stress"].Ref != b["garmin_stress"].Ref || a["basal_energy"].Ref != b["basal_energy"].Ref {
		t.Error("rules that do not use the source order keep their ref")
	}
}
