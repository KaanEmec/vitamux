//go:build integration

package withings

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

// fakeWithings scripts the Withings behaviour the lifecycle depends on
// (docs/providers/withings.md): codes exchanged once, every refresh rotates the pair, the old
// refresh token dies as soon as the new access token is used, a revoked grant refuses every
// token, notify profiles are per appli, and getmeas is fakeMeasure's stateful dataset.
type fakeWithings struct {
	t       *testing.T
	measure *fakeMeasure

	mu                       sync.Mutex
	codes                    map[string]bool
	access, refresh, oldRefr string
	seq, refreshes           int
	revoked                  bool
	subs                     map[int]string // appli -> callbackurl
}

func (f *fakeWithings) reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "body": body})
}

func (f *fakeWithings) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/measure" { // fakeMeasure reads the body itself
		f.mu.Lock()
		ok := f.authorized(r)
		f.mu.Unlock()
		if !ok {
			f.reply(w, 401, map[string]any{})
			return
		}
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer synthetic-access-1")
		f.measure.ServeHTTP(w, r)
		return
	}
	if r.ParseForm() != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	form := r.PostForm
	switch r.URL.Path {
	case "/v2/oauth2":
		if form.Get("action") != "requesttoken" || form.Get("client_id") != clientID || form.Get("client_secret") != clientSecret {
			f.t.Errorf("fake withings: token request without action or client credentials")
			f.reply(w, 503, map[string]any{})
			return
		}
		switch form.Get("grant_type") {
		case "authorization_code":
			if !f.codes[form.Get("code")] {
				f.reply(w, 503, map[string]any{})
				return
			}
			delete(f.codes, form.Get("code"))
			f.revoked, f.refresh = false, ""
		case "refresh_token":
			rt := form.Get("refresh_token")
			if f.revoked {
				f.reply(w, 503, map[string]any{}) // invalid refresh token
				return
			}
			if rt != f.refresh && (rt != f.oldRefr || f.oldRefr == "") {
				f.t.Errorf("fake withings: a dead refresh token was presented")
				f.reply(w, 503, map[string]any{})
				return
			}
			f.refreshes++
		default:
			f.t.Errorf("fake withings: unexpected grant type")
			return
		}
		f.seq++
		f.oldRefr, f.access, f.refresh = f.refresh, fmt.Sprintf("synthetic-access-%d", f.seq), fmt.Sprintf("synthetic-refresh-%d", f.seq)
		f.reply(w, 0, map[string]any{"userid": 1234567, "access_token": f.access, "refresh_token": f.refresh,
			"expires_in": 10800, "scope": "user.metrics", "token_type": "Bearer"})
	case "/notify":
		if !f.authorized(r) {
			f.reply(w, 401, map[string]any{})
			return
		}
		appli, _ := strconv.Atoi(form.Get("appli"))
		cb := form.Get("callbackurl")
		switch form.Get("action") {
		case "list":
			profiles := []map[string]any{}
			for a, u := range f.subs {
				profiles = append(profiles, map[string]any{"appli": a, "callbackurl": u, "comment": "Vitamux", "expires": 2147483647})
			}
			f.reply(w, 0, map[string]any{"profiles": profiles})
			return
		case "subscribe":
			if f.subs[appli] == cb {
				f.t.Errorf("fake withings: duplicate subscription for appli %d", appli)
			}
			f.subs[appli] = cb
		case "revoke":
			if f.subs[appli] != cb {
				f.t.Errorf("fake withings: revoke of an unknown subscription (appli %d)", appli)
			}
			delete(f.subs, appli)
		default:
			f.t.Errorf("fake withings: unexpected notify action")
		}
		f.reply(w, 0, map[string]any{})
	default:
		http.Error(w, "unexpected path", http.StatusNotFound)
	}
}

// authorized checks the bearer token. Using the newest access token kills the previous refresh
// token, as Withings does.
func (f *fakeWithings) authorized(r *http.Request) bool {
	if f.revoked || f.access == "" || r.Header.Get("Authorization") != "Bearer "+f.access {
		return false
	}
	f.oldRefr = ""
	return true
}

func (f *fakeWithings) do(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// hook returns the one callback URL all three measures applis are subscribed to.
func (f *fakeWithings) hook(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.subs) != 3 || f.subs[1] == "" || f.subs[1] != f.subs[2] || f.subs[1] != f.subs[4] {
		t.Fatalf("want applis 1, 2, 4 on one callback, have %d subscriptions", len(f.subs))
	}
	return f.subs[1]
}

// The official-connector lifecycle against the fake: connect → first sync → backfill →
// incremental → notify → refresh rotation → revoked grant → needs_reauth → reauth → resume →
// notifications off. Each step checks raw rows, canonical rows, the cursor and the connection.
func TestLifecycle(t *testing.T) {
	ctx := context.Background()
	t1 := unix("2026-01-01T00:00:00Z")
	measure := &fakeMeasure{pageSize: 25, now: t1}
	f := &fakeWithings{t: t, measure: measure, codes: map[string]bool{}, subs: map[int]string{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	e := setup(t, srv.URL)
	pub, _ := url.Parse(publicURL)
	nt := NewNotifications(e.d, e.rt, New(Config{ClientID: clientID, ClientSecret: clientSecret, APIURL: srv.URL}), pub, nil)
	e.rt.OnAuthorized(nt.Authorized)

	// Synthetic history: a BP reading every day and a weigh-in every third day for 61 days.
	nextID := int64(5_000_000)
	group := func(date int64, bp bool) fgroup {
		nextID++
		g := fgroup{id: nextID, date: date, modified: date + 120, bp: bp, vals: []int64{118 + nextID%9, 78 - nextID%5, 62 + nextID%7}}
		if !bp {
			g.vals = []int64{79000 - nextID%400, 225}
		}
		return g
	}
	day0, total, bps := unix("2025-11-01T00:00:00Z"), 0, 0
	for d := range int64(61) {
		measure.add(group(day0+d*86400+7*3600, true))
		total, bps = total+1, bps+1
		if d%3 == 0 {
			measure.add(group(day0+d*86400+6*3600, false))
			total++
		}
	}

	var id uuid.UUID
	q := func(sql string) int { t.Helper(); return e.count(sql, id) }
	expect := func(step string, raw, groups, bpGroups int, lastupdate int64, status string) {
		t.Helper()
		if n := q(`SELECT count(*) FROM raw_payloads WHERE connection_id = $1`); n != raw {
			t.Fatalf("%s: %d raw rows, want %d", step, n, raw)
		}
		if n := q(`SELECT count(*) FROM raw_payloads WHERE connection_id = $1 AND status <> 'normalized'`); n != 0 {
			t.Fatalf("%s: %d raw rows not normalized", step, n)
		}
		if n := q(`SELECT count(*) FROM measurement_groups WHERE connection_id = $1 AND superseded_at IS NULL`); n != groups {
			t.Fatalf("%s: %d active groups, want %d", step, n, groups)
		}
		// Every BP reading keeps its systolic, diastolic and pulse together in one group.
		if n := q(`SELECT count(*) FROM measurement_groups g WHERE g.connection_id = $1 AND g.kind = 'bp_reading'
			AND g.superseded_at IS NULL AND (SELECT count(DISTINCT c.code) FROM measurements m JOIN metric_catalog c ON c.id = m.metric_id
			WHERE m.group_id = g.id AND m.superseded_at IS NULL AND c.code IN ('bp_systolic', 'bp_diastolic', 'bp_pulse')) = 3`); n != bpGroups {
			t.Fatalf("%s: %d BP groups with paired components, want %d", step, n, bpGroups)
		}
		if n := e.count(`SELECT count(*) FROM sync_cursors WHERE connection_id = $1 AND stream = $2 AND cursor->>'lastupdate' = $3`,
			id, StreamMeasures, strconv.FormatInt(lastupdate, 10)); n != 1 {
			t.Fatalf("%s: cursor is not lastupdate %d", step, lastupdate)
		}
		if n := e.count(`SELECT count(*) FROM connections WHERE id = $1 AND status = $2`, id, status); n != 1 {
			t.Fatalf("%s: connection is not %s", step, status)
		}
	}
	hookHashOf := func(callback string) []byte {
		token := strings.TrimPrefix(callback, publicURL+"/webhooks/withings/")
		sum := sha256.Sum256([]byte(token))
		return sum[:]
	}
	notify := func(callback string, form url.Values) error {
		return nt.Notify(ctx, strings.TrimPrefix(callback, publicURL+"/webhooks/withings/"), form)
	}
	sync := func() {
		t.Helper()
		if _, _, err := jobs.Enqueue(ctx, e.d.Q(), jobs.NewJob{Kind: jobs.KindSync, ConnectionID: &id, Exclusive: true,
			Payload: jobs.SyncPayload{Stream: StreamMeasures, Mode: connectors.ModeIncremental, Slot: time.Now()}}); err != nil {
			t.Fatal(err)
		}
		e.run(t)
	}

	// The owner turns notifications on before connecting; there is nothing to subscribe yet.
	if err := nt.SetEnabled(ctx, e.user, true); err != nil {
		t.Fatal(err)
	}

	// 1. Connect: the code is exchanged, the authorization hook subscribes applis 1, 2, 4 on
	// one callback whose token only the hash of is stored, and a first sync is queued.
	f.do(func() { f.codes["synthetic-code-1"] = true })
	var err error
	if id, err = e.complete(e.begin(t, nil), "synthetic-code-1"); err != nil {
		t.Fatal(err)
	}
	hook := f.hook(t)
	if n := e.count(`SELECT count(*) FROM connections WHERE id = $1 AND hook_token_hash = $2`, id, hookHashOf(hook)); n != 1 {
		t.Fatal("hook token hash not stored")
	}

	// 2. First sync: lastupdate 0 is the whole history; paired BP components; cursor at updatetime.
	e.run(t)
	e.allSucceeded(t)
	expect("first sync", total, total, bps, t1, "active")

	// 3. Backfill the same two months in 30-day units: re-fetched groups are no-ops, and a
	// backfill never moves the stream cursor.
	if _, err := e.rt.CreateBackfill(ctx, connectors.BackfillSpec{ConnectionID: id, Stream: StreamMeasures,
		From: time.Unix(day0, 0).UTC(), To: time.Unix(t1, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	e.run(t)
	e.allSucceeded(t)
	expect("backfill", total, total, bps, t1, "active")
	if n := q(`SELECT count(*) FROM backfills WHERE connection_id = $1 AND status = 'done'`); n != 1 {
		t.Fatal("backfill not done")
	}

	// 4. Incremental: two new groups and an edited reading (a new raw version superseding three
	// components), cursor moved to the new updatetime.
	t2 := unix("2026-01-03T00:00:00Z")
	measure.mu.Lock()
	measure.groups[10].vals, measure.groups[10].modified, measure.now = []int64{151, 96, 71}, t2-3600, t2
	measure.mu.Unlock()
	measure.add(group(unix("2026-01-01T07:00:00Z"), true))
	measure.add(group(unix("2026-01-02T06:00:00Z"), false))
	total, bps = total+2, bps+1
	sync()
	e.allSucceeded(t)
	expect("incremental", total+1, total, bps, t2, "active")
	if n := q(`SELECT count(*) FROM measurements WHERE connection_id = $1 AND superseded_at IS NOT NULL`); n != 3 {
		t.Fatalf("incremental: %d superseded components, want 3", n)
	}

	// 5. Notify: a wrong token is unknown and enqueues nothing; ten notifications for one window
	// (and other categories, users or windows) give one correction sync, which fetches the window
	// by date and leaves the cursor alone.
	jobsBefore := e.count(`SELECT count(*) FROM jobs`)
	if err := nt.Notify(ctx, "not-a-hook-token", url.Values{"userid": {"1234567"}, "appli": {"4"}}); !errors.Is(err, ErrUnknownHook) {
		t.Fatalf("wrong token: %v", err)
	}
	reading := unix("2026-01-03T08:00:00Z")
	g := group(reading, true)
	g.modified = t2 + 600
	measure.add(g)
	total, bps = total+1, bps+1
	window := url.Values{"userid": {"1234567"}, "appli": {"4"}, "startdate": {strconv.FormatInt(reading, 10)}, "enddate": {strconv.FormatInt(reading, 10)}}
	for range 10 {
		if err := notify(hook, window); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []url.Values{
		{"userid": {"1234567"}, "appli": {"16"}, "startdate": {"1"}, "enddate": {"2"}},  // activity: not this stream
		{"userid": {"7654321"}, "appli": {"4"}, "startdate": {"1"}, "enddate": {"2"}},   // another Withings user
		{"userid": {"1234567"}, "appli": {"1"}, "startdate": {"9"}, "enddate": {"2"}},   // window ends before it starts
		{"userid": {"1234567"}, "appli": {"1"}, "startdate": {"0"}, "enddate": {"1e9"}}, // not a window
	} {
		if err := notify(hook, v); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.count(`SELECT count(*) FROM jobs`); n != jobsBefore+1 {
		t.Fatalf("notify: %d jobs enqueued, want 1", n-jobsBefore)
	}
	if n := q(`SELECT count(*) FROM jobs WHERE connection_id = $1 AND status = 'queued' AND payload->>'mode' = 'correction'
		AND dedupe_key LIKE 'notify:%'`); n != 1 {
		t.Fatal("notify: no queued correction sync")
	}
	e.run(t)
	e.allSucceeded(t)
	expect("notify", total+1, total, bps, t2, "active")

	// 6. Refresh rotation: the access token expires at Withings; the runtime refreshes once,
	// stores the rotated pair before using it, and the old refresh token is dead afterwards.
	t4 := unix("2026-01-04T00:00:00Z")
	version := q(`SELECT version FROM credentials WHERE connection_id = $1`)
	f.do(func() { f.access = "synthetic-access-expired" })
	measure.mu.Lock()
	measure.now = t4
	measure.mu.Unlock()
	sync()
	e.allSucceeded(t)
	expect("rotation", total+1, total, bps, t4, "active")
	f.do(func() {
		if f.refreshes != 1 || f.oldRefr != "" {
			t.Fatalf("rotation: %d refreshes, old refresh token alive %v", f.refreshes, f.oldRefr != "")
		}
	})
	if n := q(`SELECT version FROM credentials WHERE connection_id = $1`); n != version+1 {
		t.Fatalf("rotation: credentials version %d, want %d", n, version+1)
	}

	// 7. Revoked: Withings refuses the access and the refresh token. The sync stops with
	// reauth_required, the connection needs reauthorization, the cursor stays.
	f.do(func() { f.revoked, f.subs = true, map[int]string{} })
	t5 := unix("2026-01-05T00:00:00Z")
	g = group(unix("2026-01-04T07:30:00Z"), true)
	g.modified = t5 - 60
	measure.add(g)
	measure.mu.Lock()
	measure.now = t5
	measure.mu.Unlock()
	sync()
	if n := q(`SELECT count(*) FROM connections WHERE id = $1 AND status = 'needs_reauth' AND last_error_class = 'reauth_required'`); n != 1 {
		t.Fatal("revoked: connection does not need reauthorization")
	}
	if n := q(`SELECT count(*) FROM jobs WHERE connection_id = $1 AND status = 'dead'`); n != 1 {
		t.Fatal("revoked: the sync is not dead")
	}
	expect("revoked", total+1, total, bps, t4, "needs_reauth")
	jobsBefore = e.count(`SELECT count(*) FROM jobs`)
	if err := notify(hook, window); err != nil { // acknowledged, but nothing to run while revoked
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM jobs`); n != jobsBefore {
		t.Fatal("revoked: a notification enqueued a sync")
	}

	// 8. Reauthorize the same account: active again, and resubscribed under a new token (the
	// revocation dropped the old profiles), so the old callback is unknown.
	f.do(func() { f.codes["synthetic-code-2"] = true })
	if again, err := e.complete(e.begin(t, &id), "synthetic-code-2"); err != nil || again != id {
		t.Fatalf("reauth: %v", err)
	}
	newHook := f.hook(t)
	if newHook == hook || e.count(`SELECT count(*) FROM connections WHERE id = $1 AND hook_token_hash = $2`, id, hookHashOf(newHook)) != 1 {
		t.Fatal("reauth: not resubscribed under a new hook token")
	}
	if err := notify(hook, window); !errors.Is(err, ErrUnknownHook) {
		t.Fatalf("old hook after resubscribe: %v", err)
	}

	// 9. Resume: the queued sync continues from the stored cursor and picks up what arrived
	// while the grant was revoked.
	total, bps = total+1, bps+1
	e.run(t)
	if n := q(`SELECT count(*) FROM jobs WHERE connection_id = $1 AND status NOT IN ('succeeded', 'dead')`); n != 0 {
		e.dump(t)
		t.Fatal("resume: jobs did not succeed")
	}
	expect("resume", total+1, total, bps, t5, "active")
	if n := q(`SELECT consecutive_failures FROM connections WHERE id = $1`); n != 0 {
		t.Fatalf("resume: %d consecutive failures", n)
	}

	// 10. Notifications off: every profile is revoked and the hook is forgotten (404); polling stays.
	if err := nt.SetEnabled(ctx, e.user, false); err != nil {
		t.Fatal(err)
	}
	f.do(func() {
		if len(f.subs) != 0 {
			t.Fatalf("off: %d subscriptions left", len(f.subs))
		}
	})
	jobsBefore = e.count(`SELECT count(*) FROM jobs`)
	if err := notify(newHook, window); !errors.Is(err, ErrUnknownHook) {
		t.Fatalf("off: %v", err)
	}
	if n := q(`SELECT count(*) FROM connections WHERE id = $1 AND hook_token_hash IS NULL`); n != 1 || e.count(`SELECT count(*) FROM jobs`) != jobsBefore {
		t.Fatal("off: hook kept or a job enqueued")
	}
	if n := q(`SELECT count(*) FROM schedules WHERE connection_id = $1 AND enabled`); n != 2 {
		t.Fatalf("off: %d schedules enabled, polling must stay", n)
	}
	for _, h := range []string{hook, newHook} {
		if strings.Contains(e.logs.String(), strings.TrimPrefix(h, publicURL+"/webhooks/withings/")) {
			t.Error("logs contain a hook token")
		}
	}
}
