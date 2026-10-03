package crypto

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newKeyFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "master.key")
	if _, err := WriteKeyFile(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func newKeyring(t *testing.T, previous ...string) *Keyring {
	t.Helper()
	kr, err := Load(newKeyFile(t), previous...)
	if err != nil {
		t.Fatal(err)
	}
	return kr
}

var aad = []byte("credentials:conn-1")

func TestRoundTrip(t *testing.T) {
	kr := newKeyring(t)
	for _, pt := range [][]byte{[]byte("synthetic refresh token"), {}} {
		sealed, err := kr.Seal(Credentials, pt, aad)
		if err != nil {
			t.Fatal(err)
		}
		got, err := kr.Open(Credentials, sealed, aad)
		if err != nil || !bytes.Equal(got, pt) {
			t.Fatalf("got %q, %v", got, err)
		}
	}
	a, _ := kr.Seal(Credentials, []byte("x"), aad)
	b, _ := kr.Seal(Credentials, []byte("x"), aad)
	if bytes.Equal(a, b) {
		t.Fatal("nonce reused")
	}
}

func TestTamper(t *testing.T) {
	kr := newKeyring(t)
	sealed, err := kr.Seal(Credentials, []byte("synthetic"), aad)
	if err != nil {
		t.Fatal(err)
	}
	for i := range sealed {
		bad := bytes.Clone(sealed)
		bad[i] ^= 0x01
		if _, err := kr.Open(Credentials, bad, aad); err == nil {
			t.Fatalf("flipping a bit in byte %d was accepted", i)
		}
	}
	if _, err := kr.Open(Credentials, sealed, []byte("credentials:conn-2")); !errors.Is(err, ErrOpen) {
		t.Fatalf("wrong aad: %v", err)
	}
	if _, err := kr.Open(Credentials, sealed[:len(sealed)-1], aad); !errors.Is(err, ErrOpen) {
		t.Fatalf("truncated: %v", err)
	}
	if _, err := kr.Open(Credentials, sealed[:headerSize], aad); !errors.Is(err, ErrOpen) {
		t.Fatalf("header only: %v", err)
	}
}

func TestWrongPurpose(t *testing.T) {
	kr := newKeyring(t)
	sealed, _ := kr.Seal(Credentials, []byte("synthetic"), aad)
	if _, err := kr.Open(Documents, sealed, aad); !errors.Is(err, ErrOpen) {
		t.Fatalf("got %v", err)
	}
	if _, err := kr.Seal("nope", nil, nil); !errors.Is(err, ErrPurpose) {
		t.Fatalf("got %v", err)
	}
	if _, err := kr.Open("nope", sealed, aad); !errors.Is(err, ErrPurpose) {
		t.Fatalf("got %v", err)
	}
}

func TestUnknownKeyID(t *testing.T) {
	sealed, _ := newKeyring(t).Seal(Credentials, []byte("synthetic"), aad)
	if _, err := newKeyring(t).Open(Credentials, sealed, aad); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("got %v", err)
	}
}

func TestPreviousKey(t *testing.T) {
	oldPath := newKeyFile(t)
	old, err := Load(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := old.Seal(Credentials, []byte("synthetic"), aad)

	rotated, err := Load(newKeyFile(t), oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.KeyID() == old.KeyID() {
		t.Fatal("current key must be the new one")
	}
	if got, err := rotated.Open(Credentials, sealed, aad); err != nil || string(got) != "synthetic" {
		t.Fatalf("got %q, %v", got, err)
	}
	fresh, _ := rotated.Seal(Credentials, []byte("synthetic"), aad)
	if id := fresh[1:headerSize]; !bytes.Equal(id, rotated.current.id[:]) {
		t.Fatal("new values must use the current key")
	}
	if _, err := old.Open(Credentials, fresh, aad); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("old keyring opened new value: %v", err)
	}
}

func TestKeyIDStable(t *testing.T) {
	path := newKeyFile(t)
	a, _ := Load(path)
	b, _ := Load(path)
	if a.KeyID() != b.KeyID() || len(a.KeyID()) != 2*keyIDSize {
		t.Fatalf("ids %q %q", a.KeyID(), b.KeyID())
	}
	other := newKeyring(t)
	if other.KeyID() == a.KeyID() {
		t.Fatal("distinct keys share an id")
	}
	id, err := WriteKeyFile(filepath.Join(t.TempDir(), "k"))
	if err != nil || len(id) != 2*keyIDSize {
		t.Fatalf("WriteKeyFile id %q, %v", id, err)
	}
}

func TestPurposeKeys(t *testing.T) {
	kr := newKeyring(t)
	seen := map[string]Purpose{}
	for _, p := range purposes {
		k, err := kr.PurposeKey(p)
		if err != nil || len(k) != keySize {
			t.Fatalf("%s: %v", p, err)
		}
		if q, dup := seen[string(k)]; dup {
			t.Fatalf("%s and %s share a key", p, q)
		}
		seen[string(k)] = p
	}
	k, _ := kr.PurposeKey(SessionSigning)
	k[0] ^= 0xff
	k2, _ := kr.PurposeKey(SessionSigning)
	if k[0] == k2[0] {
		t.Fatal("PurposeKey returned shared storage")
	}
	if _, err := kr.PurposeKey("nope"); !errors.Is(err, ErrPurpose) {
		t.Fatalf("got %v", err)
	}
}

func TestLoadRejects(t *testing.T) {
	good := strings.Repeat("ab", 32)
	cases := map[string]struct {
		content string
		mode    os.FileMode
	}{
		"group readable": {good, 0o640},
		"world readable": {good, 0o604},
		"too short":      {good[:62], 0o600},
		"too long":       {good + "00", 0o600},
		"empty":          {"", 0o600},
		"not hex":        {strings.Repeat("zz", 32), 0o600},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "k")
			if err := os.WriteFile(path, []byte(c.content), c.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, c.mode); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Fatal("expected error")
			}
			if c.content != "" && strings.Contains(err.Error(), c.content[:8]) {
				t.Fatalf("error leaks key material: %v", err)
			}
		})
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("directory accepted")
	}
	path := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(path, []byte(good+"\r\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("0400 with trailing newline: %v", err)
	}
	if _, err := Load(path, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing previous key accepted")
	}
}

func TestWriteKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "master.key")
	if _, err := WriteKeyFile(path); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v", st.Mode(), err)
	}
	before, _ := os.ReadFile(path)
	if _, err := WriteKeyFile(path); !errors.Is(err, os.ErrExist) {
		t.Fatalf("overwrite: %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("existing key was modified")
	}
}
