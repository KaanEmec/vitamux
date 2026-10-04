//go:build integration

package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/documents"
	"github.com/KaanEmec/vitamux/internal/documents/analytes"
)

// fixturePDFs generates the synthetic lab PDFs (git-ignored) and returns their directory.
func fixturePDFs(t *testing.T) string {
	t.Helper()
	out := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "go", "run", "./tools/fixturegen", "labpdf", "-out", out, "-truth", filepath.Join(out, "truth"))
	cmd.Dir = "../../.."
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixturegen: %v\n%s", err, b)
	}
	return out
}

// seedSuggest mirrors the seeded aliases (analytes.SeedAliases), without a database.
func seedSuggest() func(string) string {
	m := map[string]string{}
	for _, an := range analytes.All() {
		for _, l := range an.SeedAliases() {
			m[analytes.LabelKey(l)] = an.Code
		}
	}
	return func(label string) string { return m[analytes.LabelKey(label)] }
}

// TestFixtureGroundTruthIsClean: the ground truth of every synthetic report verifies against
// its own PDF. Only the warnings the reports were designed to raise remain.
func TestFixtureGroundTruthIsClean(t *testing.T) {
	dir := fixturePDFs(t)
	suggest := seedSuggest()
	allowed := []string{WarnUnknownAnalyte, WarnDuplicateInRun}
	for i := 1; i <= 12; i++ {
		id := "lab-" + []string{"01", "02", "03", "04", "05", "06", "07", "08", "09", "10", "11", "12"}[i-1]
		pdf, err := os.ReadFile(filepath.Join(dir, id+".pdf"))
		if err != nil {
			t.Fatal(err)
		}
		truth, err := os.ReadFile("../../../fixtures/lab/" + id + ".json")
		if err != nil {
			t.Fatal(err)
		}
		x, err := documents.DecodeExtraction(truth)
		if err != nil {
			t.Fatal(err)
		}
		pages := documents.TextLayer(pdf)
		if len(pages) != x.Document.PageCount {
			t.Fatalf("%s: %d pages of text, want %d", id, len(pages), x.Document.PageCount)
		}
		if id == "lab-10" && pages[0] != "" {
			t.Errorf("%s: the scanned report has a text layer", id)
		}
		in := make([]Input, len(x.Rows))
		for j, r := range x.Rows {
			in[j] = Input{Row: r, Analyte: suggest(r.AnalyteLabel)}
		}
		now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for j, w := range Check(Context{Doc: x.Document, Pages: pages, Now: now}, in) {
			for _, code := range w {
				if id == "lab-07" && (code == WarnDateMissing || code == WarnDateAmbiguous) {
					continue // the report prints 10/09/2025
				}
				if id == "lab-11" && x.Rows[j].AnalyteLabel == "WBC" && code == WarnUnknownUnit {
					continue // urine WBC per high-power field is not the blood count the alias suggests
				}
				if !slices.Contains(allowed, code) {
					t.Errorf("%s row %d (%s): %s", id, j, x.Rows[j].AnalyteLabel, code)
				}
			}
		}
	}
}
