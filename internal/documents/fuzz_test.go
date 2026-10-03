package documents

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// FuzzValidatePDF feeds arbitrary bytes to the upload validator. CI runs it briefly and
// nightly for longer (docs/plan/E13-hardening/J13.2-fuzzing-limits.md). Invariants: no
// panic; a refusal is a *RejectError with a reason; an accepted file has between 1 page and
// the page limit, and stays accepted when validated again.
func FuzzValidatePDF(f *testing.F) {
	for _, pdf := range [][]byte{
		testPDF(1, false, false, "a"), testPDF(3, true, false, "b"), testPDF(2, false, true, "c"),
		[]byte("%PDF-1.7\n1 0 obj\n<< /Type /ObjStm /Filter /FlateDecode >>\nstream\nnot zlib\nendstream\n%%EOF"),
		[]byte("%PDF-1.4\nstream\n"), []byte("%PDF-"), nil,
	} {
		f.Add(pdf)
	}
	lim := Limits{MaxBytes: 1 << 20, MaxPages: 50}
	f.Fuzz(func(t *testing.T, data []byte) {
		pages, err := Validate(data, lim)
		if err != nil {
			if re := (*RejectError)(nil); !errors.As(err, &re) || re.Reason == "" {
				t.Fatalf("error is not a *RejectError with a reason: %v", err)
			}
			return
		}
		if pages < 1 || pages > lim.MaxPages {
			t.Fatalf("accepted with %d pages (limit %d)", pages, lim.MaxPages)
		}
		if again, err := Validate(data, lim); err != nil || again != pages {
			t.Fatalf("second validation differs: %d, %v", again, err)
		}
	})
}

// FuzzDecodeExtraction feeds arbitrary bytes to the extractor response parser. Invariants:
// no panic; a refusal is an *ExtractionError listing at least one problem; an accepted
// response marshals and decodes again to the same bytes.
func FuzzDecodeExtraction(f *testing.F) {
	// Seeds are the ground truth files cut to one row: whole files (6 to 18 KiB) make the
	// engine's minimizer crawl.
	files, err := filepath.Glob(filepath.Join(labFixtures, "lab-*.json"))
	if err != nil || len(files) == 0 {
		f.Fatalf("no seed extractions: %v", err)
	}
	for _, name := range files[:4] {
		b, err := os.ReadFile(name) //nolint:gosec // repository fixtures
		if err != nil {
			f.Fatal(err)
		}
		x, err := DecodeExtraction(b)
		if err != nil || len(x.Rows) == 0 {
			f.Fatalf("%s: %v", name, err)
		}
		x.Rows = x.Rows[:1]
		if b, err = json.Marshal(x); err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte(`{"schema":"vitamux.lab.extraction/1","document":{},"rows":[],"warnings":[]} {}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		x, err := DecodeExtraction(data)
		if err != nil {
			if ee := (*ExtractionError)(nil); !errors.As(err, &ee) || len(ee.Errors) == 0 {
				t.Fatalf("error is not a non-empty *ExtractionError: %v", err)
			}
			return
		}
		again, err := json.Marshal(x)
		if err != nil {
			t.Fatalf("accepted extraction does not marshal: %v", err)
		}
		x2, err := DecodeExtraction(again)
		if err != nil {
			t.Fatalf("marshalled extraction is rejected: %v", err)
		}
		if again2, err := json.Marshal(x2); err != nil || !bytes.Equal(again, again2) {
			t.Fatalf("round trip is not stable: %v", err)
		}
	})
}
