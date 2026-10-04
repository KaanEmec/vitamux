//go:build integration

package api

import (
	"encoding/json"
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
	"github.com/KaanEmec/vitamux/internal/backup"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/db/fixtureload"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/resolve"
	"github.com/KaanEmec/vitamux/internal/version"
)

// statusEnv is a router over a migrated database with direct owner-role access for seeding.
type statusEnv struct {
	t    *testing.T
	h    http.Handler
	d    *db.DB
	exec func(sql string, args ...any)
}

func newStatusEnv(t *testing.T, backups string) (*statusEnv, uuid.UUID) {
	t.Helper()
	u, app := dbtest.Migrated(t)
	ownerPool := dbtest.Pool(t, u, db.OwnerRole)
	d := db.New(app)
	rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), Options{DB: d, BackupDir: backups})
	if err != nil {
		t.Fatal(err)
	}
	e := &statusEnv{t: t, h: rt.mux, d: d, exec: func(sql string, args ...any) {
		t.Helper()
		if _, err := ownerPool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}}
	user := uuid.New()
	e.exec(`INSERT INTO users (id, username, password_hash) VALUES ($1, 'status-owner', 'synthetic')`, user)
	return e, user
}

// get calls the endpoint as an admin key of user, checks the response against the spec and decodes it.
func (e *statusEnv) get(user uuid.UUID, path string, want int, v any) {
	e.t.Helper()
	route, _, _ := strings.Cut(path, "?")
	pattern := "GET " + route
	req := request(e.t, http.MethodGet, path, nil)
	p := &auth.Principal{Kind: auth.APIKey, UserID: user, ID: uuid.New(), Scopes: []auth.Scope{auth.Admin}}
	res := serve(e.t, e.h, req.WithContext(auth.WithPrincipal(req.Context(), p)))
	body := checkResponse(e.t, pattern, res)
	if res.StatusCode != want {
		e.t.Fatalf("%s: %d, want %d: %s", path, res.StatusCode, want, body)
	}
	if v != nil {
		if err := json.Unmarshal(body, v); err != nil {
			e.t.Fatal(err)
		}
	}
}

func TestSystemStatus(t *testing.T) {
	backups := t.TempDir()
	e, user := newStatusEnv(t, backups)

	// Fresh instance: nothing degraded, no backup.
	var st oapi.SystemStatus
	e.get(user, "/api/v1/system/status", http.StatusOK, &st)
	if st.LastBackupAt != nil || len(st.DegradedConnections) != 0 || len(st.FailingJobs) != 0 {
		t.Fatalf("fresh status: %+v", st)
	}
	if st.Versions.App != version.Version || st.Versions.Schema == "" || st.Versions.Schema == "0" || st.Versions.Postgres == "" ||
		st.DatabaseSizeBytes <= 0 || st.BlobSizeBytes != 0 {
		t.Fatalf("versions and sizes: %+v", st)
	}

	// Seed: a failing connection, a healthy one, another owner's failing one, dead jobs, a blob and two backups.
	other := uuid.New()
	e.exec(`INSERT INTO users (id, username, password_hash) VALUES ($1, 'other-owner', 'synthetic')`, other)
	conn := func(owner uuid.UUID, provider string, status string, failures int, errClass string) uuid.UUID {
		id := uuid.New()
		e.exec(`INSERT INTO connections (id, user_id, provider_id, account_key, mode, status, consecutive_failures, last_error_class, last_success_at)
			VALUES ($1, $2, (SELECT id FROM providers WHERE code = $3), sha256($7::bytea), 'in_process', $4, $5, NULLIF($6, ''), now() - interval '2 days')`,
			id, owner, provider, status, failures, errClass, id[:])
		return id
	}
	bad := conn(user, "withings", "active", 4, "transient")
	conn(user, "garmin", "active", 0, "")
	otherBad := conn(other, "apple_health", "active", 4, "transient")
	job := func(id uuid.UUID, connID *uuid.UUID, status string, age string) {
		e.exec(`INSERT INTO jobs (id, kind, status, connection_id, attempts, finished_at) VALUES ($1, 'sync', $2, $3, 5, now() - $4::interval)`, id, status, connID, age)
	}
	dead, old, ok, foreign := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	job(dead, &bad, "dead", "1 day")
	job(old, &bad, "dead", "9 days")
	job(ok, &bad, "failed", "1 hour")
	job(foreign, &otherBad, "dead", "1 hour") // another owner's job
	job(uuid.New(), nil, "dead", "2 days")    // connection-less jobs belong to the owner
	e.exec(`INSERT INTO job_runs (job_id, attempt, outcome, error_class, error_message) VALUES ($1, 5, 'failed', 'transient', 'sanitized')`, dead)
	e.exec(`INSERT INTO blobs (sha256, size_bytes, stored_bytes, compression, refcount) VALUES (sha256('x'::bytea), 100, 40, 'zstd', 1)`)
	write := func(name string, at time.Time) {
		if err := os.MkdirAll(filepath.Join(backups, name), 0o700); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(backup.Manifest{Format: backup.Format, CreatedAt: at})
		if err := os.WriteFile(filepath.Join(backups, name, backup.ManifestFile), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	last := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	write("vitamux-20200101T000000Z", last.Add(-48*time.Hour))
	write("vitamux-20200102T000000Z", last)

	e.get(user, "/api/v1/system/status", http.StatusOK, &st)
	if st.LastBackupAt == nil || !st.LastBackupAt.Equal(last) {
		t.Fatalf("last backup %v, want %v", st.LastBackupAt, last)
	}
	if len(st.DegradedConnections) != 1 {
		t.Fatalf("degraded connections: %+v", st.DegradedConnections)
	}
	if c := st.DegradedConnections[0]; c.ID != ingest.FormatConnectionID(bad) || c.Provider != "withings" || c.Health != oapi.HealthFailing ||
		c.HealthReason == nil || *c.HealthReason != "transient" || c.Stream != nil {
		t.Fatalf("degraded connection: %+v", c)
	}
	if len(st.FailingJobs) != 2 || st.FailingJobs[0].ID != dead || st.FailingJobs[0].Kind != "sync" || st.FailingJobs[1].ConnectionID != nil ||
		st.FailingJobs[0].ErrorClass == nil || *st.FailingJobs[0].ErrorClass != "transient" {
		t.Fatalf("failing jobs (own dead jobs of the last 7 days, newest first): %+v", st.FailingJobs)
	}
	if st.BlobSizeBytes != 40 {
		t.Fatalf("blob size %d, want 40", st.BlobSizeBytes)
	}

	// A degraded stream (schema drift) surfaces its connection even when the connection itself is ok.
	drift := conn(user, "garmin", "active", 0, "")
	e.exec(`INSERT INTO sync_cursors (connection_id, stream, status, status_reason) VALUES ($1, 'daily', 'degraded', 'schema_drift')`, drift)
	e.get(user, "/api/v1/system/status", http.StatusOK, &st)
	if len(st.DegradedConnections) != 2 {
		t.Fatalf("degraded connections with drift: %+v", st.DegradedConnections)
	}
	var found bool
	for _, c := range st.DegradedConnections {
		if c.ID == ingest.FormatConnectionID(drift) {
			found = c.Health == oapi.HealthDegraded && c.Stream != nil && *c.Stream == "daily" && c.HealthReason != nil && *c.HealthReason == "schema_drift"
		}
	}
	if !found {
		t.Fatalf("drifting stream not reported: %+v", st.DegradedConnections)
	}
}

// generateYear runs fixturegen for days from start into dir (heart rate every 60 s).
func generateYear(t *testing.T, dir, start string, days int) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	cmd := exec.CommandContext(t.Context(), "go", "run", "./tools/fixturegen", "-out", dir, "-start", start,
		"-days", strconv.Itoa(days), "-hr-step", "60")
	cmd.Dir = filepath.Join(filepath.Dir(file), "..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixturegen: %v\n%s", err, out)
	}
}

// TestCoverage loads a synthetic slice (30 days; the full year with VITAMUX_COVERAGE_YEAR=1),
// rebuilds the hourly aggregates and checks the matrix and its speed: the done-when is under
// 1 s for the year, so the slice must stay far below that.
func TestCoverage(t *testing.T) {
	days, limit := 30, 500*time.Millisecond
	if os.Getenv("VITAMUX_COVERAGE_YEAR") != "" {
		days, limit = 365, time.Second
	}
	const start = "2025-03-01"
	dir := t.TempDir()
	generateYear(t, dir, start, days)
	u, app := dbtest.Migrated(t)
	stats, err := fixtureload.Load(t.Context(), app, dir)
	if err != nil {
		t.Fatal(err)
	}
	d := db.New(app)
	for _, p := range []struct{ from, tz string }{
		{"2024-01-01T00:00:00Z", "Europe/Berlin"}, {"2025-05-11T22:00:00Z", "America/New_York"}, {"2025-05-22T04:00:00Z", "Europe/Berlin"},
	} {
		if _, err := app.Exec(t.Context(), `INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES ($1, $2, $3, $4)`,
			uuid.New(), stats.UserID, p.tz, p.from); err != nil {
			t.Fatal(err)
		}
	}
	// The loader writes rows without marking their days; mark them all, then run the rebuild job's step.
	if _, err := dbtest.Pool(t, u, db.OwnerRole).Exec(t.Context(), `INSERT INTO resolution_dirty (user_id, metric_id, local_date)
		SELECT DISTINCT user_id, metric_id, local_date FROM measurements ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	for {
		n, err := resolve.RebuildAggregates(t.Context(), d, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), Options{DB: d})
	if err != nil {
		t.Fatal(err)
	}
	e := &statusEnv{t: t, h: rt.mux, d: d}

	end := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, days-1).Format(time.DateOnly)
	query := "/api/v1/coverage?start_date=" + start + "&end_date=" + end
	var cov oapi.Coverage
	t0 := time.Now()
	e.get(stats.UserID, query, http.StatusOK, &cov)
	took := time.Since(t0)
	t.Logf("coverage of %d days, all metrics: %d rows in %v", days, len(cov.Rows), took)
	if took > limit {
		t.Fatalf("coverage took %v, limit %v", took, limit)
	}
	if len(cov.Rows) == 0 {
		t.Fatal("no coverage rows")
	}
	for _, r := range cov.Rows {
		if len(r.Days) != days {
			t.Fatalf("%s/%s: %d days, want %d", r.Metric, r.Source, len(r.Days), days)
		}
		for _, c := range r.Days {
			if c < 0 || c > 1 {
				t.Fatalf("%s/%s: coverage %v out of range", r.Metric, r.Source, c)
			}
		}
	}

	// A metric filter narrows the rows; heart rate is sampled around the clock.
	var hr oapi.Coverage
	e.get(stats.UserID, query+"&metric=heart_rate", http.StatusOK, &hr)
	if len(hr.Rows) == 0 || len(hr.Rows) >= len(cov.Rows) {
		t.Fatalf("heart_rate rows %d of %d", len(hr.Rows), len(cov.Rows))
	}
	var full int
	for _, r := range hr.Rows {
		if r.Metric != "heart_rate" {
			t.Fatalf("filtered row for %s", r.Metric)
		}
		for _, c := range r.Days {
			if c >= 0.99 {
				full++
			}
		}
	}
	if full == 0 {
		t.Fatalf("no heart_rate day with full coverage: %+v", hr.Rows)
	}

	// An origin filter keeps only the hours that app contributed; an unknown origin has none.
	var nobody oapi.Coverage
	e.get(stats.UserID, query+"&origin=com.example.nobody", http.StatusOK, &nobody)
	if len(nobody.Rows) != 0 {
		t.Fatalf("unknown origin: %d rows", len(nobody.Rows))
	}
	var origin string
	if err := app.QueryRow(t.Context(), `SELECT o.origin_key FROM data_origins o
		WHERE EXISTS (SELECT 1 FROM source_hourly_aggregates a WHERE a.origin_id = o.id) LIMIT 1`).Scan(&origin); err == nil {
		var byOrigin oapi.Coverage
		e.get(stats.UserID, query+"&origin="+url.QueryEscape(origin), http.StatusOK, &byOrigin)
		if len(byOrigin.Rows) == 0 || len(byOrigin.Rows) > len(cov.Rows) {
			t.Fatalf("origin %q: %d rows of %d", origin, len(byOrigin.Rows), len(cov.Rows))
		}
	}

	// Another owner sees nothing; bad input is a 422.
	var none oapi.Coverage
	e.get(uuid.New(), query, http.StatusOK, &none)
	if len(none.Rows) != 0 {
		t.Fatalf("another owner's coverage: %d rows", len(none.Rows))
	}
	e.get(stats.UserID, query+"&metric=no_such_metric", http.StatusUnprocessableEntity, nil)
	e.get(stats.UserID, "/api/v1/coverage?start_date=2025-03-02&end_date=2025-03-01", http.StatusUnprocessableEntity, nil)
	e.get(stats.UserID, "/api/v1/coverage?start_date=2024-01-01&end_date=2025-03-01", http.StatusUnprocessableEntity, nil)
}
