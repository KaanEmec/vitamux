// Command fixtureload loads a synthetic tools/fixturegen slice into a migrated instance through
// internal/db/fixtureload, plus a timezone period for the generated owner, as the app role from
// the usual settings (VITAMUX_DATABASE_URL[_FILE]). scripts/upgrade-test.sh runs it inside the
// vitamux container, also against an older schema (no schema check: SQL that does not fit fails).
// Synthetic data only: never point it at an instance with real data.
//
//	fixtureload [-tz Europe/Berlin] DIR
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/db/fixtureload"
)

func main() {
	tz := flag.String("tz", "Europe/Berlin", "timezone of the generated owner")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: fixtureload [-tz ZONE] DIR")
		os.Exit(2)
	}
	if err := run(context.Background(), flag.Arg(0), *tz); err != nil {
		fmt.Fprintln(os.Stderr, "fixtureload:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dir, tz string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL.Value(), db.AppRole)
	if err != nil {
		return err
	}
	defer pool.Close()
	stats, err := fixtureload.Load(ctx, pool, dir)
	if err != nil {
		return err
	}
	if err := db.New(pool).Q().InsertTimezonePeriod(ctx, dbq.InsertTimezonePeriodParams{
		ID: uuid.New(), UserID: stats.UserID, Tz: tz, ValidFrom: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		return db.MapErr(err)
	}
	fmt.Printf("loaded %d measurements (%d heart rate), %d groups, %d sleep sessions, %d raw payloads\n",
		stats.Measurements, stats.HeartRate, stats.Groups, stats.Sleep, stats.Raw)
	return nil
}
