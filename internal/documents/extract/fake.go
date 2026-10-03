package extract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"

	"github.com/KaanEmec/vitamux/fixtures/lab"
)

// fake answers with the committed ground truth of the synthetic lab reports, keyed by PDF
// sha256 (fixtures/lab/manifest.json). It is deterministic, never leaves the host and needs
// neither enablement nor consent: CI and demos use it. Generate the PDFs with
// `go run ./tools/fixturegen labpdf`.
type fake struct{ truth map[string][]byte }

// NewFake returns the fake extractor.
func NewFake() (Extractor, error) { return newFake(lab.FS) }

func newFake(fsys fs.FS) (Extractor, error) {
	b, err := fs.ReadFile(fsys, "manifest.json")
	if err != nil {
		return nil, err
	}
	var m struct {
		Reports []struct {
			ID     string `json:"id"`
			SHA256 string `json:"sha256"`
		} `json:"reports"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("fake extractor manifest: %w", err)
	}
	f := &fake{truth: map[string][]byte{}}
	for _, r := range m.Reports {
		if f.truth[r.SHA256], err = fs.ReadFile(fsys, r.ID+".json"); err != nil {
			return nil, err
		}
	}
	return f, nil
}

func (*fake) ID() string     { return Fake }
func (*fake) External() bool { return false }
func (*fake) Model() string  { return "" }

func (f *fake) Extract(_ context.Context, req Request) (Response, error) {
	sum := sha256.Sum256(req.PDF)
	raw, ok := f.truth[hex.EncodeToString(sum[:])]
	if !ok {
		return Response{}, errorf(ClassUnknownDocument, "fake: not one of the synthetic fixture PDFs (go run ./tools/fixturegen labpdf)")
	}
	x, err := decode(Fake, string(raw))
	return Response{Raw: raw, Extraction: x, ModelID: "fixtures"}, err
}
