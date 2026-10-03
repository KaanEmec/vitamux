package backup

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/KaanEmec/vitamux/internal/jobs"
)

// Kind is the daily backup job; serve registers it only when VITAMUX_BACKUP_DIR is set.
const Kind = "backup"

// Job returns the Kind handler: Create into o.Out, then Prune to the newest keep backups.
func Job(o Options, keep int, log *slog.Logger) jobs.Handler {
	return func(ctx context.Context, _ jobs.Job) error {
		dir, m, err := Create(ctx, o)
		if err != nil {
			return err
		}
		removed, err := Prune(o.Out, keep, time.Now())
		if err != nil {
			return err
		}
		log.Info("backup written", "name", filepath.Base(dir), "schema_version", m.SchemaVersion,
			"blobs", m.BlobCount, "db_bytes", m.Files[0].Size, "blob_bytes", m.Files[1].Size, "pruned", len(removed))
		return nil
	}
}
