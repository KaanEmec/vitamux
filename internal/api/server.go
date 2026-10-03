package api

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/KaanEmec/vitamux/internal/version"
)

var zeroTime time.Time

// NewHandler builds the root HTTP handler. Security middleware, readiness and the
// API surface are added in J03.5 and E10.
func NewHandler(log *slog.Logger, ui fs.FS) (http.Handler, error) {
	uh, err := newUIHandler(ui)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": version.Version}); err != nil {
			log.Warn("write healthz", "err", err)
		}
	})
	mux.Handle("/api/", http.NotFoundHandler()) // never fall through to the SPA
	mux.Handle("/", uh)
	return mux, nil
}
