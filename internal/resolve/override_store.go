package resolve

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// ErrInvalidOverride wraps the reason an override was rejected before it reached the database.
var ErrInvalidOverride = errors.New("resolve: invalid override")

// Overrides keeps the owner's manual overrides (J09.7). Every create and revoke is audited with
// the actor and marks the window's local date in resolution_dirty, in the same transaction, so
// the J09.9 cache recomputes it. Rows are never deleted: a revoke keeps the override as history.
type Overrides struct{ db *db.DB }

// NewOverrides returns an Overrides store on d.
func NewOverrides(d *db.DB) *Overrides { return &Overrides{db: d} }

// NewOverride is a request to create an override: Scope and Action, plus the field its action
// uses (InputID, Group, or Value with Unit and Note). Value is in the metric's canonical unit.
type NewOverride struct {
	Scope
	Action  OverrideAction
	InputID int64
	Group   string
	Value   float64
	Unit    string
	Note    string
}

// Create validates and stores n. Errors: ErrInvalidOverride; db.ErrConflict when the window
// already has an active override of that action (and, for exclude_input, that input): revoke
// it first.
func (s *Overrides) Create(ctx context.Context, by By, n NewOverride) (Override, error) {
	if err := validateOverride(n); err != nil {
		return Override{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Override{}, err
	}
	p := dbq.InsertOverrideParams{ID: id, UserID: by.UserID, Metric: n.Metric, WindowKind: string(n.Kind), WindowKey: n.Key,
		LocalDate: midnightUTC(n.LocalDate), Action: string(n.Action), CreatedBy: by.Actor}
	switch n.Action {
	case ExcludeInput:
		p.InputID = &n.InputID
	case ForceSource:
		p.SourceGroup = &n.Group
	case SetValue:
		p.Value, p.Unit, p.Note = &n.Value, &n.Unit, &n.Note
	}
	var out Override
	err = s.db.Tx(ctx, func(q *dbq.Queries) error {
		row, err := q.InsertOverride(ctx, p)
		if err != nil {
			return err
		}
		out = overrideOf(row)
		return s.afterChange(ctx, q, by, "override.create", out)
	})
	return out, err
}

// Revoke ends an override and keeps its row. Errors: db.ErrNotFound for an unknown id (or one
// of another user); db.ErrConflict when it was already revoked.
func (s *Overrides) Revoke(ctx context.Context, by By, id uuid.UUID) (Override, error) {
	var out Override
	err := s.db.Tx(ctx, func(q *dbq.Queries) error {
		cur, err := q.GetOverride(ctx, dbq.GetOverrideParams{ID: id, UserID: by.UserID})
		if err != nil {
			return err
		}
		if cur.RevokedAt != nil {
			return fmt.Errorf("%w: override already revoked", db.ErrConflict)
		}
		row, err := q.RevokeOverride(ctx, dbq.RevokeOverrideParams{ID: id, UserID: by.UserID, RevokedBy: &by.Actor})
		if err != nil {
			return err
		}
		out = overrideOf(row)
		return s.afterChange(ctx, q, by, "override.revoke", out)
	})
	return out, err
}

// afterChange marks the window's day dirty for every code the metric resolves and audits the
// change. The audit event holds identifiers only: no value and no note.
func (s *Overrides) afterChange(ctx context.Context, q *dbq.Queries, by By, action string, o Override) error {
	if err := q.MarkOverrideDirty(ctx, dbq.MarkOverrideDirtyParams{UserID: by.UserID, LocalDate: o.LocalDate, Codes: metricCodes(o.Metric)}); err != nil {
		return err
	}
	detail := map[string]any{"metric": o.Metric, "window_kind": string(o.Kind), "window_key": o.Key,
		"local_date": o.LocalDate.Format(dateLayout), "action": string(o.Action)}
	switch o.Action {
	case ExcludeInput:
		detail["input_id"] = o.InputID
	case ForceSource:
		detail["group"] = o.Group
	case SetValue: // the value and note are health data: not audited
	}
	return audit.Record(ctx, q, audit.Event{UserID: &by.UserID, Actor: by.Actor, Action: action,
		TargetType: "manual_override", TargetID: o.ID.String(), Detail: detail})
}

// Active returns the user's unrevoked overrides of metric whose local date is in [from, to],
// oldest first: what ResolveWindowOverridden takes for that range.
func (s *Overrides) Active(ctx context.Context, userID uuid.UUID, metric string, from, to time.Time) ([]Override, error) {
	rows, err := s.db.Q().ListActiveOverrides(ctx, dbq.ListActiveOverridesParams{UserID: userID, Metric: metric,
		FromDate: midnightUTC(from), ToDate: midnightUTC(to)})
	return overridesOf(rows), db.MapErr(err)
}

// History returns up to limit overrides of metric, revoked ones included, newest first.
func (s *Overrides) History(ctx context.Context, userID uuid.UUID, metric string, limit int) ([]Override, error) {
	rows, err := s.db.Q().ListOverrideHistory(ctx, dbq.ListOverrideHistoryParams{UserID: userID, Metric: metric, MaxRows: int32(min(max(limit, 1), 1000))})
	return overridesOf(rows), db.MapErr(err)
}

// metricCodes lists the catalogue codes a rule metric resolves: the code itself, the blood
// pressure components, or every sleep-derived code.
func metricCodes(metric string) []string {
	switch metric {
	case FamilyBloodPressure, FamilySleep:
		var codes []string
		for _, m := range catalog.Metrics() {
			if (metric == FamilyBloodPressure && m.Group == "bp_reading") || (metric == FamilySleep && m.Agg == catalog.SleepDerived) {
				codes = append(codes, m.Code)
			}
		}
		return codes
	}
	return []string{metric}
}

func overridesOf(rows []dbq.ManualOverride) []Override {
	out := make([]Override, len(rows))
	for i, r := range rows {
		out[i] = overrideOf(r)
	}
	return out
}

func overrideOf(r dbq.ManualOverride) Override {
	o := Override{ID: r.ID, Metric: r.Metric, Kind: catalog.Window(r.WindowKind), Key: r.WindowKey, LocalDate: r.LocalDate,
		Action: OverrideAction(r.Action), CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, RevokedAt: r.RevokedAt}
	if r.InputID != nil {
		o.InputID = *r.InputID
	}
	if r.SourceGroup != nil {
		o.Group = *r.SourceGroup
	}
	if r.Value != nil {
		o.Value = *r.Value
	}
	if r.Unit != nil {
		o.Unit = *r.Unit
	}
	if r.Note != nil {
		o.Note = *r.Note
	}
	if r.RevokedBy != nil {
		o.RevokedBy = *r.RevokedBy
	}
	return o
}
