//go:build integration

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
)

func TestOwnerCommands(t *testing.T) {
	url, pool := dbtest.Migrated(t)
	t.Setenv("VITAMUX_ENV", "development")
	t.Setenv("VITAMUX_DATABASE_URL", url)
	t.Setenv("VITAMUX_DATABASE_URL_FILE", "")
	ctx := context.Background()
	const first, second = "SENTINEL-first-password", "SENTINEL-second-password"

	runOwner := func(cmd, input string) (int, string) {
		var out, errOut bytes.Buffer
		code := owner(cmd, pipeStdin(t, input), &out, &errOut)
		return code, out.String() + errOut.String()
	}
	if code, out := runOwner("create-owner", "owner\n"+first+"\n"); code != 0 || strings.Contains(out, first) {
		t.Fatalf("create-owner: %d %q", code, out)
	}
	if code, out := runOwner("create-owner", "second\n"+first+"\n"); code != 1 || !strings.Contains(out, "already exists") {
		t.Fatalf("second owner: %d %q", code, out)
	}
	if code, out := runOwner("reset-password", "owner\nshort\n"); code != 1 || !strings.Contains(out, "password must be") {
		t.Fatalf("weak password: %d %q", code, out)
	}
	if code, out := runOwner("reset-password", "nobody\n"+second+"\n"); code != 1 || !strings.Contains(out, "no account") {
		t.Fatalf("unknown user: %d %q", code, out)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO sessions (id, user_id, token_hash, expires_at)
		SELECT gen_random_uuid(), id, '\x01', now() + interval '1 day' FROM users`); err != nil {
		t.Fatal(err)
	}
	if code, out := runOwner("reset-password", "owner\n"+second+"\n"); code != 0 || strings.Contains(out, second) {
		t.Fatalf("reset-password: %d %q", code, out)
	}
	var hash, audits string
	var sessions int
	err := pool.QueryRow(ctx, `SELECT password_hash, (SELECT count(*) FROM sessions),
		(SELECT string_agg(action || detail::text, ' ' ORDER BY id) FROM audit_events) FROM users`).Scan(&hash, &sessions, &audits)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := auth.VerifyPassword(hash, second); !ok || sessions != 0 {
		t.Fatalf("after reset: password ok=%v, %d sessions left", ok, sessions)
	}
	if !strings.Contains(audits, "owner.create") || !strings.Contains(audits, "owner.password_reset") || strings.Contains(audits, "SENTINEL") {
		t.Fatalf("audit: %s", audits)
	}
}
