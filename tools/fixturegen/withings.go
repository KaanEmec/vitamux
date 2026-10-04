package main

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// withingsWriter renders the Withings sources of the world as getmeas responses
// (docs/providers/withings.md#measures-getmeas): withings/getmeas-NNNN.json pages of
// withingsPageSize groups, oldest first, chained by more/offset like one paged call, and
// withings/getmeas-corrections.json with the corrected groups a later lastupdate call returns.
// Deletions are not rendered: how Withings reports them is not documented. The few hundred
// groups of a year are held until Close.
type withingsWriter struct {
	dir    string
	groups []wgroup
	byExt  map[string]int
	revs   []wgroup
}

type wgroup struct {
	g        Group
	modified time.Time
}

const withingsPageSize = 100

// Measure types and units of each part (value×10^unit; Num.Dec is the negated unit).
var withingsType = map[string]int{
	"bp_systolic": 10, "bp_diastolic": 9, "bp_pulse": 11, "weight": 1, "body_fat_ratio": 6,
	"fat_mass": 8, "muscle_mass": 76, "bone_mass": 88, "hydration": 77,
}

func newWithingsWriter(dir string) (*withingsWriter, error) {
	dir = filepath.Join(dir, "withings")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &withingsWriter{dir: dir, byExt: map[string]int{}}, nil
}

func (w *withingsWriter) Source(Source) error           { return nil }
func (w *withingsWriter) Measurement(Measurement) error { return nil } // no Withings plain samples in the world
func (w *withingsWriter) Sleep(Sleep) error             { return nil }

func (w *withingsWriter) Group(g Group) error {
	if g.Src.Provider != "withings" {
		return nil
	}
	w.byExt[g.Ext] = len(w.groups)
	w.groups = append(w.groups, wgroup{g: g, modified: g.At.Add(time.Minute)})
	return nil
}

func (w *withingsWriter) Revision(r Revision) error {
	if r.Src.Provider != "withings" || r.Op != "correct" || r.Record != "group" {
		return nil
	}
	i, ok := w.byExt[r.Ext]
	if !ok {
		return fmt.Errorf("withings: revision of unknown group %s", r.Ext)
	}
	fixed := wgroup{g: w.groups[i].g, modified: r.At}
	fixed.g.Parts = append([]Part(nil), fixed.g.Parts...)
	for j := range fixed.g.Parts {
		if fixed.g.Parts[j].Metric == r.Metric {
			fixed.g.Parts[j].Val = r.Val
		}
	}
	w.revs = append(w.revs, fixed)
	return nil
}

// grpid is a stable synthetic id: the same group gets the same id in any subset of days.
func grpid(ext string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(ext))
	return int64(h.Sum64() & (1<<47 - 1))
}

func (w *withingsWriter) Close() error {
	sort.SliceStable(w.groups, func(i, j int) bool { return w.groups[i].g.At.Before(w.groups[j].g.At) })
	pages := (len(w.groups) + withingsPageSize - 1) / withingsPageSize
	for p := range max(pages, 1) {
		lo, hi := p*withingsPageSize, min((p+1)*withingsPageSize, len(w.groups))
		more, offset := 0, 0
		if hi < len(w.groups) {
			more, offset = 1, hi
		}
		name := fmt.Sprintf("getmeas-%04d.json", p+1)
		if err := w.page(name, w.groups[lo:hi], more, offset); err != nil {
			return err
		}
	}
	return w.page("getmeas-corrections.json", w.revs, 0, 0)
}

// page writes one response by hand, like the canonical writer, so bytes never depend on
// encoding/json. updatetime is the newest modification on the page.
func (w *withingsWriter) page(name string, gs []wgroup, more, offset int) error {
	var update time.Time
	for _, g := range gs {
		if g.modified.After(update) {
			update = g.modified
		}
	}
	b := []byte(`{"synthetic": true,"status":0,"body":{"updatetime":`)
	b = strconv.AppendInt(b, update.Unix(), 10)
	b = append(b, `,"timezone":"`+homeTZ+`","measuregrps":[`...)
	for i, g := range gs {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendWithingsGroup(b, g)
	}
	b = append(b, `],"more":`...)
	b = strconv.AppendInt(b, int64(more), 10)
	b = append(b, `,"offset":`...)
	b = strconv.AppendInt(b, int64(offset), 10)
	b = append(b, "}}\n"...)
	return os.WriteFile(filepath.Join(w.dir, name), b, 0o600)
}

func appendWithingsGroup(b []byte, wg wgroup) []byte {
	g, modelID := wg.g, 6 // Body Cardio
	if g.Kind == "bp_reading" {
		modelID = 45 // BPM Connect
	}
	b = append(b, `{"grpid":`...)
	b = strconv.AppendInt(b, grpid(g.Ext), 10)
	b = append(b, `,"attrib":0,"date":`...)
	b = strconv.AppendInt(b, g.At.Unix(), 10)
	b = append(b, `,"created":`...)
	b = strconv.AppendInt(b, g.At.Add(time.Minute).Unix(), 10)
	b = append(b, `,"modified":`...)
	b = strconv.AppendInt(b, wg.modified.Unix(), 10)
	b = append(b, `,"category":1,`...)
	b = appendStr(b, "deviceid", g.Src.Fingerprint)
	b = append(b, ',')
	b = appendStr(b, "hash_deviceid", g.Src.Fingerprint)
	b = append(b, `,"measures":[`...)
	for i, p := range g.Parts {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, `{"value":`...)
		b = strconv.AppendInt(b, p.Val.V, 10)
		b = append(b, `,"type":`...)
		b = strconv.AppendInt(b, int64(withingsType[p.Metric]), 10)
		b = append(b, `,"unit":`...)
		b = strconv.AppendInt(b, -int64(p.Val.Dec), 10)
		b = append(b, '}')
	}
	b = append(b, "],"...)
	b = appendStr(b, "model", g.Src.Model)
	b = append(b, `,"modelid":`...)
	b = strconv.AppendInt(b, int64(modelID), 10)
	return append(b, `,"comment":null}`...)
}
