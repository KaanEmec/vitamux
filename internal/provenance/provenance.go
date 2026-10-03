// Package provenance answers "where did this value come from?" for a canonical row
// (docs/architecture/data-model.md#principles): the row itself, every earlier and later
// version of it, and for each version the raw payload metadata, ingest batch, connection and
// client, normalizer and the fetched, normalized, corrected, superseded and deleted times.
// Raw payload bodies are never returned.
package provenance

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Entity names a canonical table.
type Entity string

const (
	Measurement      Entity = "measurement"
	MeasurementGroup Entity = "measurement_group"
	SleepSession     Entity = "sleep_session"
	Workout          Entity = "workout"
)

// Lineage is the provenance of one canonical row. Earlier and Later are the versions linked to Row
// through superseded_by, each oldest first; both are empty for a row that was never corrected.
type Lineage struct {
	Entity  Entity
	Row     Version
	Earlier []Version
	Later   []Version
}

// Version is one canonical row with where it came from.
type Version struct {
	ID           string          // table id: a number, or a UUID for sleep sessions and workouts
	SupersededBy string          // id of the replacing version; empty on the current one
	Row          json.RawMessage // the row's columns, plus metric/unit codes, device, origin and children

	Provider       string
	ConnectionID   uuid.UUID
	ConnectionMode string
	Client         *Client // pushing client; nil for in-process syncs
	Batch          *Batch  // nil for rows migrated without raw
	Raw            *Raw    // nil for rows migrated without raw
	Normalizer     Normalizer

	FetchedAt    *time.Time // raw payload fetch time
	IngestedAt   time.Time
	NormalizedAt time.Time
	CorrectedAt  *time.Time // when this version replaced its predecessor; nil on the first version
	SupersededAt *time.Time
	DeletedAt    *time.Time
	DeletedBy    *Deletion // the payload that carried the upstream deletion
}

// Raw is a raw payload's metadata. The body stays in the blob store.
type Raw struct {
	ID               int64
	Stream           string
	ExternalKey      string
	Version          int32
	ContentSHA256    string // hex; also the blob key
	ContentType      string
	SizeBytes        int64
	FetchedAt        time.Time
	StoredAt         time.Time
	RequestMeta      json.RawMessage // sanitized request
	ShapeFingerprint string
	Status           string
}

// Batch is the ingest batch that delivered the raw payload.
type Batch struct {
	ID              uuid.UUID
	SourceKind      string // sync, push, import, manual
	MigrationSource string
	IdempotencyKey  string
	ReceivedAt      time.Time
}

// Client is the ingest client that pushed the batch.
type Client struct {
	ID   uuid.UUID
	Kind string
	Name string
}

// Normalizer is the registered normalizer version that produced the row.
type Normalizer struct {
	Name    string
	Version int32
	GitSHA  string
}

// Deletion identifies the raw payload that tombstoned a row.
type Deletion struct {
	RawID     int64
	FetchedAt *time.Time
}

// Trace returns the provenance of one canonical row. id is the row's table id as text. It returns
// db.ErrNotFound when the row does not exist, and an error for an unknown entity.
func Trace(ctx context.Context, d *db.DB, entity Entity, id string) (*Lineage, error) {
	rows, err := chain(ctx, d.Q(), entity, id)
	if err != nil {
		return nil, err
	}
	t := &Lineage{Entity: entity}
	var prev *Version
	found := false
	for _, r := range rows {
		v := version(r)
		if prev != nil && prev.SupersededAt != nil {
			v.CorrectedAt = prev.SupersededAt
		}
		switch {
		case r.Depth < 0:
			t.Earlier = append(t.Earlier, v)
		case r.Depth > 0:
			t.Later = append(t.Later, v)
		default:
			t.Row, found = v, true
		}
		prev = &v
	}
	if !found {
		return nil, fmt.Errorf("provenance: %s %s: %w", entity, id, db.ErrNotFound)
	}
	return t, nil
}

// chainRow is the shared shape of the four generated chain rows.
type chainRow dbq.TraceMeasurementChainRow

func chain(ctx context.Context, q *dbq.Queries, entity Entity, id string) ([]chainRow, error) {
	// A malformed id cannot name a row.
	notFound := fmt.Errorf("provenance: %s %q: %w", entity, id, db.ErrNotFound)
	var rows []chainRow
	var err error
	switch entity {
	case Measurement, MeasurementGroup:
		n, perr := strconv.ParseInt(id, 10, 64)
		if perr != nil {
			return nil, notFound
		}
		if entity == Measurement {
			rows, err = convert(q.TraceMeasurementChain(ctx, n))
		} else {
			rows, err = convert(q.TraceGroupChain(ctx, n))
		}
	case SleepSession, Workout:
		u, perr := uuid.Parse(id)
		if perr != nil {
			return nil, notFound
		}
		if entity == SleepSession {
			rows, err = convert(q.TraceSleepChain(ctx, u))
		} else {
			rows, err = convert(q.TraceWorkoutChain(ctx, u))
		}
	default:
		return nil, fmt.Errorf("provenance: unknown entity %q", entity)
	}
	if err != nil {
		return nil, fmt.Errorf("provenance: trace %s %s: %w", entity, id, db.MapErr(err))
	}
	return rows, nil
}

// convert relies on the four generated row types having identical fields.
func convert[R dbq.TraceMeasurementChainRow | dbq.TraceGroupChainRow | dbq.TraceSleepChainRow | dbq.TraceWorkoutChainRow](rows []R, err error) ([]chainRow, error) {
	out := make([]chainRow, len(rows))
	for i, r := range rows {
		out[i] = chainRow(r)
	}
	return out, err
}

func version(r chainRow) Version {
	v := Version{
		ID: r.ID, SupersededBy: r.SupersededBy, Row: r.Row,
		Provider: r.Provider, ConnectionID: r.ConnectionID, ConnectionMode: r.ConnectionMode,
		Normalizer: Normalizer{Name: r.NormalizerName, Version: r.NormalizerVersion, GitSHA: r.NormalizerGitSha},
		FetchedAt:  r.RawFetchedAt, IngestedAt: r.IngestedAt, NormalizedAt: r.NormalizedAt,
		SupersededAt: r.SupersededAt, DeletedAt: r.DeletedAt,
	}
	if r.RawID != nil { // raw columns are all set or all null; sha, type and size are NOT NULL
		v.Raw = &Raw{
			ID: *r.RawID, Stream: deref(r.RawStream), ExternalKey: deref(r.RawExternalKey), Version: deref(r.RawVersion),
			ContentSHA256: hex.EncodeToString(r.RawContentSha256), ContentType: deref(r.RawContentType),
			SizeBytes: deref(r.RawSizeBytes), FetchedAt: *r.RawFetchedAt, StoredAt: *r.RawStoredAt,
			RequestMeta: r.RawRequestMeta, ShapeFingerprint: deref(r.RawShapeFingerprint), Status: deref(r.RawStatus),
		}
	}
	if r.BatchID != nil {
		v.Batch = &Batch{ID: *r.BatchID, SourceKind: deref(r.BatchSourceKind), MigrationSource: deref(r.BatchMigrationSource),
			IdempotencyKey: deref(r.BatchIdempotencyKey), ReceivedAt: *r.BatchReceivedAt}
	}
	if r.ClientID != nil {
		v.Client = &Client{ID: *r.ClientID, Kind: deref(r.ClientKind), Name: deref(r.ClientName)}
	}
	if r.DeletedByRawID != nil {
		v.DeletedBy = &Deletion{RawID: *r.DeletedByRawID, FetchedAt: r.DeletedByFetchedAt}
	}
	return v
}

func deref[T any](p *T) (v T) {
	if p != nil {
		v = *p
	}
	return v
}
