package blob

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"

	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Mode says whether a blob is stored as is or sealed with the documents key.
type Mode int

const (
	// Plain blobs are compressed only; they rely on volume encryption like the database.
	Plain Mode = iota
	// Sealed blobs are compressed, then sealed with purpose crypto.Documents (lab PDFs).
	Sealed
)

var (
	// ErrNotFound means no blob file exists for the hash.
	ErrNotFound = errors.New("blob: not found")
	// ErrCorrupt means the stored file does not decode to content with the expected hash.
	ErrCorrupt = errors.New("blob: corrupt")
	// ErrNotSealed means Put asked for Sealed but the same content is already stored plain.
	ErrNotSealed = errors.New("blob: content already stored unsealed")
)

// File format: one format byte, then the zstd stream (formatZstd) or a crypto sealed value
// over the zstd bytes (formatZstdSealed).
const (
	formatZstd       byte = 1
	formatZstdSealed byte = 2
)

const (
	tmpDir       = "tmp"
	namesKeyFile = "names.key"
	namesKeyAAD  = "blob-names"
	// lockKey is the advisory lock that orders writers (shared) against Sweep (exclusive).
	lockKey int64 = 0x766d78_626c6f62 // "vmx" "blob"
)

// Store is a blob directory. It is safe for concurrent use.
type Store struct {
	dir      string
	nameKey  []byte
	keyring  *crypto.Keyring
	compress *zstd.Encoder // only EncodeAll, which is safe for concurrent use
}

// Info describes a stored blob.
type Info struct {
	SHA256     []byte
	Size       int64 // uncompressed
	StoredSize int64 // on disk
	Sealed     bool
}

// Open opens or initialises the blob directory dir (normally <data dir>/blobs). File names
// are an HMAC under a random key kept in dir/names.key, sealed with purpose blob-names, so
// they survive a master key rotation (the file is resealed with the current key on open).
func Open(dir string, kr *crypto.Keyring) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, tmpDir), 0o700); err != nil {
		return nil, err
	}
	key, err := loadNameKey(dir, kr)
	if err != nil {
		return nil, err
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir, nameKey: key, keyring: kr, compress: enc}, nil
}

func loadNameKey(dir string, kr *crypto.Keyring) ([]byte, error) {
	path := filepath.Join(dir, namesKeyFile)
	sealed, err := os.ReadFile(path) //nolint:gosec // path is under the configured data dir
	if errors.Is(err, fs.ErrNotExist) {
		if n, err := countBlobFiles(dir); err != nil || n > 0 {
			return nil, errors.Join(err, fmt.Errorf("blob: %s is missing but the directory holds %d blobs; restore it from backup", path, n))
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		return key, writeNameKey(dir, kr, key)
	}
	if err != nil {
		return nil, err
	}
	key, err := kr.Open(crypto.BlobNames, sealed, []byte(namesKeyAAD))
	if err != nil {
		return nil, fmt.Errorf("blob: open %s: %w", path, err)
	}
	// Bytes 1..9 of a sealed value are its key id (ADR-0011); reseal after a rotation so the
	// previous master key can be retired.
	if len(sealed) > 9 && hex.EncodeToString(sealed[1:9]) != kr.KeyID() {
		if err := writeNameKey(dir, kr, key); err != nil {
			return nil, err
		}
	}
	return key, nil
}

func writeNameKey(dir string, kr *crypto.Keyring, key []byte) error {
	sealed, err := kr.Seal(crypto.BlobNames, key, []byte(namesKeyAAD))
	if err != nil {
		return err
	}
	return writeAtomic(dir, filepath.Join(dir, namesKeyFile), func(f *os.File) error {
		_, err := f.Write(sealed)
		return err
	})
}

// countBlobFiles counts files in the fan-out directories.
func countBlobFiles(dir string) (int, error) {
	subs, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sub := range subs {
		if !sub.IsDir() || sub.Name() == tmpDir {
			continue
		}
		files, err := os.ReadDir(filepath.Join(dir, sub.Name()))
		if err != nil {
			return 0, err
		}
		n += len(files)
	}
	return n, nil
}

// name is the file name for a content hash: hex HMAC-SHA256 under the names key.
func (s *Store) name(sum []byte) string {
	m := hmac.New(sha256.New, s.nameKey)
	m.Write(sum)
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Store) path(sum []byte) string {
	n := s.name(sum)
	return filepath.Join(s.dir, n[:2], n)
}

// Put stores the content of r and inserts its blobs row (refcount unchanged; call Retain for
// the referencing row). Run it inside the transaction that references the blob: it holds the
// shared blob lock until commit, so Sweep cannot remove the file in between. Content already
// stored is not written again. A crash before commit leaves at most an orphan file, which
// Sweep removes after its grace period.
func (s *Store) Put(ctx context.Context, q *dbq.Queries, r io.Reader, mode Mode) (Info, error) {
	tmp, err := os.CreateTemp(filepath.Join(s.dir, tmpDir), "put-*")
	if err != nil {
		return Info{}, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after the rename
	info, err := s.writeTmp(tmp, r, mode)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Info{}, err
	}

	if err := q.LockBlobsShared(ctx, lockKey); err != nil {
		return Info{}, err
	}
	var keyID *string
	if mode == Sealed {
		id := s.keyring.KeyID()
		keyID = &id
	}
	inserted, err := q.InsertBlob(ctx, dbq.InsertBlobParams{
		Sha256: info.SHA256, SizeBytes: info.Size, StoredBytes: info.StoredSize, Compression: "zstd", KeyID: keyID,
	})
	if err != nil {
		return Info{}, err
	}
	if inserted == 0 {
		row, err := q.GetBlob(ctx, info.SHA256)
		if err != nil {
			return Info{}, err
		}
		if mode == Sealed && row.KeyID == nil {
			return Info{}, ErrNotSealed
		}
		info = Info{SHA256: row.Sha256, Size: row.SizeBytes, StoredSize: row.StoredBytes, Sealed: row.KeyID != nil}
	}

	// A new row always gets our file, so the file matches the row's mode. An existing row
	// keeps its file unless the file is missing (healed here).
	final := s.path(info.SHA256)
	if _, err := os.Stat(final); inserted == 0 && err == nil {
		return info, nil
	}
	if err := s.install(tmp.Name(), final); err != nil {
		return Info{}, err
	}
	return info, nil
}

// writeTmp compresses (and seals) r into f, fsyncs it, and returns the content's Info.
func (s *Store) writeTmp(f *os.File, r io.Reader, mode Mode) (Info, error) {
	h := sha256.New()
	var size int64
	switch mode {
	case Plain:
		if _, err := f.Write([]byte{formatZstd}); err != nil {
			return Info{}, err
		}
		zw, err := zstd.NewWriter(f, zstd.WithEncoderConcurrency(1))
		if err != nil {
			return Info{}, err
		}
		size, err = io.Copy(zw, io.TeeReader(r, h))
		if cerr := zw.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return Info{}, err
		}
	case Sealed:
		content, err := io.ReadAll(io.TeeReader(r, h))
		if err != nil {
			return Info{}, err
		}
		size = int64(len(content))
		sealed, err := s.keyring.Seal(crypto.Documents, s.compress.EncodeAll(content, nil), sealAAD(h.Sum(nil)))
		if err != nil {
			return Info{}, err
		}
		if _, err := f.Write(append([]byte{formatZstdSealed}, sealed...)); err != nil {
			return Info{}, err
		}
	default:
		return Info{}, fmt.Errorf("blob: unknown mode %d", mode)
	}
	if err := f.Sync(); err != nil {
		return Info{}, err
	}
	st, err := f.Stat()
	if err != nil {
		return Info{}, err
	}
	return Info{SHA256: h.Sum(nil), Size: size, StoredSize: st.Size(), Sealed: mode == Sealed}, nil
}

// install renames a synced temp file into place and fsyncs the directories involved.
func (s *Store) install(tmp, final string) error {
	sub := filepath.Dir(final)
	created := false
	if err := os.Mkdir(sub, 0o700); err == nil {
		created = true
	} else if !errors.Is(err, fs.ErrExist) {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	if err := syncDir(sub); err != nil {
		return err
	}
	if created {
		return syncDir(s.dir)
	}
	return nil
}

// sealAAD binds a sealed blob to its content hash.
func sealAAD(sum []byte) []byte { return []byte("blob:" + hex.EncodeToString(sum)) }

// Retain adds a reference to a stored blob, in the transaction that inserts the referencing
// row. It returns db.ErrNotFound when the blob row does not exist (e.g. already swept).
func Retain(ctx context.Context, q *dbq.Queries, sum []byte) error {
	return addRef(ctx, q, sum, 1)
}

// LockShared takes the writers' side of the blob lock until commit, for a bulk reference change
// made in SQL (Retain and Release take it themselves).
func LockShared(ctx context.Context, q *dbq.Queries) error { return q.LockBlobsShared(ctx, lockKey) }

// Release drops a reference, in the transaction that deletes the referencing row.
func Release(ctx context.Context, q *dbq.Queries, sum []byte) error {
	return addRef(ctx, q, sum, -1)
}

func addRef(ctx context.Context, q *dbq.Queries, sum []byte, delta int32) error {
	if err := q.LockBlobsShared(ctx, lockKey); err != nil {
		return err
	}
	_, err := q.AddBlobRef(ctx, dbq.AddBlobRefParams{Delta: delta, Sha256: sum})
	return db.MapErr(err)
}

// Get returns the verified content of the blob with hash sum.
func (s *Store) Get(sum []byte) ([]byte, error) {
	rc, err := s.Open(sum)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// Open streams the content of the blob with hash sum. The hash is checked at the end of the
// stream: the final Read returns ErrCorrupt instead of io.EOF on a mismatch, so read to EOF
// before trusting the content.
func (s *Store) Open(sum []byte) (io.ReadCloser, error) {
	f, err := os.Open(s.path(sum))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	br := bufio.NewReader(f)
	format, err := br.ReadByte()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	switch format {
	case formatZstd:
		dec, err := zstd.NewReader(br, zstd.WithDecoderConcurrency(1))
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		return &verifier{r: dec, h: sha256.New(), want: sum, close: func() error { dec.Close(); return f.Close() }}, nil
	case formatZstdSealed:
		defer f.Close()
		sealed, err := io.ReadAll(br)
		if err != nil {
			return nil, err
		}
		compressed, err := s.keyring.Open(crypto.Documents, sealed, sealAAD(sum))
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
		}
		dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		content, err := dec.DecodeAll(compressed, nil)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
		}
		if got := sha256.Sum256(content); !bytes.Equal(got[:], sum) {
			return nil, ErrCorrupt
		}
		return io.NopCloser(bytes.NewReader(content)), nil
	}
	_ = f.Close()
	return nil, fmt.Errorf("%w: unknown format %d", ErrCorrupt, format)
}

// verifier hashes what it reads and turns io.EOF into ErrCorrupt on a hash mismatch.
type verifier struct {
	r     io.Reader
	h     hash.Hash
	want  []byte
	close func() error
}

func (v *verifier) Read(p []byte) (int, error) {
	n, err := v.r.Read(p)
	v.h.Write(p[:n])
	switch {
	case errors.Is(err, io.EOF):
		if !bytes.Equal(v.h.Sum(nil), v.want) {
			return n, ErrCorrupt
		}
	case err != nil:
		return n, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return n, err
}

func (v *verifier) Close() error { return v.close() }

// writeAtomic writes path through a synced temp file in dir/tmp and a rename.
func writeAtomic(dir, path string, write func(*os.File) error) error {
	f, err := os.CreateTemp(filepath.Join(dir, tmpDir), "write-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err = write(f); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // path is under the configured data dir
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
