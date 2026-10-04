//go:build integration

package remote

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/connectors/example"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/jobs"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

const (
	otherProvider = "other_sidecar"
	otherStream   = "other_sidecar.heart_rate"
	binding       = "synthetic-browser-binding"
)

type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

type env struct {
	d             *db.DB
	exec          func(t *testing.T, sql string, args ...any)
	count         func(t *testing.T, sql string, args ...any) int
	blobs         *blob.Store
	rt            *connectors.Runtime
	user, session uuid.UUID
}

// setup builds a runtime whose registry holds the given sidecar connectors, on a fresh
// database with an owner, a session and a timezone. Logs must never contain a secret.
func setup(t *testing.T, sidecars ...*Connector) *env {
	t.Helper()
	_, pool := dbtest.Migrated(t)
	keyPath := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.WriteKeyFile(keyPath); err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.Load(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.Open(t.TempDir(), keys)
	if err != nil {
		t.Fatal(err)
	}
	logs := &logBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e := &env{d: db.New(pool), blobs: blobs, user: uuid.New(), session: uuid.New()}
	e.exec = func(t *testing.T, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	e.count = func(t *testing.T, sql string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	cs := make([]connectors.Connector, 0, len(sidecars))
	for _, c := range sidecars {
		c.log, c.onDescribe = log.With("provider", c.name), Recorder(e.d)
		if err := c.Discover(t.Context()); err != nil {
			t.Fatal(err)
		}
		cs = append(cs, c)
	}
	reg, err := connectors.NewRegistry(cs...)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := url.Parse("https://vitamux.example.test")
	e.rt = connectors.New(connectors.Config{DB: e.d, Blobs: blobs, Keys: keys, Registry: reg, PublicURL: pub, Log: log})
	e.exec(t, "INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')", e.user)
	e.exec(t, `INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES (gen_random_uuid(), $1, 'Europe/Berlin', '2000-01-01Z')`, e.user)
	e.exec(t, `INSERT INTO sessions (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, now() + interval '1 day')`, e.session, e.user, e.session[:])
	t.Cleanup(func() {
		logs.mu.Lock()
		defer logs.mu.Unlock()
		for _, s := range []string{fakeSecret, fakePass, "synthetic-access-", "synthetic-refresh-", "step-1", "step-2"} {
			if bytes.Contains(logs.b.Bytes(), []byte(s)) {
				t.Errorf("logs contain a secret (%s…)", s[:8])
			}
		}
	})
	return e
}

// connect runs the prompt flow of provider: username and password, then the MFA code.
// conn reauthorizes that connection.
func (e *env) connect(t *testing.T, provider string, conn *uuid.UUID) (uuid.UUID, error) {
	t.Helper()
	ctx := t.Context()
	step, state, err := e.rt.BeginAuth(ctx, connectors.AuthRequest{UserID: e.user, SessionID: e.session, Provider: provider, ConnectionID: conn, Binding: binding})
	if err != nil {
		return uuid.Nil, err
	}
	if step.Prompt == nil || len(step.Prompt.Fields) != 2 || step.Session != nil {
		t.Fatalf("begin: %+v", step)
	}
	step, state, _, err = e.rt.ContinueAuth(ctx, provider, state, binding, map[string]string{"username": fakeUser, "password": fakePass})
	if err != nil {
		return uuid.Nil, err
	}
	if step.Prompt == nil || step.Prompt.Fields[0].Kind != connectors.FieldCode {
		t.Fatalf("second step: %+v", step)
	}
	_, _, id, err := e.rt.ContinueAuth(ctx, provider, state, binding, map[string]string{"code": fakeCode})
	return id, err
}

func (e *env) sync(t *testing.T, id uuid.UUID, stream string) {
	t.Helper()
	if _, _, err := jobs.Enqueue(t.Context(), e.d.Q(), jobs.NewJob{Kind: jobs.KindSync, ConnectionID: &id, Exclusive: true,
		Payload: jobs.SyncPayload{Stream: stream, Mode: connectors.ModeIncremental, Slot: time.Now()}}); err != nil {
		t.Fatal(err)
	}
}

// run runs the sync, backfill and normalize handlers until no job is due or running.
func (e *env) run(t *testing.T) {
	t.Helper()
	reg, err := normalize.NewRegistry(example.Normalizer{Stream: fakeStream}, example.Normalizer{Stream: otherStream})
	if err != nil {
		t.Fatal(err)
	}
	r := jobs.NewRunner(e.d, jobs.Config{
		Poll: 20 * time.Millisecond, ReapEvery: 100 * time.Millisecond, Lease: 10 * time.Second,
		Heartbeat: time.Second, Grace: 2 * time.Second, RetryBase: time.Hour, RetryMax: time.Hour,
	})
	r.Register(jobs.KindSync, e.rt.Handle)
	r.Register(connectors.KindBackfillUnit, e.rt.HandleBackfillUnit)
	r.Register(ingest.KindNormalizeBatch, (&normalize.Processor{DB: e.d, Blobs: e.blobs, Registry: reg, Log: slog.New(slog.DiscardHandler)}).BatchJob())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(60 * time.Second)
	for e.count(t, `SELECT count(*) FROM jobs WHERE status = 'running' OR (status = 'queued' AND run_at <= now())`) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("jobs did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// sidecar is a connector for srv under name; fetches time out after fetch.
func sidecar(t *testing.T, srv *httptest.Server, name string, fetch time.Duration) *Connector {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	l := defaultLimits
	l.fetch = fetch
	return newConnector(Options{Name: name, URL: u, Secret: fakeSecret}, l)
}

// The sidecar lifecycle through connectors.Runtime and jobs, as fake Withings: connect with
// prompts and MFA → paused (unofficial) → resume → first sync → backfill → incremental →
// expired access token (refresh rotation) → rotation during a fetch → upstream bump →
// revoked → needs_reauth → reauth → rate limit → schema drift. A second sidecar that hangs,
// cuts a page short or crashes fails only its own connection, with nothing committed.
func TestLifecycle(t *testing.T) {
	ctx := t.Context()
	f, srv := newFake(t)
	g, srvG := newFake(t)
	g.do(func() {
		g.desc.Provider, g.desc.Name, g.desc.Streams[0].Name = otherProvider, "Other sidecar", otherStream
	})
	f.add(25, t0)
	g.add(5, t0)
	cf, cg := sidecar(t, srv, fakeProvider, 5*time.Second), sidecar(t, srvG, otherProvider, 300*time.Millisecond)
	e := setup(t, cf, cg)

	if n := e.count(t, `SELECT count(*) FROM providers WHERE code IN ($1, $2) AND name IN ('Example sidecar', 'Other sidecar')`, fakeProvider, otherProvider); n != 2 {
		t.Fatal("describe did not register the providers")
	}

	// 1. Connect: a wrong password ends the flow (its state is used up); the full flow
	// creates a paused remote connection with the upstream, schedules and no queued sync.
	step, state, err := e.rt.BeginAuth(ctx, connectors.AuthRequest{UserID: e.user, SessionID: e.session, Provider: fakeProvider, Binding: binding})
	if err != nil || step.Prompt == nil {
		t.Fatalf("begin: %v", err)
	}
	if _, _, _, err := e.rt.ContinueAuth(ctx, fakeProvider, state, binding, map[string]string{"username": fakeUser, "password": "wrong"}); !errors.Is(err, connectors.ErrReauthRequired) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, _, _, err := e.rt.ContinueAuth(ctx, fakeProvider, state, binding, map[string]string{"username": fakeUser, "password": fakePass}); !errors.Is(err, connectors.ErrAuthState) {
		t.Fatalf("reused state: %v", err)
	}
	id, err := e.connect(t, fakeProvider, nil)
	if err != nil {
		t.Fatal(err)
	}
	q := func(sql string) int { t.Helper(); return e.count(t, sql, id) }
	if q(`SELECT count(*) FROM connections WHERE id = $1 AND status = 'paused' AND mode = 'remote' AND upstream->>'version' = '1.2.3'`) != 1 ||
		q(`SELECT count(*) FROM jobs WHERE connection_id = $1`) != 0 || q(`SELECT count(*) FROM schedules WHERE connection_id = $1`) != 2 {
		t.Fatal("connect: not a paused remote connection with schedules and no sync")
	}

	expect := func(step string, raw, quarantined, measurements int, status string) {
		t.Helper()
		if n := q(`SELECT count(*) FROM raw_payloads WHERE connection_id = $1`); n != raw {
			t.Fatalf("%s: %d raw rows, want %d", step, n, raw)
		}
		if n := q(`SELECT count(*) FROM raw_payloads WHERE connection_id = $1 AND status = 'quarantined'`); n != quarantined {
			t.Fatalf("%s: %d quarantined rows, want %d", step, n, quarantined)
		}
		if n := q(`SELECT count(*) FROM measurements WHERE connection_id = $1 AND superseded_at IS NULL`); n != measurements {
			t.Fatalf("%s: %d measurements, want %d", step, n, measurements)
		}
		if n := q(`SELECT count(*) FROM connections WHERE id = $1 AND status = '` + status + `'`); n != 1 {
			t.Fatalf("%s: connection is not %s", step, status)
		}
	}
	allSucceeded := func(step string) {
		t.Helper()
		if n := e.count(t, `SELECT count(*) FROM jobs WHERE status <> 'succeeded'`); n != 0 {
			t.Fatalf("%s: %d jobs did not succeed", step, n)
		}
	}
	cursorAt := func(step string, s fakeSample) {
		t.Helper()
		if n := e.count(t, `SELECT count(*) FROM sync_cursors WHERE connection_id = $1 AND (cursor->>'since')::timestamptz = $2`, id, s.Time); n != 1 {
			t.Fatalf("%s: cursor is not at %s", step, s.Time)
		}
	}
	last := func() fakeSample { f.mu.Lock(); defer f.mu.Unlock(); return f.samples[len(f.samples)-1] }

	// 2. The owner resumes it; the first sync pages through all 25 samples.
	e.exec(t, `UPDATE connections SET status = 'active' WHERE id = $1`, id)
	e.sync(t, id, fakeStream)
	e.run(t)
	allSucceeded("first sync")
	expect("first sync", 25, 0, 25, "active")
	cursorAt("first sync", last())

	// 3. Backfill the window in 30-day units: re-fetched samples are duplicates, the cursor stays.
	if _, err := e.rt.CreateBackfill(ctx, connectors.BackfillSpec{ConnectionID: id, Stream: fakeStream, From: t0.Add(-40 * 24 * time.Hour), To: t0.Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	e.run(t)
	allSucceeded("backfill")
	expect("backfill", 25, 0, 25, "active")
	cursorAt("backfill", last())
	if q(`SELECT count(*) FROM backfills WHERE connection_id = $1 AND status = 'done'`) != 1 {
		t.Fatal("backfill not done")
	}

	// 4. Incremental: five new samples.
	f.add(5, t0)
	e.sync(t, id, fakeStream)
	e.run(t)
	allSucceeded("incremental")
	expect("incremental", 30, 0, 30, "active")
	cursorAt("incremental", last())

	// 5. Rotation: the sidecar refuses the expired access token; the runtime refreshes once
	// through the sidecar and stores the rotated pair. Then the sidecar rotates the pair during
	// a fetch: the runtime stores it with the page, and the next sync works with it.
	version := q(`SELECT version FROM credentials WHERE connection_id = $1`)
	f.do(func() { f.access = "expired" })
	f.add(2, t0)
	e.sync(t, id, fakeStream)
	e.run(t)
	allSucceeded("refresh")
	expect("refresh", 32, 0, 32, "active")
	if n := q(`SELECT version FROM credentials WHERE connection_id = $1`); n != version+1 || f.called("/v1/auth/refresh") != 1 {
		t.Fatalf("refresh: credentials version %d, %d refreshes", n, f.called("/v1/auth/refresh"))
	}
	f.do(func() { f.rotate = true })
	f.add(2, t0)
	e.sync(t, id, fakeStream)
	e.run(t)
	f.do(func() { f.rotate = false })
	f.add(1, t0)
	e.sync(t, id, fakeStream)
	e.run(t)
	allSucceeded("rotation")
	expect("rotation", 35, 0, 35, "active")
	if n := q(`SELECT version FROM credentials WHERE connection_id = $1`); n != version+2 || f.called("/v1/auth/refresh") != 1 {
		t.Fatalf("rotation: credentials version %d, %d refreshes", n, f.called("/v1/auth/refresh"))
	}

	// 6. The sidecar ships a new upstream version: recorded on the connection and audited.
	f.do(func() { f.desc.Upstream.Version = "1.2.4" })
	if err := cf.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	if q(`SELECT count(*) FROM connections WHERE id = $1 AND upstream->>'version' = '1.2.4'`) != 1 ||
		e.count(t, `SELECT count(*) FROM audit_events WHERE action = 'connection.upstream_changed' AND target_id = $1
			AND detail->'from'->>'version' = '1.2.3' AND detail->'to'->>'version' = '1.2.4'`, id.String()) != 1 {
		t.Fatal("upstream bump not recorded and audited")
	}

	// 7. Revoked: access and refresh refused; the sync stops with reauth_required.
	f.do(func() { f.revoked = true })
	f.add(1, t0)
	e.sync(t, id, fakeStream)
	e.run(t)
	expect("revoked", 35, 0, 35, "needs_reauth")
	if q(`SELECT count(*) FROM jobs WHERE connection_id = $1 AND status = 'dead'`) != 1 {
		t.Fatal("revoked: the sync is not dead")
	}

	// 8. Reauthorize the same account through the prompts: active again, and the next sync
	// picks up what arrived meanwhile.
	if again, err := e.connect(t, fakeProvider, &id); err != nil || again != id {
		t.Fatalf("reauth: %v", err)
	}
	e.sync(t, id, fakeStream)
	e.run(t)
	expect("reauth", 36, 0, 36, "active")
	cursorAt("reauth", last())

	// 9. Rate limit: the job is rescheduled after retry_after_s and the provider blocked; no
	// failure is counted.
	f.do(func() { f.limited = 7 })
	e.exec(t, `DELETE FROM jobs`)
	e.sync(t, id, fakeStream)
	e.run(t)
	if q(`SELECT count(*) FROM jobs WHERE connection_id = $1 AND status = 'queued' AND run_at > now() + interval '3 seconds'`) != 1 ||
		q(`SELECT count(*) FROM connections WHERE id = $1 AND last_error_class = 'rate_limited' AND consecutive_failures = 0`) != 1 ||
		e.count(t, `SELECT count(*) FROM provider_rate_state r JOIN providers p ON p.id = r.provider_id
			WHERE p.code = $1 AND r.blocked_until > now() + interval '3 seconds'`, fakeProvider) != 1 {
		t.Fatal("rate limit: not rescheduled and blocked")
	}
	f.do(func() { f.limited = 0 })
	e.exec(t, `UPDATE provider_rate_state SET blocked_until = now()`)
	e.exec(t, `UPDATE jobs SET run_at = now() WHERE status = 'queued'`)
	f.add(1, t0)
	e.run(t)
	allSucceeded("after rate limit")
	expect("after rate limit", 37, 0, 37, "active")

	// 10. Schema drift: the page's raw lines are stored quarantined, nothing normalized, the
	// cursor stays, the stream and connection are degraded.
	before := last()
	f.do(func() { f.drift = true })
	f.add(3, t0)
	e.sync(t, id, fakeStream)
	e.run(t)
	expect("drift", 40, 3, 37, "degraded")
	cursorAt("drift", before)
	if q(`SELECT count(*) FROM sync_cursors WHERE connection_id = $1 AND status = 'degraded' AND status_reason = 'schema_drift'`) != 1 {
		t.Fatal("drift: stream not degraded")
	}
	f.do(func() { f.drift = false })

	// 11. Another sidecar hangs, then cuts a page short, then crashes: each fails only its own
	// connection with a transient error and commits nothing; the first keeps syncing.
	other, err := e.connect(t, otherProvider, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE connections SET status = 'active' WHERE id = $1`, other)
	failures := 0
	for _, breakIt := range []func(){
		func() { g.do(func() { g.hang = 2 * time.Second }) },
		func() {
			g.do(func() {
				g.hang = 0
				g.override["/v1/fetch"] = func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/x-ndjson")
					_, _ = w.Write([]byte(ndjson(`{"type":"raw","external_key":"sample:x","content_type":"application/json","body":{"id":"x"}}`)))
				}
			})
		},
		func() { srvG.Close(); cg.mu.Lock(); cg.checked = time.Time{}; cg.mu.Unlock() }, // unavailable on the next use
	} {
		breakIt()
		e.exec(t, `DELETE FROM jobs`)
		f.add(1, t0)
		e.sync(t, id, fakeStream)
		e.sync(t, other, otherStream)
		e.run(t)
		failures++
		if e.count(t, `SELECT count(*) FROM raw_payloads WHERE connection_id = $1`, other) != 0 ||
			e.count(t, `SELECT count(*) FROM connections WHERE id = $1 AND status = 'active' AND last_error_class = 'transient' AND consecutive_failures = $2`, other, failures) != 1 ||
			e.count(t, `SELECT count(*) FROM jobs WHERE connection_id = $1 AND status = 'queued' AND run_at > now()`, other) != 1 {
			t.Fatalf("broken sidecar %d: not a transient failure of its connection only", failures)
		}
		if e.count(t, `SELECT count(*) FROM jobs WHERE connection_id = $1 AND kind = $2 AND status = 'succeeded'`, id, jobs.KindSync) != 1 {
			t.Fatalf("broken sidecar %d: the healthy connection did not sync", failures)
		}
	}
	expect("isolation", 43, 3, 40, "active")
}
