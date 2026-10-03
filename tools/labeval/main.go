// Command labeval measures an extractor against the synthetic lab corpus (fixtures/lab): field
// precision and recall of the extracted rows compared with the committed ground truth. Only the
// synthetic PDFs are ever read or sent.
//
//	go run ./tools/fixturegen labpdf              # writes fixtures/generated/lab/*.pdf
//	go run ./tools/labeval -provider fake
//	VITAMUX_GEMINI_API_KEY_FILE=… VITAMUX_GEMINI_MODEL=… go run ./tools/labeval -provider gemini
//
// External providers are configured exactly as for `vitamux serve` (lab-documents.md#privacy-controls);
// keys are read from files and never printed.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/KaanEmec/vitamux/fixtures/lab"
	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/documents"
	"github.com/KaanEmec/vitamux/internal/documents/extract"
	"github.com/KaanEmec/vitamux/prompts"
	"github.com/KaanEmec/vitamux/schemas"
)

func main() {
	provider := flag.String("provider", extract.Fake, "fake, gemini, openai or openai_compatible")
	dir := flag.String("pdfs", "fixtures/generated/lab", "directory of the generated synthetic PDFs")
	verbose := flag.Bool("v", false, "print each report's score")
	flag.Parse()
	if err := run(context.Background(), os.Stdout, *provider, *dir, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, "labeval:", err)
		os.Exit(1)
	}
}

type report struct {
	ID     string `json:"id"`
	PDF    string `json:"pdf"`
	SHA256 string `json:"sha256"`
	Pages  int    `json:"pages"`
}

func run(ctx context.Context, w io.Writer, provider, dir string, verbose bool) error {
	ex, err := extractor(provider)
	if err != nil {
		return err
	}
	var man struct{ Reports []report }
	b, err := fs.ReadFile(lab.FS, "manifest.json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &man); err != nil {
		return err
	}
	if ex.External() {
		fmt.Fprintf(w, "Sending %d synthetic PDFs to %s (%s).\n", len(man.Reports), ex.ID(), ex.Model())
	}
	total := score{}
	for _, r := range man.Reports {
		pdf, err := os.ReadFile(filepath.Join(dir, r.PDF)) //nolint:gosec // the manifest's own file names
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s not found: run go run ./tools/fixturegen labpdf first", filepath.Join(dir, r.PDF))
		}
		if err != nil {
			return err
		}
		if sum := sha256.Sum256(pdf); hex.EncodeToString(sum[:]) != r.SHA256 {
			return fmt.Errorf("%s does not match fixtures/lab/manifest.json: regenerate it", r.PDF)
		}
		truth, err := truthFor(r.ID)
		if err != nil {
			return err
		}
		resp, err := ex.Extract(ctx, extract.Request{PDF: pdf, Pages: r.Pages, Prompt: prompts.LabExtractionV1, Schema: schemas.LabExtractionV1})
		s := score{}
		if err != nil || resp.Extraction == nil {
			fmt.Fprintf(w, "%s: extraction failed (%v); every printed field counts as missed\n", r.ID, err)
			s = compare(truth.Rows, nil)
		} else {
			s = compare(truth.Rows, resp.Extraction.Rows)
		}
		if verbose {
			p, rc := s.overall()
			fmt.Fprintf(w, "%s: precision %.3f recall %.3f (%d/%d rows matched)\n", r.ID, p, rc, s.matched, s.truthRows)
		}
		total.add(s)
	}
	total.print(w, provider, ex.Model())
	return nil
}

func extractor(provider string) (extract.Extractor, error) {
	if provider == extract.Fake {
		return extract.NewFake()
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	for _, e := range extract.Configured(cfg) {
		if e.ID() == provider {
			return e, nil
		}
	}
	return nil, fmt.Errorf("provider %q is not configured (see docs/architecture/lab-documents.md#privacy-controls)", provider)
}

func truthFor(id string) (*documents.Extraction, error) {
	b, err := fs.ReadFile(lab.FS, id+".json")
	if err != nil {
		return nil, err
	}
	return documents.DecodeExtraction(b)
}

// fields are the row fields scored, in print order.
var fields = []string{"page", "analyte_label", "value_text", "value_numeric", "comparator", "unit_text", "reference_range_text",
	"ref_low", "ref_high", "printed_flag", "specimen_type", "collected_at", "reported_at", "laboratory"}

// counts holds, per field, correct values, values the extractor gave, and values the truth has.
type counts struct{ correct, predicted, truth int }

type score struct {
	fields                    map[string]*counts
	matched, truthRows, extra int
}

func (s *score) field(f string) *counts {
	if s.fields == nil {
		s.fields = map[string]*counts{}
	}
	if s.fields[f] == nil {
		s.fields[f] = &counts{}
	}
	return s.fields[f]
}

func (s *score) add(o score) {
	for f, c := range o.fields {
		t := s.field(f)
		t.correct, t.predicted, t.truth = t.correct+c.correct, t.predicted+c.predicted, t.truth+c.truth
	}
	s.matched, s.truthRows, s.extra = s.matched+o.matched, s.truthRows+o.truthRows, s.extra+o.extra
}

// overall is the micro-averaged precision and recall over every field.
func (s *score) overall() (float64, float64) {
	var c counts
	for _, x := range s.fields {
		c.correct, c.predicted, c.truth = c.correct+x.correct, c.predicted+x.predicted, c.truth+x.truth
	}
	return ratio(c.correct, c.predicted), ratio(c.correct, c.truth)
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 1
	}
	return float64(a) / float64(b)
}

func (s *score) print(w io.Writer, provider, model string) {
	if model == "" {
		model = "-"
	}
	fmt.Fprintf(w, "provider %s, model %s: %d/%d truth rows matched, %d extra rows\n", provider, model, s.matched, s.truthRows, s.extra)
	fmt.Fprintf(w, "%-22s %9s %9s %7s %7s\n", "field", "precision", "recall", "given", "printed")
	for _, f := range fields {
		c := s.field(f)
		fmt.Fprintf(w, "%-22s %9.3f %9.3f %7d %7d\n", f, ratio(c.correct, c.predicted), ratio(c.correct, c.truth), c.predicted, c.truth)
	}
	p, r := s.overall()
	fmt.Fprintf(w, "%-22s %9.3f %9.3f\n", "all fields", p, r)
}

// compare scores got against want. Rows pair up by page and label (case and spacing ignored),
// then the rest by page and position. A field counts as given when not null, printed when the
// truth has it, and correct when both agree (text exactly after trimming, numbers to 1e-9).
func compare(want, got []documents.Row) score {
	s := score{truthRows: len(want)}
	used := make([]bool, len(got))
	pair := make([]int, len(want))
	for i := range pair {
		pair[i] = -1
	}
	match := func(eq func(w, g documents.Row) bool) {
		for i, wr := range want {
			for j, gr := range got {
				if pair[i] < 0 && !used[j] && eq(wr, gr) {
					pair[i], used[j] = j, true
				}
			}
		}
	}
	match(func(w, g documents.Row) bool {
		return w.Page == g.Page && labelKey(w.AnalyteLabel) == labelKey(g.AnalyteLabel)
	})
	match(func(w, g documents.Row) bool { return w.Page == g.Page && w.RowIndex == g.RowIndex })
	for i, wr := range want {
		wv := values(&wr)
		var gv map[string]any
		if pair[i] >= 0 {
			s.matched++
			gv = values(&got[pair[i]])
		}
		for _, f := range fields {
			c := s.field(f)
			if wv[f] != nil {
				c.truth++
			}
			if gv[f] != nil {
				c.predicted++
				if equal(wv[f], gv[f]) {
					c.correct++
				}
			}
		}
	}
	for j := range got {
		if used[j] {
			continue
		}
		s.extra++
		for f, v := range values(&got[j]) {
			if v != nil {
				s.field(f).predicted++
			}
		}
	}
	return s
}

func labelKey(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// values maps each scored field to a string, a float64, or nil when null.
func values(r *documents.Row) map[string]any {
	str := func(p *string) any {
		if p == nil {
			return nil
		}
		return strings.TrimSpace(*p)
	}
	num := func(p *float64) any {
		if p == nil {
			return nil
		}
		return *p
	}
	return map[string]any{"page": float64(r.Page), "analyte_label": strings.TrimSpace(r.AnalyteLabel), "value_text": str(r.ValueText),
		"value_numeric": num(r.ValueNumeric), "comparator": str(r.Comparator), "unit_text": str(r.UnitText),
		"reference_range_text": str(r.ReferenceRangeText), "ref_low": num(r.RefLow), "ref_high": num(r.RefHigh),
		"printed_flag": str(r.PrintedFlag), "specimen_type": str(r.SpecimenType), "collected_at": str(r.CollectedAt),
		"reported_at": str(r.ReportedAt), "laboratory": str(r.Laboratory)}
}

func equal(a, b any) bool {
	if x, ok := a.(float64); ok {
		y, ok := b.(float64)
		return ok && math.Abs(x-y) <= 1e-9*math.Max(1, math.Abs(x))
	}
	return a == b
}
