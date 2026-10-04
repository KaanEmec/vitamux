//go:build integration

package resolve_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/resolve"
)

const ownerID = "01900000-0000-7000-8000-000000000001" // synthetic

type event struct{ actor, action, detail string }

// env reads audit events and runs statements as the app role.
type env struct {
	events func() []event
	exec   func(stmt string) error
	d      *db.DB
}

func newStore(t *testing.T) (*resolve.Store, env, resolve.By) {
	t.Helper()
	ctx := t.Context()
	_, pool := dbtest.Migrated(t)
	if _, err := pool.Exec(ctx, "INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')", ownerID); err != nil {
		t.Fatal(err)
	}
	e := env{
		d:    db.New(pool),
		exec: func(stmt string) error { _, err := pool.Exec(ctx, stmt); return err },
		events: func() []event {
			t.Helper()
			rows, err := pool.Query(ctx, "SELECT actor, action, detail::text FROM audit_events WHERE action LIKE 'rule.%' ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var out []event
			for rows.Next() {
				var ev event
				if err := rows.Scan(&ev.actor, &ev.action, &ev.detail); err != nil {
					t.Fatal(err)
				}
				out = append(out, ev)
			}
			return out
		},
	}
	return resolve.NewStore(e.d), e, resolve.By{UserID: uuid.MustParse(ownerID), Actor: "api_key:test"}
}

// spec returns the built-in for metric as JSON after edit changes it.
func spec(t *testing.T, metric string, edit func(*resolve.Rule)) []byte {
	t.Helper()
	b, ok := resolve.LookupBuiltin(metric)
	if !ok {
		t.Fatalf("no built-in for %s", metric)
	}
	edit(&b.Rule)
	j, err := json.Marshal(b.Rule)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestFirstEditCopiesBuiltin(t *testing.T) {
	ctx := t.Context()
	s, e, by := newStore(t)

	v, err := s.Active(ctx, by.UserID, "steps")
	if err != nil || !v.Builtin || v.Ref != "builtin:steps:3" {
		t.Fatalf("before any edit the built-in applies: %+v %v", v, err)
	}
	// The owner drops the phone fallback.
	edited := spec(t, "steps", func(r *resolve.Rule) { r.Groups = r.Groups[:len(r.Groups)-1] })
	v, err = s.Create(ctx, by, edited, "no phone", true)
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != 2 || v.Ref != "rule:steps:2" || !v.Active || v.Note != "no phone" || v.CreatedBy != by.Actor {
		t.Fatalf("edit = %+v", v)
	}
	hist, err := s.History(ctx, by.UserID, "steps")
	if err != nil || len(hist) != 2 {
		t.Fatalf("history = %+v %v", hist, err)
	}
	if hist[0].Version != 2 || !hist[0].Active || hist[1].Version != 1 || hist[1].Active || hist[1].BasedOn != "builtin:steps:3" {
		t.Fatalf("history = %+v", hist)
	}
	b, _ := resolve.LookupBuiltin("steps")
	if len(hist[1].Rule.Groups) != len(b.Rule.Groups) {
		t.Error("version 1 is a copy of the built-in")
	}
	diff, err := s.Diff(ctx, by.UserID, "steps", 1, 2)
	if _, ok := diff["groups"]; err != nil || len(diff) != 1 || !ok {
		t.Fatalf("diff = %v %v", diff, err)
	}
	if active, _ := s.Active(ctx, by.UserID, "steps"); active.Ref != "rule:steps:2" {
		t.Errorf("active = %s", active.Ref)
	}
	if other, _ := s.Active(ctx, by.UserID, "heart_rate"); !other.Builtin {
		t.Error("other metrics keep their built-ins")
	}

	ev := e.events()
	want := []string{"rule.copy_builtin", "rule.create", "rule.activate"}
	if len(ev) != len(want) {
		t.Fatalf("audit = %+v", ev)
	}
	for i, e := range ev {
		if e.action != want[i] || e.actor != by.Actor {
			t.Errorf("audit %d = %+v", i, e)
		}
	}
	if !strings.Contains(ev[1].detail, `"groups"`) || !strings.Contains(ev[2].detail, `"from": "builtin:steps:3"`) ||
		!strings.Contains(ev[2].detail, `"to": "rule:steps:2"`) {
		t.Errorf("audit details carry the diff and refs: %+v", ev)
	}
}

func TestEditCreatesNextVersionAndReactivation(t *testing.T) {
	ctx := t.Context()
	s, e, by := newStore(t)
	strict := spec(t, "heart_rate", func(r *resolve.Rule) { r.Quality.PlausibleRange = []float64{30, 220} })
	if _, err := s.Create(ctx, by, strict, "", true); err != nil {
		t.Fatal(err)
	}
	mean := spec(t, "heart_rate", func(r *resolve.Rule) { r.Strategy = resolve.Strategy{Op: resolve.OpMean} })
	v3, err := s.Create(ctx, by, mean, "", false)
	if err != nil || v3.Version != 3 || v3.Active {
		t.Fatalf("v3 = %+v %v", v3, err)
	}
	if a, _ := s.Active(ctx, by.UserID, "heart_rate"); a.Version != 2 {
		t.Fatalf("creating without activating keeps v2 active, got %s", a.Ref)
	}
	if _, err := s.Activate(ctx, by, "heart_rate", 3); err != nil {
		t.Fatal(err)
	}
	// Reactivating the built-in copy (an old version) works.
	v1, err := s.Activate(ctx, by, "heart_rate", 1)
	if err != nil || !v1.Active || v1.Ref != "rule:heart_rate:1" {
		t.Fatalf("reactivate = %+v %v", v1, err)
	}
	n := len(e.events())
	if _, err := s.Activate(ctx, by, "heart_rate", 1); err != nil {
		t.Fatal(err)
	}
	if len(e.events()) != n {
		t.Error("activating the active version again is a no-op")
	}
	ev := e.events()
	last := ev[len(ev)-1]
	if last.action != "rule.activate" || !strings.Contains(last.detail, `"from": "rule:heart_rate:3"`) || !strings.Contains(last.detail, `"strategy"`) {
		t.Errorf("last audit = %+v", last)
	}
	hist, _ := s.History(ctx, by.UserID, "heart_rate")
	if len(hist) != 3 || hist[2].Version != 1 || !hist[2].Active || hist[0].Active {
		t.Errorf("history = %+v", hist)
	}
	if _, err := s.Activate(ctx, by, "heart_rate", 9); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("unknown version: %v", err)
	}
}

func TestMetricWithoutBuiltinUsesDefault(t *testing.T) {
	ctx := t.Context()
	s, e, by := newStore(t)
	if _, err := s.Active(ctx, by.UserID, "no_such_code"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("an unknown code has no rule: %v", err)
	}
	before, err := s.Active(ctx, by.UserID, "skin_temperature")
	if err != nil || !before.Default || !before.Builtin || !strings.HasPrefix(before.Ref, "default:skin_temperature:") {
		t.Fatalf("a code without a built-in uses its default rule: %+v %v", before, err)
	}
	steps, _ := s.Active(ctx, by.UserID, "steps")

	for _, bad := range [][]string{{"garmin", "garmin"}, {"no_such_provider"}} {
		if err := resolve.SetSourcePriority(ctx, e.d.Q(), by.UserID, by.Actor, bad); !errors.Is(err, resolve.ErrInvalidPriority) {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if err := resolve.SetSourcePriority(ctx, e.d.Q(), by.UserID, by.Actor, []string{"whoop", "garmin"}); err != nil {
		t.Fatal(err)
	}
	if order, err := resolve.SourcePriority(ctx, e.d.Q(), by.UserID); err != nil || strings.Join(order, ",") != "whoop,garmin" {
		t.Fatalf("source order = %v %v", order, err)
	}
	after, _ := s.Active(ctx, by.UserID, "skin_temperature")
	if after.Ref == before.Ref || after.Rule.Groups[0].ID != "whoop" {
		t.Errorf("a new source order is a new default rule: %s -> %s, %+v", before.Ref, after.Ref, after.Rule.Groups[0])
	}
	if v, _ := s.Active(ctx, by.UserID, "steps"); v.Ref != steps.Ref {
		t.Errorf("built-ins ignore the source order: %s", v.Ref)
	}

	r := *after.Rule
	r.Groups = r.Groups[:1]
	j, _ := json.Marshal(r)
	v, err := s.Create(ctx, by, j, "", true)
	if err != nil || v.Version != 2 || !v.Active {
		t.Fatalf("the first edit is version 2: %+v %v", v, err)
	}
	hist, _ := s.History(ctx, by.UserID, "skin_temperature")
	if len(hist) != 2 || hist[1].BasedOn != after.Ref || hist[1].Default {
		t.Errorf("version 1 copies the default rule: %+v", hist)
	}
	set, err := s.ActiveSet(ctx, by.UserID)
	if err != nil || len(set) != len(resolve.Builtins())+len(resolve.NoBuiltin) {
		t.Errorf("a rule for every built-in and every code without one: %d rules, %v", len(set), err)
	}
	for _, x := range set {
		if x.Metric == "skin_temperature" && x.Ref != "rule:skin_temperature:2" {
			t.Errorf("the owner's version replaces the default rule: %s", x.Ref)
		}
	}
}

func TestRejectedChangesStoreNothing(t *testing.T) {
	ctx := t.Context()
	s, e, by := newStore(t)
	var ve *resolve.ValidationError

	pooled := spec(t, "resting_heart_rate", func(r *resolve.Rule) { r.Strategy = resolve.Strategy{Op: resolve.OpMean} })
	if _, err := s.Create(ctx, by, pooled, "", true); !errors.As(err, &ve) {
		t.Fatalf("selection-only metric pooled: %v", err)
	}
	// Body composition built-ins follow weight on local_day, so weight cannot move to latest.
	moved := spec(t, "weight", func(r *resolve.Rule) {
		r.Window = resolve.RuleWindow{Kind: catalog.WindowLatest}
		r.WithinSource = nil
	})
	if _, err := s.Create(ctx, by, moved, "", true); !errors.As(err, &ve) || !strings.Contains(ve.Error(), "follower") {
		t.Fatalf("follower window: %v", err)
	}
	if hist, _ := s.History(ctx, by.UserID, "weight"); len(hist) != 0 {
		t.Fatalf("a rejected activation rolls back the version too: %+v", hist)
	}
	if len(e.events()) != 0 {
		t.Error("rejected changes are not audited")
	}
	v, err := s.Create(ctx, by, moved, "", false)
	if err != nil || v.Version != 2 {
		t.Fatalf("storing without activating is allowed: %+v %v", v, err)
	}
	if _, err := s.Activate(ctx, by, "weight", 2); !errors.As(err, &ve) {
		t.Fatalf("activation checks the set: %v", err)
	}
	if a, _ := s.Active(ctx, by.UserID, "weight"); !a.Builtin {
		t.Errorf("weight stays on its built-in, got %s", a.Ref)
	}
}

func TestVersionsAreImmutable(t *testing.T) {
	ctx := t.Context()
	s, e, by := newStore(t)
	if _, err := s.Create(ctx, by, spec(t, "vo2max", func(*resolve.Rule) {}), "", true); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{"UPDATE resolution_rules SET note = 'x'", "DELETE FROM resolution_rules"} {
		if err := e.exec(stmt); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s: %v", stmt, err)
		}
	}
}
