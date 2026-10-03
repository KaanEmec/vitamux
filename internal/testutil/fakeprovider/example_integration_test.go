//go:build integration

package fakeprovider_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	fp "github.com/KaanEmec/vitamux/internal/testutil/fakeprovider"
)

// Example integration test: a scripted provider, a fresh migrated database, assertions on rows.
// Real tests call the code under test where this one calls the client and SQL directly.
func TestExampleRateLimitIsPersisted(t *testing.T) {
	ctx := context.Background()
	dbURL, pool := dbtest.Migrated(t) // fresh database per test, app-role pool

	provider := fp.New(t)
	provider.Expect(fp.GET("/v1/measures", "synthetic-access-1", fp.RateLimited(2*time.Minute)))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.URL+"/v1/measures", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer synthetic-access-1")
	res, err := provider.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	secs, err := strconv.Atoi(res.Header.Get("Retry-After"))
	if res.StatusCode != http.StatusTooManyRequests || err != nil {
		t.Fatalf("status %d, Retry-After %q", res.StatusCode, res.Header.Get("Retry-After"))
	}

	// The shared Retry-After state survives restarts (docs/architecture/connectors.md#runtime-responsibilities).
	until := time.Now().Add(time.Duration(secs) * time.Second)
	_, err = pool.Exec(ctx, `INSERT INTO provider_rate_state (provider_id, blocked_until)
		SELECT id, $1 FROM providers WHERE code = 'withings'`, until)
	if err != nil {
		t.Fatal(err)
	}
	var blocked bool
	if err := pool.QueryRow(ctx, `SELECT blocked_until > now() + interval '1 minute' FROM provider_rate_state`).Scan(&blocked); err != nil || !blocked {
		t.Fatalf("blocked_until not stored: %v %v", blocked, err)
	}

	// Truncate resets data between scenarios in the same test; seeded providers stay.
	dbtest.Truncate(t, dbURL)
	var rate, providers int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM provider_rate_state), (SELECT count(*) FROM providers)`).Scan(&rate, &providers); err != nil {
		t.Fatal(err)
	}
	if rate != 0 || providers == 0 {
		t.Fatalf("after Truncate: %d rate rows, %d providers", rate, providers)
	}
}
