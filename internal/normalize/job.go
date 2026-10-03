package normalize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

// KindRecomputeLocalDates is the job kind a timezone-period edit enqueues.
const KindRecomputeLocalDates = "recompute_local_dates"

// RecomputePayload is the payload of a recompute_local_dates job. Zero From and To keep their
// meaning from Range (open-ended).
type RecomputePayload struct {
	UserID uuid.UUID `json:"user_id"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
}

// enqueueRecompute adds the recompute job for r to the caller's transaction.
func enqueueRecompute(ctx context.Context, q *dbq.Queries, userID uuid.UUID, r *Range) error {
	if r == nil {
		return nil
	}
	_, _, err := jobs.Enqueue(ctx, q, jobs.NewJob{
		Kind: KindRecomputeLocalDates, Priority: jobs.PriorityLow,
		Payload: RecomputePayload{UserID: userID, From: r.From, To: r.To},
	})
	return err
}

// RecomputeJob returns the KindRecomputeLocalDates handler. Replays are safe (RecomputeLocalDates
// only writes rows whose date changes). Without any timezone period there is nothing to
// resolve against, so the job is not retried.
func RecomputeJob(d *db.DB, log *slog.Logger) jobs.Handler {
	return func(ctx context.Context, j jobs.Job) error {
		var p RecomputePayload
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return jobs.Permanent(fmt.Errorf("recompute payload: %w", err))
		}
		n, err := RecomputeLocalDates(ctx, d, p.UserID, Range{From: p.From, To: p.To})
		if errors.Is(err, ErrNoTimezone) {
			return jobs.Permanent(err)
		}
		if err != nil {
			return err
		}
		log.Info("local dates recomputed", "measurements", n.Measurements, "groups", n.Groups,
			"workouts", n.Workouts, "sleep_sessions", n.SleepSessions)
		return nil
	}
}
