package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/KaanEmec/vitamux/internal/backup"
	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
)

// backupCmd is `vitamux backup [--out DIR|-]` (docs/operations/backup.md).
func backupCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "parent directory of the new backup, or - for a tar on stdout (default VITAMUX_BACKUP_DIR)")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: vitamux backup [--out DIR|-]")
		return 2
	}
	cfg, kr, code := backupConfig(stderr)
	if code != 0 {
		return code
	}
	if *out == "" {
		*out = cfg.BackupDir
	}
	if *out == "" {
		fmt.Fprintln(stderr, "backup: pass --out DIR or set VITAMUX_BACKUP_DIR")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, cfg.DatabaseURL.Value(), db.AppRole)
	if err != nil {
		fmt.Fprintf(stderr, "database: %v\n", err)
		return 1
	}
	defer pool.Close()
	opts := backup.Options{DatabaseURL: cfg.DatabaseURL.Value(), DB: db.New(pool), DataDir: cfg.DataDir, Keys: kr, Out: *out}
	if *out == "-" { // build in TMPDIR, then stream; TMPDIR needs room for the whole backup
		tmp, err := os.MkdirTemp("", "vitamux-backup-")
		if err != nil {
			fmt.Fprintf(stderr, "backup: %v\n", err)
			return 1
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		opts.Out = tmp
	}
	dir, m, err := backup.Create(ctx, opts)
	if err == nil && *out == "-" {
		err = backup.WriteTar(stdout, dir)
		stdout = stderr // the summary must not end up in the tar stream
	}
	if err != nil {
		fmt.Fprintf(stderr, "backup: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "backup written: %s (schema %d, %d blobs, master key id %s)\n", dir, m.SchemaVersion, m.BlobCount, m.KeyID)
	return 0
}

// restoreCmd is `vitamux restore --from DIR`: into an empty database and data directory, then
// the migrations if the backup is older than this binary.
func restoreCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "backup directory (vitamux-<time>)")
	if fs.Parse(args) != nil || fs.NArg() != 0 || *from == "" {
		fmt.Fprintln(stderr, "usage: vitamux restore --from DIR")
		return 2
	}
	cfg, kr, code := backupConfig(stderr)
	if code != 0 {
		return code
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, cfg.MigrateDatabaseURL.Value(), db.OwnerRole)
	if err != nil {
		fmt.Fprintf(stderr, "database: %v\n", err)
		return 1
	}
	defer pool.Close()
	m, err := backup.Restore(ctx, backup.RestoreOptions{
		From: *from, DatabaseURL: cfg.MigrateDatabaseURL.Value(), DB: db.New(pool), DataDir: cfg.DataDir, Keys: kr,
	})
	if err != nil {
		fmt.Fprintf(stderr, "restore: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "restored %s (schema %d, %d blobs)\n", *from, m.SchemaVersion, m.BlobCount)
	if m.SchemaVersion < db.ExpectedVersion() {
		mig, err := db.NewMigrator(pool)
		if err == nil {
			_, err = mig.Up(ctx)
		}
		if err != nil {
			fmt.Fprintf(stderr, "restore: migrate up: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "migrated to schema %d\n", db.ExpectedVersion())
	}
	return 0
}

// backupConfig loads the configuration and the master key, which both commands need.
func backupConfig(stderr io.Writer) (config.Config, *crypto.Keyring, int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "configuration error:\n%v\n", err)
		return cfg, nil, 1
	}
	if cfg.MasterKeyFile == "" {
		fmt.Fprintln(stderr, "VITAMUX_MASTER_KEY_FILE is not set: backups record and check the master key id")
		return cfg, nil, 1
	}
	kr, err := crypto.Load(cfg.MasterKeyFile, cfg.PreviousMasterKeyFiles...)
	if err != nil {
		fmt.Fprintf(stderr, "master key: %v\n", err)
		return cfg, nil, 1
	}
	return cfg, kr, 0
}
