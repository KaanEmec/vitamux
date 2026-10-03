//go:build integration

package example

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/jobs"
	"github.com/KaanEmec/vitamux/internal/normalize"
	fp "github.com/KaanEmec/vitamux/internal/testutil/fakeprovider"
)

// syncBuf is a log sink the runner's goroutines can share.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// samples returns synthetic samples hr-first … hr-last, one minute apart.
func samples(first, last int) []map[string]any {
	var out []map[string]any
	for i := first; i <= last; i++ {
		out = append(out, map[string]any{
			"id": fmt.Sprintf("hr-%d", i), "time": fmt.Sprintf("2026-09-14T07:%02d:00Z", 30+i), "bpm": 60 + i, "device": "synthetic-band-01",
		})
	}
	return out
}

// heartRate expects one page request; page "" asserts that no page token is sent.
func heartRate(since, page string, reply fp.Response) fp.Step {
	return fp.Step{
		Method: "GET", Path: "/v1/heart-rate", Query: url.Values{"updated_since": {since}},
		Check: func(r *fp.Request) error {
			if r.Query.Get("page") != page {
				return fmt.Errorf("page token differs")
			}
			return nil
		},
		Reply: reply,
	}
}

func TestSyncThroughRuntime(t *testing.T) {
	ctx := context.Background()
	dbURL, pool := dbtest.Migrated(t)
	owner := dbtest.Pool(t, dbURL, db.OwnerRole)
	if _, err := owner.Exec(ctx, `INSERT INTO providers (code, name) VALUES ('example', 'Example')`); err != nil {
		t.Fatal(err)
	}
	// pgxpool may not be named outside internal/db (depguard), so the helpers are closures.
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	userID, connID := uuid.New(), uuid.New()
	mustExec(`INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic-hash')`, userID)
	mustExec(`INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES ($1, $2, 'Europe/Berlin', '2000-01-01')`, uuid.New(), userID)
	mustExec(`INSERT INTO connections (id, user_id, provider_id, mode, status)
		SELECT $1, $2, id, 'in_process', 'active' FROM providers WHERE code = 'example'`, connID, userID)

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "master.key")
	if _, err := crypto.WriteKeyFile(keyPath); err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.Load(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.Open(filepath.Join(dir, "blobs"), keys)
	if err != nil {
		t.Fatal(err)
	}

	provider := fp.New(t)
	reg, err := connectors.NewRegistry(New(Config{BaseURL: provider.URL}))
	if err != nil {
		t.Fatal(err)
	}
	norms, err := normalize.NewRegistry(Normalizer{})
	if err != nil {
		t.Fatal(err)
	}
	var logs syncBuf
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := db.New(pool)
	rt := connectors.New(connectors.Config{DB: d, Blobs: blobs, Keys: keys, Registry: reg, Log: log})
	proc := &normalize.Processor{DB: d, Blobs: blobs, Registry: norms, Log: log}
	runner := jobs.NewRunner(d, jobs.Config{Log: log, Poll: 50 * time.Millisecond})
	runner.Register(jobs.KindSync, rt.Handle)
	runner.Register(ingest.KindNormalizeBatch, proc.BatchJob())
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { runner.Run(runCtx); close(done) }()
	t.Cleanup(func() { stop(); <-done })

	// waitIdle waits until no job is queued or running and due.
	waitIdle := func() {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			var n int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM jobs WHERE status = 'running' OR (status = 'queued' AND run_at <= now())`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("jobs still due after 20s")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	assertState := func(raws, measurements int, cursor string) {
		t.Helper()
		var nRaw, nNormalized, nMeas, nDevice int
		if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status = 'normalized' AND external_key LIKE 'sample:hr-%')
			FROM raw_payloads WHERE stream = $1`, Stream).Scan(&nRaw, &nNormalized); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*), count(device_id) FROM measurements m JOIN metric_catalog c ON c.id = m.metric_id
			WHERE c.code = 'heart_rate' AND m.superseded_at IS NULL`).Scan(&nMeas, &nDevice); err != nil {
			t.Fatal(err)
		}
		if nRaw != raws || nNormalized != raws || nMeas != measurements || nDevice != measurements {
			t.Fatalf("raw %d (normalized %d), measurements %d (with device %d); want %d raw, %d measurements",
				nRaw, nNormalized, nMeas, nDevice, raws, measurements)
		}
		var got string
		var hw time.Time
		if err := pool.QueryRow(ctx, `SELECT cursor::text, high_watermark FROM sync_cursors WHERE stream = $1`, Stream).Scan(&got, &hw); err != nil {
			t.Fatal(err)
		}
		if got != cursor || hw.IsZero() {
			t.Fatalf("cursor %s, high watermark %v; want %s", got, hw, cursor)
		}
	}

	syncOnce := func(mode string) {
		t.Helper()
		_, _, err := jobs.Enqueue(ctx, d.Q(), jobs.NewJob{
			Kind: jobs.KindSync, ConnectionID: &connID, Exclusive: true,
			Payload: jobs.SyncPayload{Stream: Stream, Mode: mode, Slot: time.Now()},
		})
		if err != nil {
			t.Fatal(err)
		}
		waitIdle()
	}

	// First sync: two pages from the epoch; the first page's server time becomes the cursor.
	provider.Expect(
		heartRate("1970-01-01T00:00:00Z", "", fp.JSON(200, map[string]any{
			"server_time": "2026-09-14T08:00:00Z", "samples": samples(1, 2), "next_page": "synthetic-p2",
		})),
		heartRate("1970-01-01T00:00:00Z", "synthetic-p2", fp.JSON(200, map[string]any{
			"server_time": "2026-09-14T08:00:05Z", "samples": samples(3, 3), "next_page": nil,
		})),
	)
	syncOnce(connectors.ModeManual)
	assertState(3, 3, `{"since": "2026-09-14T08:00:00Z"}`)

	// Second sync resumes from the stored updated_since; hr-3 comes again unchanged.
	provider.Expect(heartRate("2026-09-14T08:00:00Z", "", fp.JSON(200, map[string]any{
		"server_time": "2026-09-14T09:00:00Z", "samples": samples(3, 4), "next_page": nil,
	})))
	syncOnce(connectors.ModeIncremental)
	assertState(4, 4, `{"since": "2026-09-14T09:00:00Z"}`)

	var failed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE status <> 'succeeded'`).Scan(&failed); err != nil || failed != 0 {
		t.Fatalf("%d jobs did not succeed (%v)", failed, err)
	}
	if l := logs.String(); strings.Contains(l, "synthetic-band-01") || strings.Contains(l, "synthetic-p2") {
		t.Fatal("logs contain payload values")
	}
}
