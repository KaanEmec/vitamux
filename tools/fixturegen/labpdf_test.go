package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/KaanEmec/vitamux/internal/documents"
)

const committedLab = "../../fixtures/lab"

// The committed ground truth is the generator's output for seed 42. Its manifest holds each
// PDF's sha256, so this also pins the (git-ignored) PDFs byte for byte. After an intended
// generator change: go run ./tools/fixturegen labpdf
func TestLabPDFMatchesCommitted(t *testing.T) {
	out, truth := t.TempDir(), t.TempDir()
	if err := generateLabPDFs(out, truth, 42); err != nil {
		t.Fatal(err)
	}
	got, _ := filepath.Glob(filepath.Join(truth, "*.json"))
	want, _ := filepath.Glob(filepath.Join(committedLab, "*.json"))
	if len(got) != len(want) {
		t.Fatalf("generated %d truth files, %d committed", len(got), len(want))
	}
	for _, g := range got {
		a, _ := os.ReadFile(g)
		b, err := os.ReadFile(filepath.Join(committedLab, filepath.Base(g)))
		if err != nil || !bytes.Equal(a, b) {
			t.Errorf("%s differs from the committed copy (%v); regenerate with: go run ./tools/fixturegen labpdf", filepath.Base(g), err)
		}
	}
	// A second run is identical, and a different seed is not.
	out2, truth2 := t.TempDir(), t.TempDir()
	if err := generateLabPDFs(out2, truth2, 42); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lab-01.pdf", "lab-10.pdf"} {
		a, _ := os.ReadFile(filepath.Join(out, name))
		b, _ := os.ReadFile(filepath.Join(out2, name))
		if !bytes.Equal(a, b) {
			t.Errorf("%s: two runs differ", name)
		}
	}
	out3, truth3 := t.TempDir(), t.TempDir()
	if err := generateLabPDFs(out3, truth3, 7); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(filepath.Join(truth, "lab-01.json"))
	b, _ := os.ReadFile(filepath.Join(truth3, "lab-01.json"))
	if bytes.Equal(a, b) {
		t.Error("different seeds gave identical ground truth")
	}
}

func loadLabManifest(t *testing.T) labManifest {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(committedLab, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m labManifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func loadTruth(t *testing.T, id string) *documents.Extraction {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(committedLab, id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	x, err := documents.DecodeExtraction(b)
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	return x
}

// The corpus covers what J12.2 asks for.
func TestLabCorpusCoverage(t *testing.T) {
	m := loadLabManifest(t)
	if len(m.Reports) < 10 {
		t.Fatalf("%d reports, want at least 10", len(m.Reports))
	}
	units := map[string]map[string]bool{} // code -> printed units
	cmps := map[string]bool{}
	var multiPage, scanned, unknown, unreadable bool
	for _, r := range m.Reports {
		x := loadTruth(t, r.ID)
		if len(r.Analytes) != len(x.Rows) {
			t.Fatalf("%s: %d manifest analytes for %d rows", r.ID, len(r.Analytes), len(x.Rows))
		}
		multiPage = multiPage || r.Pages > 1
		scanned = scanned || !r.TextLayer
		for i, row := range x.Rows {
			if row.Comparator != nil {
				cmps[*row.Comparator] = true
			}
			if slices.Contains(row.Warnings, "unreadable_value") {
				unreadable = true
			}
			code := r.Analytes[i]
			if code == nil {
				unknown = true
				continue
			}
			if units[*code] == nil {
				units[*code] = map[string]bool{}
			}
			if row.UnitText != nil {
				units[*code][*row.UnitText] = true
			}
		}
	}
	for _, c := range []struct{ code, unit string }{
		{"glucose", "mg/dL"}, {"glucose", "mmol/L"}, {"hba1c", "%"}, {"hba1c", "mmol/mol"},
		{"lpa_mass", "mg/dL"}, {"lpa_molar", "nmol/L"}, {"d_dimer", "µg/mL FEU"},
	} {
		if !units[c.code][c.unit] {
			t.Errorf("no %s row in %s", c.code, c.unit)
		}
	}
	for _, c := range []string{"<", ">", "<="} {
		if !cmps[c] {
			t.Errorf("no row with comparator %s", c)
		}
	}
	if !multiPage || !scanned || !unknown || !unreadable {
		t.Errorf("multi-page %v, scanned %v, unknown analyte %v, unreadable cell %v; want all", multiPage, scanned, unknown, unreadable)
	}
}

var (
	tjRe     = regexp.MustCompile(`\(((?:\\.|[^\\)])*)\) Tj`)
	objRe    = regexp.MustCompile(`^(\d+) 0 obj\n`)
	octalRe  = regexp.MustCompile(`\\([0-7]{3}|.)`)
	startxRe = regexp.MustCompile(`startxref\n(\d+)\n%%EOF\n$`)
)

// Structural checks without a PDF library: xref offsets point at their objects, text-layer
// PDFs show every label and value, the scanned one has no text operators at all.
func TestLabPDFStructure(t *testing.T) {
	out, truth := t.TempDir(), t.TempDir()
	if err := generateLabPDFs(out, truth, 42); err != nil {
		t.Fatal(err)
	}
	for _, r := range loadLabManifest(t).Reports {
		pdf, err := os.ReadFile(filepath.Join(out, r.PDF))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(pdf, []byte("%PDF-1.4\n")) {
			t.Fatalf("%s: bad header", r.ID)
		}
		m := startxRe.FindSubmatch(pdf)
		if m == nil {
			t.Fatalf("%s: no startxref", r.ID)
		}
		xref, _ := strconv.Atoi(string(m[1]))
		lines := strings.Split(string(pdf[xref:]), "\n")
		n, _ := strconv.Atoi(strings.Fields(lines[1])[1])
		for i := 1; i < n; i++ {
			off, _ := strconv.Atoi(lines[2+i][:10])
			if om := objRe.FindSubmatch(pdf[off:min(len(pdf), off+20)]); om == nil || string(om[1]) != strconv.Itoa(i) {
				t.Fatalf("%s: xref entry %d does not point at its object", r.ID, i)
			}
		}
		if !r.TextLayer {
			if bytes.Contains(pdf, []byte(" Tj")) || bytes.Contains(pdf, []byte("/Font <<")) || !bytes.Contains(pdf, []byte("/Subtype /Image")) {
				t.Errorf("%s: scanned PDF must be image only", r.ID)
			}
			continue
		}
		var shown strings.Builder
		for _, s := range tjRe.FindAllSubmatch(pdf, -1) {
			shown.WriteString(decodeShown(s[1]))
			shown.WriteByte('\n')
		}
		for _, row := range loadTruth(t, r.ID).Rows {
			want := []string{row.AnalyteLabel}
			if row.ValueText != nil {
				want = append(want, strings.NewReplacer("≤", "").Replace(*row.ValueText))
			}
			for _, w := range want {
				if !strings.Contains(shown.String(), w) {
					t.Errorf("%s row %d: %q not in the text layer", r.ID, row.RowIndex, w)
				}
			}
		}
	}
}

// decodeShown reverses pdfEscape and WinAnsi for the ASCII-heavy strings the reports print.
func decodeShown(b []byte) string {
	rev := map[byte]rune{}
	for r, c := range winAnsi {
		rev[c] = r
	}
	raw := octalRe.ReplaceAllFunc(b, func(m []byte) []byte {
		if len(m) == 4 {
			v, _ := strconv.ParseUint(string(m[1:]), 8, 8)
			return []byte{byte(v)}
		}
		return m[1:]
	})
	var sb strings.Builder
	for _, c := range raw {
		if r, ok := rev[c]; ok {
			sb.WriteRune(r)
		} else {
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

func TestRunLengthRoundTrip(t *testing.T) {
	src := append(append(bytes.Repeat([]byte{0xff}, 300), 1, 2, 3, 3, 4), bytes.Repeat([]byte{0}, 129)...)
	enc := runLength(src)
	var dec []byte
	for i := 0; enc[i] != 128; {
		n := int(enc[i])
		if n < 128 {
			dec = append(dec, enc[i+1:i+2+n]...)
			i += n + 2
		} else {
			dec = append(dec, bytes.Repeat([]byte{enc[i+1]}, 257-n)...)
			i += 2
		}
	}
	if !bytes.Equal(dec, src) {
		t.Fatal("run-length round trip failed")
	}
}
