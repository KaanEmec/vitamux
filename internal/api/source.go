package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/provenance"
)

// Source data endpoints (J10.2): normalized rows with their source identity and provenance,
// filtered by the repeatable filters and keyset-paginated on (start, id), so a page boundary
// never moves when rows are inserted concurrently. Every query is scoped to the caller.
func (rt *router) sourceRoutes() {
	read := scope(auth.ReadHealth)
	rt.handle("GET /api/v1/measurements", read, rt.ops.ListMeasurements)
	rt.handle("GET /api/v1/groups", read, rt.ops.ListGroups)
	rt.handle("GET /api/v1/blood-pressure", read, rt.ops.ListBloodPressure)
	rt.handle("GET /api/v1/sleep", read, rt.ops.ListSleep)
	rt.handle("GET /api/v1/sleep/{id}", read, rt.ops.GetSleep)
	rt.handle("GET /api/v1/workouts", read, rt.ops.ListWorkouts)
	rt.handle("GET /api/v1/workouts/{id}", read, rt.ops.GetWorkout)
	rt.handle("GET /api/v1/workouts/{id}/route", read, rt.ops.GetWorkoutRoute)
	rt.handle("GET /api/v1/provenance/{entity}/{id}", read, rt.ops.GetProvenance)
}

// sourceFilter is the parsed common filter set. Nil slices and pointers mean "no filter".
type sourceFilter struct {
	user                 uuid.UUID
	providers, origins   []string
	connections, devices []uuid.UUID
	start, end           *time.Time
	startDate, endDate   *time.Time
	include              map[string]bool
}

// filterParams are the generated query parameters every list shares.
type filterParams struct {
	start, end           *time.Time
	startDate, endDate   *openapi_types.Date
	providers, origins   *[]string
	connections, devices *[]string
	include              *[]string
}

var includes = map[string]bool{"provenance": true, "stages": true, "segments": true, "superseded": true, "deleted": true}

func (o *owner) filter(ctx context.Context, p filterParams) (sourceFilter, error) {
	f := sourceFilter{user: auth.PrincipalFrom(ctx).UserID, start: p.start, end: p.end,
		providers: ptrVal(p.providers), origins: ptrVal(p.origins), include: map[string]bool{}}
	if o.opts.DB == nil {
		return f, problemErr(CodeUnavailable, "the database is not ready")
	}
	if p.startDate != nil {
		f.startDate = &p.startDate.Time
	}
	if p.endDate != nil {
		f.endDate = &p.endDate.Time
	}
	var errs []FieldError
	if f.start != nil && f.end != nil && !f.end.After(*f.start) {
		errs = append(errs, FieldError{Pointer: "/end", Detail: "must be after start"})
	}
	if f.startDate != nil && f.endDate != nil && f.endDate.Before(*f.startDate) {
		errs = append(errs, FieldError{Pointer: "/end_date", Detail: "must not be before start_date"})
	}
	for _, s := range ptrVal(p.connections) {
		id, err := ingest.ParseConnectionID(s)
		if err != nil {
			errs = append(errs, FieldError{Pointer: "/connection", Detail: "must be a conn_ id"})
			break
		}
		f.connections = append(f.connections, id)
	}
	for _, s := range ptrVal(p.devices) {
		id, err := parseDeviceID(s)
		if err != nil {
			errs = append(errs, FieldError{Pointer: "/device", Detail: "must be a dev_ id"})
			break
		}
		f.devices = append(f.devices, id)
	}
	for _, s := range ptrVal(p.include) {
		if !includes[s] {
			errs = append(errs, FieldError{Pointer: "/include", Detail: "unknown expansion " + strconv.Quote(s)})
			continue
		}
		f.include[s] = true
	}
	if len(errs) > 0 {
		return f, problemErr(CodeValidationFailed, "invalid filter", errs...)
	}
	return f, nil
}

// page is one keyset page request: the row limit and the position to continue after.
type page struct {
	query string // the operation and filters the cursor is bound to
	limit int
	after *cursor
}

// newPage validates limit and cursor. params is the request's parameters with Limit and Cursor
// cleared: the cursor's MAC covers them, so it only resumes the query that produced it.
func (o *owner) newPage(op string, params any, limit *int, c *string) (page, error) {
	b, err := json.Marshal(params)
	if err != nil {
		return page{}, err
	}
	p := page{query: op + string(b)}
	if p.limit, err = pageLimit(limit, maxPageLimit); err != nil {
		return p, err
	}
	if c != nil {
		at, err := o.cursors.decode(p.query, *c)
		if err != nil {
			return p, err
		}
		p.after = &at
	}
	return p, nil
}

// afterKey returns the cursor's start instant, nil for the first page.
func (p page) afterKey() (*time.Time, error) {
	if p.after == nil {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, p.after.Key)
	if err != nil {
		return nil, errInvalidCursor
	}
	return &t, nil
}

func (p page) afterInt() (int64, error) {
	if p.after == nil {
		return 0, nil
	}
	n, err := strconv.ParseInt(p.after.ID, 10, 64)
	if err != nil {
		return 0, errInvalidCursor
	}
	return n, nil
}

func (p page) afterUUID() (uuid.UUID, error) {
	if p.after == nil {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(p.after.ID)
	if err != nil {
		return uuid.Nil, errInvalidCursor
	}
	return id, nil
}

// afterKeyUUID returns both parts of a cursor whose id is a UUID.
func (p page) afterKeyUUID() (*time.Time, uuid.UUID, error) {
	key, err := p.afterKey()
	if err != nil {
		return nil, uuid.Nil, err
	}
	id, err := p.afterUUID()
	return key, id, err
}

// lim is the row count to fetch: one more than the page, to learn whether another follows.
func (p page) lim() int32 { return int32(p.limit + 1) } //nolint:gosec // limit <= maxPageLimit

// trim cuts rows to the page and returns has_more and the next cursor, built from the last row's
// start and id.
func trim[R any](o *owner, p page, rows []R, key func(R) (time.Time, string)) ([]R, bool, *string) {
	if len(rows) <= p.limit {
		return rows, false, nil
	}
	rows = rows[:p.limit]
	at, id := key(rows[len(rows)-1])
	next := o.cursors.encode(p.query, cursor{Key: at.Format(time.RFC3339Nano), ID: id})
	return rows, true, &next
}

func (o *owner) ListMeasurements(ctx context.Context, req oapi.ListMeasurementsRequestObject) (oapi.ListMeasurementsResponseObject, error) {
	prm := req.Params
	f, err := o.filter(ctx, filterParams{start: prm.Start, end: prm.End, startDate: prm.StartDate, endDate: prm.EndDate,
		providers: prm.Provider, origins: prm.Origin, connections: prm.Connection, devices: prm.Device, include: prm.Include})
	if err != nil {
		return nil, err
	}
	var kinds []string
	for _, k := range ptrVal(prm.Kind) {
		kinds = append(kinds, string(k))
	}
	bound := prm
	bound.Limit, bound.Cursor = nil, nil
	p, err := o.newPage("measurements", bound, prm.Limit, prm.Cursor)
	if err != nil {
		return nil, err
	}
	afterKey, err := p.afterKey()
	if err != nil {
		return nil, err
	}
	afterID, err := p.afterInt()
	if err != nil {
		return nil, err
	}
	q := o.opts.DB.Q()
	rows, err := q.ReadMeasurements(ctx, dbq.ReadMeasurementsParams{UserID: f.user, Metrics: ptrVal(prm.Metric), Kinds: kinds,
		Providers: f.providers, Connections: f.connections, Devices: f.devices, Origins: f.origins,
		StartAt: f.start, EndAt: f.end, StartDate: f.startDate, EndDate: f.endDate,
		WithSuperseded: f.include["superseded"], WithDeleted: f.include["deleted"], AfterKey: afterKey, AfterID: afterID, Lim: p.lim()})
	if err != nil {
		return nil, db.MapErr(err)
	}
	rows, more, next := trim(o, p, rows, func(r dbq.ReadMeasurementsRow) (time.Time, string) {
		return r.StartAt, strconv.FormatInt(r.ID, 10)
	})
	raws, err := loadRawRefs(ctx, o, f, rows, func(r dbq.ReadMeasurementsRow) *int64 { return r.RawPayloadID })
	if err != nil {
		return nil, err
	}
	out := oapi.ListMeasurements200JSONResponse{HasMore: more, NextCursor: next, Measurements: make([]oapi.Measurement, 0, len(rows))}
	for _, r := range rows {
		s := srcCols{r.Provider, r.ConnectionID, r.DeviceID, r.DeviceType, r.OriginKey, r.ExternalID, r.DedupeKey,
			r.RawPayloadID, r.NormalizerName, r.NormalizerVersion, r.IngestedAt, r.NormalizedAt,
			r.SupersededAt, r.SupersededBy, r.DeletedAt, r.DeletedByRawID}
		m := oapi.Measurement{ID: strconv.FormatInt(r.ID, 10), Metric: r.Metric,
			Kind: oapi.MeasurementKind(r.Kind), StartAt: r.StartAt, EndAt: r.EndAt, TzOffsetMin: intp(r.TzOffsetMin),
			LocalDate: apiDate(r.LocalDate), Value: r.Value, Unit: r.Unit, SourceValue: r.SourceValue, SourceUnit: r.SourceUnit,
			QualityFlags: int(r.QualityFlags), GroupID: idp(r.GroupID), Source: s.source(), Provenance: s.provenance(raws)}
		if len(r.Context) > 0 {
			c := json.RawMessage(r.Context)
			m.Context = &c
		}
		out.Measurements = append(out.Measurements, m)
	}
	return out, nil
}

// readGroups returns one page of groups of kind (nil for all) with their components.
func (o *owner) readGroups(ctx context.Context, f sourceFilter, p page, kind *string) ([]dbq.ReadGroupsRow, map[int64][]dbq.ReadGroupComponentsRow, bool, *string, rawRefs, error) {
	afterKey, err := p.afterKey()
	if err != nil {
		return nil, nil, false, nil, nil, err
	}
	afterID, err := p.afterInt()
	if err != nil {
		return nil, nil, false, nil, nil, err
	}
	q := o.opts.DB.Q()
	rows, err := q.ReadGroups(ctx, dbq.ReadGroupsParams{UserID: f.user, Kind: kind,
		Providers: f.providers, Connections: f.connections, Devices: f.devices, Origins: f.origins,
		StartAt: f.start, EndAt: f.end, StartDate: f.startDate, EndDate: f.endDate,
		WithSuperseded: f.include["superseded"], WithDeleted: f.include["deleted"], AfterKey: afterKey, AfterID: afterID, Lim: p.lim()})
	if err != nil {
		return nil, nil, false, nil, nil, db.MapErr(err)
	}
	rows, more, next := trim(o, p, rows, func(r dbq.ReadGroupsRow) (time.Time, string) {
		return r.MeasuredAt, strconv.FormatInt(r.ID, 10)
	})
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	comps, err := q.ReadGroupComponents(ctx, ids)
	if err != nil {
		return nil, nil, false, nil, nil, db.MapErr(err)
	}
	byGroup := map[int64][]dbq.ReadGroupComponentsRow{}
	for _, c := range comps {
		byGroup[c.GroupID] = append(byGroup[c.GroupID], c)
	}
	raws, err := loadRawRefs(ctx, o, f, rows, func(r dbq.ReadGroupsRow) *int64 { return r.RawPayloadID })
	return rows, byGroup, more, next, raws, err
}

func groupSrc(r dbq.ReadGroupsRow) srcCols {
	return srcCols{r.Provider, r.ConnectionID, r.DeviceID, r.DeviceType, r.OriginKey, r.ExternalID, r.DedupeKey,
		r.RawPayloadID, r.NormalizerName, r.NormalizerVersion, r.IngestedAt, r.NormalizedAt,
		r.SupersededAt, r.SupersededBy, r.DeletedAt, r.DeletedByRawID}
}

func (o *owner) ListGroups(ctx context.Context, req oapi.ListGroupsRequestObject) (oapi.ListGroupsResponseObject, error) {
	prm := req.Params
	f, err := o.filter(ctx, filterParams{start: prm.Start, end: prm.End, startDate: prm.StartDate, endDate: prm.EndDate,
		providers: prm.Provider, origins: prm.Origin, connections: prm.Connection, devices: prm.Device, include: prm.Include})
	if err != nil {
		return nil, err
	}
	var kind *string
	if prm.Kind != nil {
		k := string(*prm.Kind)
		if k != "bp_reading" && k != "body_composition" {
			return nil, problemErr(CodeValidationFailed, "invalid filter", FieldError{Pointer: "/kind", Detail: "must be bp_reading or body_composition"})
		}
		kind = &k
	}
	bound := prm
	bound.Limit, bound.Cursor = nil, nil
	p, err := o.newPage("groups", bound, prm.Limit, prm.Cursor)
	if err != nil {
		return nil, err
	}
	rows, comps, more, next, raws, err := o.readGroups(ctx, f, p, kind)
	if err != nil {
		return nil, err
	}
	out := oapi.ListGroups200JSONResponse{HasMore: more, NextCursor: next, Groups: make([]oapi.Group, 0, len(rows))}
	for _, r := range rows {
		cs := make([]oapi.GroupComponent, 0, len(comps[r.ID]))
		for _, c := range comps[r.ID] {
			cs = append(cs, oapi.GroupComponent{ID: strconv.FormatInt(c.ID, 10), Metric: c.Metric, Value: c.Value, Unit: c.Unit,
				SourceValue: c.SourceValue, SourceUnit: c.SourceUnit, QualityFlags: int(c.QualityFlags)})
		}
		s := groupSrc(r)
		out.Groups = append(out.Groups, oapi.Group{ID: strconv.FormatInt(r.ID, 10), Kind: oapi.GroupKind(r.Kind),
			MeasuredAt: r.MeasuredAt, TzOffsetMin: intp(r.TzOffsetMin), LocalDate: apiDate(r.LocalDate), Context: r.Context,
			Components: cs, Source: s.source(), Provenance: s.provenance(raws)})
	}
	return out, nil
}

func (o *owner) ListBloodPressure(ctx context.Context, req oapi.ListBloodPressureRequestObject) (oapi.ListBloodPressureResponseObject, error) {
	prm := req.Params
	f, err := o.filter(ctx, filterParams{start: prm.Start, end: prm.End, startDate: prm.StartDate, endDate: prm.EndDate,
		providers: prm.Provider, origins: prm.Origin, connections: prm.Connection, devices: prm.Device, include: prm.Include})
	if err != nil {
		return nil, err
	}
	bound := prm
	bound.Limit, bound.Cursor = nil, nil
	p, err := o.newPage("blood-pressure", bound, prm.Limit, prm.Cursor)
	if err != nil {
		return nil, err
	}
	kind := "bp_reading"
	rows, comps, more, next, raws, err := o.readGroups(ctx, f, p, &kind)
	if err != nil {
		return nil, err
	}
	out := oapi.ListBloodPressure200JSONResponse{HasMore: more, NextCursor: next, Readings: make([]oapi.BloodPressureReading, 0, len(rows))}
	for _, r := range rows {
		s := groupSrc(r)
		bp := oapi.BloodPressureReading{ID: strconv.FormatInt(r.ID, 10), MeasuredAt: r.MeasuredAt, TzOffsetMin: intp(r.TzOffsetMin),
			LocalDate: apiDate(r.LocalDate), Context: r.Context, Source: s.source(), Provenance: s.provenance(raws)}
		for _, c := range comps[r.ID] {
			switch c.Metric {
			case "bp_systolic":
				bp.Systolic = &c.Value
			case "bp_diastolic":
				bp.Diastolic = &c.Value
			case "bp_pulse":
				bp.Pulse = &c.Value
			}
		}
		out.Readings = append(out.Readings, bp)
	}
	return out, nil
}

// readSleep returns sleep rows and, when stages is set, their stages.
func (o *owner) readSleep(ctx context.Context, arg dbq.ReadSleepParams, stages bool) ([]dbq.ReadSleepRow, map[uuid.UUID][]oapi.SleepStage, error) {
	q := o.opts.DB.Q()
	rows, err := q.ReadSleep(ctx, arg)
	if err != nil || !stages {
		return rows, nil, db.MapErr(err)
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	st, err := q.ReadSleepStages(ctx, ids)
	if err != nil {
		return nil, nil, db.MapErr(err)
	}
	by := map[uuid.UUID][]oapi.SleepStage{}
	for _, s := range st {
		by[s.SessionID] = append(by[s.SessionID], oapi.SleepStage{Stage: oapi.SleepStageStage(s.Stage), StartAt: s.StartAt, EndAt: s.EndAt})
	}
	return rows, by, nil
}

func sleepSession(r dbq.ReadSleepRow, stages map[uuid.UUID][]oapi.SleepStage, raws rawRefs) oapi.SleepSession {
	s := srcCols{r.Provider, r.ConnectionID, r.DeviceID, r.DeviceType, r.OriginKey, r.ExternalID, r.DedupeKey,
		r.RawPayloadID, r.NormalizerName, r.NormalizerVersion, r.IngestedAt, r.NormalizedAt,
		r.SupersededAt, r.SupersededBy, r.DeletedAt, r.DeletedByRawID}
	out := oapi.SleepSession{ID: r.ID, StartAt: r.StartAt, EndAt: r.EndAt, TzOffsetMin: intp(r.TzOffsetMin),
		SleepDate: apiDate(r.SleepDate), IsNap: r.IsNap, HasStages: r.HasStages, TotalsBasis: oapi.SleepSessionTotalsBasis(r.TotalsBasis),
		AsleepS: intp(r.AsleepS), DeepS: intp(r.DeepS), LightS: intp(r.LightS), RemS: intp(r.RemS), AwakeS: intp(r.AwakeS),
		LatencyS: intp(r.LatencyS), Source: s.source(), Provenance: s.provenance(raws)}
	if stages != nil {
		st := stages[r.ID]
		if st == nil {
			st = []oapi.SleepStage{}
		}
		out.Stages = &st
	}
	return out
}

func (o *owner) ListSleep(ctx context.Context, req oapi.ListSleepRequestObject) (oapi.ListSleepResponseObject, error) {
	prm := req.Params
	f, err := o.filter(ctx, filterParams{start: prm.Start, end: prm.End, startDate: prm.StartDate, endDate: prm.EndDate,
		providers: prm.Provider, origins: prm.Origin, connections: prm.Connection, devices: prm.Device, include: prm.Include})
	if err != nil {
		return nil, err
	}
	bound := prm
	bound.Limit, bound.Cursor = nil, nil
	p, err := o.newPage("sleep", bound, prm.Limit, prm.Cursor)
	if err != nil {
		return nil, err
	}
	afterKey, afterID, err := p.afterKeyUUID()
	if err != nil {
		return nil, err
	}
	rows, stages, err := o.readSleep(ctx, dbq.ReadSleepParams{UserID: f.user,
		Providers: f.providers, Connections: f.connections, Devices: f.devices, Origins: f.origins,
		StartAt: f.start, EndAt: f.end, StartDate: f.startDate, EndDate: f.endDate,
		WithSuperseded: f.include["superseded"], WithDeleted: f.include["deleted"], AfterKey: afterKey, AfterID: afterID, Lim: p.lim()},
		f.include["stages"])
	if err != nil {
		return nil, err
	}
	rows, more, next := trim(o, p, rows, func(r dbq.ReadSleepRow) (time.Time, string) { return r.StartAt, r.ID.String() })
	raws, err := loadRawRefs(ctx, o, f, rows, func(r dbq.ReadSleepRow) *int64 { return r.RawPayloadID })
	if err != nil {
		return nil, err
	}
	out := oapi.ListSleep200JSONResponse{HasMore: more, NextCursor: next, Sleep: make([]oapi.SleepSession, 0, len(rows))}
	for _, r := range rows {
		out.Sleep = append(out.Sleep, sleepSession(r, stages, raws))
	}
	return out, nil
}

// GetSleep returns any version of one session, with its stages.
func (o *owner) GetSleep(ctx context.Context, req oapi.GetSleepRequestObject) (oapi.GetSleepResponseObject, error) {
	f, err := o.filter(ctx, filterParams{include: req.Params.Include})
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return nil, db.ErrNotFound
	}
	rows, stages, err := o.readSleep(ctx, dbq.ReadSleepParams{UserID: f.user, ID: &id, WithSuperseded: true, WithDeleted: true, Lim: 1}, true)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, db.ErrNotFound
	}
	raws, err := loadRawRefs(ctx, o, f, rows, func(r dbq.ReadSleepRow) *int64 { return r.RawPayloadID })
	if err != nil {
		return nil, err
	}
	return oapi.GetSleep200JSONResponse(sleepSession(rows[0], stages, raws)), nil
}

// readWorkouts returns workout rows and, when segments is set, their segments.
func (o *owner) readWorkouts(ctx context.Context, arg dbq.ReadWorkoutsParams, segments bool) ([]dbq.ReadWorkoutsRow, map[uuid.UUID][]oapi.WorkoutSegment, error) {
	q := o.opts.DB.Q()
	rows, err := q.ReadWorkouts(ctx, arg)
	if err != nil || !segments {
		return rows, nil, db.MapErr(err)
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	sg, err := q.ReadWorkoutSegments(ctx, ids)
	if err != nil {
		return nil, nil, db.MapErr(err)
	}
	by := map[uuid.UUID][]oapi.WorkoutSegment{}
	for _, s := range sg {
		by[s.WorkoutID] = append(by[s.WorkoutID], oapi.WorkoutSegment{Seq: int(s.Seq), Kind: oapi.WorkoutSegmentKind(s.Kind),
			StartAt: s.StartAt, EndAt: s.EndAt, Data: s.Data})
	}
	return rows, by, nil
}

func workout(r dbq.ReadWorkoutsRow, segments map[uuid.UUID][]oapi.WorkoutSegment, raws rawRefs) oapi.Workout {
	s := srcCols{r.Provider, r.ConnectionID, r.DeviceID, r.DeviceType, r.OriginKey, r.ExternalID, r.DedupeKey,
		r.RawPayloadID, r.NormalizerName, r.NormalizerVersion, r.IngestedAt, r.NormalizedAt,
		r.SupersededAt, r.SupersededBy, r.DeletedAt, r.DeletedByRawID}
	out := oapi.Workout{ID: r.ID, StartAt: r.StartAt, EndAt: r.EndAt, TzOffsetMin: intp(r.TzOffsetMin), LocalDate: apiDate(r.LocalDate),
		Sport: r.Sport, ProviderSport: r.ProviderSport, DistanceM: r.DistanceM, EnergyKcal: r.EnergyKcal,
		AvgHrBpm: r.AvgHrBpm, MaxHrBpm: r.MaxHrBpm, Source: s.source(), Provenance: s.provenance(raws)}
	if r.FileBlobSha256 != nil {
		h := hex.EncodeToString(r.FileBlobSha256)
		out.FileSha256 = &h
	}
	if segments != nil {
		sg := segments[r.ID]
		if sg == nil {
			sg = []oapi.WorkoutSegment{}
		}
		out.Segments = &sg
	}
	return out
}

func (o *owner) ListWorkouts(ctx context.Context, req oapi.ListWorkoutsRequestObject) (oapi.ListWorkoutsResponseObject, error) {
	prm := req.Params
	f, err := o.filter(ctx, filterParams{start: prm.Start, end: prm.End, startDate: prm.StartDate, endDate: prm.EndDate,
		providers: prm.Provider, origins: prm.Origin, connections: prm.Connection, devices: prm.Device, include: prm.Include})
	if err != nil {
		return nil, err
	}
	bound := prm
	bound.Limit, bound.Cursor = nil, nil
	p, err := o.newPage("workouts", bound, prm.Limit, prm.Cursor)
	if err != nil {
		return nil, err
	}
	afterKey, afterID, err := p.afterKeyUUID()
	if err != nil {
		return nil, err
	}
	rows, segments, err := o.readWorkouts(ctx, dbq.ReadWorkoutsParams{UserID: f.user,
		Providers: f.providers, Connections: f.connections, Devices: f.devices, Origins: f.origins,
		StartAt: f.start, EndAt: f.end, StartDate: f.startDate, EndDate: f.endDate,
		WithSuperseded: f.include["superseded"], WithDeleted: f.include["deleted"], AfterKey: afterKey, AfterID: afterID, Lim: p.lim()},
		f.include["segments"])
	if err != nil {
		return nil, err
	}
	rows, more, next := trim(o, p, rows, func(r dbq.ReadWorkoutsRow) (time.Time, string) { return r.StartAt, r.ID.String() })
	raws, err := loadRawRefs(ctx, o, f, rows, func(r dbq.ReadWorkoutsRow) *int64 { return r.RawPayloadID })
	if err != nil {
		return nil, err
	}
	out := oapi.ListWorkouts200JSONResponse{HasMore: more, NextCursor: next, Workouts: make([]oapi.Workout, 0, len(rows))}
	for _, r := range rows {
		out.Workouts = append(out.Workouts, workout(r, segments, raws))
	}
	return out, nil
}

// GetWorkout returns any version of one workout, with its segments.
func (o *owner) GetWorkout(ctx context.Context, req oapi.GetWorkoutRequestObject) (oapi.GetWorkoutResponseObject, error) {
	f, err := o.filter(ctx, filterParams{include: req.Params.Include})
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return nil, db.ErrNotFound
	}
	rows, segments, err := o.readWorkouts(ctx, dbq.ReadWorkoutsParams{UserID: f.user, ID: &id, WithSuperseded: true, WithDeleted: true, Lim: 1}, true)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, db.ErrNotFound
	}
	raws, err := loadRawRefs(ctx, o, f, rows, func(r dbq.ReadWorkoutsRow) *int64 { return r.RawPayloadID })
	if err != nil {
		return nil, err
	}
	return oapi.GetWorkout200JSONResponse(workout(rows[0], segments, raws)), nil
}

var provenanceEntities = map[oapi.GetProvenanceParamsEntity]provenance.Entity{
	"measurement": provenance.Measurement, "group": provenance.MeasurementGroup,
	"sleep": provenance.SleepSession, "workout": provenance.Workout,
}

// GetProvenance traces one row. A row of another user answers 404, like a missing one.
func (o *owner) GetProvenance(ctx context.Context, req oapi.GetProvenanceRequestObject) (oapi.GetProvenanceResponseObject, error) {
	if o.opts.DB == nil {
		return nil, problemErr(CodeUnavailable, "the database is not ready")
	}
	entity, ok := provenanceEntities[req.Entity]
	if !ok {
		return nil, db.ErrNotFound
	}
	l, err := provenance.Trace(ctx, o.opts.DB, entity, req.ID)
	if err != nil {
		return nil, err
	}
	var row struct {
		UserID uuid.UUID `json:"user_id"`
	}
	if err := json.Unmarshal(l.Row.Row, &row); err != nil {
		return nil, err
	}
	if row.UserID != auth.PrincipalFrom(ctx).UserID {
		return nil, db.ErrNotFound
	}
	out := oapi.GetProvenance200JSONResponse{Entity: oapi.ProvenanceEntity(req.Entity), Row: provVersion(l.Row),
		Earlier: make([]oapi.ProvenanceVersion, 0, len(l.Earlier)), Later: make([]oapi.ProvenanceVersion, 0, len(l.Later))}
	for _, v := range l.Earlier {
		out.Earlier = append(out.Earlier, provVersion(v))
	}
	for _, v := range l.Later {
		out.Later = append(out.Later, provVersion(v))
	}
	return out, nil
}

func provVersion(v provenance.Version) oapi.ProvenanceVersion {
	out := oapi.ProvenanceVersion{ID: v.ID, SupersededBy: nonEmpty(v.SupersededBy), Record: v.Row, Provider: v.Provider,
		ConnectionID: ingest.FormatConnectionID(v.ConnectionID), ConnectionMode: v.ConnectionMode,
		Normalizer: oapi.ProvenanceNormalizer{Name: v.Normalizer.Name, Version: int(v.Normalizer.Version), GitSha: v.Normalizer.GitSHA},
		FetchedAt:  v.FetchedAt, IngestedAt: v.IngestedAt, NormalizedAt: v.NormalizedAt, CorrectedAt: v.CorrectedAt,
		SupersededAt: v.SupersededAt, DeletedAt: v.DeletedAt}
	if c := v.Client; c != nil {
		out.Client = &oapi.ProvenanceClient{ID: c.ID, Kind: c.Kind, Name: c.Name}
	}
	if b := v.Batch; b != nil {
		out.Batch = &oapi.ProvenanceBatch{ID: b.ID, SourceKind: b.SourceKind, MigrationSource: nonEmpty(b.MigrationSource), ReceivedAt: b.ReceivedAt}
	}
	if r := v.Raw; r != nil {
		meta := r.RequestMeta
		if len(meta) == 0 {
			meta = json.RawMessage(`{}`)
		}
		out.Raw = &oapi.ProvenanceRaw{ID: strconv.FormatInt(r.ID, 10), Stream: r.Stream, ExternalKey: r.ExternalKey,
			Version: int(r.Version), ContentSha256: r.ContentSHA256, ContentType: r.ContentType, SizeBytes: r.SizeBytes,
			FetchedAt: r.FetchedAt, StoredAt: r.StoredAt, RequestMeta: meta, ShapeFingerprint: r.ShapeFingerprint, Status: r.Status}
	}
	if d := v.DeletedBy; d != nil {
		out.DeletedBy = &oapi.ProvenanceDeletion{RawID: strconv.FormatInt(d.RawID, 10), FetchedAt: d.FetchedAt}
	}
	return out
}

// srcCols are the source and version columns every list query ends with, in that order.
type srcCols struct {
	provider                       string
	connection                     uuid.UUID
	device                         *uuid.UUID
	deviceType, origin, externalID *string
	dedupeKey                      []byte
	raw                            *int64
	normalizer                     string
	normalizerVersion              int32
	ingestedAt, normalizedAt       time.Time
	supersededAt                   *time.Time
	supersededBy                   string // empty on the current version
	deletedAt                      *time.Time
	deletedBy                      *int64
}

func (s srcCols) source() oapi.SourceRef {
	out := oapi.SourceRef{Provider: s.provider, ConnectionID: ingest.FormatConnectionID(s.connection),
		DeviceType: s.deviceType, Origin: s.origin, ExternalID: s.externalID, DedupeKey: hex.EncodeToString(s.dedupeKey)}
	if s.device != nil {
		id := formatDeviceID(*s.device)
		out.Device = &id
	}
	return out
}

func (s srcCols) provenance(raws rawRefs) oapi.RecordProvenance {
	out := oapi.RecordProvenance{RawPayloadID: idp(s.raw), Normalizer: s.normalizer + "@" + strconv.Itoa(int(s.normalizerVersion)),
		IngestedAt: s.ingestedAt, NormalizedAt: s.normalizedAt, SupersededAt: s.supersededAt, SupersededBy: nonEmpty(s.supersededBy),
		DeletedAt: s.deletedAt, DeletedByRawID: idp(s.deletedBy)}
	if s.raw != nil {
		out.Raw = raws[*s.raw]
	}
	return out
}

// rawRefs maps raw payload ids to their fetch metadata (include=provenance).
type rawRefs map[int64]*oapi.RawRef

// loadRawRefs loads the raw metadata of rows when include=provenance; otherwise it returns nil.
func loadRawRefs[R any](ctx context.Context, o *owner, f sourceFilter, rows []R, raw func(R) *int64) (rawRefs, error) {
	if !f.include["provenance"] {
		return nil, nil
	}
	var ids []int64
	for _, r := range rows {
		if id := raw(r); id != nil {
			ids = append(ids, *id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rs, err := o.opts.DB.Q().ReadRawRefs(ctx, dbq.ReadRawRefsParams{UserID: f.user, Ids: ids})
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := make(rawRefs, len(rs))
	for _, r := range rs {
		out[r.ID] = &oapi.RawRef{Stream: r.Stream, ExternalKey: r.ExternalKey, Version: int(r.Version),
			FetchedAt: r.FetchedAt, BatchID: r.BatchID, SourceKind: r.SourceKind}
	}
	return out, nil
}

var deviceIDRe = regexp.MustCompile(`^dev_[0-9a-f]{32}$`)

// parseDeviceID returns the UUID of a dev_<32 hex> identifier.
func parseDeviceID(s string) (uuid.UUID, error) {
	if !deviceIDRe.MatchString(s) {
		return uuid.Nil, errors.New("malformed device id")
	}
	return uuid.Parse(strings.TrimPrefix(s, "dev_"))
}

func formatDeviceID(id uuid.UUID) string { return "dev_" + hex.EncodeToString(id[:]) }

func apiDate(t time.Time) openapi_types.Date { return openapi_types.Date{Time: t} }

func intp[T int16 | int32](p *T) *int {
	if p == nil {
		return nil
	}
	n := int(*p)
	return &n
}

func idp(p *int64) *string {
	if p == nil {
		return nil
	}
	s := strconv.FormatInt(*p, 10)
	return &s
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ptrVal[T any](p *T) (v T) {
	if p != nil {
		v = *p
	}
	return v
}
