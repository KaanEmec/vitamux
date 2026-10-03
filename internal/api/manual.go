package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// Manual entry (J10.4). A value the owner types is source data like any other: it is stored as a
// raw payload (stream normalize.ManualStream) of the owner's provider-manual connection and
// normalized by normalize.Manual through the regular writer, so the row has provenance, carries
// the manual_entry flag, and reprocessing replays it.
func (rt *router) manualRoutes() {
	rt.handle("POST /api/v1/measurements/manual", scope(auth.WriteConfig), rt.ops.CreateManualMeasurement)
}

// manualAccountKey makes the owner's manual connection unique (connections.account_key).
var manualAccountKey = sha256.Sum256([]byte("vitamux:manual"))

var manualRegistry = sync.OnceValues(func() (*normalize.Registry, error) { return normalize.NewRegistry(normalize.Manual{}) })

func (o *owner) CreateManualMeasurement(ctx context.Context, req oapi.CreateManualMeasurementRequestObject) (oapi.CreateManualMeasurementResponseObject, error) {
	if o.opts.DB == nil || o.opts.Blobs == nil {
		return nil, problemErr(CodeUnavailable, "manual entry needs the database, the master key and the data directory")
	}
	b := req.Body
	if m, ok := catalog.Lookup(b.Metric); ok && m.Group == "bp_reading" {
		return nil, problemErr(CodeValidationFailed, "blood-pressure components cannot be entered one by one", FieldError{Pointer: "/metric", Detail: "is part of a blood-pressure reading"})
	}
	key, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(normalize.ManualEntry{Metric: b.Metric, Value: b.Value, Unit: b.Unit, Start: b.StartAt, End: b.EndAt})
	if err != nil {
		return nil, err
	}
	// Check the entry before storing it, so a mistake leaves no failed raw payload behind.
	out, err := normalize.Manual{}.Normalize(ctx, normalize.RawPayload{ExternalKey: key.String(), Body: body}, normalize.Env{})
	if err == nil {
		err = out.Validate()
	}
	if err != nil {
		return nil, problemErr(CodeValidationFailed, strings.TrimPrefix(err.Error(), normalize.ErrInvalidOutput.Error()+": "))
	}
	reg, err := manualRegistry()
	if err != nil {
		return nil, err
	}
	vers, err := normalize.RegisterVersions(ctx, o.opts.DB.Q(), reg)
	if err != nil {
		return nil, err
	}
	p := auth.PrincipalFrom(ctx)
	var conn uuid.UUID
	var rawID int64
	err = o.opts.DB.Tx(ctx, func(q *dbq.Queries) error {
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		if conn, err = q.EnsureManualConnection(ctx, dbq.EnsureManualConnectionParams{ID: id, UserID: p.UserID, AccountKey: manualAccountKey[:]}); err != nil {
			return err
		}
		batch, err := ingest.CreateBatch(ctx, q, ingest.BatchInfo{UserID: p.UserID, ConnectionID: conn, SourceKind: ingest.SourceManual})
		if err != nil {
			return err
		}
		res, err := ingest.StoreRaw(ctx, q, o.opts.Blobs, batch, []ingest.RawItem{{Stream: normalize.ManualStream, ExternalKey: key.String(),
			ContentType: "application/json", FetchedAt: time.Now(), Body: body, Request: ingest.Request{Endpoint: "POST /api/v1/measurements/manual"}}})
		if err != nil {
			return err
		}
		rawID = res[0].RawPayloadID
		return audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: "measurement.manual_create",
			TargetType: "raw_payload", TargetID: strconv.FormatInt(rawID, 10), Detail: map[string]any{"metric": b.Metric}})
	})
	if err != nil {
		return nil, err
	}
	res, err := (&normalize.Processor{DB: o.opts.DB, Blobs: o.opts.Blobs, Registry: reg, Log: o.log}).Process(ctx, rawID, vers)
	if err != nil {
		return nil, err
	}
	if res.Outcome != normalize.Normalized {
		return nil, fmt.Errorf("manual entry %d: %s (%s)", rawID, res.Outcome, res.Code) // validated above: a bug
	}
	m, err := o.manualRow(ctx, conn, rawID, b.Metric, b.StartAt)
	if err != nil {
		return nil, err
	}
	return oapi.CreateManualMeasurement201JSONResponse(m), nil
}

// manualRow reads back the measurement raw payload rawID produced, with its provenance.
func (o *owner) manualRow(ctx context.Context, conn uuid.UUID, rawID int64, metric string, at time.Time) (oapi.Measurement, error) {
	f := sourceFilter{user: auth.PrincipalFrom(ctx).UserID, include: map[string]bool{"provenance": true}}
	start, end := at.Truncate(time.Microsecond), at.Truncate(time.Microsecond).Add(time.Microsecond)
	rows, err := o.opts.DB.Q().ReadMeasurements(ctx, dbq.ReadMeasurementsParams{UserID: f.user, Metrics: []string{metric},
		Connections: []uuid.UUID{conn}, StartAt: &start, EndAt: &end, Lim: 1000})
	if err != nil {
		return oapi.Measurement{}, db.MapErr(err)
	}
	raws, err := loadRawRefs(ctx, o, f, rows, func(r dbq.ReadMeasurementsRow) *int64 { return r.RawPayloadID })
	if err != nil {
		return oapi.Measurement{}, err
	}
	for _, r := range rows {
		if r.RawPayloadID == nil || *r.RawPayloadID != rawID {
			continue
		}
		s := srcCols{r.Provider, r.ConnectionID, r.DeviceID, r.DeviceType, r.OriginKey, r.ExternalID, r.DedupeKey,
			r.RawPayloadID, r.NormalizerName, r.NormalizerVersion, r.IngestedAt, r.NormalizedAt,
			r.SupersededAt, r.SupersededBy, r.DeletedAt, r.DeletedByRawID}
		return oapi.Measurement{ID: strconv.FormatInt(r.ID, 10), Metric: r.Metric, Kind: oapi.MeasurementKind(r.Kind),
			StartAt: r.StartAt, EndAt: r.EndAt, TzOffsetMin: intp(r.TzOffsetMin), LocalDate: apiDate(r.LocalDate), Value: r.Value,
			Unit: r.Unit, SourceValue: r.SourceValue, SourceUnit: r.SourceUnit, QualityFlags: int(r.QualityFlags),
			GroupID: idp(r.GroupID), Source: s.source(), Provenance: s.provenance(raws)}, nil
	}
	return oapi.Measurement{}, errors.New("manual entry: normalized row not found")
}
