package api

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/resolve"
)

// Rules and manual overrides (J10.4; docs/architecture/resolution.md). The stores in
// internal/resolve validate and audit every change; these handlers map their errors.
func (rt *router) ruleRoutes() {
	read, write := scope(auth.ReadConfig), scope(auth.WriteConfig)
	rt.handle("GET /api/v1/rules", read, rt.ops.ListRules)
	rt.handle("GET /api/v1/rules/{metric}/versions", read, rt.ops.ListRuleVersions)
	rt.handle("POST /api/v1/rules/{metric}/versions", write, rt.ops.CreateRuleVersion)
	rt.handle("POST /api/v1/rules/{metric}/activate", write, rt.ops.ActivateRule)
	rt.handle("GET /api/v1/overrides", read, rt.ops.ListOverrides)
	rt.handle("POST /api/v1/overrides", write, rt.ops.CreateOverride)
	rt.handle("POST /api/v1/overrides/{id}/revoke", write, rt.ops.RevokeOverride)
}

// ownerDB returns the database, or 503 before it is configured.
func (o *owner) ownerDB() (*db.DB, error) {
	if o.opts.DB == nil {
		return nil, problemErr(CodeUnavailable, "the database is not ready")
	}
	return o.opts.DB, nil
}

// by is the caller as the resolve stores record it.
func by(ctx context.Context) resolve.By {
	p := auth.PrincipalFrom(ctx)
	return resolve.By{UserID: p.UserID, Actor: p.Actor()}
}

func ruleVersionBody(v resolve.Version) oapi.RuleVersion {
	out := oapi.RuleVersion{Ref: v.Ref, Metric: v.Metric, Version: v.Version, Builtin: v.Builtin, Active: v.Active,
		Spec: v.Spec, BasedOn: optString(v.BasedOn), Note: optString(v.Note), CreatedBy: optString(v.CreatedBy)}
	if !v.CreatedAt.IsZero() {
		out.CreatedAt = &v.CreatedAt
	}
	if b, ok := resolve.LookupBuiltin(v.Metric); ok && v.Builtin && b.Version == v.Version {
		out.Reason = &b.Why
	}
	return out
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ruleProblem maps a rule ValidationError: a sum without its acknowledgement is
// rule_warning_unacknowledged, anything else validation_failed with pointers under prefix.
func ruleProblem(err error, prefix string) error {
	var ve *resolve.ValidationError
	if !errors.As(err, &ve) {
		return err
	}
	unacked := len(ve.Errors) > 0
	errs := make([]FieldError, len(ve.Errors))
	for i, f := range ve.Errors {
		errs[i] = FieldError{Pointer: prefix + f.Pointer, Detail: f.Detail}
		unacked = unacked && f.Pointer == "/acknowledged_warnings" && strings.Contains(f.Detail, "acknowledged")
	}
	if unacked {
		return problemErr(CodeRuleWarningUnacknowledged, "the rule sums across sources: acknowledge "+string(resolve.WarnCrossSourceSum)+" in acknowledged_warnings", errs...)
	}
	return problemErr(CodeValidationFailed, "invalid rule", errs...)
}

func (o *owner) ListRules(ctx context.Context, _ oapi.ListRulesRequestObject) (oapi.ListRulesResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	set, err := resolve.NewStore(o.opts.DB).ActiveSet(ctx, auth.PrincipalFrom(ctx).UserID)
	if err != nil {
		return nil, err
	}
	out := oapi.ListRules200JSONResponse{Rules: make([]oapi.Rule, len(set))}
	for i, v := range set {
		out.Rules[i] = ruleVersionBody(v)
	}
	return out, nil
}

// ListRuleVersions answers the owner's versions, or the built-in alone while the metric was
// never edited. A metric that is neither in the catalogue nor a family is 404.
func (o *owner) ListRuleVersions(ctx context.Context, req oapi.ListRuleVersionsRequestObject) (oapi.ListRuleVersionsResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	vs, err := resolve.NewStore(o.opts.DB).History(ctx, auth.PrincipalFrom(ctx).UserID, req.Metric)
	if err != nil {
		return nil, err
	}
	out := oapi.ListRuleVersions200JSONResponse{Versions: make([]oapi.RuleVersion, 0, len(vs)+1)}
	for _, v := range vs {
		out.Versions = append(out.Versions, ruleVersionBody(v))
	}
	if len(vs) == 0 {
		b, ok := resolve.LookupBuiltin(req.Metric)
		_, known := catalog.Lookup(req.Metric)
		switch {
		case ok:
			spec, err := json.Marshal(b.Rule)
			if err != nil {
				return nil, err
			}
			out.Versions = append(out.Versions, ruleVersionBody(resolve.Version{Ref: b.Ref(), Metric: b.Rule.Metric,
				Version: b.Version, Builtin: true, Spec: spec, Active: true}))
		case !known:
			return nil, problemErr(CodeNotFound, "no such metric")
		}
	}
	return out, nil
}

func (o *owner) CreateRuleVersion(ctx context.Context, req oapi.CreateRuleVersionRequestObject) (oapi.CreateRuleVersionResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	var head struct {
		Metric string `json:"metric"`
	}
	if json.Unmarshal(req.Body.Spec, &head) == nil && head.Metric != "" && head.Metric != req.Metric {
		return nil, problemErr(CodeValidationFailed, "invalid rule", FieldError{Pointer: "/spec/metric", Detail: "must be the metric in the path"})
	}
	note := ""
	if req.Body.Note != nil {
		note = *req.Body.Note
	}
	v, err := resolve.NewStore(o.opts.DB).Create(ctx, by(ctx), req.Body.Spec, note, req.Body.Activate != nil && *req.Body.Activate)
	if err != nil {
		return nil, ruleProblem(err, "/spec")
	}
	return oapi.CreateRuleVersion201JSONResponse(ruleVersionBody(v)), nil
}

func (o *owner) ActivateRule(ctx context.Context, req oapi.ActivateRuleRequestObject) (oapi.ActivateRuleResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	v, err := resolve.NewStore(o.opts.DB).Activate(ctx, by(ctx), req.Metric, req.Body.Version)
	if errors.Is(err, db.ErrNotFound) {
		return nil, problemErr(CodeNotFound, "no such rule version")
	}
	if err != nil {
		return nil, ruleProblem(err, "/active_rules")
	}
	return oapi.ActivateRule200JSONResponse(ruleVersionBody(v)), nil
}

func overrideBody(r resolve.Override) oapi.Override {
	out := oapi.Override{ID: r.ID, Metric: r.Metric, Action: oapi.OverrideAction(r.Action), Active: r.Active(),
		Window:    oapi.OverrideWindow{Kind: oapi.OverrideWindowKind(r.Kind), Key: r.Key, LocalDate: openapi_types.Date{Time: r.LocalDate}},
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, RevokedAt: r.RevokedAt, RevokedBy: optString(r.RevokedBy)}
	switch r.Action {
	case resolve.ExcludeInput:
		id := strconv.FormatInt(r.InputID, 10)
		out.InputID = &id
	case resolve.ForceSource:
		out.Group = &r.Group
	case resolve.SetValue:
		out.Value, out.Unit, out.Note = &r.Value, &r.Unit, &r.Note
	}
	return out
}

func overrideOf(row dbq.ManualOverride) resolve.Override {
	o := resolve.Override{ID: row.ID, Metric: row.Metric, Kind: catalog.Window(row.WindowKind), Key: row.WindowKey, LocalDate: row.LocalDate,
		Action: resolve.OverrideAction(row.Action), CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, RevokedAt: row.RevokedAt}
	o.InputID, o.Group, o.Value, o.Unit, o.Note, o.RevokedBy = ptrVal(row.InputID), ptrVal(row.SourceGroup), ptrVal(row.Value), ptrVal(row.Unit), ptrVal(row.Note), ptrVal(row.RevokedBy)
	return o
}

func (o *owner) ListOverrides(ctx context.Context, req oapi.ListOverridesRequestObject) (oapi.ListOverridesResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	bound := req.Params
	bound.Limit, bound.Cursor = nil, nil
	p, err := o.newPage("overrides", bound, req.Params.Limit, req.Params.Cursor)
	if err != nil {
		return nil, err
	}
	afterKey, afterID, err := p.afterKeyUUID()
	if err != nil {
		return nil, err
	}
	rows, err := o.opts.DB.Q().ListOverridesPage(ctx, dbq.ListOverridesPageParams{UserID: auth.PrincipalFrom(ctx).UserID,
		Metrics: ptrVal(req.Params.Metric), AfterKey: afterKey, AfterID: afterID, Lim: p.lim()})
	if err != nil {
		return nil, db.MapErr(err)
	}
	rows, more, next := trim(o, p, rows, func(r dbq.ManualOverride) (time.Time, string) { return r.CreatedAt, r.ID.String() })
	out := oapi.ListOverrides200JSONResponse{HasMore: more, NextCursor: next, Overrides: make([]oapi.Override, len(rows))}
	for i, r := range rows {
		out.Overrides[i] = overrideBody(overrideOf(r))
	}
	return out, nil
}

func (o *owner) CreateOverride(ctx context.Context, req oapi.CreateOverrideRequestObject) (oapi.CreateOverrideResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	b := req.Body
	n := resolve.NewOverride{Metric: b.Metric, Kind: catalog.Window(b.Window.Kind), Key: b.Window.Key, LocalDate: b.Window.LocalDate.Time,
		Action: resolve.OverrideAction(b.Action), Group: ptrVal(b.Group), Value: ptrVal(b.Value), Unit: ptrVal(b.Unit), Note: ptrVal(b.Note)}
	if b.InputID != nil {
		id, err := strconv.ParseInt(*b.InputID, 10, 64)
		if err != nil {
			return nil, problemErr(CodeValidationFailed, "invalid override", FieldError{Pointer: "/input_id", Detail: "must be a measurement id"})
		}
		n.InputID = id
	}
	r, err := resolve.NewOverrides(o.opts.DB).Create(ctx, by(ctx), n)
	switch {
	case errors.Is(err, resolve.ErrInvalidOverride):
		return nil, problemErr(CodeValidationFailed, strings.TrimPrefix(err.Error(), resolve.ErrInvalidOverride.Error()+": "))
	case errors.Is(err, db.ErrConflict):
		return nil, problemErr(CodeConflict, "the window already has an active override of this kind; revoke it first")
	case err != nil:
		return nil, err
	}
	return oapi.CreateOverride201JSONResponse(overrideBody(r)), nil
}

func (o *owner) RevokeOverride(ctx context.Context, req oapi.RevokeOverrideRequestObject) (oapi.RevokeOverrideResponseObject, error) {
	if _, err := o.ownerDB(); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return nil, problemErr(CodeNotFound, "no such override")
	}
	r, err := resolve.NewOverrides(o.opts.DB).Revoke(ctx, by(ctx), id)
	switch {
	case errors.Is(err, db.ErrNotFound):
		return nil, problemErr(CodeNotFound, "no such override")
	case errors.Is(err, db.ErrConflict):
		return nil, problemErr(CodeConflict, "the override is already revoked")
	case err != nil:
		return nil, err
	}
	return oapi.RevokeOverride200JSONResponse(overrideBody(r)), nil
}
