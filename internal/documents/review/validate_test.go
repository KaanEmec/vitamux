package review

import (
	"slices"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/documents"
)

func sp(s string) *string   { return &s }
func fp(v float64) *float64 { return &v }

// row is a clean synthetic creatinine row that matches the page text below.
func row() documents.Row {
	return documents.Row{Page: 1, AnalyteLabel: "Creatinine", ValueText: sp("0.75"), ValueNumeric: fp(0.75), UnitText: sp("mg/dL"),
		ReferenceRangeText: sp("0.60 - 1.30"), RefLow: fp(0.6), RefHigh: fp(1.3), CollectedAt: sp("2025-07-17T07:02"),
		ReportedAt: sp("2025-07-18T15:00"), EvidenceText: "Creatinine 0.75 mg/dL 0.60 - 1.30"}
}

var page = []string{"Synthetic Lab  Creatinine 0.75 mg/dL 0.60 - 1.30  Glucose 100 H mg/dL 70 - 99"}

func check(t *testing.T, c Context, in ...Input) [][]string {
	t.Helper()
	if c.Now.IsZero() {
		c.Now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if c.Pages == nil {
		c.Pages = page
	}
	return Check(c, in)
}

func TestSeededModelErrors(t *testing.T) {
	ambiguous := documents.DocumentMeta{CollectedAtText: sp("10/09/2025 09:31")}
	for name, tc := range map[string]struct {
		mutate func(*documents.Row)
		doc    documents.DocumentMeta
		want   []string
	}{
		"clean":             {func(*documents.Row) {}, documents.DocumentMeta{}, []string{}},
		"wrong decimal":     {func(r *documents.Row) { r.ValueNumeric = fp(7.5) }, documents.DocumentMeta{}, []string{WarnValueMismatch}},
		"wrong decimal too": {func(r *documents.Row) { r.ValueText, r.ValueNumeric = sp("7.5"), fp(7.5) }, documents.DocumentMeta{}, []string{WarnValueNotInEvidence}},
		"swapped units":     {func(r *documents.Row) { r.UnitText = sp("mIU/L") }, documents.DocumentMeta{}, []string{WarnUnknownUnit}},
		"invented row": {func(r *documents.Row) {
			r.AnalyteLabel, r.ValueText, r.ValueNumeric, r.EvidenceText = "Ferritin", sp("88"), fp(88), "Ferritin 88 ng/mL"
			r.UnitText, r.ReferenceRangeText, r.RefLow, r.RefHigh = sp("ng/mL"), nil, nil, nil
		}, documents.DocumentMeta{}, []string{WarnEvidenceUnverified}},
		"ambiguous date":  {func(r *documents.Row) { r.CollectedAt = sp("2025-10-09T09:31"); r.ReportedAt = nil }, ambiguous, []string{WarnDateAmbiguous}},
		"missing date":    {func(r *documents.Row) { r.CollectedAt = nil }, documents.DocumentMeta{}, []string{WarnDateMissing}},
		"future date":     {func(r *documents.Row) { r.CollectedAt, r.ReportedAt = sp("2026-03-01"), nil }, documents.DocumentMeta{}, []string{WarnDateInFuture}},
		"reported first":  {func(r *documents.Row) { r.ReportedAt = sp("2025-07-16") }, documents.DocumentMeta{}, []string{WarnDateOrder}},
		"same day report": {func(r *documents.Row) { r.ReportedAt = sp("2025-07-17") }, documents.DocumentMeta{}, []string{}},
		"lost comparator": {func(r *documents.Row) { r.ValueText = sp("< 0.75") }, documents.DocumentMeta{}, []string{WarnComparatorMismatch}},
		"range swapped":   {func(r *documents.Row) { r.RefLow, r.RefHigh = fp(1.3), fp(0.6) }, documents.DocumentMeta{}, []string{WarnRangeMismatch}},
		"missing unit":    {func(r *documents.Row) { r.UnitText = nil }, documents.DocumentMeta{}, []string{WarnUnitMissing}},
		"different quantity": {func(r *documents.Row) {
			r.AnalyteLabel, r.UnitText = "Lp(a)", sp("nmol/L")
		}, documents.DocumentMeta{}, []string{WarnUnitNotConvertible}},
	} {
		r := row()
		tc.mutate(&r)
		analyte := "creatinine"
		switch r.AnalyteLabel {
		case "Ferritin":
			analyte = "ferritin"
		case "Lp(a)":
			analyte = "lpa_mass"
		}
		got := check(t, Context{Doc: tc.doc}, Input{Row: r, Analyte: analyte})[0]
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

func TestReparsing(t *testing.T) {
	for _, tc := range []struct {
		text string
		num  *float64
		cmp  *string
		ok   bool
	}{
		{"0,87", fp(0.87), nil, true},      // decimal comma
		{"250,000", fp(250000), nil, true}, // thousands
		{"1.234,5", fp(1234.5), nil, true},
		{"≤ 0.19", fp(0.19), sp("<="), true},
		{">1476", fp(1476), sp(">"), true},
		{"Negative", nil, nil, true},
		{"Negative", fp(1), nil, false},
		{"12", nil, nil, false}, // a printed number that was not transcribed
	} {
		r := documents.Row{ValueText: sp(tc.text), ValueNumeric: tc.num, Comparator: tc.cmp}
		if got := valueChecks(r); (len(got) == 0) != tc.ok {
			t.Errorf("%q: %v", tc.text, got)
		}
	}
	for _, tc := range []struct {
		text      string
		low, high *float64
	}{
		{"0.27 – 4.20", fp(0.27), fp(4.2)},
		{"1.005-1.030", fp(1.005), fp(1.03)},
		{"< 34", nil, fp(34)},
		{">60", fp(60), nil},
		{"≤ 7.0", nil, fp(7)},
		{"70 - 99 mg/dL", fp(70), fp(99)},
		{"Negative", nil, nil},
		{"<1:80", nil, nil},
	} {
		if !rangeMatches(documents.Row{ReferenceRangeText: sp(tc.text), RefLow: tc.low, RefHigh: tc.high}) {
			t.Errorf("range %q did not match", tc.text)
		}
	}
}

func TestDuplicatesAndUnknown(t *testing.T) {
	a, b := row(), row()
	b.RowIndex = 1
	got := check(t, Context{}, Input{Row: a, Analyte: "creatinine"}, Input{Row: b, Analyte: "creatinine"}, Input{Row: row()})
	if !slices.Contains(got[0], WarnDuplicateInRun) || !slices.Contains(got[1], WarnDuplicateInRun) || !slices.Equal(got[2], []string{WarnUnknownAnalyte}) {
		t.Errorf("in run: %v", got)
	}
	// A rejected duplicate no longer counts.
	got = check(t, Context{}, Input{Row: a, Analyte: "creatinine"}, Input{Row: b, Analyte: "creatinine", Rejected: true})
	if slices.Contains(got[0], WarnDuplicateInRun) {
		t.Errorf("rejected duplicate: %v", got)
	}
	got = check(t, Context{Confirmed: map[string]bool{DupKey("creatinine", "2025-07-17"): true}}, Input{Row: a, Analyte: "creatinine"})
	if !slices.Equal(got[0], []string{WarnAlreadyConfirmed}) {
		t.Errorf("already confirmed: %v", got)
	}
	// An owner edit of the date settles the ambiguity; no text layer skips the evidence check.
	a.EvidenceText = "not on the page"
	got = check(t, Context{Doc: documents.DocumentMeta{CollectedAtText: sp("03/04/2025")}, Pages: []string{""}},
		Input{Row: a, Analyte: "creatinine", Edited: map[string]bool{"collected_at": true}})
	if len(got[0]) != 1 || got[0][0] != WarnValueNotInEvidence {
		t.Errorf("edited date, no text layer: %v", got)
	}
}
