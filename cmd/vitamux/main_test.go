package main

import (
	"bytes"
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
		{[]string{"admin"}, 1, "not implemented"},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		if code := run(c.args, &out, &errOut); code != c.code || !strings.Contains(out.String()+errOut.String(), c.out) {
			t.Errorf("%v: code %d output %q", c.args, code, out.String()+errOut.String())
		}
	}
}
