//go:build integration

package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/KaanEmec/vitamux/internal/metrics"
)

// A succeeded and a dead run feed the history queries and the job metrics.
func TestHistoryAndMetrics(t *testing.T) {
	d, pool := setup(t)
	conn, other := newConnection(t, pool, "active"), newConnection(t, pool, "active")
	boom := errors.New("provider said no")

	r := NewRunner(d, fast(Config{Workers: 1}))
	r.Register("sweep_blobs", func(context.Context, Job) error { return nil })
	r.Register("export", func(context.Context, Job) error { return Permanent(boom) })

	okBefore := testutil.ToFloat64(metrics.JobRuns.WithLabelValues("sweep_blobs", "succeeded"))
	deadBefore := testutil.ToFloat64(metrics.JobRuns.WithLabelValues("export", "dead"))
	obsBefore := testutil.CollectAndCount(metrics.JobDuration)

	okJob := enqueue(t, d, NewJob{Kind: "sweep_blobs", ConnectionID: &conn})
	badJob := enqueue(t, d, NewJob{Kind: "export", ConnectionID: &conn})
	otherJob := enqueue(t, d, NewJob{Kind: "sweep_blobs", ConnectionID: &other})
	stop := start(r)
	waitFor(t, 5*time.Second, "jobs to finish", func() bool {
		return job(t, d, okJob).Status == "succeeded" && job(t, d, badJob).Status == "dead" &&
			job(t, d, otherJob).Status == "succeeded"
	})
	stop()

	ctx := context.Background()
	all, err := RecentRuns(ctx, d, RunFilter{ConnectionID: &conn})
	if err != nil || len(all) != 2 {
		t.Fatalf("runs of the connection: %d, %v", len(all), err)
	}
	if all[0].ID < all[1].ID {
		t.Error("runs must be newest first")
	}
	dead, err := RecentRuns(ctx, d, RunFilter{ConnectionID: &conn, Kind: "export"})
	if err != nil || len(dead) != 1 {
		t.Fatalf("export runs: %d, %v", len(dead), err)
	}
	if got := dead[0]; got.Outcome != "failed" || got.ErrorClass != "permanent" || got.ErrorMessage != boom.Error() || got.FinishedAt == nil {
		t.Errorf("unexpected run %+v", got)
	}
	if n, _ := RecentRuns(ctx, d, RunFilter{Limit: 2}); len(n) != 2 {
		t.Errorf("limit: got %d runs", len(n))
	}
	if n, _ := RecentRuns(ctx, d, RunFilter{}); len(n) != 3 {
		t.Errorf("unfiltered: got %d runs, want 3", len(n))
	}

	if got := testutil.ToFloat64(metrics.JobRuns.WithLabelValues("sweep_blobs", "succeeded")) - okBefore; got != 2 {
		t.Errorf("succeeded runs counted %v, want 2", got)
	}
	if got := testutil.ToFloat64(metrics.JobRuns.WithLabelValues("export", "dead")) - deadBefore; got != 1 {
		t.Errorf("dead runs counted %v, want 1", got)
	}
	if got := testutil.CollectAndCount(metrics.JobDuration); got < obsBefore || got < 2 {
		t.Errorf("duration series = %d, want one per kind", got)
	}
}
