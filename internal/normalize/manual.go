package normalize

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// ManualStream is the raw stream of values the owner enters (POST /api/v1/measurements/manual).
// Each entry is one raw payload of the owner's provider-manual connection, so a manual row traces
// to its raw record like any other and reprocessing replays it.
const ManualStream = "manual.measurements"

// ManualEntry is the body of a ManualStream payload. End is set for an interval.
type ManualEntry struct {
	Metric string     `json:"metric"`
	Value  float64    `json:"value"`
	Unit   string     `json:"unit"`
	Start  time.Time  `json:"start"`
	End    *time.Time `json:"end,omitempty"`
}

// Manual normalizes ManualStream payloads into one measurement with the manual_entry flag (a
// one-component group for grouped metrics such as weight). The local date follows the offset
// the owner entered the time with.
type Manual struct{}

func (Manual) ID() string                    { return ManualStream }
func (Manual) Version() int                  { return 1 }
func (Manual) Accepts(stream, _ string) bool { return stream == ManualStream }

// Normalize returns the entry's measurement keyed by the raw external key, so one entry is one
// record however often it is replayed.
func (Manual) Normalize(_ context.Context, raw RawPayload, _ Env) (Output, error) {
	var e ManualEntry
	if err := json.Unmarshal(raw.Body, &e); err != nil {
		return Output{}, errors.New("manual entry: malformed body")
	}
	_, off := e.Start.Zone()
	offset := int16(off / 60) //nolint:gosec // validZone bounds it
	m := Measurement{Metric: e.Metric, Kind: catalog.Sample, Start: e.Start, Zone: Zone{OffsetMin: &offset},
		Value: e.Value, Unit: e.Unit, Flags: FlagManualEntry, Key: Key{RecordType: "entry", ExternalID: raw.ExternalKey}}
	if e.End != nil {
		m.Kind, m.End = catalog.Interval, e.End
	}
	if met, ok := catalog.Lookup(e.Metric); ok && met.Group != "" {
		// A grouped metric (weight) is a reading of one component, which takes the group's key.
		m.Key = Key{}
		return Output{Groups: []Group{{Kind: met.Group, MeasuredAt: e.Start, Zone: m.Zone,
			Key: Key{RecordType: "entry", ExternalID: raw.ExternalKey, Component: met.Group}, Components: []Measurement{m}}}}, nil
	}
	return Output{Measurements: []Measurement{m}}, nil
}
