package api

import (
	"context"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/documents/extract"
)

// Configured extraction providers (J12.6): the consent dialog names the model an external
// provider will receive the PDF with, so the UI must learn it before asking.
func (rt *router) extractorRoutes() {
	rt.handle("GET /api/v1/extractors", scope(auth.ReadConfig), rt.ops.ListExtractors)
}

func (o *owner) ListExtractors(ctx context.Context, _ oapi.ListExtractorsRequestObject) (oapi.ListExtractorsResponseObject, error) {
	svc, err := o.extractor()
	if err != nil {
		return nil, err
	}
	user := auth.PrincipalFrom(ctx).UserID
	out := oapi.ListExtractors200JSONResponse{Extractors: []oapi.Extractor{}}
	for _, id := range extract.Providers {
		ex := svc.Provider(id)
		if ex == nil {
			continue
		}
		e := oapi.Extractor{ID: oapi.ExtractorID(id), External: ex.External(), Enabled: !ex.External()}
		if m := ex.Model(); m != "" {
			e.Model = &m
		}
		if ex.External() {
			if e.Enabled, err = extract.Enabled(ctx, o.opts.DB.Q(), user, id); err != nil {
				return nil, err
			}
		}
		out.Extractors = append(out.Extractors, e)
	}
	return out, nil
}
