package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/lifecycle"
)

// purgeUserCmd is `vitamux admin purge-user [--username NAME] [--yes]`.
func purgeUserCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("admin purge-user", flag.ContinueOnError)
	fs.SetOutput(stderr)
	username := fs.String("username", "", "account to purge (default: the only account)")
	yes := fs.Bool("yes", false, "really delete; without it the counts are only printed")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprint(stderr, adminUsage)
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "configuration error:\n%v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, cfg.DatabaseURL.Value(), db.AppRole)
	if err != nil {
		fmt.Fprintf(stderr, "database: %v\n", err)
		return 1
	}
	defer pool.Close()
	if err := db.CheckSchema(ctx, pool); err != nil {
		fmt.Fprintf(stderr, "purge-user: %v\n", err)
		return 1
	}
	var blobs *blob.Store // nil: the daily sweep removes the files instead
	if cfg.MasterKeyFile != "" {
		if kr, err := crypto.Load(cfg.MasterKeyFile, cfg.PreviousMasterKeyFiles...); err == nil {
			blobs, _ = blob.Open(filepath.Join(cfg.DataDir, "blobs"), kr)
		}
	}
	return purgeUser(ctx, db.New(pool), blobs, *username, *yes, stdout, stderr)
}

// purgeUser deletes the account (lifecycle.Purge) and prints what went. Without yes it rolls
// back and prints what would go. With a blob store it sweeps unreferenced blobs afterwards.
func purgeUser(ctx context.Context, d *db.DB, blobs *blob.Store, username string, yes bool, stdout, stderr io.Writer) int {
	users, err := d.Q().ListLifecycleUsers(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "purge-user: %v\n", err)
		return 1
	}
	var id uuid.UUID
	for _, u := range users {
		if u.Username == username || (username == "" && len(users) == 1) {
			id, username = u.ID, u.Username
		}
	}
	if id == uuid.Nil {
		if username == "" {
			fmt.Fprintf(stderr, "purge-user: %d accounts; name one with --username\n", len(users))
		} else {
			fmt.Fprintf(stderr, "purge-user: no account named %q\n", username)
		}
		return 1
	}
	counts, err := lifecycle.Purge(ctx, d, id, "admin", !yes)
	if errors.Is(err, lifecycle.ErrJobRunning) {
		fmt.Fprintln(stderr, "purge-user: a job of this account is running; stop vitamux serve (or wait) and try again")
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "purge-user: %v\n", err)
		return 1
	}
	verb := "would delete"
	if yes {
		verb = "deleted"
	}
	fmt.Fprintf(stdout, "account %q: %s\n", username, verb)
	for _, c := range counts {
		if c.Rows > 0 {
			fmt.Fprintf(stdout, "  %-20s %d\n", c.Table, c.Rows)
		}
	}
	if !yes {
		fmt.Fprintln(stdout, "Nothing was deleted. Re-run with --yes to delete everything listed; this cannot be undone.")
		return 1
	}
	fmt.Fprintln(stdout, "Audit events stay, without a link to the account.")
	if blobs == nil {
		fmt.Fprintln(stdout, "Blob files are removed by the next daily sweep (the master key or data directory is not available here).")
		return 0
	}
	st, err := blob.Sweep(ctx, d, blobs, blob.DefaultGrace)
	if err != nil {
		fmt.Fprintf(stderr, "purge-user: blob sweep: %v (the daily sweep retries)\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Blob sweep removed %d files; blobs younger than %s go with the next daily sweep.\n", st.Rows, blob.DefaultGrace)
	return 0
}
