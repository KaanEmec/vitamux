package normalize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// Normalizer turns one raw payload into canonical records (docs/architecture/connectors.md#normalizer-contract).
// It is pure: the same bytes, Version and Env give identical output, with no clock, randomness,
// network or database. Bump Version on any output-affecting change; the golden tests enforce it.
type Normalizer interface {
	ID() string   // e.g. "withings.measures"; the normalizer_versions name
	Version() int // >= 1
	Accepts(stream, shapeFingerprint string) bool
	Normalize(ctx context.Context, raw RawPayload, env Env) (Output, error)
}

// RawPayload is a stored raw row with its verbatim content.
type RawPayload struct {
	ID          int64
	Stream      string
	ExternalKey string
	ContentType string
	FetchedAt   time.Time
	RequestMeta json.RawMessage // sanitized request (endpoint, params)
	Body        []byte
}

// Env is what a normalizer may know beyond the payload.
type Env struct {
	Provider string // provider code of the connection, e.g. "withings"
}

// Output is everything one payload normalizes into. Records refer to devices and origins by
// Device.Fingerprint and Origin.Key; the writer resolves them to rows.
type Output struct {
	Devices      []Device       `json:"devices,omitempty"`
	Origins      []Origin       `json:"origins,omitempty"`
	Measurements []Measurement  `json:"measurements,omitempty"`
	Groups       []Group        `json:"groups,omitempty"`
	Sleep        []SleepSession `json:"sleep,omitempty"`
	Workouts     []Workout      `json:"workouts,omitempty"`
	Events       []Event        `json:"events,omitempty"`
	Tombstones   []Key          `json:"tombstones,omitempty"` // upstream deletions, by stable id
	Warnings     []Warning      `json:"warnings,omitempty"`
}

// Key is a record's stable upstream identity. With ExternalID set the dedupe key is built from it
// (RecordType required); otherwise the writer uses the record's natural key (see DedupeKey).
// Importers must produce the same Key as the live connector for the same record.
type Key struct {
	RecordType string `json:"record_type,omitempty"`
	ExternalID string `json:"external_id,omitempty"`
	Component  string `json:"component,omitempty"` // e.g. the metric of a group component
}

// Flags is the measurements.quality_flags bitset. The writer sets Implausible and Relayed;
// normalizers set the others.
type Flags int32

const (
	FlagManualEntry Flags = 1 << iota
	FlagMotionContext
	FlagImplausible
	FlagRelayed
	FlagMigratedWithoutRaw
	FlagProratedSource
	FlagCalibrating // WHOOP scored the recovery while still calibrating to the wearer
)

// Device is a physical device as the source describes it. Fingerprint is stable per provider.
type Device struct {
	Fingerprint     string `json:"fingerprint"`
	Type            string `json:"type,omitempty"` // watch, phone, scale, bp_monitor, ring, ...
	Manufacturer    string `json:"manufacturer,omitempty"`
	Model           string `json:"model,omitempty"`
	HardwareVersion string `json:"hardware_version,omitempty"`
	SoftwareVersion string `json:"software_version,omitempty"`
}

// Origin is the app that recorded data inside a transport provider (e.g. a HealthKit bundle id).
type Origin struct {
	Key    string `json:"key"`
	Name   string `json:"name,omitempty"`
	Native bool   `json:"native,omitempty"`
}

// Measurement is one value in its source unit; the writer converts to the canonical unit.
type Measurement struct {
	Metric string       `json:"metric"`
	Kind   catalog.Kind `json:"kind"`
	Start  time.Time    `json:"start"`
	End    *time.Time   `json:"end,omitempty"` // nil exactly for samples
	Zone   Zone         `json:"zone,omitzero"`
	Value  float64      `json:"value"`
	Unit   string       `json:"unit"`
	Flags  Flags        `json:"flags,omitempty"`
	Device string       `json:"device,omitempty"`
	Origin string       `json:"origin,omitempty"`
	Key    Key          `json:"key,omitzero"`
}

// Group is a reading taken together; its components are measurements of metrics in that group.
type Group struct {
	Kind       string          `json:"kind"` // bp_reading, body_composition
	MeasuredAt time.Time       `json:"measured_at"`
	Zone       Zone            `json:"zone,omitzero"`
	Context    json.RawMessage `json:"context,omitempty"`
	Device     string          `json:"device,omitempty"`
	Origin     string          `json:"origin,omitempty"`
	Key        Key             `json:"key,omitzero"`
	Components []Measurement   `json:"components"`
}

// SleepSession is one sleep episode. Totals are provider-reported; nil means sum the stages.
// Latency (seconds in bed before sleep onset) is reported either way; nil means not reported.
type SleepSession struct {
	Start   time.Time    `json:"start"`
	End     time.Time    `json:"end"`
	Zone    Zone         `json:"zone,omitzero"`
	Nap     bool         `json:"nap,omitempty"`
	Stages  []SleepStage `json:"stages,omitempty"`
	Totals  *SleepTotals `json:"totals,omitempty"`
	Latency *int32       `json:"latency_s,omitempty"`
	Device  string       `json:"device,omitempty"`
	Origin  string       `json:"origin,omitempty"`
	Key     Key          `json:"key,omitzero"`
}

// SleepStage is one stage interval; Stage is a sleep_stages.stage value.
type SleepStage struct {
	Stage string    `json:"stage"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// SleepTotals are provider-reported durations in seconds; nil means not reported (never 0).
type SleepTotals struct {
	Asleep  *int32 `json:"asleep_s,omitempty"`
	Deep    *int32 `json:"deep_s,omitempty"`
	Light   *int32 `json:"light_s,omitempty"`
	REM     *int32 `json:"rem_s,omitempty"`
	Awake   *int32 `json:"awake_s,omitempty"`
	Latency *int32 `json:"latency_s,omitempty"`
}

// Workout is one activity with optional laps, sets or intervals (in order).
type Workout struct {
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	Zone          Zone      `json:"zone,omitzero"`
	Sport         string    `json:"sport"`
	ProviderSport string    `json:"provider_sport,omitempty"`
	DistanceM     *float64  `json:"distance_m,omitempty"`
	EnergyKcal    *float64  `json:"energy_kcal,omitempty"`
	AvgHRBpm      *float64  `json:"avg_hr_bpm,omitempty"`
	MaxHRBpm      *float64  `json:"max_hr_bpm,omitempty"`
	FileSHA256    []byte    `json:"file_sha256,omitempty"` // original FIT/GPX blob, already stored
	Device        string    `json:"device,omitempty"`
	Origin        string    `json:"origin,omitempty"`
	Key           Key       `json:"key,omitzero"`
	Segments      []Segment `json:"segments,omitempty"`
}

// Segment is a lap, set or interval of a workout.
type Segment struct {
	Kind  string          `json:"kind"`
	Start time.Time       `json:"start"`
	End   *time.Time      `json:"end,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Event is a typed health event (an alert or result) with an optional level or value; Code is a
// catalogue event code (catalog.LookupEvent). End is nil for an instant.
type Event struct {
	Code    string          `json:"code"`
	Start   time.Time       `json:"start"`
	End     *time.Time      `json:"end,omitempty"`
	Zone    Zone            `json:"zone,omitzero"`
	Value   *float64        `json:"value,omitempty"`
	Level   string          `json:"level,omitempty"`
	Context json.RawMessage `json:"context,omitempty"`
	Flags   Flags           `json:"flags,omitempty"`
	Device  string          `json:"device,omitempty"`
	Origin  string          `json:"origin,omitempty"`
	Key     Key             `json:"key,omitzero"`
}

// Warning is a non-fatal finding (unknown field or type, skipped record). Detail must not carry
// health values or secrets.
type Warning struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// ErrInvalidOutput means a normalizer produced records the canonical schema rejects: a bug in
// the normalizer, not in the data.
var ErrInvalidOutput = errors.New("normalize: invalid output")

var (
	groupKinds   = []string{"bp_reading", "body_composition"}
	stageKinds   = []string{"awake", "light", "deep", "rem", "asleep_unspecified", "in_bed", "unknown", "restless", "out_of_bed"}
	segmentKinds = []string{"lap", "set", "interval"}
)

// Validate checks o against the catalogue and the canonical tables' constraints. The writer
// calls it before touching the database, and the golden harness calls it on every case.
func (o Output) Validate() error {
	devices := map[string]bool{}
	for _, d := range o.Devices {
		if d.Fingerprint == "" {
			return invalid("device without fingerprint")
		}
		devices[d.Fingerprint] = true
	}
	origins := map[string]bool{}
	for _, x := range o.Origins {
		if x.Key == "" {
			return invalid("origin without key")
		}
		origins[x.Key] = true
	}
	src := func(what, device, origin string, k Key) error {
		if device != "" && !devices[device] {
			return invalid("%s: device %q not in Devices", what, device)
		}
		if origin != "" && !origins[origin] {
			return invalid("%s: origin %q not in Origins", what, origin)
		}
		if k.ExternalID != "" && k.RecordType == "" {
			return invalid("%s: external id without record type", what)
		}
		return nil
	}
	for i, m := range o.Measurements {
		if err := validMeasurement(fmt.Sprintf("measurement %d", i), m, ""); err != nil {
			return err
		}
		if err := src(fmt.Sprintf("measurement %d", i), m.Device, m.Origin, m.Key); err != nil {
			return err
		}
	}
	for i, g := range o.Groups {
		what := fmt.Sprintf("group %d", i)
		if !slices.Contains(groupKinds, g.Kind) || g.MeasuredAt.IsZero() || len(g.Components) == 0 {
			return invalid("%s: kind %q, a time and components are required", what, g.Kind)
		}
		if err := validZone(what, g.Zone); err != nil {
			return err
		}
		if len(g.Context) > 0 && !json.Valid(g.Context) {
			return invalid("%s: context is not JSON", what)
		}
		if err := src(what, g.Device, g.Origin, g.Key); err != nil {
			return err
		}
		for j, c := range g.Components {
			cw := fmt.Sprintf("%s component %d", what, j)
			if err := validMeasurement(cw, c, g.Kind); err != nil {
				return err
			}
			if err := src(cw, c.Device, c.Origin, c.Key); err != nil {
				return err
			}
		}
	}
	for i, s := range o.Sleep {
		what := fmt.Sprintf("sleep %d", i)
		if !s.End.After(s.Start) || s.Start.IsZero() {
			return invalid("%s: end must be after start", what)
		}
		for _, st := range s.Stages {
			if !slices.Contains(stageKinds, st.Stage) || !st.End.After(st.Start) {
				return invalid("%s: bad stage %q", what, st.Stage)
			}
		}
		if err := validZone(what, s.Zone); err != nil {
			return err
		}
		if err := src(what, s.Device, s.Origin, s.Key); err != nil {
			return err
		}
	}
	for i, w := range o.Workouts {
		what := fmt.Sprintf("workout %d", i)
		if !w.End.After(w.Start) || w.Start.IsZero() || w.Sport == "" {
			return invalid("%s: start, a later end and a sport are required", what)
		}
		if len(w.FileSHA256) != 0 && len(w.FileSHA256) != 32 {
			return invalid("%s: file_sha256 must be 32 bytes", what)
		}
		for _, f := range []*float64{w.DistanceM, w.EnergyKcal, w.AvgHRBpm, w.MaxHRBpm} {
			if f != nil && !finite(*f) {
				return invalid("%s: non-finite value", what)
			}
		}
		for _, sg := range w.Segments {
			if !slices.Contains(segmentKinds, sg.Kind) || (sg.End != nil && sg.End.Before(sg.Start)) ||
				(len(sg.Data) > 0 && !json.Valid(sg.Data)) {
				return invalid("%s: bad segment %q", what, sg.Kind)
			}
		}
		if err := validZone(what, w.Zone); err != nil {
			return err
		}
		if err := src(what, w.Device, w.Origin, w.Key); err != nil {
			return err
		}
	}
	for i, e := range o.Events {
		what := fmt.Sprintf("event %d", i)
		ev, ok := catalog.LookupEvent(e.Code)
		switch {
		case !ok:
			return invalid("%s: unknown event %q", what, e.Code)
		case !ev.AllowsLevel(e.Level):
			return invalid("%s: level %q not allowed for %s", what, e.Level, e.Code)
		case e.Start.IsZero() || (e.End != nil && e.End.Before(e.Start)):
			return invalid("%s: start, and an end not before it, are required", what)
		case e.Value != nil && !finite(*e.Value):
			return invalid("%s: non-finite value", what)
		case len(e.Context) > 0 && !json.Valid(e.Context):
			return invalid("%s: context is not JSON", what)
		}
		if err := validZone(what, e.Zone); err != nil {
			return err
		}
		if err := src(what, e.Device, e.Origin, e.Key); err != nil {
			return err
		}
	}
	for i, k := range o.Tombstones {
		if k.RecordType == "" || k.ExternalID == "" {
			return invalid("tombstone %d: record type and external id are required", i)
		}
	}
	return nil
}

// validMeasurement checks one measurement; group is the enclosing group kind, if any.
func validMeasurement(what string, m Measurement, group string) error {
	met, ok := catalog.Lookup(m.Metric)
	if !ok {
		return invalid("%s: unknown metric %q", what, m.Metric)
	}
	if !slices.Contains(met.Kinds, m.Kind) {
		return invalid("%s: kind %q not allowed for %s", what, m.Kind, m.Metric)
	}
	if met.Group != group {
		return invalid("%s: %s belongs to group %q, not %q", what, m.Metric, met.Group, group)
	}
	if m.Start.IsZero() || (m.End == nil) != (m.Kind == catalog.Sample) || (m.End != nil && m.End.Before(m.Start)) {
		return invalid("%s: start/end do not fit kind %s", what, m.Kind)
	}
	if !finite(m.Value) {
		return invalid("%s: non-finite value", what)
	}
	if _, _, err := catalog.ToCanonical(m.Metric, m.Value, m.Unit); err != nil {
		return invalid("%s: %v", what, err)
	}
	return validZone(what, m.Zone)
}

func validZone(what string, z Zone) error {
	if z.OffsetMin != nil && (*z.OffsetMin < -maxOffsetMin || *z.OffsetMin > maxOffsetMin) {
		return invalid("%s: offset out of range", what)
	}
	if z.TZ != "" {
		if _, err := location(z.TZ); err != nil {
			return invalid("%s: %v", what, err)
		}
	}
	return nil
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidOutput, fmt.Sprintf(format, args...))
}

// ErrNoNormalizer means no registered normalizer accepts a stream and shape.
var ErrNoNormalizer = errors.New("normalize: no normalizer accepts this payload")

// Registry holds the normalizers of one process. Connectors contribute theirs when the binary
// wires itself up; normalize never imports connectors.
type Registry struct{ list []Normalizer } // sorted by ID

// NewRegistry rejects empty or duplicate IDs and versions below 1.
func NewRegistry(ns ...Normalizer) (*Registry, error) {
	list := slices.Clone(ns)
	sort.Slice(list, func(i, j int) bool { return list[i].ID() < list[j].ID() })
	for i, n := range list {
		if n.ID() == "" || n.Version() < 1 {
			return nil, fmt.Errorf("normalize: normalizer %q needs an ID and a version >= 1", n.ID())
		}
		if i > 0 && list[i-1].ID() == n.ID() {
			return nil, fmt.Errorf("normalize: duplicate normalizer %q", n.ID())
		}
	}
	return &Registry{list}, nil
}

// Get returns the normalizer with this ID.
func (r *Registry) Get(id string) (Normalizer, bool) {
	i := slices.IndexFunc(r.list, func(n Normalizer) bool { return n.ID() == id })
	if i < 0 {
		return nil, false
	}
	return r.list[i], true
}

// For returns the one normalizer that accepts a payload. None is ErrNoNormalizer; more than one
// is a wiring error, since the choice would otherwise depend on registration order.
func (r *Registry) For(stream, shapeFingerprint string) (Normalizer, error) {
	var found Normalizer
	for _, n := range r.list {
		if !n.Accepts(stream, shapeFingerprint) {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("normalize: %q and %q both accept stream %q", found.ID(), n.ID(), stream)
		}
		found = n
	}
	if found == nil {
		return nil, fmt.Errorf("%w: stream %q", ErrNoNormalizer, stream)
	}
	return found, nil
}

// All returns the normalizers sorted by ID.
func (r *Registry) All() []Normalizer { return slices.Clone(r.list) }
