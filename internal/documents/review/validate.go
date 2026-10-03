// Package review implements deterministic validation, owner review and confirmation of
// extracted lab rows (docs/architecture/lab-documents.md#validation and
// #review-and-confirmation). Validation only produces warnings that point the reviewer at
// something to compare with the PDF; it never judges a result, and nothing is confirmed
// without the owner reviewing every row.
package review

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/KaanEmec/vitamux/internal/documents"
	"github.com/KaanEmec/vitamux/internal/documents/analytes"
)

// Validation warning codes. They are about the transcription, never about the value.
const (
	WarnValueMismatch      = "value_mismatch"            // value_numeric is not the number value_text prints
	WarnComparatorMismatch = "comparator_mismatch"       // comparator is not the one value_text prints
	WarnRangeMismatch      = "range_mismatch"            // ref_low/ref_high are not the bounds reference_range_text prints
	WarnEvidenceUnverified = "evidence_unverified"       // evidence_text is not in the PDF text layer
	WarnValueNotInEvidence = "value_not_in_evidence"     // value_text does not occur in evidence_text
	WarnUnknownUnit        = "unknown_unit"              // not a unit of the analyte
	WarnUnitNotConvertible = "unit_not_convertible"      // a unit of a different quantity (e.g. Lp(a) nmol/L vs mg/dL)
	WarnUnitMissing        = "unit_missing"              // no unit although the analyte has one
	WarnDateMissing        = "date_missing"              // no collection date: required to confirm
	WarnDateInFuture       = "date_in_future"            // collected or reported after today
	WarnDateAmbiguous      = "date_ambiguous"            // the printed date reads both day-month and month-day
	WarnDateOrder          = "reported_before_collected" // reported_at precedes collected_at
	WarnDuplicateInRun     = "duplicate_in_run"          // another row has the same analyte and collection date
	WarnAlreadyConfirmed   = "already_confirmed"         // a confirmed result has the same analyte and collection date
	WarnUnknownAnalyte     = "unknown_analyte"           // no alias matches the label; confirmable as is
)

// Input is one row to check: its current (possibly edited) values, the analyte review would
// confirm ("" for unknown) and the fields the owner edited, whose checks against the
// extractor's evidence no longer apply.
type Input struct {
	Row      documents.Row
	Analyte  string
	Rejected bool
	Edited   map[string]bool
}

// Context is what the checks compare rows with.
type Context struct {
	Doc   documents.DocumentMeta
	Pages []string  // documents.TextLayer; nil or "" for a page without a text layer
	Now   time.Time // for date_in_future
	// Confirmed holds DupKey(analyte, date) of results confirmed from other runs.
	Confirmed map[string]bool
}

// DupKey identifies an analyte on a collection date.
func DupKey(analyte, date string) string { return analyte + "|" + date }

// Check returns the validation warnings of each row, in a stable order.
func Check(c Context, rows []Input) [][]string {
	var all strings.Builder
	pageKeys := make([]string, len(c.Pages))
	for i, p := range c.Pages {
		pageKeys[i] = matchKey(p)
		all.WriteString(pageKeys[i])
	}
	docKey := all.String()
	inRun := map[string]int{}
	for _, in := range rows {
		if !in.Rejected && in.Analyte != "" {
			inRun[DupKey(in.Analyte, datePart(in.Row.CollectedAt))]++
		}
	}
	ambiguous := ambiguousDate(c.Doc.CollectedAtText)
	out := make([][]string, len(rows))
	for i, in := range rows {
		r, w := in.Row, []string{}
		add := func(ok bool, code string) {
			if !ok {
				w = append(w, code)
			}
		}
		w = append(w, valueChecks(r)...)
		add(rangeMatches(r), WarnRangeMismatch)

		if r.Page >= 1 && r.Page <= len(pageKeys) && pageKeys[r.Page-1] != "" {
			ev := matchKey(r.EvidenceText)
			add(ev == "" || strings.Contains(pageKeys[r.Page-1], ev) || strings.Contains(docKey, ev), WarnEvidenceUnverified)
		}
		if r.ValueText != nil && !in.Edited["value_text"] {
			v := matchKey(*r.ValueText)
			add(v == "" || strings.Contains(matchKey(r.EvidenceText), v), WarnValueNotInEvidence)
		}

		if in.Analyte != "" && r.ValueNumeric != nil { // qualitative results have no unit to check
			if an, ok := analytes.Lookup(in.Analyte); ok && an.Unit != "" {
				_, _, err := analytes.Canonical(in.Analyte, 1, deref(r.UnitText))
				switch {
				case errors.Is(err, analytes.ErrNoConversion):
					w = append(w, WarnUnitNotConvertible)
				case errors.Is(err, analytes.ErrUnknownUnit) && r.UnitText == nil:
					w = append(w, WarnUnitMissing)
				case errors.Is(err, analytes.ErrUnknownUnit):
					w = append(w, WarnUnknownUnit)
				}
			}
		}

		collected, cOK := parseLocal(r.CollectedAt)
		reported, rOK := parseLocal(r.ReportedAt)
		add(r.CollectedAt != nil, WarnDateMissing)
		limit := c.Now.UTC().Add(24 * time.Hour) // local wall times: a day covers every UTC offset
		if cOK && collected.After(limit) || rOK && reported.After(limit) {
			w = append(w, WarnDateInFuture)
		}
		add(!ambiguous || in.Edited["collected_at"], WarnDateAmbiguous)
		if cOK && rOK {
			if len(*r.CollectedAt) == 10 || len(*r.ReportedAt) == 10 { // a date only: compare days
				collected, reported = collected.Truncate(24*time.Hour), reported.Truncate(24*time.Hour)
			}
			add(!reported.Before(collected), WarnDateOrder)
		}

		if in.Analyte == "" {
			w = append(w, WarnUnknownAnalyte)
		} else {
			key := DupKey(in.Analyte, datePart(r.CollectedAt))
			add(in.Rejected || inRun[key] < 2, WarnDuplicateInRun)
			add(r.CollectedAt == nil || !c.Confirmed[key], WarnAlreadyConfirmed)
		}
		out[i] = w
	}
	return out
}

// valueChecks re-parses value_text and compares it with value_numeric and comparator.
func valueChecks(r documents.Row) []string {
	var w []string
	cmp, nums := "", []float64(nil)
	if r.ValueText != nil {
		var rest string
		cmp, rest = splitComparator(*r.ValueText)
		nums = parseNumber(rest)
	}
	if (r.ValueNumeric == nil) != (len(nums) == 0) || (r.ValueNumeric != nil && !containsNum(nums, *r.ValueNumeric)) {
		w = append(w, WarnValueMismatch)
	}
	if cmp != deref(r.Comparator) {
		w = append(w, WarnComparatorMismatch)
	}
	return w
}

var (
	rangeBounds = regexp.MustCompile(`^([0-9][0-9.,]*)\s*(?:-|–|—|to|bis)\s*([0-9][0-9.,]*)(?:\s.*)?$`)
	rangeOne    = regexp.MustCompile(`^([0-9][0-9.,]*)(?:\s.*)?$`)
)

// rangeMatches re-parses reference_range_text: "a - b", "< b", "≤ b", "> a" or "≥ a", with an
// optional trailing unit. Text that is none of these (qualitative ranges) has no bounds.
func rangeMatches(r documents.Row) bool {
	var low, high []float64
	if r.ReferenceRangeText != nil {
		cmp, rest := splitComparator(*r.ReferenceRangeText)
		switch m := rangeBounds.FindStringSubmatch(rest); {
		case cmp == "" && m != nil:
			low, high = parseNumber(m[1]), parseNumber(m[2])
		case cmp != "":
			if m := rangeOne.FindStringSubmatch(rest); m != nil {
				if cmp[0] == '<' {
					high = parseNumber(m[1])
				} else {
					low = parseNumber(m[1])
				}
			}
		}
	}
	return boundMatches(low, r.RefLow) && boundMatches(high, r.RefHigh)
}

func boundMatches(parsed []float64, v *float64) bool {
	if v == nil {
		return len(parsed) == 0
	}
	return containsNum(parsed, *v)
}

// splitComparator returns the leading comparator of s in the extraction's spelling
// (<, >, <=, >=) and the rest.
func splitComparator(s string) (string, string) {
	s = strings.TrimSpace(s)
	for _, p := range []struct{ prefix, cmp string }{{"<=", "<="}, {">=", ">="}, {"≤", "<="}, {"≥", ">="}, {"<", "<"}, {">", ">"}} {
		if strings.HasPrefix(s, p.prefix) {
			return p.cmp, strings.TrimSpace(s[len(p.prefix):])
		}
	}
	return "", s
}

var numberRe = regexp.MustCompile(`^[+-]?[0-9]+(?:[.,][0-9]+)*$`)

// parseNumber returns the values a printed number may mean: with one separator both the
// decimal and the thousands reading (0,87 and 1,476), with both kinds the last one is the
// decimal mark. Nil when s is not a number.
func parseNumber(s string) []float64 {
	s = strings.TrimSpace(s)
	if !numberRe.MatchString(s) {
		return nil
	}
	dots, commas := strings.Count(s, "."), strings.Count(s, ",")
	var cands []string
	switch {
	case dots == 0 && commas == 0:
		cands = []string{s}
	case dots > 0 && commas > 0:
		if strings.LastIndex(s, ".") > strings.LastIndex(s, ",") {
			cands = []string{strings.ReplaceAll(s, ",", "")}
		} else {
			cands = []string{strings.ReplaceAll(strings.ReplaceAll(s, ".", ""), ",", ".")}
		}
	default:
		sep := "."
		if commas > 0 {
			sep = ","
		}
		if strings.Count(s, sep) == 1 {
			cands = append(cands, strings.Replace(s, sep, ".", 1))
		}
		if groups := strings.Split(s, sep); thousands(groups) {
			cands = append(cands, strings.Join(groups, ""))
		}
	}
	var out []float64
	for _, c := range cands {
		if v, err := strconv.ParseFloat(c, 64); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func thousands(groups []string) bool {
	for _, g := range groups[1:] {
		if len(g) != 3 {
			return false
		}
	}
	return len(strings.TrimLeft(groups[0], "+-")) <= 3
}

func containsNum(vs []float64, v float64) bool {
	for _, x := range vs {
		d := x - v
		if d < 0 {
			d = -d
		}
		scale := max(abs(x), abs(v), 1)
		if d <= 1e-9*scale {
			return true
		}
	}
	return false
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// matchKey reduces text to what the evidence check compares: lower-case letters, digits and
// decimal marks. Spacing, punctuation and symbols (≤, ↑, which simple text layers often
// cannot decode) are dropped; µ and the Greek mu are the same letter.
func matchKey(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r == 'μ':
			sb.WriteRune('µ')
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == ',':
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

var ambiguousRe = regexp.MustCompile(`^\s*([0-9]{1,2})[./-]([0-9]{1,2})[./-][0-9]{2,4}\b`)

// ambiguousDate reports whether a printed date reads both as day-month and month-day
// (10/09/2025), so the extractor's choice needs a human check.
func ambiguousDate(text *string) bool {
	if text == nil {
		return false
	}
	m := ambiguousRe.FindStringSubmatch(*text)
	if m == nil {
		return false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return a != b && a >= 1 && a <= 12 && b >= 1 && b <= 12
}

var localLayouts = []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"}

// parseLocal parses an ISO 8601 local date or date-time without offset as a wall time in UTC.
func parseLocal(s *string) (time.Time, bool) {
	if s == nil {
		return time.Time{}, false
	}
	for _, l := range localLayouts {
		if t, err := time.Parse(l, *s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func datePart(s *string) string {
	if s == nil || len(*s) < 10 {
		return ""
	}
	return (*s)[:10]
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
