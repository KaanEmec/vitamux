package api

import (
	"context"
	"errors"
	"maps"
	"strings"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/lifecycle"
)

// Retention settings (J13.4; lifecycle.Retention) inside GET and PATCH /api/v1/settings.

func retentionSettings(ctx context.Context, q *dbq.Queries, user uuid.UUID, out *oapi.Settings) error {
	r, err := lifecycle.GetRetention(ctx, q, user)
	if err != nil {
		return err
	}
	out.RetentionRawDays, out.RetentionSupersededAfterDays, out.RetentionIdempotencyKeyDays = &r.RawDays, &r.SupersededDays, &r.IdempotencyKeyDays
	return nil
}

// updateRetention applies the retention keys present in body; raw days merge per provider.
func (o *owner) updateRetention(ctx context.Context, body *oapi.Settings) error {
	if body.RetentionRawDays == nil && body.RetentionSupersededAfterDays == nil && body.RetentionIdempotencyKeyDays == nil {
		return nil
	}
	p := auth.PrincipalFrom(ctx)
	err := o.opts.DB.Tx(ctx, func(q *dbq.Queries) error {
		r, err := lifecycle.GetRetention(ctx, q, p.UserID)
		if err != nil {
			return err
		}
		if body.RetentionRawDays != nil {
			maps.Copy(r.RawDays, *body.RetentionRawDays)
		}
		if body.RetentionSupersededAfterDays != nil {
			r.SupersededDays = *body.RetentionSupersededAfterDays
		}
		if body.RetentionIdempotencyKeyDays != nil {
			r.IdempotencyKeyDays = *body.RetentionIdempotencyKeyDays
		}
		return lifecycle.SetRetention(ctx, q, p.UserID, p.Actor(), r)
	})
	if errors.Is(err, lifecycle.ErrInvalidRetention) {
		return problemErr(CodeValidationFailed, "invalid retention setting",
			FieldError{Pointer: "/retention", Detail: strings.TrimPrefix(err.Error(), lifecycle.ErrInvalidRetention.Error()+": ")})
	}
	return err
}
