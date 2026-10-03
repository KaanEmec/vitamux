//go:build integration

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
)

func TestPurgeUserCommand(t *testing.T) {
	_, pool := dbtest.Migrated(t)
	ctx := context.Background()
	d := db.New(pool)
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sessions (id, user_id, token_hash, expires_at) SELECT gen_random_uuid(), id, '\x01', now() + interval '1 day' FROM users`); err != nil {
		t.Fatal(err)
	}
	users := func() (n int) {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	run := func(username string, yes bool) (int, string) {
		var out bytes.Buffer
		code := purgeUser(ctx, d, nil, username, yes, &out, &out)
		return code, out.String()
	}

	if code, out := run("", false); code != 1 || !strings.Contains(out, "would delete") || !strings.Contains(out, "sessions") || !strings.Contains(out, "--yes") || users() != 1 {
		t.Fatalf("without --yes: %d %q", code, out)
	}
	if code, out := run("nobody", true); code != 1 || !strings.Contains(out, "no account") {
		t.Fatalf("unknown account: %d %q", code, out)
	}
	if code, out := run("", true); code != 0 || !strings.Contains(out, "deleted") || users() != 0 {
		t.Fatalf("with --yes: %d %q", code, out)
	}
	if code, out := run("", true); code != 1 || !strings.Contains(out, "0 accounts") {
		t.Fatalf("no account left: %d %q", code, out)
	}
}
