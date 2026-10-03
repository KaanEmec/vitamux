// Command fixturegen writes a synthetic, fully fictional health-data year as canonical truth:
// what the normalizers should end up with, using only v1 codes from docs/architecture/metric-catalog.md.
// Output is byte-identical for the same flags on every platform: all values are integers, formatting
// is by hand, timezone data is embedded and every random stream is a seeded PCG (see rng.go).
//
//	go run ./tools/fixturegen -seed 42 -out fixtures/generated
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"
	_ "time/tzdata" // embedded so local dates never depend on the host's zoneinfo
)

// ShapeWriter receives the generated world one record at a time, so a year of 6 s heart rate
// streams never sits in memory. The canonical NDJSON writer is the truth;
// provider-shaped writers (Withings in J08.1, HealthKit in E15) implement the same interface
// to render the same world as that provider's raw wire format for normalizer golden tests.
// withingsWriter (withings.go) is the Withings one.
type ShapeWriter interface {
	Source(Source) error
	Measurement(Measurement) error
	Group(Group) error
	Sleep(Sleep) error
	Revision(Revision) error
	// Close flushes and finishes the output. Called once, after the last record.
	Close() error
}

func main() {
	seed := flag.Uint64("seed", 42, "PRNG seed")
	out := flag.String("out", "fixtures/generated", "output directory")
	start := flag.String("start", "2025-01-01", "first local date (YYYY-MM-DD)")
	days := flag.Int("days", 365, "number of local days")
	hrStep := flag.Int("hr-step", 6, "seconds between high-frequency heart rate samples")
	flag.Parse()

	if err := generate(*out, *seed, *start, *days, *hrStep); err != nil {
		fmt.Fprintln(os.Stderr, "fixturegen:", err)
		os.Exit(1)
	}
}

func generate(out string, seed uint64, start string, days, hrStep int) error {
	first, err := time.Parse("2006-01-02", start)
	if err != nil || days < 1 || hrStep < 1 {
		return errors.New("need -start YYYY-MM-DD, -days >= 1 and -hr-step >= 1")
	}
	w, err := newCanonicalWriter(out, header{Seed: seed, Start: start, Days: days})
	if err != nil {
		return err
	}
	ww, err := newWithingsWriter(out)
	if err != nil {
		return err
	}
	g := &gen{seed: seed, hrStep: hrStep, out: []ShapeWriter{w, ww}}
	if err := g.run(dayIndex(first), days); err != nil {
		return err
	}
	return errors.Join(w.Close(), ww.Close())
}
