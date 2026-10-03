package review

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Result is a confirmed lab result at one revision with its provenance.
type Result struct {
	ID uuid.UUID
	Snapshot
	CreatedAt time.Time
	// Provenance: the report and the run and row it was confirmed from. RunID and RowIndex
	// are nil once the original (and with it the run) was deleted.
	ReportID      uuid.UUID
	DocumentID    uuid.UUID
	RunID         *uuid.UUID
	RowIndex      *int32
	Laboratory    *string
	ReportedAt    *time.Time
	Provider      string
	Model         *string
	SchemaVersion string
	PromptVersion string
	ConfirmedBy   string
	ConfirmedAt   time.Time
}

// ResultFilter selects results by collection date (inclusive, nil for open) after the keyset
// position (AfterDate, AfterID), ordered by collection date and id.
type ResultFilter struct {
	Start, End, AfterDate *time.Time
	AfterID               uuid.UUID
	Limit                 int32
}

// ListResults returns the user's confirmed results.
func (s *Service) ListResults(ctx context.Context, user uuid.UUID, f ResultFilter) ([]Result, error) {
	rows, err := s.db.Q().ListLabResults(ctx, dbq.ListLabResultsParams{UserID: user, StartDate: f.Start, EndDate: f.End,
		AfterDate: f.AfterDate, AfterID: f.AfterID, Lim: f.Limit})
	if err != nil {
		return nil, err
	}
	out := make([]Result, len(rows))
	for i, r := range rows {
		out[i] = result(r)
	}
	return out, nil
}

// History returns a result's revisions, newest (the current one) first.
func (s *Service) History(ctx context.Context, user, id uuid.UUID) ([]Result, error) {
	q := s.db.Q()
	row, err := q.GetLabResult(ctx, dbq.GetLabResultParams{UserID: user, ID: id})
	if err != nil {
		return nil, db.MapErr(err)
	}
	cur := result(dbq.ListLabResultsRow(row))
	revs, err := q.ListLabResultRevisions(ctx, id)
	if err != nil {
		return nil, err
	}
	out := []Result{cur}
	for _, rv := range revs {
		old := cur
		old.Snapshot = Snapshot{}
		if err := json.Unmarshal(rv.Snapshot, &old.Snapshot); err != nil {
			return nil, err
		}
		out = append(out, old)
	}
	return out, nil
}

func result(r dbq.ListLabResultsRow) Result {
	return Result{ID: r.ID, CreatedAt: r.CreatedAt, ReportID: r.ReportID, DocumentID: r.DocumentID, RunID: r.RunID, RowIndex: r.RowIndex,
		Laboratory: r.Laboratory, ReportedAt: r.ReportedAt, Provider: r.Provider, Model: r.Model, SchemaVersion: r.SchemaVersion,
		PromptVersion: r.PromptVersion, ConfirmedBy: r.ConfirmedBy, ConfirmedAt: r.ConfirmedAt,
		Snapshot: snapshotOf(dbq.ListReportResultsRow{OriginalLabel: r.OriginalLabel, ValueText: r.ValueText, ValueNumeric: r.ValueNumeric,
			Comparator: r.Comparator, UnitText: r.UnitText, ReferenceRangeText: r.ReferenceRangeText, RefLow: r.RefLow, RefHigh: r.RefHigh,
			AbnormalFlagPrinted: r.AbnormalFlagPrinted, SpecimenType: r.SpecimenType, CanonicalValue: r.CanonicalValue,
			CanonicalUnit: r.CanonicalUnit, ConversionFactor: r.ConversionFactor, ConversionOffset: r.ConversionOffset,
			CatalogVersion: r.CatalogVersion, CollectedAt: r.CollectedAt, CollectedDate: r.CollectedDate, Page: r.Page,
			EvidenceText: r.EvidenceText, Revision: r.Revision, UpdatedAt: r.UpdatedAt, AnalyteCode: r.AnalyteCode})}
}
