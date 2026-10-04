package lifecycle

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// ErrJobRunning: one of the owner's jobs is running, so Purge would race with it.
var ErrJobRunning = errors.New("lifecycle: a job of this owner is running; stop vitamux serve or wait for it to finish")

// Count is the number of rows Purge removed (or would remove) from one table.
type Count struct {
	Table string
	Rows  int64
}

var errDryRun = errors.New("dry run")

// Purge deletes everything of user in one transaction, in dependency order: canonical rows, import
// records, raw payloads and batches, documents (document keys first: crypto-shredding), exports
// and their jobs, clients, connections (with credentials, schedules, cursors and jobs), devices,
// origins, sessions, API keys, settings, the provider app credentials and panel sidecars it
// entered, and finally the user with rules, overrides and the rest
// of the cascade. Blob references are released; blob.Sweep removes the files. Audit events stay,
// unlinked from the user (they never hold health values), plus one user.purged event with the
// counts. dryRun rolls everything back and only reports the counts. db.ErrNotFound if there is no
// such user; ErrJobRunning while one of its jobs runs.
func Purge(ctx context.Context, d *db.DB, user uuid.UUID, actor string, dryRun bool) ([]Count, error) {
	var out []Count
	err := d.Tx(ctx, func(q *dbq.Queries) error {
		out = nil
		statuses, err := q.LockPurgeJobs(ctx, user)
		if err != nil {
			return err
		}
		if slices.Contains(statuses, "running") {
			return ErrJobRunning
		}
		cascade, err := q.CountPurgeCascade(ctx, user)
		if err != nil {
			return err
		}
		jobIDs, err := q.ListPurgeJobIDs(ctx, user)
		if err != nil {
			return err
		}
		type step struct {
			table string
			fn    func(context.Context, uuid.UUID) (int64, error)
		}
		run := func(steps ...step) error {
			for _, s := range steps {
				n, err := s.fn(ctx, user)
				if err != nil {
					return err
				}
				out = append(out, Count{s.table, n})
			}
			return nil
		}
		if err := run(
			step{"resolution_dirty", q.PurgeResolutionDirty},
			step{"measurements", q.PurgeMeasurements}, // before their groups
			step{"measurement_groups", q.PurgeGroups},
			step{"sleep_sessions", q.PurgeSleep},
			step{"workouts", q.PurgeWorkouts},
			step{"health_events", q.PurgeEvents},
			step{"import_items", q.PurgeImportItems},
			step{"import_runs", q.PurgeImportRuns},
		); err != nil {
			return err
		}
		if err := blob.LockShared(ctx, q); err != nil {
			return err
		}
		if err := q.ReleaseUserBlobs(ctx, user); err != nil {
			return err
		}
		if err := run(
			step{"raw_payloads", q.PurgeRaw},
			step{"ingest_batches", q.PurgeBatches},
			step{"document_keys", q.PurgeDocumentKeys}, // crypto-shred before the rows go
			step{"documents", q.PurgeDocuments},
			step{"exports", q.PurgeExports},
			step{"jobs", func(ctx context.Context, _ uuid.UUID) (int64, error) { return q.PurgeOwnerJobs(ctx, jobIDs) }},
			step{"clients", q.PurgeClients},
			step{"connections", q.PurgeConnections},
			step{"devices", q.PurgeDevices},
			step{"data_origins", q.PurgeOrigins},
			step{"sessions", q.PurgeSessions},
			step{"api_keys", q.PurgeAPIKeys},
			step{"settings", q.PurgeSettings},
			step{"provider_app_credentials", q.PurgeProviderApps},
			step{"sidecars", q.PurgeSidecars},
		); err != nil {
			return err
		}
		n, err := q.PurgeUser(ctx, user)
		if err != nil {
			return err
		}
		if n == 0 {
			return db.ErrNotFound
		}
		out = append(out, Count{"resolution_rules", cascade.Rules}, Count{"manual_overrides", cascade.Overrides}, Count{"users", n})
		detail := map[string]any{}
		for _, c := range out {
			detail[c.Table] = c.Rows
		}
		if err := audit.Record(ctx, q, audit.Event{Actor: actor, Action: "user.purged", TargetType: "user", TargetID: user.String(), Detail: detail}); err != nil {
			return err
		}
		if dryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		err = nil
	}
	return out, err
}
