//go:build integration

package metrics

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
)

func TestStateGauges(t *testing.T) {
	_, p := dbtest.Migrated(t)
	ctx := t.Context()
	owner, active, reauth := uuid.New(), uuid.New(), uuid.New()
	for _, sql := range []struct {
		q    string
		args []any
	}{
		{"INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')", []any{owner}},
		{`INSERT INTO connections (id, user_id, provider_id, mode, status, last_success_at)
			VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'withings'), 'in_process', 'active', '2026-10-03T12:00:00Z')`, []any{active, owner}},
		{`INSERT INTO connections (id, user_id, provider_id, mode, status)
			VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'withings'), 'in_process', 'needs_reauth')`, []any{reauth, owner}},
		{`INSERT INTO jobs (id, kind, run_at) VALUES (gen_random_uuid(), 'sweep_blobs', now() - interval '90 seconds')`, nil},
		{`INSERT INTO jobs (id, kind, status, finished_at) VALUES (gen_random_uuid(), 'sweep_blobs', 'dead', now())`, nil},
		{`INSERT INTO jobs (id, kind, status, finished_at) VALUES (gen_random_uuid(), 'sweep_blobs', 'succeeded', now())`, nil},
	} {
		if _, err := p.Exec(ctx, sql.q, sql.args...); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	Handler(db.New(p)).ServeHTTP(rec, httptest.NewRequestWithContext(ctx, "GET", "/metrics", nil))
	b, _ := io.ReadAll(rec.Body)
	body := string(b)
	for _, want := range []string{
		`vitamux_jobs{kind="sweep_blobs",status="queued"} 1`,
		`vitamux_jobs{kind="sweep_blobs",status="running"} 0`,
		`vitamux_jobs{kind="sweep_blobs",status="dead"} 1`,
		`vitamux_connections{provider="withings",status="active"} 1`,
		`vitamux_connections{provider="withings",status="needs_reauth"} 1`,
		`vitamux_connections{provider="withings",status="paused"} 0`,
		`vitamux_connection_last_success_timestamp_seconds{connection_id="` + active.String() + `",provider="withings"} 1.7910288e+09`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition lacks %q", want)
		}
	}
	if !strings.Contains(body, `vitamux_job_queue_age_seconds{kind="sweep_blobs"} 9`) { // ~90 s old
		t.Errorf("queue age missing or wrong:\n%s", grep(body, "queue_age"))
	}
	if strings.Contains(body, reauth.String()) {
		t.Error("a connection that never succeeded must have no last-success series")
	}
}

func grep(s, sub string) string {
	var out []string
	for l := range strings.SplitSeq(s, "\n") {
		if strings.Contains(l, sub) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
