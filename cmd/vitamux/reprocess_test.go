package main

import (
	"bytes"
	"testing"
	"time"
)

func TestParseTime(t *testing.T) {
	if got, err := parseTime("--since", ""); got != nil || err != nil {
		t.Errorf("empty: %v %v", got, err)
	}
	for in, want := range map[string]time.Time{
		"2026-06-15":           time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
		"2026-06-15T07:30:00Z": time.Date(2026, 6, 15, 7, 30, 0, 0, time.UTC),
	} {
		if got, err := parseTime("--since", in); err != nil || !got.Equal(want) {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	if _, err := parseTime("--since", "yesterday"); err == nil {
		t.Error("yesterday parsed")
	}
}

func TestReprocessRejectsBadFlags(t *testing.T) {
	for _, args := range [][]string{{"--since", "yesterday"}, {"extra"}, {"--nope"}} {
		var out, errOut bytes.Buffer
		if code := reprocess(args, &out, &errOut); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
