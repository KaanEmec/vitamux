package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// digest hashes every output file in a fixed order.
func digest(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	for _, name := range []string{"manifest.json", "sources.ndjson", "measurements.ndjson", "groups.ndjson", "sleep.ndjson", "revisions.ndjson"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(name))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// The pinned digests catch any change in output, including one caused by a platform difference
// (float fusing, host tzdata, map order). After an intended generator change, replace them with
// the values the failure message prints.
func TestDeterministic(t *testing.T) {
	cases := []struct {
		name, start string
		days        int
		want        string
	}{
		{"dst", "2025-03-28", 4, "b943979482a06edaede194011aad8175125647abf9df475e70be424fde2a30e9"},
		{"trip", "2025-05-09", 16, "aa02da98ba2f8af2d46e7fc5a04c636f470a2d337de5d2968a7123105a4efabe"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, b := t.TempDir(), t.TempDir()
			for _, dir := range []string{a, b} {
				if err := generate(dir, 42, c.start, c.days, 60); err != nil {
					t.Fatal(err)
				}
			}
			da, db := digest(t, a), digest(t, b)
			if da != db {
				t.Fatalf("two runs differ: %s vs %s", da, db)
			}
			if da != c.want {
				t.Fatalf("digest %s, pinned %s", da, c.want)
			}
		})
	}
}

func TestSeedChangesOutput(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if err := generate(a, 1, "2025-03-28", 2, 60); err != nil {
		t.Fatal(err)
	}
	if err := generate(b, 2, "2025-03-28", 2, 60); err != nil {
		t.Fatal(err)
	}
	if digest(t, a) == digest(t, b) {
		t.Fatal("different seeds gave identical output")
	}
}

// A subset of days must equal the same days of a longer run, so CI can use a 30-day slice.
func TestSubsetMatches(t *testing.T) {
	full, sub := t.TempDir(), t.TempDir()
	if err := generate(full, 42, "2025-03-29", 3, 60); err != nil {
		t.Fatal(err)
	}
	if err := generate(sub, 42, "2025-03-30", 1, 60); err != nil {
		t.Fatal(err)
	}
	fa, _ := os.ReadFile(filepath.Join(full, "measurements.ndjson"))
	fb, _ := os.ReadFile(filepath.Join(sub, "measurements.ndjson"))
	for _, line := range strings.Split(strings.TrimSpace(string(fb)), "\n")[1:] {
		if !strings.Contains(string(fa), line+"\n") {
			t.Fatalf("subset line missing from full run: %s", line)
		}
	}
}

// Every code must exist in internal/catalog (the v1 seed) with the kind and group the file
// claims and a plausible value, and every file carries the synthetic marker.
func TestMatchesCatalogue(t *testing.T) {
	dir := t.TempDir()
	if err := generate(dir, 42, "2025-02-10", 5, 60); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sources", "measurements", "groups", "sleep", "revisions"} {
		lines := readLines(t, filepath.Join(dir, name+".ndjson"))
		if !strings.HasPrefix(lines[0], `{"record":"header","synthetic": true,`) {
			t.Errorf("%s: missing synthetic header", name)
		}
		for _, line := range lines[1:] {
			var rec struct {
				Metric string  `json:"metric"`
				Kind   string  `json:"kind"`
				Value  float64 `json:"value"`
				Parts  []struct {
					Metric string  `json:"metric"`
					Value  float64 `json:"value"`
				} `json:"parts"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("%s: %v: %s", name, err, line)
			}
			check := func(code string, kind catalog.Kind, group string, v float64) {
				m, ok := catalog.Lookup(code)
				switch {
				case !ok:
					t.Errorf("%s: %q is not a catalogue code", name, code)
				case kind != "" && !slices.Contains(m.Kinds, kind):
					t.Errorf("%s: %q does not allow kind %q", name, code, kind)
				case group != m.Group && name == "groups":
					t.Errorf("%s: %q belongs to group %q, not %q", name, code, m.Group, group)
				case v < m.Min || v > m.Max:
					t.Errorf("%s: %q value %v outside %v..%v", name, code, v, m.Min, m.Max)
				}
			}
			if name == "measurements" {
				check(rec.Metric, catalog.Kind(rec.Kind), "", rec.Value)
			}
			for _, p := range rec.Parts {
				check(p.Metric, catalog.Sample, rec.Kind, p.Value)
			}
		}
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}
