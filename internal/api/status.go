package api

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/backup"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/version"
)

// Coverage and system status (J10.5).
func (rt *router) statusRoutes() {
	rt.handle("GET /api/v1/coverage", scope(auth.ReadHealth), rt.ops.GetCoverage)
	rt.handle("GET /api/v1/system/status", scope(auth.ReadConfig), rt.ops.GetSystemStatus)
}

const (
	failingJobsWindow = 7 * 24 * time.Hour
	failingJobsMax    = 50
)

// GetSystemStatus assembles versions, sizes, the last backup, the connections that are not
// healthy and the jobs that died in the last week. Nothing in it comes from provider data.
func (o *owner) GetSystemStatus(ctx context.Context, _ oapi.GetSystemStatusRequestObject) (oapi.GetSystemStatusResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	userID := auth.PrincipalFrom(ctx).UserID
	schema, err := d.SchemaVersion(ctx)
	if err != nil {
		return nil, db.MapErr(err)
	}
	sizes, err := d.Q().InstanceSizes(ctx)
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := oapi.GetSystemStatus200JSONResponse{
		Versions: oapi.StatusVersions{App: version.Version, Commit: version.Commit,
			Schema: strconv.FormatInt(schema, 10), Postgres: sizes.PostgresVersion},
		DatabaseSizeBytes:   sizes.DatabaseBytes,
		BlobSizeBytes:       sizes.BlobBytes,
		DegradedConnections: []oapi.StatusConnection{},
		FailingJobs:         []oapi.StatusJob{},
	}
	if o.opts.BackupDir != "" {
		if m, ok, err := backup.Latest(o.opts.BackupDir); err != nil {
			o.log.Warn("status: read backups", "err", err) // the page still answers
		} else if ok {
			out.LastBackupAt = &m.CreatedAt
		}
	}

	conns, err := d.Q().ListOwnerConnections(ctx, dbq.ListOwnerConnectionsParams{UserID: userID})
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
	for _, c := range conns {
		h, stream := connectors.DeriveHealth(healthInput(c, incrementalInterval(byConn[c.ID], ""))), ""
		if h.Health == connectors.HealthOK { // a degraded stream (schema drift, say) makes the connection worth a look
			streams, err := o.streams(ctx, c)
			if err != nil {
				return nil, err
			}
			for _, s := range streams {
				if s.Health != oapi.HealthOk && s.Health != oapi.HealthPaused && s.Health != oapi.HealthDisabled {
					h, stream = connectors.HealthState{Health: connectors.Health(s.Health), Reason: ptrVal(s.HealthReason)}, s.Name
					break
				}
			}
		}
		if h.Health == connectors.HealthOK || h.Health == connectors.HealthPaused || h.Health == connectors.HealthDisabled {
			continue
		}
		out.DegradedConnections = append(out.DegradedConnections, oapi.StatusConnection{ID: ingest.FormatConnectionID(c.ID),
			Provider: c.Provider, Health: oapi.Health(h.Health), HealthReason: optString(h.Reason), Stream: optString(stream),
			LastSuccessAt: c.LastSuccessAt})
	}

	since := time.Now().Add(-failingJobsWindow)
	dead, err := d.Q().ListDeadJobsSince(ctx, dbq.ListDeadJobsSinceParams{UserID: userID, Since: &since, Lim: failingJobsMax})
	if err != nil {
		return nil, db.MapErr(err)
	}
	for _, j := range dead {
		sj := oapi.StatusJob{ID: j.ID, Kind: j.Kind, Attempts: int(j.Attempts), FinishedAt: j.FinishedAt, ErrorClass: j.ErrorClass}
		if j.ConnectionID != nil {
			id := ingest.FormatConnectionID(*j.ConnectionID)
			sj.ConnectionID = &id
		}
		out.FailingJobs = append(out.FailingJobs, sj)
	}
	return out, nil
}
