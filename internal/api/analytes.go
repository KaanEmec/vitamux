package api

import (
	"context"
	"errors"
	"strconv"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/documents/analytes"
)

// Analyte aliases (J12.5; docs/architecture/analyte-catalog.md): seeded labels plus the
// owner's own, which extraction review uses to suggest an analyte.
func (rt *router) analyteRoutes() {
	rt.handle("GET /api/v1/analytes/aliases", scope(auth.ReadConfig), rt.ops.ListAnalyteAliases)
	rt.handle("POST /api/v1/analytes/aliases", scope(auth.WriteConfig), rt.ops.CreateAnalyteAlias)
	rt.handle("DELETE /api/v1/analytes/aliases/{id}", scope(auth.WriteConfig), rt.ops.DeleteAnalyteAlias)
}

func apiAlias(a analytes.Alias) oapi.AnalyteAlias {
	src := oapi.AnalyteAliasSource("seed")
	if a.Owner {
		src = "owner"
	}
	return oapi.AnalyteAlias{ID: strconv.FormatInt(a.ID, 10), Label: a.Label, Analyte: a.Analyte, Source: src, CreatedAt: a.CreatedAt}
}

func (o *owner) ListAnalyteAliases(ctx context.Context, _ oapi.ListAnalyteAliasesRequestObject) (oapi.ListAnalyteAliasesResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	as, err := analytes.ListAliases(ctx, o.opts.DB, auth.PrincipalFrom(ctx).UserID)
	if err != nil {
		return nil, err
	}
	out := oapi.ListAnalyteAliases200JSONResponse{Aliases: make([]oapi.AnalyteAlias, len(as))}
	for i, a := range as {
		out.Aliases[i] = apiAlias(a)
	}
	return out, nil
}

func (o *owner) CreateAnalyteAlias(ctx context.Context, req oapi.CreateAnalyteAliasRequestObject) (oapi.CreateAnalyteAliasResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	p := auth.PrincipalFrom(ctx)
	a, err := analytes.AddAlias(ctx, o.opts.DB, p.UserID, p.Actor(), req.Body.Label, req.Body.Analyte)
	switch {
	case errors.Is(err, analytes.ErrEmptyLabel):
		return nil, problemErr(CodeValidationFailed, "invalid label", FieldError{Pointer: "/label", Detail: "needs a letter or digit and at most 200 bytes"})
	case errors.Is(err, analytes.ErrUnknownAnalyte):
		return nil, problemErr(CodeValidationFailed, "unknown analyte", FieldError{Pointer: "/analyte", Detail: "not a code in docs/analytes.md"})
	case errors.Is(err, db.ErrConflict):
		return nil, problemErr(CodeConflict, "you already map this label; remove that alias first")
	case err != nil:
		return nil, err
	}
	return oapi.CreateAnalyteAlias201JSONResponse(apiAlias(a)), nil
}

func (o *owner) DeleteAnalyteAlias(ctx context.Context, req oapi.DeleteAnalyteAliasRequestObject) (oapi.DeleteAnalyteAliasResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		return nil, db.ErrNotFound
	}
	p := auth.PrincipalFrom(ctx)
	switch err := analytes.RemoveAlias(ctx, o.opts.DB, p.UserID, p.Actor(), id); {
	case errors.Is(err, analytes.ErrSeedAlias):
		return nil, problemErr(CodeConflict, "seeded aliases cannot be removed; add your own alias for the label instead")
	case err != nil:
		return nil, err
	}
	return oapi.DeleteAnalyteAlias204Response{}, nil
}
