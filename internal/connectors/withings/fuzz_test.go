package withings

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/normalize"
)

// FuzzNormalizeMeasures feeds arbitrary raw records to the measures normalizer. CI runs it
// briefly and nightly for longer (docs/plan/E13-hardening/J13.2-fuzzing-limits.md).
// Invariants: no panic; the output is deterministic; an output that passes the writer's
// validation marshals (finite numbers).
func FuzzNormalizeMeasures(f *testing.F) {
	files, err := filepath.Glob(filepath.Join("testdata", StreamMeasures, "*.raw.json"))
	if err != nil || len(files) == 0 {
		f.Fatalf("no seed records: %v", err)
	}
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte(`{"measuregrp":{"grpid":1,"date":1,"category":1,"measures":[{"value":1,"type":1,"unit":9999}]}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		raw := normalize.RawPayload{Stream: StreamMeasures, ContentType: "application/json", ExternalKey: "fuzz", Body: data}
		out, err := Normalizer{}.Normalize(t.Context(), raw, normalize.Env{Provider: Provider})
		again, err2 := Normalizer{}.Normalize(t.Context(), raw, normalize.Env{Provider: Provider})
		if (err == nil) != (err2 == nil) {
			t.Fatal("error is not deterministic")
		}
		if err != nil {
			return
		}
		a, aerr := json.Marshal(out)
		b, berr := json.Marshal(again)
		if (aerr == nil) != (berr == nil) || !bytes.Equal(a, b) {
			t.Fatal("output is not deterministic")
		}
		if aerr != nil && out.Validate() == nil {
			t.Fatalf("output passes validation but does not marshal: %v", aerr)
		}
	})
}

// FuzzParseNotification feeds arbitrary form bodies to the notification parser. Invariants: no
// panic; a usable window starts at or after the epoch, ends no earlier and spans at most
// maxNotifyRange, so the sync window built from it cannot overflow.
func FuzzParseNotification(f *testing.F) {
	f.Add("userid=synthetic-user&appli=1&startdate=1735711500&enddate=1735711560")
	f.Add("appli=4&startdate=0&enddate=10000000000")
	f.Add("startdate=-9223372036854775808&enddate=9223372036854775807&appli=2")
	f.Add("%zz=&&=;appli")
	f.Fuzz(func(t *testing.T, body string) {
		form, _ := url.ParseQuery(body) // what ParseForm does with a POST body; errors leave the rest
		n := parseNotification(form)
		if !n.windowOK {
			return
		}
		if n.start < 0 || n.end < n.start || time.Duration(n.end-n.start)*time.Second > maxNotifyRange {
			t.Fatalf("window accepted: %d..%d", n.start, n.end)
		}
		if from, to := time.Unix(n.start, 0), time.Unix(n.end+1, 0); !to.After(from) {
			t.Fatalf("window %d..%d does not make a forward range", n.start, n.end)
		}
	})
}

// A notification must not pass the window check by overflowing the Duration product: the
// span below is 1e10 s, whose nanoseconds do not fit an int64.
func TestParseNotificationWindow(t *testing.T) {
	for _, c := range []struct {
		name, form string
		ok         bool
	}{
		{"a few seconds", "startdate=1735711500&enddate=1735711560", true},
		{"31 days", "startdate=0&enddate=2678400", true},
		{"31 days and a second", "startdate=0&enddate=2678401", false},
		{"overflowing span", "startdate=0&enddate=10000000000", false},
		{"span wraps to a small value", "startdate=-9223372036854775808&enddate=9223372036854775807", false},
		{"reversed", "startdate=10&enddate=9", false},
		{"before the epoch", "startdate=-5&enddate=5", false},
		{"missing", "appli=1", false},
	} {
		form, _ := url.ParseQuery(c.form)
		if got := parseNotification(form).windowOK; got != c.ok {
			t.Errorf("%s: windowOK = %v, want %v", c.name, got, c.ok)
		}
	}
}
