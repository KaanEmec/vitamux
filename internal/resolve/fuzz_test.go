package resolve

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// FuzzParseRule feeds arbitrary bytes to the rule parser. CI runs it briefly and nightly for
// longer (docs/plan/E13-hardening/J13.2-fuzzing-limits.md). Invariants: no panic; a rejected
// rule fails with *ValidationError; an accepted rule marshals and parses again to the same
// bytes.
func FuzzParseRule(f *testing.F) {
	files, err := filepath.Glob(filepath.Join("testdata", "valid", "*.json"))
	if err != nil || len(files) == 0 {
		f.Fatalf("no seed rules: %v", err)
	}
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte(`{"schema":"vitamux.rule/1","metric":"steps"} {}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := ParseRule(data)
		if err != nil {
			if ve := (*ValidationError)(nil); !errors.As(err, &ve) || len(ve.Errors) == 0 {
				t.Fatalf("error is not a non-empty *ValidationError: %v", err)
			}
			return
		}
		again, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("accepted rule does not marshal: %v", err)
		}
		r2, err := ParseRule(again)
		if err != nil {
			t.Fatalf("marshalled rule is rejected: %v", err)
		}
		again2, err := json.Marshal(r2)
		if err != nil || !bytes.Equal(again, again2) {
			t.Fatalf("round trip is not stable: %v", err)
		}
	})
}
