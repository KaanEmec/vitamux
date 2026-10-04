package api

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

// Paired devices (J15.2; docs/architecture/apple-health.md#pairing-and-security). The owner
// creates pairing codes, lists, revokes and asks devices for anchor resets; a device pairs with
// a code, reads its configuration and rotates its token on the ingest API. A device is a client
// of kind device; its token is checked like every client token (auth.Service.Bearer), so a
// revoked or rotated token fails on the next request.
func (rt *router) deviceRoutes() {
	write := scope(auth.WriteConfig)
	rt.handle("GET /api/v1/devices", scope(auth.ReadConfig), rt.ops.ListDevices)
	rt.handle("POST /api/v1/devices/pairing-codes", write, rt.ops.CreatePairingCode)
	rt.handle("POST /api/v1/devices/{id}/request-anchor-reset", write, rt.ops.RequestDeviceAnchorReset)
	rt.handle("POST /api/v1/devices/{id}/revoke", write, rt.ops.RevokeDevice)
	rt.handle("GET /api/v1/origins", scope(auth.ReadConfig), rt.ops.ListOrigins)
	rt.handle("PATCH /api/v1/origins/{id}", write, rt.ops.ClassifyOrigin)
	rt.handle("GET /api/v1/source-devices", scope(auth.ReadConfig), rt.ops.ListSourceDevices)
	rt.handle("PATCH /api/v1/source-devices/{id}", session, rt.ops.UpdateSourceDevice)
	rt.handle("POST /api/v1/source-devices/{id}/merge", session, rt.ops.MergeSourceDevice)

	client := access{ingest: true}
	rt.handle("POST /api/ingest/v1/devices/pair", public, rt.pairDevice)
	rt.handle("GET /api/ingest/v1/devices/self", client, rt.deviceSelf)
	rt.handle("POST /api/ingest/v1/devices/self/rotate-token", client, rt.rotateDeviceToken)
}

const (
	// allTypes is the anchor reset key for every HealthKit type.
	allTypes      = "*"
	maxResetTypes = 64
	// silentAfter is how long a requested type may store nothing before it is flagged as possibly
	// denied (docs/architecture/apple-health.md#permissions).
	silentAfter = 7 * 24 * time.Hour
)

var (
	errNoDevice = problemErr(CodeNotFound, "no such active device")
	hkType      = regexp.MustCompile(`^HK[A-Za-z0-9]{1,126}$`)
)

func (o *owner) ListDevices(ctx context.Context, _ oapi.ListDevicesRequestObject) (oapi.ListDevicesResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	rows, err := d.Q().ListOwnerDevices(ctx, auth.PrincipalFrom(ctx).UserID)
	if err != nil {
		return nil, db.MapErr(err)
	}
	now := time.Now()
	conns := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		conns[i] = r.ConnectionID
	}
	active, err := d.Q().ListActiveHealthKitTypes(ctx, dbq.ListActiveHealthKitTypesParams{ConnectionIds: conns, Since: now.Add(-silentAfter)})
	if err != nil {
		return nil, db.MapErr(err)
	}
	stored := map[uuid.UUID][]string{} // connection -> types with a payload since then
	for _, a := range active {
		stored[a.ConnectionID] = append(stored[a.ConnectionID], a.Type)
	}
	out := oapi.ListDevices200JSONResponse{Devices: make([]oapi.PairedDevice, len(rows))}
	for i, r := range rows {
		resets, err := anchorResets(r.AnchorResets)
		if err != nil {
			return nil, err
		}
		types := checkpointTypes(r.Checkpoint)
		dev := oapi.PairedDevice{ID: r.ID, Name: r.Name, ConnectionID: ingest.FormatConnectionID(r.ConnectionID),
			CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt, RevokedAt: r.RevokedAt, Types: types,
			PossiblyDenied: possiblyDenied(r, types, stored[r.ConnectionID], now), AnchorResets: resets}
		if t, ok := r.LastSyncAt.(time.Time); ok {
			dev.LastSyncAt = &t
		}
		out.Devices[i] = dev
	}
	return out, nil
}

// possiblyDenied lists the requested types with no stored payload in the last silentAfter, for a
// device that is paired that long and was seen in that time (otherwise every type is silent for
// another reason). HealthKit never reports read denial, so the flag is only a hint.
func possiblyDenied(d dbq.ListOwnerDevicesRow, types, stored []string, now time.Time) []string {
	out := []string{}
	since := now.Add(-silentAfter)
	if d.RevokedAt != nil || d.CreatedAt.After(since) || d.LastSeenAt == nil || d.LastSeenAt.Before(since) {
		return out
	}
	for _, t := range types {
		if !slices.Contains(stored, t) {
			out = append(out, t)
		}
	}
	return out
}

// checkpointTypes lists the HealthKit types keyed in a heartbeat checkpoint (per-type anchor
// hashes); the checkpoint is opaque, so anything else yields none.
func checkpointTypes(checkpoint []byte) []string {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(checkpoint, &m)
	types := []string{}
	for k := range m {
		if hkType.MatchString(k) {
			types = append(types, k)
		}
	}
	slices.Sort(types)
	return types
}

// anchorResets renders clients.anchor_resets ({type: requested_at}) sorted by type.
func anchorResets(raw json.RawMessage) ([]oapi.AnchorReset, error) {
	var m map[string]time.Time
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	out := make([]oapi.AnchorReset, 0, len(m))
	for t, at := range m {
		out = append(out, oapi.AnchorReset{Type: t, RequestedAt: at})
	}
	slices.SortFunc(out, func(a, b oapi.AnchorReset) int { return strings.Compare(a.Type, b.Type) })
	return out, nil
}

func (o *owner) CreatePairingCode(ctx context.Context, _ oapi.CreatePairingCodeRequestObject) (oapi.CreatePairingCodeResponseObject, error) {
	if o.opts.PublicURL == nil {
		return nil, problemErr(CodeUnavailable, "pairing needs VITAMUX_PUBLIC_URL, the address devices reach Vitamux at")
	}
	pc, err := o.opts.Auth.CreatePairingCode(ctx, auth.PrincipalFrom(ctx))
	if err != nil {
		return nil, err
	}
	base := o.opts.PublicURL.String()
	qr, err := json.Marshal(map[string]string{"url": base, "code": pc.Code})
	if err != nil {
		return nil, err
	}
	return oapi.CreatePairingCode201JSONResponse{Code: pc.Code, ExpiresAt: pc.ExpiresAt, URL: base, QrPayload: string(qr)}, nil
}

func (o *owner) RequestDeviceAnchorReset(ctx context.Context, req oapi.RequestDeviceAnchorResetRequestObject) (oapi.RequestDeviceAnchorResetResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return nil, errNoDevice
	}
	var types []string
	if req.Body != nil && req.Body.Types != nil {
		types = *req.Body.Types
	}
	if len(types) > maxResetTypes {
		return nil, problemErr(CodeValidationFailed, "too many types", FieldError{Pointer: "/types", Detail: "at most " + strconv.Itoa(maxResetTypes)})
	}
	for i, t := range types {
		if !hkType.MatchString(t) {
			return nil, problemErr(CodeValidationFailed, "invalid type", FieldError{Pointer: "/types/" + strconv.Itoa(i), Detail: "not a HealthKit type identifier"})
		}
	}
	if len(types) == 0 {
		types = []string{allTypes}
	}
	now := time.Now().UTC()
	resets := make(map[string]time.Time, len(types))
	for _, t := range types {
		resets[t] = now
	}
	body, err := json.Marshal(resets)
	if err != nil {
		return nil, err
	}
	p := auth.PrincipalFrom(ctx)
	err = d.Tx(ctx, func(q *dbq.Queries) error {
		n, err := q.AddAnchorResets(ctx, dbq.AddAnchorResetsParams{Resets: body, ID: id, UserID: p.UserID})
		if err != nil {
			return err
		}
		if n == 0 {
			return errNoDevice
		}
		return audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: "device.anchor_reset",
			TargetType: "client", TargetID: id.String(), Detail: map[string]any{"types": types}})
	})
	if err != nil {
		return nil, err
	}
	return oapi.RequestDeviceAnchorReset204Response{}, nil
}

func (o *owner) RevokeDevice(ctx context.Context, req oapi.RevokeDeviceRequestObject) (oapi.RevokeDeviceResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return nil, errNoDevice
	}
	p := auth.PrincipalFrom(ctx)
	err = d.Tx(ctx, func(q *dbq.Queries) error {
		n, err := q.RevokeOwnerDevice(ctx, dbq.RevokeOwnerDeviceParams{Now: time.Now(), ID: id, UserID: p.UserID})
		if err != nil {
			return err
		}
		if n == 0 {
			return errNoDevice
		}
		return audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: "device.revoke",
			TargetType: "client", TargetID: id.String()})
	})
	if err != nil {
		return nil, err
	}
	return oapi.RevokeDevice204Response{}, nil
}

// pairDevice exchanges a pairing code for a device token (public: the code is the credential).
func (rt *router) pairDevice(w http.ResponseWriter, r *http.Request) {
	if rt.opts.Auth == nil {
		writeProblem(w, r, CodeUnavailable, "pairing is unavailable: the master key is not loaded")
		return
	}
	var in struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	pd, err := rt.opts.Auth.PairDevice(r.Context(), in.Code, in.Name, ClientAddr(r.Context()))
	var throttled *auth.ThrottledError
	switch {
	case errors.As(err, &throttled):
		retryAfter(w, throttled.RetryAfter)
		writeProblem(w, r, CodeRateLimited, "too many wrong pairing codes; try again later")
	case errors.Is(err, auth.ErrBadDeviceName):
		writeProblem(w, r, CodeValidationFailed, "invalid device name", FieldError{Pointer: "/name", Detail: "1 to 100 characters without control characters"})
	case errors.Is(err, auth.ErrInvalidPairingCode):
		writeProblem(w, r, CodeValidationFailed, "invalid pairing code", FieldError{Pointer: "/code", Detail: "unknown, used or expired; create a new code"})
	case err != nil:
		rt.internal(w, r, "pair device", err)
	default:
		writeJSON(rt.log, w, http.StatusCreated, map[string]string{"device_id": pd.DeviceID.String(),
			"connection_id": ingest.FormatConnectionID(pd.ConnectionID), "token": pd.Token})
	}
}

// deviceSelf answers the calling device's configuration.
func (rt *router) deviceSelf(w http.ResponseWriter, r *http.Request) {
	if rt.opts.DB == nil {
		writeProblem(w, r, CodeUnavailable, "ingest is unavailable")
		return
	}
	c, err := rt.opts.DB.Q().GetClient(r.Context(), auth.PrincipalFrom(r.Context()).ID)
	if err != nil {
		rt.internal(w, r, "device self", db.MapErr(err))
		return
	}
	resets, err := anchorResets(c.AnchorResets)
	if err != nil {
		rt.internal(w, r, "device self", err)
		return
	}
	writeJSON(rt.log, w, http.StatusOK, map[string]any{"device_id": c.ID, "connection_id": ingest.FormatConnectionID(c.ConnectionID),
		"name": c.Name, "anchor_resets": resets})
}

// rotateDeviceToken replaces the caller's token; the old one is invalid from now on.
func (rt *router) rotateDeviceToken(w http.ResponseWriter, r *http.Request) {
	token, err := rt.opts.Auth.RotateClientToken(r.Context(), auth.PrincipalFrom(r.Context()))
	switch {
	case errors.Is(err, auth.ErrInvalidToken):
		writeProblem(w, r, CodeUnauthenticated, "invalid, revoked or expired token")
	case err != nil:
		rt.internal(w, r, "rotate device token", err)
	default:
		writeJSON(rt.log, w, http.StatusOK, map[string]string{"token": token})
	}
}

// retryAfter sets Retry-After in whole seconds, rounded up.
func retryAfter(w http.ResponseWriter, d time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(d.Seconds()))))
}
