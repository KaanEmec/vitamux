package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KaanEmec/vitamux/internal/crypto"
)

func TestAdminUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"nope"}, {"init-secrets", "extra"}} {
		var out, errOut bytes.Buffer
		if code := admin(args, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage: vitamux admin") {
			t.Errorf("%v: code %d output %q", args, code, errOut.String())
		}
	}
}

func TestInitSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	var out, errOut bytes.Buffer
	if code := admin([]string{"init-secrets", "--out", path}, &out, &errOut); code != 0 {
		t.Fatalf("code %d: %s", code, errOut.String())
	}
	kr, err := crypto.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(out.String(), path) || !strings.Contains(out.String(), kr.KeyID()) || strings.Contains(out.String(), strings.TrimSpace(string(raw))) {
		t.Fatalf("unexpected output %q", out.String())
	}

	out.Reset()
	errOut.Reset()
	if code := admin([]string{"init-secrets", "--out", path}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "refusing to overwrite") {
		t.Fatalf("second run: code %d output %q", code, errOut.String())
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(raw, after) {
		t.Fatal("existing key was modified")
	}
	if code := admin([]string{"init-secrets", "--if-missing", "--out", path}, &out, &errOut); code != 0 {
		t.Fatalf("--if-missing on an existing key: code %d", code)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(raw, after) {
		t.Fatal("--if-missing modified the existing key")
	}
}

func TestInitSecretsDefaultPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VITAMUX_MASTER_KEY_FILE", "")
	t.Setenv("VITAMUX_DATA_DIR", dir)
	var out, errOut bytes.Buffer
	if code := initSecrets(nil, &out, &errOut); code != 0 {
		t.Fatalf("code %d: %s", code, errOut.String())
	}
	if _, err := crypto.Load(filepath.Join(dir, "master.key")); err != nil {
		t.Fatal(err)
	}
}

func pipeStdin(t *testing.T, input string) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	go func() {
		_, _ = w.WriteString(input)
		_ = w.Close()
	}()
	return r
}

func TestReadCredentialsFromPipe(t *testing.T) {
	user, pw, err := readCredentials(pipeStdin(t, " owner \r\npass word with spaces\r\n"), io.Discard)
	if err != nil || user != "owner" || pw != "pass word with spaces" {
		t.Fatalf("got %q %q %v", user, pw, err)
	}
	if _, _, err := readCredentials(pipeStdin(t, "owner\n"), io.Discard); err == nil {
		t.Fatal("a missing password line must fail")
	}
}

func TestInitSecretsSidecars(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VITAMUX_DATA_DIR", dir)
	t.Setenv("VITAMUX_SIDECARS", "example_sidecar=http://example-sidecar:8090,other=http://other:8080")
	other := filepath.Join(dir, "elsewhere", "other.secret")
	t.Setenv("VITAMUX_SIDECAR_OTHER_SECRET_FILE", other)
	key := filepath.Join(dir, "master.key")
	var out, errOut bytes.Buffer
	if code := initSecrets([]string{"--out", key}, &out, &errOut); code != 0 {
		t.Fatalf("code %d: %s", code, errOut.String())
	}
	def := filepath.Join(dir, "secrets", "sidecar-example_sidecar.secret")
	first := map[string][]byte{}
	for _, p := range []string{def, other} {
		st, err := os.Stat(p)
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", p, err, st)
		}
		b, _ := os.ReadFile(p)
		if len(strings.TrimSpace(string(b))) != 64 || strings.Contains(out.String(), strings.TrimSpace(string(b))) {
			t.Fatalf("%s: not a 32-byte hex secret, or printed", p)
		}
		first[p] = b
	}
	// A second run keeps the master key and every existing sidecar secret.
	if code := initSecrets([]string{"--if-missing", "--out", key}, &out, &errOut); code != 0 {
		t.Fatalf("second run: code %d: %s", code, errOut.String())
	}
	for p, b := range first {
		if after, _ := os.ReadFile(p); !bytes.Equal(b, after) {
			t.Fatalf("%s was overwritten", p)
		}
	}
}
