package api

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Origins and source devices for the configurator (J15.6;
// docs/architecture/apple-health.md#origins-and-relays). The owner's classification of an origin
// is stored on the origin itself: it wins over known_relay_origins, which only seeds new ones, and a
// trigger clears the resolved cache because the `relayed` selector reads it live.

func (o *owner) ListOrigins(ctx context.Context, _ oapi.ListOriginsRequestObject) (oapi.ListOriginsResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	rows, err := d.Q().ListOwnerOrigins(ctx, auth.PrincipalFrom(ctx).UserID)
	if err != nil {
		return nil, db.MapErr(err)
	}
	targets, err := d.Q().ListRelayTargets(ctx)
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := oapi.ListOrigins200JSONResponse{Origins: make([]oapi.DataOrigin, len(rows)), RelayTargets: make([]oapi.RelayTarget, len(targets))}
	for i, r := range rows {
		out.Origins[i] = oapi.DataOrigin{ID: r.ID, Provider: r.Provider, OriginKey: r.OriginKey, Name: r.Name, IsNative: r.IsNative,
			RelayedProvider: r.RelayedProvider, CreatedAt: r.CreatedAt}
	}
	for i, t := range targets {
		out.RelayTargets[i] = oapi.RelayTarget{Code: t.Code, Name: t.Name}
	}
	return out, nil
}

func (o *owner) ClassifyOrigin(ctx context.Context, req oapi.ClassifyOriginRequestObject) (oapi.ClassifyOriginResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return nil, problemErr(CodeNotFound, "no such origin")
	}
	target := req.Body.RelayedProvider
	p := auth.PrincipalFrom(ctx)
	err = d.Tx(ctx, func(q *dbq.Queries) error {
		if target != nil {
			targets, err := q.ListRelayTargets(ctx)
			if err != nil {
				return err
			}
			if !slices.ContainsFunc(targets, func(t dbq.ListRelayTargetsRow) bool { return t.Code == *target }) {
				return problemErr(CodeValidationFailed, "unknown relay target", FieldError{Pointer: "/relayed_provider", Detail: "not one of relay_targets"})
			}
		}
		n, err := q.SetOriginRelay(ctx, dbq.SetOriginRelayParams{RelayedProvider: target, ID: id, UserID: p.UserID})
		if err != nil {
			return err
		}
		if n == 0 {
			return problemErr(CodeNotFound, "no such origin")
		}
		return audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: "origin.classify",
			TargetType: "data_origin", TargetID: id.String(), Detail: map[string]any{"relayed_provider": target}})
	})
	if err != nil {
		return nil, err
	}
	return oapi.ClassifyOrigin204Response{}, nil
}

func (o *owner) ListSourceDevices(ctx context.Context, _ oapi.ListSourceDevicesRequestObject) (oapi.ListSourceDevicesResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	rows, err := d.Q().ListSourceDevices(ctx, auth.PrincipalFrom(ctx).UserID)
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := oapi.ListSourceDevices200JSONResponse{Devices: make([]oapi.SourceDevice, len(rows))}
	for i, r := range rows {
		out.Devices[i] = oapi.SourceDevice{ID: formatDeviceID(r.ID), Provider: r.Provider, DeviceType: r.DeviceType,
			Manufacturer: r.Manufacturer, Model: r.Model}
	}
	return out, nil
}
