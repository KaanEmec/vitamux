package documents

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"slices"
	"testing"
)

// synthPDF builds a PDF whose pages show the given content streams; flate compresses them.
// Kids list the pages in reverse object order, so page order must come from the page tree.
func synthPDF(flate bool, extra string, contents ...string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n% synthetic: true\n1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n")
	kids := ""
	for i := range slices.Backward(contents) {
		kids = fmt.Sprintf("%d 0 R ", 10+2*i) + kids
	}
	fmt.Fprintf(&b, "2 0 obj << /Type /Pages /Kids [%s] /Count %d >> endobj\n", kids, len(contents))
	for i, content := range slices.Backward(contents) {
		data, filter := []byte(content), ""
		if flate {
			var z bytes.Buffer
			w := zlib.NewWriter(&z)
			_, _ = w.Write(data)
			_ = w.Close()
			data, filter = z.Bytes(), " /Filter /FlateDecode"
		}
		fmt.Fprintf(&b, "%d 0 obj << /Type /Page /Parent 2 0 R /Contents %d 0 R >> endobj\n", 10+2*i, 11+2*i)
		fmt.Fprintf(&b, "%d 0 obj << /Length %d%s >>\nstream\n%s\nendstream\nendobj\n", 11+2*i, len(data), filter, data)
	}
	b.WriteString(extra)
	b.WriteString("trailer << /Root 1 0 R >>\n%%EOF\n")
	return b.Bytes()
}

func TestTextLayer(t *testing.T) {
	page1 := `BT /F1 9 Tf 72 700 Td (Creatinine) Tj ET BT 200 700 Td [(0.)-20(75)] TJ ET BT 260 700 Td <6D672F644C> Tj ET` +
		"\n% a comment (not text)\nBT (Esc\\(aped\\) \\265g/L) Tj ET"
	scan := "q 612 0 0 792 0 0 cm /Im1 Do Q"
	for _, flate := range []bool{false, true} {
		got := TextLayer(synthPDF(flate, "", page1, scan))
		if len(got) != 2 || got[0] != "Creatinine 0. 75 mg/dL Esc(aped) µg/L" || got[1] != "" {
			t.Errorf("flate=%v: %q", flate, got)
		}
	}
	if got := TextLayer(synthPDF(false, "90 0 obj << /Type /Font /Subtype /Type0 /BaseFont /X >> endobj\n", page1)); got != nil {
		t.Errorf("a Type0 font must report no text layer: %q", got)
	}
	if got := TextLayer([]byte("%PDF-1.4\n%%EOF\n")); got != nil {
		t.Errorf("no page tree: %q", got)
	}
}
