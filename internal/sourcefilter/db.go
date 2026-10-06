package sourcefilter

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Defaults lists the owner's default-ignore patterns as they are now.
func Defaults(ctx context.Context, q *dbq.Queries, user uuid.UUID) ([]Default, error) {
	rows, err := q.ListSourceFilterDefaults(ctx, user)
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := make([]Default, len(rows))
	for i, r := range rows {
		out[i] = Default{Pattern: r.Pattern, Provider: r.Provider, ProviderName: r.ProviderName}
	}
	return out, nil
}

// Build joins a stored filter with the owner's defaults.
func Build(ctx context.Context, q *dbq.Queries, user uuid.UUID, version int32, raw json.RawMessage) (Filter, error) {
	s, err := Parse(raw)
	if err != nil {
		return Filter{}, err
	}
	defs, err := Defaults(ctx, q, user)
	if err != nil {
		return Filter{}, err
	}
	return Filter{Version: version, Choices: s.Origins, Defaults: defs}, nil
}

// ForRaw returns the filter of the paired device that pushed raw payload id; ok is false when
// another kind of client (or a sync) stored it, which no filter applies to.
func ForRaw(ctx context.Context, q *dbq.Queries, id int64) (f Filter, ok bool, err error) {
	r, err := q.GetRawDeviceFilter(ctx, id)
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return Filter{}, false, nil
	} else if err != nil {
		return Filter{}, false, err
	}
	f, err = Build(ctx, q, r.UserID, r.SourceFilterVersion, r.SourceFilter)
	return f, err == nil, err
}

// HealthSource is an app the device found in Apple Health (clients.health_sources).
type HealthSource struct {
	BundleID string       `json:"bundle_id"`
	Name     string       `json:"name,omitempty"`
	Types    []SourceType `json:"types"`
}

// SourceType is a HealthKit type an app wrote, with its newest sample's end when the device read it.
type SourceType struct {
	Type         string     `json:"type"`
	LastSampleAt *time.Time `json:"last_sample_at,omitempty"`
}

// ParseSources reads clients.health_sources.
func ParseSources(raw []byte) ([]HealthSource, error) {
	var out []HealthSource
	if len(raw) == 0 {
		return out, nil
	}
	return out, json.Unmarshal(raw, &out)
}

// Writes maps each reported source to the types it writes.
func Writes(sources []HealthSource) map[string][]string {
	out := make(map[string][]string, len(sources))
	for _, s := range sources {
		for _, t := range s.Types {
			out[s.BundleID] = append(out[s.BundleID], t.Type)
		}
		slices.Sort(out[s.BundleID])
	}
	return out
}
