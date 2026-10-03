package example

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// errUnreadable carries no payload bytes, unlike json's syntax errors.
var errUnreadable = errors.New("example.heart_rate: unreadable sample")

// Normalizer turns one stored sample into a heart_rate measurement.
type Normalizer struct{}

func (Normalizer) ID() string                    { return Stream }
func (Normalizer) Version() int                  { return 1 }
func (Normalizer) Accepts(stream, _ string) bool { return stream == Stream }

// Normalize maps a sample; one without bpm or time is skipped with a warning.
func (Normalizer) Normalize(_ context.Context, raw normalize.RawPayload, _ normalize.Env) (normalize.Output, error) {
	var s struct {
		ID     string    `json:"id"`
		Time   time.Time `json:"time"`
		BPM    *float64  `json:"bpm"`
		Device string    `json:"device"`
	}
	if err := json.Unmarshal(raw.Body, &s); err != nil || s.ID == "" {
		return normalize.Output{}, errUnreadable
	}
	var out normalize.Output
	if s.BPM == nil || s.Time.IsZero() {
		out.Warnings = append(out.Warnings, normalize.Warning{Code: "sample_skipped", Detail: "sample without bpm or time"})
		return out, nil
	}
	if s.Device != "" {
		out.Devices = append(out.Devices, normalize.Device{Fingerprint: s.Device, Type: "watch", Manufacturer: "Example"})
	}
	out.Measurements = append(out.Measurements, normalize.Measurement{
		Metric: "heart_rate", Kind: catalog.Sample, Start: s.Time, Value: *s.BPM, Unit: "bpm",
		Device: s.Device, Key: normalize.Key{RecordType: "sample", ExternalID: s.ID},
	})
	return out, nil
}
