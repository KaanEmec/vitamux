// Command vitamux is the single Vitamux binary (ADR-0002). Modes are subcommands.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/KaanEmec/vitamux/internal/api"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/backup"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/connectors/withings"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/documents"
	"github.com/KaanEmec/vitamux/internal/documents/extract"
	"github.com/KaanEmec/vitamux/internal/export"
	"github.com/KaanEmec/vitamux/internal/jobs"
	"github.com/KaanEmec/vitamux/internal/lifecycle"
	"github.com/KaanEmec/vitamux/internal/metrics"
	"github.com/KaanEmec/vitamux/internal/normalize"
	"github.com/KaanEmec/vitamux/internal/obs"
	"github.com/KaanEmec/vitamux/internal/version"
	"github.com/KaanEmec/vitamux/web"
)

const usage = `vitamux — self-hosted personal health data aggregator

Usage: vitamux <command>

Commands:
  serve     run HTTP server, scheduler and workers
  healthcheck  probe the local /readyz (container healthcheck; exit 0 when ready)
  migrate   up | status | down-to VERSION (development only)
  admin     administrative tasks (E03)
  keys      rotate (re-seal values under the current master key)
  reprocess re-normalize stored raw payloads after a normalizer change
  import    ndjson [--merge] EXPORT (load a Vitamux export zip)
  backup    [--out DIR|-] (database dump, blobs and manifest; default VITAMUX_BACKUP_DIR)
  restore   --from DIR (into an empty database and data dir, then migrate up)
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
			version.Version, version.Commit, db.ExpectedVersion(), web.Embedded)
		return 0
	case "serve":
		return serve(stderr)
	case "healthcheck":
		return healthcheck(stderr)
	case "migrate":
		return migrate(args[1:], stdout, stderr)
	case "admin":
		return admin(args[1:], stdout, stderr)
	case "keys":
		return keys(args[1:], stdout, stderr)
	case "reprocess":
		return reprocess(args[1:], stdout, stderr)
	case "import":
		return importCmd(args[1:], stdout, stderr)
	case "backup":
		return backupCmd(args[1:], stdout, stderr)
	case "restore":
		return restoreCmd(args[1:], stdout, stderr)
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, cfg.DatabaseURL.Value(), db.AppRole)
	if err != nil {
		log.Error("database", "err", err)
		return 1
	}
	defer pool.Close()
	if err := db.CheckSchema(ctx, pool); err != nil {
		log.Error("schema check", "err", err)
		return 1
	}

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		log.Warn("create data dir", "err", err) // /readyz reports it as not writable
	}
	var keys *crypto.Keyring
	keyErr := errors.New("VITAMUX_MASTER_KEY_FILE is not set (create one with `vitamux admin init-secrets`)")
	if cfg.MasterKeyFile != "" {
		keys, keyErr = crypto.Load(cfg.MasterKeyFile, cfg.PreviousMasterKeyFiles...)
	}
	var authSvc *auth.Service // nil without a key: sign-in fails closed
	if keyErr == nil {
		authSvc, keyErr = auth.New(db.New(pool), keys)
	}
	if keyErr != nil {
		log.Error("master key", "err", keyErr) // /readyz reports it as not loaded
	}
	runner := jobs.NewRunner(db.New(pool), jobs.Config{Log: log}) // job handlers register on it before Run
	scheduler := jobs.NewScheduler(db.New(pool), log)
	var blobs *blob.Store // nil without a key or data dir; /readyz reports both
	if keys != nil {
		if blobs, err = blob.Open(filepath.Join(cfg.DataDir, "blobs"), keys); err != nil {
			log.Error("blob store", "err", err)
		}
	}
	if blobs != nil {
		runner.Register(blob.KindSweep, blob.SweepJob(db.New(pool), blobs, log))
		scheduler.Daily(blob.KindSweep)
		runner.Register(export.Kind, export.Handler(db.New(pool), blobs))
	}
	if blobs != nil && cfg.BackupDir != "" { // daily backup (docs/operations/backup.md)
		runner.Register(backup.Kind, backup.Job(backup.Options{DatabaseURL: cfg.DatabaseURL.Value(), DB: db.New(pool), DataDir: cfg.DataDir, Keys: keys, Out: cfg.BackupDir}, cfg.BackupKeep, log))
		scheduler.Daily(backup.Kind)
	}
	runner.Register(normalize.KindRecomputeLocalDates, normalize.RecomputeJob(db.New(pool), log))
	lifecycle.Register(runner, scheduler, db.New(pool), log) // daily retention jobs
	documents.Register(runner, scheduler, db.New(pool), blobs, keys, log) // no-op without blobs and key
	// Extraction providers; nil without blobs and key.
	extractSvc, err := extract.Setup(runner, db.New(pool), blobs, keys, log, cfg)
	if err != nil {
		log.Error("extractors", "err", err)
		return 1
	}
	withingsConn := withings.New(withings.Config{ClientID: cfg.WithingsClientID, ClientSecret: cfg.WithingsClientSecret.Value()})
	syncRegistry, err := connectors.NewRegistry(withingsConn) // provider connectors are added as arguments
	if err != nil {
		log.Error("connector registry", "err", err)
		return 1
	}
	var syncRuntime *connectors.Runtime
	var withingsNotify *withings.Notifications
	if blobs != nil { // syncs need the blob store and the master key
		syncRuntime = connectors.New(connectors.Config{DB: db.New(pool), Blobs: blobs, Keys: keys, Registry: syncRegistry, Log: log, PublicURL: cfg.PublicURL})
		runner.Register(jobs.KindSync, syncRuntime.Handle)
		runner.Register(connectors.KindBackfillUnit, syncRuntime.HandleBackfillUnit)
		withingsNotify = withings.NewNotifications(db.New(pool), syncRuntime, withingsConn, cfg.PublicURL, log)
		syncRuntime.OnAuthorized(withingsNotify.Authorized)
	}
	if err := registerNormalizeJobs(runner, db.New(pool), blobs, log); err != nil {
		log.Error("normalizers", "err", err)
		return 1
	}
	handler, err := api.NewHandler(log, web.Assets(), api.Options{
		HSTS:           cfg.PublicURL.Scheme == "https",
		TrustedProxies: cfg.TrustedProxies,
		Auth:           authSvc,
		Development:    cfg.Env == config.Development,
		DB:             db.New(pool),
		Blobs:          blobs,
		Keys:           keys,
		Connectors:     syncRuntime,
		Withings:       withingsNotify,
		Extract:        extractSvc,
		ReadyChecks: []api.ReadyCheck{
			{Name: "database", Check: pool.Ping},
			{Name: "schema", Check: func(ctx context.Context) error { return db.CheckSchema(ctx, pool) }},
			{Name: "data_dir", Check: func(context.Context) error { return dirWritable(cfg.DataDir) }},
			{Name: "master_key", Check: func(context.Context) error { return keyErr }},
			{Name: "workers", Check: runner.Ready},
		},
	})
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

	var background sync.WaitGroup
	if cfg.MetricsAddr != "" { // private listener; stops with ctx like the workers
		ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.MetricsAddr)
		if err != nil {
			log.Error("metrics listener", "err", err)
			return 1
		}
		background.Go(func() { metrics.Serve(ctx, ln, db.New(pool), log) })
	}
	background.Go(func() { runner.Run(ctx) })
	background.Go(func() { scheduler.Run(ctx) })
	defer func() { stop(); background.Wait() }() // SIGTERM drain: finish or release jobs before the pool closes

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

// dirWritable proves the directory accepts new files by creating and removing one.
func dirWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".ready-*")
	if err != nil {
		return err
	}
	return errors.Join(f.Close(), os.Remove(f.Name()))
}

func migrate(args []string, stdout, stderr io.Writer) int {
	const use = "usage: vitamux migrate up | status | down-to VERSION\n"
	var target int64
	switch {
	case len(args) == 1 && (args[0] == "up" || args[0] == "status"):
	case len(args) == 2 && args[0] == "down-to":
		v, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || v < 0 {
			fmt.Fprint(stderr, use)
			return 2
		}
		target = v
	default:
		fmt.Fprint(stderr, use)
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "configuration error:\n%v\n", err)
		return 1
	}
	if args[0] == "down-to" && cfg.Env != config.Development {
		fmt.Fprintln(stderr, "migrate down-to is only allowed with VITAMUX_ENV=development")
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, cfg.MigrateDatabaseURL.Value(), db.OwnerRole)
	if err != nil {
		fmt.Fprintf(stderr, "database: %v\n", err)
		return 1
	}
	defer pool.Close()
	m, err := db.NewMigrator(pool)
	if err != nil {
		fmt.Fprintf(stderr, "migrator: %v\n", err)
		return 1
	}

	switch args[0] {
	case "status":
		st, err := m.Status(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "status: %v\n", err)
			return 1
		}
		for _, s := range st {
			fmt.Fprintf(stdout, "%05d  %-8s  %s\n", s.Source.Version, s.State, filepath.Base(s.Source.Path))
		}
	case "up", "down-to":
		res, err := m.Up(ctx)
		if args[0] == "down-to" {
			res, err = m.DownTo(ctx, target)
		}
		for _, r := range res {
			fmt.Fprintf(stdout, "%-4s %05d  %s (%s)\n", r.Direction, r.Source.Version, filepath.Base(r.Source.Path), r.Duration.Round(time.Millisecond))
		}
		if err != nil {
			fmt.Fprintf(stderr, "migrate %s: %v\n", args[0], err)
			return 1
		}
		if len(res) == 0 {
			fmt.Fprintf(stdout, "schema already at version %d\n", db.ExpectedVersion())
		}
	}
	return 0
}
