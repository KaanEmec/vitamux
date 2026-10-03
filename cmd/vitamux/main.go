// Command vitamux is the single Vitamux binary (ADR-0002). Modes are subcommands.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/KaanEmec/vitamux/internal/api"
	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/obs"
	"github.com/KaanEmec/vitamux/internal/version"
	"github.com/KaanEmec/vitamux/web"
)

const usage = `vitamux — self-hosted personal health data aggregator

Usage: vitamux <command>

Commands:
  serve     run HTTP server, scheduler and workers
  migrate   apply database migrations (J02.1)
  admin     administrative tasks (E03)
  version   print version information
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "vitamux %s (commit %s, schema %d, ui embedded: %v)\n",
			version.Version, version.Commit, version.SchemaVersion, web.Embedded)
		return 0
	case "serve":
		return serve(stderr)
	case "migrate", "admin":
		fmt.Fprintf(stderr, "vitamux %s: not implemented yet\n", args[0])
		return 1
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func serve(stderr io.Writer) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "configuration error:\n%v\n", err)
		return 1
	}
	log := obs.NewLogger(stderr, cfg.LogLevel)
	log.Info("starting", "version", version.Version, "config", cfg.String())
	if cfg.Env == config.Development && cfg.DatabaseURL.IsSet() {
		log.Warn("secret supplied as a plain environment variable; use *_FILE outside development")
	}

	handler, err := api.NewHandler(log, web.Assets())
	if err != nil {
		log.Error("build handler", "err", err)
		return 1
	}
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.HTTPAddr)

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			return 1
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("shutdown", "err", err)
			return 1
		}
	}
	return 0
}
