//go:build integration

package withings

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/jobs"
	fp "github.com/KaanEmec/vitamux/internal/testutil/fakeprovider"
)

// fakeMeasure is a stateful getmeas server over a synthetic dataset: lastupdate filters on
// modified, startdate/enddate (inclusive) on date, and offset/more page through the result.
type fakeMeasure struct {
	mu       sync.Mutex
	groups   []fgroup
	now      int64 // updatetime of every answer
	pageSize int
	calls    []url.Values
}

type fgroup struct {
	id, date, modified int64
	bp                 bool
	vals               []int64
}

func (f *fakeMeasure) add(g fgroup) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groups = append(f.groups, g)
}

func (f *fakeMeasure) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/measure" || r.Header.Get("Authorization") != "Bearer synthetic-access-1" || r.ParseForm() != nil {
		http.Error(w, "unexpected request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	form := r.PostForm
	f.calls = append(f.calls, form)
	num := func(k string) int64 { n, _ := strconv.ParseInt(form.Get(k), 10, 64); return n }
	var sel []fgroup
	for _, g := range f.groups {
		if form.Has("lastupdate") && g.modified > num("lastupdate") ||
			!form.Has("lastupdate") && g.date >= num("startdate") && g.date <= num("enddate") {
			sel = append(sel, g)
		}
	}
	sort.Slice(sel, func(i, j int) bool { return sel[i].date < sel[j].date })
	off := min(int(num("offset")), len(sel))
	end := min(off+f.pageSize, len(sel))
	grps := []map[string]any{}
	for _, g := range sel[off:end] {
		var ms []map[string]any
		types, units, model := []int{10, 9, 11}, []int{0, 0, 0}, 45
		if !g.bp {
			types, units, model = []int{1, 6}, []int{-3, -1}, 6
		}
		for i, v := range g.vals {
			ms = append(ms, map[string]any{"value": v, "type": types[i], "unit": units[i], "algo": 0, "fm": 3})
		}
		grps = append(grps, map[string]any{"grpid": g.id, "attrib": 0, "date": g.date, "created": g.date + 60,
			"modified": g.modified, "category": 1, "deviceid": "synthetic-device", "hash_deviceid": "synthetic-device",
			"measures": ms, "model_id": model, "comment": nil})
	}
	more := 0
	if end < len(sel) {
		more = 1
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"synthetic": true, "status": 0, "body": map[string]any{
		"updatetime": f.now, "timezone": "Europe/Berlin", "measuregrps": grps, "more": more, "offset": end}})
}

func unix(s string) int64 {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.Unix()
}

// A synthetic year backfilled in 30-day units, then incremental syncs: every group exactly
// once, a modified group superseded, and the lastupdate cursor carried from call to call.
func TestBackfillThenIncremental(t *testing.T) {
	ctx := t.Context()
	f := &fakeMeasure{pageSize: 20, now: unix("2026-01-01T00:00:00Z")}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	e := setup(t, srv.URL)

	day0 := unix("2025-01-01T00:00:00Z")
	nextID := int64(1_000_000)
	group := func(date int64, bp bool, d int64) fgroup {
		nextID++
		g := fgroup{id: nextID, date: date, modified: date + 120, bp: bp, vals: []int64{120 + d%15, 80 - d%7, 60 + d%10}}
		if !bp {
			g.vals = []int64{80000 - d*5, 230}
		}
		return g
	}
	for d := range int64(365) {
		f.add(group(day0+d*86400+7*3600+(d%30)*60, true, d))
		if d%2 == 0 {
			f.add(group(day0+d*86400+6*3600+1800, false, d))
		}
	}
	year := len(f.groups)

	conn := uuid.New()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	e.exec(`INSERT INTO connections (id, user_id, provider_id, account_key, mode, status)
		VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'withings'), $3, 'in_process', 'active')`, conn, e.user, key)
	if err := e.d.Tx(ctx, func(q *dbq.Queries) error {
		return e.rt.SaveCredentials(ctx, q, conn, connectors.Credentials{
			AccessToken: "synthetic-access-1", RefreshToken: "synthetic-refresh-1", ExpiresAt: time.Now().Add(3 * time.Hour)})
	}); err != nil {
		t.Fatal(err)
	}
	activeGroups := func() int {
		return e.count(`SELECT count(*) FROM measurement_groups WHERE connection_id = $1 AND superseded_at IS NULL`, conn)
	}
	check := func(want int) {
		t.Helper()
		if n := activeGroups(); n != want {
			t.Fatalf("%d active groups, want %d", n, want)
		}
		if n := e.count(`SELECT count(DISTINCT external_id) FROM measurement_groups WHERE connection_id = $1 AND superseded_at IS NULL`, conn); n != want {
			t.Fatalf("%d distinct groups, want %d (duplicates)", n, want)
		}
		if n := e.count(`SELECT count(*) FROM raw_payloads WHERE connection_id = $1 AND status <> 'normalized'`, conn); n != 0 {
			t.Fatalf("%d raw payloads not normalized", n)
		}
	}

	// Backfill 2025 in 30-day units (13), paging 20 groups at a time.
	from, to := time.Unix(day0, 0).UTC(), time.Unix(unix("2026-01-01T00:00:00Z"), 0).UTC()
	if _, err := e.rt.CreateBackfill(ctx, connectors.BackfillSpec{ConnectionID: conn, Stream: StreamMeasures, From: from, To: to}); err != nil {
		t.Fatal(err)
	}
	e.run(t)
	e.allSucceeded(t)
	check(year)
	if n := e.count(`SELECT count(*) FROM measurements m JOIN measurement_groups g ON g.id = m.group_id
		WHERE g.connection_id = $1 AND m.superseded_at IS NULL`, conn); n != 365*3+183*2 {
		t.Fatalf("%d group components", n)
	}
	if n := e.count(`SELECT count(*) FROM sync_cursors WHERE connection_id = $1 AND cursor IS NOT NULL`, conn); n != 0 {
		t.Fatal("backfill moved the stream cursor")
	}

	// New groups and a later edit of an old reading; the first incremental starts from 0.
	f.mu.Lock()
	f.groups[300].vals = []int64{150, 95, 70}
	f.groups[300].modified = unix("2026-01-02T12:00:00Z")
	f.now = unix("2026-01-03T00:00:00Z")
	f.mu.Unlock()
	f.add(group(unix("2026-01-01T07:10:00Z"), true, 1))
	f.add(group(unix("2026-01-02T06:40:00Z"), false, 2))
	sync := func() {
		t.Helper()
		if _, _, err := jobs.Enqueue(ctx, e.d.Q(), jobs.NewJob{Kind: jobs.KindSync, ConnectionID: &conn, Exclusive: true,
			Payload: jobs.SyncPayload{Stream: StreamMeasures, Mode: connectors.ModeIncremental, Slot: time.Now()}}); err != nil {
			t.Fatal(err)
		}
		e.run(t)
		e.allSucceeded(t)
	}
	sync()
	check(year + 2)
	// The group row is unchanged; its three edited components are superseded by new versions.
	if n := e.count(`SELECT count(*) FROM measurements WHERE connection_id = $1 AND superseded_at IS NOT NULL`, conn); n != 3 {
		t.Fatalf("%d superseded measurements, want the 3 edited components", n)
	}
	if n := e.count(`SELECT count(*) FROM measurements m JOIN metric_catalog c ON c.id = m.metric_id
		WHERE m.connection_id = $1 AND m.superseded_at IS NULL AND c.code = 'bp_systolic' AND m.value = 150`, conn); n != 1 {
		t.Fatal("edited systolic value not active")
	}
	if n := e.count(`SELECT count(*) FROM sync_cursors WHERE connection_id = $1 AND cursor->>'lastupdate' = $2`, conn,
		strconv.FormatInt(unix("2026-01-03T00:00:00Z"), 10)); n != 1 {
		t.Fatal("cursor is not the first page's updatetime")
	}

	// The next incremental asks only for changes since that updatetime.
	f.mu.Lock()
	f.now, f.calls = unix("2026-01-04T00:00:00Z"), nil
	f.mu.Unlock()
	f.add(group(unix("2026-01-03T07:05:00Z"), true, 3))
	sync()
	check(year + 3)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 1 || f.calls[0].Get("lastupdate") != strconv.FormatInt(unix("2026-01-03T00:00:00Z"), 10) {
		t.Fatalf("incremental made %d calls with lastupdate %q", len(f.calls), f.calls[0].Get("lastupdate"))
	}
}

// A getmeas body without the fields the connector relies on is drift: the page is kept
// quarantined, the cursor stays, the connection is degraded, nothing is substituted.
func TestSchemaDrift(t *testing.T) {
	ctx := t.Context()
	fake := fp.New(t)
	e := setup(t, fake.URL)
	conn := uuid.New()
	e.exec(`INSERT INTO connections (id, user_id, provider_id, mode, status)
		VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'withings'), 'in_process', 'active')`, conn, e.user)
	if err := e.d.Tx(ctx, func(q *dbq.Queries) error {
		return e.rt.SaveCredentials(ctx, q, conn, connectors.Credentials{AccessToken: "synthetic-access-1", RefreshToken: "synthetic-refresh-1"})
	}); err != nil {
		t.Fatal(err)
	}
	fake.Expect(fp.Step{Method: http.MethodPost, Path: "/measure", Header: map[string]string{"Authorization": "Bearer synthetic-access-1"},
		Reply: fp.JSON(http.StatusOK, map[string]any{"status": 0, "body": map[string]any{"updatetime": 1, "series": []any{}}})})
	if _, _, err := jobs.Enqueue(ctx, e.d.Q(), jobs.NewJob{Kind: jobs.KindSync, ConnectionID: &conn, Exclusive: true,
		Payload: jobs.SyncPayload{Stream: StreamMeasures, Mode: connectors.ModeIncremental, Slot: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	e.run(t)
	if n := e.count(`SELECT count(*) FROM raw_payloads WHERE connection_id = $1 AND status = 'quarantined'`, conn); n != 1 {
		t.Fatalf("%d quarantined pages", n)
	}
	if n := e.count(`SELECT count(*) FROM connections WHERE id = $1 AND status = 'degraded' AND last_error_class = 'schema_drift'`, conn); n != 1 {
		t.Fatal("connection not degraded by drift")
	}
	if n := e.count(`SELECT count(*) FROM sync_cursors WHERE connection_id = $1 AND cursor IS NOT NULL`, conn); n != 0 {
		t.Fatal("drift moved the cursor")
	}
}
