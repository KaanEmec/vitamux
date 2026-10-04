//go:build integration

package audit_test

import (
	"strings"
	"testing"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
)

const sentinel = "SENTINEL-not-a-real-secret"

func TestRecordMasksSecrets(t *testing.T) {
	_, pool := dbtest.Migrated(t)
	d := db.New(pool)
	ctx := t.Context()

	err := d.Tx(ctx, func(q *dbq.Queries) error {
		return audit.Record(ctx, q, audit.Event{
			Actor:  audit.Owner,
			Action: "owner.password_reset",
			Detail: map[string]any{
				"password": sentinel,
				"nested":   map[string]any{"refresh_token": sentinel, "scope": "read:health"},
				"diff":     audit.Diff(map[string]any{"api_key": sentinel, "name": "a"}, map[string]any{"api_key": "x", "name": "b"}),
			},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	var detail string
	if err := pool.QueryRow(ctx, "SELECT detail::text FROM audit_events").Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(detail, sentinel) || !strings.Contains(detail, "read:health") || !strings.Contains(detail, `"to": "b"`) {
		t.Fatalf("detail = %s", detail)
	}
}
