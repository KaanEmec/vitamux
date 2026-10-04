//go:build integration

package export_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/export"
)

// TestImportMatchesProvidersByCode: sidecars register their providers at runtime, so ids differ
// between instances. The target here registers sidecar_b only (so its id differs and sidecar_a
// is missing); the imported connection, device, origin and workout still point at the right codes.
func TestImportMatchesProvidersByCode(t *testing.T) {
	src := loaded(t, "2025-03-01", 1)
	src.exec(`SELECT register_provider('sidecar_a', 'Sidecar A')`)
	src.exec(`SELECT register_provider('sidecar_b', 'Sidecar B')`)
	conn := uuid.Must(uuid.NewV7())
	src.exec(`INSERT INTO connections (id, user_id, provider_id, mode, status, upstream)
		SELECT $1, $2, id, 'remote', 'active', '{"package": "synthetic-collector", "version": "1.0"}' FROM providers WHERE code = 'sidecar_b'`, conn, src.user)
	src.exec(`INSERT INTO devices (id, user_id, provider_id, fingerprint, device_type)
		SELECT $1, $2, id, 'synthetic-device', 'watch' FROM providers WHERE code = 'sidecar_b'`, uuid.Must(uuid.NewV7()), src.user)
	src.exec(`INSERT INTO data_origins (id, user_id, provider_id, origin_key, relayed_provider_id)
		SELECT $1, $2, b.id, 'synthetic.origin', a.id FROM providers a, providers b WHERE a.code = 'sidecar_a' AND b.code = 'sidecar_b'`, uuid.Must(uuid.NewV7()), src.user)
	src.exec(`INSERT INTO workouts (id, user_id, start_at, end_at, tz_offset_min, local_date, sport, provider_id, connection_id, external_id, dedupe_key,
		raw_payload_id, normalizer_version_id)
		SELECT $1, $2, '2025-03-02T07:00:00Z', '2025-03-02T07:45:00Z', 0, '2025-03-02', 'running', c.provider_id, c.id, 'run-1', substr(sha256('run-1'), 1, 16),
		(SELECT min(id) FROM raw_payloads), (SELECT min(id) FROM normalizer_versions) FROM connections c WHERE c.id = $3`, uuid.Must(uuid.NewV7()), src.user, conn)
	path, _ := writeExport(t, src, nil, export.Options{Format: export.FormatNDJSON})

	dst := newInstance(t)
	dst.createOwner()
	dst.exec(`SELECT register_provider('sidecar_b', 'Sidecar B')`)
	idOf := func(in *instance) int64 { return in.int(`SELECT id FROM providers WHERE code = 'sidecar_b'`) }
	if idOf(src) == idOf(dst) {
		t.Fatalf("test setup: sidecar_b has the same id %d in both instances", idOf(src))
	}
	if _, err := importZip(t, dst, path, export.ImportOptions{}); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		what, sql string
	}{
		{"providers", `SELECT count(*) FROM providers WHERE code IN ('sidecar_a', 'sidecar_b')`},
		{"connection", `SELECT count(*) FROM connections c JOIN providers p ON p.id = c.provider_id
			WHERE p.code = 'sidecar_b' AND c.mode = 'remote' AND c.upstream->>'package' = 'synthetic-collector'`},
		{"device", `SELECT count(*) FROM devices d JOIN providers p ON p.id = d.provider_id WHERE p.code = 'sidecar_b' AND d.fingerprint = 'synthetic-device'`},
		{"origin", `SELECT count(*) FROM data_origins o JOIN providers p ON p.id = o.provider_id JOIN providers r ON r.id = o.relayed_provider_id
			WHERE p.code = 'sidecar_b' AND r.code = 'sidecar_a' AND o.origin_key = 'synthetic.origin'`},
		{"workout", `SELECT count(*) FROM workouts w JOIN providers p ON p.id = w.provider_id WHERE p.code = 'sidecar_b' AND w.external_id = 'run-1'`},
	} {
		want := int64(1)
		if c.what == "providers" {
			want = 2
		}
		if got := dst.int(c.sql); got != want {
			t.Errorf("%s: %d rows with the right provider code, want %d", c.what, got, want)
		}
	}
	// The built-in providers keep their rows as well.
	q := `SELECT md5(string_agg(p.code || c.mode, ',' ORDER BY p.code, c.mode)) FROM connections c JOIN providers p ON p.id = c.provider_id`
	if src.str(q) != dst.str(q) {
		t.Error("connections differ by provider code between source and target")
	}
}
