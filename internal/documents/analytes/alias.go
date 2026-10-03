package analytes

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Alias maps a printed label to an analyte. Seeded aliases have no owner and cannot be
// removed; an owner alias takes precedence over a seeded one with the same LabelKey.
type Alias struct {
	ID        int64
	Label     string
	Analyte   string // code
	Owner     bool
	CreatedAt time.Time
}

var (
	// ErrSeedAlias means a seeded alias was to be removed.
	ErrSeedAlias = errors.New("analytes: seeded aliases cannot be removed")
	// ErrEmptyLabel means the label has no letters or digits.
	ErrEmptyLabel = errors.New("analytes: label is empty")
)

const maxLabel = 200

// ListAliases returns the seeded aliases and the user's own.
func ListAliases(ctx context.Context, d *db.DB, user uuid.UUID) ([]Alias, error) {
	rows, err := d.Q().ListAnalyteAliases(ctx, user)
	if err != nil {
		return nil, err
	}
	out := make([]Alias, len(rows))
	for i, r := range rows {
		out[i] = Alias{ID: r.ID, Label: r.Label, Analyte: r.Analyte, Owner: r.UserID != nil, CreatedAt: r.CreatedAt}
	}
	return out, nil
}

// AddAlias maps label to the analyte code for user. It returns ErrUnknownAnalyte for a code
// outside the catalogue and db.ErrConflict when the user already maps that label.
func AddAlias(ctx context.Context, d *db.DB, user uuid.UUID, actor, label, code string) (Alias, error) {
	label = strings.TrimSpace(label)
	key := LabelKey(label)
	if key == "" || len(label) > maxLabel {
		return Alias{}, ErrEmptyLabel
	}
	if _, ok := Lookup(code); !ok {
		return Alias{}, ErrUnknownAnalyte
	}
	var a Alias
	err := d.Tx(ctx, func(q *dbq.Queries) error {
		r, err := q.InsertAnalyteAlias(ctx, dbq.InsertAnalyteAliasParams{Label: label, LabelKey: key, UserID: user, CreatedBy: actor, Analyte: code})
		if errors.Is(db.MapErr(err), db.ErrNotFound) {
			return ErrUnknownAnalyte // in code but not seeded: a missing migration
		}
		if err != nil {
			return err
		}
		a = Alias{ID: r.ID, Label: label, Analyte: code, Owner: true, CreatedAt: r.CreatedAt}
		return audit.Record(ctx, q, audit.Event{UserID: &user, Actor: actor, Action: "analyte_alias.create",
			TargetType: "analyte_alias", TargetID: strconv.FormatInt(r.ID, 10), Detail: map[string]any{"analyte": code}})
	})
	return a, err
}

// RemoveAlias deletes one of the user's aliases: db.ErrNotFound when there is none with that
// id, ErrSeedAlias for a seeded alias.
func RemoveAlias(ctx context.Context, d *db.DB, user uuid.UUID, actor string, id int64) error {
	return d.Tx(ctx, func(q *dbq.Queries) error {
		a, err := q.GetAnalyteAlias(ctx, dbq.GetAnalyteAliasParams{ID: id, UserID: user})
		if err != nil {
			return err
		}
		if a.UserID == nil {
			return ErrSeedAlias
		}
		if _, err := q.DeleteOwnerAnalyteAlias(ctx, dbq.DeleteOwnerAnalyteAliasParams{ID: id, UserID: user}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &user, Actor: actor, Action: "analyte_alias.delete",
			TargetType: "analyte_alias", TargetID: strconv.FormatInt(id, 10), Detail: map[string]any{"analyte": a.Analyte}})
	})
}

// Suggest returns the analyte code a printed label maps to, the user's alias first. It is a
// suggestion for review, never a confirmed mapping.
func Suggest(ctx context.Context, q *dbq.Queries, user uuid.UUID, label string) (string, bool, error) {
	key := LabelKey(label)
	if key == "" {
		return "", false, nil
	}
	code, err := q.FindAnalyteByLabel(ctx, dbq.FindAnalyteByLabelParams{LabelKey: key, UserID: user})
	if errors.Is(db.MapErr(err), db.ErrNotFound) {
		return "", false, nil
	}
	return code, err == nil, err
}
