//go:build integration

package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/db/fixtureload"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// resolvedEnv is a tools/fixturegen slice (the synthetic persona, fixtures/README.md) behind the
// real router.
type resolvedEnv struct {
	t     *testing.T
	h     http.Handler
	d     *db.DB
	user  uuid.UUID
	count func(table string) int
	exec  func(sql string) // as the owner role
}

func newResolvedEnv(t *testing.T, start string, days int) *resolvedEnv {
	t.Helper()
	ctx := t.Context()
	dir := t.TempDir()
	_, file, _, _ := runtime.Caller(0)
	cmd := exec.CommandContext(ctx, "go", "run", "./tools/fixturegen", "-out", dir, "-start", start, "-days", strconv.Itoa(days), "-hr-step", "60")
	cmd.Dir = filepath.Join(filepath.Dir(file), "..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixturegen: %v\n%s", err, out)
	}
	u, app := dbtest.Migrated(t)
	stats, err := fixtureload.Load(ctx, app, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct{ from, tz string }{
		{"2024-01-01T00:00:00Z", "Europe/Berlin"}, {"2025-05-11T22:00:00Z", "America/New_York"}, {"2025-05-22T04:00:00Z", "Europe/Berlin"},
	} {
		if _, err := app.Exec(ctx, `INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES ($1, $2, $3, $4)`, uuid.New(), stats.UserID, p.tz, p.from); err != nil {
			t.Fatal(err)
		}
	}
	owner := dbtest.Pool(t, u, db.OwnerRole)
	d := db.New(app)
	rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), Options{DB: d})
	if err != nil {
		t.Fatal(err)
	}
	return &resolvedEnv{t: t, h: rt.mux, d: d, user: stats.UserID, exec: func(sql string) {
		if _, err := owner.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}, count: func(table string) int {
		var n int
		if err := owner.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}}
}

// call sends a request as the owner's admin API key, checks it against the spec operation and the
// status, and decodes the body into v; it returns the body.
func (e *resolvedEnv) call(pattern, target, body string, want int, v any) []byte {
	e.t.Helper()
	method, _, _ := strings.Cut(pattern, " ")
	var req *http.Request
	if body != "" {
		req = request(e.t, method, target, strings.NewReader(body))
	} else {
		req = request(e.t, method, target, nil)
	}
	p := &auth.Principal{Kind: auth.APIKey, UserID: e.user, ID: uuid.New(), Scopes: []auth.Scope{auth.Admin}}
	res := serve(e.t, e.h, req.WithContext(auth.WithPrincipal(req.Context(), p)))
	out := checkResponse(e.t, pattern, res)
	if res.StatusCode != want {
		e.t.Fatalf("%s %s: %d, want %d: %s", method, target, res.StatusCode, want, out)
	}
	if v != nil {
		if err := json.Unmarshal(out, v); err != nil {
			e.t.Fatal(err)
		}
	}
	return out
}

const (
	daily   = "GET /api/v1/resolved/daily"
	series  = "GET /api/v1/resolved/series"
	drill   = "GET /api/v1/resolved/{metric}/{window_key}/sources"
	preview = "POST /api/v1/resolution/preview"
)

// stepsMax is the owner rule of the api.md example: the maximum of three sources per local day.
const stepsMax = `{"schema": "vitamux.rule/1", "metric": "steps", "window": {"kind": "local_day"},
	"groups": [{"id": "garmin", "match": [{"provider": "garmin"}]},
		{"id": "apple_watch", "match": [{"provider": "apple_health", "device_type": "watch", "relayed": false}]},
		{"id": "withings", "match": [{"provider": "withings"}]}],
	"exclude": [{"provider": "apple_health", "relayed": true}],
	"within_source": {"daily_value_policy": "prefer_reported"},
	"strategy": {"op": "maximum_across_sources"}}`

// TestResolvedDocExamples produces the api.md examples from a fixturegen slice: each example is
// part of the real response (docExample), and the response matches the spec.
func TestResolvedDocExamples(t *testing.T) {
	e := newResolvedEnv(t, "2025-09-13", 3)
	e.call("POST /api/v1/rules/{metric}/versions", "/api/v1/rules/steps/versions", `{"activate": true, "spec": `+stepsMax+`}`, http.StatusCreated, nil)

	md, err := os.ReadFile("../../docs/architecture/api.md")
	if err != nil {
		t.Fatal(err)
	}
	for heading, pattern := range map[string]string{"## Example: resolved day": daily, "## Example: all-sources drilldown": drill} {
		_, rest, _ := strings.Cut(string(md), heading)
		_, rest, _ = strings.Cut(rest, "`GET ")
		target, rest, _ := strings.Cut(rest, "`")
		_, rest, _ = strings.Cut(rest, "```json\n")
		example, _, _ := strings.Cut(rest, "```")
		body := e.call(pattern, target, "", http.StatusOK, nil)
		var want, got any
		if err := json.Unmarshal([]byte(example), &want); err != nil {
			t.Fatalf("%s: %v", heading, err)
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if at := docExample(want, got, ""); at != "" {
			t.Errorf("%s is not part of the response at %s; response:\n%s", heading, at, body)
		}
	}
}

// docExample returns where want (a documentation example) is not part of got, or "": objects by
// their keys, arrays as an ordered subsequence, numbers and strings exactly except that a string
// with "…" stands for anything starting with what precedes it.
func docExample(want, got any, at string) string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return at
		}
		for k, v := range w {
			if p := docExample(v, g[k], at+"/"+k); p != "" {
				return p
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok {
			return at
		}
		j := 0
		for i, v := range w {
			for j < len(g) && docExample(v, g[j], "") != "" {
				j++
			}
			if j == len(g) {
				return fmt.Sprintf("%s/%d", at, i)
			}
			j++
		}
	case string:
		g, ok := got.(string)
		if prefix, _, cut := strings.Cut(w, "…"); cut {
			if !ok || !strings.HasPrefix(g, prefix) {
				return at
			}
		} else if g != w {
			return at
		}
	default:
		if fmt.Sprint(want) != fmt.Sprint(got) {
			return at
		}
	}
	return ""
}

func TestResolvedDaily(t *testing.T) {
	e := newResolvedEnv(t, "2025-09-13", 3)
	var d oapi.ResolvedDaily
	// Comma-separated and repeated metrics; a family, a sleep code and a night metric.
	for _, q := range []string{"metrics=steps,blood_pressure,sleep_total,spo2", "metrics=steps&metrics=blood_pressure&metrics=sleep_total,spo2"} {
		e.call(daily, "/api/v1/resolved/daily?start_date=2025-09-13&end_date=2025-09-15&"+q, "", http.StatusOK, &d)
		if d.Timezone != "Europe/Berlin" || len(d.Days) != 3 || len(d.Days[1].Metrics) != 4 {
			t.Fatalf("%s: %+v", q, d)
		}
	}
	day := d.Days[1].Metrics
	if s := day["steps"]; s.Window.Kind != "local_day" || s.Value == nil || s.Links == nil {
		t.Errorf("steps: %+v", s)
	}
	if bp, ok := day["blood_pressure"].Value.(map[string]any); !ok || bp["bp_systolic"] == nil {
		t.Errorf("blood pressure: %+v", day["blood_pressure"])
	}
	if s := day["sleep_total"]; s.Window.Kind != "local_night" || s.Value == nil {
		t.Errorf("sleep_total: %+v", s)
	}
	if s := day["spo2"]; s.Window.Kind != "local_night" {
		t.Errorf("spo2: %+v", s)
	}
	// The closed dates went to the cache; a second read gives the same values.
	if n := e.count("resolved_cache"); n == 0 {
		t.Fatal("daily values were not cached")
	}
	var again oapi.ResolvedDaily
	e.call(daily, "/api/v1/resolved/daily?start_date=2025-09-13&end_date=2025-09-15&metrics=steps,blood_pressure,sleep_total,spo2", "", http.StatusOK, &again)
	if a, b := fmt.Sprint(again.Days[1].Metrics["steps"].Value), fmt.Sprint(day["steps"].Value); a != b {
		t.Errorf("cached steps %s, live %s", a, b)
	}

	// Every metric with a rule; a metric without one is no_data with its reason.
	e.call(daily, "/api/v1/resolved/daily?start_date=2025-09-14&end_date=2025-09-14", "", http.StatusOK, &d)
	if _, ok := d.Days[0].Metrics["sleep"]; !ok || len(d.Days[0].Metrics) < 10 {
		t.Errorf("all metrics: %d", len(d.Days[0].Metrics))
	}
	e.call(daily, "/api/v1/resolved/daily?start_date=2025-09-14&end_date=2025-09-14&metrics=skin_temperature", "", http.StatusOK, &d)
	if v := d.Days[0].Metrics["skin_temperature"]; v.Status != "no_data" || !strings.Contains(v.Explanation, "No rule") {
		t.Errorf("skin_temperature: %+v", v)
	}
	for _, target := range []string{
		"/api/v1/resolved/daily?start_date=2025-09-14&end_date=2025-09-14&metrics=nope",
		"/api/v1/resolved/daily?start_date=2025-09-15&end_date=2025-09-14",
		"/api/v1/resolved/daily?start_date=2024-01-01&end_date=2025-09-14",
		"/api/v1/resolved/daily?end_date=2025-09-14",
	} {
		e.call(daily, target, "", http.StatusUnprocessableEntity, nil)
	}
}

func TestResolvedSeries(t *testing.T) {
	e := newResolvedEnv(t, "2025-09-13", 3)
	base := "/api/v1/resolved/series?metric=heart_rate&start=" + url.QueryEscape("2025-09-14T00:00:00+02:00") + "&end=" + url.QueryEscape("2025-09-15T00:00:00+02:00")
	var s oapi.ResolvedSeries
	e.call(series, base+"&window=hour", "", http.StatusOK, &s)
	if len(s.Points) != 24 || s.HasMore || s.Window.Kind != "hour" || len(s.SourcesUsed) == 0 || s.Points[0].Start.Format(time.RFC3339) != "2025-09-14T00:00:00+02:00" {
		t.Fatalf("hours: %d points, %+v", len(s.Points), s.Window)
	}
	all := s.Points
	// Pages of 10 add up to the same points.
	var paged []oapi.ResolvedPoint
	target := base + "&window=hour&limit=10"
	for range 5 {
		e.call(series, target, "", http.StatusOK, &s)
		paged = append(paged, s.Points...)
		if !s.HasMore {
			break
		}
		target = base + "&window=hour&limit=10&cursor=" + url.QueryEscape(*s.NextCursor)
	}
	if len(paged) != len(all) || paged[23].Key != all[23].Key || fmt.Sprint(paged[10].Value) != fmt.Sprint(all[10].Value) {
		t.Fatalf("paged %d points, want %d", len(paged), len(all))
	}
	// The rule's 5-minute buckets, and another bucket size.
	e.call(series, base, "", http.StatusOK, &s)
	if len(s.Points) != 288 || s.Window.Kind != "bucket" || *s.Window.Size != "5m" {
		t.Errorf("rule window: %d points %+v", len(s.Points), s.Window)
	}
	e.call(series, base+"&window=15m", "", http.StatusOK, &s)
	if len(s.Points) != 96 || *s.Window.Size != "15m" || s.Points[0].Links == nil {
		t.Errorf("15m: %d points %+v", len(s.Points), s.Window)
	}
	e.call(series, base+"&window=local_day", "", http.StatusOK, &s)
	if len(s.Points) != 1 || s.Points[0].LocalDate.String() != "2025-09-14" {
		t.Errorf("local_day: %+v", s.Points)
	}
	e.call(series, base+"&window=hour&cursor=tampered", "", http.StatusUnprocessableEntity, nil)
	e.call(series, strings.Replace(base, "heart_rate", "steps", 1)+"&window=local_night", "", http.StatusUnprocessableEntity, nil)
	e.call(series, base+"&window=week", "", http.StatusUnprocessableEntity, nil)
}

func TestResolvedSleepAndDrilldown(t *testing.T) {
	e := newResolvedEnv(t, "2025-09-13", 3)
	var s oapi.ResolvedSleep
	e.call("GET /api/v1/resolved/sleep", "/api/v1/resolved/sleep?start_date=2025-09-14&end_date=2025-09-15", "", http.StatusOK, &s)
	if len(s.Nights) != 2 {
		t.Fatalf("nights: %+v", s)
	}
	n := s.Nights[0]
	selected := 0
	for _, m := range n.Members {
		if m.Selected {
			selected++
			if m.Group == nil || *m.Group != *n.Result.Selected || len(m.SessionRefs) == 0 {
				t.Errorf("selected member: %+v", m)
			}
		}
	}
	if n.Result.Status == "no_data" || selected != 1 || len(n.Members) < 2 {
		t.Errorf("night: %+v", n)
	}

	// Drilldowns: a day, an hour, a night; the sources match the result's inputs.
	var dd oapi.SourcesDrilldown
	e.call(drill, "/api/v1/resolved/steps/2025-09-14/sources", "", http.StatusOK, &dd)
	if dd.Window.Kind != "local_day" || len(dd.Sources) < 3 || dd.Sources[0].Records == nil || dd.Sources[0].Provenance == nil {
		t.Fatalf("steps day: %+v", dd)
	}
	e.call(drill, "/api/v1/resolved/heart_rate/2025-09-14T06:00:00Z/sources?window=hour", "", http.StatusOK, &dd)
	if dd.Window.Kind != "hour" || len(dd.Sources) == 0 {
		t.Fatalf("hour: %+v", dd)
	}
	e.call(drill, "/api/v1/resolved/heart_rate/"+url.PathEscape("2025-09-14T08:00:00+02:00")+"/sources?window=hour", "", http.StatusOK, &dd)
	if *dd.Window.Key != "2025-09-14T06:00:00Z" {
		t.Errorf("offset key: %+v", dd.Window)
	}
	e.call(drill, "/api/v1/resolved/sleep/2025-09-14/sources", "", http.StatusOK, &dd)
	if dd.Window.Kind != "local_night" || len(dd.Sources) == 0 || dd.Sources[0].SessionRefs == nil {
		t.Fatalf("night: %+v", dd)
	}
	e.call(drill, "/api/v1/resolved/skin_temperature/2025-09-14/sources", "", http.StatusOK, &dd)
	e.call(drill, "/api/v1/resolved/nope/2025-09-14/sources", "", http.StatusNotFound, nil)
	e.call(drill, "/api/v1/resolved/heart_rate/2025-09-14T06:07:00Z/sources?window=hour", "", http.StatusNotFound, nil)
	e.call(drill, "/api/v1/resolved/steps/2025-09-14/sources?window=local_night", "", http.StatusUnprocessableEntity, nil)
	e.call(drill, "/api/v1/resolved/steps/2025-09-14/sources?window=hour", "", http.StatusUnprocessableEntity, nil)
	e.call(drill, "/api/v1/resolved/steps/yesterday/sources", "", http.StatusUnprocessableEntity, nil)
}

// TestResolutionPreviewIsolation: a preview resolves the draft beside the rule in effect and
// writes nothing: no rule versions, no cache rows, no jobs, no audit events.
func TestResolutionPreviewIsolation(t *testing.T) {
	e := newResolvedEnv(t, "2025-09-13", 3)
	tables := []string{"resolved_cache", "resolution_rules", "active_rules", "jobs", "audit_events"}
	before := map[string]int{}
	for _, tb := range tables {
		before[tb] = e.count(tb)
	}
	var p oapi.ResolutionPreview
	e.call(preview, "/api/v1/resolution/preview", `{"spec": `+stepsMax+`, "start_date": "2025-09-13", "end_date": "2025-09-15"}`, http.StatusOK, &p)
	if p.Metric != "steps" || p.Window != "local_day" || p.DraftRule.Ref != "draft:steps" || p.ActiveRule == nil ||
		p.ActiveRule.Ref != "builtin:steps:2" || len(p.Days) != 3 {
		t.Fatalf("preview: %+v", p)
	}
	d := p.Days[1]
	if d.Draft.Rule.Ref != "draft:steps" || d.Active.Rule.Ref != "builtin:steps:2" || d.Draft.Value == nil || d.Active.Value == nil {
		t.Errorf("day: %+v", d)
	}
	// A follower draft resolves its leader (which reads the cache otherwise) live too.
	e.call(preview, "/api/v1/resolution/preview", `{"spec": {"schema": "vitamux.rule/1", "metric": "fat_mass", "window": {"kind": "local_day"},
		"follow": "weight", "groups": [{"id": "scale", "match": [{"device_type": "scale"}]}], "strategy": {"op": "first_available"}},
		"start_date": "2025-09-13", "end_date": "2025-09-15"}`, http.StatusOK, &p)
	e.call(preview, "/api/v1/resolution/preview", `{"spec": {"schema": "vitamux.rule/1", "metric": "sleep", "window": {"kind": "local_night"},
		"groups": [{"id": "apple_watch", "match": [{"provider": "apple_health"}]}], "strategy": {"op": "event_priority"}},
		"start_date": "2025-09-14", "end_date": "2025-09-14"}`, http.StatusOK, &p)
	if p.Window != "local_night" || len(p.Days) != 1 {
		t.Errorf("sleep preview: %+v", p)
	}
	for _, tb := range tables {
		if n := e.count(tb); n != before[tb] {
			t.Errorf("%s: %d rows after previews, %d before", tb, n, before[tb])
		}
	}

	// Invalid drafts answer like rule creation.
	sum := strings.Replace(stepsMax, "maximum_across_sources", "sum_across_sources", 1)
	e.call(preview, "/api/v1/resolution/preview", `{"spec": `+sum+`, "start_date": "2025-09-13", "end_date": "2025-09-15"}`, http.StatusConflict, nil)
	e.call(preview, "/api/v1/resolution/preview", `{"spec": {"metric": "steps"}, "start_date": "2025-09-13", "end_date": "2025-09-15"}`, http.StatusUnprocessableEntity, nil)
	e.call(preview, "/api/v1/resolution/preview", `{"spec": `+stepsMax+`, "start_date": "2025-09-15", "end_date": "2025-09-13"}`, http.StatusUnprocessableEntity, nil)
}

func TestResolvedWorkouts(t *testing.T) {
	e := newSourceEnv(t)
	f := func(v float64) *float64 { return &v }
	e.write(e.conn, normalize.Output{
		Devices: []normalize.Device{{Fingerprint: "strap", Type: "chest_strap", Model: "Synthetic strap"}, {Fingerprint: "watch", Type: "watch", Model: "Synthetic watch"}},
		Workouts: []normalize.Workout{
			{Start: at("2026-06-15T16:00:00Z"), End: at("2026-06-15T17:00:00Z"), Sport: "running", Device: "watch", DistanceM: f(10000), Key: normalize.Key{RecordType: "workout", ExternalID: "w1"}},
			{Start: at("2026-06-15T16:02:00Z"), End: at("2026-06-15T16:58:00Z"), Sport: "other", Device: "strap", AvgHRBpm: f(150), Key: normalize.Key{RecordType: "workout", ExternalID: "w2"}},
			{Start: at("2026-06-16T07:00:00Z"), End: at("2026-06-16T07:30:00Z"), Sport: "walking", Device: "watch", Key: normalize.Key{RecordType: "workout", ExternalID: "w3"}},
		},
	})
	var w oapi.ResolvedWorkouts
	e.get(e.user, "GET /api/v1/resolved/workouts", "/api/v1/resolved/workouts?start_date=2026-06-15&end_date=2026-06-16", http.StatusOK, &w)
	if w.Rule.Ref != "builtin:heart_rate:1" || len(w.Workouts) != 2 {
		t.Fatalf("workouts: %+v", w)
	}
	run := w.Workouts[0]
	if run.Sport != "running" || len(run.Members) != 2 || run.Selected == nil || *run.Group != "chest_strap" {
		t.Fatalf("run: %+v", run)
	}
	for _, m := range run.Members {
		if m.Selected != (m.ID == *run.Selected) || m.Selected != (m.Device.Type != nil && *m.Device.Type == string("chest_strap")) {
			t.Errorf("member: %+v", m)
		}
		if !m.Selected && m.RuleStatus != "not_in_rule" {
			t.Errorf("watch: %+v", m)
		}
	}
	if walk := w.Workouts[1]; walk.Selected != nil || walk.LocalDate.String() != "2026-06-16" || !strings.Contains(walk.Explanation, "None") {
		t.Errorf("walk: %+v", walk)
	}
	e.get(e.user, "GET /api/v1/resolved/workouts", "/api/v1/resolved/workouts?start_date=2026-06-16&end_date=2026-06-16", http.StatusOK, &w)
	if len(w.Workouts) != 1 {
		t.Errorf("one day: %+v", w.Workouts)
	}
	e.get(e.other, "GET /api/v1/resolved/workouts", "/api/v1/resolved/workouts?start_date=2026-06-15&end_date=2026-06-16", http.StatusOK, &w)
	if len(w.Workouts) != 0 {
		t.Errorf("another owner sees %+v", w.Workouts)
	}
}

func TestMetricsCatalogue(t *testing.T) {
	e := newSourceEnv(t)
	var l struct{ Metrics []oapi.Metric }
	e.get(e.user, "GET /api/v1/metrics", "/api/v1/metrics", http.StatusOK, &l)
	if len(l.Metrics) != len(catalog.Metrics()) {
		t.Fatalf("%d metrics", len(l.Metrics))
	}
	var m oapi.Metric
	e.get(e.user, "GET /api/v1/metrics/{code}", "/api/v1/metrics/steps", http.StatusOK, &m)
	if m.Unit != "count" || m.Aggregation != "additive" || fmt.Sprint(m.Windows) != "[bucket hour local_day]" ||
		!strings.Contains(fmt.Sprint(m.Strategies), "sum_across_sources") || m.Family != nil {
		t.Errorf("steps: %+v", m)
	}
	e.get(e.user, "GET /api/v1/metrics/{code}", "/api/v1/metrics/bp_systolic", http.StatusOK, &m)
	if m.Family == nil || *m.Family != "blood_pressure" || m.Group == nil {
		t.Errorf("bp_systolic: %+v", m)
	}
	e.get(e.user, "GET /api/v1/metrics/{code}", "/api/v1/metrics/resting_heart_rate_nocturnal", http.StatusOK, &m)
	if m.DerivedFrom == nil || *m.DerivedFrom != "heart_rate" || fmt.Sprint(m.Windows) != "[local_night sleep_episode]" {
		t.Errorf("derived: %+v", m)
	}
	e.get(e.user, "GET /api/v1/metrics/{code}", "/api/v1/metrics/nope", http.StatusNotFound, nil)

	// Without a timezone period (a fresh install) daily values are no_data, not an error.
	e.exec(`DELETE FROM timezone_periods WHERE user_id = $1`, e.other)
	var d oapi.ResolvedDaily
	e.get(e.other, daily, "/api/v1/resolved/daily?start_date=2026-06-15&end_date=2026-06-15&metrics=steps", http.StatusOK, &d)
	if v := d.Days[0].Metrics["steps"]; d.Timezone != "" || v.Status != "no_data" || !strings.Contains(v.Explanation, "timezone") {
		t.Errorf("no timezone: %+v", d)
	}
	e.get(e.other, series, "/api/v1/resolved/series?metric=steps&start=2026-06-15T00:00:00Z&end=2026-06-16T00:00:00Z", http.StatusUnprocessableEntity, nil)
}
