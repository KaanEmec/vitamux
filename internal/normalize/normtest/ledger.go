package normtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// checkLedger enforces the field ledger of a stream (docs/adapters.md#field-ledgers):
// testdata/<id>/fields.json ({"synthetic": true, "fields": {...}}) maps every leaf path of every JSON raw case to catalogue codes
// (comma separated) or to "raw: <reason>". A path no ledger entry names fails, so a new provider
// field is catalogued or explained before it ships. Paths use "." for object keys and "[]" for
// array elements; the top-level "synthetic" marker is not a field.
func checkLedger(t *testing.T, dir string, cases []string) {
	t.Helper()
	var file struct {
		Fields map[string]string `json:"fields"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "fields.json")) //nolint:gosec // test data path
	if err == nil {
		err = json.Unmarshal(b, &file)
	}
	if err != nil {
		t.Fatalf("field ledger %s/fields.json: %v (want {\"synthetic\": true, \"fields\": {<raw path>: <code> or \"raw: <reason>\"}})", dir, err)
	}
	ledger := file.Fields
	for path, to := range ledger {
		if err := checkTarget(to); err != nil {
			t.Errorf("%s/fields.json %s: %v", dir, path, err)
		}
	}
	missing := map[string]bool{}
	for _, c := range cases {
		if !strings.HasSuffix(c, ".json") {
			continue
		}
		body, err := os.ReadFile(c) //nolint:gosec // test data path
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			t.Fatalf("%s: %v", c, err)
		}
		if m, ok := v.(map[string]any); ok {
			delete(m, "synthetic")
		}
		for _, p := range leafPaths(v, "") {
			if _, ok := ledger[p]; !ok {
				missing[p] = true
			}
		}
	}
	if len(missing) > 0 {
		paths := make([]string, 0, len(missing))
		for p := range missing {
			paths = append(paths, p)
		}
		slices.Sort(paths)
		t.Errorf("%s/fields.json lacks %d raw path(s); map each to a catalogue code or \"raw: <reason>\":\n  %s", dir, len(paths), strings.Join(paths, "\n  "))
	}
}

// checkTarget accepts "raw: <reason>" or a comma-separated list of metric or event codes.
func checkTarget(to string) error {
	if reason, ok := strings.CutPrefix(to, "raw:"); ok {
		if strings.TrimSpace(reason) == "" {
			return errors.New("raw needs a reason")
		}
		return nil
	}
	for code := range strings.SplitSeq(to, ",") {
		code = strings.TrimSpace(code)
		if _, ok := catalog.Lookup(code); ok {
			continue
		}
		if _, ok := catalog.LookupEvent(code); !ok {
			return fmt.Errorf("%q is neither a catalogue code nor \"raw: <reason>\"", code)
		}
	}
	return nil
}

// leafPaths lists the paths of the scalar, null and empty container values under v.
func leafPaths(v any, prefix string) []string {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			return []string{prefix}
		}
		var out []string
		for k, e := range x {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			out = append(out, leafPaths(e, p)...)
		}
		return out
	case []any:
		if len(x) == 0 {
			return []string{prefix}
		}
		var out []string
		for _, e := range x {
			out = append(out, leafPaths(e, prefix+"[]")...)
		}
		return out
	default:
		return []string{prefix}
	}
}
