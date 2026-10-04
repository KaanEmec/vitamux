//go:build integration

package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/httpx"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/jobs"
	fp "github.com/KaanEmec/vitamux/internal/testutil/fakeprovider"
)

const stream = "withings.measures"

// fake is a scripted connector for the "withings" provider (seeded, so provider_rate_state works).
type fake struct {
	auth    AuthKind
	fetch   func(ctx context.Context, c Conn, cred Credentials, u WorkUnit, out *RawSink) (FetchResult, error)
	refresh func(ctx context.Context, c Conn, cred Credentials) (Credentials, error)
	mu      sync.Mutex
	plans   []PlanRequest
	fetches atomic.Int32
}

func (f *fake) Describe() Descriptor {
	return Descriptor{
		Provider: "withings", Version: "test", AuthKind: cmpOr(f.auth, AuthNone),
		Streams: []StreamSpec{{Name: stream, Interval: time.Hour, Lookback: 7 * 24 * time.Hour,
			MaxBackfill: 2 * 365 * 24 * time.Hour, UnitSize: 24 * time.Hour}},
		RateLimits:   []RateLimitSpec{{Requests: 1000, Per: time.Second}},
		Capabilities: Capabilities{Backfill: true},
	}
}

func (f *fake) Plan(_ context.Context, _ Conn, req PlanRequest) ([]WorkUnit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.plans = append(f.plans, req)
	return []WorkUnit{{Cursor: req.Cursor, From: req.From, To: req.To}}, nil
}

func (f *fake) Fetch(ctx context.Context, c Conn, cred Credentials, u WorkUnit, out *RawSink) (FetchResult, error) {
	f.fetches.Add(1)
	return f.fetch(ctx, c, cred, u, out)
}

func (f *fake) Refresh(ctx context.Context, c Conn, cred Credentials) (Credentials, error) {
	return f.refresh(ctx, c, cred)
}

func cmpOr(k, def AuthKind) AuthKind {
	if k == "" {
		return def
	}
	return k
}

type env struct {
	d     *db.DB
	keys  *crypto.Keyring
	blobs *blob.Store
	reg   *Registry
	rt    *Runtime
	f     *fake
	conn  uuid.UUID
	user  uuid.UUID
	exec  func(sql string, args ...any)
	scan  func(sql string, args []any, dest ...any)
}

func setup(t *testing.T, f *fake) *env {
	t.Helper()
	ctx := t.Context()
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
	reg, err := NewRegistry(f)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{d: db.New(pool), keys: keys, blobs: blobs, reg: reg, f: f, conn: uuid.New(), user: uuid.New()}
	e.exec = func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	e.scan = func(sql string, args []any, dest ...any) {
		t.Helper()
		if err := pool.QueryRow(ctx, sql, args...).Scan(dest...); err != nil {
			t.Fatal(err)
		}
	}
	e.exec("INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')", e.user)
	e.exec(`INSERT INTO connections (id, user_id, provider_id, mode, status)
		VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'withings'), 'in_process', 'active')`, e.conn, e.user)
	e.rt = e.newRuntime()
	return e
}

// newRuntime is a fresh process: same database, empty in-memory buckets and blocks.
func (e *env) newRuntime() *Runtime {
	return New(Config{DB: e.d, Blobs: e.blobs, Keys: e.keys, Registry: e.reg, HTTP: httpx.New(httpx.Options{Timeout: 5 * time.Second})})
}

func (e *env) enqueue(t *testing.T, p jobs.SyncPayload) uuid.UUID {
	t.Helper()
	if p.Stream == "" {
		p.Stream = stream
	}
	if p.Mode == "" {
		p.Mode = ModeIncremental
	}
	if p.Slot.IsZero() {
		p.Slot = time.Now()
	}
	id, _, err := jobs.Enqueue(t.Context(), e.d.Q(), jobs.NewJob{
		Kind: jobs.KindSync, ConnectionID: &e.conn, Exclusive: true, Payload: p,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// drive runs a job runner with rt until job id has wantRuns finished runs, then drains it.
func (e *env) drive(t *testing.T, rt *Runtime, id uuid.UUID, wantRuns int) dbq.Job {
	t.Helper()
	r := jobs.NewRunner(e.d, jobs.Config{
		Poll: 20 * time.Millisecond, ReapEvery: 100 * time.Millisecond, Lease: 10 * time.Second,
		Heartbeat: time.Second, Grace: 2 * time.Second, RetryBase: time.Hour, RetryMax: time.Hour,
	})
	r.Register(jobs.KindSync, rt.Handle)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(15 * time.Second)
	for len(e.finishedRuns(t, id)) < wantRuns {
		if time.Now().After(deadline) {
			t.Fatalf("job did not finish run %d", wantRuns)
		}
		time.Sleep(10 * time.Millisecond)
	}
	j, err := e.d.Q().GetJob(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func (e *env) finishedRuns(t *testing.T, id uuid.UUID) []dbq.JobRun {
	t.Helper()
	rs, err := e.d.Q().ListJobRuns(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var out []dbq.JobRun
	for _, r := range rs {
		if r.Outcome != nil {
			out = append(out, r)
		}
	}
	return out
}

type connState struct {
	status, class string
	failures      int
	lastSuccess   bool
}

func (e *env) connection(t *testing.T) connState {
	t.Helper()
	var s connState
	var class *string
	e.scan(`SELECT status, last_error_class, consecutive_failures, last_success_at IS NOT NULL FROM connections WHERE id = $1`,
		[]any{e.conn}, &s.status, &class, &s.failures, &s.lastSuccess)
	if class != nil {
		s.class = *class
	}
	return s
}

// cursor returns the stored stream cursor and status ("" when there is no row).
func (e *env) cursor(t *testing.T) (cursor, status string) {
	t.Helper()
	var c *string
	var st string
	var n int
	e.scan(`SELECT count(*) FROM sync_cursors WHERE connection_id = $1`, []any{e.conn}, &n)
	if n == 0 {
		return "", ""
	}
	e.scan(`SELECT cursor::text, status || coalesce(':' || status_reason, '') FROM sync_cursors WHERE connection_id = $1 AND stream = $2`,
		[]any{e.conn, stream}, &c, &st)
	if c == nil {
		return "", st
	}
	return *c, st
}

// raw lists external_key:status of the stored raw payloads in id order.
func (e *env) raw(t *testing.T) string {
	t.Helper()
	var s string
	e.scan(`SELECT coalesce(string_agg(external_key || ':' || status, ',' ORDER BY id), '') FROM raw_payloads`, nil, &s)
	return s
}

// normalizeJobs counts the normalize_batch jobs, each for a batch of this connection.
func (e *env) normalizeJobs(t *testing.T) int {
	t.Helper()
	var n, bad int
	e.scan(`SELECT count(*), count(*) FILTER (WHERE connection_id IS DISTINCT FROM $2 OR dedupe_key <> 'normalize_batch:' || (payload->>'batch_id')
		OR NOT EXISTS (SELECT 1 FROM ingest_batches b WHERE b.id::text = payload->>'batch_id'))
		FROM jobs WHERE kind = $1`, []any{ingest.KindNormalizeBatch, e.conn}, &n, &bad)
	if bad > 0 {
		t.Fatalf("%d normalize_batch jobs without their connection, dedupe key or batch", bad)
	}
	return n
}

func item(key string) ingest.RawItem {
	return ingest.RawItem{ExternalKey: key, ContentType: "application/json", Body: []byte(`{"synthetic":"` + key + `"}`)}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestErrorTransitions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		conn      connState
		jobStatus string
		run       string // outcome/error_class
		attempts  int32
		stream    string // cursor row status
		raw       string
	}{
		{name: "success", conn: connState{status: "active", lastSuccess: true},
			jobStatus: "succeeded", run: "succeeded/", attempts: 1, stream: "ok", raw: "k1:stored"},
		{name: "reauth", err: fmt.Errorf("401: %w", ErrReauthRequired), conn: connState{"needs_reauth", ClassReauthRequired, 1, false},
			jobStatus: "dead", run: "failed/reauth_required", attempts: 1},
		{name: "rate_limited", err: &RateLimitedError{RetryAfter: 2 * time.Minute}, conn: connState{"active", ClassRateLimited, 0, false},
			jobStatus: "queued", run: "rescheduled/rate_limited", attempts: 0},
		{name: "transient", err: fmt.Errorf("503: %w", ErrTransient), conn: connState{"active", ClassTransient, 1, false},
			jobStatus: "queued", run: "failed/transient", attempts: 1},
		{name: "unclassified", err: errors.New("connection reset"), conn: connState{"active", ClassTransient, 1, false},
			jobStatus: "queued", run: "failed/transient", attempts: 1},
		{name: "schema_drift", err: &SchemaDriftError{Endpoint: "/measure", Fingerprint: "abc"}, conn: connState{"degraded", ClassSchemaDrift, 1, false},
			jobStatus: "dead", run: "failed/schema_drift", attempts: 1, stream: "degraded:schema_drift", raw: "k1:quarantined"},
		{name: "permanent", err: fmt.Errorf("400: %w", ErrPermanent), conn: connState{"error", ClassPermanent, 1, false},
			jobStatus: "dead", run: "failed/permanent", attempts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{fetch: func(_ context.Context, _ Conn, _ Credentials, _ WorkUnit, out *RawSink) (FetchResult, error) {
				out.Put(item("k1"))
				return FetchResult{Done: true, NextCursor: json.RawMessage(`{"n": 1}`)}, tc.err
			}}
			e := setup(t, f)
			if tc.err == nil { // a degraded, failing connection recovers on success
				e.exec(`UPDATE connections SET status = 'degraded', consecutive_failures = 3, last_error_class = 'transient' WHERE id = $1`, e.conn)
				e.exec(`INSERT INTO sync_cursors (connection_id, stream, status, status_reason) VALUES ($1, $2, 'degraded', 'schema_drift')`, e.conn, stream)
			}
			before := time.Now()
			id := e.enqueue(t, jobs.SyncPayload{})
			j := e.drive(t, e.rt, id, 1)
			runs := e.finishedRuns(t, id)
			if got := deref(runs[0].Outcome) + "/" + deref(runs[0].ErrorClass); got != tc.run {
				t.Errorf("run = %s, want %s", got, tc.run)
			}
			if j.Status != tc.jobStatus || j.Attempts != tc.attempts {
				t.Errorf("job = %s attempts %d, want %s attempts %d", j.Status, j.Attempts, tc.jobStatus, tc.attempts)
			}
			if got := e.connection(t); got != tc.conn {
				t.Errorf("connection = %+v, want %+v", got, tc.conn)
			}
			cur, st := e.cursor(t)
			if st != tc.stream {
				t.Errorf("stream status = %q, want %q", st, tc.stream)
			}
			if wantCur := map[bool]string{true: `{"n": 1}`}[tc.err == nil]; cur != wantCur {
				t.Errorf("cursor = %q, want %q", cur, wantCur)
			}
			if got := e.raw(t); got != tc.raw {
				t.Errorf("raw = %q, want %q", got, tc.raw)
			}
			// New raw rows are queued for normalization in their commit; quarantined ones are not.
			if got, want := e.normalizeJobs(t), map[bool]int{true: 1}[tc.err == nil]; got != want {
				t.Errorf("%d normalize_batch jobs, want %d", got, want)
			}
			if tc.name == "rate_limited" {
				var blocked time.Time
				e.scan(`SELECT blocked_until FROM provider_rate_state`, nil, &blocked)
				if blocked.Before(before.Add(2*time.Minute)) || j.RunAt.Before(before.Add(2*time.Minute)) {
					t.Errorf("blocked until %s, run at %s; want ≥ %s", blocked, j.RunAt, before.Add(2*time.Minute))
				}
			}
		})
	}
}

// J06.5: a 429 with Retry-After 120 reschedules ≥ 120 s later without using an attempt, and a
// restarted process inside the block window makes no provider call.
func TestRateLimitSurvivesRestart(t *testing.T) {
	provider := fp.New(t)
	provider.Expect(fp.Step{Method: http.MethodGet, Path: "/v1/measures", Reply: fp.RateLimited(120 * time.Second)})
	f := &fake{fetch: func(ctx context.Context, c Conn, _ Credentials, _ WorkUnit, out *RawSink) (FetchResult, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.URL+"/v1/measures", nil)
		if err != nil {
			return FetchResult{}, err
		}
		res, err := c.HTTP.Do(req)
		if err != nil {
			return FetchResult{}, err
		}
		defer res.Body.Close()
		return FetchResult{Done: true}, nil
	}}
	e := setup(t, f)

	before := time.Now()
	id := e.enqueue(t, jobs.SyncPayload{})
	j := e.drive(t, e.rt, id, 1)
	if j.Status != "queued" || j.Attempts != 0 || j.RunAt.Before(before.Add(120*time.Second)) {
		t.Fatalf("after 429: job %s attempts %d run at +%s; want queued, 0 attempts, ≥ 120 s", j.Status, j.Attempts, j.RunAt.Sub(before))
	}
	var blocked time.Time
	e.scan(`SELECT blocked_until FROM provider_rate_state`, nil, &blocked)
	if blocked.Before(before.Add(120 * time.Second)) {
		t.Fatalf("blocked_until only +%s", blocked.Sub(before))
	}

	// Restart: a new runtime (empty memory) picks the job up early; the shared block holds.
	e.exec(`UPDATE jobs SET run_at = now() WHERE id = $1`, id)
	j = e.drive(t, e.newRuntime(), id, 2)
	if n := f.fetches.Load(); n != 1 || len(provider.Requests()) != 1 {
		t.Fatalf("restart inside the block window: %d fetches, %d provider requests; want 1, 1", n, len(provider.Requests()))
	}
	runs := e.finishedRuns(t, id)
	if j.Status != "queued" || j.Attempts != 0 || !j.RunAt.Equal(blocked) || deref(runs[1].ErrorClass) != ClassRateLimited {
		t.Fatalf("after restart: job %s attempts %d run at %s (block %s), class %s", j.Status, j.Attempts, j.RunAt, blocked, deref(runs[1].ErrorClass))
	}
}

// oauthFake is a connector whose Fetch and Refresh talk to the fake provider like a real
// OAuth2 connector: bearer token on the API, refresh_token grant on /oauth2/token.
func oauthFake(provider *fp.Server) *fake {
	return &fake{
		auth: AuthOAuth2,
		fetch: func(ctx context.Context, c Conn, cred Credentials, _ WorkUnit, out *RawSink) (FetchResult, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.URL+"/v1/measures", nil)
			if err != nil {
				return FetchResult{}, err
			}
			req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
			res, err := c.HTTP.Do(req)
			if err != nil {
				return FetchResult{}, err
			}
			defer res.Body.Close()
			if res.StatusCode == http.StatusUnauthorized {
				return FetchResult{}, ErrReauthRequired
			}
			out.Put(item("m1"))
			return FetchResult{Done: true}, nil
		},
		refresh: func(ctx context.Context, c Conn, cred Credentials) (Credentials, error) {
			form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {cred.RefreshToken}}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.URL+"/oauth2/token", strings.NewReader(form.Encode()))
			if err != nil {
				return Credentials{}, err
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			res, err := c.HTTP.Do(req)
			if err != nil {
				return Credentials{}, err
			}
			defer res.Body.Close()
			if res.StatusCode == http.StatusBadRequest {
				return Credentials{}, fmt.Errorf("refresh refused: %w", ErrReauthRequired)
			}
			var tok struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
				ExpiresIn    int    `json:"expires_in"`
			}
			if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&tok) != nil {
				return Credentials{}, fmt.Errorf("token endpoint answered %d: %w", res.StatusCode, ErrTransient)
			}
			return Credentials{AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken,
				ExpiresAt: time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)}, nil
		},
	}
}

func (e *env) saveCreds(t *testing.T, c Credentials) {
	t.Helper()
	if err := e.rt.SaveCredentials(t.Context(), e.d.Q(), e.conn, c); err != nil {
		t.Fatal(err)
	}
}

func (e *env) storedCreds(t *testing.T) (Credentials, int32) {
	t.Helper()
	row, err := e.d.Q().GetCredentials(t.Context(), e.conn)
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.rt.creds.open(e.conn, row.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	return c, row.Version
}

// 100 concurrent callers on an expired token: exactly one refresh (the fake fails the test on
// a second one or on a stale refresh token) and nobody ends up needing reauthorization.
func TestConcurrentRefreshSingleFlight(t *testing.T) {
	provider := fp.New(t)
	provider.Expect(fp.TokenRefresh("/oauth2/token", "synthetic-refresh-1", "synthetic-access-2", "synthetic-refresh-2", time.Hour))
	f := oauthFake(provider)
	e := setup(t, f)
	e.saveCreds(t, Credentials{AccessToken: "synthetic-access-1", RefreshToken: "synthetic-refresh-1", ExpiresAt: time.Now().Add(-time.Minute)})

	c := Conn{ID: e.conn, UserID: e.user, Provider: "withings", HTTP: e.rt.clients.get(f.Describe())}
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for range 100 {
		wg.Go(func() {
			cred, _, err := e.rt.creds.current(t.Context(), c, f)
			if err == nil && cred.AccessToken != "synthetic-access-2" {
				err = errors.New("caller got a stale access token")
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent refresh: %v (reauth: %v)", err, errors.Is(err, ErrReauthRequired))
		}
	}
	if cred, v := e.storedCreds(t); cred.RefreshToken != "synthetic-refresh-2" || v != 2 {
		t.Fatalf("stored credentials: version %d, rotated refresh token stored: %v", v, cred.RefreshToken == "synthetic-refresh-2")
	}
}

// A refused access token is refreshed once and the page retried; a refused refresh token
// makes the connection needs_reauth without retries.
func TestRefreshOnUnauthorized(t *testing.T) {
	t.Run("recovers", func(t *testing.T) {
		provider := fp.New(t)
		provider.Expect(
			fp.GET("/v1/measures", "synthetic-access-1", fp.Unauthorized()),
			fp.TokenRefresh("/oauth2/token", "synthetic-refresh-1", "synthetic-access-2", "synthetic-refresh-2", time.Hour),
			fp.GET("/v1/measures", "synthetic-access-2", fp.JSON(http.StatusOK, map[string]any{"status": 0})),
		)
		e := setup(t, oauthFake(provider))
		e.saveCreds(t, Credentials{AccessToken: "synthetic-access-1", RefreshToken: "synthetic-refresh-1", ExpiresAt: time.Now().Add(time.Hour)})
		j := e.drive(t, e.rt, e.enqueue(t, jobs.SyncPayload{}), 1)
		if j.Status != "succeeded" || e.connection(t).status != "active" || e.raw(t) != "m1:stored" {
			t.Fatalf("job %s, connection %+v, raw %q", j.Status, e.connection(t), e.raw(t))
		}
		if _, v := e.storedCreds(t); v != 2 {
			t.Fatalf("credentials version %d, want 2", v)
		}
	})
	t.Run("refresh refused", func(t *testing.T) {
		provider := fp.New(t)
		provider.Expect(fp.Step{Method: http.MethodPost, Path: "/oauth2/token", Reply: fp.InvalidGrant()})
		e := setup(t, oauthFake(provider))
		e.saveCreds(t, Credentials{AccessToken: "synthetic-access-1", RefreshToken: "synthetic-refresh-1", ExpiresAt: time.Now().Add(-time.Minute)})
		j := e.drive(t, e.rt, e.enqueue(t, jobs.SyncPayload{}), 1)
		if c := e.connection(t); j.Status != "dead" || c.status != "needs_reauth" || e.f.fetches.Load() != 0 {
			t.Fatalf("job %s, connection %+v, fetches %d", j.Status, c, e.f.fetches.Load())
		}
		if _, v := e.storedCreds(t); v != 1 {
			t.Fatalf("credentials version %d, want 1 (nothing rotated)", v)
		}
	})
}

// A crash between storing raw rows and committing leaves neither raw rows nor a moved cursor;
// pages already committed keep theirs.
func TestCursorNeverAdvancesWithoutRaw(t *testing.T) {
	f := &fake{fetch: func(_ context.Context, _ Conn, _ Credentials, u WorkUnit, out *RawSink) (FetchResult, error) {
		if u.Cursor == nil {
			out.Put(item("page1"))
			return FetchResult{NextCursor: json.RawMessage(`{"page": 1}`)}, nil
		}
		out.Put(item("page2"))
		return FetchResult{NextCursor: json.RawMessage(`{"page": 2}`), Done: true}, nil
	}}
	e := setup(t, f)
	var commits atomic.Int32
	e.rt.beforeCommit = func() error {
		if commits.Add(1) == 2 {
			return errors.New("injected crash before commit")
		}
		return nil
	}
	j := e.drive(t, e.rt, e.enqueue(t, jobs.SyncPayload{}), 1)
	if cur, _ := e.cursor(t); j.Status != "queued" || cur != `{"page": 1}` || e.raw(t) != "page1:stored" {
		t.Fatalf("after crash: job %s, cursor %q, raw %q", j.Status, cur, e.raw(t))
	}
	var batches int
	e.scan(`SELECT count(*) FROM ingest_batches`, nil, &batches)
	if batches != 1 || e.normalizeJobs(t) != 1 {
		t.Fatalf("%d ingest batches, %d normalize jobs; want 1, 1 (the crashed commit rolled back)", batches, e.normalizeJobs(t))
	}

	e.rt.beforeCommit = nil // the retry resumes from the committed cursor
	e.exec(`UPDATE jobs SET run_at = now() WHERE id = $1`, j.ID)
	j = e.drive(t, e.rt, j.ID, 2)
	if cur, st := e.cursor(t); j.Status != "succeeded" || cur != `{"page": 2}` || st != "ok" || e.raw(t) != "page1:stored,page2:stored" {
		t.Fatalf("after retry: job %s, cursor %q %s, raw %q", j.Status, cur, st, e.raw(t))
	}
	if n := e.normalizeJobs(t); n != 2 {
		t.Fatalf("%d normalize jobs after retry, want 2", n)
	}
	if last := f.plans[len(f.plans)-1]; string(last.Cursor) != `{"page": 1}` {
		t.Fatalf("retry planned from cursor %s", last.Cursor)
	}
}

func TestCorrectionLeavesCursor(t *testing.T) {
	f := &fake{fetch: func(_ context.Context, _ Conn, _ Credentials, u WorkUnit, out *RawSink) (FetchResult, error) {
		out.Put(item("w1"))
		return FetchResult{Done: true, NextCursor: json.RawMessage(`{"n": 99}`)}, nil
	}}
	e := setup(t, f)
	e.exec(`INSERT INTO sync_cursors (connection_id, stream, cursor) VALUES ($1, $2, '{"n": 5}')`, e.conn, stream)
	slot := time.Now().Truncate(time.Second)
	from := slot.Add(-7 * 24 * time.Hour)
	j := e.drive(t, e.rt, e.enqueue(t, jobs.SyncPayload{Mode: ModeCorrection, Slot: slot, From: &from}), 1)
	if cur, _ := e.cursor(t); j.Status != "succeeded" || cur != `{"n": 5}` || e.raw(t) != "w1:stored" {
		t.Fatalf("job %s, cursor %q, raw %q", j.Status, cur, e.raw(t))
	}
	if p := f.plans[0]; !p.From.Equal(from) || !p.To.Equal(slot) || p.Mode != ModeCorrection {
		t.Fatalf("plan request %+v", p)
	}
	// Re-fetching the same content stores nothing new and queues no normalization.
	j = e.drive(t, e.rt, e.enqueue(t, jobs.SyncPayload{Mode: ModeCorrection, Slot: slot, From: &from}), 1)
	if n := e.normalizeJobs(t); j.Status != "succeeded" || n != 1 || e.raw(t) != "w1:stored" {
		t.Fatalf("second correction: job %s, %d normalize jobs, raw %q", j.Status, n, e.raw(t))
	}
}

func TestEnsureSchedules(t *testing.T) {
	f := &fake{}
	e := setup(t, f)
	for range 2 { // idempotent
		if err := EnsureSchedules(t.Context(), e.d.Q(), e.conn, f.Describe()); err != nil {
			t.Fatal(err)
		}
	}
	got, err := jobs.ListSchedules(t.Context(), e.d.Q(), &e.conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Mode != ModeCorrection || got[0].Interval != 24*time.Hour || got[0].Lookback != 7*24*time.Hour ||
		got[1].Mode != ModeIncremental || got[1].Interval != time.Hour {
		t.Fatalf("schedules: %+v", got)
	}
}

func TestDroppedStreamIsRetired(t *testing.T) {
	f := &fake{}
	e := setup(t, f)
	sc, err := jobs.EnsureSchedule(t.Context(), e.d.Q(), jobs.ScheduleSpec{ConnectionID: e.conn, Stream: "dropped", Mode: ModeIncremental, Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	// The stream drifted before it was dropped, which left the connection degraded.
	e.exec(`INSERT INTO sync_cursors (connection_id, stream, status, status_reason) VALUES ($1, 'dropped', 'degraded', 'schema_drift')`, e.conn)
	e.exec(`UPDATE connections SET status = 'degraded' WHERE id = $1`, e.conn)
	j := e.drive(t, e.rt, e.enqueue(t, jobs.SyncPayload{ScheduleID: sc.ID, Stream: "dropped"}), 1)
	if j.Status != "succeeded" {
		t.Fatalf("job %s, want succeeded", j.Status)
	}
	var enabled bool
	e.scan(`SELECT enabled FROM schedules WHERE id = $1`, []any{sc.ID}, &enabled)
	if enabled {
		t.Fatal("schedule of a dropped stream stays enabled")
	}
	if s := e.connection(t); s.status != "active" || s.failures != 0 {
		t.Fatalf("connection %+v, want active without failures", s)
	}
	var status string
	e.scan(`SELECT status FROM sync_cursors WHERE connection_id = $1 AND stream = 'dropped'`, []any{e.conn}, &status)
	if status != "ok" {
		t.Fatalf("stream status %q, want ok", status)
	}
}
