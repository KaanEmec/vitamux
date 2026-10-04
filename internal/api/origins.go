package api

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/resolve"
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

func (o *owner) ListSourceDevices(ctx context.Context, req oapi.ListSourceDevicesRequestObject) (oapi.ListSourceDevicesResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	user := auth.PrincipalFrom(ctx).UserID
	rows, err := d.Q().ListSourceDevices(ctx, user)
	if err != nil {
		return nil, db.MapErr(err)
	}
	var records map[uuid.UUID][]oapi.DeviceConnectionRecords
	if req.Params.Include != nil && slices.Contains(*req.Params.Include, oapi.Records) {
		if records, err = deviceRecords(ctx, d.Q(), user); err != nil {
			return nil, err
		}
	}
	out := oapi.ListSourceDevices200JSONResponse{Devices: make([]oapi.SourceDevice, len(rows)), DeviceTypes: resolve.DeviceTypes}
	for i, r := range rows {
		dev := oapi.SourceDevice{ID: formatDeviceID(r.ID), Provider: r.Provider, Fingerprint: r.Fingerprint, Name: r.Name,
			DeviceType: r.DeviceType, Manufacturer: r.Manufacturer, Model: r.Model}
		if r.MergedInto != nil {
			into := formatDeviceID(*r.MergedInto)
			dev.MergedInto = &into
		}
		if records != nil {
			conns := records[r.ID]
			if conns == nil {
				conns = []oapi.DeviceConnectionRecords{}
			}
			dev.Connections = &conns
		}
		out.Devices[i] = dev
	}
	return out, nil
}

// deviceRecords counts each device's active records per connection.
func deviceRecords(ctx context.Context, q *dbq.Queries, user uuid.UUID) (map[uuid.UUID][]oapi.DeviceConnectionRecords, error) {
	rows, err := q.SourceDeviceRecords(ctx, user)
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := map[uuid.UUID][]oapi.DeviceConnectionRecords{}
	for _, r := range rows { // ordered by device, then connection
		conn := ingest.FormatConnectionID(r.ConnectionID)
		cs := out[r.DeviceID]
		if len(cs) == 0 || cs[len(cs)-1].ConnectionID != conn {
			cs = append(cs, oapi.DeviceConnectionRecords{ConnectionID: conn})
		}
		c := &cs[len(cs)-1].Records
		switch r.Kind {
		case "measurements":
			c.Measurements = r.N
		case "groups":
			c.Groups = r.N
		case "sleep_sessions":
			c.SleepSessions = r.N
		case "workouts":
			c.Workouts = r.N
		case "events":
			c.Events = r.N
		}
		out[r.DeviceID] = cs
	}
	return out, nil
}

var errNoSourceDevice = problemErr(CodeNotFound, "no such device")

// lockSourceDevice locks a device of the owner, refusing a merged one, whose records live on the
// device it was merged into.
func lockSourceDevice(ctx context.Context, q *dbq.Queries, user, id uuid.UUID, notFound error) (dbq.LockDevicesForMergeRow, error) {
	devs, err := q.LockDevicesForMerge(ctx, dbq.LockDevicesForMergeParams{UserID: user, Ids: []uuid.UUID{id}})
	if err != nil {
		return dbq.LockDevicesForMergeRow{}, err
	}
	i := slices.IndexFunc(devs, func(x dbq.LockDevicesForMergeRow) bool { return x.ID == id })
	switch {
	case i < 0:
		return dbq.LockDevicesForMergeRow{}, notFound
	case devs[i].MergedInto != nil:
		return devs[i], problemErr(CodeConflict, formatDeviceID(id)+" is merged into "+formatDeviceID(*devs[i].MergedInto)+"; use that device")
	}
	return devs[i], nil
}

// UpdateSourceDevice sets a device's type or name (J20.7). A set type wins over the normalizer's;
// the devices trigger clears the resolved cache when it changes.
func (o *owner) UpdateSourceDevice(ctx context.Context, req oapi.UpdateSourceDeviceRequestObject) (oapi.UpdateSourceDeviceResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	id, err := parseDeviceID(req.ID)
	if err != nil {
		return nil, errNoSourceDevice
	}
	var patch map[string]json.RawMessage
	if req.Body == nil || json.Unmarshal(*req.Body, &patch) != nil || patch == nil {
		return nil, problemErr(CodeValidationFailed, "a JSON object is required")
	}
	p := auth.PrincipalFrom(ctx)
	params := dbq.UpdateSourceDeviceParams{ID: id, UserID: p.UserID}
	detail := map[string]any{}
	for k, v := range patch {
		var val *string
		if err := json.Unmarshal(v, &val); err != nil {
			return nil, problemErr(CodeValidationFailed, "invalid "+k, FieldError{Pointer: "/" + k, Detail: "a string or null"})
		}
		switch k {
		case "device_type":
			if val != nil && !slices.Contains(resolve.DeviceTypes, *val) {
				return nil, problemErr(CodeValidationFailed, "unknown device type", FieldError{Pointer: "/device_type", Detail: "one of device_types"})
			}
			params.SetType, params.DeviceType = true, val
		case "name":
			if val != nil {
				n := strings.TrimSpace(*val)
				if n == "" || utf8.RuneCountInString(n) > 100 || strings.ContainsFunc(n, unicode.IsControl) {
					return nil, problemErr(CodeValidationFailed, "invalid name", FieldError{Pointer: "/name", Detail: "1 to 100 characters without control characters"})
				}
				val = &n
			}
			params.SetName, params.Name = true, val
		default:
			return nil, problemErr(CodeValidationFailed, "unknown field", FieldError{Pointer: "/" + k, Detail: "only device_type and name can be set"})
		}
		detail[k] = val
	}
	err = d.Tx(ctx, func(q *dbq.Queries) error {
		if _, err := lockSourceDevice(ctx, q, p.UserID, id, errNoSourceDevice); err != nil {
			return err
		}
		if _, err := q.UpdateSourceDevice(ctx, params); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: "device.update",
			TargetType: "device", TargetID: id.String(), Detail: detail})
	})
	if err != nil {
		return nil, err
	}
	return oapi.UpdateSourceDevice204Response{}, nil
}

// MergeSourceDevice moves a device's records to another device of the same provider and points
// it (and the devices merged into it) there, in one transaction (J20.7). The canonical writer
// follows merged_into, so later records of its fingerprint land on the target; dedupe keys keep
// the fingerprint, so reprocessing is a no-op. Moved dates are marked dirty as a write would.
func (o *owner) MergeSourceDevice(ctx context.Context, req oapi.MergeSourceDeviceRequestObject) (oapi.MergeSourceDeviceResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	src, err := parseDeviceID(req.ID)
	if err != nil {
		return nil, errNoSourceDevice
	}
	into, err := parseDeviceID(req.Body.Into)
	if err != nil {
		return nil, problemErr(CodeValidationFailed, "invalid target", FieldError{Pointer: "/into", Detail: "a dev_ id"})
	}
	if into == src {
		return nil, problemErr(CodeValidationFailed, "a device cannot merge into itself", FieldError{Pointer: "/into", Detail: "another device"})
	}
	var sleepCodes []string
	for _, m := range catalog.Metrics() {
		if m.Agg == catalog.SleepDerived {
			sleepCodes = append(sleepCodes, m.Code)
		}
	}
	p := auth.PrincipalFrom(ctx)
	var moved oapi.DeviceRecords
	err = d.Tx(ctx, func(q *dbq.Queries) error {
		// Lock in id order, so two opposite merges cannot deadlock.
		first, second := src, into
		if into.String() < src.String() {
			first, second = into, src
		}
		a, err := lockSourceDevice(ctx, q, p.UserID, first, errNoSourceDevice)
		if err != nil {
			return mergeTargetErr(err, first == into)
		}
		b, err := lockSourceDevice(ctx, q, p.UserID, second, errNoSourceDevice)
		if err != nil {
			return mergeTargetErr(err, second == into)
		}
		if a.ProviderID != b.ProviderID {
			return problemErr(CodeValidationFailed, "devices of different providers cannot merge", FieldError{Pointer: "/into", Detail: "a device of the same provider"})
		}
		// Mark first: the marks read the rows by their old device.
		if err := q.MarkDeviceDirty(ctx, dbq.MarkDeviceDirtyParams{DeviceID: &src, SleepCodes: sleepCodes}); err != nil {
			return err
		}
		n, err := q.MoveDeviceRecords(ctx, dbq.MoveDeviceRecordsParams{TargetID: &into, DeviceID: &src})
		if err != nil {
			return err
		}
		moved = oapi.DeviceRecords{Measurements: n.Measurements, Groups: n.Groups, SleepSessions: n.SleepSessions,
			Workouts: n.Workouts, Events: n.Events}
		if err := q.MergeDevice(ctx, dbq.MergeDeviceParams{TargetID: &into, UserID: p.UserID, DeviceID: src}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: "device.merge",
			TargetType: "device", TargetID: src.String(), Detail: map[string]any{"into": into.String(), "moved": moved}})
	})
	if err != nil {
		return nil, err
	}
	return oapi.MergeSourceDevice200JSONResponse{Moved: moved}, nil
}

// mergeTargetErr reports a missing target as a field error; a missing source stays 404.
func mergeTargetErr(err error, target bool) error {
	if target && errors.Is(err, errNoSourceDevice) {
		return problemErr(CodeValidationFailed, "no such target device", FieldError{Pointer: "/into", Detail: "not one of your devices"})
	}
	return err
}
