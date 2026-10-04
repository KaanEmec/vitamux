package metrics

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestInstrumentSeries(t *testing.T) {
	JobRuns.Reset()
	JobDuration.Reset()
	SyncPages.Reset()
	RawItems.Reset()
	ProviderBlocks.Reset()

	JobRuns.WithLabelValues("sync", "succeeded").Inc()
	JobRuns.WithLabelValues("sync", "dead").Inc()
	JobDuration.WithLabelValues("sync").Observe(0.4)
	SyncPages.WithLabelValues("withings", "withings.measures").Inc()
	RawItems.WithLabelValues("withings", "withings.measures", "stored").Add(3)
	RawItems.WithLabelValues("withings", "withings.measures", "duplicate").Inc()
	ProviderBlocks.WithLabelValues("withings").Inc()

	srv := httptest.NewServer(handler())
	defer srv.Close()
	res := get(t, srv.URL)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	body := string(b)
	for _, want := range []string{
		`vitamux_job_runs_total{kind="sync",outcome="succeeded"} 1`,
		`vitamux_job_runs_total{kind="sync",outcome="dead"} 1`,
		`vitamux_job_duration_seconds_count{kind="sync"} 1`,
		`vitamux_sync_pages_total{provider="withings",stream="withings.measures"} 1`,
		`vitamux_sync_raw_items_total{outcome="stored",provider="withings",stream="withings.measures"} 3`,
		`vitamux_sync_raw_items_total{outcome="duplicate",provider="withings",stream="withings.measures"} 1`,
		`vitamux_provider_rate_limit_blocks_total{provider="withings"} 1`,
		`go_goroutines`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition lacks %q", want)
		}
	}
	if n := testutil.CollectAndCount(JobRuns); n != 2 {
		t.Errorf("job run series = %d, want 2", n)
	}
}

// The private listener answers /metrics only (that route needs a database; see the integration test).
func TestServeOnlyMetrics(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { Serve(ctx, ln, nil, slog.New(slog.DiscardHandler)); close(done) }()

	base := "http://" + ln.Addr().String()
	for path, want := range map[string]int{"/": 404, "/healthz": 404, "/debug/pprof/": 404} {
		res := get(t, base+path)
		_ = res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, res.StatusCode, want)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not stop on context cancel")
	}
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
