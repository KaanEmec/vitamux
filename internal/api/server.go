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

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/connectors/withings"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/documents/extract"
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
	// Auth authenticates sessions and tokens. Nil (no master key) fails closed: login
	// answers 503 and every protected route 401.
	Auth *auth.Service
	// Development lets the session cookie drop Secure on plain-http requests.
	Development bool
	// DB and Blobs back the ingest endpoints, which answer 503 while either is nil.
	DB    *db.DB
	Blobs *blob.Store
	// Keys signs pagination cursors. Nil uses a per-process key.
	Keys *crypto.Keyring
	// Connectors runs OAuth connection flows; nil answers 503.
	Connectors *connectors.Runtime
	// Withings handles Withings notification callbacks; nil answers 404.
	Withings *withings.Notifications
	// Extract starts and lists document extractions; nil answers 503.
	Extract *extract.Service
}

// NewHandler builds the root HTTP handler: the route table behind the middleware chain.
// The rest of the API surface is added in E10.
func NewHandler(log *slog.Logger, ui fs.FS, opts Options) (http.Handler, error) {
	rt, err := newRouter(log, ui, opts)
	if err != nil {
		return nil, err
	}
	return rt.handler(), nil
}

// router is the route table. API routes are added with handle, which records who may
// call them.
type router struct {
	log    *slog.Logger
	opts   Options
	mux    *http.ServeMux
	routes []route
	ops    *oapi.ServerInterfaceWrapper // generated owner operations (owner.go)
}

func newRouter(log *slog.Logger, ui fs.FS, opts Options) (*router, error) {
	uh, err := newUIHandler(ui)
	if err != nil {
		return nil, err
	}
	rt := &router{log: log, opts: opts, mux: http.NewServeMux()}
	if rt.ops, err = newOwnerOps(rt); err != nil {
		return nil, err
	}
	rt.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(log, w, http.StatusOK, map[string]string{"status": "ok", "version": version.Version})
	})
	rt.mux.HandleFunc("GET /readyz", readyz(log, opts.ReadyChecks))
	rt.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { // never fall through to the SPA
		writeProblem(w, r, CodeNotFound, "no such API endpoint")
	})
	rt.mux.Handle("/", uh)
	rt.authRoutes()
	rt.systemRoutes()
	rt.ingestRoutes()
	rt.oauthRoutes()
	rt.sourceRoutes()
	rt.webhookRoutes()
	rt.exportRoutes()
	rt.documentRoutes()
	rt.extractionRoutes()
	rt.analyteRoutes()
	rt.ruleRoutes()
	rt.connectionRoutes()
	rt.configRoutes()
	rt.manualRoutes()
	return rt, nil
}

func (rt *router) handler() http.Handler { return middleware(rt.log, rt.opts, rt.mux) }

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
