package applehealth

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
	"github.com/KaanEmec/vitamux/internal/normalize/normtest"
)

func TestGoldenSamples(t *testing.T) {
	normtest.Golden(t, Normalizer{}, normalize.RawPayload{Stream: StreamSamples, ContentType: "application/json"},
		normalize.Env{Provider: Provider})
}

func TestGoldenExport(t *testing.T) {
	normtest.Golden(t, ExportNormalizer{}, normalize.RawPayload{Stream: StreamExport, ContentType: "application/json"},
		normalize.Env{Provider: Provider})
}

// TestMappingsFitCatalog: every mapped quantity yields a kind its metric allows, in a unit that
// converts to the canonical one; every event code is in the catalogue with the mapped levels.
func TestMappingsFitCatalog(t *testing.T) {
	for id, q := range quantities {
		m, ok := catalog.Lookup(q.metric)
		if !ok || !slices.Contains(m.Kinds, q.kind) {
			t.Errorf("%s: %s with kind %s is not in the catalogue", id, q.metric, q.kind)
		}
		if _, _, err := catalog.ToCanonical(q.metric, 1, q.unit); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
	for id, ev := range events {
		e, ok := catalog.LookupEvent(ev.code)
		if !ok {
			t.Errorf("%s: event %s is not in the catalogue", id, ev.code)
		}
		for _, l := range ev.levels {
			if !e.AllowsLevel(l) {
				t.Errorf("%s: level %s", id, l)
			}
		}
	}
}

// TestRegistryIsMapped is the drift check against the iPhone side: every type of the type registry
// v1 (apple/HealthBridgeKit Registry.swift) is mapped, quantities in the unit the app reads them in.
func TestRegistryIsMapped(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "apple", "HealthBridgeKit", "Sources", "HealthBridgeHealthKit", "Registry.swift"))
	if err != nil {
		t.Skipf("registry source not available: %v", err)
	}
	qs := regexp.MustCompile(`\bq\("(\w+)", \.\w+, "([^"]+)"\)`).FindAllStringSubmatch(string(src), -1)
	cs := regexp.MustCompile(`\bc\("(\w+)", \.\w+\)`).FindAllStringSubmatch(string(src), -1)
	if len(qs) < 40 || len(cs) < 8 {
		t.Fatalf("parsed %d quantities and %d categories; has Registry.swift changed shape?", len(qs), len(cs))
	}
	for _, m := range qs {
		if q, ok := quantities[hkQuantity+m[1]]; !ok || q.hkUnit != m[2] {
			t.Errorf("quantity %s in %s is not mapped", m[1], m[2])
		}
	}
	for _, m := range cs {
		id := hkCategory + m[1]
		if _, ok := events[id]; !ok && id != typeSleep && id != typeStandHour {
			t.Errorf("category %s is not mapped", m[1])
		}
	}
}

func TestDeviceType(t *testing.T) {
	for want, d := range map[string]hkDevice{
		"watch":       {Name: "Synthetic Watch", Manufacturer: "Apple Inc.", Model: "Watch", HardwareVersion: "Watch7,1"},
		"phone":       {Name: "Synthetic Phone", Model: "iPhone", HardwareVersion: "iPhone17,1"},
		"chest_strap": {Name: "Polar H10 0A1B2C3D", Manufacturer: "Polar Electro Oy"},
		"arm_band":    {Name: "Polar Verity Sense", Manufacturer: "Polar Electro Oy"},
		"ring":        {Name: "Oura Ring", Manufacturer: "Oura"},
		"scale":       {Name: "Body Cardio", Manufacturer: "Withings"},
		"bp_monitor":  {Name: "BPM Connect", Manufacturer: "Withings"},
		"band":        {Name: "WHOOP 4.0"},
		"":            {Name: "Spring Thing"},
	} {
		if got := deviceType(d); got != want {
			t.Errorf("deviceType(%+v) = %q, want %q", d, got, want)
		}
	}
	// "Forerunner" is a watch; the HRM paired with it is a chest strap.
	if deviceType(hkDevice{Name: "Forerunner 965", Manufacturer: "Garmin"}) != "watch" ||
		deviceType(hkDevice{Name: "HRM-Pro Plus", Manufacturer: "Garmin"}) != "chest_strap" {
		t.Error("Garmin watch and strap")
	}
}

func TestSport(t *testing.T) {
	for at, want := range map[int][2]string{
		37: {"running", "running"}, 63: {"high_intensity_interval_training", "highIntensityIntervalTraining"},
		3000: {"other", "other"}, 999: {"other", "hk_activity_999"},
	} {
		if c, p, _ := sport(at); c != want[0] || p != want[1] {
			t.Errorf("sport(%d) = %s, %s", at, c, p)
		}
	}
}
