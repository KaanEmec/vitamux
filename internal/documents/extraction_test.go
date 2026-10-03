package documents

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const labFixtures = "../../fixtures/lab"

func compileExtractionSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	abs, err := filepath.Abs("../../schemas/lab-extraction.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(abs)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func schemaAccepts(s *jsonschema.Schema, doc []byte) bool {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	return err == nil && s.Validate(inst) == nil
}

// The committed ground truth of the synthetic lab PDFs (tools/fixturegen labpdf) must pass
// both the JSON Schema and DecodeExtraction.
func TestGroundTruthValidates(t *testing.T) {
	s := compileExtractionSchema(t)
	files, err := filepath.Glob(filepath.Join(labFixtures, "lab-*.json"))
	if err != nil || len(files) < 10 {
		t.Fatalf("want at least 10 ground truth files in %s, got %d (%v)", labFixtures, len(files), err)
	}
	for _, f := range files {
		doc, err := os.ReadFile(f) //nolint:gosec // repository fixtures
		if err != nil {
			t.Fatal(err)
		}
		if !schemaAccepts(s, doc) {
			t.Errorf("%s: rejected by the JSON Schema", f)
		}
		x, err := DecodeExtraction(doc)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if !x.Synthetic {
			t.Errorf("%s: missing synthetic marker", f)
		}
	}
}

func mutateDoc(t *testing.T, doc []byte, f func(m map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(doc, &m); err != nil {
		t.Fatal(err)
	}
	f(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func row0(m map[string]any) map[string]any { return m["rows"].([]any)[0].(map[string]any) }
func docm(m map[string]any) map[string]any { return m["document"].(map[string]any) }

// The Go validator and the JSON Schema must agree on every case.
func TestExtractionValidatorMatchesSchema(t *testing.T) {
	s := compileExtractionSchema(t)
	base, err := os.ReadFile(filepath.Join(labFixtures, "lab-01.json"))
	if err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(m map[string]any){
		"wrong schema":         func(m map[string]any) { m["schema"] = "vitamux.lab.extraction/2" },
		"missing rows":         func(m map[string]any) { delete(m, "rows") },
		"null rows":            func(m map[string]any) { m["rows"] = nil },
		"unknown top field":    func(m map[string]any) { m["patient"] = "x" },
		"missing doc key":      func(m map[string]any) { delete(docm(m), "laboratory") },
		"zero pages":           func(m map[string]any) { docm(m)["page_count"] = 0 },
		"date with offset":     func(m map[string]any) { docm(m)["collected_at"] = "2025-03-14T08:10:00Z" },
		"date as printed":      func(m map[string]any) { docm(m)["collected_at"] = "14.03.2025" },
		"empty date text":      func(m map[string]any) { docm(m)["reported_at_text"] = "" },
		"unknown doc warning":  func(m map[string]any) { m["warnings"] = []any{"looks_fine"} },
		"repeated doc warning": func(m map[string]any) { m["warnings"] = []any{"low_quality_image", "low_quality_image"} },
		"null warnings":        func(m map[string]any) { m["warnings"] = nil },
		"missing row key":      func(m map[string]any) { delete(row0(m), "printed_flag") },
		"unknown row key":      func(m map[string]any) { row0(m)["interpretation"] = "x" },
		"empty label":          func(m map[string]any) { row0(m)["analyte_label"] = "" },
		"null label":           func(m map[string]any) { row0(m)["analyte_label"] = nil },
		"unicode comparator":   func(m map[string]any) { row0(m)["comparator"] = "≤" },
		"empty unit":           func(m map[string]any) { row0(m)["unit_text"] = "" },
		"numeric as string":    func(m map[string]any) { row0(m)["value_numeric"] = "5.2" },
		"page zero":            func(m map[string]any) { row0(m)["page"] = 0 },
		"negative row index":   func(m map[string]any) { row0(m)["row_index"] = -1 },
		"empty evidence":       func(m map[string]any) { row0(m)["evidence_text"] = "" },
		"confidence above 1":   func(m map[string]any) { row0(m)["confidence"] = 1.5 },
		"bbox out of page":     func(m map[string]any) { row0(m)["bbox"] = map[string]any{"x0": 0, "y0": 0, "x1": 2, "y1": 0.1} },
		"bbox missing corner":  func(m map[string]any) { row0(m)["bbox"] = map[string]any{"x0": 0, "y0": 0, "x1": 0.5} },
		"unknown row warning":  func(m map[string]any) { row0(m)["warnings"] = []any{"high_value"} },
		"long flag":            func(m map[string]any) { row0(m)["printed_flag"] = string(bytes.Repeat([]byte("H"), 33)) },
	}
	for name, f := range bad {
		doc := mutateDoc(t, base, f)
		if schemaAccepts(s, doc) {
			t.Errorf("%s: JSON Schema accepted it", name)
		}
		_, err := DecodeExtraction(doc)
		var xe *ExtractionError
		if !errors.As(err, &xe) {
			t.Errorf("%s: DecodeExtraction returned %v, want an ExtractionError", name, err)
		}
	}
	ok := map[string]func(m map[string]any){
		"no synthetic marker": func(m map[string]any) { delete(m, "synthetic") },
		"null bbox":           func(m map[string]any) { row0(m)["bbox"] = nil },
		"unreadable value": func(m map[string]any) {
			r := row0(m)
			r["value_text"], r["value_numeric"], r["warnings"] = nil, nil, []any{"unreadable_value"}
		},
		"date only":      func(m map[string]any) { docm(m)["collected_at"] = "2025-03-14" },
		"seconds":        func(m map[string]any) { docm(m)["reported_at"] = "2025-03-14T08:10:59" },
		"null dates":     func(m map[string]any) { docm(m)["collected_at"], docm(m)["collected_at_text"] = nil, nil },
		"no rows":        func(m map[string]any) { m["rows"], m["warnings"] = []any{}, []any{"not_lab_report"} },
		"ge comparator":  func(m map[string]any) { row0(m)["comparator"] = ">=" },
		"unicode labels": func(m map[string]any) { row0(m)["analyte_label"] = "Glukose nüchtern" },
	}
	for name, f := range ok {
		doc := mutateDoc(t, base, f)
		if !schemaAccepts(s, doc) {
			t.Errorf("%s: JSON Schema rejected it", name)
		}
		if _, err := DecodeExtraction(doc); err != nil {
			t.Errorf("%s: DecodeExtraction: %v", name, err)
		}
	}
}

// Rules the schema cannot express.
func TestExtractionGoOnlyChecks(t *testing.T) {
	base, err := os.ReadFile(filepath.Join(labFixtures, "lab-01.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(m map[string]any){
		"row index out of order": func(m map[string]any) { row0(m)["row_index"] = 3 },
		"page beyond count":      func(m map[string]any) { row0(m)["page"] = 9 },
		"inverted bbox":          func(m map[string]any) { row0(m)["bbox"] = map[string]any{"x0": 0.5, "y0": 0.1, "x1": 0.2, "y1": 0.2} },
		"impossible date":        func(m map[string]any) { docm(m)["collected_at"] = "2025-02-30" },
	}
	for name, f := range cases {
		if _, err := DecodeExtraction(mutateDoc(t, base, f)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := DecodeExtraction(append(base, []byte(" {}")...)); err == nil {
		t.Error("trailing data accepted")
	}
}
