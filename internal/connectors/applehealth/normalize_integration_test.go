//go:build integration

package applehealth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

type env struct {
	t     *testing.T
	d     *db.DB
	blobs *blob.Store
	run   func(sql string, args ...any) error
	scan  func(dest any, sql string, args ...any) error
	conn  uuid.UUID
	nv    int32
}

func newEnv(t *testing.T) *env {
	u, app := dbtest.Migrated(t)
	owner := dbtest.Pool(t, u, db.OwnerRole)
	keyPath := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.WriteKeyFile(keyPath); err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.Load(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.Open(t.TempDir(), kr)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, d: db.New(app), blobs: blobs, conn: uuid.New(),
		run: func(sql string, args ...any) error { _, err := owner.Exec(t.Context(), sql, args...); return err },
		scan: func(dest any, sql string, args ...any) error {
			return owner.QueryRow(t.Context(), sql, args...).Scan(dest)
		}}
	user := uuid.New()
	e.exec(`INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')`, user)
	e.exec(`INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES (gen_random_uuid(), $1, 'Europe/Amsterdam', '2020-01-01Z')`, user)
	e.exec(`INSERT INTO connections (id, user_id, provider_id, mode, status)
		VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'apple_health'), 'push', 'active')`, e.conn, user)
	e.exec(`INSERT INTO blobs (sha256, size_bytes, stored_bytes, compression) VALUES (sha256('synthetic'), 1, 1, 'none')`)
	e.exec(`INSERT INTO ingest_batches (id, user_id, connection_id, source_kind) VALUES ($1, $2, $3, 'push')`, uuid.New(), user, e.conn)
	if err := e.scan(&e.nv, `INSERT INTO normalizer_versions (name, version, git_sha) VALUES ($1, 1, 'dev') RETURNING id`,
		NormalizerID); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if err := e.run(sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *env) int(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.scan(&n, sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// raw stores body as a raw payload of the connection and returns its id.
func (e *env) raw(body []byte) int64 {
	e.t.Helper()
	var id int64
	if err := e.scan(&id, `INSERT INTO raw_payloads (user_id, connection_id, batch_id, stream, external_key, content_sha256, content_type, fetched_at)
		SELECT c.user_id, c.id, (SELECT id FROM ingest_batches LIMIT 1), $2, $3, sha256('synthetic'), 'application/json', now()
		FROM connections c WHERE c.id = $1 RETURNING id`, e.conn, StreamSamples, uuid.NewString()); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// normalize runs the normalizer over body and writes it as raw payload rawID.
func (e *env) write(rawID int64, body []byte) normalize.WriteStats {
	e.t.Helper()
	out, err := Normalizer{}.Normalize(e.t.Context(), normalize.RawPayload{ID: rawID, Stream: StreamSamples, Body: body}, normalize.Env{Provider: Provider})
	if err != nil {
		e.t.Fatal(err)
	}
	var st normalize.WriteStats
	src := normalize.Source{ConnectionID: e.conn, RawPayloadID: rawID, NormalizerVersionID: e.nv, Blobs: e.blobs}
	if err := e.d.Tx(e.t.Context(), func(q *dbq.Queries) (err error) {
		st, err = normalize.Write(e.t.Context(), q, src, out)
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
	return st
}

func golden(t *testing.T, name string) []byte {
	b, err := os.ReadFile(filepath.Join("testdata", NormalizerID, name+".raw.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestReplayIsNoOp: every golden case writes, and replaying the same page changes nothing, blob
// documents included (one reference per event row).
func TestReplayIsNoOp(t *testing.T) {
	e := newEnv(t)
	cases, _ := filepath.Glob(filepath.Join("testdata", NormalizerID, "*.raw.json"))
	for _, path := range cases {
		name := strings.TrimSuffix(filepath.Base(path), ".raw.json")
		body := golden(t, name)
		id := e.raw(body)
		first := e.write(id, body)
		again := e.write(id, body)
		if again.Inserted != 0 || again.Superseded != 0 || again.Deleted != 0 || again.Reversioned != 0 ||
			again.Unchanged != first.Inserted+first.Unchanged {
			t.Errorf("%s: replay changed rows: first %+v, again %+v", name, first, again)
		}
	}
	// v1 cases plus the Watch cases: 8 summary values, 6 beats (later withdrawn), 1 effort score;
	// ECG, route, State of Mind, symptom, cycle and mindful events; the multisport workout.
	for table, want := range map[string]int{"measurements": 34, "measurement_groups": 3, "sleep_sessions": 3,
		"sleep_stages": 8, "workouts": 3, "health_events": 8, "workout_segments": 4} {
		if n := e.int(`SELECT count(*) FROM ` + table); n != want {
			t.Errorf("%s: %d rows, want %d", table, n, want)
		}
	}
}

// TestDeletionTombstones: deleted UUIDs tombstone the measurement, the group with its
// components, the sleep session keyed by its first sample, the workout and the event.
func TestDeletionTombstones(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"heart_rate_native", "bp_correlation", "sleep", "workout", "hr_alert_event"} {
		body := golden(t, name)
		e.write(e.raw(body), body)
	}
	del := func(typ, id string) int {
		body := []byte(`{"type": "` + typ + `", "anchor": {}, "samples": [], "deleted": [{"uuid": "` + id + `"}]}`)
		return e.write(e.raw(body), body).Deleted
	}
	for _, c := range []struct {
		typ, id string
		want    int
	}{
		{"HKQuantityTypeIdentifierHeartRate", "6f0d0000-0000-4000-8000-000000000001", 1},
		{"HKCorrelationTypeIdentifierBloodPressure", "6F0D0000-0000-4000-8000-000000000015", 3}, // group and two components
		{"HKCategoryTypeIdentifierSleepAnalysis", "6F0D0000-0000-4000-8000-00000000000C", 1},
		{"HKCategoryTypeIdentifierSleepAnalysis", "6F0D0000-0000-4000-8000-00000000000D", 0}, // not a session's first sample
		{"HKWorkoutTypeIdentifier", "6F0D0000-0000-4000-8000-000000000017", 1},
		{"HKCategoryTypeIdentifierHighHeartRateEvent", "6F0D0000-0000-4000-8000-000000000009", 1},
		{"HKQuantityTypeIdentifierStepCount", "6F0D0000-0000-4000-8000-000000000002", 0}, // the HR sample's UUID under another type
	} {
		if got := del(c.typ, c.id); got != c.want {
			t.Errorf("delete %s %s: %d rows, want %d", c.typ, c.id, got, c.want)
		}
	}
	if n := e.int(`SELECT count(*) FROM measurements WHERE deleted_at IS NOT NULL AND superseded_at IS NULL`); n != 3 {
		t.Errorf("%d deleted measurements, want 3", n)
	}
	// A deletion replayed is a no-op.
	if got := del("HKQuantityTypeIdentifierHeartRate", "6F0D0000-0000-4000-8000-000000000001"); got != 0 {
		t.Errorf("replayed deletion deleted %d rows", got)
	}
}

// TestOriginsAndRelays: Garmin Connect is relayed (known_relay_origins) and its rows carry the
// relayed flag; Apple's own origins are native; user-entered rows carry manual_entry.
func TestOriginsAndRelays(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"steps_relayed", "weight_user_entered", "hr_alert_event"} {
		body := golden(t, name)
		e.write(e.raw(body), body)
	}
	if n := e.int(`SELECT count(*) FROM data_origins o JOIN providers p ON p.id = o.relayed_provider_id
		WHERE o.origin_key = 'com.garmin.connect.mobile' AND p.code = 'garmin' AND NOT o.is_native`); n != 1 {
		t.Error("Garmin Connect origin is not relayed to garmin")
	}
	if n := e.int(`SELECT count(*) FROM data_origins WHERE is_native AND relayed_provider_id IS NULL AND origin_key LIKE 'com.apple.health.%'`); n != 2 {
		t.Errorf("%d native Apple origins, want 2", n)
	}
	if n := e.int(`SELECT count(*) FROM measurements m JOIN data_origins o ON o.id = m.origin_id
		WHERE (o.origin_key = 'com.garmin.connect.mobile') <> (m.quality_flags & 8 = 8)`); n != 0 ||
		e.int(`SELECT count(*) FROM measurements WHERE quality_flags & 8 = 8`) != 1 {
		t.Error("relayed flag must be set exactly on the Garmin Connect steps")
	}
	if n := e.int(`SELECT count(*) FROM devices WHERE device_type = 'watch' AND manufacturer = 'Garmin'`); n != 1 {
		t.Error("Garmin watch device")
	}
	if n := e.int(`SELECT count(*) FROM measurements WHERE quality_flags & 1 = 1 AND group_id IS NOT NULL`); n != 1 {
		t.Error("the user-entered weight must carry manual_entry")
	}
	if n := e.int(`SELECT count(*) FROM health_events WHERE code = 'high_heart_rate_alert' AND quality_flags = 0
		AND context ->> 'HKMetadataKeyHeartRateEventThreshold' = '120 count/min' AND local_date = '2026-09-14'`); n != 1 {
		t.Error("health event row")
	}
}
