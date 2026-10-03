// Package normtest is the golden harness for normalizers (docs/architecture/connectors.md#normalizer-contract).
//
// A normalizer package keeps synthetic raw cases in testdata/<normalizer id>/<case>.raw.<ext> and
// the expected output beside them in <case>.golden.json, which also records the normalizer
// version. Name the test TestGolden… so `make golden` runs it. UPDATE_GOLDEN=1 rewrites goldens,
// but only after a Version() bump: changing output at the same version always fails.
package normtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KaanEmec/vitamux/internal/normalize"
)

// golden is the file format. Synthetic is the fixture-guard marker.
type golden struct {
	Synthetic  bool              `json:"synthetic"`
	Normalizer string            `json:"normalizer"`
	Version    int               `json:"version"`
	Error      string            `json:"error,omitempty"`
	Output     *normalize.Output `json:"output,omitempty"`
}

// Golden runs n over every raw case of its testdata directory. raw is the template payload:
// Body is each case file, and ExternalKey defaults to the case name. A case may also pin an
// expected Normalize error, which the golden records instead of output.
func Golden(t *testing.T, n normalize.Normalizer, raw normalize.RawPayload, env normalize.Env) {
	t.Helper()
	cases, err := filepath.Glob(filepath.Join("testdata", n.ID(), "*.raw.*"))
	if err != nil || len(cases) == 0 {
		t.Fatalf("no golden cases in testdata/%s (want <case>.raw.<ext>)", n.ID())
	}
	update := os.Getenv("UPDATE_GOLDEN") == "1"
	for _, path := range cases {
		name := strings.SplitN(filepath.Base(path), ".raw.", 2)[0]
		t.Run(name, func(t *testing.T) {
			body, err := os.ReadFile(path) //nolint:gosec // test data path
			if err != nil {
				t.Fatal(err)
			}
			in := raw
			in.Body = body
			if in.ExternalKey == "" {
				in.ExternalKey = name
			}
			got, err := render(n, in, env)
			if err != nil {
				t.Fatal(err)
			}
			again, err := render(n, in, env)
			if err != nil || !bytes.Equal(got, again) {
				t.Fatal("output is not deterministic: two runs over the same bytes differ")
			}
			gpath := filepath.Join(filepath.Dir(path), name+".golden.json")
			old, err := os.ReadFile(gpath) //nolint:gosec // test data path
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			write, err := check(old, got, n.Version(), update)
			if err != nil {
				t.Fatalf("%s: %v", gpath, err)
			}
			if write {
				if err := os.WriteFile(gpath, got, 0o600); err != nil {
					t.Fatal(err)
				}
				t.Logf("wrote %s", gpath)
			}
		})
	}
}

// render normalizes once and encodes the golden file, validating the output first.
func render(n normalize.Normalizer, raw normalize.RawPayload, env normalize.Env) ([]byte, error) {
	g := golden{Synthetic: true, Normalizer: n.ID(), Version: n.Version()}
	out, err := n.Normalize(context.Background(), raw, env)
	if err != nil {
		g.Error = err.Error()
	} else {
		if err := out.Validate(); err != nil {
			return nil, err
		}
		g.Output = &out
	}
	b, err := json.MarshalIndent(g, "", "  ")
	return append(b, '\n'), err
}

// check is the version-bump guard. It reports whether got should replace old.
func check(old, got []byte, version int, update bool) (write bool, err error) {
	if old == nil {
		if update {
			return true, nil
		}
		return false, errors.New("golden file missing; run with UPDATE_GOLDEN=1")
	}
	var o golden
	if err := json.Unmarshal(old, &o); err != nil {
		return false, fmt.Errorf("unreadable golden: %w", err)
	}
	same, err := sameResult(old, got)
	if err != nil {
		return false, err
	}
	switch {
	case o.Version > version:
		return false, fmt.Errorf("golden records version %d, newer than the normalizer's %d", o.Version, version)
	case o.Version == version && same:
		return false, nil
	case o.Version == version:
		return false, fmt.Errorf("output changed without a version bump: bump Version() to %d, then run with UPDATE_GOLDEN=1", version+1)
	case update:
		return true, nil
	default:
		return false, fmt.Errorf("normalizer is at version %d but the golden records %d; review the output and run with UPDATE_GOLDEN=1", version, o.Version)
	}
}

// sameResult compares the output and error of two golden files, ignoring the version.
func sameResult(a, b []byte) (bool, error) {
	type result struct {
		Error  string          `json:"error"`
		Output json.RawMessage `json:"output"`
	}
	var x, y result
	if err := json.Unmarshal(a, &x); err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, &y); err != nil {
		return false, err
	}
	var cx, cy bytes.Buffer
	if len(x.Output) > 0 {
		if err := json.Compact(&cx, x.Output); err != nil {
			return false, err
		}
	}
	if len(y.Output) > 0 {
		if err := json.Compact(&cy, y.Output); err != nil {
			return false, err
		}
	}
	return x.Error == y.Error && bytes.Equal(cx.Bytes(), cy.Bytes()), nil
}
