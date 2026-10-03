package api

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/connectors/withings"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/jobs"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// Schedules, jobs, timezone periods and settings (J10.4). Every mutation is audited; the
// timezone period service audits and enqueues the local-date recompute itself.
func (rt *router) configRoutes() {
	read, write := scope(auth.ReadConfig), scope(auth.WriteConfig)
	rt.handle("GET /api/v1/schedules", read, rt.ops.ListSchedules)
	rt.handle("PATCH /api/v1/schedules/{id}", write, rt.ops.UpdateSchedule)
	rt.handle("GET /api/v1/jobs", read, rt.ops.ListJobs)
	rt.handle("GET /api/v1/timezone-periods", read, rt.ops.ListTimezonePeriods)
	rt.handle("POST /api/v1/timezone-periods", write, rt.ops.CreateTimezonePeriod)
	rt.handle("PATCH /api/v1/timezone-periods/{id}", write, rt.ops.UpdateTimezonePeriod)
	rt.handle("DELETE /api/v1/timezone-periods/{id}", write, rt.ops.DeleteTimezonePeriod)
	rt.handle("GET /api/v1/settings", read, rt.ops.GetSettings)
	rt.handle("PATCH /api/v1/settings", write, rt.ops.UpdateSettings)
}

func scheduleBody(s dbq.Schedule) oapi.Schedule {
	return oapi.Schedule{ID: s.ID, ConnectionID: ingest.FormatConnectionID(s.ConnectionID), Stream: s.Stream,
		Mode: oapi.ScheduleMode(s.Mode), IntervalSeconds: int(s.RunInterval / time.Second), LookbackSeconds: int(s.Lookback / time.Second),
		Enabled: s.Enabled, NextRunAt: s.NextRunAt}
}

func (o *owner) ListSchedules(ctx context.Context, req oapi.ListSchedulesRequestObject) (oapi.ListSchedulesResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	var conn *uuid.UUID
	if req.Params.Connection != nil {
		id, err := ingest.ParseConnectionID(*req.Params.Connection)
		if err != nil {
			return nil, problemErr(CodeValidationFailed, "invalid filter", FieldError{Pointer: "/connection", Detail: "must be a conn_ id"})
		}
		conn = &id
	}
	rows, err := o.schedulesOf(ctx, conn)
	if err != nil {
		return nil, err
	}
	out := oapi.ListSchedules200JSONResponse{Schedules: make([]oapi.Schedule, len(rows))}
	for i, s := range rows {
		out.Schedules[i] = scheduleBody(s)
	}
	return out, nil
}

// UpdateSchedule merges the patch into the schedule (jobs.UpdateSchedule validates it).
func (o *owner) UpdateSchedule(ctx context.Context, req oapi.UpdateScheduleRequestObject) (oapi.UpdateScheduleResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return nil, problemErr(CodeNotFound, "no such schedule")
	}
	p := auth.PrincipalFrom(ctx)
	var out dbq.Schedule
	err = d.Tx(ctx, func(q *dbq.Queries) error {
		cur, err := q.GetOwnerSchedule(ctx, dbq.GetOwnerScheduleParams{ID: id, UserID: p.UserID})
		if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
			return problemErr(CodeNotFound, "no such schedule")
		} else if err != nil {
			return err
		}
		u := jobs.ScheduleUpdate{Interval: cur.RunInterval, Lookback: cur.Lookback, Enabled: cur.Enabled}
		if b := req.Body; b != nil {
			if b.IntervalSeconds != nil {
				u.Interval = time.Duration(*b.IntervalSeconds) * time.Second
			}
			if b.LookbackSeconds != nil {
				u.Lookback = time.Duration(*b.LookbackSeconds) * time.Second
			}
			if b.Enabled != nil {
				u.Enabled = *b.Enabled
			}
		}
		s, err := jobs.UpdateSchedule(ctx, q, id, u)
		if errors.Is(err, jobs.ErrInvalidSchedule) {
			return problemErr(CodeValidationFailed, strings.TrimPrefix(err.Error(), jobs.ErrInvalidSchedule.Error()+": "))
		} else if err != nil {
			return err
		}
		out = dbq.Schedule{ID: s.ID, ConnectionID: s.ConnectionID, Stream: s.Stream, Mode: s.Mode, RunInterval: s.Interval,
			Lookback: s.Lookback, NextRunAt: s.NextRunAt, Enabled: s.Enabled}
		before := map[string]any{"interval_seconds": cur.RunInterval.Seconds(), "lookback_seconds": cur.Lookback.Seconds(), "enabled": cur.Enabled}
		after := map[string]any{"interval_seconds": s.Interval.Seconds(), "lookback_seconds": s.Lookback.Seconds(), "enabled": s.Enabled}
		return audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: "schedule.update",
			TargetType: "schedule", TargetID: id.String(), Detail: map[string]any{"stream": s.Stream, "mode": s.Mode, "diff": audit.Diff(before, after)}})
	})
	if err != nil {
		return nil, err
	}
	return oapi.UpdateSchedule200JSONResponse(scheduleBody(out)), nil
}

var jobStatuses = []string{"queued", "running", "succeeded", "failed", "dead", "cancelled"}

func (o *owner) ListJobs(ctx context.Context, req oapi.ListJobsRequestObject) (oapi.ListJobsResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	if s := req.Params.Status; s != nil && !slices.Contains(jobStatuses, *s) {
		return nil, problemErr(CodeValidationFailed, "invalid filter", FieldError{Pointer: "/status", Detail: "must be one of " + strings.Join(jobStatuses, ", ")})
	}
	bound := req.Params
	bound.Limit, bound.Cursor = nil, nil
	p, err := o.newPage("jobs", bound, req.Params.Limit, req.Params.Cursor)
	if err != nil {
		return nil, err
	}
	afterKey, err := p.afterKey()
	if err != nil {
		return nil, err
	}
	afterID, err := p.afterUUID()
	if err != nil {
		return nil, err
	}
	rows, err := o.opts.DB.Q().ListOwnerJobs(ctx, dbq.ListOwnerJobsParams{UserID: auth.PrincipalFrom(ctx).UserID,
		Status: req.Params.Status, AfterKey: afterKey, AfterID: afterID, Lim: p.lim()})
	if err != nil {
		return nil, db.MapErr(err)
	}
	rows, more, next := trim(o, p, rows, func(j dbq.Job) (time.Time, string) { return j.CreatedAt, j.ID.String() })
	out := oapi.ListJobs200JSONResponse{HasMore: more, NextCursor: next, Jobs: make([]oapi.Job, len(rows))}
	for i, j := range rows {
		out.Jobs[i] = jobBody(j)
	}
	return out, nil
}

// periods returns the caller's timezone periods, oldest first, each ending where the next starts.
func (o *owner) periods(ctx context.Context) ([]oapi.TimezonePeriod, error) {
	tl, err := normalize.NewPeriods(o.opts.DB).Timeline(ctx, auth.PrincipalFrom(ctx).UserID)
	if err != nil {
		return nil, err
	}
	out := make([]oapi.TimezonePeriod, len(tl))
	for i, p := range tl {
		out[i] = oapi.TimezonePeriod{ID: p.ID, Tz: p.TZ, ValidFrom: p.ValidFrom}
		if i+1 < len(tl) {
			out[i].ValidTo = &tl[i+1].ValidFrom
		}
	}
	return out, nil
}

func (o *owner) period(ctx context.Context, id uuid.UUID) (oapi.TimezonePeriod, error) {
	ps, err := o.periods(ctx)
	if err != nil {
		return oapi.TimezonePeriod{}, err
	}
	i := slices.IndexFunc(ps, func(p oapi.TimezonePeriod) bool { return p.ID == id })
	if i < 0 {
		return oapi.TimezonePeriod{}, problemErr(CodeNotFound, "no such timezone period")
	}
	return ps[i], nil
}

// periodProblem maps the period service's errors.
func periodProblem(err error) error {
	switch {
	case errors.Is(err, normalize.ErrBadTimezone):
		return problemErr(CodeValidationFailed, "unknown timezone", FieldError{Pointer: "/tz", Detail: "must be an IANA timezone name"})
	case errors.Is(err, db.ErrConflict):
		return problemErr(CodeConflict, "another period starts at the same instant; edit that one")
	case errors.Is(err, db.ErrNotFound):
		return problemErr(CodeNotFound, "no such timezone period")
	}
	return err
}

func (o *owner) ListTimezonePeriods(ctx context.Context, _ oapi.ListTimezonePeriodsRequestObject) (oapi.ListTimezonePeriodsResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	ps, err := o.periods(ctx)
	if err != nil {
		return nil, err
	}
	return oapi.ListTimezonePeriods200JSONResponse{TimezonePeriods: ps}, nil
}

func (o *owner) CreateTimezonePeriod(ctx context.Context, req oapi.CreateTimezonePeriodRequestObject) (oapi.CreateTimezonePeriodResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	p := auth.PrincipalFrom(ctx)
	np, _, err := normalize.NewPeriods(o.opts.DB).Add(ctx, p.UserID, p.Actor(), req.Body.ValidFrom, req.Body.Tz)
	if err != nil {
		return nil, periodProblem(err)
	}
	out, err := o.period(ctx, np.ID)
	if err != nil {
		return nil, err
	}
	return oapi.CreateTimezonePeriod201JSONResponse(out), nil
}

func (o *owner) UpdateTimezonePeriod(ctx context.Context, req oapi.UpdateTimezonePeriodRequestObject) (oapi.UpdateTimezonePeriodResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return nil, problemErr(CodeNotFound, "no such timezone period")
	}
	p := auth.PrincipalFrom(ctx)
	if _, err := normalize.NewPeriods(o.opts.DB).Edit(ctx, p.UserID, p.Actor(), id, req.Body.ValidFrom, req.Body.Tz); err != nil {
		return nil, periodProblem(err)
	}
	out, err := o.period(ctx, id)
	if err != nil {
		return nil, err
	}
	return oapi.UpdateTimezonePeriod200JSONResponse(out), nil
}

func (o *owner) DeleteTimezonePeriod(ctx context.Context, req oapi.DeleteTimezonePeriodRequestObject) (oapi.DeleteTimezonePeriodResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return nil, problemErr(CodeNotFound, "no such timezone period")
	}
	p := auth.PrincipalFrom(ctx)
	if _, err := normalize.NewPeriods(o.opts.DB).Remove(ctx, p.UserID, p.Actor(), id); err != nil {
		return nil, periodProblem(err)
	}
	return oapi.DeleteTimezonePeriod204Response{}, nil
}

// settings reads the owner's settings; a key never set has its default.
func (o *owner) settings(ctx context.Context) (oapi.Settings, error) {
	v, err := o.opts.DB.Q().GetUserSetting(ctx, dbq.GetUserSettingParams{UserID: auth.PrincipalFrom(ctx).UserID, Key: withings.SettingNotifications})
	on := false
	if err = db.MapErr(err); err == nil {
		_ = json.Unmarshal(v, &on) // anything but true is off, as in withings.Notifications.Enabled
	} else if !errors.Is(err, db.ErrNotFound) {
		return oapi.Settings{}, err
	}
	return oapi.Settings{WithingsNotifications: &on}, nil
}

func (o *owner) GetSettings(ctx context.Context, _ oapi.GetSettingsRequestObject) (oapi.GetSettingsResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	s, err := o.settings(ctx)
	if err != nil {
		return nil, err
	}
	return oapi.GetSettings200JSONResponse(s), nil
}

// UpdateSettings applies the keys present. withings.notifications also (un)subscribes every
// Withings connection; the setting is saved even when that fails at the provider (503: save
// again to retry).
func (o *owner) UpdateSettings(ctx context.Context, req oapi.UpdateSettingsRequestObject) (oapi.UpdateSettingsResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	before, err := o.settings(ctx)
	if err != nil {
		return nil, err
	}
	var applyErr error
	if on := req.Body.WithingsNotifications; on != nil {
		if o.opts.Withings == nil {
			return nil, problemErr(CodeUnavailable, "Withings is unavailable: the master key or data directory is missing")
		}
		p := auth.PrincipalFrom(ctx)
		applyErr = o.opts.Withings.SetEnabled(ctx, p.UserID, *on)
		if errors.Is(applyErr, withings.ErrNoPublicURL) {
			return nil, problemErr(CodeValidationFailed, "notifications need VITAMUX_PUBLIC_URL", FieldError{Pointer: "/withings.notifications", Detail: "set VITAMUX_PUBLIC_URL first"})
		}
		err := o.opts.DB.Tx(ctx, func(q *dbq.Queries) error {
			return audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: "settings.update",
				TargetType: "setting", TargetID: withings.SettingNotifications,
				Detail: map[string]any{"from": *before.WithingsNotifications, "to": *on}})
		})
		if err != nil {
			return nil, err
		}
	}
	if applyErr != nil {
		o.log.WarnContext(ctx, "apply withings notifications", "request_id", requestIDFrom(ctx), "err", applyErr)
		return nil, problemErr(CodeUnavailable, "the setting was saved, but applying it at Withings failed; save it again to retry")
	}
	s, err := o.settings(ctx)
	if err != nil {
		return nil, err
	}
	return oapi.UpdateSettings200JSONResponse(s), nil
}
