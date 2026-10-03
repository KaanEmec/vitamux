package main

import (
	"archive/zip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/export"
)

const importUsage = `usage: vitamux import ndjson [--merge] EXPORT

Imports a Vitamux export (the zip from POST /api/v1/exports, or its unpacked directory) with
its ids, timestamps and provenance. The instance needs its owner (vitamux admin create-owner)
and no health data yet, unless --merge: then rows it already has (same dedupe key, natural key
or id) are skipped and the rest added; running it again changes nothing. The export must have
the same schema version as this build. Stop serve while importing. Raw content in the export
needs VITAMUX_MASTER_KEY_FILE and the data directory.
`

func importCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "ndjson" {
		fmt.Fprint(stderr, importUsage)
		return 2
	}
	fset := flag.NewFlagSet("import ndjson", flag.ContinueOnError)
	fset.SetOutput(stderr)
	fset.Usage = func() { fmt.Fprint(stderr, importUsage) }
	merge := fset.Bool("merge", false, "add to an instance that already has data")
	if err := fset.Parse(args[1:]); err != nil {
		return 2
	}
	if fset.NArg() != 1 {
		fmt.Fprint(stderr, importUsage)
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "configuration error:\n%v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fsys, closeExport, err := openExport(fset.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "import: %v\n", err)
		return 1
	}
	defer closeExport()
	var blobs *blob.Store
	if cfg.MasterKeyFile != "" {
		keys, err := crypto.Load(cfg.MasterKeyFile, cfg.PreviousMasterKeyFiles...)
		if err == nil {
			blobs, err = blob.Open(filepath.Join(cfg.DataDir, "blobs"), keys)
		}
		if err != nil {
			fmt.Fprintf(stderr, "import: blob store: %v\n", err)
			return 1
		}
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL.Value(), db.AppRole)
	if err != nil {
		fmt.Fprintf(stderr, "database: %v\n", err)
		return 1
	}
	defer pool.Close()
	if err := db.CheckSchema(ctx, pool); err != nil {
		fmt.Fprintf(stderr, "import: %v\n", err)
		return 1
	}

	st, err := export.Import(ctx, db.New(pool), fsys, export.ImportOptions{Merge: *merge, Blobs: blobs})
	if err != nil {
		fmt.Fprintf(stderr, "import: %v\n", err)
		if errors.Is(err, export.ErrNotEmpty) || errors.Is(err, export.ErrIncompatible) {
			return 2
		}
		return 1
	}
	for _, t := range st.Tables {
		fmt.Fprintf(stdout, "%-20s %10d read %10d new\n", t.Name, t.Read, t.Inserted)
	}
	if st.RawContent > 0 {
		fmt.Fprintf(stdout, "%-20s %10d stored\n", "raw content", st.RawContent)
	}
	return 0
}

// openExport opens a directory or a zip as a file system.
func openExport(path string) (fs.FS, func(), error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return os.DirFS(path), func() {}, nil
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, nil, err
	}
	return zr, func() { _ = zr.Close() }, nil
}
