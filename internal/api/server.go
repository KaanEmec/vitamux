package api

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/KaanEmec/vitamux/internal/version"
)

var zeroTime time.Time

// readyTimeout bounds each readiness check so a hung dependency fails the probe quickly.
const readyTimeout = 2 * time.Second

// ReadyCheck is one named readiness probe. Its error text is only logged, never returned.
type ReadyCheck struct {
	Name  string
	Check func(context.Context) error
}

// Options configures the HTTP layer.
type Options struct {
	// HSTS adds Strict-Transport-Security; set it when the public URL is https.
	HSTS bool
	// TrustedProxies are the peers whose X-Forwarded-For/-Proto are believed. Empty trusts none.
	TrustedProxies []netip.Prefix
	ReadyChecks    []ReadyCheck
}

// NewHandler builds the root HTTP handler: the route table behind the middleware chain.
// The API surface is added in E10.
func NewHandler(log *slog.Logger, ui fs.FS, opts Options) (http.Handler, error) {
	uh, err := newUIHandler(ui)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(log, w, http.StatusOK, map[string]string{"status": "ok", "version": version.Version})
	})
	mux.HandleFunc("GET /readyz", readyz(log, opts.ReadyChecks))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { // never fall through to the SPA
		writeProblem(w, r, CodeNotFound, "no such API endpoint")
	})
	mux.Handle("/", uh)
	return middleware(log, opts, mux), nil
}

func writeJSON(log *slog.Logger, w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Warn("write response", "err", err)
	}
}

// readyz runs every check concurrently and answers 200, or 503 if any failed. The body
// names checks only; failure details go to the log.
func readyz(log *slog.Logger, checks []ReadyCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()
		errs := make([]error, len(checks))
		var wg sync.WaitGroup
		for i, c := range checks {
			wg.Go(func() {
				done := make(chan error, 1)
				go func() { done <- c.Check(ctx) }()
				select {
				case errs[i] = <-done:
				case <-ctx.Done():
					errs[i] = ctx.Err()
				}
			})
		}
		wg.Wait()

		results, status := make(map[string]string, len(checks)), "ok"
		for i, c := range checks {
			results[c.Name] = "ok"
			if errs[i] != nil {
				results[c.Name], status = "fail", "unavailable"
				log.WarnContext(r.Context(), "readiness check failed", "check", c.Name, "err", errs[i])
			}
		}
		code := http.StatusOK
		if status != "ok" {
			code = http.StatusServiceUnavailable
		}
		writeJSON(log, w, code, map[string]any{"status": status, "checks": results})
	}
}
