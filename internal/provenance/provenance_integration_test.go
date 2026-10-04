//go:build integration

package provenance_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/normalize"
	"github.com/KaanEmec/vitamux/internal/provenance"
)

// env is a synthetic owner with a push connection and client; fixtures go in as the owner role.
type env struct {
	t      *testing.T
	d      *db.DB
	exec   func(sql string, args ...any)
	scan   func(dest any, sql string, args ...any)
	user   uuid.UUID
	conn   uuid.UUID
	client uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	u, app := dbtest.Migrated(t)
	owner := dbtest.Pool(t, u, db.OwnerRole)
	e := &env{t: t, d: db.New(app), user: uuid.New(), conn: uuid.New(), client: uuid.New()}
	e.exec = func(sql string, args ...any) {
		t.Helper()
		if _, err := owner.Exec(context.Background(), sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	e.scan = func(dest any, sql string, args ...any) {
		t.Helper()
		if err := owner.QueryRow(context.Background(), sql, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	e.exec(`INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')`, e.user)
	e.exec(`INSERT INTO connections (id, user_id, provider_id, account_key, mode, status)
		VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'withings'), sha256('acct'), 'push', 'active')`, e.conn, e.user)
	e.exec(`INSERT INTO clients (id, user_id, connection_id, kind, name, token_hash) VALUES ($1, $2, $3, 'collector', 'synthetic-collector', 'x')`,
		e.client, e.user, e.conn)
	e.exec(`INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES (gen_random_uuid(), $1, 'Europe/Amsterdam', '2020-01-01Z')`, e.user)
	return e
}

// write stores a new raw payload (own batch, pushed by the client) and normalizes out from it with
// the given normalizer version; it returns the raw id.
func (e *env) write(version int, out normalize.Output) int64 {
	e.t.Helper()
	ctx := context.Background()
	body := uuid.New()
	e.exec(`INSERT INTO blobs (sha256, size_bytes, stored_bytes, compression) VALUES (sha256($1::bytea), 42, 42, 'none')`, body[:])
	batch := uuid.New()
	e.exec(`INSERT INTO ingest_batches (id, user_id, connection_id, client_id, source_kind, idempotency_key)
		VALUES ($1, $2, $3, $4, 'push', $5)`, batch, e.user, e.conn, e.client, batch.String())
	var raw int64
	e.scan(&raw, `INSERT INTO raw_payloads (user_id, connection_id, batch_id, stream, external_key, content_sha256, content_type, fetched_at, request_meta, shape_fingerprint)
		VALUES ($1, $2, $3, 'sample.readings.v1', $4, sha256($5::bytea), 'application/json', now(), '{"endpoint": "/measure"}', 'fp1') RETURNING id`,
		e.user, e.conn, batch, batch.String(), body[:])
	err := e.d.Tx(ctx, func(q *dbq.Queries) error {
		nv, err := q.RegisterNormalizerVersion(ctx, dbq.RegisterNormalizerVersionParams{Name: "sample.readings", Version: int32(version), GitSha: "abc123"})
		if err != nil {
			return err
		}
		_, err = normalize.Write(ctx, q, normalize.Source{ConnectionID: e.conn, RawPayloadID: raw, NormalizerVersionID: nv}, out)
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return raw
}

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// fixture has one record of each canonical table type; hr, ctx, stage and pace are the correctable parts.
func fixture(hr float64, arm, stage string, pace int) normalize.Output {
	t0 := ts("2026-06-15T07:00:00Z")
	end := ts("2026-06-15T17:00:00Z")
	return normalize.Output{
		Devices: []normalize.Device{{Fingerprint: "dev-1", Type: "bp_monitor", Model: "Synthetic 1"}},
		Origins: []normalize.Origin{{Key: "com.example.app", Name: "Example"}},
		Measurements: []normalize.Measurement{{Metric: "heart_rate", Kind: catalog.Sample, Start: t0, Value: hr, Unit: "bpm",
			Device: "dev-1", Origin: "com.example.app", Key: normalize.Key{RecordType: "reading", ExternalID: "r1"}}},
		Groups: []normalize.Group{{Kind: "bp_reading", MeasuredAt: t0, Device: "dev-1", Context: json.RawMessage(`{"arm": "` + arm + `"}`),
			Key: normalize.Key{RecordType: "bp", ExternalID: "b1"}, Components: []normalize.Measurement{
				{Metric: "bp_systolic", Kind: catalog.Sample, Start: t0, Value: 121, Unit: "mmHg"},
				{Metric: "bp_diastolic", Kind: catalog.Sample, Start: t0, Value: 79, Unit: "mmHg"},
			}}},
		Sleep: []normalize.SleepSession{{Start: ts("2026-06-14T21:00:00Z"), End: ts("2026-06-15T05:00:00Z"),
			Key: normalize.Key{RecordType: "sleep", ExternalID: "s1"}, Stages: []normalize.SleepStage{
				{Stage: "light", Start: ts("2026-06-14T21:00:00Z"), End: ts("2026-06-15T01:00:00Z")},
				{Stage: stage, Start: ts("2026-06-15T01:00:00Z"), End: ts("2026-06-15T05:00:00Z")},
			}}},
		Workouts: []normalize.Workout{{Start: ts("2026-06-15T16:00:00Z"), End: end, Sport: "running",
			Key: normalize.Key{RecordType: "workout", ExternalID: "w1"}, Segments: []normalize.Segment{
				{Kind: "lap", Start: ts("2026-06-15T16:00:00Z"), Data: json.RawMessage(`{"pace_s_per_km": ` + string(rune('0'+pace)) + `}`)},
			}}},
	}
}

// active returns the id of the one non-superseded row of table with the given external id.
func (e *env) active(table, external string) string {
	var id string
	e.scan(&id, `SELECT id::text FROM `+table+` WHERE external_id = $1 AND superseded_at IS NULL`, external)
	return id
}

func (e *env) trace(entity provenance.Entity, id string) *provenance.Lineage {
	e.t.Helper()
	l, err := provenance.Trace(context.Background(), e.d, entity, id)
	if err != nil {
		e.t.Fatalf("trace %s %s: %v", entity, id, err)
	}
	return l
}

// complete asserts the whole chain of one version: connection, client, batch, raw metadata and
// normalizer, and that a row JSON came back.
func complete(t *testing.T, e *env, v provenance.Version, raw int64, normVersion int32) {
	t.Helper()
	switch {
	case v.Provider != "withings" || v.ConnectionID != e.conn || v.ConnectionMode != "push":
		t.Errorf("connection: %+v", v)
	case v.Client == nil || v.Client.ID != e.client || v.Client.Name != "synthetic-collector" || v.Client.Kind != "collector":
		t.Errorf("client: %+v", v.Client)
	case v.Batch == nil || v.Batch.SourceKind != "push" || v.Batch.IdempotencyKey == "":
		t.Errorf("batch: %+v", v.Batch)
	case v.Raw == nil || v.Raw.ID != raw || v.Raw.Stream != "sample.readings.v1" || v.Raw.SizeBytes != 42 ||
		len(v.Raw.ContentSHA256) != 64 || v.Raw.ShapeFingerprint != "fp1" || v.Raw.Status != "stored" ||
		string(v.Raw.RequestMeta) != `{"endpoint": "/measure"}`:
		t.Errorf("raw: %+v", v.Raw)
	case v.Normalizer != provenance.Normalizer{Name: "sample.readings", Version: normVersion, GitSHA: "abc123"}:
		t.Errorf("normalizer: %+v", v.Normalizer)
	case v.FetchedAt == nil || !v.FetchedAt.Equal(v.Raw.FetchedAt) || v.IngestedAt.IsZero() || v.NormalizedAt.IsZero():
		t.Errorf("times: %+v", v)
	case !json.Valid(v.Row):
		t.Errorf("row: %s", v.Row)
	}
}

func field(t *testing.T, row json.RawMessage, key string) any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(row, &m); err != nil {
		t.Fatal(err)
	}
	return m[key]
}

func TestTraceEveryEntityAfterCorrection(t *testing.T) {
	e := newEnv(t)
	raw1 := e.write(1, fixture(61, "left", "deep", 5))

	type target struct {
		entity provenance.Entity
		table  string
		ext    string
		id     string
	}
	targets := []*target{
		{entity: provenance.Measurement, table: "measurements", ext: "r1"},
		{entity: provenance.MeasurementGroup, table: "measurement_groups", ext: "b1"},
		{entity: provenance.SleepSession, table: "sleep_sessions", ext: "s1"},
		{entity: provenance.Workout, table: "workouts", ext: "w1"},
	}
	for _, tg := range targets {
		tg.id = e.active(tg.table, tg.ext)
		l := e.trace(tg.entity, tg.id)
		if l.Entity != tg.entity || l.Row.ID != tg.id || len(l.Earlier) != 0 || len(l.Later) != 0 {
			t.Fatalf("%s: uncorrected lineage %+v", tg.entity, l)
		}
		v := l.Row
		complete(t, e, v, raw1, 1)
		if v.SupersededBy != "" || v.SupersededAt != nil || v.CorrectedAt != nil || v.DeletedAt != nil || v.DeletedBy != nil {
			t.Errorf("%s: a never-corrected row carries history: %+v", tg.entity, v)
		}
		if field(t, v.Row, "dedupe_key") == nil {
			t.Errorf("%s: row lacks its dedupe key: %s", tg.entity, v.Row)
		}
	}
	// Row content per entity.
	if r := e.trace(provenance.Measurement, targets[0].id).Row.Row; field(t, r, "metric") != "heart_rate" || field(t, r, "device") != "dev-1" || field(t, r, "origin") != "com.example.app" || field(t, r, "unit") != "bpm" || field(t, r, "value") != 61.0 {
		t.Errorf("measurement row: %s", r)
	}
	if r := e.trace(provenance.MeasurementGroup, targets[1].id).Row.Row; len(field(t, r, "components").([]any)) != 2 {
		t.Errorf("group components: %s", r)
	}
	if r := e.trace(provenance.SleepSession, targets[2].id).Row.Row; len(field(t, r, "stages").([]any)) != 2 {
		t.Errorf("sleep stages: %s", r)
	}
	if r := e.trace(provenance.Workout, targets[3].id).Row.Row; len(field(t, r, "segments").([]any)) != 1 {
		t.Errorf("workout segments: %s", r)
	}

	// Correct every entity under a new normalizer version and a new raw payload.
	raw2 := e.write(2, fixture(64, "right", "rem", 6))
	for _, tg := range targets {
		newID := e.active(tg.table, tg.ext)
		if newID == tg.id {
			t.Fatalf("%s: correction kept the id", tg.entity)
		}
		// From the old row: one later version.
		old := e.trace(tg.entity, tg.id)
		if len(old.Earlier) != 0 || len(old.Later) != 1 || old.Later[0].ID != newID {
			t.Fatalf("%s: old lineage %+v", tg.entity, old)
		}
		if old.Row.SupersededBy != newID || old.Row.SupersededAt == nil {
			t.Errorf("%s: old row not linked: %+v", tg.entity, old.Row)
		}
		complete(t, e, old.Row, raw1, 1)
		// From the new row: the same chain seen from the other end.
		cur := e.trace(tg.entity, newID)
		if len(cur.Later) != 0 || len(cur.Earlier) != 1 || cur.Earlier[0].ID != tg.id {
			t.Fatalf("%s: new lineage %+v", tg.entity, cur)
		}
		complete(t, e, cur.Row, raw2, 2)
		if cur.Row.CorrectedAt == nil || !cur.Row.CorrectedAt.Equal(*old.Row.SupersededAt) || cur.Row.SupersededAt != nil {
			t.Errorf("%s: corrected/superseded times: %+v", tg.entity, cur.Row)
		}
		complete(t, e, cur.Earlier[0], raw1, 1)
		if !cur.Row.FetchedAt.After(*cur.Earlier[0].FetchedAt) && !cur.Row.FetchedAt.Equal(*cur.Earlier[0].FetchedAt) {
			t.Errorf("%s: fetch times out of order", tg.entity)
		}
	}
	// The measurement value moved with its version.
	if v := e.trace(provenance.Measurement, targets[0].id).Later[0]; field(t, v.Row, "value") != 64.0 {
		t.Errorf("corrected value: %s", v.Row)
	}
}

func TestTraceLongChainAndDeletion(t *testing.T) {
	e := newEnv(t)
	e.write(1, fixture(61, "left", "deep", 5))
	first := e.active("measurements", "r1")
	e.write(1, fixture(62, "left", "deep", 5))
	mid := e.active("measurements", "r1")
	raw3 := e.write(1, fixture(63, "left", "deep", 5))
	last := e.active("measurements", "r1")

	// From the middle: one earlier, one later, in order.
	l := e.trace(provenance.Measurement, mid)
	if len(l.Earlier) != 1 || l.Earlier[0].ID != first || len(l.Later) != 1 || l.Later[0].ID != last {
		t.Fatalf("middle lineage %+v", l)
	}
	// From the first: the whole chain ahead of it, oldest first.
	if l := e.trace(provenance.Measurement, first); len(l.Later) != 2 || l.Later[0].ID != mid || l.Later[1].ID != last {
		t.Fatalf("first lineage %+v", l)
	}

	// An upstream deletion records the payload that carried it.
	raw4 := e.write(1, normalize.Output{Tombstones: []normalize.Key{{RecordType: "reading", ExternalID: "r1"}}})
	l = e.trace(provenance.Measurement, last)
	if l.Row.DeletedAt == nil || l.Row.DeletedBy == nil || l.Row.DeletedBy.RawID != raw4 || l.Row.DeletedBy.FetchedAt == nil {
		t.Fatalf("deletion not traced: %+v", l.Row)
	}
	complete(t, e, l.Row, raw3, 1) // the row still points at the payload that created it
	if len(l.Earlier) != 2 {
		t.Errorf("earlier %d, want 2", len(l.Earlier))
	}
}

func TestTraceNotFoundAndBadInput(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, tc := range []struct {
		entity provenance.Entity
		id     string
	}{
		{provenance.Measurement, "99999"},
		{provenance.Measurement, "not-a-number"},
		{provenance.MeasurementGroup, "99999"},
		{provenance.SleepSession, uuid.NewString()},
		{provenance.Workout, "not-a-uuid"},
	} {
		if _, err := provenance.Trace(ctx, e.d, tc.entity, tc.id); !errors.Is(err, db.ErrNotFound) {
			t.Errorf("%s %s: %v, want ErrNotFound", tc.entity, tc.id, err)
		}
	}
	if _, err := provenance.Trace(ctx, e.d, "bogus", "1"); err == nil || errors.Is(err, db.ErrNotFound) {
		t.Errorf("unknown entity: %v", err)
	}
}
