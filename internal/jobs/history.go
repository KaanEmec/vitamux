package jobs

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// RunFilter narrows RecentRuns; zero fields match everything.
type RunFilter struct {
	ConnectionID *uuid.UUID
	Kind         string
	Limit        int // default 50, at most 500
}

// Run is one job execution (job_runs row) for the history views. ErrorMessage is sanitized.
type Run struct {
	ID           int64
	JobID        uuid.UUID
	Kind         string
	ConnectionID *uuid.UUID
	Attempt      int32
	StartedAt    time.Time
	FinishedAt   *time.Time
	Outcome      string // empty while running
	ErrorClass   string
	ErrorMessage string
	Stats        json.RawMessage
}

// RecentRuns returns the newest runs first, optionally of one connection and/or kind.
func RecentRuns(ctx context.Context, d *db.DB, f RunFilter) ([]Run, error) {
	p := dbq.ListRecentJobRunsParams{ConnectionID: f.ConnectionID, RowLimit: int32(min(max(f.Limit, 1), 500))}
	if f.Limit == 0 {
		p.RowLimit = 50
	}
	if f.Kind != "" {
		p.Kind = &f.Kind
	}
	rows, err := d.Q().ListRecentJobRuns(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]Run, len(rows))
	for i, r := range rows {
		out[i] = Run{
			ID: r.ID, JobID: r.JobID, Kind: r.Kind, ConnectionID: r.ConnectionID, Attempt: r.Attempt,
			StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Outcome: deref(r.Outcome),
			ErrorClass: deref(r.ErrorClass), ErrorMessage: deref(r.ErrorMessage), Stats: r.Stats,
		}
	}
	return out, nil
}
