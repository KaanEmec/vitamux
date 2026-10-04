package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/connectors/applehealth"
	"github.com/KaanEmec/vitamux/internal/connectors/withings"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/jobs"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

const reprocessUsage = `usage: vitamux reprocess [--normalizer ID] [--stream NAME] [--since TIME] [--until TIME] [--wait]

Re-normalizes stored raw payloads (newest version of each record) whose last attempt was not
made by the current version of their normalizer. Unchanged output only gets the new version;
changed records are superseded. TIME is RFC 3339 or YYYY-MM-DD (UTC) and filters on fetch time.
It queues a job for the running server; --wait prints the result when the job finishes.
`

// normalizers is every normalizer this build ships. Connectors (E08, E15) add theirs here.
func normalizers() (*normalize.Registry, error) {
	return normalize.NewRegistry(withings.Normalizer{}, applehealth.Normalizer{}, applehealth.ExportNormalizer{}, normalize.Manual{})
}

func reprocess(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reprocess", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, reprocessUsage) }
	var sel normalize.ReprocessPayload
	var since, until string
	var wait bool
	fs.StringVar(&sel.Normalizer, "normalizer", "", "only payloads of this normalizer ID")
	fs.StringVar(&sel.Stream, "stream", "", "only this stream")
	fs.StringVar(&since, "since", "", "fetched at or after")
	fs.StringVar(&until, "until", "", "fetched before")
	fs.BoolVar(&wait, "wait", false, "wait for the job and print its summary")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var err error
	if fs.NArg() != 0 {
		err = fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if err == nil {
		sel.Since, err = parseTime("--since", since)
	}
	if err == nil {
		sel.Until, err = parseTime("--until", until)
	}
	if err != nil {
		fmt.Fprintf(stderr, "reprocess: %v\n", err)
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "configuration error:\n%v\n", err)
		return 1
	}
	reg, err := normalizers()
	if err != nil {
		fmt.Fprintf(stderr, "reprocess: %v\n", err)
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
		fmt.Fprintf(stderr, "reprocess: %v\n", err)
		return 1
	}
	d := db.New(pool)

	// The count needs no blobs: it only decides which payloads qualify.
	selected, scanned, err := (&normalize.Processor{DB: d, Registry: reg}).CountReprocess(ctx, sel)
	if err != nil {
		fmt.Fprintf(stderr, "reprocess: %v\n", err)
		return 1
	}
	if selected == 0 {
		fmt.Fprintf(stdout, "nothing to reprocess (%d raw payloads scanned)\n", scanned)
		return 0
	}
	id, created, err := normalize.EnqueueReprocess(ctx, d, sel)
	if err != nil {
		fmt.Fprintf(stderr, "reprocess: %v\n", err)
		return 1
	}
	verb := "queued"
	if !created {
		verb = "already queued as"
	}
	fmt.Fprintf(stdout, "%d of %d raw payloads selected; reprocess job %s %s\n", selected, scanned, verb, id)
	if !wait {
		return 0
	}
	return waitReprocess(ctx, d, id, stdout, stderr)
}

// waitReprocess polls the job until it ends and prints the summary it checkpointed.
func waitReprocess(ctx context.Context, d *db.DB, id uuid.UUID, stdout, stderr io.Writer) int {
	for {
		j, err := d.Q().GetJob(ctx, id)
		if err != nil {
			fmt.Fprintf(stderr, "reprocess: %v\n", db.MapErr(err))
			return 1
		}
		switch j.Status {
		case "succeeded", "failed", "dead", "cancelled":
			var st struct {
				Summary normalize.Summary `json:"summary"`
			}
			if json.Unmarshal(j.Checkpoint, &st) == nil && j.Checkpoint != nil {
				fmt.Fprintf(stdout, "job %s: %s\n", j.Status, st.Summary)
			} else {
				fmt.Fprintf(stdout, "job %s\n", j.Status)
			}
			if j.Status != "succeeded" {
				return 1
			}
			return 0
		}
		select {
		case <-ctx.Done():
			fmt.Fprintf(stderr, "reprocess: stopped waiting; job %s keeps running\n", id)
			return 1
		case <-time.After(time.Second):
		}
	}
}

// parseTime reads RFC 3339 or a UTC date; empty is nil.
func parseTime(flagName, s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("%s: %q is not RFC 3339 or YYYY-MM-DD", flagName, s)
}

// registerNormalizeJobs wires normalize_batch and reprocess. Both read raw bodies, so without a
// blob store (reported by /readyz) their jobs stay queued.
func registerNormalizeJobs(r *jobs.Runner, d *db.DB, blobs *blob.Store, log *slog.Logger) error {
	if blobs == nil {
		return nil
	}
	reg, err := normalizers()
	if err != nil {
		return err
	}
	p := &normalize.Processor{DB: d, Blobs: blobs, Registry: reg, Log: log}
	r.Register(ingest.KindNormalizeBatch, p.BatchJob())
	r.Register(normalize.KindReprocess, p.ReprocessJob())
	return nil
}
