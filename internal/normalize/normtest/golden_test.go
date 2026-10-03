package normtest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// sample is a test-only normalizer that proves the harness: readings with ids, one blood
// pressure reading as a group, and deletions.
type sample struct{ version int }

func (s sample) ID() string                    { return "sample.readings" }
func (s sample) Version() int                  { return s.version }
func (s sample) Accepts(stream, _ string) bool { return stream == "sample.readings.v1" }
func (s sample) Normalize(_ context.Context, raw normalize.RawPayload, _ normalize.Env) (normalize.Output, error) {
	var in struct {
		Device   string `json:"device"`
		Readings []struct {
			ID     string    `json:"id"`
			Metric string    `json:"metric"`
			At     time.Time `json:"at"`
			Value  float64   `json:"value"`
			Unit   string    `json:"unit"`
			Offset *int16    `json:"offset_min"`
		} `json:"readings"`
		BP []struct {
			ID  string    `json:"id"`
			At  time.Time `json:"at"`
			Sys float64   `json:"sys"`
			Dia float64   `json:"dia"`
		} `json:"bp"`
		Deleted []string `json:"deleted"`
	}
	if err := json.Unmarshal(raw.Body, &in); err != nil {
		return normalize.Output{}, fmt.Errorf("sample: %w", err)
	}
	var out normalize.Output
	if in.Device != "" {
		out.Devices = []normalize.Device{{Fingerprint: in.Device, Type: "bp_monitor"}}
	}
	for _, r := range in.Readings {
		if _, ok := catalog.Lookup(r.Metric); !ok {
			out.Warnings = append(out.Warnings, normalize.Warning{Code: "unknown_metric", Detail: r.ID})
			continue
		}
		out.Measurements = append(out.Measurements, normalize.Measurement{Metric: r.Metric, Kind: catalog.Sample,
			Start: r.At, Zone: normalize.Zone{OffsetMin: r.Offset}, Value: r.Value, Unit: r.Unit, Device: in.Device,
			Key: normalize.Key{RecordType: "reading", ExternalID: r.ID}})
	}
	for _, b := range in.BP {
		out.Groups = append(out.Groups, normalize.Group{Kind: "bp_reading", MeasuredAt: b.At, Device: in.Device,
			Key: normalize.Key{RecordType: "bp", ExternalID: b.ID},
			Components: []normalize.Measurement{
				{Metric: "bp_systolic", Kind: catalog.Sample, Start: b.At, Value: b.Sys, Unit: "mmHg"},
				{Metric: "bp_diastolic", Kind: catalog.Sample, Start: b.At, Value: b.Dia, Unit: "mmHg"},
			}})
	}
	for _, id := range in.Deleted {
		out.Tombstones = append(out.Tombstones, normalize.Key{RecordType: "reading", ExternalID: id})
	}
	return out, nil
}

func TestGoldenSample(t *testing.T) {
	Golden(t, sample{version: 1}, normalize.RawPayload{Stream: "sample.readings.v1", ContentType: "application/json"},
		normalize.Env{Provider: "manual"})
}

// TestGuard pins the version-bump rule: changed output needs a bump, a bump needs UPDATE_GOLDEN.
func TestGuard(t *testing.T) {
	file := func(version int, value float64) []byte {
		out := normalize.Output{Measurements: []normalize.Measurement{{Metric: "heart_rate", Kind: catalog.Sample,
			Start: time.Date(2026, 6, 15, 7, 0, 0, 0, time.UTC), Value: value, Unit: "bpm"}}}
		b, err := json.MarshalIndent(golden{Synthetic: true, Normalizer: "x", Version: version, Output: &out}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for name, tc := range map[string]struct {
		old, got []byte
		version  int
		update   bool
		write    bool
		err      string
	}{
		"unchanged passes":                     {file(1, 60), file(1, 60), 1, false, false, ""},
		"unchanged with update writes nothing": {file(1, 60), file(1, 60), 1, true, false, ""},
		"changed without bump fails":           {file(1, 60), file(1, 61), 1, false, false, "without a version bump"},
		"update cannot hide a missing bump":    {file(1, 60), file(1, 61), 1, true, false, "without a version bump"},
		"bump without update fails":            {file(1, 60), file(2, 61), 2, false, false, "UPDATE_GOLDEN"},
		"bump with update rewrites":            {file(1, 60), file(2, 61), 2, true, true, ""},
		"bump with same output still rewrites": {file(1, 60), file(2, 60), 2, true, true, ""},
		"downgrade fails":                      {file(3, 60), file(2, 60), 2, true, false, "newer"},
		"missing golden fails":                 {nil, file(1, 60), 1, false, false, "missing"},
		"missing golden is created on update":  {nil, file(1, 60), 1, true, true, ""},
	} {
		write, err := check(tc.old, tc.got, tc.version, tc.update)
		if write != tc.write || (tc.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("%s: write=%v err=%v", name, write, err)
		}
	}
}
