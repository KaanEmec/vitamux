package api

import (
	"context"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/version"
)

// The version handshake is public so the app can check a server before sign-in (J22.3); the
// build version and commit are for read:config callers only.
func (rt *router) systemRoutes() {
	rt.handle("GET /api/v1/system/version", public, rt.ops.GetSystemVersion)
}

func (o *owner) GetSystemVersion(ctx context.Context, _ oapi.GetSystemVersionRequestObject) (oapi.GetSystemVersionResponseObject, error) {
	out := oapi.GetSystemVersion200JSONResponse{Product: oapi.Vitamux, APIVersion: version.APIVersion, MinAppVersion: version.MinAppVersion}
	if p := auth.PrincipalFrom(ctx); p != nil && p.Can(auth.ReadConfig) {
		v, c := version.Version, version.Commit
		out.Version, out.Commit = &v, &c
	}
	return out, nil
}
