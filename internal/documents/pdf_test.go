package documents

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// testPDF builds a small synthetic PDF with n pages. With objStm the page objects live in a
// FlateDecode object stream (PDF 1.5 style); with encrypted the trailer names an /Encrypt
// dictionary. The scanner needs no cross-reference table, so none is written.
func testPDF(n int, objStm, encrypted bool, salt string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n%synthetic " + salt + "\n")
	b.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	kids := make([]string, n)
	for i := range n {
		kids[i] = fmt.Sprintf("%d 0 R", 10+i)
	}
	fmt.Fprintf(&b, "2 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", strings.Join(kids, " "), n)
	var pages bytes.Buffer
	for i := range n {
		fmt.Fprintf(&pages, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] >>\n")
		if !objStm {
			fmt.Fprintf(&b, "%d 0 obj\n<</Type/Page/Parent 2 0 R>>\nendobj\n", 10+i)
		}
	}
	if objStm {
		var z bytes.Buffer
		w := zlib.NewWriter(&z)
		_, _ = w.Write(pages.Bytes())
		_ = w.Close()
		fmt.Fprintf(&b, "3 0 obj\n<< /Type /ObjStm /N %d /First 0 /Filter /FlateDecode /Length %d >>\nstream\n", n, z.Len())
		b.Write(z.Bytes())
		b.WriteString("\nendstream\nendobj\n")
	}
	b.WriteString("trailer\n<< /Root 1 0 R")
	if encrypted {
		b.WriteString(" /Encrypt 4 0 R")
	}
	b.WriteString(" >>\n%%EOF\n")
	return b.Bytes()
}

func TestValidate(t *testing.T) {
	lim := Limits{MaxBytes: 4096, MaxPages: 5}
	for name, tc := range map[string]struct {
		pdf   []byte
		pages int
		want  Reason
	}{
		"plain":           {testPDF(3, false, false, "a"), 3, ""},
		"object stream":   {testPDF(4, true, false, "a"), 4, ""},
		"empty":           {nil, 0, ReasonEmpty},
		"not a pdf":       {[]byte("PK\x03\x04 not a pdf %%EOF"), 0, ReasonNotPDF},
		"html":            {[]byte("<html>%PDF-1.4</html>"), 0, ReasonNotPDF},
		"truncated":       {bytes.TrimSuffix(testPDF(1, false, false, "a"), []byte("%%EOF\n")), 0, ReasonMalformed},
		"encrypted":       {testPDF(1, false, true, "a"), 0, ReasonEncrypted},
		"too many pages":  {testPDF(6, false, false, "a"), 0, ReasonTooManyPages},
		"too many in stm": {testPDF(6, true, false, "a"), 0, ReasonTooManyPages},
		"no pages":        {[]byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\n%%EOF"), 0, ReasonMalformed},
		"too large":       {append(testPDF(1, false, false, "a"), bytes.Repeat([]byte(" "), 4096)...), 0, ReasonTooLarge},
	} {
		pages, err := Validate(tc.pdf, lim)
		var re *RejectError
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case tc.want == "" && pages != tc.pages:
			t.Errorf("%s: %d pages, want %d", name, pages, tc.pages)
		case tc.want != "" && (!errors.As(err, &re) || re.Reason != tc.want):
			t.Errorf("%s: err %v, want %s", name, err, tc.want)
		}
	}
}

func TestValidateInflateBudget(t *testing.T) {
	// An object stream that inflates past the budget is refused, not read to the end.
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	_, _ = w.Write(bytes.Repeat([]byte{0}, maxInflated+1))
	_ = w.Close()
	pdf := []byte("%PDF-1.7\n3 0 obj\n<< /Type /ObjStm /Filter /FlateDecode >>\nstream\n")
	pdf = append(append(pdf, z.Bytes()...), "\nendstream\nendobj\n4 0 obj <</Type /Page>> endobj\n%%EOF\n"...)
	var re *RejectError
	if _, err := Validate(pdf, Limits{MaxBytes: 1 << 30, MaxPages: 50}); !errors.As(err, &re) || re.Reason != ReasonMalformed {
		t.Fatalf("bomb: %v", err)
	}
}

func TestSealWith(t *testing.T) {
	key := bytes.Repeat([]byte{7}, dataKeySize)
	sealed, err := sealWith(key, []byte("synthetic"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := openWith(key, sealed, []byte("aad")); err != nil || string(pt) != "synthetic" {
		t.Fatalf("open: %q, %v", pt, err)
	}
	if _, err := openWith(key, sealed, []byte("other")); err == nil {
		t.Fatal("opened with the wrong aad")
	}
	if _, err := openWith(bytes.Repeat([]byte{8}, dataKeySize), sealed, []byte("aad")); err == nil {
		t.Fatal("opened with the wrong key")
	}
}
