package documents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Extraction contract (schemas/lab-extraction.v1.json) and the prompt it pairs with
// (prompts/lab-extraction/v1.md). The Go checks below mirror the JSON Schema; the schema test
// keeps the two in agreement. Checks the schema cannot express (row_index order, page within
// page_count, bbox orientation, real calendar dates) are Go-only.
const (
	ExtractionSchema = "vitamux.lab.extraction/1"
	PromptVersion    = "lab-extraction/v1"

	MaxExtractionRows  = 500
	MaxExtractionPages = 50
)

// Comparators allowed in Row.Comparator.
var comparators = map[string]bool{"<": true, ">": true, "<=": true, ">=": true}

// Warning codes an extractor may emit. Deterministic validation (J12.4) adds its own codes elsewhere.
var (
	RowWarnings = []string{"unreadable_label", "unreadable_value", "unreadable_unit", "unreadable_range", "unreadable_flag",
		"uncertain_reading", "handwritten", "crossed_out", "split_across_pages", "footnote", "multiple_values"}
	DocumentWarnings = []string{"page_unreadable", "low_quality_image", "possibly_truncated", "not_lab_report", "multiple_reports", "dates_not_found"}
)

var (
	localDateRe  = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])(T([01][0-9]|2[0-3]):[0-5][0-9](:[0-5][0-9])?)?$`)
	localLayouts = []string{"2006-01-02", "2006-01-02T15:04", "2006-01-02T15:04:05"}
)

// Extraction is one extractor response for one document.
type Extraction struct {
	Schema    string       `json:"schema"`
	Synthetic bool         `json:"synthetic,omitempty"`
	Document  DocumentMeta `json:"document"`
	Rows      []Row        `json:"rows"`
	Warnings  []string     `json:"warnings"`
}

// DocumentMeta holds the header fields printed once per report. Dates are ISO 8601 local
// time without offset; the *Text fields keep them as printed.
type DocumentMeta struct {
	Laboratory      *string `json:"laboratory"`
	SpecimenType    *string `json:"specimen_type"`
	CollectedAt     *string `json:"collected_at"`
	CollectedAtText *string `json:"collected_at_text"`
	ReportedAt      *string `json:"reported_at"`
	ReportedAtText  *string `json:"reported_at_text"`
	PageCount       int     `json:"page_count"`
}

// Row is one printed result line. Text fields are verbatim; the numeric fields are parsed
// from them. Specimen, dates and laboratory are the row's effective values (the document's
// unless the row prints its own).
type Row struct {
	Page               int      `json:"page"`
	RowIndex           int      `json:"row_index"`
	AnalyteLabel       string   `json:"analyte_label"`
	ValueText          *string  `json:"value_text"`
	ValueNumeric       *float64 `json:"value_numeric"`
	Comparator         *string  `json:"comparator"`
	UnitText           *string  `json:"unit_text"`
	ReferenceRangeText *string  `json:"reference_range_text"`
	RefLow             *float64 `json:"ref_low"`
	RefHigh            *float64 `json:"ref_high"`
	PrintedFlag        *string  `json:"printed_flag"`
	SpecimenType       *string  `json:"specimen_type"`
	CollectedAt        *string  `json:"collected_at"`
	ReportedAt         *string  `json:"reported_at"`
	Laboratory         *string  `json:"laboratory"`
	EvidenceText       string   `json:"evidence_text"`
	BBox               *BBox    `json:"bbox"`
	Confidence         float64  `json:"confidence"`
	Warnings           []string `json:"warnings"`
}

// BBox is a row area as fractions of the page, origin top left.
type BBox struct {
	X0 float64 `json:"x0"`
	Y0 float64 `json:"y0"`
	X1 float64 `json:"x1"`
	Y1 float64 `json:"y1"`
}

// FieldProblem points at one invalid field (RFC 6901 pointer). Detail never echoes values,
// which may be health data.
type FieldProblem struct {
	Pointer string
	Detail  string
}

// ExtractionError lists every problem found in an extractor response.
type ExtractionError struct{ Errors []FieldProblem }

func (e *ExtractionError) Error() string {
	parts := make([]string, len(e.Errors))
	for i, f := range e.Errors {
		parts[i] = f.Pointer + ": " + f.Detail
	}
	return "documents: invalid extraction: " + strings.Join(parts, "; ")
}

// Every key is required by the schema, also the nullable ones; encoding/json cannot tell a
// missing key from null, so DecodeExtraction checks key presence separately.
var (
	extractionKeys = []string{"schema", "document", "rows", "warnings"}
	documentKeys   = []string{"laboratory", "specimen_type", "collected_at", "collected_at_text", "reported_at", "reported_at_text", "page_count"}
	rowKeys        = []string{"page", "row_index", "analyte_label", "value_text", "value_numeric", "comparator", "unit_text",
		"reference_range_text", "ref_low", "ref_high", "printed_flag", "specimen_type", "collected_at", "reported_at",
		"laboratory", "evidence_text", "bbox", "confidence", "warnings"}
	bboxKeys = []string{"x0", "y0", "x1", "y1"}
)

// DecodeExtraction parses and validates an extractor response. Errors are *ExtractionError.
func DecodeExtraction(data []byte) (*Extraction, error) {
	var x Extraction
	if err := exDecodeStrict(data, &x); err != nil {
		return nil, err
	}
	var c exChecker
	var top struct {
		Document json.RawMessage   `json:"document"`
		Rows     []json.RawMessage `json:"rows"`
	}
	_ = json.Unmarshal(data, &top) // already decoded strictly above
	c.keys(data, "", extractionKeys)
	if top.Document != nil {
		c.keys(top.Document, "/document", documentKeys)
	}
	for i, r := range top.Rows {
		p := "/rows/" + strconv.Itoa(i)
		c.keys(r, p, rowKeys)
		var row struct {
			BBox json.RawMessage `json:"bbox"`
		}
		if json.Unmarshal(r, &row) == nil && row.BBox != nil && string(row.BBox) != "null" {
			c.keys(row.BBox, p+"/bbox", bboxKeys)
		}
	}
	if err := c.err(); err != nil {
		return nil, err
	}
	return &x, x.Validate()
}

func exDecodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil {
		if _, extra := dec.Token(); !errors.Is(extra, io.EOF) {
			err = errors.New("trailing data after the JSON value")
		}
	}
	if err == nil {
		return nil
	}
	ptr, detail := "", "invalid JSON"
	var typeErr *json.UnmarshalTypeError
	var syntaxErr *json.SyntaxError
	switch {
	case errors.As(err, &typeErr):
		ptr, detail = "/"+strings.ReplaceAll(typeErr.Field, ".", "/"), "must be "+typeErr.Type.Kind().String()
	case errors.As(err, &syntaxErr):
		detail = "invalid JSON at offset " + strconv.FormatInt(syntaxErr.Offset, 10)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		detail = strings.TrimPrefix(err.Error(), "json: ")
	case strings.HasPrefix(err.Error(), "trailing"):
		detail = err.Error()
	}
	return &ExtractionError{Errors: []FieldProblem{{Pointer: ptr, Detail: detail}}}
}

type exChecker struct{ errs []FieldProblem }

func (c *exChecker) check(ok bool, ptr, detail string) {
	if !ok {
		c.errs = append(c.errs, FieldProblem{Pointer: ptr, Detail: detail})
	}
}

func (c *exChecker) err() error {
	if len(c.errs) == 0 {
		return nil
	}
	return &ExtractionError{Errors: c.errs}
}

func (c *exChecker) keys(obj json.RawMessage, ptr string, want []string) {
	var m map[string]json.RawMessage
	if json.Unmarshal(obj, &m) != nil {
		c.check(false, ptr, "must be an object")
		return
	}
	for _, k := range want {
		_, ok := m[k]
		c.check(ok, ptr+"/"+k, "is required (use null when not printed)")
	}
}

func (c *exChecker) text(s *string, nullable bool, maxRunes int, ptr string) {
	if s == nil {
		c.check(nullable, ptr, "must not be null")
		return
	}
	n := utf8.RuneCountInString(*s)
	c.check(n >= 1 && n <= maxRunes, ptr, fmt.Sprintf("must be 1-%d characters", maxRunes))
}

func (c *exChecker) date(s *string, ptr string) {
	if s == nil {
		return
	}
	ok := false
	if localDateRe.MatchString(*s) {
		for _, l := range localLayouts {
			if _, err := time.Parse(l, *s); err == nil {
				ok = true
			}
		}
	}
	c.check(ok, ptr, "must be an ISO 8601 local date or date-time without offset")
}

func (c *exChecker) fraction(v float64, ptr string) {
	c.check(v >= 0 && v <= 1, ptr, "must be between 0 and 1")
}

func (c *exChecker) warnings(ws []string, allowed []string, ptr string) {
	c.check(ws != nil, ptr, "must be an array")
	seen := map[string]bool{}
	for i, w := range ws {
		known := false
		for _, a := range allowed {
			known = known || a == w
		}
		c.check(known, ptr+"/"+strconv.Itoa(i), "is not a known warning code")
		c.check(!seen[w], ptr+"/"+strconv.Itoa(i), "is repeated")
		seen[w] = true
	}
}

// Validate checks x against schemas/lab-extraction.v1.json plus the Go-only rules.
func (x *Extraction) Validate() error {
	var c exChecker
	c.check(x.Schema == ExtractionSchema, "/schema", "must be "+ExtractionSchema)
	d := x.Document
	c.text(d.Laboratory, true, 200, "/document/laboratory")
	c.text(d.SpecimenType, true, 64, "/document/specimen_type")
	c.date(d.CollectedAt, "/document/collected_at")
	c.text(d.CollectedAtText, true, 64, "/document/collected_at_text")
	c.date(d.ReportedAt, "/document/reported_at")
	c.text(d.ReportedAtText, true, 64, "/document/reported_at_text")
	c.check(d.PageCount >= 1 && d.PageCount <= MaxExtractionPages, "/document/page_count", fmt.Sprintf("must be 1 to %d", MaxExtractionPages))
	c.check(x.Rows != nil && len(x.Rows) <= MaxExtractionRows, "/rows", fmt.Sprintf("must be an array of at most %d rows", MaxExtractionRows))
	for i, r := range x.Rows {
		r.validate(&c, "/rows/"+strconv.Itoa(i), i, d.PageCount)
	}
	c.warnings(x.Warnings, DocumentWarnings, "/warnings")
	return c.err()
}

func (r Row) validate(c *exChecker, p string, i, pages int) {
	c.check(r.Page >= 1 && r.Page <= MaxExtractionPages, p+"/page", fmt.Sprintf("must be 1 to %d", MaxExtractionPages))
	c.check(r.Page <= pages, p+"/page", "must not exceed document page_count")
	c.check(r.RowIndex == i, p+"/row_index", "must equal the row's position in rows")
	c.text(&r.AnalyteLabel, false, 200, p+"/analyte_label")
	c.text(r.ValueText, true, 200, p+"/value_text")
	c.check(r.Comparator == nil || comparators[*r.Comparator], p+"/comparator", "must be <, >, <= or >=")
	c.text(r.UnitText, true, 64, p+"/unit_text")
	c.text(r.ReferenceRangeText, true, 200, p+"/reference_range_text")
	c.text(r.PrintedFlag, true, 32, p+"/printed_flag")
	c.text(r.SpecimenType, true, 64, p+"/specimen_type")
	c.date(r.CollectedAt, p+"/collected_at")
	c.date(r.ReportedAt, p+"/reported_at")
	c.text(r.Laboratory, true, 200, p+"/laboratory")
	c.text(&r.EvidenceText, false, 1000, p+"/evidence_text")
	if b := r.BBox; b != nil {
		c.fraction(b.X0, p+"/bbox/x0")
		c.fraction(b.Y0, p+"/bbox/y0")
		c.fraction(b.X1, p+"/bbox/x1")
		c.fraction(b.Y1, p+"/bbox/y1")
		c.check(b.X0 < b.X1 && b.Y0 < b.Y1, p+"/bbox", "must have x0 < x1 and y0 < y1")
	}
	c.fraction(r.Confidence, p+"/confidence")
	c.warnings(r.Warnings, RowWarnings, p+"/warnings")
}
