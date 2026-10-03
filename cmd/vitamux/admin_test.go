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
