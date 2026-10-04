// Package applehealth holds the Apple Health bridge's server side that is not pairing: the
// healthkit.samples normalizer (docs/architecture/apple-health.md#payload). The iPhone app pushes
// one HKAnchoredObjectQuery page per raw payload; the body contract is schemas/healthkit-samples.v1.json.
package applehealth

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

const (
	Provider      = "apple_health"
	StreamSamples = "healthkit.samples.v1"
	NormalizerID  = "healthkit.samples"

	// sleepGap splits one origin's sleep samples into sessions: a gap longer than this between
	// the end of the samples so far and the next start begins a new session.
	sleepGap = time.Hour

	motionActive = 2 // HKHeartRateMotionContext.active
)

// errUnreadable carries no payload bytes, unlike json's syntax errors.
var errUnreadable = errors.New("healthkit.samples: unreadable page")

// Normalizer turns one healthkit.samples.v1 page into canonical rows: quantities, stand hours,
// sleep sessions, blood-pressure groups, workouts and health events, plus tombstones for deleted
// UUIDs. A type it does not map is a warning; its raw page stays stored for a later version.
type Normalizer struct{}

func (Normalizer) ID() string                    { return NormalizerID }
func (Normalizer) Version() int                  { return 2 }
func (Normalizer) Accepts(stream, _ string) bool { return stream == StreamSamples }

type page struct {
	Type    string    `json:"type"`
	Samples []sample  `json:"samples"`
	Deleted []deleted `json:"deleted"`
}

type deleted struct {
	UUID string `json:"uuid"`
}

type sample struct {
	UUID           string                     `json:"uuid"`
	Start          time.Time                  `json:"start"`
	End            time.Time                  `json:"end"`
	Value          *float64                   `json:"value"`
	Unit           string                     `json:"unit"`
	Source         source                     `json:"source_revision"`
	Device         *hkDevice                  `json:"device"`
	Metadata       map[string]json.RawMessage `json:"metadata"`
	WasUserEntered bool                       `json:"was_user_entered"`
	Objects        []member                   `json:"objects"`
	Workout        *workoutInfo               `json:"workout"`
}

type workoutInfo struct {
	ActivityType int                `json:"activity_type"`
	DurationS    float64            `json:"duration_s"`
	Totals       map[string]float64 `json:"totals"`
}

// member is a correlation member. Type is optional: v1 senders omit it (see bp).
type member struct {
	Type  string   `json:"type"`
	Value *float64 `json:"value"`
	Unit  string   `json:"unit"`
}

type source struct {
	BundleID    string `json:"bundle_id"`
	Name        string `json:"name"`
	ProductType string `json:"product_type"`
}

type hkDevice struct {
	Name            string `json:"name"`
	Manufacturer    string `json:"manufacturer"`
	Model           string `json:"model"`
	HardwareVersion string `json:"hardware_version"`
	SoftwareVersion string `json:"software_version"`
}

// builder collects one page's output.
type builder struct {
	typ     string
	out     normalize.Output
	devices map[string]normalize.Device
	origins map[string]normalize.Origin
}

// Normalize is pure. Local dates come from HKTimeZone when the sample has one, else
// from the owner's timezone periods: without that key the timestamp offset is only the phone's
// zone at upload time, not where the sample was recorded.
func (Normalizer) Normalize(_ context.Context, raw normalize.RawPayload, _ normalize.Env) (normalize.Output, error) {
	var p page
	if err := json.Unmarshal(raw.Body, &p); err != nil || p.Type == "" {
		return normalize.Output{}, errUnreadable
	}
	return normalizePage(p), nil
}

// normalizePage maps one page of samples of p.Type; the export importer shares it (export.go).
func normalizePage(p page) normalize.Output {
	b := &builder{typ: p.Type, devices: map[string]normalize.Device{}, origins: map[string]normalize.Origin{}}
	q, isQuantity := quantities[p.Type]
	ev, isEvent := events[p.Type]
	switch {
	case isQuantity:
		for _, s := range p.Samples {
			b.quantity(s, q)
		}
	case isEvent:
		for _, s := range p.Samples {
			b.event(s, ev)
		}
	case p.Type == typeStandHour:
		for _, s := range p.Samples {
			b.standHour(s)
		}
	case p.Type == typeSleep:
		b.sleep(p.Samples)
	case p.Type == typeBloodPressure:
		for _, s := range p.Samples {
			b.bp(s)
		}
	case p.Type == typeWorkout:
		for _, s := range p.Samples {
			b.workout(s)
		}
	default:
		if len(p.Samples) > 0 {
			b.warn("unmapped_type", p.Type)
		}
	}
	for _, d := range p.Deleted {
		if d.UUID == "" {
			b.warn("deleted_without_uuid", p.Type)
			continue
		}
		b.out.Tombstones = append(b.out.Tombstones, normalize.Key{RecordType: p.Type, ExternalID: strings.ToUpper(d.UUID)})
	}
	for _, k := range sortedKeys(b.devices) {
		b.out.Devices = append(b.out.Devices, b.devices[k])
	}
	for _, k := range sortedKeys(b.origins) {
		b.out.Origins = append(b.out.Origins, b.origins[k])
	}
	return b.out
}

func (b *builder) warn(code, detail string) {
	b.out.Warnings = append(b.out.Warnings, normalize.Warning{Code: code, Detail: detail})
}

// key is the record's identity: its HealthKit type and UUID, which deletions name too.
func (b *builder) key(s sample) normalize.Key {
	return normalize.Key{RecordType: b.typ, ExternalID: strings.ToUpper(s.UUID)}
}

// valid reports whether s has a UUID and an end not before its start, warning otherwise.
func (b *builder) valid(s sample) bool {
	switch {
	case s.UUID == "":
		b.warn("sample_without_uuid", b.typ)
	case s.Start.IsZero() || s.End.Before(s.Start):
		b.warn("bad_interval", s.UUID)
	default:
		return true
	}
	return false
}

// source registers the sample's origin and device and returns their keys.
func (b *builder) source(s sample) (device, origin string) {
	if s.Source.BundleID != "" {
		o := b.origins[s.Source.BundleID]
		o.Key, o.Name = s.Source.BundleID, s.Source.Name
		o.Native = o.Native || native(s)
		b.origins[o.Key] = o
		origin = o.Key
	}
	if d := s.Device; d != nil && (d.Name != "" || d.Manufacturer != "" || d.Model != "" || d.HardwareVersion != "") {
		// HKDevice has no stable id. The software version changes with updates, so it is not part of it.
		sum := sha256.Sum256([]byte(strings.Join([]string{d.Name, d.Manufacturer, d.Model, d.HardwareVersion}, "\x00")))
		device = "hk:" + hex.EncodeToString(sum[:16])
		if _, ok := b.devices[device]; !ok {
			b.devices[device] = normalize.Device{Fingerprint: device, Type: deviceType(*d), Manufacturer: d.Manufacturer,
				Model: d.Model, HardwareVersion: d.HardwareVersion, SoftwareVersion: d.SoftwareVersion}
		}
	}
	return device, origin
}

// native: Apple's own per-device sources (com.apple.health.<UUID>), or an Apple app on an Apple device.
func native(s sample) bool {
	id := s.Source.BundleID
	return strings.HasPrefix(id, "com.apple.health.") ||
		strings.HasPrefix(id, "com.apple.") && s.Device != nil && s.Device.Manufacturer == "Apple Inc."
}

func (b *builder) zone(s sample) normalize.Zone {
	var tz string
	if raw, ok := s.Metadata["HKTimeZone"]; ok && json.Unmarshal(raw, &tz) == nil && tz != "" {
		if _, err := time.LoadLocation(tz); err == nil && tz != "Local" {
			return normalize.Zone{TZ: tz}
		}
		b.warn("bad_timezone", s.UUID)
	}
	return normalize.Zone{}
}

func flags(s sample) normalize.Flags {
	var f normalize.Flags
	if s.WasUserEntered {
		f |= normalize.FlagManualEntry
	}
	var motion float64
	if raw, ok := s.Metadata["HKMetadataKeyHeartRateMotionContext"]; ok && json.Unmarshal(raw, &motion) == nil && motion == motionActive {
		f |= normalize.FlagMotionContext
	}
	return f
}

// measurement adds m, as a one-component group when its metric belongs to a group kind
// (HealthKit has no body-composition correlation, so each sample is its own reading).
func (b *builder) measurement(s sample, m normalize.Measurement) {
	m.Device, m.Origin = b.source(s)
	m.Zone, m.Flags = b.zone(s), flags(s)
	if met, _ := catalog.Lookup(m.Metric); met.Group != "" {
		b.out.Groups = append(b.out.Groups, normalize.Group{Kind: met.Group, MeasuredAt: m.Start, Zone: m.Zone,
			Device: m.Device, Origin: m.Origin, Key: b.key(s), Components: []normalize.Measurement{m}})
		return
	}
	m.Key = b.key(s)
	b.out.Measurements = append(b.out.Measurements, m)
}

func (b *builder) quantity(s sample, q quantity) {
	if !b.valid(s) {
		return
	}
	if s.Value == nil || !finite(*s.Value) || s.Unit != q.hkUnit {
		b.warn("unexpected_value_or_unit", s.UUID)
		return
	}
	m := normalize.Measurement{Metric: q.metric, Kind: q.kind, Start: s.Start, Value: *s.Value, Unit: q.unit}
	if q.kind != catalog.Sample {
		m.End = &s.End
	}
	b.measurement(s, m)
}

// category returns the raw category value; ok is false when it is missing or not an integer.
func category(s sample) (int, bool) {
	if s.Value == nil || *s.Value != math.Trunc(*s.Value) || math.Abs(*s.Value) > 1e6 {
		return 0, false
	}
	return int(*s.Value), true
}

func (b *builder) standHour(s sample) {
	if !b.valid(s) {
		return
	}
	c, ok := category(s)
	v, known := standHour[c]
	if !ok || !known {
		b.warn("unknown_category_value", s.UUID)
		return
	}
	b.measurement(s, normalize.Measurement{Metric: "stand_hours", Kind: catalog.Interval, Start: s.Start, End: &s.End, Value: v, Unit: "count"})
}

func (b *builder) event(s sample, ev event) {
	if !b.valid(s) {
		return
	}
	var level string
	if ev.levels != nil {
		c, ok := category(s)
		if level = ev.levels[c]; !ok || level == "" {
			b.warn("unknown_category_value", s.UUID)
			return
		}
	}
	e := normalize.Event{Code: ev.code, Start: s.Start, Level: level, Zone: b.zone(s), Flags: flags(s), Key: b.key(s)}
	if s.End.After(s.Start) {
		e.End = &s.End
	}
	if len(s.Metadata) > 0 {
		e.Context, _ = json.Marshal(s.Metadata) // keys sorted, so the bytes are deterministic
	}
	e.Device, e.Origin = b.source(s)
	b.out.Events = append(b.out.Events, e)
}

// bp maps one blood-pressure correlation to a bp_reading group. v1 members carry no type, and
// HealthKit requires exactly one systolic and one diastolic member, so without types the higher
// pressure is systolic by definition; a heart-rate member (count/min) becomes bp_pulse.
func (b *builder) bp(s sample) {
	if !b.valid(s) {
		return
	}
	var comps []normalize.Measurement
	var pressures []float64
	add := func(metric string, v float64, unit string) {
		comps = append(comps, normalize.Measurement{Metric: metric, Kind: catalog.Sample, Start: s.Start, Value: v, Unit: unit})
	}
	for _, m := range s.Objects {
		switch {
		case m.Value == nil || !finite(*m.Value):
		case m.Type == typeSystolic && m.Unit == "mmHg":
			add("bp_systolic", *m.Value, "mmHg")
			continue
		case m.Type == typeDiastolic && m.Unit == "mmHg":
			add("bp_diastolic", *m.Value, "mmHg")
			continue
		case m.Type == "" && m.Unit == "mmHg":
			pressures = append(pressures, *m.Value)
			continue
		case (m.Type == "" || m.Type == typeHeartRate) && m.Unit == "count/min":
			add("bp_pulse", *m.Value, "bpm")
			continue
		}
		b.warn("unexpected_member", s.UUID)
		return
	}
	switch len(pressures) {
	case 0:
	case 2:
		add("bp_systolic", max(pressures[0], pressures[1]), "mmHg")
		add("bp_diastolic", min(pressures[0], pressures[1]), "mmHg")
	default:
		b.warn("unexpected_member", s.UUID)
		return
	}
	n := map[string]int{}
	for _, c := range comps {
		n[c.Metric]++
	}
	if n["bp_systolic"] != 1 || n["bp_diastolic"] != 1 || n["bp_pulse"] > 1 {
		b.warn("unexpected_member", s.UUID)
		return
	}
	slices.SortFunc(comps, func(x, y normalize.Measurement) int { return cmp.Compare(x.Metric, y.Metric) })
	f := flags(s)
	for i := range comps {
		comps[i].Flags = f
	}
	g := normalize.Group{Kind: "bp_reading", MeasuredAt: s.Start, Zone: b.zone(s), Key: b.key(s), Components: comps}
	g.Device, g.Origin = b.source(s)
	b.out.Groups = append(b.out.Groups, g)
}

func (b *builder) workout(s sample) {
	if !b.valid(s) {
		return
	}
	if s.Workout == nil || !s.End.After(s.Start) {
		b.warn("bad_workout", s.UUID)
		return
	}
	sp, provider, known := sport(s.Workout.ActivityType)
	if !known {
		b.warn("unknown_activity_type", provider)
	}
	w := normalize.Workout{Start: s.Start, End: s.End, Zone: b.zone(s), Sport: sp, ProviderSport: provider, Key: b.key(s)}
	for _, id := range workoutDistances {
		if v, ok := s.Workout.Totals[id]; ok && finite(v) {
			w.DistanceM = &v
			break
		}
	}
	if v, ok := s.Workout.Totals[hkQuantity+"ActiveEnergyBurned"]; ok && finite(v) {
		w.EnergyKcal = &v
	}
	w.Device, w.Origin = b.source(s)
	b.out.Workouts = append(b.out.Workouts, w)
}

// sleep groups each origin's samples into sessions: sorted by start, a sample joins the current
// session unless it starts more than sleepGap after the session's end so far. A session is keyed
// by its earliest sample, so deleting that sample deletes the session.
func (b *builder) sleep(samples []sample) {
	byOrigin := map[string][]sample{}
	for _, s := range samples {
		if !b.valid(s) {
			continue
		}
		c, ok := category(s)
		if _, known := sleepStages[c]; !ok || !known || !s.End.After(s.Start) {
			b.warn("unknown_category_value", s.UUID)
			continue
		}
		byOrigin[s.Source.BundleID] = append(byOrigin[s.Source.BundleID], s)
	}
	for _, origin := range sortedKeys(byOrigin) {
		ss := byOrigin[origin]
		slices.SortFunc(ss, func(x, y sample) int {
			return cmp.Or(x.Start.Compare(y.Start), x.End.Compare(y.End), cmp.Compare(x.UUID, y.UUID))
		})
		var cur *normalize.SleepSession
		for _, s := range ss {
			if cur == nil || s.Start.After(cur.End.Add(sleepGap)) {
				b.out.Sleep = append(b.out.Sleep, normalize.SleepSession{Start: s.Start, End: s.End, Zone: b.zone(s), Key: b.key(s)})
				cur = &b.out.Sleep[len(b.out.Sleep)-1]
				cur.Device, cur.Origin = b.source(s)
			}
			c, _ := category(s)
			cur.Stages = append(cur.Stages, normalize.SleepStage{Stage: sleepStages[c], Start: s.Start, End: s.End})
			if s.End.After(cur.End) {
				cur.End = s.End
			}
		}
	}
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
