package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/documents/review"
)

// Extraction review, confirmation and lab results (J12.4; lab-documents.md#review-and-confirmation).
// Every mutation is audited by the review service with identifiers only.
func (rt *router) reviewRoutes() {
	read, write := scope(auth.ReadHealth), scope(auth.WriteDocuments)
	rt.handle("GET /api/v1/extractions/{id}", read, rt.ops.GetExtraction)
	rt.handle("PATCH /api/v1/extractions/{id}/rows/{row}", write, rt.ops.UpdateExtractionRow)
	rt.handle("POST /api/v1/extractions/{id}/confirm", write, rt.ops.ConfirmExtraction)
	rt.handle("POST /api/v1/extractions/{id}/unconfirm", write, rt.ops.UnconfirmExtraction)
	rt.handle("GET /api/v1/lab-results", read, rt.ops.ListLabResults)
	rt.handle("GET /api/v1/lab-results/{id}/history", read, rt.ops.GetLabResultHistory)
}

func (o *owner) reviewer() (*review.Service, error) {
	store, err := o.docs()
	if err != nil {
		return nil, err
	}
	return review.New(o.opts.DB, store), nil
}

var (
	extractionIDRe = regexp.MustCompile(`^ext_[0-9a-f]{32}$`)
	labResultIDRe  = regexp.MustCompile(`^lab_[0-9a-f]{32}$`)
)

// parsePrefixedID accepts <prefix>_<32 hex>; anything else is not found.
func parsePrefixedID(re *regexp.Regexp, s string) (uuid.UUID, error) {
	if !re.MatchString(s) {
		return uuid.Nil, db.ErrNotFound
	}
	return uuid.Parse(s[strings.IndexByte(s, '_')+1:])
}

func prefixedID(prefix string, id uuid.UUID) string { return prefix + "_" + hex.EncodeToString(id[:]) }

// reviewError maps the review service's refusals.
func reviewError(err error) error {
	var inv *review.InvalidError
	switch {
	case errors.As(err, &inv):
		fe := make([]FieldError, len(inv.Problems))
		for i, p := range inv.Problems {
			fe[i] = FieldError{Pointer: p.Pointer, Detail: p.Detail}
		}
		return problemErr(CodeValidationFailed, "the review is incomplete or the edit is invalid", fe...)
	case errors.Is(err, review.ErrNotReviewable):
		return problemErr(CodeConflict, "the extraction has not succeeded, so it has no rows to review")
	case errors.Is(err, review.ErrOtherConfirmed):
		return problemErr(CodeConflict, "another extraction of this document is confirmed; unconfirm it first")
	case errors.Is(err, review.ErrNotConfirmed):
		return problemErr(CodeConflict, "this extraction is not the document's confirmed one")
	}
	return err
}

// extraction answers a run with its rows: run fields from the extraction service, rows from review.
func (o *owner) extraction(ctx context.Context, rv *review.Service, runID uuid.UUID) (oapi.Extraction, error) {
	svc, err := o.extractor()
	if err != nil {
		return oapi.Extraction{}, err
	}
	user := auth.PrincipalFrom(ctx).UserID
	x, err := rv.Get(ctx, user, runID)
	if err != nil {
		return oapi.Extraction{}, err
	}
	runs, err := svc.List(ctx, user, x.DocumentID)
	if err != nil {
		return oapi.Extraction{}, err
	}
	for _, r := range runs {
		if r.ID == runID {
			out := apiExtraction(r)
			rows := make([]oapi.ExtractionRow, len(x.Rows))
			for i, row := range x.Rows {
				rows[i] = apiExtractionRow(row)
			}
			out.Rows = &rows
			return out, nil
		}
	}
	return oapi.Extraction{}, db.ErrNotFound
}

func apiExtractionRow(r review.Row) oapi.ExtractionRow {
	out := oapi.ExtractionRow{Index: r.RowIndex, AnalyteLabel: r.AnalyteLabel, ValueText: r.ValueText, ValueNumeric: r.ValueNumeric,
		Comparator: r.Comparator, UnitText: r.UnitText, ReferenceRangeText: r.ReferenceRangeText, RefLow: r.RefLow, RefHigh: r.RefHigh,
		PrintedFlag: r.PrintedFlag, SpecimenType: r.SpecimenType, CollectedAt: r.CollectedAt, ReportedAt: r.ReportedAt,
		Laboratory: r.Laboratory, EvidenceText: r.EvidenceText, Confidence: r.Confidence, ReviewStatus: oapi.ExtractionRowReviewStatus(r.Status),
		ReviewedAt: r.ReviewedAt, Analyte: nonEmpty(r.Analyte), SuggestedAnalyte: nonEmpty(r.Suggested), Warnings: r.Warnings,
		Validation: r.Validation, Edits: make([]oapi.ExtractionRowEdit, len(r.Edits))}
	if r.Page > 0 {
		out.Page = &r.Page
	}
	if r.BBox != nil {
		b, _ := json.Marshal(r.BBox)
		raw := json.RawMessage(b)
		out.Bbox = &raw
	}
	if r.ResultID != nil {
		id := prefixedID("lab", *r.ResultID)
		out.LabResultID = &id
	}
	for i, e := range r.Edits {
		out.Edits[i] = oapi.ExtractionRowEdit{Action: oapi.ExtractionRowEditAction(e.Action), Changes: e.Changes, Actor: e.Actor, CreatedAt: e.At}
	}
	if out.Validation == nil {
		out.Validation = []string{}
	}
	return out
}

func (o *owner) GetExtraction(ctx context.Context, req oapi.GetExtractionRequestObject) (oapi.GetExtractionResponseObject, error) {
	rv, err := o.reviewer()
	if err != nil {
		return nil, err
	}
	id, err := parsePrefixedID(extractionIDRe, req.ID)
	if err != nil {
		return nil, err
	}
	out, err := o.extraction(ctx, rv, id)
	if err != nil {
		return nil, err
	}
	return oapi.GetExtraction200JSONResponse(out), nil
}

// UpdateExtractionRow applies a review patch; the body is parsed by the review service so
// null and absent fields differ (merge patch).
func (o *owner) UpdateExtractionRow(ctx context.Context, req oapi.UpdateExtractionRowRequestObject) (oapi.UpdateExtractionRowResponseObject, error) {
	rv, err := o.reviewer()
	if err != nil {
		return nil, err
	}
	id, err := parsePrefixedID(extractionIDRe, req.ID)
	if err != nil {
		return nil, err
	}
	index, err := strconv.Atoi(req.Row)
	if err != nil || index < 0 || index >= 10_000 {
		return nil, problemErr(CodeNotFound, "no such row")
	}
	if req.Body == nil {
		return nil, problemErr(CodeValidationFailed, "a JSON object is required")
	}
	p := auth.PrincipalFrom(ctx)
	row, err := rv.EditRow(ctx, p.UserID, p.Actor(), id, index, *req.Body)
	if err != nil {
		return nil, reviewError(err)
	}
	return oapi.UpdateExtractionRow200JSONResponse(apiExtractionRow(row)), nil
}

func (o *owner) ConfirmExtraction(ctx context.Context, req oapi.ConfirmExtractionRequestObject) (oapi.ConfirmExtractionResponseObject, error) {
	rv, err := o.reviewer()
	if err != nil {
		return nil, err
	}
	id, err := parsePrefixedID(extractionIDRe, req.ID)
	if err != nil {
		return nil, err
	}
	p := auth.PrincipalFrom(ctx)
	if _, err := rv.Confirm(ctx, p.UserID, p.Actor(), id); err != nil {
		return nil, reviewError(err)
	}
	out, err := o.extraction(ctx, rv, id)
	if err != nil {
		return nil, err
	}
	return oapi.ConfirmExtraction200JSONResponse(out), nil
}

func (o *owner) UnconfirmExtraction(ctx context.Context, req oapi.UnconfirmExtractionRequestObject) (oapi.UnconfirmExtractionResponseObject, error) {
	rv, err := o.reviewer()
	if err != nil {
		return nil, err
	}
	id, err := parsePrefixedID(extractionIDRe, req.ID)
	if err != nil {
		return nil, err
	}
	p := auth.PrincipalFrom(ctx)
	if err := rv.Unconfirm(ctx, p.UserID, p.Actor(), id); err != nil {
		return nil, reviewError(err)
	}
	out, err := o.extraction(ctx, rv, id)
	if err != nil {
		return nil, err
	}
	return oapi.UnconfirmExtraction200JSONResponse(out), nil
}

func apiLabResult(r review.Result) oapi.LabResult {
	out := oapi.LabResult{ID: prefixedID("lab", r.ID), Revision: int(r.Revision), Analyte: r.Analyte, OriginalLabel: r.OriginalLabel,
		ValueText: r.ValueText, ValueNumeric: r.ValueNumeric, Comparator: r.Comparator, UnitText: r.UnitText,
		ReferenceRangeText: r.ReferenceRangeText, RefLow: r.RefLow, RefHigh: r.RefHigh, PrintedFlag: r.PrintedFlag,
		SpecimenType: r.SpecimenType, CanonicalValue: r.CanonicalValue, CanonicalUnit: r.CanonicalUnit, ConversionFactor: r.ConversionFactor,
		ConversionOffset: r.ConversionOffset, CollectedAt: r.CollectedAt, EvidenceText: r.EvidenceText, CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt}
	if d, err := time.Parse(time.DateOnly, r.CollectedDate); err == nil {
		out.CollectedDate = apiDate(d)
	}
	if r.CatalogVersion != nil {
		v := int(*r.CatalogVersion)
		out.CatalogVersion = &v
	}
	if r.Page != nil {
		v := int(*r.Page)
		out.Page = &v
	}
	pv := &out.Provenance
	pv.ReportID, pv.DocumentID = prefixedID("rpt", r.ReportID), formatDocumentID(r.DocumentID)
	pv.Laboratory, pv.ReportedAt, pv.Provider, pv.Model = r.Laboratory, r.ReportedAt, r.Provider, r.Model
	pv.SchemaVersion, pv.PromptVersion, pv.ConfirmedBy, pv.ConfirmedAt = r.SchemaVersion, r.PromptVersion, r.ConfirmedBy, r.ConfirmedAt
	if r.RunID != nil {
		id := formatExtractionID(*r.RunID)
		pv.ExtractionID = &id
	}
	if r.RowIndex != nil {
		v := int(*r.RowIndex)
		pv.RowIndex = &v
	}
	return out
}

func (o *owner) ListLabResults(ctx context.Context, req oapi.ListLabResultsRequestObject) (oapi.ListLabResultsResponseObject, error) {
	rv, err := o.reviewer()
	if err != nil {
		return nil, err
	}
	bound := req.Params
	bound.Limit, bound.Cursor = nil, nil
	p, err := o.newPage("lab_results", bound, req.Params.Limit, req.Params.Cursor)
	if err != nil {
		return nil, err
	}
	f := review.ResultFilter{Limit: p.lim()}
	if f.AfterDate, f.AfterID, err = p.afterKeyUUID(); err != nil {
		return nil, err
	}
	if d := req.Params.StartDate; d != nil {
		f.Start = &d.Time
	}
	if d := req.Params.EndDate; d != nil {
		f.End = &d.Time
	}
	rows, err := rv.ListResults(ctx, auth.PrincipalFrom(ctx).UserID, f)
	if err != nil {
		return nil, err
	}
	rows, more, next := trim(o, p, rows, func(r review.Result) (time.Time, string) {
		d, _ := time.Parse(time.DateOnly, r.CollectedDate)
		return d, r.ID.String()
	})
	out := oapi.ListLabResults200JSONResponse{HasMore: more, NextCursor: next, LabResults: make([]oapi.LabResult, len(rows))}
	for i, r := range rows {
		out.LabResults[i] = apiLabResult(r)
	}
	return out, nil
}

func (o *owner) GetLabResultHistory(ctx context.Context, req oapi.GetLabResultHistoryRequestObject) (oapi.GetLabResultHistoryResponseObject, error) {
	rv, err := o.reviewer()
	if err != nil {
		return nil, err
	}
	id, err := parsePrefixedID(labResultIDRe, req.ID)
	if err != nil {
		return nil, err
	}
	hist, err := rv.History(ctx, auth.PrincipalFrom(ctx).UserID, id)
	if err != nil {
		return nil, err
	}
	out := oapi.GetLabResultHistory200JSONResponse{Revisions: make([]oapi.LabResult, len(hist))}
	for i, r := range hist {
		out.Revisions[i] = apiLabResult(r)
	}
	return out, nil
}
