package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/documents"
)

// labpdf writes synthetic blood-test reports as real PDFs plus their ground truth in the
// extraction contract (schemas/lab-extraction.v1.json), for J12 extractor and review tests.
// Labs, patients and values are fictional: values are drawn from the seed within the printed
// range, a few deliberately outside it so printed flags appear. Analyte codes in the manifest
// come from docs/architecture/analyte-catalog.md; an empty code is an analyte the catalogue
// does not know.
//
//	go run ./tools/fixturegen labpdf -seed 42 -out fixtures/generated/lab -truth fixtures/lab

func labpdfMain(args []string) int {
	fs := flag.NewFlagSet("labpdf", flag.ExitOnError)
	seed := fs.Uint64("seed", 42, "PRNG seed")
	out := fs.String("out", "fixtures/generated/lab", "directory for the PDFs (git-ignored)")
	truth := fs.String("truth", "fixtures/lab", "directory for the ground truth JSON and manifest (committed)")
	_ = fs.Parse(args)
	if err := generateLabPDFs(*out, *truth, *seed); err != nil {
		fmt.Fprintln(os.Stderr, "fixturegen labpdf:", err)
		return 1
	}
	return 0
}

type labReport struct {
	id, lab, title string
	layout         string // table | inline | stacked | twocol
	scanned        bool   // raster image only, no text layer (ASCII only)
	letter         bool   // US Letter, else A4
	german         bool   // German labels and decimal comma
	dates          string // iso | us | de | dmy (day and month order not determinable)
	specimen       string
	cols           []labCol
	size           int    // row font size
	rangeSep       string // between lo and hi
	cmpSpace       bool   // "< 5" rather than "<5"
	perPage        int    // rows per page at most; 0 = as many as fit
	sections       []labSection
	features       []string
}

type labCol struct {
	head  string
	field string // label | value | flag | unit | range
	x     int
}

type labSection struct {
	heading, specimen string // specimen overrides the report's for these rows
	rows              []labRow
}

// labRow describes one printed result. lo/hi are printed bounds in '.' notation; rng picks
// the range shape: "" lo-hi, "<" or "<=" below hi, ">" above lo, "text" refText verbatim.
type labRow struct {
	code, label, unit string
	lo, hi, rng       string
	refText           string
	fixed             string // printed result instead of a random one ('.' notation)
	flag              string // printed flag
	out               int    // +1 above the range, -1 below
	hba1cPct          bool   // % (NGSP) derived from the previous row's mmol/mol
	unreadable        bool   // result cell smudged (scanned report only)
}

const labNotice = "SYNTHETIC TEST DOCUMENT - fictional, not a medical record"

func generateLabPDFs(out, truth string, seed uint64) error {
	for _, d := range []string{out, truth} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return err
		}
	}
	man := labManifest{Synthetic: true, Generator: "fixturegen labpdf", Seed: seed, Schema: documents.ExtractionSchema}
	for _, rep := range labReports() {
		pdf, x, codes := renderLabReport(rep, seed)
		js, err := marshalTruth(x)
		if err != nil {
			return err
		}
		if _, err := documents.DecodeExtraction(js); err != nil {
			return fmt.Errorf("%s: ground truth invalid: %w", rep.id, err)
		}
		name := rep.id + ".pdf"
		if err := os.WriteFile(filepath.Join(out, name), pdf, 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, name+".synthetic"), []byte("synthetic: true\n"), 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(truth, rep.id+".json"), js, 0o600); err != nil {
			return err
		}
		sum := sha256.Sum256(pdf)
		size := "a4"
		if rep.letter {
			size = "letter"
		}
		man.Reports = append(man.Reports, labManifestReport{ID: rep.id, PDF: name, SHA256: hex.EncodeToString(sum[:]),
			Pages: x.Document.PageCount, TextLayer: !rep.scanned, Layout: rep.layout, PageSize: size, Features: rep.features, Analytes: codes})
	}
	js, err := marshalTruth(man)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(truth, "manifest.json"), js, 0o600)
}

// labManifest indexes the reports: PDF digest (pins the generator output), layout, the
// features each report exercises and the catalogue code per row (null = not in the catalogue).
type labManifest struct {
	Synthetic bool                `json:"synthetic"`
	Generator string              `json:"generator"`
	Seed      uint64              `json:"seed"`
	Schema    string              `json:"schema"`
	Reports   []labManifestReport `json:"reports"`
}

type labManifestReport struct {
	ID        string    `json:"id"`
	PDF       string    `json:"pdf"`
	SHA256    string    `json:"sha256"`
	Pages     int       `json:"pages"`
	TextLayer bool      `json:"text_layer"`
	Layout    string    `json:"layout"`
	PageSize  string    `json:"page_size"`
	Features  []string  `json:"features"`
	Analytes  []*string `json:"analytes"`
}

func marshalTruth(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(v)
	return b.Bytes(), err
}

// ---- layout ----

type labDates struct {
	collected, reported         time.Time
	collectedText, reportedText string
	iso                         bool // false when the printed order of day and month is ambiguous
}

func labReportDates(rep labReport, seed uint64) labDates {
	r := stream(seed, "labpdf/"+rep.id, 0)
	c := time.Date(2025, 1, 1, between(r, 7, 10), between(r, 0, 59), 0, 0, time.UTC).AddDate(0, 0, between(r, 0, 330))
	if rep.dates == "dmy" { // day and month of both dates <= 12 and different
		m := between(r, 1, 10)
		c = time.Date(2025, time.Month(m), between(r, m+1, 11), c.Hour(), c.Minute(), 0, 0, time.UTC)
	}
	rep2 := time.Date(c.Year(), c.Month(), c.Day()+1, between(r, 12, 17), between(r, 0, 59), 0, 0, time.UTC)
	layout := map[string]string{"iso": "2006-01-02 15:04", "us": "Jan 2, 2006 3:04 PM", "de": "02.01.2006 15:04", "dmy": "02/01/2006 15:04"}[rep.dates]
	return labDates{collected: c, reported: rep2, collectedText: c.Format(layout), reportedText: rep2.Format(layout), iso: rep.dates != "dmy"}
}

type labLayout struct {
	rep       labReport
	w, h      int
	lh, rowH  int // header line height and table row height
	pages     []canvas
	rows      []documents.Row
	codes     []*string
	dates     labDates
	newCanvas func() canvas
	total     int
	pageRows  int
	y         int
}

// renderLabReport lays the report out twice: once to count pages, once with "Page n of N".
func renderLabReport(rep labReport, seed uint64) ([]byte, documents.Extraction, []*string) {
	l := layoutLab(rep, seed, 0)
	l = layoutLab(rep, seed, len(l.pages))
	pages := make([]pdfPage, len(l.pages))
	for i, c := range l.pages {
		switch c := c.(type) {
		case *pdfCanvas:
			pages[i] = pdfPage{content: c.buf.Bytes()}
		case *rasterCanvas:
			c.speckle()
			pages[i] = pdfPage{image: c}
		}
	}
	strp := func(s string) *string { return &s }
	doc := documents.DocumentMeta{Laboratory: strp(rep.lab), SpecimenType: strp(rep.specimen),
		CollectedAtText: strp(l.dates.collectedText), ReportedAtText: strp(l.dates.reportedText), PageCount: len(pages)}
	if l.dates.iso {
		doc.CollectedAt, doc.ReportedAt = strp(l.dates.collected.Format("2006-01-02T15:04")), strp(l.dates.reported.Format("2006-01-02T15:04"))
	}
	for i := range l.rows {
		l.rows[i].CollectedAt, l.rows[i].ReportedAt = doc.CollectedAt, doc.ReportedAt
	}
	x := documents.Extraction{Schema: documents.ExtractionSchema, Synthetic: true, Document: doc, Rows: l.rows, Warnings: []string{}}
	return writePDF(l.w, l.h, "Synthetic lab report "+rep.id, pages), x, l.codes
}

func layoutLab(rep labReport, seed uint64, total int) *labLayout {
	l := &labLayout{rep: rep, w: 595, h: 842, lh: 13, rowH: 14, total: total, dates: labReportDates(rep, seed), rows: []documents.Row{}}
	if rep.letter {
		l.w, l.h = 612, 792
	}
	rnd := stream(seed, "labpdf-raster/"+rep.id, 0)
	l.newCanvas = func() canvas { return &pdfCanvas{} }
	if rep.scanned {
		l.lh, l.rowH = 17, 20
		l.newCanvas = func() canvas { return newRasterCanvas(l.w, l.h, rnd) }
	}
	values := stream(seed, "labpdf-values/"+rep.id, 0)
	l.newPage()
	if rep.layout == "twocol" {
		l.twoColumns(values)
		return l
	}
	for _, s := range rep.sections {
		if l.y-18-l.rowH < 60 || (rep.perPage > 0 && l.pageRows >= rep.perPage) {
			l.newPage()
		}
		if s.heading != "" {
			l.y -= 18
			l.cur().text(40, l.y, 10, true, s.heading)
		}
		if rep.layout == "table" {
			l.tableHead(40)
		}
		prev := 0
		for _, row := range s.rows {
			need := l.rowH
			if rep.layout == "stacked" {
				need = 30
			}
			if l.y-need < 60 || (rep.perPage > 0 && l.pageRows >= rep.perPage) {
				l.newPage()
				if rep.layout == "table" {
					l.tableHead(40)
				}
			}
			cells, numeric := l.cells(row, values, prev)
			prev = numeric
			l.row(row, s, cells, 0)
		}
	}
	return l
}

func (l *labLayout) cur() canvas { return l.pages[len(l.pages)-1] }

func (l *labLayout) german(en, de string) string {
	if l.rep.german {
		return de
	}
	return en
}

func (l *labLayout) newPage() {
	c := l.newCanvas()
	l.pages = append(l.pages, c)
	l.pageRows = 0
	n := len(l.pages)
	num := strings.TrimPrefix(l.rep.id, "lab-")
	y := l.h - 50
	c.text(40, y, 15, true, l.rep.lab)
	y -= l.lh
	c.text(40, y, 8, false, "1 Fixture Way, Testville - synthetic laboratory")
	y -= l.lh + 10
	c.text(40, y, 12, true, l.rep.title)
	y -= l.lh + 3
	c.text(40, y, 9, false, fmt.Sprintf("Patient: SYNTHETIC PATIENT %s   %s SYN-00%s   %s 0000-00-00", num,
		l.german("Patient ID:", "Patienten-ID:"), num, l.german("DOB:", "Geb.-Datum:")))
	y -= l.lh
	c.text(40, y, 9, false, fmt.Sprintf("%s %s   %s %s", l.german("Collected:", "Entnahme:"), l.dates.collectedText,
		l.german("Reported:", "Befund:"), l.dates.reportedText))
	y -= l.lh
	c.text(40, y, 9, false, l.german("Specimen: ", "Material: ")+l.rep.specimen)
	y -= 8
	c.rule(40, y, l.w-40, y)
	if l.total > 0 {
		c.text(40, 30, 7, false, labNotice)
		p := fmt.Sprintf(l.german("Page %d of %d", "Seite %d von %d"), n, l.total)
		c.text(l.w-40-c.width(p, 7, false)/1000, 30, 7, false, p)
	}
	l.y = y
}

func (l *labLayout) tableHead(x0 int) {
	l.y -= l.rowH + 2
	for _, col := range l.rep.cols {
		l.cur().text(x0+col.x-40, l.y, l.rep.size, true, col.head)
	}
	l.cur().rule(x0, l.y-4, min(l.w-40, x0+l.rep.cols[len(l.rep.cols)-1].x-40+70), l.y-4)
}

// cells formats one row and returns its printed cells and its scaled numeric value.
func (l *labLayout) cells(row labRow, r *rand.Rand, prev int) (map[string]string, int) {
	dec := max(decimals(row.lo), decimals(row.hi))
	lo, hi := scaled(row.lo, dec), scaled(row.hi, dec)
	cmp := ""
	if row.rng != "" && row.rng != "text" {
		cmp = row.rng
		if row.rng == "<=" {
			cmp = "≤"
		}
		if l.rep.cmpSpace {
			cmp += " "
		}
	}
	c := map[string]string{"label": row.label, "unit": row.unit, "flag": row.flag}
	switch row.rng {
	case "":
		if row.lo != "" {
			c["range"] = l.num(row.lo) + l.rep.rangeSep + l.num(row.hi)
		}
	case "<", "<=":
		c["range"] = cmp + l.num(row.hi)
	case ">":
		c["range"] = cmp + l.num(row.lo)
	case "text":
		c["range"] = row.refText
	}
	pick := func(a, b int) int { return a + r.IntN(b-a+1) }
	v := 0
	switch {
	case row.fixed != "":
		c["value"] = l.num(row.fixed)
		return c, 0
	case row.hba1cPct: // % (NGSP) = mmol/mol / 10.929 + 2.15, one decimal
		v = (prev*100000/10929 + 215 + 5) / 10
		dec = 1
	case row.rng == "<" || row.rng == "<=":
		v = pick(max(1, hi/5), hi-1)
		if row.out > 0 {
			v = hi + pick(1, max(1, hi/5))
		}
	case row.rng == ">":
		v = pick(lo+1, lo*2)
		if row.out < 0 {
			v = lo - pick(1, max(1, lo/5))
		}
	default:
		v = pick(lo, hi)
		if row.out > 0 {
			v = hi + pick(1, max(1, (hi-lo)/5))
		} else if row.out < 0 {
			v = lo - pick(1, max(1, min((hi-lo)/5, lo-1)))
		}
	}
	c["value"] = l.num(formatScaled(v, dec))
	return c, v
}

// num localizes a '.'-notation number.
func (l *labLayout) num(s string) string {
	if l.rep.german {
		return strings.ReplaceAll(s, ".", ",")
	}
	return s
}

// row draws one result at l.y (table, inline or stacked) with x offset dx, and records its truth.
func (l *labLayout) row(row labRow, s labSection, cells map[string]string, dx int) {
	c := l.cur()
	size := l.rep.size
	var parts []string
	x0, x1 := 40+dx, 0
	top, bottom := l.y, l.y
	draw := func(x, y int, bold bool, s string) {
		c.text(x, y, size, bold, s)
		x1 = max(x1, x*1000+c.width(s, size, bold))
	}
	switch l.rep.layout {
	case "table", "twocol":
		l.y -= l.rowH
		top, bottom = l.y, l.y
		for _, col := range l.rep.cols {
			v := cells[col.field]
			x := col.x + dx
			if col.field == "value" && row.unreadable {
				c.smudge(x-2, l.y-4, x+42, l.y+12)
				x1 = max(x1, (x+42)*1000)
				continue
			}
			if v != "" {
				draw(x, l.y, false, v)
				parts = append(parts, v)
			}
		}
	case "inline":
		l.y -= l.rowH
		top, bottom = l.y, l.y
		line := row.label + ": " + strings.TrimSpace(cells["value"]+" "+cells["unit"])
		if cells["range"] != "" {
			line += "   (Ref. " + cells["range"] + ")"
		}
		if cells["flag"] != "" {
			line += "   " + cells["flag"]
		}
		draw(40+dx, l.y, false, line)
		parts = strings.Fields(line)
	case "stacked":
		l.y -= l.rowH + 4
		top = l.y
		draw(40, l.y, true, row.label)
		parts = append(parts, row.label)
		l.y -= 12
		bottom = l.y
		for _, p := range []struct {
			x          int
			head, cell string
		}{{55, "Result: ", strings.TrimSpace(cells["value"] + " " + cells["unit"])}, {250, "Reference: ", cells["range"]}, {420, "Flag: ", cells["flag"]}} {
			if p.cell != "" {
				draw(p.x, l.y, false, p.head+p.cell)
				parts = append(parts, p.head+p.cell)
			}
		}
		l.y -= 4
	}
	l.pageRows++
	l.record(row, s, cells, strings.Join(strings.Fields(strings.Join(parts, " ")), " "), x0*1000, x1, top, bottom)
}

func (l *labLayout) record(row labRow, s labSection, cells map[string]string, evidence string, x0, x1, top, bottom int) {
	asc, desc := l.cur().lineBox(l.rep.size)
	w, h := l.w*1000, l.h*1000
	frac := func(m, total int) float64 { return float64((m*10000+total/2)/total) / 10000 }
	strp := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	spec := l.rep.specimen
	if s.specimen != "" {
		spec = s.specimen
	}
	r := documents.Row{
		Page: len(l.pages), RowIndex: len(l.rows), AnalyteLabel: row.label,
		ValueText: strp(cells["value"]), UnitText: strp(row.unit), ReferenceRangeText: strp(cells["range"]),
		PrintedFlag: strp(row.flag), SpecimenType: strp(spec), Laboratory: strp(l.rep.lab), EvidenceText: evidence,
		BBox:       &documents.BBox{X0: frac(x0, w), Y0: frac(h-top*1000-asc, h), X1: frac(x1, w), Y1: frac(h-bottom*1000+desc, h)},
		Confidence: 1, Warnings: []string{},
	}
	r.Comparator, r.ValueNumeric = parseLabValue(cells["value"])
	switch row.rng {
	case "":
		if row.lo != "" {
			r.RefLow, r.RefHigh = parseNum(row.lo), parseNum(row.hi)
		}
	case "<", "<=":
		r.RefHigh = parseNum(row.hi)
	case ">":
		r.RefLow = parseNum(row.lo)
	}
	if row.unreadable {
		r.ValueText, r.ValueNumeric, r.Comparator, r.Warnings = nil, nil, nil, []string{"unreadable_value"}
	}
	l.rows = append(l.rows, r)
	if row.code == "" {
		l.codes = append(l.codes, nil)
	} else {
		code := row.code
		l.codes = append(l.codes, &code)
	}
}

// twoColumns draws the first two sections side by side; reading order is left panel first.
func (l *labLayout) twoColumns(values *rand.Rand) {
	top := l.y
	for i, s := range l.rep.sections[:2] {
		dx := i * 270
		l.y = top - 18
		l.cur().text(40+dx, l.y, 10, true, s.heading)
		l.tableHead(40 + dx)
		for _, row := range s.rows {
			cells, _ := l.cells(row, values, 0)
			l.row(row, s, cells, dx)
		}
	}
}

func decimals(s string) int {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return len(s) - i - 1
	}
	return 0
}

// scaled parses a '.'-notation number as an integer count of 10^-dec.
func scaled(s string, dec int) int {
	if s == "" {
		return 0
	}
	d := decimals(s)
	n, err := strconv.Atoi(strings.Replace(s, ".", "", 1))
	if err != nil {
		panic("labpdf: bad number " + s)
	}
	for ; d < dec; d++ {
		n *= 10
	}
	return n
}

func formatScaled(v, dec int) string {
	s := strconv.Itoa(v)
	if dec == 0 {
		return s
	}
	for len(s) <= dec {
		s = "0" + s
	}
	return s[:len(s)-dec] + "." + s[len(s)-dec:]
}

func parseNum(s string) *float64 {
	f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	if err != nil {
		return nil
	}
	return &f
}

// parseLabValue splits a printed result into comparator and number, as the prompt asks.
func parseLabValue(s string) (*string, *float64) {
	var cmp *string
	for _, p := range [][2]string{{"<=", "<="}, {">=", ">="}, {"≤", "<="}, {"≥", ">="}, {"<", "<"}, {">", ">"}} {
		if rest, ok := strings.CutPrefix(s, p[0]); ok {
			c := p[1]
			cmp, s = &c, strings.TrimSpace(rest)
			break
		}
	}
	return cmp, parseNum(s)
}
