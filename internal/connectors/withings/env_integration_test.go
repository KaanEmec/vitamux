//go:build integration

package withings

import (
	"bytes"
	"context"
	"log/slog"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/httpx"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/jobs"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// Synthetic application and public URL; the log check proves no token value is ever logged.
const (
	clientID     = "synthetic-client"
	clientSecret = "synthetic-secret-SENTINEL"
	publicURL    = "https://vitamux.example.test"
	callback     = publicURL + "/oauth/withings/callback"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type env struct {
	d             *db.DB
	blobs         *blob.Store
	rt            *connectors.Runtime
	logs          *syncBuffer
	user, session uuid.UUID
	exec          func(sql string, args ...any)
	dump          func(t *testing.T)
	count         func(sql string, args ...any) int
}

// setup builds a runtime with the Withings connector pointed at apiURL, a fresh database,
// an owner with a live session and a timezone period.
func setup(t *testing.T, apiURL string) *env {
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
	reg, err := connectors.NewRegistry(New(Config{ClientID: clientID, ClientSecret: clientSecret, AuthURL: apiURL + "/oauth2_user/authorize2", APIURL: apiURL}))
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := url.Parse(publicURL)
	e := &env{d: db.New(pool), blobs: blobs, logs: &syncBuffer{}, user: uuid.New(), session: uuid.New()}
	e.rt = connectors.New(connectors.Config{
		DB: e.d, Blobs: blobs, Keys: keys, Registry: reg, PublicURL: pub,
		HTTP: httpx.New(httpx.Options{Timeout: 5 * time.Second}),
		Log:  slog.New(slog.NewJSONHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	e.exec = func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	e.count = func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	e.dump = func(t *testing.T) {
		rows, err := pool.Query(ctx, `SELECT j.kind, j.status, coalesce(r.error_class, ''), coalesce(r.error_message, '')
			FROM jobs j LEFT JOIN job_runs r ON r.job_id = j.id WHERE j.status <> 'succeeded' LIMIT 5`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var k, s, c, m string
			_ = rows.Scan(&k, &s, &c, &m)
			t.Logf("job %s %s: %s %s", k, s, c, m)
		}
	}
	e.exec("INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')", e.user)
	e.exec(`INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES (gen_random_uuid(), $1, 'Europe/Berlin', '2000-01-01Z')`, e.user)
	e.newSession()
	t.Cleanup(func() {
		for _, s := range []string{clientSecret, "synthetic-access", "synthetic-refresh", "synthetic-code"} {
			if bytes.Contains([]byte(e.logs.String()), []byte(s)) {
				t.Errorf("logs contain a secret (%s…)", s[:12])
			}
		}
	})
	return e
}

func (e *env) newSession() {
	e.session = uuid.New()
	e.exec(`INSERT INTO sessions (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, now() + interval '1 day')`,
		e.session, e.user, e.session[:])
}

// run runs a job runner with the sync, backfill and normalize handlers until no job is due
// or running.
func (e *env) run(t *testing.T) {
	t.Helper()
	reg, err := normalize.NewRegistry(Normalizer{})
	if err != nil {
		t.Fatal(err)
	}
	r := jobs.NewRunner(e.d, jobs.Config{
		Poll: 20 * time.Millisecond, ReapEvery: 100 * time.Millisecond, Lease: 10 * time.Second,
		Heartbeat: time.Second, Grace: 2 * time.Second, RetryBase: time.Hour, RetryMax: time.Hour,
	})
	r.Register(jobs.KindSync, e.rt.Handle)
	r.Register(connectors.KindBackfillUnit, e.rt.HandleBackfillUnit)
	p := &normalize.Processor{DB: e.d, Blobs: e.blobs, Registry: reg, Log: slog.New(slog.NewJSONHandler(e.logs, nil))}
	r.Register(ingest.KindNormalizeBatch, p.BatchJob())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(60 * time.Second)
	for e.count(`SELECT count(*) FROM jobs WHERE status = 'running' OR (status = 'queued' AND run_at <= now())`) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("jobs did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (e *env) allSucceeded(t *testing.T) {
	t.Helper()
	if n := e.count(`SELECT count(*) FROM jobs WHERE status <> 'succeeded'`); n > 0 {
		e.dump(t)
		t.Fatalf("%d jobs did not succeed", n)
	}
}
