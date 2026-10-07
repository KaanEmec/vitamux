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

// A succeeded and a dead run feed the job metrics.
func TestJobMetrics(t *testing.T) {
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
