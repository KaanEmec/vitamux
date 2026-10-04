//go:build integration

package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// sourceEnv is two synthetic owners, each with a push connection, behind the real router.
// Requests carry a principal directly; access itself is covered by the auth tests.
type sourceEnv struct {
	t     *testing.T
	d     *db.DB
	scanE func(dest any, sql string, args ...any) error // scan without failing the test
	exec  func(sql string, args ...any)
	scan  func(dest any, sql string, args ...any)
	h     http.Handler
	user  uuid.UUID
	conn  uuid.UUID
	other uuid.UUID // second user
	oconn uuid.UUID
}

func newSourceEnv(t *testing.T) *sourceEnv {
	t.Helper()
	u, app := dbtest.Migrated(t)
	ownerPool := dbtest.Pool(t, u, db.OwnerRole)
	e := &sourceEnv{t: t, d: db.New(app), user: uuid.New(), conn: uuid.New(), other: uuid.New(), oconn: uuid.New()}
	e.exec = func(sql string, args ...any) {
		t.Helper()
		if _, err := ownerPool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	e.scanE = func(dest any, sql string, args ...any) error {
		return ownerPool.QueryRow(t.Context(), sql, args...).Scan(dest)
	}
	e.scan = func(dest any, sql string, args ...any) {
		t.Helper()
		if err := e.scanE(dest, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	for i, x := range [][2]uuid.UUID{{e.user, e.conn}, {e.other, e.oconn}} {
		e.exec(`INSERT INTO users (id, username, password_hash) VALUES ($1, $2, 'synthetic')`, x[0], fmt.Sprintf("owner%d", i))
		e.exec(`INSERT INTO connections (id, user_id, provider_id, account_key, mode, status)
			VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'withings'), sha256($3::bytea), 'push', 'active')`, x[1], x[0], x[1][:])
		e.exec(`INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES (gen_random_uuid(), $1, 'Europe/Amsterdam', '2020-01-01Z')`, x[0])
	}
	rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), Options{DB: e.d})
	if err != nil {
		t.Fatal(err)
	}
	e.h = rt.mux
	return e
}

// write stores a raw payload for conn and normalizes out from it.
func (e *sourceEnv) write(conn uuid.UUID, out normalize.Output) {
	e.t.Helper()
	if err := e.tryWrite(conn, out); err != nil {
		e.t.Fatal(err)
	}
}

// tryWrite is write for goroutines other than the test's.
func (e *sourceEnv) tryWrite(conn uuid.UUID, out normalize.Output) error {
	ctx := e.t.Context()
	body, batch := uuid.New(), uuid.New()
	var raw int64
	err := e.scanE(&raw, `WITH b AS (
			INSERT INTO blobs (sha256, size_bytes, stored_bytes, compression) VALUES (sha256($2::bytea), 42, 42, 'none') RETURNING sha256
		), ib AS (
			INSERT INTO ingest_batches (id, user_id, connection_id, source_kind)
			SELECT $3, user_id, id, 'push' FROM connections WHERE id = $1 RETURNING id, user_id, connection_id
		)
		INSERT INTO raw_payloads (user_id, connection_id, batch_id, stream, external_key, content_sha256, content_type, fetched_at)
		SELECT ib.user_id, ib.connection_id, ib.id, 'sample.readings.v1', ib.id::text, b.sha256, 'application/json', now()
		FROM ib, b RETURNING id`, conn, body[:], batch)
	if err != nil {
		return err
	}
	return e.d.Tx(ctx, func(q *dbq.Queries) error {
		nv, err := q.RegisterNormalizerVersion(ctx, dbq.RegisterNormalizerVersionParams{Name: "sample.readings", Version: 1, GitSha: "abc123"})
		if err != nil {
			return err
		}
		_, err = normalize.Write(ctx, q, normalize.Source{ConnectionID: conn, RawPayloadID: raw, NormalizerVersionID: nv}, out)
		return err
	})
}

// get calls target as user with read:health and checks the response against the spec operation.
func (e *sourceEnv) get(user uuid.UUID, pattern, target string, want int, v any) {
	e.t.Helper()
	req := request(e.t, http.MethodGet, target, nil)
	p := &auth.Principal{Kind: auth.APIKey, UserID: user, ID: uuid.New(), Scopes: []auth.Scope{auth.ReadHealth}}
	res := serve(e.t, e.h, req.WithContext(auth.WithPrincipal(req.Context(), p)))
	body := checkResponse(e.t, pattern, res)
	if res.StatusCode != want {
		e.t.Fatalf("GET %s: %d, want %d: %s", target, res.StatusCode, want, body)
	}
	if v != nil {
		if err := json.Unmarshal(body, v); err != nil {
			e.t.Fatal(err)
		}
	}
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// sourceFixture has one record of each kind; hr, sys, arm, stage and sport are the correctable parts.
// r2 is a second reading that a later payload deletes.
func sourceFixture(hr, sys float64, arm, stage, sport string) normalize.Output {
	t0 := at("2026-06-15T07:00:00Z")
	return normalize.Output{
		Devices: []normalize.Device{{Fingerprint: "dev-1", Type: "bp_monitor", Model: "Synthetic 1"}},
		Origins: []normalize.Origin{{Key: "com.example.app", Name: "Example"}},
		Measurements: []normalize.Measurement{
			{Metric: "heart_rate", Kind: catalog.Sample, Start: t0, Value: hr, Unit: "bpm", Device: "dev-1", Origin: "com.example.app",
				Key: normalize.Key{RecordType: "reading", ExternalID: "r1"}},
			{Metric: "heart_rate", Kind: catalog.Sample, Start: t0.Add(time.Hour), Value: 70, Unit: "bpm",
				Key: normalize.Key{RecordType: "reading", ExternalID: "r2"}},
		},
		Groups: []normalize.Group{{Kind: "bp_reading", MeasuredAt: t0, Device: "dev-1", Context: json.RawMessage(`{"arm": "` + arm + `"}`),
			Key: normalize.Key{RecordType: "bp", ExternalID: "b1"}, Components: []normalize.Measurement{
				{Metric: "bp_systolic", Kind: catalog.Sample, Start: t0, Value: sys, Unit: "mmHg"},
				{Metric: "bp_diastolic", Kind: catalog.Sample, Start: t0, Value: 79, Unit: "mmHg"},
				{Metric: "bp_pulse", Kind: catalog.Sample, Start: t0, Value: 64, Unit: "bpm"},
			}}},
		Sleep: []normalize.SleepSession{{Start: at("2026-06-14T21:00:00Z"), End: at("2026-06-15T05:00:00Z"),
			Key: normalize.Key{RecordType: "sleep", ExternalID: "s1"}, Stages: []normalize.SleepStage{
				{Stage: "light", Start: at("2026-06-14T21:00:00Z"), End: at("2026-06-15T01:00:00Z")},
				{Stage: stage, Start: at("2026-06-15T01:00:00Z"), End: at("2026-06-15T05:00:00Z")},
			}}},
		Workouts: []normalize.Workout{{Start: at("2026-06-15T16:00:00Z"), End: at("2026-06-15T17:00:00Z"), Sport: sport,
			Key: normalize.Key{RecordType: "workout", ExternalID: "w1"}, Segments: []normalize.Segment{
				{Kind: "lap", Start: at("2026-06-15T16:00:00Z"), Data: json.RawMessage(`{"pace_s_per_km": 300}`)},
			}}},
	}
}

func TestSourceEndpointsContract(t *testing.T) {
	e := newSourceEnv(t)
	e.write(e.conn, sourceFixture(61, 121, "left", "deep", "running"))
	e.write(e.conn, sourceFixture(62, 124, "right", "rem", "cycling")) // corrects r1, b1, s1, w1
	e.write(e.conn, normalize.Output{Tombstones: []normalize.Key{{RecordType: "reading", ExternalID: "r2"}}})
	e.write(e.oconn, sourceFixture(90, 140, "left", "deep", "running"))
	conn := ingest.FormatConnectionID(e.conn)

	const ms = "GET /api/v1/measurements"
	var m oapi.MeasurementPage
	e.get(e.user, ms, "/api/v1/measurements?metric=heart_rate", http.StatusOK, &m)
	if len(m.Measurements) != 1 || m.Measurements[0].Value != 62 || m.HasMore || m.NextCursor != nil {
		t.Fatalf("active heart rate: %+v", m)
	}
	cur := m.Measurements[0]
	if cur.Source.ConnectionID != conn || cur.Source.Provider != "withings" || cur.Source.Origin == nil ||
		*cur.Source.Origin != "com.example.app" || cur.Source.Device == nil || cur.Unit != "bpm" ||
		cur.Provenance.Normalizer != "sample.readings@1" || cur.Provenance.Raw != nil || cur.LocalDate.String() != "2026-06-15" {
		t.Fatalf("measurement: %+v", cur)
	}

	// include=superseded shows the corrected history; include=deleted the tombstoned reading.
	e.get(e.user, ms, "/api/v1/measurements?metric=heart_rate&include=superseded", http.StatusOK, &m)
	if len(m.Measurements) != 2 || m.Measurements[0].Provenance.SupersededAt == nil ||
		*m.Measurements[0].Provenance.SupersededBy != cur.ID || m.Measurements[0].Value != 61 {
		t.Fatalf("history: %+v", m.Measurements)
	}
	e.get(e.user, ms, "/api/v1/measurements?metric=heart_rate&include=deleted,provenance", http.StatusOK, &m)
	if len(m.Measurements) != 2 || m.Measurements[1].Provenance.DeletedAt == nil || m.Measurements[1].Provenance.DeletedByRawID == nil ||
		m.Measurements[0].Provenance.Raw == nil || m.Measurements[0].Provenance.Raw.SourceKind != "push" {
		t.Fatalf("deleted with provenance: %+v", m.Measurements)
	}

	// Filters.
	dev := *cur.Source.Device
	for target, n := range map[string]int{
		"/api/v1/measurements": 4, // r1 + three BP components
		"/api/v1/measurements?metric=bp_systolic&metric=bp_diastolic":              2,
		"/api/v1/measurements?provider=withings&connection=" + conn:                4,
		"/api/v1/measurements?provider=garmin":                                     0,
		"/api/v1/measurements?device=" + dev:                                       4,
		"/api/v1/measurements?device=dev_00000000000000000000000000000000":         0,
		"/api/v1/measurements?origin=com.example.app&metric=heart_rate":            1,
		"/api/v1/measurements?kind=sample":                                         4,
		"/api/v1/measurements?kind=interval":                                       0,
		"/api/v1/measurements?start=2026-06-15T07:00:00Z&end=2026-06-15T07:00:01Z": 4,
		"/api/v1/measurements?start=2026-06-15T07:00:01Z":                          0,
		"/api/v1/measurements?start_date=2026-06-15&end_date=2026-06-15":           4,
		"/api/v1/measurements?end_date=2026-06-14":                                 0,
		"/api/v1/measurements?include=superseded,deleted":                          9,
		"/api/v1/measurements?connection=" + ingest.FormatConnectionID(e.oconn):    0, // another user's connection
	} {
		e.get(e.user, ms, target, http.StatusOK, &m)
		if len(m.Measurements) != n {
			t.Errorf("%s: %d rows, want %d", target, len(m.Measurements), n)
		}
	}
	for _, target := range []string{
		"/api/v1/measurements?connection=conn_nope",
		"/api/v1/measurements?device=" + strings.TrimPrefix(dev, "dev_"),
		"/api/v1/measurements?include=everything",
		"/api/v1/measurements?start=2026-06-16T00:00:00Z&end=2026-06-15T00:00:00Z",
		"/api/v1/measurements?limit=0",
		"/api/v1/measurements?limit=10001",
		"/api/v1/measurements?cursor=garbage",
		"/api/v1/measurements?start=yesterday",
	} {
		e.get(e.user, ms, target, http.StatusUnprocessableEntity, nil)
	}

	// Groups and blood pressure.
	var g oapi.GroupPage
	e.get(e.user, "GET /api/v1/groups", "/api/v1/groups?kind=bp_reading", http.StatusOK, &g)
	if len(g.Groups) != 1 || len(g.Groups[0].Components) != 3 || string(g.Groups[0].Context) != `{"arm":"right"}` {
		t.Fatalf("groups: %+v", g)
	}
	e.get(e.user, "GET /api/v1/groups", "/api/v1/groups?include=superseded", http.StatusOK, &g)
	if len(g.Groups) != 2 || g.Groups[0].Provenance.SupersededBy == nil {
		t.Fatalf("group history: %+v", g)
	}
	for _, gr := range g.Groups {
		vals := map[string]float64{}
		for _, c := range gr.Components {
			vals[c.Metric] = c.Value
		}
		want := 124.0
		if gr.Provenance.SupersededAt != nil {
			want = 121
		}
		if len(gr.Components) != 3 || vals["bp_systolic"] != want {
			t.Errorf("group %s components: %+v", gr.ID, gr.Components)
		}
	}
	e.get(e.user, "GET /api/v1/groups", "/api/v1/groups?kind=weigh_in", http.StatusUnprocessableEntity, nil)
	var bp oapi.BloodPressurePage
	e.get(e.user, "GET /api/v1/blood-pressure", "/api/v1/blood-pressure?start_date=2026-06-15", http.StatusOK, &bp)
	if len(bp.Readings) != 1 || *bp.Readings[0].Systolic != 124 || *bp.Readings[0].Diastolic != 79 || *bp.Readings[0].Pulse != 64 {
		t.Fatalf("blood pressure: %+v", bp)
	}

	// Sleep.
	var sl oapi.SleepPage
	e.get(e.user, "GET /api/v1/sleep", "/api/v1/sleep", http.StatusOK, &sl)
	if len(sl.Sleep) != 1 || sl.Sleep[0].Stages != nil || sl.Sleep[0].SleepDate.String() != "2026-06-15" {
		t.Fatalf("sleep: %+v", sl)
	}
	e.get(e.user, "GET /api/v1/sleep", "/api/v1/sleep?include=stages,superseded", http.StatusOK, &sl)
	if len(sl.Sleep) != 2 || sl.Sleep[0].Stages == nil || (*sl.Sleep[0].Stages)[1].Stage != "deep" || (*sl.Sleep[1].Stages)[1].Stage != "rem" {
		t.Fatalf("sleep history: %+v", sl)
	}
	var s oapi.SleepSession
	old := sl.Sleep[0].ID.String()
	e.get(e.user, "GET /api/v1/sleep/{id}", "/api/v1/sleep/"+old, http.StatusOK, &s)
	if s.Provenance.SupersededAt == nil || s.Stages == nil || len(*s.Stages) != 2 {
		t.Fatalf("superseded sleep by id: %+v", s)
	}
	e.get(e.other, "GET /api/v1/sleep/{id}", "/api/v1/sleep/"+old, http.StatusNotFound, nil)
	e.get(e.user, "GET /api/v1/sleep/{id}", "/api/v1/sleep/not-a-uuid", http.StatusNotFound, nil)
	e.get(e.user, "GET /api/v1/sleep/{id}", "/api/v1/sleep/"+old+"?include=bogus", http.StatusUnprocessableEntity, nil)

	// Workouts.
	var w oapi.WorkoutPage
	e.get(e.user, "GET /api/v1/workouts", "/api/v1/workouts?include=segments", http.StatusOK, &w)
	if len(w.Workouts) != 1 || w.Workouts[0].Sport != "cycling" || w.Workouts[0].Segments == nil || len(*w.Workouts[0].Segments) != 1 {
		t.Fatalf("workouts: %+v", w)
	}
	var wo oapi.Workout
	e.get(e.user, "GET /api/v1/workouts/{id}", "/api/v1/workouts/"+w.Workouts[0].ID.String()+"?include=provenance", http.StatusOK, &wo)
	if wo.Segments == nil || wo.Provenance.Raw == nil {
		t.Fatalf("workout by id: %+v", wo)
	}
	e.get(e.other, "GET /api/v1/workouts/{id}", "/api/v1/workouts/"+wo.ID.String(), http.StatusNotFound, nil)

	// Provenance, scoped to the caller.
	const pv = "GET /api/v1/provenance/{entity}/{id}"
	var p oapi.Provenance
	e.get(e.user, pv, "/api/v1/provenance/measurement/"+cur.ID, http.StatusOK, &p)
	if p.Row.ID != cur.ID || len(p.Earlier) != 1 || len(p.Later) != 0 || p.Row.CorrectedAt == nil || p.Row.Raw == nil ||
		p.Row.ConnectionID != conn || p.Row.Normalizer.Name != "sample.readings" {
		t.Fatalf("provenance: %+v", p)
	}
	e.get(e.user, pv, "/api/v1/provenance/sleep/"+old, http.StatusOK, &p)
	if len(p.Later) != 1 {
		t.Fatalf("sleep provenance: %+v", p)
	}
	e.get(e.user, pv, "/api/v1/provenance/group/"+g.Groups[0].ID, http.StatusOK, nil)
	e.get(e.user, pv, "/api/v1/provenance/workout/"+wo.ID.String(), http.StatusOK, nil)
	e.get(e.other, pv, "/api/v1/provenance/measurement/"+cur.ID, http.StatusNotFound, nil)
	e.get(e.user, pv, "/api/v1/provenance/lab_result/1", http.StatusNotFound, nil)
	e.get(e.user, pv, "/api/v1/provenance/measurement/999999", http.StatusNotFound, nil)

	// The other owner sees only their own rows.
	e.get(e.other, ms, "/api/v1/measurements?metric=heart_rate", http.StatusOK, &m)
	if len(m.Measurements) != 2 || m.Measurements[0].Value != 90 {
		t.Fatalf("other owner: %+v", m)
	}
}

// TestSourcePaginationConcurrentInserts pages through measurements while another writer keeps
// inserting rows, before and after the cursor. Every row that existed when paging started comes
// back exactly once, in (start_at, id) order.
func TestSourcePaginationConcurrentInserts(t *testing.T) {
	e := newSourceEnv(t)
	t0 := at("2026-06-01T00:00:00Z")
	sample := func(i int, ext string) normalize.Measurement {
		return normalize.Measurement{Metric: "heart_rate", Kind: catalog.Sample, Start: t0.Add(time.Duration(i) * time.Minute),
			Value: 60, Unit: "bpm", Key: normalize.Key{RecordType: "reading", ExternalID: ext}}
	}
	var out normalize.Output
	for i := range 100 {
		out.Measurements = append(out.Measurements, sample(i*2, fmt.Sprintf("m%d", i)))
	}
	e.write(e.conn, out)
	var initial []string
	var m oapi.MeasurementPage
	e.get(e.user, "GET /api/v1/measurements", "/api/v1/measurements?metric=heart_rate&limit=1000", http.StatusOK, &m)
	for _, x := range m.Measurements {
		initial = append(initial, x.ID)
	}
	if len(initial) != 100 {
		t.Fatalf("seeded %d rows", len(initial))
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			// Odd minutes interleave with the seeded rows; some land before the cursor, some after.
			if err := e.tryWrite(e.conn, normalize.Output{Measurements: []normalize.Measurement{sample((i*37)%200|1, fmt.Sprintf("c%d", i))}}); err != nil {
				t.Error(err)
				return
			}
		}
	})

	seen := map[string]int{}
	var last time.Time
	var lastID int64
	target := "/api/v1/measurements?metric=heart_rate&limit=7"
	for pages := 0; ; pages++ {
		if pages > 100 {
			t.Fatal("pagination does not terminate")
		}
		e.get(e.user, "GET /api/v1/measurements", target, http.StatusOK, &m)
		for _, x := range m.Measurements {
			seen[x.ID]++
			id, _ := strconv.ParseInt(x.ID, 10, 64)
			if x.StartAt.Before(last) || (x.StartAt.Equal(last) && id <= lastID) {
				t.Errorf("row %s at %s out of order after %d at %s", x.ID, x.StartAt, lastID, last)
			}
			last, lastID = x.StartAt, id
		}
		if !m.HasMore {
			break
		}
		// One row behind the cursor (never returned) and one just ahead of it (returned later).
		e.write(e.conn, normalize.Output{Measurements: []normalize.Measurement{sample(-1-pages, fmt.Sprintf("behind%d", pages))}})
		ahead := sample(0, fmt.Sprintf("ahead%d", pages))
		ahead.Start = last.Add(time.Second)
		e.write(e.conn, normalize.Output{Measurements: []normalize.Measurement{ahead}})
		target = "/api/v1/measurements?metric=heart_rate&limit=7&cursor=" + url.QueryEscape(*m.NextCursor)
	}
	close(stop)
	wg.Wait()

	for id, n := range seen {
		if n != 1 {
			t.Errorf("row %s returned %d times", id, n)
		}
	}
	for _, id := range initial {
		if seen[id] != 1 {
			t.Errorf("seeded row %s returned %d times", id, seen[id])
		}
	}
	var behind, ahead []string
	e.scan(&behind, `SELECT array_agg(id::text) FROM measurements WHERE external_id LIKE 'behind%'`)
	e.scan(&ahead, `SELECT array_agg(id::text) FROM measurements WHERE external_id LIKE 'ahead%'`)
	for _, id := range behind {
		if seen[id] != 0 {
			t.Errorf("row %s inserted behind the cursor was returned", id)
		}
	}
	for _, id := range ahead {
		if seen[id] != 1 {
			t.Errorf("row %s inserted ahead of the cursor returned %d times", id, seen[id])
		}
	}
	if len(ahead) < 10 {
		t.Fatalf("only %d pages", len(ahead))
	}

	// A cursor only resumes the query that produced it.
	e.get(e.user, "GET /api/v1/measurements", "/api/v1/measurements?metric=heart_rate&limit=2", http.StatusOK, &m)
	e.get(e.user, "GET /api/v1/measurements", "/api/v1/measurements?metric=steps&limit=2&cursor="+url.QueryEscape(*m.NextCursor),
		http.StatusUnprocessableEntity, nil)
	e.get(e.user, "GET /api/v1/measurements", "/api/v1/measurements?metric=heart_rate&limit=5&cursor="+url.QueryEscape(*m.NextCursor),
		http.StatusOK, nil)
}
