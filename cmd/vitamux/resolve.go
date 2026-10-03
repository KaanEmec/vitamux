package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/resolve"
)

const resolveUsage = `usage: vitamux resolve verify [--windows N] [--from DATE] [--to DATE] [--seed S]

verify compares the resolved cache with live resolution: it fills the cache for every metric
with a rule in effect on the dates from through to (default: the 365 days before today), then
resolves N random (metric, date) pairs from the cache and live and lists those that differ.
It exits 1 when any differ. DATE is YYYY-MM-DD.
`

func resolveCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "verify" {
		fmt.Fprint(stderr, resolveUsage)
		return 2
	}
	fs := flag.NewFlagSet("resolve verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, resolveUsage) }
	today := time.Now().UTC().Truncate(24 * time.Hour)
	n := fs.Int("windows", 1000, "random (metric, date) pairs to compare")
	fromS := fs.String("from", today.AddDate(0, 0, -365).Format(time.DateOnly), "first local date")
	toS := fs.String("to", today.AddDate(0, 0, -1).Format(time.DateOnly), "last local date")
	seed := fs.Uint64("seed", uint64(time.Now().UnixNano()), "random seed") //nolint:gosec // a seed, not a size
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	from, errF := time.Parse(time.DateOnly, *fromS)
	to, errT := time.Parse(time.DateOnly, *toS)
	if errF != nil || errT != nil || to.Before(from) || *n < 1 || fs.NArg() != 0 {
		fmt.Fprint(stderr, resolveUsage)
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
		fmt.Fprintf(stderr, "resolve verify: %v\n", err)
		return 1
	}
	start := time.Now()
	rep, err := resolve.Verify(ctx, db.New(pool), from, to, *n, rand.New(rand.NewPCG(*seed, 0))) //nolint:gosec // sampling, not security
	if err != nil {
		fmt.Fprintf(stderr, "resolve verify: %v\n", err)
		return 1
	}
	for _, d := range rep.Diffs {
		fmt.Fprintln(stdout, d)
	}
	fmt.Fprintf(stdout, "%d windows compared, %d diffs (seed %d, %s)\n", rep.Windows, len(rep.Diffs), *seed, time.Since(start).Round(time.Second))
	if len(rep.Diffs) > 0 {
		return 1
	}
	return 0
}
