//go:build integration

package connectors

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/jobs"
	"github.com/KaanEmec/vitamux/internal/metrics"
)

// Committed pages and their raw outcomes are counted once the transaction commits; a provider
// block is counted when it is set.
func TestSyncMetrics(t *testing.T) {
	count := func(outcome string) float64 {
		return testutil.ToFloat64(metrics.RawItems.WithLabelValues("withings", stream, outcome))
	}
	body := `{"v":1}`
	rateLimited := false
	f := &fake{fetch: func(_ context.Context, _ Conn, _ Credentials, _ WorkUnit, out *RawSink) (FetchResult, error) {
		if rateLimited {
			return FetchResult{}, &RateLimitedError{RetryAfter: time.Minute}
		}
		out.Put(item("k1"))
		changed := item("k2")
		changed.Body = []byte(body)
		out.Put(changed)
		return FetchResult{Done: true, NextCursor: json.RawMessage(`{"n":1}`)}, nil
	}}
	e := setup(t, f)

	pages := testutil.ToFloat64(metrics.SyncPages.WithLabelValues("withings", stream))
	stored, dup, newVer := count(string(ingest.Stored)), count(string(ingest.Duplicate)), count(string(ingest.NewVersion))

	e.drive(t, e.rt, e.enqueue(t, jobs.SyncPayload{}), 1) // two new items
	body = `{"v":2}`
	e.drive(t, e.rt, e.enqueue(t, jobs.SyncPayload{}), 1) // k1 unchanged, k2 changed

	if got := testutil.ToFloat64(metrics.SyncPages.WithLabelValues("withings", stream)) - pages; got != 2 {
		t.Errorf("pages = %v, want 2", got)
	}
	for outcome, want := range map[string]float64{"stored": 2, "duplicate": 1, "new_version": 1} {
		got := count(outcome) - map[string]float64{"stored": stored, "duplicate": dup, "new_version": newVer}[outcome]
		if got != want {
			t.Errorf("%s items = %v, want %v", outcome, got, want)
		}
	}

	blocks := testutil.ToFloat64(metrics.ProviderBlocks.WithLabelValues("withings"))
	rateLimited = true
	e.drive(t, e.rt, e.enqueue(t, jobs.SyncPayload{}), 1)
	if got := testutil.ToFloat64(metrics.ProviderBlocks.WithLabelValues("withings")) - blocks; got != 1 {
		t.Errorf("rate-limit blocks = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.SyncPages.WithLabelValues("withings", stream)) - pages; got != 2 {
		t.Errorf("a rate-limited fetch counted as a page: %v", got)
	}
}
