package withings

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// meastypes maps Withings measure types to catalogue codes and the unit Withings reports
// (the W column of docs/metrics.md). 11 is bp_pulse inside a blood-pressure group and
// heart_rate elsewhere. The catalogue decides which codes form a group.
var meastypes = map[int]struct{ metric, unit string }{
	1: {"weight", "kg"}, 4: {"height", "m"}, 5: {"fat_free_mass", "kg"}, 6: {"body_fat_ratio", "%"},
	8: {"fat_mass", "kg"}, 9: {"bp_diastolic", "mmHg"}, 10: {"bp_systolic", "mmHg"}, 11: {"heart_rate", "bpm"},
	12: {"body_temperature", "°C"}, 54: {"spo2", "%"}, 71: {"body_temperature", "°C"}, 73: {"skin_temperature", "°C"},
	76: {"muscle_mass", "kg"}, 77: {"hydration", "kg"}, 88: {"bone_mass", "kg"}, 91: {"pulse_wave_velocity", "m/s"},
	123: {"vo2max", "mL/kg/min"}, 155: {"vascular_age", "years"}, 168: {"extracellular_water", "kg"},
	169: {"intracellular_water", "kg"}, 170: {"visceral_fat_index", "index"}, 226: {"basal_metabolic_rate", "kcal/day"},
}

// groupKinds orders the canonical groups one measure group can produce.
var groupKinds = []string{"bp_reading", "body_composition"}

// Normalizer turns one withings.measures record (docs/providers/withings.md#how-vitamux-syncs)
// into canonical rows: blood pressure and body composition groups, other types as samples.
type Normalizer struct{}

func (Normalizer) ID() string                    { return StreamMeasures }
func (Normalizer) Version() int                  { return 1 }
func (Normalizer) Accepts(stream, _ string) bool { return stream == StreamMeasures }

type rawGroup struct {
	GrpID        int64  `json:"grpid"`
	Attrib       int    `json:"attrib"`
	Date         int64  `json:"date"`
	Category     int    `json:"category"`
	DeviceID     string `json:"deviceid"`
	HashDeviceID string `json:"hash_deviceid"`
	Model        string `json:"model"`
	ModelID      int    `json:"model_id"`
	Measures     []struct {
		Value int64 `json:"value"`
		Type  int   `json:"type"`
		Unit  int   `json:"unit"`
	} `json:"measures"`
}

// Normalize is pure: local dates come from the owner's timezone periods (the record's
// timezone is the account's current zone, kept in raw only), so no zone is set.
func (Normalizer) Normalize(_ context.Context, raw normalize.RawPayload, _ normalize.Env) (normalize.Output, error) {
	var rec struct {
		MeasureGrp *rawGroup `json:"measuregrp"`
	}
	if err := json.Unmarshal(raw.Body, &rec); err != nil {
		return normalize.Output{}, errors.New("withings: undecodable measure group record")
	}
	g := rec.MeasureGrp
	if g == nil || g.GrpID == 0 || g.Date == 0 {
		return normalize.Output{}, errors.New("withings: record without measure group, id or date")
	}
	var out normalize.Output
	if g.Category != 1 {
		out.Warnings = append(out.Warnings, normalize.Warning{Code: "category_skipped", Detail: "category " + strconv.Itoa(g.Category)})
		return out, nil
	}
	at := time.Unix(g.Date, 0).UTC()
	ext := strconv.FormatInt(g.GrpID, 10)
	dev := g.HashDeviceID
	if dev == "" {
		dev = g.DeviceID
	}
	if dev != "" {
		out.Devices = []normalize.Device{{Fingerprint: dev, Type: deviceType(g.ModelID), Manufacturer: "Withings", Model: g.Model}}
	}
	var flags normalize.Flags
	if g.Attrib == 2 || g.Attrib == 4 {
		flags = normalize.FlagManualEntry
	}
	bp := false
	for _, m := range g.Measures {
		bp = bp || m.Type == 9 || m.Type == 10
	}
	byKind := map[string][]normalize.Measurement{}
	seen := map[string]bool{}
	for _, m := range g.Measures {
		t, ok := meastypes[m.Type]
		if !ok {
			out.Warnings = append(out.Warnings, normalize.Warning{Code: "unknown_meastype", Detail: "type " + strconv.Itoa(m.Type)})
			continue
		}
		if m.Type == 11 && bp {
			t.metric = "bp_pulse"
		}
		if seen[t.metric] {
			out.Warnings = append(out.Warnings, normalize.Warning{Code: "duplicate_meastype", Detail: "type " + strconv.Itoa(m.Type)})
			continue
		}
		seen[t.metric] = true
		meta, _ := catalog.Lookup(t.metric)
		x := normalize.Measurement{Metric: t.metric, Kind: catalog.Sample, Start: at, Value: decimal(m.Value, m.Unit), Unit: t.unit, Flags: flags}
		if meta.Group == "" {
			x.Device, x.Key = dev, normalize.Key{RecordType: "measuregrp", ExternalID: ext, Component: t.metric}
			out.Measurements = append(out.Measurements, x)
			continue
		}
		byKind[meta.Group] = append(byKind[meta.Group], x)
	}
	gctx, _ := json.Marshal(map[string]int{"attrib": g.Attrib})
	for _, kind := range groupKinds {
		if comps := byKind[kind]; len(comps) > 0 {
			out.Groups = append(out.Groups, normalize.Group{Kind: kind, MeasuredAt: at, Context: gctx, Device: dev,
				Key: normalize.Key{RecordType: "measuregrp", ExternalID: ext, Component: kind}, Components: comps})
		}
	}
	return out, nil
}

// decimal is value×10^unit. Dividing by an exact power of ten gives the double nearest to the
// decimal (multiplying by 10^-n would not).
func decimal(v int64, unit int) float64 {
	if unit >= 0 {
		return float64(v) * math.Pow10(unit)
	}
	return float64(v) / math.Pow10(-unit)
}

// deviceType maps Withings model_id ranges (OpenAPI measuregrp model_id) to rule device types.
func deviceType(modelID int) string {
	switch {
	case modelID >= 1 && modelID <= 18:
		return "scale"
	case modelID >= 41 && modelID <= 48:
		return "bp_monitor"
	case modelID >= 51 && modelID <= 59, modelID >= 90 && modelID <= 95:
		return "watch"
	case modelID >= 60 && modelID <= 63:
		return "sleep_monitor"
	case modelID == 70 || modelID == 71:
		return "thermometer"
	}
	return ""
}
