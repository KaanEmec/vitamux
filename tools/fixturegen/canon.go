package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// header is the first line of every NDJSON file and the body of manifest.json. The synthetic
// marker is what the fixture privacy guard (J04.2) requires.
type header struct {
	Seed  uint64
	Start string
	Days  int
}

const schema = "vitamux.fixtures.canonical/1"

// canonicalWriter writes the world as canonical truth: five NDJSON files (one record per line,
// fixed key order) plus manifest.json with record counts. Lines are built by hand so formatting
// never depends on encoding/json or float printing.
type canonicalWriter struct {
	dir   string
	hdr   header
	files map[string]*ndjson
	order []string
}

type ndjson struct {
	f   *os.File
	w   *bufio.Writer
	buf []byte
	n   int
}

func newCanonicalWriter(dir string, h header) (*canonicalWriter, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	c := &canonicalWriter{dir: dir, hdr: h, files: map[string]*ndjson{}}
	for _, name := range []string{"sources", "measurements", "groups", "sleep", "revisions"} {
		f, err := os.Create(filepath.Join(dir, name+".ndjson")) //nolint:gosec // output dir is a flag
		if err != nil {
			return nil, err
		}
		n := &ndjson{f: f, w: bufio.NewWriterSize(f, 1<<20)}
		c.files[name] = n
		c.order = append(c.order, name)
		line := fmt.Sprintf(`{"record":"header","synthetic": true,"schema":"%s","file":"%s","seed":%d,"start":"%s","days":%d}`+"\n",
			schema, name, h.Seed, h.Start, h.Days)
		if _, err := n.w.WriteString(line); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// emit writes the line the caller built in n.buf.
func (n *ndjson) emit() error {
	n.buf = append(n.buf, '\n')
	n.n++
	_, err := n.w.Write(n.buf)
	return err
}

func (n *ndjson) start() []byte { return n.buf[:0] }

func appendStr(b []byte, key, v string) []byte {
	b = append(b, '"')
	b = append(b, key...)
	b = append(b, `":"`...)
	b = append(b, v...)
	return append(b, '"')
}

func appendInt(b []byte, key string, v int) []byte {
	b = append(b, '"')
	b = append(b, key...)
	b = append(b, `":`...)
	return strconv.AppendInt(b, int64(v), 10)
}

func appendTime(b []byte, key string, t time.Time) []byte {
	b = append(b, '"')
	b = append(b, key...)
	b = append(b, `":"`...)
	b = t.UTC().AppendFormat(b, "2006-01-02T15:04:05Z")
	return append(b, '"')
}

// appendNum writes v with exactly Dec decimals.
func appendNum(b []byte, v Num) []byte {
	if v.Dec == 0 {
		return strconv.AppendInt(b, v.V, 10)
	}
	if v.V < 0 {
		b = append(b, '-')
		v.V = -v.V
	}
	p := int64(1)
	for range v.Dec {
		p *= 10
	}
	b = strconv.AppendInt(b, v.V/p, 10)
	b = append(b, '.')
	frac := strconv.AppendInt(nil, v.V%p, 10)
	for i := len(frac); i < int(v.Dec); i++ {
		b = append(b, '0')
	}
	return append(b, frac...)
}

func (c *canonicalWriter) Source(s Source) error {
	n := c.files["sources"]
	b := append(n.start(), `{"record":"source",`...)
	b = appendStr(b, "key", s.Key)
	b = append(b, ',')
	b = appendStr(b, "provider", s.Provider)
	for _, kv := range [][2]string{{"device_type", s.DeviceType}, {"fingerprint", s.Fingerprint}, {"model", s.Model}, {"origin_key", s.OriginKey}, {"relayed_provider", s.RelayedProvider}} {
		if kv[1] != "" {
			b = append(b, ',')
			b = appendStr(b, kv[0], kv[1])
		}
	}
	n.buf = append(b, '}')
	return n.emit()
}

func (c *canonicalWriter) Measurement(m Measurement) error {
	n := c.files["measurements"]
	b := append(n.start(), '{')
	b = appendStr(b, "src", m.Src.Key)
	b = append(b, ',')
	b = appendStr(b, "metric", m.Metric)
	b = append(b, ',')
	b = appendStr(b, "kind", m.Kind)
	b = append(b, ',')
	b = appendTime(b, "start", m.Start)
	if m.Kind != "sample" {
		b = append(b, ',')
		b = appendTime(b, "end", m.End)
	}
	b = append(b, ',')
	b = appendInt(b, "tz", m.Offset)
	b = append(b, ',')
	b = appendStr(b, "date", m.Date)
	b = append(b, `,"value":`...)
	b = appendNum(b, m.Val)
	if m.Ext != "" {
		b = append(b, ',')
		b = appendStr(b, "ext", m.Ext)
	}
	b = appendFlags(b, m.Flags)
	n.buf = append(b, '}')
	return n.emit()
}

func appendFlags(b []byte, f uint8) []byte {
	if f == 0 {
		return b
	}
	b = append(b, `,"flags":[`...)
	sep := ""
	for _, fl := range []struct {
		bit  uint8
		name string
	}{{flagManual, "manual_entry"}, {flagRelayed, "relayed"}} {
		if f&fl.bit != 0 {
			b = append(b, sep...)
			b = append(b, '"')
			b = append(b, fl.name...)
			b = append(b, '"')
			sep = ","
		}
	}
	return append(b, ']')
}

func (c *canonicalWriter) Group(g Group) error {
	n := c.files["groups"]
	b := append(n.start(), '{')
	b = appendStr(b, "src", g.Src.Key)
	b = append(b, ',')
	b = appendStr(b, "kind", g.Kind)
	b = append(b, ',')
	b = appendTime(b, "at", g.At)
	b = append(b, ',')
	b = appendInt(b, "tz", g.Offset)
	b = append(b, ',')
	b = appendStr(b, "date", g.Date)
	b = append(b, ',')
	b = appendStr(b, "ext", g.Ext)
	b = append(b, `,"context":`...)
	b = append(b, g.Context...)
	b = append(b, `,"parts":[`...)
	for i, p := range g.Parts {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '{')
		b = appendStr(b, "metric", p.Metric)
		b = append(b, `,"value":`...)
		b = appendNum(b, p.Val)
		b = append(b, '}')
	}
	n.buf = append(b, `]}`...)
	return n.emit()
}

func (c *canonicalWriter) Sleep(s Sleep) error {
	n := c.files["sleep"]
	b := append(n.start(), '{')
	b = appendStr(b, "src", s.Src.Key)
	b = append(b, ',')
	b = appendStr(b, "ext", s.Ext)
	b = append(b, ',')
	b = appendTime(b, "start", s.Start)
	b = append(b, ',')
	b = appendTime(b, "end", s.End)
	b = append(b, ',')
	b = appendInt(b, "tz", s.Offset)
	b = append(b, ',')
	b = appendStr(b, "date", s.Date)
	b = append(b, `,"nap":`...)
	b = strconv.AppendBool(b, s.Nap)
	b = append(b, `,"has_stages":`...)
	b = strconv.AppendBool(b, s.HasStages)
	b = append(b, ',')
	b = appendStr(b, "totals_basis", s.Basis)
	for _, kv := range []struct {
		k string
		v int
	}{{"asleep_s", s.AsleepS}, {"deep_s", s.DeepS}, {"light_s", s.LightS}, {"rem_s", s.REMS}, {"awake_s", s.AwakeS}, {"latency_s", s.LatS}} {
		b = append(b, ',')
		if kv.v < 0 {
			b = append(b, '"')
			b = append(b, kv.k...)
			b = append(b, `":null`...)
		} else {
			b = appendInt(b, kv.k, kv.v)
		}
	}
	b = append(b, `,"stages":[`...)
	for i, st := range s.Stages {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '{')
		b = appendStr(b, "stage", st.Name)
		b = append(b, ',')
		b = appendTime(b, "start", st.Start)
		b = append(b, ',')
		b = appendTime(b, "end", st.End)
		b = append(b, '}')
	}
	n.buf = append(b, `]}`...)
	return n.emit()
}

func (c *canonicalWriter) Revision(r Revision) error {
	n := c.files["revisions"]
	b := append(n.start(), '{')
	b = appendStr(b, "op", r.Op)
	b = append(b, ',')
	b = appendTime(b, "at", r.At)
	b = append(b, ',')
	b = appendStr(b, "record", r.Record)
	b = append(b, ',')
	b = appendStr(b, "src", r.Src.Key)
	if r.Metric != "" {
		b = append(b, ',')
		b = appendStr(b, "metric", r.Metric)
	}
	b = append(b, ',')
	b = appendStr(b, "ext", r.Ext)
	if r.Op == "correct" {
		b = append(b, `,"value":`...)
		b = appendNum(b, r.Val)
	}
	n.buf = append(b, '}')
	return n.emit()
}

// Close flushes every file and writes manifest.json.
func (c *canonicalWriter) Close() error {
	var counts strings.Builder
	for i, name := range c.order {
		n := c.files[name]
		if err := n.w.Flush(); err != nil {
			return err
		}
		if err := n.f.Close(); err != nil {
			return err
		}
		if i > 0 {
			counts.WriteString(",")
		}
		fmt.Fprintf(&counts, "\n    %q: %d", name, n.n)
	}
	manifest := fmt.Sprintf("{\n  \"synthetic\": true,\n  \"schema\": %q,\n  \"seed\": %d,\n  \"start\": %q,\n  \"days\": %d,\n  \"records\": {%s\n  }\n}\n",
		schema, c.hdr.Seed, c.hdr.Start, c.hdr.Days, counts.String())
	return os.WriteFile(filepath.Join(c.dir, "manifest.json"), []byte(manifest), 0o600)
}
