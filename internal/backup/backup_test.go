package backup

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/crypto"
)

func keyring(t *testing.T) *crypto.Keyring {
	t.Helper()
	path := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.WriteKeyFile(path); err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return kr
}

// fakeBackup writes a backup directory with a synthetic dump and a blob store of two files.
func fakeBackup(t *testing.T, kr *crypto.Keyring) string {
	t.Helper()
	store := t.TempDir()
	for name, body := range map[string]string{"names.key": "sealed-key", "ab/abcd": "blob-1", "cd/cdef": "blob-2", "tmp/put-1": "in flight"} {
		p := filepath.Join(store, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(t.TempDir(), "vitamux-20261003T120000Z")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DumpFile), []byte("synthetic dump"), 0o600); err != nil {
		t.Fatal(err)
	}
	df, err := checksum(dir, DumpFile)
	if err != nil {
		t.Fatal(err)
	}
	bf, n, err := writeBlobs(store, filepath.Join(dir, BlobsFile))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("blob count = %d, want 2 (names.key and tmp/ excluded)", n)
	}
	m := Manifest{Format: Format, CreatedAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC), SchemaVersion: 20, KeyID: kr.KeyID(), BlobCount: n, Files: []File{df, bf}}
	if m.MAC, err = mac(m, kr); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, dir, m)
	return dir
}

func writeManifest(t *testing.T, dir string, m Manifest) {
	t.Helper()
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestVerify(t *testing.T) {
	kr := keyring(t)
	dir := fakeBackup(t, kr)
	m, err := Verify(dir, kr)
	if err != nil {
		t.Fatal(err)
	}
	if m.KeyID != kr.KeyID() || m.SchemaVersion != 20 {
		t.Errorf("manifest = %+v", m)
	}

	t.Run("edited manifest", func(t *testing.T) {
		d := fakeBackup(t, kr)
		m, _ := Verify(d, kr)
		m.SchemaVersion = 19 // checksums still match; only the MAC catches it
		writeManifest(t, d, m)
		if _, err := Verify(d, kr); !errors.Is(err, ErrTampered) {
			t.Errorf("err = %v, want ErrTampered", err)
		}
	})
	t.Run("edited file", func(t *testing.T) {
		d := fakeBackup(t, kr)
		if err := os.WriteFile(filepath.Join(d, DumpFile), []byte("synthetic dumq"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(d, kr); !errors.Is(err, ErrTampered) {
			t.Errorf("err = %v, want ErrTampered", err)
		}
	})
	t.Run("unknown field", func(t *testing.T) {
		d := fakeBackup(t, kr)
		raw, _ := os.ReadFile(filepath.Join(d, ManifestFile))
		raw = bytes.Replace(raw, []byte(`"format"`), []byte(`"extra": 1, "format"`), 1)
		_ = os.WriteFile(filepath.Join(d, ManifestFile), raw, 0o600)
		if _, err := Verify(d, kr); !errors.Is(err, ErrTampered) {
			t.Errorf("err = %v, want ErrTampered", err)
		}
	})
	t.Run("other master key", func(t *testing.T) {
		if _, err := Verify(dir, keyring(t)); err == nil || !strings.Contains(err.Error(), kr.KeyID()) {
			t.Errorf("err = %v, want one naming key id %s", err, kr.KeyID())
		}
	})
}

func TestExtract(t *testing.T) {
	kr := keyring(t)
	dir := fakeBackup(t, kr)
	out := filepath.Join(t.TempDir(), "blobs")
	if err := extract(filepath.Join(dir, BlobsFile), out); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"names.key": "sealed-key", "ab/abcd": "blob-1", "cd/cdef": "blob-2"} {
		got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(name)))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "tmp")); err == nil {
		t.Error("tmp/ was archived")
	}

	for _, h := range []tar.Header{
		{Name: "../escape", Typeflag: tar.TypeReg},
		{Name: "/abs", Typeflag: tar.TypeReg},
		{Name: "ab/../../x", Typeflag: tar.TypeReg},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
	} {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		_ = tw.WriteHeader(&h)
		_ = tw.Close()
		archive := filepath.Join(t.TempDir(), "evil.tar")
		_ = os.WriteFile(archive, buf.Bytes(), 0o600)
		if err := extract(archive, filepath.Join(t.TempDir(), "blobs")); err == nil {
			t.Errorf("%q: extracted, want refusal", h.Name)
		}
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for _, name := range []string{"vitamux-20261001T000000Z", "vitamux-20261002T000000Z", "vitamux-20261003T000000Z", ".vitamux-20260901T000000Z.partial", ".vitamux-20261003T010000Z.partial", "vitamux-incomplete"} {
		_ = os.Mkdir(filepath.Join(dir, name), 0o700)
		if strings.HasPrefix(name, "vitamux-2026") {
			_ = os.WriteFile(filepath.Join(dir, name, ManifestFile), []byte("{}"), 0o600)
		}
	}
	old := now.Add(-48 * time.Hour)
	_ = os.Chtimes(filepath.Join(dir, ".vitamux-20260901T000000Z.partial"), old, old)

	removed, err := Prune(dir, 2, now)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(removed)
	if want := []string{".vitamux-20260901T000000Z.partial", "vitamux-20261001T000000Z"}; !slices.Equal(removed, want) {
		t.Errorf("removed %v, want %v", removed, want)
	}
	left, _ := os.ReadDir(dir)
	if len(left) != 4 { // two backups, the fresh partial, the directory without a manifest
		t.Errorf("%d entries left, want 4", len(left))
	}
}

func TestLibpq(t *testing.T) {
	conn, env, err := libpq("postgres://vitamux_app:s3cret@db:5432/vitamux?sslmode=disable&pool_max_conns=4")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(conn, "s3cret") || strings.Contains(conn, "pool_max_conns") || !strings.Contains(conn, "sslmode=disable") {
		t.Errorf("conn = %q", conn)
	}
	if !slices.Contains(env, "PGPASSWORD=s3cret") {
		t.Error("PGPASSWORD not in the environment")
	}
	if _, _, err := libpq("host=db password=s3cret"); err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("keyword DSN: err = %v", err)
	}
}

func TestWriteTar(t *testing.T) {
	dir := fakeBackup(t, keyring(t))
	var buf bytes.Buffer
	if err := WriteTar(&buf, dir); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(&buf)
	var names []string
	for h, err := tr.Next(); err == nil; h, err = tr.Next() {
		names = append(names, h.Name)
	}
	base := filepath.Base(dir)
	if want := []string{base + "/", base + "/" + DumpFile, base + "/" + BlobsFile, base + "/" + ManifestFile}; !slices.Equal(names, want) {
		t.Errorf("entries %v, want %v", names, want)
	}
}
