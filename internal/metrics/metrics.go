// Package metrics holds the Prometheus series of docs/architecture/reliability.md#health-logs-metrics
// and the private listener that serves them. Instruments are package-level so the job runner
// and connector runtime record with one line; gauges that mirror database state are read at
// scrape time, so they are right after a restart.
package metrics

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/KaanEmec/vitamux/internal/db"
)

// Instruments recorded in place by internal/jobs and internal/connectors.
var (
	// JobRuns counts finished job executions; outcome is the job_runs outcome, or "dead"
	// when the run exhausted or forfeited its retries.
	JobRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vitamux_job_runs_total", Help: "Finished job executions by kind and outcome.",
	}, []string{"kind", "outcome"})
	JobDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "vitamux_job_duration_seconds", Help: "Job execution time by kind.",
		Buckets: []float64{0.05, 0.25, 1, 5, 15, 60, 300, 900},
	}, []string{"kind"})
	// SyncPages counts committed sync pages; RawItems their raw rows by StoreRaw outcome
	// (stored, duplicate, new_version).
	SyncPages = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vitamux_sync_pages_total", Help: "Committed sync pages.",
	}, []string{"provider", "stream"})
	RawItems = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vitamux_sync_raw_items_total", Help: "Raw items of committed sync pages by outcome.",
	}, []string{"provider", "stream", "outcome"})
	// ProviderBlocks counts new provider-wide pauses (a 429 or Retry-After).
	ProviderBlocks = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vitamux_provider_rate_limit_blocks_total", Help: "Provider rate-limit blocks set.",
	}, []string{"provider"})
)

// Handler serves the metrics of this process: the instruments above, the database gauges of
// d, and the Go runtime.
func Handler(d *db.DB) http.Handler { return handler(&stateCollector{db: d}) }

func handler(extra ...prometheus.Collector) http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(JobRuns, JobDuration, SyncPages, RawItems, ProviderBlocks,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	reg.MustRegister(extra...)
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

// Serve answers /metrics on ln, and nothing else, until ctx ends, then shuts down. The
// listener must be private; it is never wired into the public mux.
func Serve(ctx context.Context, ln net.Listener, d *db.DB, log *slog.Logger) {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", Handler(d))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("metrics listening", "addr", ln.Addr().String())
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		log.Error("metrics server", "err", err)
	}
}

var (
	jobStatuses        = []string{"queued", "running", "dead"}
	connectionStatuses = []string{"active", "degraded", "needs_reauth", "paused", "error", "disabled"}

	jobsDesc    = prometheus.NewDesc("vitamux_jobs", "Jobs by kind and status (queued, running, dead).", []string{"kind", "status"}, nil)
	queueDesc   = prometheus.NewDesc("vitamux_job_queue_age_seconds", "Age of the oldest queued job that is due, by kind.", []string{"kind"}, nil)
	connsDesc   = prometheus.NewDesc("vitamux_connections", "Connections by provider and status.", []string{"provider", "status"}, nil)
	successDesc = prometheus.NewDesc("vitamux_connection_last_success_timestamp_seconds", "Unix time of a connection's last successful sync.", []string{"provider", "connection_id"}, nil)
)

// stateCollector reads queue and connection state at scrape time.
type stateCollector struct{ db *db.DB }

func (*stateCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{jobsDesc, queueDesc, connsDesc, successDesc} {
		ch <- d
	}
}

func (c *stateCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q := c.db.Q()

	jobs, err := q.CountJobsByKindStatus(ctx)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(jobsDesc, err)
	}
	counts := map[[2]string]float64{}
	age := map[string]float64{}
	for _, r := range jobs {
		counts[[2]string{r.Kind, r.Status}] = float64(r.N)
		age[r.Kind] = max(age[r.Kind], r.OldestDueSeconds)
	}
	for kind := range age { // zero-fill so a drained queue reads 0 instead of vanishing
		for _, st := range jobStatuses {
			ch <- prometheus.MustNewConstMetric(jobsDesc, prometheus.GaugeValue, counts[[2]string{kind, st}], kind, st)
		}
		ch <- prometheus.MustNewConstMetric(queueDesc, prometheus.GaugeValue, age[kind], kind)
	}

	conns, err := q.ListConnectionMetrics(ctx)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(connsDesc, err)
	}
	byStatus := map[[2]string]float64{}
	providers := map[string]bool{}
	for _, r := range conns {
		byStatus[[2]string{r.Provider, r.Status}]++
		providers[r.Provider] = true
		if r.LastSuccessAt != nil {
			ch <- prometheus.MustNewConstMetric(successDesc, prometheus.GaugeValue,
				float64(r.LastSuccessAt.Unix()), r.Provider, r.ID.String())
		}
	}
	for p := range providers {
		for _, st := range connectionStatuses {
			ch <- prometheus.MustNewConstMetric(connsDesc, prometheus.GaugeValue, byStatus[[2]string{p, st}], p, st)
		}
	}
}
