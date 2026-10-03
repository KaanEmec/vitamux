package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type wpage struct {
	Synthetic bool `json:"synthetic"`
	Status    int  `json:"status"`
	Body      struct {
		UpdateTime  int64  `json:"updatetime"`
		Timezone    string `json:"timezone"`
		More        int    `json:"more"`
		Offset      int    `json:"offset"`
		MeasureGrps []struct {
			GrpID    int64 `json:"grpid"`
			Date     int64 `json:"date"`
			Category int   `json:"category"`
			Measures []struct {
				Value int64 `json:"value"`
				Type  int   `json:"type"`
				Unit  int   `json:"unit"`
			} `json:"measures"`
		} `json:"measuregrps"`
	} `json:"body"`
}

func readPage(t *testing.T, path string) wpage {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test output dir
	if err != nil {
		t.Fatal(err)
	}
	var p wpage
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return p
}

// The Withings pages render exactly the Withings groups of the canonical files, chained by
// more/offset, deterministically, with known meastypes and the synthetic marker.
func TestWithingsPages(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, dir := range []string{a, b} {
		if err := generate(dir, 42, "2025-01-20", 70, 600); err != nil {
			t.Fatal(err)
		}
	}
	pages, _ := filepath.Glob(filepath.Join(a, "withings", "getmeas-0*.json"))
	if len(pages) < 2 {
		t.Fatalf("want several pages, got %d", len(pages))
	}
	known := map[int]bool{}
	for _, v := range withingsType {
		known[v] = true
	}
	ids, offset := map[int64]bool{}, 0
	for i, path := range pages {
		other, _ := os.ReadFile(filepath.Join(b, "withings", filepath.Base(path))) //nolint:gosec // test output dir
		mine, _ := os.ReadFile(path)                                               //nolint:gosec // test output dir
		if !bytes.Equal(mine, other) {
			t.Fatalf("%s differs between runs", filepath.Base(path))
		}
		p := readPage(t, path)
		if !p.Synthetic || p.Status != 0 || p.Body.Timezone != homeTZ {
			t.Fatalf("%s: bad envelope", filepath.Base(path))
		}
		if last := i == len(pages)-1; last != (p.Body.More == 0) {
			t.Fatalf("%s: more=%d", filepath.Base(path), p.Body.More)
		}
		offset += len(p.Body.MeasureGrps)
		if p.Body.More == 1 && p.Body.Offset != offset {
			t.Fatalf("%s: offset %d, want %d", filepath.Base(path), p.Body.Offset, offset)
		}
		for _, g := range p.Body.MeasureGrps {
			if ids[g.GrpID] || g.Category != 1 || len(g.Measures) == 0 {
				t.Fatalf("group %d: duplicate, wrong category or empty", g.GrpID)
			}
			ids[g.GrpID] = true
			for _, m := range g.Measures {
				if !known[m.Type] {
					t.Fatalf("group %d: unknown type %d", g.GrpID, m.Type)
				}
			}
		}
	}
	want := 0
	for _, line := range readLines(t, filepath.Join(a, "groups.ndjson"))[1:] {
		var g struct {
			Src string `json:"src"`
		}
		if err := json.Unmarshal([]byte(line), &g); err != nil {
			t.Fatal(err)
		}
		if g.Src == bpMonitor.Key || g.Src == scale.Key {
			want++
		}
	}
	if len(ids) != want {
		t.Fatalf("pages hold %d groups, canonical has %d Withings groups", len(ids), want)
	}
	fix := readPage(t, filepath.Join(a, "withings", "getmeas-corrections.json"))
	if len(fix.Body.MeasureGrps) != 1 || !ids[fix.Body.MeasureGrps[0].GrpID] {
		t.Fatalf("corrections page: want the one corrected BP group, got %d groups", len(fix.Body.MeasureGrps))
	}
}
