package applehealth

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/KaanEmec/vitamux/internal/normalize"
)

// FuzzNormalizeSamples feeds arbitrary pages to the normalizer. Invariants: no panic; the
// output is deterministic; an accepted page always passes the writer's validation, so one odd
// sample is a warning and never fails the whole page.
func FuzzNormalizeSamples(f *testing.F) {
	files, err := filepath.Glob(filepath.Join("testdata", NormalizerID, "*.raw.json"))
	if err != nil || len(files) == 0 {
		f.Fatalf("no seed pages: %v", err)
	}
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		raw := normalize.RawPayload{Stream: StreamSamples, ContentType: "application/json", ExternalKey: "fuzz", Body: data}
		out, err := Normalizer{}.Normalize(t.Context(), raw, normalize.Env{Provider: Provider})
		again, err2 := Normalizer{}.Normalize(t.Context(), raw, normalize.Env{Provider: Provider})
		if (err == nil) != (err2 == nil) {
			t.Fatal("error is not deterministic")
		}
		if err != nil {
			return
		}
		if verr := out.Validate(); verr != nil {
			t.Fatalf("accepted page fails validation: %v", verr)
		}
		a, aerr := json.Marshal(out)
		b, berr := json.Marshal(again)
		if aerr != nil || berr != nil || !bytes.Equal(a, b) {
			t.Fatalf("output is not deterministic or does not marshal: %v %v", aerr, berr)
		}
	})
}
