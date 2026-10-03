package blob

import (
	"context"
	"log/slog"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

// KindSweep is the job kind of the daily blob sweep; jobs.Scheduler.Daily enqueues it.
const KindSweep = "sweep_blobs"

// SweepJob returns the KindSweep handler: Sweep with DefaultGrace, logging what it removed.
func SweepJob(d *db.DB, s *Store, log *slog.Logger) jobs.Handler {
	return func(ctx context.Context, _ jobs.Job) error {
		st, err := Sweep(ctx, d, s, DefaultGrace)
		if err != nil {
			return err
		}
		log.Info("blob sweep", "rows", st.Rows, "files", st.Files)
		return nil
	}
}
