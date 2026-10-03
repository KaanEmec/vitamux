package blob

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/KaanEmec/vitamux/internal/crypto"
)

func testKeyring(t *testing.T, dir string, previous ...string) (*crypto.Keyring, string) {
	t.Helper()
	path := filepath.Join(dir, "master-"+strings.Repeat("x", len(previous))+".key")
	if _, err := crypto.WriteKeyFile(path); err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.Load(path, previous...)
	if err != nil {
		t.Fatal(err)
	}
	return kr, path
}

// putFile stores content without the database, as Put does after its row insert.
func putFile(t *testing.T, s *Store, content []byte, mode Mode) []byte {
	t.Helper()
	f, err := os.CreateTemp(filepath.Join(s.dir, tmpDir), "put-*")
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.writeTmp(f, bytes.NewReader(content), mode)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := s.install(f.Name(), s.path(info.SHA256)); err != nil {
		t.Fatal(err)
	}
	return info.SHA256
}

func synthetic(n int) []byte {
	return bytes.Repeat([]byte(`{"synthetic":true,"value":61},`), n)
}

func TestRoundTripAndCorruption(t *testing.T) {
	for _, mode := range []Mode{Plain, Sealed} {
		dir := t.TempDir()
		kr, _ := testKeyring(t, t.TempDir())
		s, err := Open(dir, kr)
		if err != nil {
			t.Fatal(err)
		}
		content := synthetic(5000)
		sum := putFile(t, s, content, mode)
		if want := sha256.Sum256(content); !bytes.Equal(sum, want[:]) {
			t.Fatalf("mode %d: sum mismatch", mode)
		}
		got, err := s.Get(sum)
		if err != nil || !bytes.Equal(got, content) {
			t.Fatalf("mode %d: round trip failed: %v", mode, err)
		}
		path := s.path(sum)
		if strings.Contains(path, hex.EncodeToString(sum)) {
			t.Fatalf("mode %d: file name reveals the content hash", mode)
		}
		raw, _ := os.ReadFile(path)
		if len(raw) >= len(content)/10 {
			t.Fatalf("mode %d: not compressed (%d bytes)", mode, len(raw))
		}

		// A flipped byte is caught by zstd, GCM or the hash.
		raw[len(raw)/2] ^= 0xff
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(sum); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("mode %d: flipped byte: got %v, want ErrCorrupt", mode, err)
		}
	}
}

func TestValidStreamWithWrongContentIsCorrupt(t *testing.T) {
	kr, _ := testKeyring(t, t.TempDir())
	s, err := Open(t.TempDir(), kr)
	if err != nil {
		t.Fatal(err)
	}
	sum := putFile(t, s, synthetic(10), Plain)
	enc, _ := zstd.NewWriter(nil)
	other := append([]byte{formatZstd}, enc.EncodeAll(synthetic(11), nil)...)
	if err := os.WriteFile(s.path(sum), other, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(sum); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("got %v, want ErrCorrupt", err)
	}
	if _, err := s.Get(make([]byte, 32)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing blob: got %v, want ErrNotFound", err)
	}
}

func TestNamesSurviveMasterKeyRotation(t *testing.T) {
	keys, dir := t.TempDir(), t.TempDir()
	oldKR, oldPath := testKeyring(t, keys)
	s, err := Open(dir, oldKR)
	if err != nil {
		t.Fatal(err)
	}
	sum := putFile(t, s, synthetic(3), Plain)

	newKR, newPath := testKeyring(t, keys, oldPath)
	if _, err := Open(dir, newKR); err != nil {
		t.Fatal(err)
	}
	// names.key is now sealed with the new key alone.
	onlyNew, err := crypto.Load(newPath)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir, onlyNew)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Get(sum); err != nil {
		t.Fatalf("blob not found after rotation: %v", err)
	}
}

func TestMissingNamesKeyWithBlobsRefuses(t *testing.T) {
	kr, _ := testKeyring(t, t.TempDir())
	dir := t.TempDir()
	s, err := Open(dir, kr)
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, s, synthetic(1), Plain)
	if err := os.Remove(filepath.Join(dir, namesKeyFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, kr); err == nil {
		t.Fatal("Open created a new names key over existing blobs")
	}
}
