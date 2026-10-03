package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

var (
	// ErrTampered means the manifest's MAC or a file's size or checksum does not match.
	ErrTampered = errors.New("backup: manifest or files do not match (tampered or corrupt)")
	// ErrNotEmpty means the target database or blob directory already holds data.
	ErrNotEmpty = errors.New("backup: restore needs an empty database and data directory")
)

// Verify authenticates dir's manifest with the master key and checks every file against it.
func Verify(dir string, kr *crypto.Keyring) (Manifest, error) {
	var m Manifest
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFile)) //nolint:gosec // operator-supplied backup directory
	if err != nil {
		return m, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("%w: %s is not a valid manifest", ErrTampered, ManifestFile)
	}
	if m.Format != Format {
		return m, fmt.Errorf("backup: manifest format %d, this binary restores format %d", m.Format, Format)
	}
	sealed, err := base64.StdEncoding.DecodeString(m.MAC)
	if err != nil {
		return m, fmt.Errorf("%w: missing MAC", ErrTampered)
	}
	got, err := kr.Open(crypto.Documents, sealed, []byte(manifestAAD))
	if errors.Is(err, crypto.ErrUnknownKey) {
		return m, fmt.Errorf("backup: made with master key %s, which is not loaded (VITAMUX_MASTER_KEY_FILE or VITAMUX_PREVIOUS_MASTER_KEY_FILES)", m.KeyID)
	}
	want, derr := digest(m)
	if err != nil || derr != nil || subtle.ConstantTimeCompare(got, want) != 1 {
		return m, fmt.Errorf("%w: manifest MAC", ErrTampered)
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if (f.Name != DumpFile && f.Name != BlobsFile) || seen[f.Name] {
			return m, fmt.Errorf("%w: unexpected file %q", ErrTampered, f.Name)
		}
		seen[f.Name] = true
		have, err := checksum(dir, f.Name)
		if err != nil {
			return m, err
		}
		if have != f {
			return m, fmt.Errorf("%w: %s", ErrTampered, f.Name)
		}
	}
	if !seen[DumpFile] || !seen[BlobsFile] {
		return m, fmt.Errorf("%w: manifest lists no %s or %s", ErrTampered, DumpFile, BlobsFile)
	}
	return m, nil
}

// RestoreOptions configure Restore.
type RestoreOptions struct {
	From string // the backup directory
	// DatabaseURL is the postgres:// URL of the owner login (VITAMUX_MIGRATE_DATABASE_URL);
	// objects are created as vitamux_owner.
	DatabaseURL string
	DB          *db.DB // the same database opened with db.OwnerRole
	DataDir     string
	Keys        *crypto.Keyring
}

// Restore verifies the backup in o.From and restores it into an empty database (roles.sql
// applied, no migrations) and an empty blob directory, then checks that every referenced blob
// exists. A manifest older than this binary's schema is restored as is: the caller runs the
// migrations afterwards. A newer one is refused.
func Restore(ctx context.Context, o RestoreOptions) (Manifest, error) {
	m, err := Verify(o.From, o.Keys)
	if err != nil {
		return m, err
	}
	if want := db.ExpectedVersion(); m.SchemaVersion > want {
		return m, fmt.Errorf("backup: schema version %d is newer than this binary (%d): restore with a newer vitamux", m.SchemaVersion, want)
	}
	if n, err := o.DB.Q().CountSchemaTables(ctx); err != nil {
		return m, err
	} else if n > 0 {
		return m, fmt.Errorf("%w: the %s schema has %d tables", ErrNotEmpty, db.Schema, n)
	}
	blobsDir := filepath.Join(o.DataDir, "blobs")
	if entries, err := os.ReadDir(blobsDir); err == nil && len(entries) > 0 {
		return m, fmt.Errorf("%w: %s is not empty", ErrNotEmpty, blobsDir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return m, err
	}

	if err := extract(filepath.Join(o.From, BlobsFile), blobsDir); err != nil {
		_ = os.RemoveAll(blobsDir)
		return m, err
	}
	store, err := blob.Open(blobsDir, o.Keys) // proves names.key opens with this master key
	if err == nil {
		err = pgRestore(ctx, o)
	}
	if err != nil {
		_ = os.RemoveAll(blobsDir) // the database rolled back (single transaction)
		return m, err
	}
	return m, checkBlobs(ctx, o.DB.Q(), store)
}

// schemaEntry matches the TOC line that creates the schema, which roles.sql already made.
var schemaEntry = regexp.MustCompile(`^\d+; \d+ \d+ SCHEMA - ` + db.Schema + ` `)

// pgRestore loads the dump in one transaction as vitamux_owner. The app role's default
// privileges are lifted meanwhile: pg_dump only records how each object's ACL differs from the
// built-in default, so objects must not pick up roles.sql's defaults when they are created.
func pgRestore(ctx context.Context, o RestoreOptions) error {
	conn, env, err := libpq(o.DatabaseURL)
	if err != nil {
		return err
	}
	dump := filepath.Join(o.From, DumpFile)
	var toc bytes.Buffer
	if err := run(ctx, env, &toc, "pg_restore", "--list", dump); err != nil {
		return err
	}
	var list bytes.Buffer
	for l := range strings.SplitSeq(toc.String(), "\n") {
		if schemaEntry.MatchString(l) {
			l = ";" + l
		}
		list.WriteString(l + "\n")
	}
	listFile, err := os.CreateTemp("", "vitamux-restore-*.list")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(listFile.Name()) }()
	if _, err := listFile.Write(list.Bytes()); err != nil {
		_ = listFile.Close()
		return err
	}
	if err := listFile.Close(); err != nil {
		return err
	}

	if err := o.DB.Tx(ctx, func(q *dbq.Queries) error {
		return errors.Join(q.RevokeAppDefaultPrivileges(ctx), q.RevokeAppDefaultSequencePrivileges(ctx), q.RevokeAppDefaultFunctionPrivileges(ctx))
	}); err != nil {
		return err
	}
	err = run(ctx, env, nil, "pg_restore", "--single-transaction", "--exit-on-error", "--no-owner", "--no-password",
		"--role="+string(db.OwnerRole), "--use-list="+listFile.Name(), "--dbname="+conn, dump)
	// Restore the roles.sql defaults whatever happened (the dump may carry them too).
	gctx := context.WithoutCancel(ctx)
	grant := o.DB.Tx(gctx, func(q *dbq.Queries) error {
		return errors.Join(q.GrantAppDefaultPrivileges(gctx), q.GrantAppDefaultSequencePrivileges(gctx), q.GrantAppDefaultFunctionPrivileges(gctx))
	})
	return errors.Join(err, grant)
}

// checkBlobs fails if a blob with references has no file.
func checkBlobs(ctx context.Context, q *dbq.Queries, s *blob.Store) error {
	sums, err := q.ListReferencedBlobHashes(ctx)
	if err != nil {
		return err
	}
	missing := 0
	for _, sum := range sums {
		rc, err := s.Open(sum)
		switch {
		case errors.Is(err, blob.ErrNotFound):
			missing++
		case err != nil:
			return err
		default:
			_ = rc.Close()
		}
	}
	if missing > 0 {
		return fmt.Errorf("backup: restored, but %d of %d referenced blobs have no file", missing, len(sums))
	}
	return nil
}

// extract unpacks a blobs.tar into dir. Only plain relative paths, directories and regular
// files are accepted.
func extract(archive, dir string) error {
	f, err := os.Open(archive) //nolint:gosec // a verified file of the backup directory
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("backup: %s: %w", BlobsFile, err)
		}
		name := strings.TrimSuffix(h.Name, "/")
		if name == "" || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("backup: %s: unsafe path %q", BlobsFile, h.Name)
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			if err := writeFile(target, func(w io.Writer) error {
				_, err := io.CopyN(w, tr, h.Size)
				return err
			}); err != nil {
				return err
			}
		default:
			return fmt.Errorf("backup: %s: unsupported entry %q", BlobsFile, h.Name)
		}
	}
}
