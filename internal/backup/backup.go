// Package backup implements `vitamux backup` and `vitamux restore` (docs/operations/backup.md,
// docs/architecture/reliability.md#backup-and-restore).
//
// A backup is a directory vitamux-<UTC time>/ holding db.dump (pg_dump custom format of the
// vitamux schema), blobs.tar (the blob store with names.key, ADR-0004) and manifest.json (schema
// version, master key id, size and SHA-256 of each file). The manifest is authenticated with the
// master key, so a backup cannot be altered unnoticed; the key itself is never in it.
package backup

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/version"
)

// Format is the manifest format this binary writes and restores.
const Format = 1

// Files of a backup directory.
const (
	ManifestFile = "manifest.json"
	DumpFile     = "db.dump"
	BlobsFile    = "blobs.tar"
	namePrefix   = "vitamux-"
	partialExt   = ".partial"
	manifestAAD  = "backup-manifest"
)

// Manifest describes one backup.
type Manifest struct {
	Format         int       `json:"format"`
	CreatedAt      time.Time `json:"created_at"`
	VitamuxVersion string    `json:"vitamux_version"`
	SchemaVersion  int64     `json:"schema_version"`
	// KeyID is the master key a restore needs (crypto.Keyring.KeyID). Values sealed before an
	// unfinished rotation also need the previous key.
	KeyID     string `json:"key_id"`
	BlobCount int    `json:"blob_count"`
	Files     []File `json:"files"`
	// MAC is the SHA-256 of the manifest without this field, sealed with the master key
	// (purpose documents, AAD "backup-manifest"), base64.
	MAC string `json:"mac,omitempty"`
}

// File is one part of a backup.
type File struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Options configure Create.
type Options struct {
	// DatabaseURL is the postgres:// URL pg_dump connects with. The app role is enough: the dump
	// only reads, and it runs as vitamux_app.
	DatabaseURL string
	DB          *db.DB // the same database, for the schema version
	DataDir     string // VITAMUX_DATA_DIR; the blob store is DataDir/blobs
	Keys        *crypto.Keyring
	Out         string           // parent directory of the backup directories
	Now         func() time.Time // default time.Now
}

// Create writes a new backup directory under o.Out and returns its path. The dump comes first,
// then the blobs: blobs are immutable and content-addressed, so every blob the dump references
// is still there when it is copied (the sweep only removes rows with refcount 0). Files are
// written into a hidden .partial directory that is renamed into place when complete.
func Create(ctx context.Context, o Options) (string, Manifest, error) {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	m := Manifest{Format: Format, CreatedAt: now().UTC().Truncate(time.Second), VitamuxVersion: version.Version, KeyID: o.Keys.KeyID()}
	var err error
	if m.SchemaVersion, err = o.DB.SchemaVersion(ctx); err != nil {
		return "", m, fmt.Errorf("backup: schema version: %w", err)
	}
	if err := os.MkdirAll(o.Out, 0o700); err != nil {
		return "", m, err
	}
	name := namePrefix + m.CreatedAt.Format("20060102T150405Z")
	final := filepath.Join(o.Out, name)
	if _, err := os.Stat(final); err == nil {
		return "", m, fmt.Errorf("backup: %s already exists", final)
	}
	tmp := filepath.Join(o.Out, "."+name+partialExt)
	if err := os.Mkdir(tmp, 0o700); err != nil {
		return "", m, err
	}
	defer func() { _ = os.RemoveAll(tmp) }() // no-op after the rename

	conn, env, err := libpq(o.DatabaseURL)
	if err != nil {
		return "", m, err
	}
	dump := filepath.Join(tmp, DumpFile)
	if err := run(ctx, env, nil, "pg_dump", "--format=custom", "--schema="+db.Schema, "--role="+string(db.AppRole),
		"--no-password", "--file="+dump, "--dbname="+conn); err != nil {
		return "", m, err
	}
	if err := os.Chmod(dump, 0o600); err != nil {
		return "", m, err
	}
	df, err := checksum(tmp, DumpFile)
	if err != nil {
		return "", m, err
	}
	bf, n, err := writeBlobs(filepath.Join(o.DataDir, "blobs"), filepath.Join(tmp, BlobsFile))
	if err != nil {
		return "", m, err
	}
	m.Files, m.BlobCount = []File{df, bf}, n

	if m.MAC, err = mac(m, o.Keys); err != nil {
		return "", m, err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", m, err
	}
	if err := writeFile(filepath.Join(tmp, ManifestFile), func(w io.Writer) error {
		_, err := w.Write(append(raw, '\n'))
		return err
	}); err != nil {
		return "", m, err
	}
	if err := syncDir(tmp); err != nil {
		return "", m, err
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", m, err
	}
	return final, m, syncDir(o.Out)
}

// writeBlobs tars the blob store (names.key and the fan-out directories, not tmp/) into path and
// returns its File entry and the number of blobs. A missing store is an empty archive.
func writeBlobs(dir, path string) (File, int, error) {
	n := 0
	err := writeFile(path, func(w io.Writer) error {
		tw := tar.NewWriter(w)
		err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
			switch {
			case errors.Is(err, fs.ErrNotExist) && p == dir:
				return fs.SkipAll
			case errors.Is(err, fs.ErrNotExist):
				return nil // removed by the sweep meanwhile: an unreferenced blob
			case err != nil:
				return err
			case p == dir:
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			switch {
			case e.IsDir() && rel == "tmp":
				return fs.SkipDir
			case e.IsDir():
				return tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: rel + "/", Mode: 0o700})
			case !e.Type().IsRegular() || strings.HasPrefix(e.Name(), "."):
				return nil
			}
			f, err := os.Open(p) //nolint:gosec // walking the configured blob directory
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			} else if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			st, err := f.Stat()
			if err != nil {
				return err
			}
			if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: rel, Mode: 0o600, Size: st.Size(), ModTime: st.ModTime()}); err != nil {
				return err
			}
			if _, err := io.Copy(tw, f); err != nil {
				return err
			}
			if rel != "names.key" {
				n++
			}
			return nil
		})
		if err != nil {
			return err
		}
		return tw.Close()
	})
	if err != nil {
		return File{}, 0, fmt.Errorf("backup: blobs: %w", err)
	}
	f, err := checksum(filepath.Dir(path), filepath.Base(path))
	return f, n, err
}

// mac seals the digest of m without its MAC.
func mac(m Manifest, kr *crypto.Keyring) (string, error) {
	sum, err := digest(m)
	if err != nil {
		return "", err
	}
	sealed, err := kr.Seal(crypto.Documents, sum, []byte(manifestAAD))
	return base64.StdEncoding.EncodeToString(sealed), err
}

func digest(m Manifest) ([]byte, error) {
	m.MAC = ""
	raw, err := json.Marshal(m)
	sum := sha256.Sum256(raw)
	return sum[:], err
}

func checksum(dir, name string) (File, error) {
	f, err := os.Open(filepath.Join(dir, name)) //nolint:gosec // a file of the backup directory
	if err != nil {
		return File{}, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return File{Name: name, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, err
}

// writeFile creates path (mode 0600), lets write fill it and fsyncs it.
func writeFile(path string, write func(io.Writer) error) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path built by the caller
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // a directory the caller created
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

// Prune keeps the newest keep backups in dir and removes older ones, plus .partial directories
// left by an interrupted run that are older than a day. It returns the removed names.
func Prune(dir string, keep int, now time.Time) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var done, removed []string
	for _, e := range entries {
		name := e.Name()
		switch {
		case !e.IsDir():
		case strings.HasPrefix(name, "."+namePrefix) && strings.HasSuffix(name, partialExt):
			if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > 24*time.Hour {
				if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
					return removed, err
				}
				removed = append(removed, name)
			}
		case strings.HasPrefix(name, namePrefix):
			if _, err := os.Stat(filepath.Join(dir, name, ManifestFile)); err == nil {
				done = append(done, name)
			}
		}
	}
	sort.Strings(done) // names sort by creation time
	for len(done) > max(keep, 1) {
		if err := os.RemoveAll(filepath.Join(dir, done[0])); err != nil {
			return removed, err
		}
		removed, done = append(removed, done[0]), done[1:]
	}
	return removed, nil
}

// WriteTar streams a backup directory as an uncompressed tar (for `vitamux backup --out -`).
func WriteTar(w io.Writer, dir string) error {
	tw := tar.NewWriter(w)
	name := filepath.Base(dir)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: 0o700}); err != nil {
		return err
	}
	for _, part := range []string{DumpFile, BlobsFile, ManifestFile} {
		raw, err := os.Open(filepath.Join(dir, part)) //nolint:gosec // a file of the backup directory
		if err != nil {
			return err
		}
		st, err := raw.Stat()
		if err == nil {
			err = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name + "/" + part, Mode: 0o600, Size: st.Size(), ModTime: st.ModTime()})
		}
		if err == nil {
			_, err = io.Copy(tw, raw)
		}
		if err := errors.Join(err, raw.Close()); err != nil {
			return err
		}
	}
	return tw.Close()
}
