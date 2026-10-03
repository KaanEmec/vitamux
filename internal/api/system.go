package api

import (
	"context"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/version"
)

func (rt *router) systemRoutes() {
	rt.handle("GET /api/v1/system/version", scope(auth.ReadConfig), rt.ops.GetSystemVersion)
}

func (o *owner) GetSystemVersion(context.Context, oapi.GetSystemVersionRequestObject) (oapi.GetSystemVersionResponseObject, error) {
	return oapi.GetSystemVersion200JSONResponse{Version: version.Version, Commit: version.Commit}, nil
}
