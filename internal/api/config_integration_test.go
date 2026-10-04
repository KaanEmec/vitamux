//go:build integration

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/connectors/withings"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

// cfgEnv is a synthetic owner with a Withings connection (and its default schedules) and a
// second owner, behind the real router with a connector runtime. Requests carry an admin API
// key principal; access itself is covered by the auth tests.
type cfgEnv struct {
	t     *testing.T
	h     http.Handler
	exec  func(sql string, args ...any)
	scan  func(dest any, sql string, args ...any)
	user  uuid.UUID
	other uuid.UUID
	conn  uuid.UUID
	cid   string // conn_ form of conn
}

func newCfgEnv(t *testing.T) *cfgEnv {
	t.Helper()
	u, app := dbtest.Migrated(t)
	ownerPool := dbtest.Pool(t, u, db.OwnerRole)
	d := db.New(app)
	keyPath := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.WriteKeyFile(keyPath); err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.Load(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.Open(filepath.Join(t.TempDir(), "blobs"), kr)
	if err != nil {
		t.Fatal(err)
	}
	wc := withings.New(withings.Config{})
	reg, err := connectors.NewRegistry(wc)
	if err != nil {
		t.Fatal(err)
	}
	crt := connectors.New(connectors.Config{DB: d, Blobs: blobs, Keys: kr, Registry: reg})
	e := &cfgEnv{t: t, user: uuid.New(), other: uuid.New(), conn: uuid.New()}
	e.cid = ingest.FormatConnectionID(e.conn)
	e.exec = func(sql string, args ...any) {
		t.Helper()
		if _, err := ownerPool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	e.scan = func(dest any, sql string, args ...any) {
		t.Helper()
		if err := ownerPool.QueryRow(t.Context(), sql, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	for i, id := range []uuid.UUID{e.user, e.other} {
		e.exec(`INSERT INTO users (id, username, password_hash) VALUES ($1, $2, 'synthetic')`, id, fmt.Sprintf("owner%d", i))
	}
	e.exec(`INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES (gen_random_uuid(), $1, 'Europe/Amsterdam', '2020-01-01Z')`, e.user)
	e.exec(`INSERT INTO connections (id, user_id, provider_id, account_key, mode, status)
		VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'withings'), sha256($3::bytea), 'in_process', 'active')`, e.conn, e.user, e.conn[:])
	if err := d.Tx(t.Context(), func(q *dbq.Queries) error { return connectors.EnsureSchedules(t.Context(), q, e.conn, wc.Describe()) }); err != nil {
		t.Fatal(err)
	}
	rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), Options{DB: d, Blobs: blobs, Keys: kr, Connectors: crt,
		Withings: withings.NewNotifications(d, crt, wc, nil, nil)})
	if err != nil {
		t.Fatal(err)
	}
	e.h = rt.mux
	return e
}

// call sends body as user, checks the response against the spec operation pattern and the
// wanted status, and decodes it into v.
func (e *cfgEnv) call(user uuid.UUID, pattern, target, body string, want int, v any) {
	e.t.Helper()
	method, _, _ := strings.Cut(pattern, " ")
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := request(e.t, method, target, r)
	req.Header.Set("Content-Type", "application/json")
	p := &auth.Principal{Kind: auth.APIKey, UserID: user, ID: uuid.New(), Scopes: []auth.Scope{auth.Admin}}
	res := serve(e.t, e.h, req.WithContext(auth.WithPrincipal(req.Context(), p)))
	got := checkResponse(e.t, pattern, res)
	if res.StatusCode != want {
		e.t.Fatalf("%s %s: %d, want %d: %s", method, target, res.StatusCode, want, got)
	}
	if v != nil {
		if err := json.Unmarshal(got, v); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *cfgEnv) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	e.scan(&n, sql, args...)
	return n
}

// audited runs fn and asserts it wrote want audit events.
func (e *cfgEnv) audited(want int, fn func()) {
	e.t.Helper()
	before := e.count(`SELECT count(*) FROM audit_events`)
	fn()
	if got := e.count(`SELECT count(*) FROM audit_events`) - before; got != want {
		e.t.Fatalf("audit events: %d, want %d", got, want)
	}
}

const stepsSumRule = `{"schema": "vitamux.rule/1", "metric": "steps", "window": {"kind": "local_day"},
  "groups": [{"id": "watch", "match": [{"device_type": "watch"}]}, {"id": "phone", "match": [{"device_type": "phone"}]}],
  "within_source": {"daily_value_policy": "prefer_reported"},
  "strategy": {"op": "sum_across_sources", "min_sources": 2, "on_insufficient": "no_value"}%s}`

func TestRuleAndOverrideEndpoints(t *testing.T) {
	e := newCfgEnv(t)
	var rules struct{ Rules []oapi.RuleVersion }
	e.call(e.user, "GET /api/v1/rules", "/api/v1/rules", "", http.StatusOK, &rules)
	if len(rules.Rules) == 0 || !rules.Rules[0].Builtin || !rules.Rules[0].Active {
		t.Fatalf("rules: %+v", rules.Rules)
	}
	var versions struct{ Versions []oapi.RuleVersion }
	e.call(e.user, "GET /api/v1/rules/{metric}/versions", "/api/v1/rules/steps/versions", "", http.StatusOK, &versions)
	if len(versions.Versions) != 1 || !versions.Versions[0].Builtin {
		t.Fatalf("never edited: %+v", versions.Versions)
	}
	e.call(e.user, "GET /api/v1/rules/{metric}/versions", "/api/v1/rules/no_such_metric/versions", "", http.StatusNotFound, nil)

	create := "POST /api/v1/rules/{metric}/versions"
	e.audited(0, func() {
		e.call(e.user, create, "/api/v1/rules/steps/versions", `{"spec": `+fmt.Sprintf(stepsSumRule, "")+`}`, http.StatusConflict, nil)
		e.call(e.user, create, "/api/v1/rules/heart_rate/versions", `{"spec": `+fmt.Sprintf(stepsSumRule, "")+`}`, http.StatusUnprocessableEntity, nil)
		e.call(e.user, create, "/api/v1/rules/steps/versions", `{"spec": {"schema": "vitamux.rule/1", "metric": "steps"}}`, http.StatusUnprocessableEntity, nil)
	})
	var v oapi.RuleVersion
	acked := fmt.Sprintf(stepsSumRule, `, "acknowledged_warnings": ["cross_source_sum_duplicate_risk"]`)
	e.audited(3, func() { // copy of the built-in as version 1, version 2, activation
		e.call(e.user, create, "/api/v1/rules/steps/versions", `{"spec": `+acked+`, "note": "sum watch and phone", "activate": true}`, http.StatusCreated, &v)
	})
	if v.Ref != "rule:steps:2" || !v.Active || v.Note == nil || *v.Note != "sum watch and phone" {
		t.Fatalf("created: %+v", v)
	}
	e.audited(1, func() {
		e.call(e.user, "POST /api/v1/rules/{metric}/activate", "/api/v1/rules/steps/activate", `{"version": 1}`, http.StatusOK, &v)
	})
	if v.Version != 1 || !v.Active {
		t.Fatalf("activated: %+v", v)
	}
	e.call(e.user, "POST /api/v1/rules/{metric}/activate", "/api/v1/rules/steps/activate", `{"version": 9}`, http.StatusNotFound, nil)
	e.call(e.other, "GET /api/v1/rules/{metric}/versions", "/api/v1/rules/steps/versions", "", http.StatusOK, &versions)
	if len(versions.Versions) != 1 || !versions.Versions[0].Builtin {
		t.Fatalf("another owner sees: %+v", versions.Versions)
	}

	force := `{"metric": "steps", "window": {"kind": "local_day", "key": "2026-09-14", "local_date": "2026-09-14"}, "action": "force_source", "group": "watch"}`
	var o oapi.Override
	e.audited(1, func() { e.call(e.user, "POST /api/v1/overrides", "/api/v1/overrides", force, http.StatusCreated, &o) })
	if o.Group == nil || *o.Group != "watch" || !o.Active || o.Value != nil {
		t.Fatalf("override: %+v", o)
	}
	e.audited(0, func() {
		e.call(e.user, "POST /api/v1/overrides", "/api/v1/overrides", force, http.StatusConflict, nil)
		e.call(e.user, "POST /api/v1/overrides", "/api/v1/overrides", strings.Replace(force, `"group": "watch"`, `"value": 1`, 1), http.StatusUnprocessableEntity, nil)
	})
	setValue := `{"metric": "steps", "window": {"kind": "local_day", "key": "2026-09-15", "local_date": "2026-09-15"}, "action": "set_value", "value": 9000, "unit": "count", "note": "forgot the watch"}`
	e.call(e.user, "POST /api/v1/overrides", "/api/v1/overrides", setValue, http.StatusCreated, nil)
	var page oapi.OverridePage
	e.call(e.user, "GET /api/v1/overrides", "/api/v1/overrides?metric=steps&limit=1", "", http.StatusOK, &page)
	if len(page.Overrides) != 1 || !page.HasMore || page.NextCursor == nil || page.Overrides[0].Action != oapi.OverrideActionSetValue {
		t.Fatalf("page 1: %+v", page)
	}
	e.call(e.user, "GET /api/v1/overrides", "/api/v1/overrides?metric=steps&limit=1&cursor="+url.QueryEscape(*page.NextCursor), "", http.StatusOK, &page)
	if len(page.Overrides) != 1 || page.HasMore || page.Overrides[0].ID != o.ID {
		t.Fatalf("page 2: %+v", page)
	}
	e.call(e.user, "GET /api/v1/overrides", "/api/v1/overrides?metric=heart_rate&cursor="+url.QueryEscape("x"), "", http.StatusUnprocessableEntity, nil)
	revoke := "POST /api/v1/overrides/{id}/revoke"
	e.call(e.other, revoke, "/api/v1/overrides/"+o.ID.String()+"/revoke", "", http.StatusNotFound, nil)
	e.audited(1, func() { e.call(e.user, revoke, "/api/v1/overrides/"+o.ID.String()+"/revoke", "", http.StatusOK, &o) })
	if o.Active || o.RevokedAt == nil {
		t.Fatalf("revoked: %+v", o)
	}
	e.call(e.user, revoke, "/api/v1/overrides/"+o.ID.String()+"/revoke", "", http.StatusConflict, nil)
}

func TestConnectionEndpoints(t *testing.T) {
	e := newCfgEnv(t)
	one := "/api/v1/connections/" + e.cid
	var list struct{ Connections []oapi.Connection }
	e.call(e.user, "GET /api/v1/connections", "/api/v1/connections", "", http.StatusOK, &list)
	if len(list.Connections) != 1 || list.Connections[0].ID != e.cid || list.Connections[0].Official == nil || !*list.Connections[0].Official ||
		list.Connections[0].Health != oapi.Health(connectors.HealthOK) || ptrVal(list.Connections[0].HealthReason) != connectors.ReasonAwaitingFirstSync {
		t.Fatalf("connections: %+v", list.Connections)
	}
	e.call(e.other, "GET /api/v1/connections/{id}", one, "", http.StatusNotFound, nil)
	e.call(e.user, "GET /api/v1/connections/{id}", "/api/v1/connections/not-an-id", "", http.StatusNotFound, nil)

	var push oapi.Connection
	e.audited(1, func() {
		e.call(e.user, "POST /api/v1/connections", "/api/v1/connections", `{"provider": "file_import"}`, http.StatusCreated, &push)
	})
	if push.Mode != oapi.Push || push.Official != nil || push.Status != oapi.ConnectionStatusActive {
		t.Fatalf("push connection: %+v", push)
	}
	for _, p := range []string{"withings", "manual", "nope"} {
		e.call(e.user, "POST /api/v1/connections", "/api/v1/connections", `{"provider": "`+p+`"}`, http.StatusUnprocessableEntity, nil)
	}

	// Pause and resume; pausing twice changes (and audits) nothing.
	var c oapi.Connection
	e.audited(1, func() {
		e.call(e.user, "PATCH /api/v1/connections/{id}", one, `{"status": "paused"}`, http.StatusOK, &c)
		e.call(e.user, "PATCH /api/v1/connections/{id}", one, `{"status": "paused"}`, http.StatusOK, &c)
	})
	if c.Status != oapi.ConnectionStatusPaused || c.Health != oapi.Health(connectors.HealthPaused) {
		t.Fatalf("paused: %+v", c)
	}
	e.call(e.user, "POST /api/v1/connections/{id}/sync", one+"/sync", "", http.StatusConflict, nil)
	e.call(e.user, "PATCH /api/v1/connections/{id}", one, `{"status": "active"}`, http.StatusOK, &c)
	e.exec(`UPDATE connections SET status = 'needs_reauth' WHERE id = $1`, e.conn)
	e.call(e.user, "PATCH /api/v1/connections/{id}", one, `{"status": "paused"}`, http.StatusConflict, nil)
	e.call(e.user, "POST /api/v1/connections/{id}/sync", one+"/sync", "", http.StatusConflict, nil)
	e.exec(`UPDATE connections SET status = 'active' WHERE id = $1`, e.conn)

	// Two quick manual syncs queue one job per Withings stream; both requests are audited.
	var s1, s2 oapi.SyncQueued
	e.audited(2, func() {
		e.call(e.user, "POST /api/v1/connections/{id}/sync", one+"/sync", "", http.StatusAccepted, &s1)
		e.call(e.user, "POST /api/v1/connections/{id}/sync", one+"/sync", "", http.StatusAccepted, &s2)
	})
	if len(s1.Jobs) != 4 || len(s2.Jobs) != 4 || s1.Jobs[0].ID != s2.Jobs[0].ID || s1.Jobs[0].Status != oapi.JobStatusQueued {
		t.Fatalf("syncs: %+v / %+v", s1, s2)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE connection_id = $1 AND kind = 'sync'`, e.conn); n != 4 {
		t.Fatalf("sync jobs: %d, want 4", n)
	}
	e.call(e.user, "POST /api/v1/connections/{id}/sync", "/api/v1/connections/"+push.ID+"/sync", "", http.StatusConflict, nil)

	// Streams and the cursor reset, refused while a job of the connection runs.
	e.exec(`INSERT INTO sync_cursors (connection_id, stream, cursor, high_watermark) VALUES ($1, 'withings.measures', '{"lastupdate": 1}', now())`, e.conn)
	var streams struct{ Streams []oapi.Stream }
	e.call(e.user, "GET /api/v1/connections/{id}/streams", one+"/streams", "", http.StatusOK, &streams)
	cursors := 0
	for _, st := range streams.Streams {
		if st.HasCursor {
			cursors++
		}
		if len(st.Schedules) != 2 {
			t.Fatalf("streams: %+v", streams.Streams)
		}
	}
	if len(streams.Streams) != 4 || cursors != 1 {
		t.Fatalf("streams: %+v", streams.Streams)
	}
	reset := "POST /api/v1/connections/{id}/streams/{stream}/reset-cursor"
	e.exec(`UPDATE jobs SET status = 'running', lease_owner = 'synthetic', lease_expires_at = now() + interval '1 hour', started_at = now() WHERE id = $1`, s1.Jobs[0].ID)
	e.audited(0, func() {
		e.call(e.user, reset, one+"/streams/withings.measures/reset-cursor", "", http.StatusConflict, nil)
		e.call(e.user, "DELETE /api/v1/connections/{id}", one+"?data=delete", "", http.StatusConflict, nil)
	})
	e.exec(`UPDATE jobs SET status = 'succeeded', finished_at = now() WHERE id = $1`, s1.Jobs[0].ID)
	e.exec(`INSERT INTO job_runs (job_id, attempt, finished_at, outcome) VALUES ($1, 1, now(), 'succeeded')`, s1.Jobs[0].ID)
	var st oapi.Stream
	e.audited(1, func() { e.call(e.user, reset, one+"/streams/withings.measures/reset-cursor", "", http.StatusOK, &st) })
	if st.HasCursor || st.HighWatermark != nil {
		t.Fatalf("after reset: %+v", st)
	}
	e.call(e.user, reset, one+"/streams/nope/reset-cursor", "", http.StatusNotFound, nil)
	var runs oapi.RunPage
	e.call(e.user, "GET /api/v1/connections/{id}/runs", one+"/runs", "", http.StatusOK, &runs)
	if len(runs.Runs) != 1 || ptrVal(runs.Runs[0].Outcome) != "succeeded" || runs.Runs[0].Kind != "sync" {
		t.Fatalf("runs: %+v", runs)
	}

	// Backfills: create, list, get with units, cancel; nothing left to retry.
	var b oapi.Backfill
	start := time.Now().Add(-45 * 24 * time.Hour).UTC().Format(time.RFC3339)
	e.audited(1, func() {
		e.call(e.user, "POST /api/v1/connections/{id}/backfills", one+"/backfills", `{"stream": "withings.measures", "start": "`+start+`"}`, http.StatusAccepted, &b)
	})
	if b.Status != oapi.BackfillStatusRunning || b.UnitCounts.Pending != 2 {
		t.Fatalf("backfill: %+v", b)
	}
	e.call(e.user, "POST /api/v1/connections/{id}/backfills", one+"/backfills", `{"stream": "nope", "start": "`+start+`"}`, http.StatusUnprocessableEntity, nil)
	var bl struct{ Backfills []oapi.Backfill }
	e.call(e.user, "GET /api/v1/connections/{id}/backfills", one+"/backfills", "", http.StatusOK, &bl)
	if len(bl.Backfills) != 1 || bl.Backfills[0].ID != b.ID {
		t.Fatalf("backfills: %+v", bl)
	}
	bOne := one + "/backfills/" + b.ID.String()
	e.call(e.user, "GET /api/v1/connections/{id}/backfills/{backfill_id}", bOne, "", http.StatusOK, &b)
	if b.Units == nil || len(*b.Units) != 2 {
		t.Fatalf("units: %+v", b.Units)
	}
	e.call(e.user, "GET /api/v1/connections/{id}/backfills/{backfill_id}", "/api/v1/connections/"+push.ID+"/backfills/"+b.ID.String(), "", http.StatusNotFound, nil)
	e.audited(1, func() {
		e.call(e.user, "POST /api/v1/connections/{id}/backfills/{backfill_id}/cancel", bOne+"/cancel", "", http.StatusOK, &b)
	})
	if b.Status != oapi.BackfillStatusCancelled {
		t.Fatalf("cancelled: %+v", b)
	}
	e.audited(0, func() {
		e.call(e.user, "POST /api/v1/connections/{id}/backfills/{backfill_id}/cancel", bOne+"/cancel", "", http.StatusConflict, nil)
		e.call(e.user, "POST /api/v1/connections/{id}/backfills/{backfill_id}/retry", bOne+"/retry", "", http.StatusConflict, nil)
	})

	// data=keep disables and drops credentials; data=delete removes the connection.
	e.audited(1, func() {
		e.call(e.user, "DELETE /api/v1/connections/{id}", "/api/v1/connections/"+push.ID+"?data=keep", "", http.StatusNoContent, nil)
	})
	e.call(e.user, "GET /api/v1/connections/{id}", "/api/v1/connections/"+push.ID, "", http.StatusOK, &c)
	if c.Status != oapi.ConnectionStatusDisabled {
		t.Fatalf("kept: %+v", c)
	}
	e.call(e.user, "DELETE /api/v1/connections/{id}", one+"?data=everything", "", http.StatusUnprocessableEntity, nil)
	e.call(e.other, "DELETE /api/v1/connections/{id}", one+"?data=delete", "", http.StatusNotFound, nil)
	e.audited(1, func() {
		e.call(e.user, "DELETE /api/v1/connections/{id}", one+"?data=delete", "", http.StatusNoContent, nil)
	})
	if n := e.count(`SELECT count(*) FROM jobs WHERE connection_id = $1`, e.conn) + e.count(`SELECT count(*) FROM schedules WHERE connection_id = $1`, e.conn) +
		e.count(`SELECT count(*) FROM backfills WHERE connection_id = $1`, e.conn) + e.count(`SELECT count(*) FROM sync_cursors WHERE connection_id = $1`, e.conn); n != 0 {
		t.Fatalf("%d rows of the deleted connection left", n)
	}
	e.call(e.user, "GET /api/v1/connections/{id}", one, "", http.StatusNotFound, nil)
}

func TestManualMeasurementAndDeleteWithData(t *testing.T) {
	e := newCfgEnv(t)
	manual := "POST /api/v1/measurements/manual"
	var m oapi.Measurement
	e.audited(1, func() {
		e.call(e.user, manual, "/api/v1/measurements/manual", `{"metric": "weight", "value": 176.4, "unit": "lb", "start_at": "2026-09-14T07:30:00+02:00"}`, http.StatusCreated, &m)
	})
	if m.Metric != "weight" || m.Unit != "kg" || math.Abs(m.Value-80.0136) > 0.001 || ptrVal(m.SourceUnit) != "lb" || m.QualityFlags&1 == 0 ||
		m.Source.Provider != "manual" || m.GroupID == nil || m.LocalDate.String() != "2026-09-14" || ptrVal(m.TzOffsetMin) != 120 ||
		m.Provenance.RawPayloadID == nil || m.Provenance.Raw == nil || m.Provenance.Raw.Stream != "manual.measurements" || m.Provenance.Normalizer != "manual.measurements@1" {
		t.Fatalf("manual weight: %+v", m)
	}
	e.call(e.user, manual, "/api/v1/measurements/manual", `{"metric": "steps", "value": 4200, "unit": "count", "start_at": "2026-09-14T09:00:00-04:00", "end_at": "2026-09-14T10:00:00-04:00"}`, http.StatusCreated, &m)
	if m.Kind != oapi.MeasurementKindInterval || m.GroupID != nil || m.Source.ConnectionID == "" {
		t.Fatalf("manual steps: %+v", m)
	}
	e.audited(0, func() {
		for _, body := range []string{
			`{"metric": "bp_systolic", "value": 120, "unit": "mmHg", "start_at": "2026-09-14T07:30:00Z"}`,
			`{"metric": "no_such_metric", "value": 1, "unit": "count", "start_at": "2026-09-14T07:30:00Z"}`,
			`{"metric": "weight", "value": 80, "unit": "count", "start_at": "2026-09-14T07:30:00Z"}`,
			`{"metric": "steps", "value": 10, "unit": "count", "start_at": "2026-09-14T07:30:00Z"}`, // steps are intervals
		} {
			e.call(e.user, manual, "/api/v1/measurements/manual", body, http.StatusUnprocessableEntity, nil)
		}
	})
	conn, err := ingest.ParseConnectionID(m.Source.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM connections c JOIN providers p ON p.id = c.provider_id WHERE p.code = 'manual' AND c.user_id = $1`, e.user); n != 1 {
		t.Fatalf("manual connections: %d, want 1", n)
	}
	if n := e.count(`SELECT count(*) FROM raw_payloads WHERE connection_id = $1 AND status = 'normalized'`, conn); n != 2 {
		t.Fatalf("normalized manual raw payloads: %d, want 2", n)
	}

	// Deleting the manual connection with its data removes rows, raw payloads and blob references.
	var hashes int
	e.scan(&hashes, `SELECT count(DISTINCT content_sha256) FROM raw_payloads WHERE connection_id = $1`, conn)
	e.exec(`DELETE FROM resolution_dirty`)
	e.audited(1, func() {
		e.call(e.user, "DELETE /api/v1/connections/{id}", "/api/v1/connections/"+m.Source.ConnectionID+"?data=delete", "", http.StatusNoContent, nil)
	})
	for table, n := range map[string]int{
		"measurements":       e.count(`SELECT count(*) FROM measurements WHERE connection_id = $1`, conn),
		"measurement_groups": e.count(`SELECT count(*) FROM measurement_groups WHERE connection_id = $1`, conn),
		"raw_payloads":       e.count(`SELECT count(*) FROM raw_payloads WHERE connection_id = $1`, conn),
		"ingest_batches":     e.count(`SELECT count(*) FROM ingest_batches WHERE connection_id = $1`, conn),
		"connections":        e.count(`SELECT count(*) FROM connections WHERE id = $1`, conn),
		"blob references":    e.count(`SELECT coalesce(sum(refcount), 0) FROM blobs`),
	} {
		if n != 0 {
			t.Errorf("%s: %d left", table, n)
		}
	}
	if hashes != 2 || e.count(`SELECT count(*) FROM resolution_dirty WHERE local_date = '2026-09-14'`) != 2 {
		t.Fatalf("dirty days after delete: %d (hashes %d)", e.count(`SELECT count(*) FROM resolution_dirty`), hashes)
	}
	var detail string
	e.scan(&detail, `SELECT detail::text FROM audit_events WHERE action = 'connection.deleted'`)
	if !strings.Contains(detail, `"raw_payloads": 2`) || strings.Contains(detail, "176") {
		t.Fatalf("audit detail: %s", detail)
	}
}

func TestScheduleJobTimezoneSettingEndpoints(t *testing.T) {
	e := newCfgEnv(t)
	var scheds struct{ Schedules []oapi.Schedule }
	e.call(e.user, "GET /api/v1/schedules", "/api/v1/schedules?connection="+e.cid, "", http.StatusOK, &scheds)
	if len(scheds.Schedules) != 8 {
		t.Fatalf("schedules: %+v", scheds)
	}
	e.call(e.user, "GET /api/v1/schedules", "/api/v1/schedules?connection=bad", "", http.StatusUnprocessableEntity, nil)
	e.call(e.other, "GET /api/v1/schedules", "/api/v1/schedules", "", http.StatusOK, &scheds)
	if len(scheds.Schedules) != 0 {
		t.Fatalf("another owner sees schedules: %+v", scheds)
	}
	var inc oapi.Schedule
	e.call(e.user, "GET /api/v1/schedules", "/api/v1/schedules", "", http.StatusOK, &scheds)
	for _, s := range scheds.Schedules {
		if s.Mode == oapi.Incremental {
			inc = s
		}
	}
	patch := "PATCH /api/v1/schedules/{id}"
	var s oapi.Schedule
	e.audited(1, func() {
		e.call(e.user, patch, "/api/v1/schedules/"+inc.ID.String(), `{"interval_seconds": 7200, "enabled": false}`, http.StatusOK, &s)
	})
	if s.IntervalSeconds != 7200 || s.Enabled || s.LookbackSeconds != inc.LookbackSeconds {
		t.Fatalf("patched: %+v", s)
	}
	e.audited(0, func() {
		e.call(e.user, patch, "/api/v1/schedules/"+inc.ID.String(), `{"interval_seconds": 30}`, http.StatusUnprocessableEntity, nil)
		e.call(e.other, patch, "/api/v1/schedules/"+inc.ID.String(), `{"enabled": true}`, http.StatusNotFound, nil)
	})

	// Jobs: newest first, paged, scoped to the owner's connections.
	for range 2 {
		e.exec(`INSERT INTO jobs (id, kind, connection_id) VALUES (gen_random_uuid(), 'sync', $1)`, e.conn)
	}
	var jp oapi.JobPage
	e.call(e.user, "GET /api/v1/jobs", "/api/v1/jobs?status=queued&limit=1", "", http.StatusOK, &jp)
	if len(jp.Jobs) != 1 || !jp.HasMore || jp.Jobs[0].ConnectionID == nil || *jp.Jobs[0].ConnectionID != e.cid {
		t.Fatalf("jobs page 1: %+v", jp)
	}
	first := jp.Jobs[0].ID
	e.call(e.user, "GET /api/v1/jobs", "/api/v1/jobs?status=queued&limit=1&cursor="+url.QueryEscape(*jp.NextCursor), "", http.StatusOK, &jp)
	if len(jp.Jobs) != 1 || jp.HasMore || jp.Jobs[0].ID == first {
		t.Fatalf("jobs page 2: %+v", jp)
	}
	e.call(e.user, "GET /api/v1/jobs", "/api/v1/jobs?status=bogus", "", http.StatusUnprocessableEntity, nil)
	e.call(e.other, "GET /api/v1/jobs", "/api/v1/jobs", "", http.StatusOK, &jp)
	if len(jp.Jobs) != 0 {
		t.Fatalf("another owner sees jobs: %+v", jp)
	}

	// Timezone periods: add, conflict, bad zone, edit, remove; each change is audited and
	// queues the local-date recompute.
	tzs := "/api/v1/timezone-periods"
	var p oapi.TimezonePeriod
	e.audited(1, func() {
		e.call(e.user, "POST /api/v1/timezone-periods", tzs, `{"tz": "America/New_York", "valid_from": "2026-06-01T00:00:00Z"}`, http.StatusCreated, &p)
	})
	if p.Tz != "America/New_York" || p.ValidTo != nil {
		t.Fatalf("period: %+v", p)
	}
	e.audited(0, func() {
		e.call(e.user, "POST /api/v1/timezone-periods", tzs, `{"tz": "Europe/Paris", "valid_from": "2026-06-01T00:00:00Z"}`, http.StatusConflict, nil)
		e.call(e.user, "POST /api/v1/timezone-periods", tzs, `{"tz": "Mars/Olympus", "valid_from": "2026-07-01T00:00:00Z"}`, http.StatusUnprocessableEntity, nil)
		e.call(e.other, "PATCH /api/v1/timezone-periods/{id}", tzs+"/"+p.ID.String(), `{"tz": "Europe/Paris", "valid_from": "2026-06-01T00:00:00Z"}`, http.StatusNotFound, nil)
	})
	var periods struct {
		TimezonePeriods []oapi.TimezonePeriod `json:"timezone_periods"`
	}
	e.call(e.user, "GET /api/v1/timezone-periods", tzs, "", http.StatusOK, &periods)
	if len(periods.TimezonePeriods) != 2 || periods.TimezonePeriods[0].ValidTo == nil || !periods.TimezonePeriods[0].ValidTo.Equal(p.ValidFrom) {
		t.Fatalf("periods: %+v", periods)
	}
	e.audited(1, func() {
		e.call(e.user, "PATCH /api/v1/timezone-periods/{id}", tzs+"/"+p.ID.String(), `{"tz": "Asia/Tokyo", "valid_from": "2026-06-01T00:00:00Z"}`, http.StatusOK, &p)
	})
	if p.Tz != "Asia/Tokyo" {
		t.Fatalf("edited: %+v", p)
	}
	e.audited(1, func() {
		e.call(e.user, "DELETE /api/v1/timezone-periods/{id}", tzs+"/"+p.ID.String(), "", http.StatusNoContent, nil)
	})
	e.call(e.user, "DELETE /api/v1/timezone-periods/{id}", tzs+"/"+p.ID.String(), "", http.StatusNotFound, nil)
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = 'recompute_local_dates'`); n == 0 {
		t.Fatal("no local-date recompute queued")
	}

	// Settings: the Withings notifications toggle (no public URL here, so it cannot turn on).
	var set oapi.Settings
	e.call(e.user, "GET /api/v1/settings", "/api/v1/settings", "", http.StatusOK, &set)
	if set.WithingsNotifications == nil || *set.WithingsNotifications {
		t.Fatalf("settings: %+v", set)
	}
	e.audited(1, func() {
		e.call(e.user, "PATCH /api/v1/settings", "/api/v1/settings", `{"withings.notifications": false}`, http.StatusOK, &set)
	})
	e.audited(0, func() {
		e.call(e.user, "PATCH /api/v1/settings", "/api/v1/settings", `{"withings.notifications": true}`, http.StatusUnprocessableEntity, nil)
		e.call(e.user, "PATCH /api/v1/settings", "/api/v1/settings", `{}`, http.StatusOK, &set)
	})
}
