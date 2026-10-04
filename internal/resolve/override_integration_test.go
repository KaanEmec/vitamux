//go:build integration

package resolve_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/normalize"
	"github.com/KaanEmec/vitamux/internal/resolve"
)

// ovEnv is a migrated database with one synthetic owner and heart rate samples from two
// providers (withings, garmin) in two consecutive hours.
type ovEnv struct {
	ctx    context.Context
	appSQL func(stmt string) error               // as the app role
	owner  func(query string, dest ...any) error // QueryRow(...).Scan as the schema owner
	ownerX func(stmt string) error
	ov     *resolve.Overrides
	by     resolve.By
	rule   *resolve.Rule
	series resolve.Series
	h1, h2 resolve.Window
	ids    map[string]int64 // "<provider>/<hour>" -> measurements.id
}

func newOvEnv(t *testing.T) *ovEnv {
	t.Helper()
	ctx := context.Background()
	u, pool := dbtest.Migrated(t)
	owner := dbtest.Pool(t, u, db.OwnerRole)
	e := &ovEnv{ctx: ctx, ov: resolve.NewOverrides(db.New(pool)),
		by: resolve.By{UserID: uuid.MustParse(ownerID), Actor: "api_key:test"}, ids: map[string]int64{}}
	e.appSQL = func(stmt string) error { _, err := pool.Exec(ctx, stmt); return err }
	e.ownerX = func(stmt string) error { _, err := owner.Exec(ctx, stmt); return err }
	e.owner = func(query string, dest ...any) error { return owner.QueryRow(ctx, query).Scan(dest...) }
	stmts := []string{
		"INSERT INTO users (id, username, password_hash) VALUES ('" + ownerID + "', 'owner', 'synthetic')",
		"INSERT INTO normalizer_versions (name, version, git_sha) VALUES ('test', 1, 'dev')",
	}
	for _, p := range []string{"withings", "garmin"} {
		stmts = append(stmts, fmt.Sprintf("INSERT INTO connections (id, user_id, provider_id, account_key, mode, status) VALUES ('%s', '%s', (SELECT id FROM providers WHERE code = '%s'), sha256('%s'), 'in_process', 'active')",
			uuid.New(), ownerID, p, p))
	}
	for _, s := range stmts {
		if err := e.ownerX(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	e.h1 = hourWindow("2026-06-15T10:00:00Z")
	e.h2 = hourWindow("2026-06-15T11:00:00Z")
	e.rule = &resolve.Rule{Schema: resolve.SchemaV1, Metric: "heart_rate", Window: resolve.RuleWindow{Kind: catalog.WindowHour},
		Strategy: resolve.Strategy{Op: resolve.OpFirstAvailable},
		Groups:   []resolve.Group{{ID: "withings", Match: []resolve.Selector{{Provider: "withings"}}}, {ID: "garmin", Match: []resolve.Selector{{Provider: "garmin"}}}}}
	e.series = resolve.Series{"heart_rate": nil}
	for _, row := range []struct {
		provider string
		w        resolve.Window
		hour     string
		value    float64
	}{{"withings", e.h1, "h1", 61}, {"garmin", e.h1, "h1", 71}, {"withings", e.h2, "h2", 62}, {"garmin", e.h2, "h2", 72}} {
		var id int64
		err := e.owner(fmt.Sprintf(`INSERT INTO measurements (user_id, metric_id, kind, start_at, local_date, value, provider_id, connection_id,
			dedupe_key, normalizer_version_id)
			SELECT '%s', (SELECT id FROM metric_catalog WHERE code = 'heart_rate'), 'sample', '%s', '2026-06-15', %v, c.provider_id, c.id,
			substring(sha256('%s') for 16), 1 FROM connections c WHERE c.account_key = sha256('%s') RETURNING id`,
			ownerID, row.w.Start.Add(time.Minute).Format(time.RFC3339), row.value, row.provider+row.hour, row.provider), &id)
		if err != nil {
			t.Fatal(err)
		}
		e.ids[row.provider+"/"+row.hour] = id
		e.series["heart_rate"] = append(e.series["heart_rate"], resolve.Input{ID: id, Source: resolve.Source{Provider: row.provider},
			Kind: catalog.Sample, Start: row.w.Start.Add(time.Minute), LocalDate: row.w.Date, Value: row.value, Flags: normalize.Flags(0)})
	}
	return e
}

func hourWindow(start string) resolve.Window {
	s, _ := time.Parse(time.RFC3339, start)
	return resolve.Window{Kind: catalog.WindowHour, Start: s, End: s.Add(time.Hour), Date: time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, time.UTC), Key: s.Format(time.RFC3339)}
}

// measurementsHash fingerprints every measurements row.
func (e *ovEnv) measurementsHash(t *testing.T) string {
	t.Helper()
	var h string
	if err := e.owner("SELECT coalesce(md5(string_agg(m::text, '|' ORDER BY id)), '') FROM measurements m", &h); err != nil || h == "" {
		t.Fatalf("hash %q: %v", h, err)
	}
	return h
}

func (e *ovEnv) resolveAll(t *testing.T) []resolve.Resolved {
	t.Helper()
	ovs, err := e.ov.Active(e.ctx, e.by.UserID, "heart_rate", e.h1.Date, e.h2.Date)
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.rule.ResolveWindowsOverridden([]resolve.Window{e.h1, e.h2}, e.series, resolve.Options{}, ovs)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (e *ovEnv) create(t *testing.T, n resolve.NewOverride) resolve.Override {
	t.Helper()
	o, err := e.ov.Create(e.ctx, e.by, n)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestOverridesLifecycle(t *testing.T) {
	e := newOvEnv(t)
	hashBefore := e.measurementsHash(t)
	computed := e.resolveAll(t)
	if computed[0].Selected != "withings" || computed[0].Value != 61 || computed[1].Value != 62 {
		t.Fatalf("computed %+v %+v", computed[0].WindowResult, computed[1].WindowResult)
	}

	cases := []struct {
		name    string
		n       resolve.NewOverride
		status  resolve.ResultStatus
		value   float64
		changed int // the window index that changes: 0 = h1; h2 never does
	}{
		{"exclude", resolve.NewOverride{Scope: resolve.ScopeOf("heart_rate", e.h1), Action: resolve.ExcludeInput, InputID: e.ids["withings/h1"]}, resolve.ResultOverridden, 71, 0},
		{"force", resolve.NewOverride{Scope: resolve.ScopeOf("heart_rate", e.h1), Action: resolve.ForceSource, Group: "garmin"}, resolve.ResultOverridden, 71, 0},
		{"set", resolve.NewOverride{Scope: resolve.ScopeOf("heart_rate", e.h1), Action: resolve.SetValue, Value: 65, Unit: "bpm", Note: "strap"}, resolve.ResultOverridden, 65, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created := e.create(t, tc.n)
			got := e.resolveAll(t)
			if got[0].Status != tc.status || got[0].Value != tc.value || len(got[0].Overrides) != 1 || got[0].Overrides[0].ID != created.ID {
				t.Errorf("h1: %s %v applied %v", got[0].Status, got[0].Value, got[0].Overrides)
			}
			if got[0].Computed == nil || got[0].Computed.Value != 61 {
				t.Errorf("computed not kept: %+v", got[0].Computed)
			}
			if got[1].Status != computed[1].Status || got[1].Value != 62 || got[1].Overrides != nil {
				t.Errorf("h2 changed: %+v", got[1])
			}
			if h := e.measurementsHash(t); h != hashBefore {
				t.Error("source rows changed")
			}
			// Revoke restores the computed result and keeps the row as history.
			revoked, err := e.ov.Revoke(e.ctx, e.by, created.ID)
			if err != nil || revoked.RevokedAt == nil || revoked.RevokedBy != e.by.Actor {
				t.Fatalf("revoke: %+v %v", revoked, err)
			}
			after := e.resolveAll(t)
			if after[0].Status != computed[0].Status || after[0].Value != 61 || after[0].Selected != "withings" || after[0].Computed != nil {
				t.Errorf("not restored: %+v", after[0])
			}
			if _, err := e.ov.Revoke(e.ctx, e.by, created.ID); !errors.Is(err, db.ErrConflict) {
				t.Errorf("second revoke: %v", err)
			}
		})
	}

	hist, err := e.ov.History(e.ctx, e.by.UserID, "heart_rate", 10)
	if err != nil || len(hist) != 3 || hist[0].RevokedAt == nil {
		t.Errorf("history %d rows, err %v", len(hist), err)
	}
	if h := e.measurementsHash(t); h != hashBefore {
		t.Error("source rows changed")
	}
}

func TestOverridesDirtyAuditAndConflict(t *testing.T) {
	e := newOvEnv(t)
	n := resolve.NewOverride{Scope: resolve.ScopeOf("heart_rate", e.h1), Action: resolve.SetValue, Value: 66.5, Unit: "bpm", Note: "private note"}
	count := func(q string) (c int) {
		t.Helper()
		if err := e.owner(q, &c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	if count("SELECT count(*) FROM resolution_dirty") != 0 {
		t.Fatal("dirty rows before any change")
	}
	o := e.create(t, n)
	dirty := "SELECT count(*) FROM resolution_dirty d JOIN metric_catalog m ON m.id = d.metric_id WHERE m.code = 'heart_rate' AND d.local_date = '2026-06-15'"
	if count(dirty) != 1 {
		t.Error("create did not mark the day dirty")
	}
	if err := e.ownerX("DELETE FROM resolution_dirty"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ov.Revoke(e.ctx, e.by, o.ID); err != nil || count(dirty) != 1 {
		t.Errorf("revoke: err %v, dirty %d", err, count(dirty))
	}

	// One active set_value per window; a second needs the first revoked.
	e.create(t, n)
	if _, err := e.ov.Create(e.ctx, e.by, n); !errors.Is(err, db.ErrConflict) {
		t.Errorf("duplicate set_value: %v", err)
	}
	// Invalid requests never reach the database.
	bad := n
	bad.Unit = "bps"
	if _, err := e.ov.Create(e.ctx, e.by, bad); !errors.Is(err, resolve.ErrInvalidOverride) {
		t.Errorf("bad unit: %v", err)
	}
	// Another user's id is not found.
	if _, err := e.ov.Revoke(e.ctx, resolve.By{UserID: uuid.New(), Actor: "owner"}, o.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("foreign revoke: %v", err)
	}

	var events string
	err := e.owner(`SELECT coalesce(string_agg(concat_ws('|', actor, action, target_id, detail::text), E'\n' ORDER BY id), '') FROM audit_events WHERE action LIKE 'override.%'`, &events)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for line := range strings.SplitSeq(events, "\n") {
		f := strings.SplitN(line, "|", 4)
		if len(f) != 4 {
			t.Fatalf("audit line %q", line)
		}
		actor, action, target, detail := f[0], f[1], f[2], f[3]
		actions = append(actions, action)
		if actor != e.by.Actor || target == "" || strings.Contains(detail, "private note") || strings.Contains(detail, "66.5") || !strings.Contains(detail, "heart_rate") {
			t.Errorf("audit %s: actor %q target %q detail %s", action, actor, target, detail)
		}
	}
	if strings.Join(actions, ",") != "override.create,override.revoke,override.create" {
		t.Errorf("audit actions %v", actions)
	}
}

func TestOverridesAppRoleCannotDelete(t *testing.T) {
	e := newOvEnv(t)
	e.create(t, resolve.NewOverride{Scope: resolve.ScopeOf("heart_rate", e.h1), Action: resolve.ForceSource, Group: "garmin"})
	for _, stmt := range []string{"DELETE FROM manual_overrides", "UPDATE manual_overrides SET value = 1", "UPDATE manual_overrides SET action = 'set_value'"} {
		if err := e.appSQL(stmt); err == nil {
			t.Errorf("%s succeeded for the app role", stmt)
		}
	}
}
