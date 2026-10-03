package lifecycle

import (
	"context"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// DeleteConnection removes connection id of user inside the caller's transaction: its canonical
// rows, import records, raw payloads (releasing their blobs), batches, clients, and through the
// cascade its credentials, schedules, cursors, backfills and jobs. Days that lose active rows are
// marked for re-resolution. Devices and origins stay: other connections may share them. It
// returns row counts for the audit event; db.ErrNotFound if user has no such connection. The
// caller makes sure none of the connection's jobs is running.
func DeleteConnection(ctx context.Context, q *dbq.Queries, user, id uuid.UUID) (map[string]int64, error) {
	var sleepCodes []string
	for _, m := range catalog.Metrics() {
		if m.Agg == catalog.SleepDerived {
			sleepCodes = append(sleepCodes, m.Code)
		}
	}
	if err := q.MarkConnectionDirty(ctx, dbq.MarkConnectionDirtyParams{ConnectionID: id, SleepCodes: sleepCodes}); err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	for _, del := range []struct {
		name string
		fn   func(context.Context, uuid.UUID) (int64, error)
	}{
		{"measurements", q.DeleteConnectionMeasurements}, {"groups", q.DeleteConnectionGroups},
		{"sleep_sessions", q.DeleteConnectionSleep}, {"workouts", q.DeleteConnectionWorkouts},
	} {
		n, err := del.fn(ctx, id)
		if err != nil {
			return nil, err
		}
		counts[del.name] = n
	}
	if err := blob.LockShared(ctx, q); err != nil {
		return nil, err
	}
	if err := q.ReleaseConnectionRawBlobs(ctx, id); err != nil {
		return nil, err
	}
	if err := q.DeleteConnectionImports(ctx, id); err != nil {
		return nil, err
	}
	if err := q.DeleteConnectionImportRuns(ctx, &id); err != nil {
		return nil, err
	}
	n, err := q.DeleteConnectionRaw(ctx, id)
	if err != nil {
		return nil, err
	}
	counts["raw_payloads"] = n
	if err := q.DeleteConnectionBatches(ctx, id); err != nil {
		return nil, err
	}
	if err := q.DeleteConnectionClients(ctx, id); err != nil {
		return nil, err
	}
	switch n, err := q.DeleteOwnerConnection(ctx, dbq.DeleteOwnerConnectionParams{ID: id, UserID: user}); {
	case err != nil:
		return nil, err
	case n == 0:
		return nil, db.ErrNotFound
	}
	return counts, nil
}
