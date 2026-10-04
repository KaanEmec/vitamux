package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/resolve"
)

// Dashboard layout (J21.7): the owner's cards, kept as the versioned JSON document of the
// dashboard.layout setting. Without one, GET answers the curated default.

const (
	settingDashboard  = "dashboard.layout"
	maxDashboardCards = 50
	maxDashboardHero  = 4
)

// defaultHero is the curated hero: steps, resting heart rate, HRV and weight.
var defaultHero = []string{"steps", "resting_heart_rate", "hrv_rmssd_nightly", "weight"}

// defaultDashboard is the curated layout: sleep as the hero, then the mainstream metrics. The
// panel hides cards without data until a source provides it.
var defaultDashboard = []oapi.DashboardCard{
	{Metric: resolve.FamilySleep, Size: oapi.L},
	{Metric: "resting_heart_rate", Size: oapi.M},
	{Metric: "hrv_rmssd_nightly", Size: oapi.M},
	{Metric: "hrv_sdnn", Size: oapi.M},
	{Metric: "steps", Size: oapi.M},
	{Metric: "vo2max", Size: oapi.S},
	{Metric: "weight", Size: oapi.S},
	{Metric: resolve.FamilyBloodPressure, Size: oapi.S},
	{Metric: "spo2", Size: oapi.S},
	{Metric: "respiratory_rate", Size: oapi.S},
	{Metric: "active_energy", Size: oapi.S},
}

func (o *owner) GetDashboardLayout(ctx context.Context, _ oapi.GetDashboardLayoutRequestObject) (oapi.GetDashboardLayoutResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	raw, err := d.Q().GetUserSetting(ctx, dbq.GetUserSettingParams{UserID: auth.PrincipalFrom(ctx).UserID, Key: settingDashboard})
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return oapi.GetDashboardLayout200JSONResponse{Version: 1, Cards: append([]oapi.DashboardCard{}, defaultDashboard...), Hero: append([]string{}, defaultHero...), IsDefault: true}, nil
	} else if err != nil {
		return nil, err
	}
	var stored oapi.DashboardLayoutInput
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("dashboard layout: %w", err)
	}
	out := oapi.GetDashboardLayout200JSONResponse{Version: 1, Cards: []oapi.DashboardCard{}, Hero: append([]string{}, defaultHero...)}
	if stored.Hero != nil { // an empty list is the owner's choice of none
		out.Hero = []string{}
		for _, m := range *stored.Hero {
			if resolvable(m) {
				out.Hero = append(out.Hero, m)
			}
		}
	}
	for _, c := range stored.Cards {
		if resolvable(c.Metric) { // a code removed from the catalogue since
			out.Cards = append(out.Cards, c)
		}
	}
	return out, nil
}

// PutDashboardLayout replaces the layout after checking every card; audited.
func (o *owner) PutDashboardLayout(ctx context.Context, req oapi.PutDashboardLayoutRequestObject) (oapi.PutDashboardLayoutResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	in := *req.Body
	var errs []FieldError
	if in.Version != oapi.DashboardLayoutInputVersionN1 {
		errs = append(errs, FieldError{Pointer: "/version", Detail: "must be 1"})
	}
	if len(in.Cards) > maxDashboardCards {
		errs = append(errs, FieldError{Pointer: "/cards", Detail: "at most 50 cards"})
	}
	seen := map[string]bool{}
	for i, c := range in.Cards {
		at := fmt.Sprintf("/cards/%d", i)
		switch {
		case !resolvable(c.Metric):
			errs = append(errs, FieldError{Pointer: at + "/metric", Detail: "not a catalogue code or rule family"})
		case seen[c.Metric]:
			errs = append(errs, FieldError{Pointer: at + "/metric", Detail: "already on the dashboard"})
		}
		if c.Size != oapi.S && c.Size != oapi.M && c.Size != oapi.L {
			errs = append(errs, FieldError{Pointer: at + "/size", Detail: "must be S, M or L"})
		}
		seen[c.Metric] = true
	}
	if in.Hero != nil {
		if len(*in.Hero) > maxDashboardHero {
			errs = append(errs, FieldError{Pointer: "/hero", Detail: "at most 4 metrics"})
		}
		heroSeen := map[string]bool{}
		for i, m := range *in.Hero {
			at := fmt.Sprintf("/hero/%d", i)
			switch {
			case !resolvable(m):
				errs = append(errs, FieldError{Pointer: at, Detail: "not a catalogue code or rule family"})
			case heroSeen[m]:
				errs = append(errs, FieldError{Pointer: at, Detail: "already a hero metric"})
			}
			heroSeen[m] = true
		}
	}
	if len(errs) > 0 {
		return nil, problemErr(CodeValidationFailed, "invalid layout", errs...)
	}
	if in.Cards == nil {
		in.Cards = []oapi.DashboardCard{}
	}
	value, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	p := auth.PrincipalFrom(ctx)
	err = d.Tx(ctx, func(q *dbq.Queries) error {
		if err := q.PutUserSetting(ctx, dbq.PutUserSettingParams{UserID: p.UserID, Key: settingDashboard, Value: value}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: "settings.update",
			TargetType: "setting", TargetID: settingDashboard, Detail: map[string]any{"cards": len(in.Cards)}})
	})
	if err != nil {
		return nil, err
	}
	out := oapi.PutDashboardLayout200JSONResponse{Version: 1, Cards: in.Cards, Hero: append([]string{}, defaultHero...)}
	if in.Hero != nil {
		out.Hero = slices.Clone(*in.Hero)
	}
	return out, nil
}
