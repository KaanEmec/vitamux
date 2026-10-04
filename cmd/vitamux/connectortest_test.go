package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectorTestCmd(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("synthetic-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		args []string
		code int
		out  string
	}{
		"no flags":         {[]string{"connector-test"}, 2, ""},
		"unreadable":       {[]string{"connector-test", "--url", "http://127.0.0.1:1", "--secret-file", secret + ".missing"}, 1, ""},
		"bad url":          {[]string{"connector-test", "--url", "ftp://x", "--secret-file", secret}, 1, ""},
		"unreachable":      {[]string{"connector-test", "--url", "http://127.0.0.1:1", "--secret-file", secret}, 1, "FAIL  protocol_header"},
		"bad scenario":     {[]string{"connector-test", "--url", "http://127.0.0.1:1", "--secret-file", secret, "--scenario", secret}, 1, ""},
		"unknown argument": {[]string{"connector-test", "--url", "http://127.0.0.1:1", "--secret-file", secret, "extra"}, 2, ""},
	} {
		var stdout, stderr bytes.Buffer
		if got := run(tc.args, &stdout, &stderr); got != tc.code || !strings.Contains(stdout.String(), tc.out) {
			t.Errorf("%s: exit %d, want %d; stdout %q stderr %q", name, got, tc.code, stdout.String(), stderr.String())
		}
	}
}
