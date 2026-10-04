package resolve

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

func ruleSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	abs, err := filepath.Abs("../../schemas/resolution-rule.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(abs)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func schemaValid(t *testing.T, s *jsonschema.Schema, doc []byte) bool {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return false
	}
	return s.Validate(inst) == nil
}

func example(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "valid", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The CLAUDE.md examples (mean across sources, first_available ladder, Apple Watch first with a
// relayed exclusion, acknowledged sum), one rule per MVP extension, and both rule families.
func TestValidExamples(t *testing.T) {
	s := ruleSchema(t)
	files, err := filepath.Glob(filepath.Join("testdata", "valid", "*.json"))
	if err != nil || len(files) < 12 {
		t.Fatalf("want at least 12 examples, got %d (%v)", len(files), err)
	}
	for _, f := range files {
		doc := example(t, filepath.Base(f))
		if !schemaValid(t, s, doc) {
			t.Errorf("%s: rejected by the JSON Schema", f)
		}
		r, err := ParseRule(doc)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		// Rules round-trip: what J09.2 stores from a Rule parses and validates again.
		again, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseRule(again); err != nil || !schemaValid(t, s, again) {
			t.Errorf("%s: re-encoded rule rejected: %v", f, err)
		}
	}
}

func mutate(t *testing.T, doc []byte, f func(m map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(doc, &m); err != nil {
		t.Fatal(err)
	}
	f(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func obj(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		m = m[k].(map[string]any)
	}
	return m
}

func group(m map[string]any, i int) map[string]any {
	return m["groups"].([]any)[i].(map[string]any)
}

func TestInvalidRules(t *testing.T) {
	s := ruleSchema(t)
	cases := []struct {
		name, base string
		mut        func(m map[string]any)
		ptr        string
		detail     string
		schema     bool // the JSON Schema rejects it too (structural)
	}{
		{"unknown field", "claude-heart-rate-mean.json", func(m map[string]any) { m["priority"] = 1 }, "", `unknown field "priority"`, true},
		{"unknown selector field", "claude-heart-rate-mean.json", func(m map[string]any) {
			group(m, 0)["match"] = []any{map[string]any{"vendor": "garmin"}}
		}, "", `unknown field "vendor"`, true},
		{"wrong schema", "claude-heart-rate-mean.json", func(m map[string]any) { m["schema"] = "vitamux.rule/2" }, "/schema", "must be", true},
		{"unknown metric", "claude-heart-rate-mean.json", func(m map[string]any) { m["metric"] = "heart_rates" }, "/metric", "unknown metric", false},
		{"window not allowed for aggregation", "claude-steps-sum.json", func(m map[string]any) { m["window"] = map[string]any{"kind": "local_night"} }, "/window/kind", "not allowed for steps", false},
		{"bucket without size", "claude-heart-rate-mean.json", func(m map[string]any) { delete(obj(m, "window"), "size") }, "/window/size", "1m, 5m", true},
		{"bucket size not allowed", "claude-heart-rate-mean.json", func(m map[string]any) { obj(m, "window")["size"] = "7m" }, "/window/size", "1m, 5m", true},
		{"size on a day window", "claude-steps-sum.json", func(m map[string]any) { obj(m, "window")["size"] = "5m" }, "/window/size", "only bucket", true},
		{"no groups", "claude-heart-rate-mean.json", func(m map[string]any) { m["groups"] = []any{} }, "/groups", "at least one group", true},
		{"empty group", "claude-heart-rate-mean.json", func(m map[string]any) { group(m, 1)["match"] = []any{} }, "/groups/1/match", "at least one selector", true},
		{"empty selector", "claude-heart-rate-mean.json", func(m map[string]any) { m["exclude"] = []any{map[string]any{}} }, "/exclude/0", "at least one field", true},
		{"duplicate group id", "claude-heart-rate-mean.json", func(m map[string]any) { group(m, 2)["id"] = "garmin" }, "/groups/2/id", "duplicate", false},
		{"bad connection id", "claude-heart-rate-mean.json", func(m map[string]any) {
			group(m, 0)["match"] = []any{map[string]any{"connection_id": "conn-1"}}
		}, "/groups/0/match/0/connection_id", "UUID", true},
		{"origin key and prefix", "claude-apple-watch-first.json", func(m map[string]any) {
			m["exclude"] = []any{map[string]any{"origin_key": "a.b", "origin_key_prefix": "a."}}
		}, "/exclude/0/origin_key_prefix", "not both", true},
		{"brand too long", "brand-selectors.json", func(m map[string]any) {
			m["exclude"] = []any{map[string]any{"device_manufacturer": strings.Repeat("g", 257)}}
		}, "/exclude/0/device_manufacturer", "at most 256", true},
		{"bad entry", "claude-apple-watch-first.json", func(m map[string]any) { m["exclude"] = []any{map[string]any{"entry": "typed"}} }, "/exclude/0/entry", "device or manual", true},
		{"unknown op", "claude-heart-rate-mean.json", func(m map[string]any) { obj(m, "strategy")["op"] = "median_across_sources" }, "/strategy/op", "must be", true},
		{"single_source with two groups", "claude-heart-rate-mean.json", func(m map[string]any) { m["strategy"] = map[string]any{"op": "single_source"} }, "/groups", "exactly one group", true},
		{"min_sources on first_available", "claude-resting-hr-first-available.json", func(m map[string]any) { obj(m, "strategy")["min_sources"] = 2 }, "/strategy/min_sources", "mean, minimum", false},
		{"min_sources above groups", "claude-heart-rate-mean.json", func(m map[string]any) { obj(m, "strategy")["min_sources"] = 4 }, "/strategy/min_sources", "cannot exceed", false},
		{"unacknowledged sum", "claude-steps-sum.json", func(m map[string]any) { delete(m, "acknowledged_warnings") }, "/acknowledged_warnings", "cross_source_sum_duplicate_risk", true},
		{"unacknowledged intra_group sum", "e9-steps-compose.json", func(m map[string]any) { m["within_source"] = map[string]any{"intra_group": "sum"} }, "/acknowledged_warnings", "intra_group: sum", true},
		{"sum on intensive", "claude-heart-rate-mean.json", func(m map[string]any) {
			m["strategy"] = map[string]any{"op": "sum_across_sources"}
			m["acknowledged_warnings"] = []any{"cross_source_sum_duplicate_risk"}
		}, "/strategy/op", "additive metric", false},
		{"unknown acknowledged warning", "claude-steps-sum.json", func(m map[string]any) {
			m["acknowledged_warnings"] = []any{"cross_source_sum_duplicate_risk", "trust_me"}
		}, "/acknowledged_warnings/1", "unknown warning", true},
		{"unknown quality flag", "claude-heart-rate-mean.json", func(m map[string]any) { obj(m, "quality")["exclude_flags"] = []any{"manual"} }, "/quality/exclude_flags/0", "unknown quality flag", true},
		{"plausible range inverted", "claude-heart-rate-mean.json", func(m map[string]any) { obj(m, "quality")["plausible_range"] = []any{230, 25} }, "/quality/plausible_range", "below high", false},
		{"coverage above one", "claude-heart-rate-mean.json", func(m map[string]any) { obj(m, "quality")["min_coverage"] = 1.5 }, "/quality/min_coverage", "(0, 1]", true},
		{"bad staleness", "claude-resting-hr-first-available.json", func(m map[string]any) { obj(m, "quality")["max_staleness"] = "1.5d" }, "/quality/max_staleness", "duration", true},
		{"night anchor in the morning", "sleep-family.json", func(m map[string]any) { obj(m, "quality", "sleep")["night_anchor"] = "06:00" }, "/quality/sleep/night_anchor", "12:00", true},
		{"contexts naming an unknown group", "e1-workout-hr-contexts.json", func(m map[string]any) { obj(m, "contexts")["sleep"] = []any{"oura"} }, "/contexts/sleep/0", "names no group", false},
		{"unknown context", "e1-workout-hr-contexts.json", func(m map[string]any) { obj(m, "contexts")["commute"] = []any{"ring"} }, "/contexts/commute", "unknown context", true},
		{"workout source outside workout", "e1-workout-hr-contexts.json", func(m map[string]any) { obj(m, "contexts")["sleep"] = []any{"@workout_source"} }, "/contexts/sleep/0", "only valid in contexts.workout", true},
		{"require_wear without buckets", "vo2max-latest.json", func(m map[string]any) { obj(m, "quality")["require_wear"] = "heart_rate" }, "/quality/require_wear", "buckets", false},
		{"require_wear on a non-sample metric", "e3-active-energy-wear.json", func(m map[string]any) { obj(m, "quality")["require_wear"] = "steps" }, "/quality/require_wear", "intensive sample metric", false},
		{"compose on non-additive", "claude-resting-hr-first-available.json", func(m map[string]any) {
			m["compose"] = map[string]any{"from": "hour", "op": "first_available"}
		}, "/compose", "additive metric", false},
		{"compose outside local_day", "e9-steps-compose.json", func(m map[string]any) { m["window"] = map[string]any{"kind": "hour"} }, "/compose", "local_day", false},
		{"compose from bucket", "e9-steps-compose.json", func(m map[string]any) { obj(m, "compose")["from"] = "bucket" }, "/compose/from", "must be hour", true},
		{"per-code rule for a sleep code", "sleep-family.json", func(m map[string]any) { m["metric"] = "sleep_deep" }, "/metric", "sleep-family rule", false},
		{"per-code rule for a blood pressure component", "blood-pressure-family.json", func(m map[string]any) { m["metric"] = "bp_systolic" }, "/metric", "blood_pressure rule", false},
		{"mean across blood pressure sources", "blood-pressure-family.json", func(m map[string]any) { m["strategy"] = map[string]any{"op": "mean_across_sources"} }, "/strategy/op", "never mix sources", false},
		{"event_priority outside sleep", "claude-resting-hr-first-available.json", func(m map[string]any) { m["strategy"] = map[string]any{"op": "event_priority"} }, "/strategy/op", "sleep family only", false},
		{"plausible range on a family", "sleep-family.json", func(m map[string]any) { obj(m, "quality")["plausible_range"] = []any{0, 1} }, "/quality/plausible_range", "no single unit", false},
		{"follow itself", "e5-body-fat-follow.json", func(m map[string]any) { m["follow"] = "body_fat_ratio" }, "/follow", "itself", false},
		{"follow leader with incompatible window", "e2-nocturnal-min-rolling-mean.json", func(m map[string]any) { m["follow"] = "steps" }, "/follow", "leader steps cannot use window local_night", false},
		{"follow a sleep code", "e5-body-fat-follow.json", func(m map[string]any) { m["follow"] = "sleep_total" }, "/follow", "rule family", false},
		{"min_rolling_mean without span", "e2-nocturnal-min-rolling-mean.json", func(m map[string]any) { delete(obj(m, "within_source"), "span") }, "/within_source/span", "required", true},
		{"min_rolling_mean span off the base bucket", "e2-nocturnal-min-rolling-mean.json", func(m map[string]any) { obj(m, "within_source")["span"] = "7m" }, "/within_source/span", "multiple", false},
		{"min_rolling_mean on a bucket window", "e2-nocturnal-min-rolling-mean.json", func(m map[string]any) { m["window"] = map[string]any{"kind": "bucket", "size": "5m"} }, "/within_source/statistic", "local_night", false},
		{"daily mean on an intensive metric", "claude-heart-rate-mean.json", func(m map[string]any) { m["within_source"] = map[string]any{"statistic": "mean"} }, "/within_source/statistic", "latest-type", false},
		{"daily_value_policy on intensive", "claude-heart-rate-mean.json", func(m map[string]any) { m["within_source"] = map[string]any{"daily_value_policy": "prefer_reported"} }, "/within_source/daily_value_policy", "additive", false},
		{"wrong type", "claude-heart-rate-mean.json", func(m map[string]any) { obj(m, "strategy")["min_sources"] = "two" }, "/strategy/min_sources", "must be int", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := mutate(t, example(t, tc.base), tc.mut)
			_, err := ParseRule(doc)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("ParseRule = %v, want *ValidationError", err)
			}
			found := false
			for _, fe := range ve.Errors {
				if fe.Pointer == tc.ptr && strings.Contains(fe.Detail, tc.detail) {
					found = true
				}
			}
			if !found {
				t.Errorf("errors %v, want pointer %q with %q", ve.Errors, tc.ptr, tc.detail)
			}
			if tc.schema && schemaValid(t, s, doc) {
				t.Error("JSON Schema accepted a structurally invalid rule")
			}
		})
	}
}

// A provider-namespaced score is never pooled across providers (metric-catalog.md#rules).
// The catalogue has no score yet, so a test catalogue adds one.
func TestProviderScopedScoreNotPooled(t *testing.T) {
	lookup := func(code string) (catalog.Metric, bool) {
		if code == "acme_readiness" {
			return catalog.Metric{Code: code, Unit: "score", Kinds: []catalog.Kind{catalog.DailyValue}, Agg: catalog.DailySummary, ProviderScoped: true}, true
		}
		return catalog.Lookup(code)
	}
	r := &Rule{Schema: SchemaV1, Metric: "acme_readiness", Window: RuleWindow{Kind: catalog.WindowLocalDay},
		Groups:   []Group{{ID: "acme", Match: []Selector{{Provider: "acme"}}}, {ID: "relay", Match: []Selector{{Provider: "apple_health"}}}},
		Strategy: Strategy{Op: OpMean}}
	err := validate(r, lookup)
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Errors) != 1 || ve.Errors[0].Pointer != "/strategy/op" || !strings.Contains(ve.Errors[0].Detail, "never pooled") {
		t.Fatalf("mean on a provider-scoped score: %v", err)
	}
	r.Strategy = Strategy{Op: OpFirstAvailable}
	if err := validate(r, lookup); err != nil {
		t.Fatalf("first_available on a provider-scoped score: %v", err)
	}
}

func TestValidateSet(t *testing.T) {
	rule := func(metric, follow string, kind catalog.Window) *Rule {
		return &Rule{Schema: SchemaV1, Metric: metric, Window: RuleWindow{Kind: kind}, Follow: follow,
			Groups: []Group{{ID: "watch", Match: []Selector{{DeviceType: "watch"}}}}, Strategy: Strategy{Op: OpFirstAvailable}}
	}
	day, hour := catalog.WindowLocalDay, catalog.WindowHour
	cases := []struct {
		name   string
		rules  []*Rule
		ptr    string
		detail string
	}{
		{"ok chain", []*Rule{rule("active_energy", "", day), rule("steps", "active_energy", day), rule("distance_walk_run", "steps", day)}, "", ""},
		{"cycle", []*Rule{rule("steps", "distance_walk_run", day), rule("distance_walk_run", "steps", day)}, "/0/follow", "follow cycle: steps -> distance_walk_run -> steps"},
		{"three-step cycle", []*Rule{rule("steps", "active_energy", day), rule("active_energy", "distance_walk_run", day), rule("distance_walk_run", "steps", day)}, "/1/follow", "follow cycle"},
		{"leader window differs", []*Rule{rule("active_energy", "", hour), rule("steps", "active_energy", day)}, "/1/follow", "same window"},
		{"duplicate metric", []*Rule{rule("steps", "", day), rule("steps", "", hour)}, "/1/metric", "already covers"},
		{"member error is prefixed", []*Rule{rule("steps", "", day), rule("sleep_rem", "", catalog.WindowLocalNight)}, "/1/metric", "sleep-family"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSet(tc.rules)
			if tc.ptr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("ValidateSet = %v", err)
			}
			for _, fe := range ve.Errors {
				if fe.Pointer == tc.ptr && strings.Contains(fe.Detail, tc.detail) {
					return
				}
			}
			t.Errorf("errors %v, want %q with %q", ve.Errors, tc.ptr, tc.detail)
		})
	}
}

func TestDurationAndAnchor(t *testing.T) {
	for in, want := range map[Duration]time.Duration{"5m": 5 * time.Minute, "36h": 36 * time.Hour, "30d": 30 * 24 * time.Hour,
		"90s": 90 * time.Second, "": 0, "0m": 0, "05m": 0, "1.5h": 0, "5w": 0, "m": 0, "-5m": 0} {
		if got := in.Std(); got != want {
			t.Errorf("Duration(%q).Std() = %v, want %v", in, got, want)
		}
	}
	r := &Rule{}
	if r.NightAnchor() != 18*time.Hour {
		t.Errorf("default anchor = %v", r.NightAnchor())
	}
	r.Quality = &Quality{Sleep: &SleepQuality{NightAnchor: "20:30"}}
	if r.NightAnchor() != 20*time.Hour+30*time.Minute {
		t.Errorf("anchor = %v", r.NightAnchor())
	}
}

func TestWarningsKnown(t *testing.T) {
	for _, w := range []Warning{WarnCrossSourceSum, WarnCompositeExceeds, WarnInsufficientSources, WarnPreferredUnavailable, WarnDefinitionChanged, WarnFollowUnavailable} {
		if !w.Known() {
			t.Errorf("%s not in the catalogue", w)
		}
	}
	if Warning("nope").Known() {
		t.Error("unknown warning reported as known")
	}
}
