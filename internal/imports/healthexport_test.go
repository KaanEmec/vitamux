package imports

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KaanEmec/vitamux/internal/connectors/applehealth"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

type parsed struct {
	typ string
	rec applehealth.ExportRecord
}

func parseAll(t testing.TB, r io.Reader, lim Limits) ([]parsed, error) {
	t.Helper()
	var out []parsed
	err := ParseExport(r, lim, func(typ string, rec applehealth.ExportRecord) error {
		out = append(out, parsed{typ, rec})
		return nil
	})
	return out, err
}

func TestParseSyntheticExport(t *testing.T) {
	in, err := OpenExport(writeSyntheticExport(t, t.TempDir()), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	recs, err := parseAll(t, in, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != syntheticRecords {
		t.Fatalf("%d records, want %d", len(recs), syntheticRecords)
	}
	byType := map[string][]applehealth.ExportRecord{}
	for _, p := range recs {
		byType[p.typ] = append(byType[p.typ], p.rec)
	}
	bp := byType["HKCorrelationTypeIdentifierBloodPressure"]
	if len(bp) != 1 || len(bp[0].Records) != 2 || bp[0].Metadata["HKTimeZone"] != "Europe/Amsterdam" {
		t.Errorf("correlation: %+v", bp)
	}
	w := byType["HKWorkoutTypeIdentifier"]
	if len(w) != 1 || len(w[0].Statistics) != 3 || len(w[0].Metadata) != 2 {
		t.Errorf("workout: %+v", w) // the route's metadata entry is not the workout's
	}
	if hr := byType["HKQuantityTypeIdentifierHeartRate"]; hr[0].Attrs["device"] == "" || hr[0].Metadata["HKMetadataKeyHeartRateMotionContext"] != "1" {
		t.Errorf("heart rate: %+v", hr[0])
	}

	// Every mapped family normalizes; the top-level BP members only warn.
	var out normalize.Output
	for typ, rs := range byType {
		o := applehealth.ExportOutput(typ, rs)
		if err := o.Validate(); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		out.Measurements = append(out.Measurements, o.Measurements...)
		out.Groups = append(out.Groups, o.Groups...)
		out.Sleep = append(out.Sleep, o.Sleep...)
		out.Workouts = append(out.Workouts, o.Workouts...)
		out.Events = append(out.Events, o.Events...)
		out.Warnings = append(out.Warnings, o.Warnings...)
	}
	if len(out.Measurements) != 8 || len(out.Groups) != 2 || len(out.Sleep) != 1 || len(out.Sleep[0].Stages) != 5 ||
		len(out.Workouts) != 1 || len(out.Events) != 1 || len(out.Warnings) != 2 {
		t.Errorf("output: %d measurements, %d groups, %d sleep, %d workouts, %d events, warnings %v",
			len(out.Measurements), len(out.Groups), len(out.Sleep), len(out.Workouts), len(out.Events), out.Warnings)
	}
	if wk := out.Workouts[0]; wk.Sport != "running" || *wk.DistanceM != 5200 || *wk.EnergyKcal != 310.5 {
		t.Errorf("workout: %+v", wk)
	}
}

func TestExportIDIgnoresSpellingAndAddress(t *testing.T) {
	a := applehealth.ExportRecord{Attrs: map[string]string{"startDate": "2026-09-14 07:31:05 +0200", "endDate": "2026-09-14 07:31:05 +0200",
		"value": "61", "unit": "count/min", "sourceName": "W", "device": "<<HKDevice: 0x1>, name:W>"}}
	b := applehealth.ExportRecord{Attrs: map[string]string{"startDate": "2026-09-14 05:31:05 +0000", "endDate": "2026-09-14 05:31:05 +0000",
		"value": "61", "unit": "count/min", "sourceName": "W", "device": "<<HKDevice: 0xabc>, name:W>"}}
	typ := "HKQuantityTypeIdentifierHeartRate"
	if applehealth.ExportID(typ, a) != applehealth.ExportID(typ, b) {
		t.Error("the same instant and device must give the same id")
	}
	b.Attrs["value"] = "62"
	if applehealth.ExportID(typ, a) == applehealth.ExportID(typ, b) {
		t.Error("another value must give another id")
	}
}

func TestParseLimits(t *testing.T) {
	head := `<?xml version="1.0"?><HealthData>`
	rec := `<Record type="HKQuantityTypeIdentifierStepCount" sourceName="S" startDate="2026-09-14 08:00:00 +0200" endDate="2026-09-14 08:00:00 +0200" value="1" unit="count"/>`
	small := Limits{MaxXMLBytes: 1 << 20, MaxRatio: 10, MaxZipEntries: 3, MaxDepth: 4, MaxElements: 5, MaxAttrs: 8, MaxChildren: 2}
	for name, c := range map[string]struct {
		xml string
		ok  bool
	}{
		"fine":           {head + rec + `</HealthData>`, true},
		"too many":       {head + strings.Repeat(rec, 5) + `</HealthData>`, false},
		"too deep":       {head + `<a><b><c><d/></c></b></a></HealthData>`, false},
		"too many attrs": {head + `<Record a="1" b="1" c="1" d="1" e="1" f="1" g="1" h="1" i="1"/></HealthData>`, false},
		"metadata":       {head + `<Record type="T" sourceName="S"><MetadataEntry key="a" value="1"/><MetadataEntry key="b" value="1"/><MetadataEntry key="c" value="1"/></Record></HealthData>`, false},
		"truncated":      {head + rec, false},
		"wrong root":     {`<Other>` + rec + `</Other>`, false},
		"empty":          {``, false},
		"entity":         {head + `<Record type="&xxe;" sourceName="S"/></HealthData>`, false},
		"external dtd": {`<?xml version="1.0"?><!DOCTYPE HealthData SYSTEM "file:///etc/passwd" [<!ENTITY x SYSTEM "file:///etc/passwd">]>` +
			head[len(`<?xml version="1.0"?>`):] + `<Record type="T" sourceName="&x;"/></HealthData>`, false},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseAll(t, strings.NewReader(c.xml), small)
			if (err == nil) != c.ok {
				t.Errorf("err = %v, want ok %v", err, c.ok)
			}
		})
	}
	// The size cap fails instead of truncating.
	_, err := parseAll(t, capped(strings.NewReader(head+rec+`</HealthData>`), 40), small)
	if !errors.Is(err, ErrLimit) {
		t.Errorf("oversized: %v", err)
	}
}

func TestOpenExportZipLimits(t *testing.T) {
	write := func(t *testing.T, files map[string]string) string {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, body := range files {
			w, _ := zw.Create(name)
			_, _ = w.Write([]byte(body))
		}
		_ = zw.Close()
		p := filepath.Join(t.TempDir(), "x.zip")
		if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	lim := DefaultLimits
	lim.MaxRatio = 50
	bomb := `<HealthData>` + strings.Repeat(" ", 1<<20) + `</HealthData>` // ~1000:1
	if _, err := OpenExport(write(t, map[string]string{"apple_health_export/export.xml": bomb}), lim); !errors.Is(err, ErrLimit) {
		t.Errorf("bomb: %v", err)
	}
	if _, err := OpenExport(write(t, map[string]string{"a/export.xml": "<HealthData/>", "b/export.xml": "<HealthData/>"}), lim); err == nil {
		t.Error("two export.xml files")
	}
	if _, err := OpenExport(write(t, map[string]string{"export_cda.xml": "<x/>"}), lim); err == nil {
		t.Error("no export.xml")
	}
	lim.MaxZipEntries = 1
	if _, err := OpenExport(write(t, map[string]string{"export.xml": "<HealthData/>", "x": ""}), lim); !errors.Is(err, ErrLimit) {
		t.Errorf("entries: %v", err)
	}
	// A plain export.xml opens as is.
	p := filepath.Join(t.TempDir(), "export.xml")
	if err := os.WriteFile(p, []byte(syntheticExportXML()), 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := OpenExport(p, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	if recs, err := parseAll(t, in, DefaultLimits); err != nil || len(recs) != syntheticRecords {
		t.Errorf("plain xml: %d records, %v", len(recs), err)
	}
}

// FuzzParseExport feeds arbitrary bytes to the export parser under small limits. Invariants: no
// panic; every record it yields maps to output that passes the canonical writer's validation;
// a page of those records round-trips through the export normalizer.
func FuzzParseExport(f *testing.F) {
	f.Add([]byte(syntheticExportXML()))
	f.Add([]byte(`<HealthData><Record type="HKQuantityTypeIdentifierBodyMass" sourceName="S" unit="lb" value="1e308" startDate="2026-09-14 08:00:00 +0200" endDate="2026-09-14 08:00:00 +0200"/></HealthData>`))
	f.Add([]byte(`<HealthData><Workout workoutActivityType="HKWorkoutActivityTypeX" startDate="2026-09-14 08:00:00 +0200" endDate="2026-09-14 07:00:00 +0200"/></HealthData>`))
	lim := Limits{MaxXMLBytes: 1 << 20, MaxRatio: 100, MaxZipEntries: 10, MaxDepth: 8, MaxElements: 2000, MaxAttrs: 32, MaxChildren: 16}
	f.Fuzz(func(t *testing.T, data []byte) {
		pages := map[string][]applehealth.ExportRecord{}
		_ = ParseExport(bytes.NewReader(data), lim, func(typ string, rec applehealth.ExportRecord) error {
			if err := applehealth.ExportOutput(typ, []applehealth.ExportRecord{rec}).Validate(); err != nil {
				t.Fatalf("record maps to invalid output: %v", err)
			}
			pages[typ] = append(pages[typ], rec)
			return nil
		})
		for typ, recs := range pages {
			body, err := json.Marshal(applehealth.ExportPage{Type: typ, Records: recs[1:], Overlapping: recs[:1]})
			if err != nil {
				t.Fatal(err)
			}
			out, err := applehealth.ExportNormalizer{}.Normalize(t.Context(), normalize.RawPayload{Stream: applehealth.StreamExport, Body: body}, normalize.Env{})
			if err != nil {
				t.Fatalf("page does not normalize: %v", err)
			}
			if err := out.Validate(); err != nil {
				t.Fatalf("page output: %v", err)
			}
		}
	})
}
