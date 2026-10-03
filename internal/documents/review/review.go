package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/documents"
	"github.com/KaanEmec/vitamux/internal/documents/analytes"
)

// Review statuses (lab_extracted_rows.review_status).
const (
	Pending  = "pending"
	Accepted = "accepted"
	Edited   = "edited"
	Rejected = "rejected"
)

// Refusals the API maps to 409 conflict; *InvalidError maps to 422.
var (
	ErrNotReviewable  = errors.New("review: the extraction has no rows to review (not succeeded)")
	ErrOtherConfirmed = errors.New("review: another extraction of this document is confirmed; unconfirm it first")
	ErrNotConfirmed   = errors.New("review: the extraction is not confirmed")
)

// InvalidError lists rejected inputs (RFC 6901 pointers). Details never echo values.
type InvalidError struct{ Problems []documents.FieldProblem }

func (e *InvalidError) Error() string {
	return fmt.Sprintf("review: %d invalid fields", len(e.Problems))
}

// Service reviews and confirms extraction runs.
type Service struct {
	db   *db.DB
	docs *documents.Store
	now  func() time.Time
}

// New returns a Service; docs reads the PDF text layer for the evidence check.
func New(d *db.DB, docs *documents.Store) *Service {
	return &Service{db: d, docs: docs, now: time.Now}
}

// Row is an extracted row under review.
type Row struct {
	documents.Row // current values; Warnings are the extractor's
	Status        string
	ReviewedAt    *time.Time
	// Analyte is what confirming would record ("" for unknown): the alias suggestion while
	// the row is pending, afterwards the analyte the owner reviewed.
	Analyte    string
	Suggested  string   // the current alias suggestion, "" for none
	Validation []string // deterministic warnings (Warn* codes)
	Edits      []Edit
	ResultID   *uuid.UUID // the confirmed lab result
}

// Edit is one entry of the row's review trail.
type Edit struct {
	Action  string
	Changes json.RawMessage // {"field": {"from": old, "to": new}}
	Actor   string
	At      time.Time
}

// Extraction is a run with its rows.
type Extraction struct {
	RunID, DocumentID uuid.UUID
	Status            string
	Rows              []Row
}

func reviewable(status string) bool { return status == "succeeded" || status == "confirmed" }

// Get returns one of the user's runs with every row, its suggestion and its warnings.
func (s *Service) Get(ctx context.Context, user, runID uuid.UUID) (Extraction, error) {
	q := s.db.Q()
	run, err := q.GetOwnerExtractionRun(ctx, dbq.GetOwnerExtractionRunParams{ID: runID, UserID: user})
	if err != nil {
		return Extraction{}, db.MapErr(err)
	}
	x := Extraction{RunID: run.ID, DocumentID: run.DocumentID, Status: run.Status}
	rows, err := q.ListReviewRows(ctx, run.ID)
	if err != nil || len(rows) == 0 {
		return x, err
	}
	edits, err := q.ListRunRowEdits(ctx, run.ID)
	if err != nil {
		return Extraction{}, err
	}
	byRow := map[int64][]dbq.ExtractionRowEdit{}
	for _, e := range edits {
		byRow[e.RowID] = append(byRow[e.RowID], e)
	}

	x.Rows = make([]Row, len(rows))
	in := make([]Input, len(rows))
	var codes []string
	for i, r := range rows {
		suggested, _, err := analytes.Suggest(ctx, q, user, r.AnalyteLabel)
		if err != nil {
			return Extraction{}, err
		}
		row := Row{Row: documentRow(r), Status: r.ReviewStatus, ReviewedAt: r.ReviewedAt, Suggested: suggested, ResultID: r.ResultID, Analyte: suggested}
		if r.ReviewStatus != Pending {
			row.Analyte = deref(r.AnalyteCode)
		}
		edited := map[string]bool{}
		for _, e := range byRow[r.ID] {
			row.Edits = append(row.Edits, Edit{Action: e.Action, Changes: e.Changes, Actor: e.Actor, At: e.CreatedAt})
			var ch map[string]json.RawMessage
			_ = json.Unmarshal(e.Changes, &ch)
			for f := range ch {
				edited[f] = true
			}
		}
		x.Rows[i] = row
		in[i] = Input{Row: row.Row, Analyte: row.Analyte, Rejected: row.Status == Rejected, Edited: edited}
		if row.Analyte != "" {
			codes = append(codes, row.Analyte)
		}
	}

	c := Context{Now: s.now(), Confirmed: map[string]bool{}}
	if err := json.Unmarshal(run.DocMeta, &c.Doc); err != nil {
		return Extraction{}, fmt.Errorf("review: document fields of run %s: %w", run.ID, err)
	}
	dups, err := q.ListConfirmedAnalyteDates(ctx, dbq.ListConfirmedAnalyteDatesParams{UserID: user, Codes: codes, RunID: run.ID})
	if err != nil {
		return Extraction{}, err
	}
	for _, d := range dups {
		c.Confirmed[DupKey(d.Code, d.CollectedDate.Format(time.DateOnly))] = true
	}
	if c.Pages, err = s.textLayer(ctx, user, run.DocumentID); err != nil {
		return Extraction{}, err
	}
	for i, w := range Check(c, in) {
		x.Rows[i].Validation = w
	}
	return x, nil
}

// textLayer reads the PDF's text; none once the original was deleted.
func (s *Service) textLayer(ctx context.Context, user, docID uuid.UUID) ([]string, error) {
	pdf, err := s.docs.File(ctx, user, docID)
	if errors.Is(err, db.ErrNotFound) || errors.Is(err, documents.ErrShredded) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer clear(pdf)
	return documents.TextLayer(pdf), nil
}

func documentRow(r dbq.ListReviewRowsRow) documents.Row {
	out := documents.Row{RowIndex: int(r.RowIndex), AnalyteLabel: r.AnalyteLabel, ValueText: r.ValueText, ValueNumeric: r.ValueNumeric,
		Comparator: r.Comparator, UnitText: r.UnitText, ReferenceRangeText: r.ReferenceRangeText, RefLow: r.RefLow, RefHigh: r.RefHigh,
		PrintedFlag: r.AbnormalFlagPrinted, SpecimenType: r.SpecimenType, CollectedAt: r.CollectedAt, ReportedAt: r.ReportedAt,
		Laboratory: r.Laboratory, EvidenceText: deref(r.EvidenceText), Warnings: r.Warnings}
	if r.Page != nil {
		out.Page = int(*r.Page)
	}
	if r.Confidence != nil {
		out.Confidence = float64(*r.Confidence)
	}
	if r.Bbox != nil {
		out.BBox = &documents.BBox{}
		_ = json.Unmarshal(r.Bbox, out.BBox)
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	return out
}

// Editable fields, named as in the extraction schema, with their kind and size limit.
type kind int

const (
	kText kind = iota
	kNumber
	kComparator
	kDate
	kAnalyte
)

var editable = map[string]struct {
	kind kind
	max  int
}{
	"analyte_label": {kText, 200}, "value_text": {kText, 200}, "value_numeric": {kNumber, 0}, "comparator": {kComparator, 0},
	"unit_text": {kText, 64}, "reference_range_text": {kText, 200}, "ref_low": {kNumber, 0}, "ref_high": {kNumber, 0},
	"printed_flag": {kText, 32}, "specimen_type": {kText, 64}, "collected_at": {kDate, 0}, "reported_at": {kDate, 0},
	"laboratory": {kText, 200}, "analyte": {kAnalyte, 0},
}

// EditRow applies a review patch to row index of a run: a JSON object with any editable field
// (null clears it; analyte null means unknown) and optionally "review": "accept" or "reject".
// Changes are appended to extraction_row_edits with old and new values; the audit event names
// the fields only. A row of a confirmed run may still be edited: Confirm again to revise the
// results.
func (s *Service) EditRow(ctx context.Context, user uuid.UUID, actor string, runID uuid.UUID, index int, patch json.RawMessage) (Row, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(patch, &body); err != nil || body == nil {
		return Row{}, &InvalidError{[]documents.FieldProblem{{Pointer: "", Detail: "must be a JSON object"}}}
	}
	review := ""
	if raw, ok := body["review"]; ok {
		if json.Unmarshal(raw, &review) != nil || (review != "accept" && review != "reject") {
			return Row{}, &InvalidError{[]documents.FieldProblem{{Pointer: "/review", Detail: "must be accept or reject"}}}
		}
		delete(body, "review")
	}
	set, err := decodePatch(body)
	if err != nil {
		return Row{}, err
	}

	err = s.db.Tx(ctx, func(q *dbq.Queries) error {
		run, err := q.GetOwnerExtractionRun(ctx, dbq.GetOwnerExtractionRunParams{ID: runID, UserID: user})
		if err != nil {
			return err
		}
		if _, err := q.LockDocument(ctx, dbq.LockDocumentParams{UserID: user, ID: run.DocumentID}); err != nil {
			return err
		}
		if run, err = q.GetOwnerExtractionRun(ctx, dbq.GetOwnerExtractionRunParams{ID: runID, UserID: user}); err != nil {
			return err
		}
		if !reviewable(run.Status) {
			return ErrNotReviewable
		}
		r, err := q.LockExtractedRow(ctx, dbq.LockExtractedRowParams{RunID: run.ID, RowIndex: int32(index)}) //nolint:gosec // bounded by the API
		if err != nil {
			return err
		}
		cur := map[string]any{"analyte_label": &r.AnalyteLabel, "value_text": r.ValueText, "value_numeric": r.ValueNumeric,
			"comparator": r.Comparator, "unit_text": r.UnitText, "reference_range_text": r.ReferenceRangeText, "ref_low": r.RefLow,
			"ref_high": r.RefHigh, "printed_flag": r.AbnormalFlagPrinted, "specimen_type": r.SpecimenType, "collected_at": r.CollectedAt,
			"reported_at": r.ReportedAt, "laboratory": r.Laboratory, "analyte": r.AnalyteCode}
		if r.ReviewStatus == Pending { // the suggestion shown is what the owner reviews
			code, _, err := analytes.Suggest(ctx, q, user, r.AnalyteLabel)
			if err != nil {
				return err
			}
			cur["analyte"] = optional(code)
		}
		if _, chosen := set["analyte"]; !chosen && set["analyte_label"] != nil { // a corrected label gets its own suggestion
			code, _, err := analytes.Suggest(ctx, q, user, *set["analyte_label"].(*string))
			if err != nil {
				return err
			}
			set["analyte"] = optional(code)
		}
		changes := map[string]any{}
		for f, v := range set {
			if !sameJSON(cur[f], v) {
				changes[f] = map[string]any{"from": cur[f], "to": v}
				cur[f] = v
			}
		}
		var status string
		switch {
		case review == "reject":
			status = Rejected
		case len(changes) > 0:
			status = Edited
		case review == "accept":
			status = Accepted
		default:
			return nil // nothing to record
		}
		if err := q.ReviewExtractedRow(ctx, dbq.ReviewExtractedRowParams{ID: r.ID, ReviewStatus: status,
			AnalyteLabel: *cur["analyte_label"].(*string), ValueText: str(cur["value_text"]), ValueNumeric: num(cur["value_numeric"]),
			Comparator: str(cur["comparator"]), UnitText: str(cur["unit_text"]), ReferenceRangeText: str(cur["reference_range_text"]),
			RefLow: num(cur["ref_low"]), RefHigh: num(cur["ref_high"]), AbnormalFlagPrinted: str(cur["printed_flag"]),
			SpecimenType: str(cur["specimen_type"]), CollectedAt: str(cur["collected_at"]), ReportedAt: str(cur["reported_at"]),
			Laboratory: str(cur["laboratory"]), Analyte: str(cur["analyte"])}); err != nil {
			return err
		}
		var actions []string
		if len(changes) > 0 {
			b, err := json.Marshal(changes)
			if err != nil {
				return err
			}
			if err := q.InsertRowEdit(ctx, dbq.InsertRowEditParams{RowID: r.ID, Action: "edit", Changes: b, Actor: actor}); err != nil {
				return err
			}
			actions = append(actions, "edit")
		}
		if review != "" && (review == "reject" || len(changes) == 0) {
			if err := q.InsertRowEdit(ctx, dbq.InsertRowEditParams{RowID: r.ID, Action: review, Changes: []byte("{}"), Actor: actor}); err != nil {
				return err
			}
			actions = append(actions, review)
		}
		fields := make([]string, 0, len(changes))
		for f := range changes {
			fields = append(fields, f)
		}
		slices.Sort(fields)
		return audit.Record(ctx, q, audit.Event{UserID: &user, Actor: actor, Action: "extraction_row.review",
			TargetType: "extraction", TargetID: run.ID.String(),
			Detail: map[string]any{"row_index": index, "actions": actions, "fields": fields, "review_status": status}})
	})
	if err != nil {
		return Row{}, db.MapErr(err)
	}
	x, err := s.Get(ctx, user, runID)
	if err != nil {
		return Row{}, err
	}
	return x.Rows[index], nil
}

// decodePatch type-checks every field of a patch. Values become *string or *float64, nil for null.
func decodePatch(body map[string]json.RawMessage) (map[string]any, error) {
	var probs []documents.FieldProblem
	bad := func(f, detail string) {
		probs = append(probs, documents.FieldProblem{Pointer: "/" + f, Detail: detail})
	}
	set := map[string]any{}
	for f, raw := range body {
		spec, ok := editable[f]
		if !ok {
			bad(f, "is not an editable field")
			continue
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if f == "analyte_label" {
				bad(f, "must not be null")
				continue
			}
			set[f] = nil
			continue
		}
		if spec.kind == kNumber {
			var v float64
			if json.Unmarshal(raw, &v) != nil || math.IsInf(v, 0) || math.IsNaN(v) {
				bad(f, "must be a number or null")
				continue
			}
			set[f] = &v
			continue
		}
		var v string
		if json.Unmarshal(raw, &v) != nil {
			bad(f, "must be a string or null")
			continue
		}
		switch spec.kind {
		case kNumber: // decoded above
		case kText:
			if n := utf8.RuneCountInString(v); n < 1 || n > spec.max {
				bad(f, "must be 1-"+strconv.Itoa(spec.max)+" characters (null when not printed)")
				continue
			}
		case kComparator:
			if v != "<" && v != ">" && v != "<=" && v != ">=" {
				bad(f, "must be <, >, <=, >= or null")
				continue
			}
		case kDate:
			if _, ok := parseLocal(&v); !ok {
				bad(f, "must be an ISO 8601 local date or date-time without offset")
				continue
			}
		case kAnalyte:
			if _, ok := analytes.Lookup(v); !ok {
				bad(f, "must be an analyte code from docs/analytes.md, or null for unknown")
				continue
			}
		}
		set[f] = &v
	}
	if len(probs) > 0 {
		slices.SortFunc(probs, func(a, b documents.FieldProblem) int { return compareStrings(a.Pointer, b.Pointer) })
		return nil, &InvalidError{probs}
	}
	return set, nil
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func str(v any) *string {
	s, _ := v.(*string)
	return s
}

func num(v any) *float64 {
	f, _ := v.(*float64)
	return f
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
