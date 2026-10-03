package review

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/documents"
	"github.com/KaanEmec/vitamux/internal/documents/analytes"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// Confirmation counts what Confirm did.
type Confirmation struct {
	ReportID                             uuid.UUID
	Created, Revised, Removed, Unchanged int
}

// Confirm turns the reviewed rows of a run into one lab report with its lab results, in one
// transaction. Every row must be accepted, edited or rejected, and every kept row needs a
// label, a value and a collection date (an accepted row without a unit is confirmed as
// unitless). Each result keeps the printed label, value, unit, range and flag; it gets a
// canonical value only when its analyte has a conversion for the printed unit.
//
// Confirming an already confirmed run again applies later edits: a changed result stores
// its previous values in lab_result_revisions, a newly kept row adds a result, a row now
// rejected removes its result.
func (s *Service) Confirm(ctx context.Context, user uuid.UUID, actor string, runID uuid.UUID) (Confirmation, error) {
	tl, err := normalize.NewPeriods(s.db).Timeline(ctx, user)
	if err != nil {
		return Confirmation{}, err
	}
	var res Confirmation
	err = s.db.Tx(ctx, func(q *dbq.Queries) error {
		res = Confirmation{}
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
		report, err := q.GetDocumentReport(ctx, run.DocumentID)
		existing := err == nil
		switch {
		case existing && (report.RunID == nil || *report.RunID != run.ID):
			return ErrOtherConfirmed
		case !existing && !errors.Is(db.MapErr(err), db.ErrNotFound):
			return err
		}
		rows, err := q.ListReviewRows(ctx, run.ID)
		if err != nil {
			return err
		}
		if err := confirmable(rows); err != nil {
			return err
		}

		var meta documents.DocumentMeta
		if err := json.Unmarshal(run.DocMeta, &meta); err != nil {
			return err
		}
		if !existing {
			report = dbq.LabReport{ID: uuid.Must(uuid.NewV7()), Laboratory: meta.Laboratory}
			if report.Laboratory == nil {
				for _, r := range rows {
					if r.Laboratory != nil && r.ReviewStatus != Rejected {
						report.Laboratory = r.Laboratory
						break
					}
				}
			}
			if meta.ReportedAt != nil {
				report.ReportedAt, _, _ = localInstant(*meta.ReportedAt, tl, true)
			}
			if err := q.InsertLabReport(ctx, dbq.InsertLabReportParams{ID: report.ID, UserID: user, DocumentID: run.DocumentID,
				RunID: &run.ID, Laboratory: report.Laboratory, ReportedAt: report.ReportedAt, Provider: run.Provider, Model: run.Model,
				SchemaVersion: run.SchemaVersion, PromptVersion: run.PromptVersion, ConfirmedBy: actor}); err != nil {
				return err
			}
		}
		res.ReportID = report.ID

		old, err := q.ListReportResults(ctx, report.ID)
		if err != nil {
			return err
		}
		bySource := map[int64]dbq.ListReportResultsRow{}
		for _, r := range old {
			if r.SourceRowID != nil {
				bySource[*r.SourceRowID] = r
			} else if err := s.remove(ctx, q, &res, r.ID); err != nil {
				return err
			}
		}
		ids := map[string]*int16{}
		for _, r := range rows {
			prev, had := bySource[r.ID]
			if r.ReviewStatus == Rejected {
				if had {
					if err := s.remove(ctx, q, &res, prev.ID); err != nil {
						return err
					}
				}
				continue
			}
			v, err := resultValues(ctx, q, ids, r, tl)
			if err != nil {
				return err
			}
			if !had {
				if err := q.InsertLabResult(ctx, insertParams(uuid.Must(uuid.NewV7()), user, report.ID, r.ID, v)); err != nil {
					return err
				}
				res.Created++
				continue
			}
			before := snapshotOf(prev)
			after := v.snapshot
			after.Revision, after.UpdatedAt = before.Revision, before.UpdatedAt
			if sameJSON(before, after) {
				res.Unchanged++
				continue
			}
			b, err := json.Marshal(before)
			if err != nil {
				return err
			}
			reason := "confirmed again after review edits"
			if err := q.InsertLabResultRevision(ctx, dbq.InsertLabResultRevisionParams{ResultID: prev.ID, Revision: prev.Revision,
				Snapshot: b, Reason: &reason, ChangedBy: actor}); err != nil {
				return err
			}
			p := insertParams(prev.ID, user, report.ID, r.ID, v)
			if err := q.ReviseLabResult(ctx, dbq.ReviseLabResultParams{ID: prev.ID, AnalyteID: p.AnalyteID, OriginalLabel: p.OriginalLabel,
				ValueText: p.ValueText, ValueNumeric: p.ValueNumeric, Comparator: p.Comparator, UnitText: p.UnitText,
				ReferenceRangeText: p.ReferenceRangeText, RefLow: p.RefLow, RefHigh: p.RefHigh, AbnormalFlagPrinted: p.AbnormalFlagPrinted,
				SpecimenType: p.SpecimenType, CanonicalValue: p.CanonicalValue, CanonicalUnit: p.CanonicalUnit,
				ConversionFactor: p.ConversionFactor, ConversionOffset: p.ConversionOffset, CatalogVersion: p.CatalogVersion,
				CollectedAt: p.CollectedAt, CollectedDate: p.CollectedDate, Page: p.Page, EvidenceText: p.EvidenceText}); err != nil {
				return err
			}
			res.Revised++
		}
		if err := q.SetExtractionRunStatus(ctx, dbq.SetExtractionRunStatusParams{Status: "confirmed", ID: run.ID}); err != nil {
			return err
		}
		if err := q.SetLiveDocumentStatus(ctx, dbq.SetLiveDocumentStatusParams{Status: documents.StatusConfirmed, ID: run.DocumentID}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &user, Actor: actor, Action: "extraction.confirm",
			TargetType: "extraction", TargetID: run.ID.String(), Detail: map[string]any{
				"document_id": run.DocumentID.String(), "report_id": report.ID.String(), "again": existing,
				"created": res.Created, "revised": res.Revised, "removed": res.Removed, "unchanged": res.Unchanged}})
	})
	return res, db.MapErr(err)
}

func (s *Service) remove(ctx context.Context, q *dbq.Queries, res *Confirmation, id uuid.UUID) error {
	res.Removed++
	return q.DeleteLabResult(ctx, id)
}

// confirmable checks that every row was reviewed and every kept row has the required fields.
func confirmable(rows []dbq.ListReviewRowsRow) error {
	var probs []documents.FieldProblem
	kept := 0
	for _, r := range rows {
		p := "/rows/" + strconv.Itoa(int(r.RowIndex))
		switch r.ReviewStatus {
		case Pending:
			probs = append(probs, documents.FieldProblem{Pointer: p, Detail: "not reviewed: accept, edit or reject it"})
			continue
		case Rejected:
			continue
		}
		kept++
		if r.ValueText == nil {
			probs = append(probs, documents.FieldProblem{Pointer: p + "/value_text", Detail: "is required"})
		}
		if _, ok := parseLocal(r.CollectedAt); !ok {
			probs = append(probs, documents.FieldProblem{Pointer: p + "/collected_at", Detail: "is required: edit the row to add the collection date"})
		}
	}
	if len(probs) == 0 && kept == 0 {
		probs = append(probs, documents.FieldProblem{Pointer: "/rows", Detail: "every row is rejected: nothing to confirm"})
	}
	if len(probs) > 0 {
		return &InvalidError{probs}
	}
	return nil
}

// Snapshot is a result's values at one revision (lab_result_revisions.snapshot and the
// comparison that decides whether confirming again revises a result). Times are UTC.
type Snapshot struct {
	Analyte            *string    `json:"analyte"`
	OriginalLabel      string     `json:"original_label"`
	ValueText          string     `json:"value_text"`
	ValueNumeric       *float64   `json:"value_numeric"`
	Comparator         *string    `json:"comparator"`
	UnitText           *string    `json:"unit_text"`
	ReferenceRangeText *string    `json:"reference_range_text"`
	RefLow             *float64   `json:"ref_low"`
	RefHigh            *float64   `json:"ref_high"`
	PrintedFlag        *string    `json:"printed_flag"`
	SpecimenType       *string    `json:"specimen_type"`
	CanonicalValue     *float64   `json:"canonical_value"`
	CanonicalUnit      *string    `json:"canonical_unit"`
	ConversionFactor   *float64   `json:"conversion_factor"`
	ConversionOffset   *float64   `json:"conversion_offset"`
	CatalogVersion     *int32     `json:"catalog_version"`
	CollectedAt        *time.Time `json:"collected_at"`
	CollectedDate      string     `json:"collected_date"`
	Page               *int32     `json:"page"`
	EvidenceText       *string    `json:"evidence_text"`
	Revision           int32      `json:"revision"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type values struct {
	snapshot  Snapshot
	analyteID *int16
	date      time.Time
}

// resultValues computes a result from a reviewed row. ids caches analyte ids by code.
func resultValues(ctx context.Context, q *dbq.Queries, ids map[string]*int16, r dbq.ListReviewRowsRow, tl normalize.Timeline) (values, error) {
	v := values{snapshot: Snapshot{Analyte: r.AnalyteCode, OriginalLabel: r.AnalyteLabel, ValueText: *r.ValueText, ValueNumeric: r.ValueNumeric,
		Comparator: r.Comparator, UnitText: r.UnitText, ReferenceRangeText: r.ReferenceRangeText, RefLow: r.RefLow, RefHigh: r.RefHigh,
		PrintedFlag: r.AbnormalFlagPrinted, SpecimenType: r.SpecimenType, Page: r.Page, EvidenceText: r.EvidenceText}}
	var instant *time.Time
	instant, v.date, _ = localInstant(*r.CollectedAt, tl, false)
	v.snapshot.CollectedAt, v.snapshot.CollectedDate = instant, v.date.Format(time.DateOnly)
	code := deref(r.AnalyteCode)
	if code == "" {
		return v, nil
	}
	id, ok := ids[code]
	if !ok {
		n, err := q.GetAnalyteID(ctx, code)
		if err != nil {
			return values{}, err
		}
		id, ids[code] = &n, &n
	}
	v.analyteID = id
	if r.ValueNumeric != nil {
		if cv, c, err := analytes.Canonical(code, *r.ValueNumeric, deref(r.UnitText)); err == nil {
			an, _ := analytes.Lookup(code)
			ver := int32(analytes.Version)
			v.snapshot.CanonicalValue, v.snapshot.CanonicalUnit = &cv, &an.Unit
			v.snapshot.ConversionFactor, v.snapshot.ConversionOffset, v.snapshot.CatalogVersion = &c.Factor, &c.Offset, &ver
		}
	}
	return v, nil
}

func insertParams(id, user, report uuid.UUID, rowID int64, v values) dbq.InsertLabResultParams {
	s := v.snapshot
	return dbq.InsertLabResultParams{ID: id, UserID: user, ReportID: report, SourceRowID: &rowID, AnalyteID: v.analyteID,
		OriginalLabel: s.OriginalLabel, ValueText: s.ValueText, ValueNumeric: s.ValueNumeric, Comparator: s.Comparator, UnitText: s.UnitText,
		ReferenceRangeText: s.ReferenceRangeText, RefLow: s.RefLow, RefHigh: s.RefHigh, AbnormalFlagPrinted: s.PrintedFlag,
		SpecimenType: s.SpecimenType, CanonicalValue: s.CanonicalValue, CanonicalUnit: s.CanonicalUnit, ConversionFactor: s.ConversionFactor,
		ConversionOffset: s.ConversionOffset, CatalogVersion: s.CatalogVersion, CollectedAt: s.CollectedAt, CollectedDate: v.date,
		Page: s.Page, EvidenceText: s.EvidenceText}
}

// snapshotOf is the stored result as a Snapshot.
func snapshotOf(r dbq.ListReportResultsRow) Snapshot {
	return Snapshot{Analyte: r.AnalyteCode, OriginalLabel: r.OriginalLabel, ValueText: r.ValueText, ValueNumeric: r.ValueNumeric, Comparator: r.Comparator,
		UnitText: r.UnitText, ReferenceRangeText: r.ReferenceRangeText, RefLow: r.RefLow, RefHigh: r.RefHigh, PrintedFlag: r.AbnormalFlagPrinted,
		SpecimenType: r.SpecimenType, CanonicalValue: r.CanonicalValue, CanonicalUnit: r.CanonicalUnit, ConversionFactor: r.ConversionFactor,
		ConversionOffset: r.ConversionOffset, CatalogVersion: r.CatalogVersion, CollectedAt: utc(r.CollectedAt),
		CollectedDate: r.CollectedDate.Format(time.DateOnly), Page: r.Page, EvidenceText: r.EvidenceText, Revision: r.Revision,
		UpdatedAt: r.UpdatedAt.UTC()}
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// localInstant reads an ISO 8601 local date(-time) in the owner's timezone. The date is the
// printed calendar date. The instant is nil without a timezone period, and for a date without
// a time unless midnight is wanted.
func localInstant(s string, tl normalize.Timeline, midnight bool) (*time.Time, time.Time, bool) {
	wall, ok := parseLocal(&s)
	if !ok {
		return nil, time.Time{}, false
	}
	date := wall.Truncate(24 * time.Hour)
	name, ok := tl.At(wall)
	if !ok || (len(s) == 10 && !midnight) {
		return nil, date, true
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, date, true
	}
	t := time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), 0, loc).UTC()
	return &t, date, true
}

// Unconfirm deletes the lab report and results confirmed from a run, which returns to review.
// The audit event keeps the identifiers and the number of results deleted.
func (s *Service) Unconfirm(ctx context.Context, user uuid.UUID, actor string, runID uuid.UUID) error {
	return db.MapErr(s.db.Tx(ctx, func(q *dbq.Queries) error {
		run, err := q.GetOwnerExtractionRun(ctx, dbq.GetOwnerExtractionRunParams{ID: runID, UserID: user})
		if err != nil {
			return err
		}
		if _, err := q.LockDocument(ctx, dbq.LockDocumentParams{UserID: user, ID: run.DocumentID}); err != nil {
			return err
		}
		report, err := q.GetDocumentReport(ctx, run.DocumentID)
		if errors.Is(db.MapErr(err), db.ErrNotFound) || (err == nil && (report.RunID == nil || *report.RunID != run.ID)) {
			return ErrNotConfirmed
		}
		if err != nil {
			return err
		}
		results, err := q.ListReportResults(ctx, report.ID)
		if err != nil {
			return err
		}
		if err := q.DeleteLabReport(ctx, report.ID); err != nil {
			return err
		}
		if err := q.SetExtractionRunStatus(ctx, dbq.SetExtractionRunStatusParams{Status: "succeeded", ID: run.ID}); err != nil {
			return err
		}
		if err := q.SetLiveDocumentStatus(ctx, dbq.SetLiveDocumentStatusParams{Status: documents.StatusNeedsReview, ID: run.DocumentID}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &user, Actor: actor, Action: "extraction.unconfirm",
			TargetType: "extraction", TargetID: run.ID.String(), Detail: map[string]any{
				"document_id": run.DocumentID.String(), "report_id": report.ID.String(), "results_deleted": len(results)}})
	}))
}
