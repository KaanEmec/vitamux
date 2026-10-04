package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/jobs"
	"github.com/KaanEmec/vitamux/internal/lifecycle"
)

// Connections (J10.4; docs/architecture/connectors.md): list and health, push connections,
// pause/resume, delete with or without data, manual sync, runs, streams and backfills. OAuth
// begin and the callback are in oauth.go. Every mutation is audited.
func (rt *router) connectionRoutes() {
	read, write := scope(auth.ReadConfig), scope(auth.WriteConfig)
	rt.handle("GET /api/v1/providers", read, rt.ops.ListProviders)
	rt.handle("GET /api/v1/connections", read, rt.ops.ListConnections)
	rt.handle("POST /api/v1/connections", write, rt.ops.CreateConnection)
	rt.handle("GET /api/v1/connections/{id}", read, rt.ops.GetConnection)
	rt.handle("PATCH /api/v1/connections/{id}", write, rt.ops.UpdateConnection)
	rt.handle("DELETE /api/v1/connections/{id}", write, rt.ops.DeleteConnection)
	rt.handle("POST /api/v1/connections/{id}/sync", write, rt.ops.SyncConnection)
	rt.handle("GET /api/v1/connections/{id}/runs", read, rt.ops.ListConnectionRuns)
	rt.handle("GET /api/v1/connections/{id}/streams", read, rt.ops.ListConnectionStreams)
	rt.handle("POST /api/v1/connections/{id}/streams/{stream}/reset-cursor", write, rt.ops.ResetStreamCursor)
	rt.handle("GET /api/v1/connections/{id}/backfills", read, rt.ops.ListBackfills)
	rt.handle("POST /api/v1/connections/{id}/backfills", write, rt.ops.CreateBackfill)
	rt.handle("GET /api/v1/connections/{id}/backfills/{backfill_id}", read, rt.ops.GetBackfill)
	rt.handle("POST /api/v1/connections/{id}/backfills/{backfill_id}/retry", write, rt.ops.RetryBackfill)
	rt.handle("POST /api/v1/connections/{id}/backfills/{backfill_id}/cancel", write, rt.ops.CancelBackfill)
}

var errNoConnection = problemErr(CodeNotFound, "no such connection")

// managedProviders get their connection from Vitamux itself, never from POST /connections.
var managedProviders = []string{"manual", "lab_document"}

// connection loads one of the caller's connections by its conn_ id.
func (o *owner) connection(ctx context.Context, id string) (dbq.ListOwnerConnectionsRow, error) {
	d, err := o.ownerDB()
	if err != nil {
		return dbq.ListOwnerConnectionsRow{}, err
	}
	cid, err := ingest.ParseConnectionID(id)
	if err != nil {
		return dbq.ListOwnerConnectionsRow{}, errNoConnection
	}
	rows, err := d.Q().ListOwnerConnections(ctx, dbq.ListOwnerConnectionsParams{UserID: auth.PrincipalFrom(ctx).UserID, ID: &cid})
	if err != nil {
		return dbq.ListOwnerConnectionsRow{}, db.MapErr(err)
	}
	if len(rows) == 0 {
		return dbq.ListOwnerConnectionsRow{}, errNoConnection
	}
	return rows[0], nil
}

func (o *owner) runtime() (*connectors.Runtime, error) {
	if o.opts.Connectors == nil {
		return nil, problemErr(CodeUnavailable, "connections are unavailable: the master key or data directory is missing")
	}
	return o.opts.Connectors, nil
}

// healthInput is what DeriveHealth needs from a connection; interval is its shortest enabled
// incremental schedule (0 = unscheduled).
func healthInput(c dbq.ListOwnerConnectionsRow, interval time.Duration) connectors.HealthInput {
	return connectors.HealthInput{Now: time.Now(), ConnectionStatus: c.Status, LastSuccessAt: ptrVal(c.LastSuccessAt),
		LastErrorClass: ptrVal(c.LastErrorClass), ConsecutiveFailures: int(c.ConsecutiveFailures),
		BlockedUntil: ptrVal(c.BlockedUntil), Interval: interval}
}

func incrementalInterval(scheds []dbq.Schedule, stream string) time.Duration {
	var d time.Duration
	for _, s := range scheds {
		if s.Enabled && s.Mode == jobs.ModeIncremental && (stream == "" || s.Stream == stream) && (d == 0 || s.RunInterval < d) {
			d = s.RunInterval
		}
	}
	return d
}

func (o *owner) connectionBody(c dbq.ListOwnerConnectionsRow, scheds []dbq.Schedule) oapi.Connection {
	h := connectors.DeriveHealth(healthInput(c, incrementalInterval(scheds, "")))
	out := oapi.Connection{ID: ingest.FormatConnectionID(c.ID), Provider: c.Provider, Mode: oapi.ConnectionMode(c.Mode),
		Status: oapi.ConnectionStatus(c.Status), Health: oapi.Health(h.Health), HealthReason: optString(h.Reason),
		LastSuccessAt: c.LastSuccessAt, LastErrorClass: c.LastErrorClass, ConsecutiveFailures: int(c.ConsecutiveFailures),
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
	if c.Upstream != nil {
		var u oapi.Upstream
		if json.Unmarshal(c.Upstream, &u) == nil {
			out.Upstream = &u
		}
	}
	if o.opts.Connectors != nil {
		if d, ok := o.opts.Connectors.Describe(c.Provider); ok {
			out.Official = &d.Official
		}
	}
	return out
}

// ListProviders lists the registered connectors, in-process and sidecars.
func (o *owner) ListProviders(context.Context, oapi.ListProvidersRequestObject) (oapi.ListProvidersResponseObject, error) {
	rt, err := o.runtime()
	if err != nil {
		return nil, err
	}
	ds := rt.Providers()
	out := oapi.ListProviders200JSONResponse{Providers: make([]oapi.Provider, len(ds))}
	for i, d := range ds {
		p := oapi.Provider{Code: d.Provider, Name: cmp.Or(d.Name, d.Provider), Official: d.Official, Remote: d.Remote, Available: d.Available()}
		if d.AuthKind != "" {
			k := oapi.ProviderAuthKind(d.AuthKind)
			p.AuthKind = &k
		}
		if u := d.Upstream; u != nil {
			p.Upstream = &oapi.Upstream{Package: u.Package, Version: u.Version, SourceURL: u.SourceURL}
		}
		out.Providers[i] = p
	}
	return out, nil
}

// schedulesOf returns the caller's schedules, of one connection when id is set.
func (o *owner) schedulesOf(ctx context.Context, id *uuid.UUID) ([]dbq.Schedule, error) {
	rows, err := o.opts.DB.Q().ListOwnerSchedules(ctx, dbq.ListOwnerSchedulesParams{UserID: auth.PrincipalFrom(ctx).UserID, ConnectionID: id})
	return rows, db.MapErr(err)
}

func (o *owner) ListConnections(ctx context.Context, _ oapi.ListConnectionsRequestObject) (oapi.ListConnectionsResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	rows, err := d.Q().ListOwnerConnections(ctx, dbq.ListOwnerConnectionsParams{UserID: auth.PrincipalFrom(ctx).UserID})
	if err != nil {
		return nil, db.MapErr(err)
	}
	scheds, err := o.schedulesOf(ctx, nil)
	if err != nil {
		return nil, err
	}
	byConn := map[uuid.UUID][]dbq.Schedule{}
	for _, s := range scheds {
		byConn[s.ConnectionID] = append(byConn[s.ConnectionID], s)
	}
	out := oapi.ListConnections200JSONResponse{Connections: make([]oapi.Connection, len(rows))}
	for i, c := range rows {
		out.Connections[i] = o.connectionBody(c, byConn[c.ID])
	}
	return out, nil
}

// getConnection answers one connection with its health.
func (o *owner) getConnection(ctx context.Context, id string) (oapi.Connection, error) {
	c, err := o.connection(ctx, id)
	if err != nil {
		return oapi.Connection{}, err
	}
	scheds, err := o.schedulesOf(ctx, &c.ID)
	if err != nil {
		return oapi.Connection{}, err
	}
	return o.connectionBody(c, scheds), nil
}

func (o *owner) GetConnection(ctx context.Context, req oapi.GetConnectionRequestObject) (oapi.GetConnectionResponseObject, error) {
	c, err := o.getConnection(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	return oapi.GetConnection200JSONResponse(c), nil
}

// CreateConnection adds a push connection for a provider without a server-side connector.
func (o *owner) CreateConnection(ctx context.Context, req oapi.CreateConnectionRequestObject) (oapi.CreateConnectionResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	provider := req.Body.Provider
	ok, err := d.Q().ProviderExists(ctx, provider)
	switch {
	case err != nil:
		return nil, db.MapErr(err)
	case !ok:
		return nil, problemErr(CodeValidationFailed, "unknown provider", FieldError{Pointer: "/provider", Detail: "not a known provider code"})
	case slices.Contains(managedProviders, provider):
		return nil, problemErr(CodeValidationFailed, "Vitamux manages this provider's connection", FieldError{Pointer: "/provider", Detail: "cannot be created"})
	case o.opts.Connectors != nil && o.opts.Connectors.HasProvider(provider):
		return nil, problemErr(CodeValidationFailed, "connect this provider with POST /api/v1/providers/"+provider+"/auth/begin", FieldError{Pointer: "/provider", Detail: "has a server-side connector"})
	}
	p := auth.PrincipalFrom(ctx)
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	err = d.Tx(ctx, func(q *dbq.Queries) error {
		if _, err := q.InsertPushConnection(ctx, dbq.InsertPushConnectionParams{ID: id, UserID: p.UserID, Provider: provider}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: "connection.create",
			TargetType: "connection", TargetID: id.String(), Detail: map[string]any{"provider": provider, "mode": "push"}})
	})
	if err != nil {
		return nil, err
	}
	c, err := o.getConnection(ctx, ingest.FormatConnectionID(id))
	if err != nil {
		return nil, err
	}
	return oapi.CreateConnection201JSONResponse(c), nil
}

// UpdateConnection pauses or resumes a connection. Paused connections are not scheduled.
func (o *owner) UpdateConnection(ctx context.Context, req oapi.UpdateConnectionRequestObject) (oapi.UpdateConnectionResponseObject, error) {
	c, err := o.connection(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if req.Body.Status != nil {
		to, from, action := "paused", []string{"active", "degraded"}, "connection.pause"
		if *req.Body.Status == oapi.ConnectionPatchStatusActive {
			to, from, action = "active", []string{"paused"}, "connection.resume"
		}
		if c.Status != to {
			p := auth.PrincipalFrom(ctx)
			err := o.opts.DB.Tx(ctx, func(q *dbq.Queries) error {
				n, err := q.SetOwnerConnectionStatus(ctx, dbq.SetOwnerConnectionStatusParams{Status: to, ID: c.ID, UserID: p.UserID, FromStatuses: from})
				if err != nil {
					return err
				}
				if n == 0 {
					return problemErr(CodeConflict, "the connection is "+c.Status+": only "+strings.Join(from, " or ")+" connections can be set "+to)
				}
				return audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: action,
					TargetType: "connection", TargetID: c.ID.String(), Detail: map[string]any{"from": c.Status, "to": to}})
			})
			if err != nil {
				return nil, err
			}
		}
	}
	out, err := o.getConnection(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	return oapi.UpdateConnection200JSONResponse(out), nil
}

// DeleteConnection disconnects (data=keep, see connectors.Runtime.Disconnect) or deletes the
// connection with everything it brought in (data=delete).
func (o *owner) DeleteConnection(ctx context.Context, req oapi.DeleteConnectionRequestObject) (oapi.DeleteConnectionResponseObject, error) {
	c, err := o.connection(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	p := auth.PrincipalFrom(ctx)
	switch req.Params.Data {
	case oapi.DeleteConnectionParamsDataKeep:
		rt, err := o.runtime()
		if err != nil {
			return nil, err
		}
		if err := rt.Disconnect(ctx, p.UserID, c.ID, p.Actor()); err != nil {
			return nil, err
		}
	case oapi.DeleteConnectionParamsDataDelete:
		if err := o.deleteConnectionData(ctx, c); err != nil {
			return nil, err
		}
	default:
		return nil, problemErr(CodeValidationFailed, "invalid data mode", FieldError{Pointer: "/data", Detail: "must be keep or delete"})
	}
	return oapi.DeleteConnection204Response{}, nil
}

var errJobRunning = problemErr(CodeConflict, "one of the connection's jobs is running; try again when it has finished")

// lockIdle locks the connection's queued jobs, so no worker claims one before commit, and
// fails with 409 while one runs.
func lockIdle(ctx context.Context, q *dbq.Queries, id uuid.UUID) error {
	statuses, err := q.LockConnectionJobs(ctx, &id)
	if err != nil {
		return err
	}
	if slices.Contains(statuses, "running") {
		return errJobRunning
	}
	return nil
}

// deleteConnectionData removes the connection with everything it brought in, in one audited
// transaction (lifecycle.DeleteConnection).
func (o *owner) deleteConnectionData(ctx context.Context, c dbq.ListOwnerConnectionsRow) error {
	p := auth.PrincipalFrom(ctx)
	return o.opts.DB.Tx(ctx, func(q *dbq.Queries) error {
		if err := lockIdle(ctx, q, c.ID); err != nil {
			return err
		}
		counts, err := lifecycle.DeleteConnection(ctx, q, p.UserID, c.ID)
		if err != nil {
			return err
		}
		detail := map[string]any{"data": "delete", "provider": c.Provider}
		for k, n := range counts {
			detail[k] = n
		}
		return audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: "connection.deleted",
			TargetType: "connection", TargetID: c.ID.String(), Detail: detail})
	})
}

func jobBody(j dbq.Job) oapi.Job {
	out := oapi.Job{ID: j.ID, Kind: j.Kind, Status: oapi.JobStatus(j.Status), Priority: int(j.Priority),
		Attempts: int(j.Attempts), MaxAttempts: int(j.MaxAttempts), Payload: j.Payload, RunAt: j.RunAt,
		CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt}
	if j.ConnectionID != nil {
		id := ingest.FormatConnectionID(*j.ConnectionID)
		out.ConnectionID = &id
	}
	return out
}

// SyncConnection queues a manual sync per stream; a pending one is answered instead (Runtime.SyncNow).
func (o *owner) SyncConnection(ctx context.Context, req oapi.SyncConnectionRequestObject) (oapi.SyncConnectionResponseObject, error) {
	c, err := o.connection(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	rt, err := o.runtime()
	if err != nil {
		return nil, err
	}
	ids, err := rt.SyncNow(ctx, c.ID)
	switch {
	case errors.Is(err, connectors.ErrReauthRequired):
		return nil, problemErr(CodeReauthRequired, "reconnect the account first")
	case errors.Is(err, connectors.ErrNotSyncable):
		return nil, problemErr(CodeConflict, err.Error())
	case err != nil:
		return nil, err
	}
	p := auth.PrincipalFrom(ctx)
	var rows []dbq.Job
	err = o.opts.DB.Tx(ctx, func(q *dbq.Queries) error {
		var err error
		if rows, err = q.GetJobsByID(ctx, ids); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: "connection.sync",
			TargetType: "connection", TargetID: c.ID.String(), Detail: map[string]any{"jobs": len(ids)}})
	})
	if err != nil {
		return nil, err
	}
	out := oapi.SyncConnection202JSONResponse{Jobs: make([]oapi.Job, len(rows))}
	for i, j := range rows {
		out.Jobs[i] = jobBody(j)
	}
	return out, nil
}

func (o *owner) ListConnectionRuns(ctx context.Context, req oapi.ListConnectionRunsRequestObject) (oapi.ListConnectionRunsResponseObject, error) {
	c, err := o.connection(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	bound := req.Params
	bound.Limit, bound.Cursor = nil, nil
	p, err := o.newPage("runs:"+req.ID, bound, req.Params.Limit, req.Params.Cursor)
	if err != nil {
		return nil, err
	}
	var after *int64
	if p.after != nil {
		n, err := p.afterInt()
		if err != nil {
			return nil, err
		}
		after = &n
	}
	rows, err := o.opts.DB.Q().ListConnectionRuns(ctx, dbq.ListConnectionRunsParams{ConnectionID: &c.ID, AfterID: after, Lim: p.lim()})
	if err != nil {
		return nil, db.MapErr(err)
	}
	rows, more, next := trim(o, p, rows, func(r dbq.ListConnectionRunsRow) (time.Time, string) {
		return r.StartedAt, strconv.FormatInt(r.ID, 10)
	})
	out := oapi.ListConnectionRuns200JSONResponse{HasMore: more, NextCursor: next, Runs: make([]oapi.Run, len(rows))}
	for i, r := range rows {
		out.Runs[i] = oapi.Run{ID: strconv.FormatInt(r.ID, 10), JobID: r.JobID, Kind: r.Kind, Attempt: int(r.Attempt),
			StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Outcome: r.Outcome, ErrorClass: r.ErrorClass,
			ErrorMessage: r.ErrorMessage, Stats: r.Stats}
	}
	return out, nil
}

// streams lists the connector's streams, then any other stream with a cursor or a schedule
// (push clients report theirs in heartbeats).
func (o *owner) streams(ctx context.Context, c dbq.ListOwnerConnectionsRow) ([]oapi.Stream, error) {
	cursors, err := o.opts.DB.Q().ListStreamCursors(ctx, c.ID)
	if err != nil {
		return nil, db.MapErr(err)
	}
	scheds, err := o.schedulesOf(ctx, &c.ID)
	if err != nil {
		return nil, err
	}
	var names []string
	add := func(n string) {
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	if o.opts.Connectors != nil {
		if d, ok := o.opts.Connectors.Describe(c.Provider); ok {
			for _, s := range d.Streams {
				add(s.Name)
			}
		}
	}
	byName := map[string]dbq.ListStreamCursorsRow{}
	for _, cur := range cursors {
		add(cur.Stream)
		byName[cur.Stream] = cur
	}
	for _, s := range scheds {
		add(s.Stream)
	}
	out := make([]oapi.Stream, 0, len(names))
	for _, n := range names {
		cur, ok := byName[n]
		st := oapi.Stream{Name: n, Status: oapi.StreamStatusOk, Schedules: []oapi.Schedule{}}
		in := healthInput(c, incrementalInterval(scheds, n))
		if ok {
			st.Status, st.StatusReason, st.HasCursor, st.HighWatermark, st.UpdatedAt = oapi.StreamStatus(cur.Status), cur.StatusReason, cur.HasCursor, cur.HighWatermark, &cur.UpdatedAt
			in.StreamStatus, in.StreamReason = cur.Status, ptrVal(cur.StatusReason)
		}
		h := connectors.DeriveHealth(in)
		st.Health, st.HealthReason = oapi.Health(h.Health), optString(h.Reason)
		for _, s := range scheds {
			if s.Stream == n {
				st.Schedules = append(st.Schedules, scheduleBody(s))
			}
		}
		out = append(out, st)
	}
	return out, nil
}

func (o *owner) ListConnectionStreams(ctx context.Context, req oapi.ListConnectionStreamsRequestObject) (oapi.ListConnectionStreamsResponseObject, error) {
	c, err := o.connection(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	streams, err := o.streams(ctx, c)
	if err != nil {
		return nil, err
	}
	return oapi.ListConnectionStreams200JSONResponse{Streams: streams}, nil
}

// ResetStreamCursor clears a stream's cursor and high watermark; the data stays.
func (o *owner) ResetStreamCursor(ctx context.Context, req oapi.ResetStreamCursorRequestObject) (oapi.ResetStreamCursorResponseObject, error) {
	c, err := o.connection(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	streams, err := o.streams(ctx, c)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(streams, func(s oapi.Stream) bool { return s.Name == req.Stream })
	switch {
	case i < 0:
		return nil, problemErr(CodeNotFound, "no such stream")
	case c.Mode != "in_process":
		return nil, problemErr(CodeConflict, "push clients keep their own checkpoint; reset it on the client")
	}
	p := auth.PrincipalFrom(ctx)
	err = o.opts.DB.Tx(ctx, func(q *dbq.Queries) error {
		if err := lockIdle(ctx, q, c.ID); err != nil {
			return err
		}
		if err := q.ResetStreamCursor(ctx, dbq.ResetStreamCursorParams{ConnectionID: c.ID, Stream: req.Stream}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: "stream.cursor_reset",
			TargetType: "connection", TargetID: c.ID.String(), Detail: map[string]any{"stream": req.Stream}})
	})
	if err != nil {
		return nil, err
	}
	if streams, err = o.streams(ctx, c); err != nil {
		return nil, err
	}
	return oapi.ResetStreamCursor200JSONResponse(streams[slices.IndexFunc(streams, func(s oapi.Stream) bool { return s.Name == req.Stream })]), nil
}

func backfillBody(b connectors.Backfill) oapi.Backfill {
	out := oapi.Backfill{ID: b.ID, ConnectionID: ingest.FormatConnectionID(b.ConnectionID), Stream: b.Stream, Start: b.From, End: b.To,
		Status: oapi.BackfillStatus(b.Status), CreatedAt: b.CreatedAt, FinishedAt: b.FinishedAt}
	out.UnitCounts.Pending, out.UnitCounts.Running, out.UnitCounts.Done, out.UnitCounts.Failed = b.Pending, b.Running, b.Done, b.Failed
	return out
}

// backfill finds a backfill of the caller's connection.
func (o *owner) backfill(ctx context.Context, conn string, id uuid.UUID) (*connectors.Runtime, dbq.ListOwnerConnectionsRow, connectors.Backfill, error) {
	c, err := o.connection(ctx, conn)
	if err != nil {
		return nil, c, connectors.Backfill{}, err
	}
	rt, err := o.runtime()
	if err != nil {
		return nil, c, connectors.Backfill{}, err
	}
	bs, err := rt.ListBackfills(ctx, c.ID)
	if err != nil {
		return nil, c, connectors.Backfill{}, db.MapErr(err)
	}
	i := slices.IndexFunc(bs, func(b connectors.Backfill) bool { return b.ID == id })
	if i < 0 {
		return nil, c, connectors.Backfill{}, problemErr(CodeNotFound, "no such backfill")
	}
	return rt, c, bs[i], nil
}

// auditBackfill records a backfill change after the runtime made it.
func (o *owner) auditBackfill(ctx context.Context, action string, b connectors.Backfill, detail map[string]any) error {
	p := auth.PrincipalFrom(ctx)
	detail["backfill_id"], detail["stream"] = b.ID.String(), b.Stream
	return o.opts.DB.Tx(ctx, func(q *dbq.Queries) error {
		return audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: action,
			TargetType: "connection", TargetID: b.ConnectionID.String(), Detail: detail})
	})
}

func (o *owner) ListBackfills(ctx context.Context, req oapi.ListBackfillsRequestObject) (oapi.ListBackfillsResponseObject, error) {
	c, err := o.connection(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	rt, err := o.runtime()
	if err != nil {
		return nil, err
	}
	bs, err := rt.ListBackfills(ctx, c.ID)
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := oapi.ListBackfills200JSONResponse{Backfills: make([]oapi.Backfill, len(bs))}
	for i, b := range bs {
		out.Backfills[i] = backfillBody(b)
	}
	return out, nil
}

func (o *owner) CreateBackfill(ctx context.Context, req oapi.CreateBackfillRequestObject) (oapi.CreateBackfillResponseObject, error) {
	c, err := o.connection(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	rt, err := o.runtime()
	if err != nil {
		return nil, err
	}
	spec := connectors.BackfillSpec{ConnectionID: c.ID, Stream: req.Body.Stream, From: req.Body.Start, To: ptrVal(req.Body.End)}
	id, err := rt.CreateBackfill(ctx, spec)
	if errors.Is(err, connectors.ErrInvalidBackfill) {
		return nil, problemErr(CodeValidationFailed, strings.TrimPrefix(err.Error(), connectors.ErrInvalidBackfill.Error()+": "))
	}
	if err != nil {
		return nil, err
	}
	_, _, b, err := o.backfill(ctx, req.ID, id)
	if err != nil {
		return nil, err
	}
	if err := o.auditBackfill(ctx, "backfill.create", b, map[string]any{"from": b.From.Format(time.RFC3339), "to": b.To.Format(time.RFC3339)}); err != nil {
		return nil, err
	}
	return oapi.CreateBackfill202JSONResponse(backfillBody(b)), nil
}

func (o *owner) GetBackfill(ctx context.Context, req oapi.GetBackfillRequestObject) (oapi.GetBackfillResponseObject, error) {
	rt, _, b, err := o.backfill(ctx, req.ID, req.BackfillID)
	if err != nil {
		return nil, err
	}
	units, err := rt.BackfillUnits(ctx, b.ID, "")
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := backfillBody(b)
	list := make([]oapi.BackfillUnit, len(units))
	for i, u := range units {
		list[i] = oapi.BackfillUnit{Start: u.From, End: u.To, Status: oapi.BackfillUnitStatus(u.Status), Attempts: u.Attempts,
			ErrorClass: optString(u.ErrorClass), UpdatedAt: u.UpdatedAt}
	}
	out.Units = &list
	return oapi.GetBackfill200JSONResponse(out), nil
}

func (o *owner) RetryBackfill(ctx context.Context, req oapi.RetryBackfillRequestObject) (oapi.RetryBackfillResponseObject, error) {
	rt, _, b, err := o.backfill(ctx, req.ID, req.BackfillID)
	if err != nil {
		return nil, err
	}
	var unit *time.Time
	if req.Body != nil {
		unit = req.Body.UnitStart
	}
	n, err := rt.RetryBackfill(ctx, b.ID, unit)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, problemErr(CodeConflict, "no failed or unfinished unit to retry")
	}
	if err := o.auditBackfill(ctx, "backfill.retry", b, map[string]any{"units": n}); err != nil {
		return nil, err
	}
	if _, _, b, err = o.backfill(ctx, req.ID, b.ID); err != nil {
		return nil, err
	}
	return oapi.RetryBackfill200JSONResponse(backfillBody(b)), nil
}

func (o *owner) CancelBackfill(ctx context.Context, req oapi.CancelBackfillRequestObject) (oapi.CancelBackfillResponseObject, error) {
	rt, _, b, err := o.backfill(ctx, req.ID, req.BackfillID)
	if err != nil {
		return nil, err
	}
	if err := rt.CancelBackfill(ctx, b.ID); errors.Is(err, db.ErrNotFound) {
		return nil, problemErr(CodeConflict, "the backfill is "+b.Status+"; only running or failed backfills can be cancelled")
	} else if err != nil {
		return nil, err
	}
	if err := o.auditBackfill(ctx, "backfill.cancel", b, map[string]any{}); err != nil {
		return nil, err
	}
	if _, _, b, err = o.backfill(ctx, req.ID, b.ID); err != nil {
		return nil, err
	}
	return oapi.CancelBackfill200JSONResponse(backfillBody(b)), nil
}
