package blob

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// DefaultGrace is how long an unreferenced blob or orphan file is kept before Sweep removes it.
const DefaultGrace = 24 * time.Hour

// SweepStats counts what Sweep removed.
type SweepStats struct {
	Rows  int // unreferenced blobs rows (and their files)
	Files int // orphan files without a row, and stale temp files
}

// Sweep removes blobs that have been unreferenced for longer than grace: rows with refcount 0
// and their files, files with no row (a crash between write and commit), and stale temp files.
// It holds the blob lock exclusively, so writers wait for it to finish. A refcount that has
// drifted to 0 while a row still references the blob fails the sweep on the foreign key
// before any file is removed.
func Sweep(ctx context.Context, d *db.DB, s *Store, grace time.Duration) (SweepStats, error) {
	var st SweepStats
	err := d.Tx(ctx, func(q *dbq.Queries) error {
		st = SweepStats{}
		if err := q.LockBlobsExclusive(ctx, lockKey); err != nil {
			return err
		}
		cutoff := time.Now().Add(-grace)
		gone, err := q.DeleteUnreferencedBlobs(ctx, cutoff)
		if err != nil {
			return err
		}
		for _, sum := range gone {
			if err := removeIfExists(s.path(sum)); err != nil {
				return err
			}
			st.Rows++
		}
		// The lock guarantees no writer is mid-transaction, so every live blob has a
		// committed row and any other old file is an orphan.
		sums, err := q.ListBlobHashes(ctx)
		if err != nil {
			return err
		}
		live := make(map[string]bool, len(sums))
		for _, sum := range sums {
			live[s.name(sum)] = true
		}
		st.Files, err = s.removeOrphans(live, cutoff)
		return err
	})
	return st, err
}

func (s *Store) removeOrphans(live map[string]bool, cutoff time.Time) (int, error) {
	subs, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sub := range subs {
		if !sub.IsDir() {
			continue // names.key
		}
		dir := filepath.Join(s.dir, sub.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			return n, err
		}
		for _, f := range files {
			if sub.Name() != tmpDir && live[f.Name()] {
				continue
			}
			fi, err := f.Info()
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return n, err
			}
			if fi.ModTime().After(cutoff) {
				continue
			}
			if err := removeIfExists(filepath.Join(dir, f.Name())); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
