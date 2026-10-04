//go:build integration

package main

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
)

func loadKeyring(t *testing.T, path string, previous ...string) *crypto.Keyring {
	t.Helper()
	kr, err := crypto.Load(path, previous...)
	if err != nil {
		t.Fatal(err)
	}
	return kr
}

func TestRotateKeys(t *testing.T) {
	_, pool := dbtest.Migrated(t)
	d := db.New(pool)
	ctx := t.Context()
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	countKeyID := func(table, col, id string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE "+col+" = $1", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	dir := t.TempDir()
	keyA, keyB := filepath.Join(dir, "a.key"), filepath.Join(dir, "b.key")
	for _, p := range []string{keyA, keyB} {
		if _, err := crypto.WriteKeyFile(p); err != nil {
			t.Fatal(err)
		}
	}
	krA, krB, krBOnly := loadKeyring(t, keyA), loadKeyring(t, keyB, keyA), loadKeyring(t, keyB)

	userID := uuid.New()
	mustExec("INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'x')", userID)
	totp := []byte("synthetic totp secret")
	sealedTOTP, err := krA.Seal(crypto.Credentials, totp, crypto.TOTPAAD(userID))
	if err != nil {
		t.Fatal(err)
	}
	mustExec("UPDATE users SET totp_ciphertext = $2, totp_key_id = $3 WHERE id = $1", userID, sealedTOTP, krA.KeyID())

	conns := map[uuid.UUID][]byte{}
	for i := range 3 {
		id := uuid.New()
		conns[id] = []byte{'t', 'o', 'k', 'e', 'n', byte('0' + i)}
		mustExec(`INSERT INTO connections (id, user_id, provider_id, mode, status)
			SELECT $1, $2, id, 'in_process', 'active' FROM providers WHERE code = 'withings'`, id, userID)
		sealed, err := krA.Seal(crypto.Credentials, conns[id], crypto.CredentialsAAD(id))
		if err != nil {
			t.Fatal(err)
		}
		mustExec("INSERT INTO credentials (connection_id, ciphertext, key_id) VALUES ($1, $2, $3)", id, sealed, krA.KeyID())
	}

	// Source setup secrets (ADR-0021): a provider app and a panel sidecar.
	appSecret, sidecarSecret := []byte("synthetic-app-secret"), []byte("synthetic-sidecar-secret")
	sealedApp, err := krA.Seal(crypto.Credentials, appSecret, crypto.ProviderAppAAD("withings"))
	if err != nil {
		t.Fatal(err)
	}
	mustExec("INSERT INTO provider_app_credentials (provider, client_id, ciphertext, key_id) VALUES ('withings', 'synthetic-client', $1, $2)", sealedApp, krA.KeyID())
	sealedSidecar, err := krA.Seal(crypto.Credentials, sidecarSecret, crypto.SidecarAAD("my_collector"))
	if err != nil {
		t.Fatal(err)
	}
	mustExec("INSERT INTO sidecars (name, url, ciphertext, key_id) VALUES ('my_collector', 'http://my-collector:8080', $1, $2)", sealedSidecar, krA.KeyID())

	// Interrupted after one batch of two: one credential and the TOTP secret remain.
	n, err := rotateKeys(ctx, d, krB, 2, 1)
	if err != nil || n != (rotated{Credentials: 2}) {
		t.Fatalf("partial run: %+v, %v", n, err)
	}
	if got := countKeyID("credentials", "key_id", krB.KeyID()); got != 2 {
		t.Fatalf("credentials under B after partial run = %d", got)
	}

	n, err = rotateKeys(ctx, d, krB, 2, 0)
	if err != nil || n != (rotated{Credentials: 1, TOTP: 1, ProviderApps: 1, Sidecars: 1}) {
		t.Fatalf("resumed run: %+v, %v", n, err)
	}
	n, err = rotateKeys(ctx, d, krB, 2, 0)
	if err != nil || n != (rotated{}) {
		t.Fatalf("re-run must be a no-op: %+v, %v", n, err)
	}

	if got := countKeyID("credentials", "key_id", krB.KeyID()); got != 3 {
		t.Fatalf("credentials under B = %d", got)
	}
	if got := countKeyID("users", "totp_key_id", krB.KeyID()); got != 1 {
		t.Fatalf("users under B = %d", got)
	}
	for id, want := range conns {
		var ct []byte
		var version int
		if err := pool.QueryRow(ctx, "SELECT ciphertext, version FROM credentials WHERE connection_id = $1", id).Scan(&ct, &version); err != nil {
			t.Fatal(err)
		}
		got, err := krBOnly.Open(crypto.Credentials, ct, crypto.CredentialsAAD(id))
		if err != nil || string(got) != string(want) || version != 1 {
			t.Fatalf("credentials %s: %q, version %d, %v", id, got, version, err)
		}
	}
	var ct []byte
	if err := pool.QueryRow(ctx, "SELECT totp_ciphertext FROM users WHERE id = $1", userID).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if got, err := krBOnly.Open(crypto.Credentials, ct, crypto.TOTPAAD(userID)); err != nil || string(got) != string(totp) {
		t.Fatalf("totp: %q, %v", got, err)
	}

	for _, c := range []struct {
		sql  string
		aad  []byte
		want []byte
	}{
		{"SELECT ciphertext FROM provider_app_credentials WHERE provider = 'withings'", crypto.ProviderAppAAD("withings"), appSecret},
		{"SELECT ciphertext FROM sidecars WHERE name = 'my_collector'", crypto.SidecarAAD("my_collector"), sidecarSecret},
	} {
		if err := pool.QueryRow(ctx, c.sql).Scan(&ct); err != nil {
			t.Fatal(err)
		}
		if got, err := krBOnly.Open(crypto.Credentials, ct, c.aad); err != nil || string(got) != string(c.want) {
			t.Fatalf("%s: %v", c.sql, err)
		}
	}

	// One audit event per run that changed something; counts only.
	var events int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE actor = 'system' AND action = 'keys.rotate'").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 2 {
		t.Fatalf("audit events = %d, want 2", events)
	}
}
