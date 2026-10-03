package normalize

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Range is a span of instants whose local dates may have changed. A zero From means from the
// beginning of time, a zero To means without end. Rows are in it by their instant: start_at,
// measured_at, or end_at for sleep sessions.
type Range struct{ From, To time.Time }

// Periods manages an owner's timezone periods. An edit returns the Range whose local dates it can
// change (nil when none can); the caller passes it to RecomputeLocalDates.
type Periods struct{ db *db.DB }

// NewPeriods returns the period service over d.
func NewPeriods(d *db.DB) *Periods { return &Periods{d} }

// Timeline loads the owner's periods in order.
func (p *Periods) Timeline(ctx context.Context, userID uuid.UUID) (Timeline, error) {
	return loadTimeline(ctx, p.db.Q(), userID)
}

func loadTimeline(ctx context.Context, q *dbq.Queries, userID uuid.UUID) (Timeline, error) {
	rows, err := q.ListTimezonePeriods(ctx, userID)
	if err != nil {
		return nil, db.MapErr(err)
	}
	tl := make(Timeline, len(rows))
	for i, r := range rows {
		tl[i] = Period{ID: r.ID, ValidFrom: r.ValidFrom, TZ: r.Tz}
	}
	return tl, nil
}

// Add starts a period at validFrom. A second period at the same instant is db.ErrConflict; use Edit.
func (p *Periods) Add(ctx context.Context, userID uuid.UUID, actor string, validFrom time.Time, tz string) (Period, *Range, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Period{}, nil, err
	}
	np := Period{ID: id, ValidFrom: validFrom.UTC(), TZ: tz}
	if _, err := location(tz); err != nil {
		return Period{}, nil, err
	}
	var r *Range
	err = p.db.Tx(ctx, func(q *dbq.Queries) error {
		before, err := loadTimeline(ctx, q, userID)
		if err != nil {
			return err
		}
		r = affected(before, before.with(np))
		if err := q.InsertTimezonePeriod(ctx, dbq.InsertTimezonePeriodParams{ID: id, UserID: userID, Tz: tz, ValidFrom: np.ValidFrom}); err != nil {
			return err
		}
		return audit.Record(ctx, q, periodEvent(userID, actor, "timezone_period.add", np))
	}, db.Serializable())
	return np, r, err
}

// Edit changes a period's zone and/or start. Moving it onto another period's start is db.ErrConflict.
func (p *Periods) Edit(ctx context.Context, userID uuid.UUID, actor string, id uuid.UUID, validFrom time.Time, tz string) (*Range, error) {
	if _, err := location(tz); err != nil {
		return nil, err
	}
	np := Period{ID: id, ValidFrom: validFrom.UTC(), TZ: tz}
	var r *Range
	err := p.db.Tx(ctx, func(q *dbq.Queries) error {
		before, err := loadTimeline(ctx, q, userID)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(before, func(x Period) bool { return x.ID == id }) {
			return db.ErrNotFound
		}
		r = affected(before, before.without(id).with(np))
		if _, err := q.UpdateTimezonePeriod(ctx, dbq.UpdateTimezonePeriodParams{ID: id, UserID: userID, Tz: tz, ValidFrom: np.ValidFrom}); err != nil {
			return err
		}
		return audit.Record(ctx, q, periodEvent(userID, actor, "timezone_period.edit", np))
	}, db.Serializable())
	return r, err
}

// Remove deletes a period; the previous one then runs until the next period starts. Removing the
// last period leaves records without an offset unresolvable (ErrNoTimezone) until a new one is added.
func (p *Periods) Remove(ctx context.Context, userID uuid.UUID, actor string, id uuid.UUID) (*Range, error) {
	var r *Range
	err := p.db.Tx(ctx, func(q *dbq.Queries) error {
		before, err := loadTimeline(ctx, q, userID)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(before, func(x Period) bool { return x.ID == id })
		if i < 0 {
			return db.ErrNotFound
		}
		r = affected(before, before.without(id))
		if _, err := q.DeleteTimezonePeriod(ctx, dbq.DeleteTimezonePeriodParams{ID: id, UserID: userID}); err != nil {
			return err
		}
		return audit.Record(ctx, q, periodEvent(userID, actor, "timezone_period.remove", before[i]))
	}, db.Serializable())
	return r, err
}

func periodEvent(userID uuid.UUID, actor, action string, p Period) audit.Event {
	return audit.Event{
		UserID: &userID, Actor: actor, Action: action,
		TargetType: "timezone_period", TargetID: p.ID.String(),
		Detail: map[string]any{"tz": p.TZ, "valid_from": p.ValidFrom.Format(time.RFC3339)},
	}
}

// with returns a copy of tl including p, kept sorted.
func (tl Timeline) with(p Period) Timeline {
	out := append(slices.Clone(tl), p)
	slices.SortFunc(out, func(a, b Period) int { return a.ValidFrom.Compare(b.ValidFrom) })
	return out
}

func (tl Timeline) without(id uuid.UUID) Timeline {
	return slices.DeleteFunc(slices.Clone(tl), func(p Period) bool { return p.ID == id })
}

// affected returns the smallest Range covering every instant at which the two timelines give a
// different zone, or nil when they agree everywhere. It compares segment by segment between
// the period starts of both, so it needs no case analysis per kind of edit.
func affected(before, after Timeline) *Range {
	var bounds []time.Time
	for _, p := range before {
		bounds = append(bounds, p.ValidFrom)
	}
	for _, p := range after {
		bounds = append(bounds, p.ValidFrom)
	}
	if len(bounds) == 0 {
		return nil
	}
	slices.SortFunc(bounds, func(a, b time.Time) int { return a.Compare(b) })
	bounds = slices.CompactFunc(bounds, func(a, b time.Time) bool { return a.Equal(b) })

	var r *Range
	// Segment i runs from lo to hi (zero = unbounded); probe is any instant inside it.
	for i := 0; i <= len(bounds); i++ {
		var lo, hi, probe time.Time
		if i > 0 {
			lo, probe = bounds[i-1], bounds[i-1]
		} else {
			probe = bounds[0].Add(-time.Hour)
		}
		if i < len(bounds) {
			hi = bounds[i]
		}
		a, _ := before.At(probe)
		b, _ := after.At(probe)
		if a == b {
			continue
		}
		if r == nil {
			r = &Range{From: lo}
		}
		r.To = hi
	}
	return r
}
