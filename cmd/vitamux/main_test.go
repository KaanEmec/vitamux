package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCommands(t *testing.T) {
	cases := []struct {
		args []string
		code int
		out  string
	}{
		{[]string{"version"}, 0, "vitamux dev"},
		{[]string{"help"}, 0, "Commands:"},
		{nil, 2, "Usage:"},
		{[]string{"nope"}, 2, "unknown command"},
		{[]string{"migrate"}, 2, "usage: vitamux migrate"},
		{[]string{"migrate", "down-to", "x"}, 2, "usage: vitamux migrate"},
		{[]string{"admin"}, 2, "usage: vitamux admin"},
		{[]string{"keys"}, 2, "usage: vitamux keys"},
		{[]string{"keys", "nope"}, 2, "usage: vitamux keys"},
		{[]string{"keys", "rotate", "extra"}, 2, "usage: vitamux keys"},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		if code := run(c.args, &out, &errOut); code != c.code || !strings.Contains(out.String()+errOut.String(), c.out) {
			t.Errorf("%v: code %d output %q", c.args, code, out.String()+errOut.String())
		}
	}
}

func TestDirWritable(t *testing.T) {
	if err := dirWritable(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := dirWritable(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing directory must not be ready")
	}
}
