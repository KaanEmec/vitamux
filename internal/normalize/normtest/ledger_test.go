package normtest

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestLeafPaths(t *testing.T) {
	var v any
	if err := json.Unmarshal([]byte(`{"a": 1, "b": {"c": [{"d": null}, {"e": "x"}], "f": []}, "g": {}, "h": [1, 2]}`), &v); err != nil {
		t.Fatal(err)
	}
	got := leafPaths(v, "")
	got = slices.Compact(slices.Sorted(slices.Values(got)))
	want := []string{"a", "b.c[].d", "b.c[].e", "b.f", "g", "h[]"}
	if !slices.Equal(got, want) {
		t.Errorf("paths = %v, want %v", got, want)
	}
}

func TestCheckTarget(t *testing.T) {
	for to, ok := range map[string]bool{
		"heart_rate": true, "bp_systolic, bp_diastolic": true, "hypertension_alert": true,
		"raw: identifier": true, "raw:": false, "raw: ": false, "not_a_code": false, "heart_rate, nope": false,
	} {
		if err := checkTarget(to); (err == nil) != ok {
			t.Errorf("checkTarget(%q) = %v, want ok=%v", to, err, ok)
		}
	}
}
