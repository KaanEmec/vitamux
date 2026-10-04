package withings

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// meastypes maps Withings measure types to catalogue codes and the unit Withings reports
// (the W column of docs/metrics.md). 11 is bp_pulse inside a blood-pressure group and
// heart_rate elsewhere; 12 is a generic temperature, body temperature only from a thermometer.
// 173–175 are segmental: the measure's position names the body part (segments). The catalogue
// decides which codes form a group.
var meastypes = map[int]struct{ metric, unit string }{
	1: {"weight", "kg"}, 4: {"height", "m"}, 5: {"fat_free_mass", "kg"}, 6: {"body_fat_ratio", "%"},
	8: {"fat_mass", "kg"}, 9: {"bp_diastolic", "mmHg"}, 10: {"bp_systolic", "mmHg"}, 11: {"heart_rate", "bpm"},
	12: {"body_temperature", "°C"}, 54: {"spo2", "%"}, 71: {"body_temperature", "°C"}, 73: {"skin_temperature", "°C"},
	76: {"muscle_mass", "kg"}, 77: {"hydration", "kg"}, 88: {"bone_mass", "kg"}, 91: {"pulse_wave_velocity", "m/s"},
	123: {"vo2max", "mL/kg/min"}, 135: {"ecg_qrs", "s"}, 136: {"ecg_pr", "s"}, 137: {"ecg_qt", "s"}, 138: {"ecg_qtc", "s"},
	155: {"vascular_age", "years"}, 167: {"withings_nerve_health_score", "index"}, 168: {"extracellular_water", "kg"},
	169: {"intracellular_water", "kg"}, 170: {"visceral_fat_index", "index"}, 173: {"fat_free_mass", "kg"},
	174: {"fat_mass", "kg"}, 175: {"muscle_mass", "kg"}, 196: {"withings_nerve_response_score", "index"},
	226: {"basal_metabolic_rate", "kcal/day"}, 227: {"withings_metabolic_age", "years"}, 229: {"withings_esc", "µS"},
	147: {"urine_ph", "pH"}, 148: {"urine_specific_gravity", "ratio"}, 151: {"urine_nitrites", "µmol/L"},
	204: {"urine_ketones", "mmol/L"}, 205: {"urine_vitamin_c", "mmol/L"}, 248: {"urine_calcium", "mmol/L"},
	249: {"urine_creatinine", "mmol/L"}, 251: {"urine_calcium_creatinine_ratio", "mmol/mmol"},
}

// segments maps the position of a segmental measure (173–175) to its code suffix.
var segments = map[int]string{2: "_right_arm", 3: "_left_arm", 10: "_left_leg", 11: "_right_leg", 12: "_trunk"}

// afibEvents maps the AFib classification types to their events: the value is a category 0 to 13
// (catalog.AfibCategories), not a quantity.
var afibEvents = map[int]string{130: "afib_ecg_result", 139: "afib_ppg_result"}

// maxMeasureDate is 9999-12-31T23:59:59Z: later instants do not marshal as RFC 3339 and
// eventually overflow timestamptz, so a record dated past it is refused, not written.
const maxMeasureDate = 253402300799

// groupKinds orders the canonical groups one measure group can produce.
var groupKinds = []string{"bp_reading", "body_composition"}

// Normalizers returns the normalizer of every Withings stream.
func Normalizers() []normalize.Normalizer {
	return []normalize.Normalizer{Normalizer{},
		streamNormalizer{StreamActivity, 1, normalizeActivity},
		streamNormalizer{StreamIntraday, 2, normalizeIntraday},
		streamNormalizer{StreamSleep, 1, normalizeSleep}}
}

// streamNormalizer normalizes the activity, intraday and sleep streams
// (docs/providers/withings.md#activity-intraday-and-sleep). Normalize is pure: local days come
// from each record's IANA timezone.
type streamNormalizer struct {
	stream  string
	version int
	fn      func(body []byte, out *normalize.Output) error
}

func (n streamNormalizer) ID() string                    { return n.stream }
func (n streamNormalizer) Version() int                  { return n.version }
func (n streamNormalizer) Accepts(stream, _ string) bool { return stream == n.stream }
func (n streamNormalizer) Normalize(_ context.Context, raw normalize.RawPayload, _ normalize.Env) (normalize.Output, error) {
	var out normalize.Output
	if err := n.fn(raw.Body, &out); err != nil {
		return normalize.Output{}, err
	}
	return out, nil
}

// Normalizer turns one withings.measures record (docs/providers/withings.md#how-vitamux-syncs)
// into canonical rows: blood pressure and body composition groups, other types as samples.
type Normalizer struct{}

func (Normalizer) ID() string                    { return StreamMeasures }
func (Normalizer) Version() int                  { return 3 }
func (Normalizer) Accepts(stream, _ string) bool { return stream == StreamMeasures }

type rawGroup struct {
	GrpID    int64 `json:"grpid"`
	Attrib   int   `json:"attrib"`
	Date     int64 `json:"date"`
	Category int   `json:"category"`
	deviceFields
	Measures []struct {
		Value    int64 `json:"value"`
		Type     int   `json:"type"`
		Unit     int   `json:"unit"`
		Position *int  `json:"position"`
	} `json:"measures"`
}

// deviceFields are the device fields Withings sends with a record; all may be null (manual
// entries). Live responses name the model id modelid, the OpenAPI spec model_id; model is a
// name in measures and may be a number elsewhere.
type deviceFields struct {
	DeviceID     string          `json:"deviceid"`
	HashDeviceID string          `json:"hash_deviceid"`
	Model        json.RawMessage `json:"model"`
	ModelID      *int            `json:"modelid"`
	SpecModelID  *int            `json:"model_id"`
}

// source adds the record's device to out, or its origin when a phone app relayed it (model ids
// 1051–1060; the origin is flagged relayed, so it is never counted twice), and returns the
// device fingerprint, origin key and device type.
func (d deviceFields) source(out *normalize.Output) (dev, origin, typ string) {
	id := d.modelID()
	var name string
	_ = json.Unmarshal(d.Model, &name) // a number or null is no name
	if id >= 1051 && id <= 1060 {
		origin = "relay:model:" + strconv.Itoa(id)
		addOrigin(out, normalize.Origin{Key: origin, Name: name})
	} else {
		typ = deviceType(id, name)
	}
	if dev = d.HashDeviceID; dev == "" {
		dev = d.DeviceID
	}
	if dev != "" && !slices.ContainsFunc(out.Devices, func(x normalize.Device) bool { return x.Fingerprint == dev }) {
		out.Devices = append(out.Devices, normalize.Device{Fingerprint: dev, Type: typ, Manufacturer: "Withings", Model: name})
	}
	return dev, origin, typ
}

// modelID is the model id under either name; 0 when none is sent.
func (d deviceFields) modelID() int {
	switch {
	case d.ModelID != nil:
		return *d.ModelID
	case d.SpecModelID != nil:
		return *d.SpecModelID
	}
	return 0
}

func addOrigin(out *normalize.Output, o normalize.Origin) {
	if !slices.ContainsFunc(out.Origins, func(x normalize.Origin) bool { return x.Key == o.Key }) {
		out.Origins = append(out.Origins, o)
	}
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
	if g == nil || g.GrpID == 0 || g.Date <= 0 || g.Date > maxMeasureDate {
		return normalize.Output{}, errors.New("withings: record without measure group, id or a date in 1970..9999")
	}
	var out normalize.Output
	if g.Category != 1 {
		out.Warnings = append(out.Warnings, normalize.Warning{Code: "category_skipped", Detail: "category " + strconv.Itoa(g.Category)})
		return out, nil
	}
	at := time.Unix(g.Date, 0).UTC()
	ext := strconv.FormatInt(g.GrpID, 10)
	dev, origin, typ := g.source(&out)
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
		warn := func(code string) {
			out.Warnings = append(out.Warnings, normalize.Warning{Code: code, Detail: "type " + strconv.Itoa(m.Type)})
		}
		switch {
		case afibEvents[m.Type] != "":
			code := afibEvents[m.Type]
			cat := int(decimal(m.Value, m.Unit))
			if cat < 0 || cat >= len(catalog.AfibCategories) || seen[code] {
				warn("unknown_afib_category")
				continue
			}
			seen[code] = true
			ectx, _ := json.Marshal(map[string]int{"category": cat})
			out.Events = append(out.Events, normalize.Event{Code: code, Start: at, Level: catalog.AfibCategories[cat], Context: ectx,
				Flags: flags, Device: dev, Origin: origin, Key: normalize.Key{RecordType: "measuregrp", ExternalID: ext, Component: code}})
			continue
		case !ok:
			warn("unknown_meastype")
			continue
		case m.Type == 11 && bp:
			t.metric = "bp_pulse"
		case m.Type == 12 && typ != "thermometer":
			warn("not_body_temperature") // e.g. a scale's room temperature
			continue
		case m.Type >= 173 && m.Type <= 175:
			var seg string
			if m.Position != nil {
				seg = segments[*m.Position]
			}
			if seg == "" {
				warn("unknown_position")
				continue
			}
			t.metric += seg
		}
		if seen[t.metric] {
			out.Warnings = append(out.Warnings, normalize.Warning{Code: "duplicate_meastype", Detail: "type " + strconv.Itoa(m.Type)})
			continue
		}
		seen[t.metric] = true
		meta, _ := catalog.Lookup(t.metric)
		x := normalize.Measurement{Metric: t.metric, Kind: catalog.Sample, Start: at, Value: decimal(m.Value, m.Unit), Unit: t.unit, Flags: flags}
		if meta.Group == "" {
			x.Device, x.Origin, x.Key = dev, origin, normalize.Key{RecordType: "measuregrp", ExternalID: ext, Component: t.metric}
			out.Measurements = append(out.Measurements, x)
			continue
		}
		byKind[meta.Group] = append(byKind[meta.Group], x)
	}
	gctx, _ := json.Marshal(map[string]int{"attrib": g.Attrib})
	for _, kind := range groupKinds {
		if comps := byKind[kind]; len(comps) > 0 {
			out.Groups = append(out.Groups, normalize.Group{Kind: kind, MeasuredAt: at, Context: gctx, Device: dev, Origin: origin,
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

// deviceType maps a Withings model id to a rule device type (docs/providers/withings.md#devices);
// without an id, a known word of the model name decides.
func deviceType(id int, name string) string {
	switch {
	case id >= 1 && id <= 7, id >= 9 && id <= 12, id >= 14 && id <= 16, id == 18:
		return "scale"
	case id == 13, id >= 60 && id <= 63:
		return "under_mattress"
	case id >= 41 && id <= 48:
		return "bp_monitor"
	case id == 51, id == 54, id == 58:
		return "band"
	case id == 52, id == 53, id == 55, id == 59, id >= 90 && id <= 95:
		return "watch"
	case id == 70, id == 71:
		return "thermometer"
	case id != 0:
		return ""
	}
	for _, w := range modelWords {
		if strings.Contains(name, w.word) {
			return w.typ
		}
	}
	return ""
}

var modelWords = []struct{ word, typ string }{
	{"BPM", "bp_monitor"}, {"Sleep", "under_mattress"}, {"Thermo", "thermometer"}, {"ScanWatch", "watch"}, {"Body", "scale"},
}
