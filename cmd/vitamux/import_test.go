package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestImportUsage(t *testing.T) {
	for _, args := range [][]string{{"import"}, {"import", "csv", "x.zip"}, {"import", "ndjson"}, {"import", "ndjson", "a", "b"}, {"import", "apple-health-export"}, {"import", "apple-health-export", "--x", "a"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage: vitamux import ndjson") {
			t.Errorf("%v: exit %d, stderr %q", args, code, stderr.String())
		}
	}
}
