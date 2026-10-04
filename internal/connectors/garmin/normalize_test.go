package garmin

import (
	"slices"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
	"github.com/KaanEmec/vitamux/internal/normalize/normtest"
)

func TestGolden(t *testing.T) {
	for _, n := range Normalizers() {
		t.Run(n.ID(), func(t *testing.T) {
			normtest.Golden(t, n, normalize.RawPayload{Stream: n.ID(), ContentType: "application/json"},
				normalize.Env{Provider: Provider})
		})
	}
}

// TestNormalizersRegister: one normalizer per stream, each accepting only its own stream, so they
// form a valid registry together.
func TestNormalizersRegister(t *testing.T) {
	ns := Normalizers()
	reg, err := normalize.NewRegistry(ns...)
	if err != nil || len(ns) != len(streams) {
		t.Fatalf("registry: %v (%d normalizers)", err, len(ns))
	}
	for s := range streams {
		if n, err := reg.For(s, "fp"); err != nil || n.ID() != s {
			t.Errorf("%s: %v", s, err)
		}
	}
}

// TestFITStaysRaw: a FIT download on garmin.activities normalizes to nothing.
func TestFITStaysRaw(t *testing.T) {
	out, err := Normalizer{StreamActivities}.Normalize(t.Context(),
		normalize.RawPayload{Stream: StreamActivities, ContentType: "application/zip", Body: []byte("PK\x03\x04synthetic")}, normalize.Env{})
	if err != nil || !slices.Equal(out.Measurements, nil) || out.Workouts != nil {
		t.Fatalf("FIT: %+v %v", out, err)
	}
}

// TestScoresAreProviderScoped: the Garmin codes exist and are never pooled across providers.
func TestScoresAreProviderScoped(t *testing.T) {
	for _, code := range []string{"garmin_stress", "garmin_body_battery", "garmin_training_readiness", "garmin_sleep_score"} {
		if m, ok := catalog.Lookup(code); !ok || !m.ProviderScoped || m.Poolable() {
			t.Errorf("%s: missing or poolable", code)
		}
	}
}

// TestTrainingByEndpoint: the stored request endpoint, not the shape, tells the two raw items of
// garmin.training apart. The item below has both shapes' keys: as maxmet it is a VO2max, as
// readiness it lacks a timestamp, and an unknown endpoint is drift.
func TestTrainingByEndpoint(t *testing.T) {
	body := []byte(`{"synthetic": true, "unit": {"date": "2026-06-15"}, "response": [{"generic": {"calendarDate": "2026-06-15", "vo2MaxPreciseValue": 47.3}, "score": 1}]}`)
	for endpoint, vo2max := range map[string]bool{
		"/metrics-service/metrics/maxmet/daily/2026-06-15/2026-06-15":   true,
		"/metrics-service/metrics/trainingreadiness/2026-06-15":         false,
		"/metrics-service/metrics/trainingstatus/aggregated/2026-06-15": false,
	} {
		raw := normalize.RawPayload{Stream: StreamTraining, ContentType: "application/json", Body: body,
			RequestMeta: []byte(`{"endpoint": "` + endpoint + `"}`)}
		out, err := Normalizer{StreamTraining}.Normalize(t.Context(), raw, normalize.Env{})
		got := err == nil && len(out.Measurements) == 1 && out.Measurements[0].Metric == "vo2max"
		if got != vo2max || (!vo2max && err == nil) {
			t.Errorf("%s: %+v %v", endpoint, out, err)
		}
	}
}

// TestVO2maxCalendarDate: the date-only VO2max lands on Garmin's calendar date for owners at
// every offset, including UTC-12 and UTC+14.
func TestVO2maxCalendarDate(t *testing.T) {
	body := []byte(`{"synthetic": true, "unit": {"date": "2026-06-15"}, "response": [{"generic": {"calendarDate": "2026-06-15", "vo2MaxPreciseValue": 47.3}}]}`)
	out, err := Normalizer{StreamTraining}.Normalize(t.Context(), normalize.RawPayload{Stream: StreamTraining, Body: body,
		RequestMeta: []byte(`{"endpoint": "/metrics-service/metrics/maxmet/daily/2026-06-15/2026-06-15"}`)}, normalize.Env{})
	if err != nil || len(out.Measurements) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	m := out.Measurements[0]
	for _, tz := range []string{"Etc/GMT+12", "America/Los_Angeles", "UTC", "Pacific/Auckland", "Pacific/Kiritimati"} {
		l, err := normalize.LocalDate(m.Start, m.Zone, normalize.Timeline{{TZ: tz}})
		if err != nil || l.Date.Format(time.DateOnly) != "2026-06-15" {
			t.Errorf("%s: %v %v", tz, l.Date, err)
		}
	}
}
