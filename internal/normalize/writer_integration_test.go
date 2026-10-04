//go:build integration

package normalize

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// writerEnv extends env with a timezone period and raw payloads to cite.
func writerEnv(t *testing.T) *env {
	e := newEnv(t)
	e.exec(`INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES (gen_random_uuid(), $1, 'Europe/Amsterdam', '2020-01-01Z')`, e.user)
	return e
}

// raw inserts a synthetic raw payload for conn and returns its id.
func (e *env) raw(conn uuid.UUID) int64 {
	e.t.Helper()
	sha := uuid.New()
	e.exec(`INSERT INTO blobs (sha256, size_bytes, stored_bytes, compression) VALUES (sha256($1::bytea), 1, 1, 'none')`, sha[:])
	batch := uuid.New()
	e.exec(`INSERT INTO ingest_batches (id, user_id, connection_id, source_kind) VALUES ($1, $2, $3, 'sync')`, batch, e.user, conn)
	var id int64
	if err := e.scan(&id, `INSERT INTO raw_payloads (user_id, connection_id, batch_id, stream, external_key, content_sha256, content_type, fetched_at)
		VALUES ($1, $2, $3, 'test', $4, sha256($5::bytea), 'application/json', now()) RETURNING id`,
		e.user, conn, batch, uuid.NewString(), sha[:]); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) write(conn uuid.UUID, version int32, out Output) WriteStats {
	e.t.Helper()
	src := Source{ConnectionID: conn, RawPayloadID: e.raw(conn), NormalizerVersionID: version}
	var st WriteStats
	err := e.d.Tx(e.t.Context(), func(q *dbq.Queries) (err error) {
		st, err = Write(e.t.Context(), q, src, out)
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

func (e *env) int(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.scan(&n, sql, args...); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) text(sql string, args ...any) string {
	e.t.Helper()
	var s string
	if err := e.scan(&s, sql, args...); err != nil {
		e.t.Fatal(err)
	}
	return s
}

// activeHash hashes every column of every active canonical row, children included.
func (e *env) activeHash() string {
	const live = `superseded_at IS NULL AND deleted_at IS NULL`
	return e.text(`SELECT md5(concat_ws('#',
		(SELECT string_agg(m::text, '|' ORDER BY m.id) FROM measurements m WHERE ` + live + `),
		(SELECT string_agg(g::text, '|' ORDER BY g.id) FROM measurement_groups g WHERE ` + live + `),
		(SELECT string_agg(s::text, '|' ORDER BY s.id) FROM sleep_sessions s WHERE ` + live + `),
		(SELECT string_agg(st::text, '|' ORDER BY st.id) FROM sleep_stages st JOIN sleep_sessions s ON s.id = st.session_id WHERE s.` + live + `),
		(SELECT string_agg(w::text, '|' ORDER BY w.id) FROM workouts w WHERE ` + live + `),
		(SELECT string_agg(x::text, '|' ORDER BY x.workout_id, x.seq) FROM workout_segments x JOIN workouts w ON w.id = x.workout_id WHERE w.` + live + `),
		(SELECT string_agg(d::text, '|' ORDER BY d.id) FROM devices d),
		(SELECT string_agg(o::text, '|' ORDER BY o.id) FROM data_origins o)))`)
}

func ts(s string) time.Time { return utc(s) }

func tp(s string) *time.Time { t := utc(s); return &t }

// fixture covers every record type: id-keyed and natural-keyed measurements, a unit conversion,
// both group kinds, a staged sleep session and a workout with segments.
func fixture(hr float64) Output {
	return Output{
		Devices: []Device{{Fingerprint: "dev-1", Type: "scale", Model: "Synthetic 1"}},
		Origins: []Origin{{Key: "com.example.app", Name: "Example"}},
		Measurements: []Measurement{
			{Metric: "heart_rate", Kind: catalog.Sample, Start: ts("2026-06-15T07:00:00Z"), Value: hr, Unit: "bpm",
				Device: "dev-1", Origin: "com.example.app", Key: Key{RecordType: "reading", ExternalID: "r1"}},
			{Metric: "steps", Kind: catalog.Interval, Start: ts("2026-06-15T22:30:00Z"), End: tp("2026-06-15T22:35:00Z"), Value: 120, Unit: "count"},
			{Metric: "body_temperature", Kind: catalog.Sample, Start: ts("2026-06-15T07:05:00Z"), Value: 98.6, Unit: "°F",
				Zone: Zone{OffsetMin: off(-300)}, Key: Key{RecordType: "reading", ExternalID: "r2"}},
		},
		Groups: []Group{
			{Kind: "bp_reading", MeasuredAt: ts("2026-06-15T07:15:00Z"), Device: "dev-1", Context: json.RawMessage(`{"arm": "left"}`),
				Key: Key{RecordType: "bp", ExternalID: "b1"}, Components: []Measurement{
					{Metric: "bp_systolic", Kind: catalog.Sample, Start: ts("2026-06-15T07:15:00Z"), Value: 121, Unit: "mmHg"},
					{Metric: "bp_diastolic", Kind: catalog.Sample, Start: ts("2026-06-15T07:15:00Z"), Value: 79, Unit: "mmHg"},
				}},
			{Kind: "body_composition", MeasuredAt: ts("2026-06-15T06:50:00Z"), Device: "dev-1", Components: []Measurement{
				{Metric: "weight", Kind: catalog.Sample, Start: ts("2026-06-15T06:50:00Z"), Value: 154, Unit: "lb"},
				{Metric: "body_fat_ratio", Kind: catalog.Sample, Start: ts("2026-06-15T06:50:00Z"), Value: 20, Unit: "%"},
			}},
		},
		Sleep: []SleepSession{{Start: ts("2026-06-14T21:00:00Z"), End: ts("2026-06-15T05:00:00Z"), Key: Key{RecordType: "sleep", ExternalID: "s1"},
			Stages: []SleepStage{
				{Stage: "light", Start: ts("2026-06-14T21:00:00Z"), End: ts("2026-06-15T01:00:00Z")},
				{Stage: "deep", Start: ts("2026-06-15T01:00:00Z"), End: ts("2026-06-15T03:00:00Z")},
				{Stage: "rem", Start: ts("2026-06-15T03:00:00Z"), End: ts("2026-06-15T05:00:00Z")},
			}}},
		Workouts: []Workout{{Start: ts("2026-06-15T16:00:00Z"), End: ts("2026-06-15T17:00:00Z"), Sport: "running", ProviderSport: "run",
			Key: Key{RecordType: "workout", ExternalID: "w1"}, Segments: []Segment{
				{Kind: "lap", Start: ts("2026-06-15T16:00:00Z"), End: tp("2026-06-15T16:30:00Z"), Data: json.RawMessage(`{"pace_s_per_km": 330}`)},
				{Kind: "lap", Start: ts("2026-06-15T16:30:00Z"), End: tp("2026-06-15T17:00:00Z")},
			}}},
	}
}

const recordsInFixture = 11 // 3 measurements + 2 groups + 4 components + sleep + workout

func TestWriterReplayIsIdempotent(t *testing.T) {
	e := writerEnv(t)
	st := e.write(e.conn, 1, fixture(61))
	if st.Inserted != recordsInFixture || st.Superseded != 0 {
		t.Fatalf("first write %+v", st)
	}
	h := e.activeHash()
	for i := range 3 {
		st := e.write(e.conn, 1, fixture(61))
		if st.Inserted != 0 || st.Superseded != 0 || st.Unchanged != recordsInFixture || st.Reversioned != 0 {
			t.Fatalf("replay %d: %+v", i, st)
		}
		if got := e.activeHash(); got != h {
			t.Fatalf("replay %d changed the active rows", i)
		}
	}
	if n := e.int(`SELECT count(*) FROM measurements`); n != 7 {
		t.Errorf("%d measurement rows, want 7", n)
	}

	// Canonical units with the source value kept only when converted; local dates from the period
	// (no stored offset) or the record offset.
	var v, sv float64
	var unit string
	if err := e.scan(&v, `SELECT value FROM measurements WHERE external_id = 'r2'`); err != nil || v < 36.99 || v > 37.01 {
		t.Errorf("°F not converted: %v %v", v, err)
	}
	if err := e.scan(&sv, `SELECT source_value FROM measurements WHERE external_id = 'r2'`); err != nil || sv != 98.6 {
		t.Errorf("source value %v %v", sv, err)
	}
	if err := e.scan(&unit, `SELECT u.code FROM measurements m JOIN units u ON u.id = m.source_unit_id WHERE external_id = 'r2'`); err != nil || unit != "°F" {
		t.Errorf("source unit %q %v", unit, err)
	}
	if n := e.int(`SELECT count(*) FROM measurements WHERE external_id = 'r1' AND source_value IS NULL AND tz_offset_min IS NULL AND local_date = '2026-06-15'`); n != 1 {
		t.Error("unconverted value or period-derived date wrong")
	}
	// 22:30 UTC is 00:30 the next day in Amsterdam.
	if n := e.int(`SELECT count(*) FROM measurements m JOIN metric_catalog c ON c.id = m.metric_id WHERE c.code = 'steps' AND local_date = '2026-06-16'`); n != 1 {
		t.Error("steps local date should be the Amsterdam date")
	}
	if n := e.int(`SELECT count(*) FROM measurements WHERE external_id = 'r2' AND tz_offset_min = -300`); n != 1 {
		t.Error("record offset not stored")
	}
	// Components point at their group and carry the group's id as external id.
	if n := e.int(`SELECT count(*) FROM measurements m JOIN measurement_groups g ON g.id = m.group_id WHERE g.external_id = 'b1' AND m.external_id = 'b1' AND m.device_id IS NOT NULL`); n != 2 {
		t.Errorf("bp components linked: %d", n)
	}
	// Sleep totals summed from stages, dated by waking up; workout segments kept in order.
	if n := e.int(`SELECT count(*) FROM sleep_sessions WHERE totals_basis = 'stages' AND has_stages AND deep_s = 7200 AND asleep_s = 28800 AND sleep_date = '2026-06-15'`); n != 1 {
		t.Error("sleep totals")
	}
	if n := e.int(`SELECT count(*) FROM sleep_stages`); n != 3 {
		t.Errorf("%d stages", n)
	}
	if n := e.int(`SELECT count(*) FROM workout_segments WHERE (seq = 0 AND data->>'pace_s_per_km' = '330') OR (seq = 1 AND data = '{}')`); n != 2 {
		t.Error("workout segments")
	}
}

func TestWriterCorrectionSupersedesOneRow(t *testing.T) {
	e := writerEnv(t)
	e.write(e.conn, 1, fixture(61))
	e.exec(`DELETE FROM resolution_dirty`)

	st := e.write(e.conn, 1, fixture(64))
	if st.Inserted != 1 || st.Superseded != 1 || st.Unchanged != recordsInFixture-1 {
		t.Fatalf("correction %+v", st)
	}
	if n := e.int(`SELECT count(*) FROM measurements WHERE external_id = 'r1'`); n != 2 {
		t.Fatalf("%d rows for r1, want old + new", n)
	}
	if n := e.int(`SELECT count(*) FROM measurements o JOIN measurements n ON n.id = o.superseded_by
		WHERE o.external_id = 'r1' AND o.value = 61 AND o.superseded_at IS NOT NULL AND n.value = 64 AND n.superseded_at IS NULL`); n != 1 {
		t.Error("old row not linked to its successor")
	}
	// The correction marked its day dirty, in the same transaction; untouched rows marked nothing.
	if n := e.int(`SELECT count(*) FROM resolution_dirty d JOIN metric_catalog c ON c.id = d.metric_id WHERE c.code = 'heart_rate' AND d.local_date = '2026-06-15'`); n != 1 {
		t.Error("no dirty mark for the corrected day")
	}
	if n := e.int(`SELECT count(*) FROM resolution_dirty`); n != 1 {
		t.Errorf("%d dirty marks, want 1", n)
	}

	// A changed systolic supersedes only that component; the group row stays.
	out := fixture(64)
	out.Groups[0].Components[0].Value = 125
	if st := e.write(e.conn, 1, out); st.Inserted != 1 || st.Superseded != 1 {
		t.Fatalf("component correction %+v", st)
	}
	if n := e.int(`SELECT count(*) FROM measurement_groups WHERE external_id = 'b1'`); n != 1 {
		t.Error("group row was replaced for a component change")
	}
	// A changed group context replaces the group and re-points its components.
	out.Groups[0].Context = json.RawMessage(`{"arm": "right"}`)
	if st := e.write(e.conn, 1, out); st.Inserted != 3 || st.Superseded != 3 {
		t.Fatalf("group correction %+v", st)
	}
	if n := e.int(`SELECT count(*) FROM measurements m JOIN measurement_groups g ON g.id = m.group_id
		WHERE m.superseded_at IS NULL AND g.superseded_at IS NULL AND g.context->>'arm' = 'right'`); n != 2 {
		t.Error("components not moved to the new group row")
	}
	// Sleep stage and workout segment changes supersede the event with its children.
	out.Sleep[0].Stages[1].Stage = "light"
	out.Workouts[0].Segments[1].Data = json.RawMessage(`{"pace_s_per_km": 320}`)
	if st := e.write(e.conn, 1, out); st.Inserted != 2 || st.Superseded != 2 {
		t.Fatalf("event correction %+v", st)
	}
	if n := e.int(`SELECT count(*) FROM sleep_stages st JOIN sleep_sessions s ON s.id = st.session_id WHERE s.superseded_at IS NULL AND st.stage = 'light'`); n != 2 {
		t.Error("new sleep stages")
	}
	if n := e.int(`SELECT count(*) FROM sleep_sessions`); n != 2 {
		t.Error("old sleep session kept as history")
	}
}

func TestWriterTombstoneSetsDeletedOnly(t *testing.T) {
	e := writerEnv(t)
	e.write(e.conn, 1, fixture(61))
	cols := `SELECT m::text FROM (SELECT id, user_id, metric_id, kind, start_at, end_at, tz_offset_min, local_date, value,
		source_value, source_unit_id, provider_id, connection_id, device_id, origin_id, group_id, external_id, dedupe_key,
		quality_flags, raw_payload_id, normalizer_version_id, ingested_at, normalized_at, superseded_at, superseded_by
		FROM measurements WHERE external_id = 'r1') m`
	before := e.text(cols)
	e.exec(`DELETE FROM resolution_dirty`)

	st := e.write(e.conn, 1, Output{Tombstones: []Key{{RecordType: "reading", ExternalID: "r1"}, {RecordType: "bp", ExternalID: "b1"},
		{RecordType: "sleep", ExternalID: "s1"}, {RecordType: "workout", ExternalID: "w1"}, {RecordType: "reading", ExternalID: "unknown"}}})
	if st.Deleted != 6 || st.Inserted != 0 { // r1, the bp group and its two components, the sleep session, the workout
		t.Fatalf("tombstones %+v", st)
	}
	if got := e.text(cols); got != before {
		t.Errorf("tombstone changed more than deleted_at:\n%s\n%s", before, got)
	}
	if n := e.int(`SELECT count(*) FROM measurements WHERE external_id IN ('r1', 'b1') AND deleted_at IS NOT NULL AND deleted_by_raw_id IS NOT NULL`); n != 3 {
		t.Errorf("%d deleted measurements, want 3", n)
	}
	for _, table := range []string{"measurement_groups", "sleep_sessions", "workouts"} {
		if n := e.int(`SELECT count(*) FROM ` + table + ` WHERE deleted_at IS NULL`); n != map[string]int{"measurement_groups": 1}[table] {
			t.Errorf("%s: %d not deleted", table, n)
		}
	}
	if n := e.int(`SELECT count(*) FROM resolution_dirty d JOIN metric_catalog c ON c.id = d.metric_id WHERE c.code IN ('heart_rate', 'bp_systolic', 'sleep_deep')`); n != 3 {
		t.Errorf("deletions marked %d of 3 metrics dirty", n)
	}
	// Tombstoning again changes nothing; the record reappearing upstream comes back as a new row.
	if st := e.write(e.conn, 1, Output{Tombstones: []Key{{RecordType: "reading", ExternalID: "r1"}}}); st.Deleted != 0 {
		t.Errorf("second tombstone %+v", st)
	}
	out := fixture(61)
	if st := e.write(e.conn, 1, Output{Devices: out.Devices, Origins: out.Origins, Measurements: out.Measurements[:1]}); st.Inserted != 1 || st.Superseded != 1 {
		t.Errorf("reappearing record %+v", st)
	}
}

func TestWriterReconnectCreatesNoDuplicates(t *testing.T) {
	e := writerEnv(t)
	e.write(e.conn, 1, fixture(61))
	h := e.activeHash()
	// The owner disconnects, then connects the same Withings account again as a new connection row.
	e.exec(`UPDATE connections SET account_key = NULL, status = 'disabled' WHERE id = $1`, e.conn)
	again := uuid.New()
	e.exec(`INSERT INTO connections (id, user_id, provider_id, account_key, mode, status)
		VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'withings'), sha256('acct'), 'in_process', 'active')`, again, e.user)

	st := e.write(again, 1, fixture(61))
	if st.Inserted != 0 || st.Unchanged != recordsInFixture {
		t.Fatalf("reconnect %+v", st)
	}
	if e.activeHash() != h {
		t.Error("reconnect changed active rows")
	}
}

// TestWriterFollowsMergedDevice: after the owner merges dev-1 into another device (J20.7, rows
// repointed as the merge endpoint does), replaying the payload is a no-op and the device's
// fingerprint resolves to the target.
func TestWriterFollowsMergedDevice(t *testing.T) {
	e := writerEnv(t)
	e.write(e.conn, 1, fixture(61))
	target := uuid.New()
	e.exec(`INSERT INTO devices (id, user_id, provider_id, fingerprint) SELECT $1, $2, id, 'dev-2' FROM providers WHERE code = 'withings'`, target, e.user)
	for _, table := range []string{"measurements", "measurement_groups", "sleep_sessions", "workouts", "health_events"} {
		e.exec(`UPDATE `+table+` SET device_id = $1 WHERE device_id = (SELECT id FROM devices WHERE fingerprint = 'dev-1')`, target)
	}
	e.exec(`UPDATE devices SET merged_into = $1 WHERE fingerprint = 'dev-1'`, target)
	h := e.activeHash()

	st := e.write(e.conn, 1, fixture(61))
	if st.Inserted != 0 || st.Superseded != 0 || st.Unchanged != recordsInFixture {
		t.Fatalf("replay after merge %+v", st)
	}
	if e.activeHash() != h {
		t.Error("replay after merge changed active rows")
	}
	if st := e.write(e.conn, 1, fixture(62)); st.Inserted != 1 || st.Superseded != 1 {
		t.Fatalf("correction after merge %+v", st)
	}
	if n := e.int(`SELECT count(*) FROM measurements WHERE external_id = 'r1' AND superseded_at IS NULL AND device_id = $1`, target); n != 1 {
		t.Error("the corrected row is not on the target device")
	}
}

func TestWriterVersionBumpOnlyReversions(t *testing.T) {
	e := writerEnv(t)
	e.write(e.conn, 1, fixture(61))
	e.exec(`INSERT INTO normalizer_versions (name, version, git_sha) VALUES ('test', 2, 'dev')`)
	st := e.write(e.conn, 2, fixture(61))
	if st.Inserted != 0 || st.Reversioned != recordsInFixture {
		t.Fatalf("version bump %+v", st)
	}
	if n := e.int(`SELECT count(*) FROM measurements WHERE normalizer_version_id <> 2`); n != 0 {
		t.Errorf("%d rows kept the old version", n)
	}
	if n := e.int(`SELECT count(*) FROM resolution_dirty WHERE marked_at > now() - interval '1 hour'`); n == 0 {
		t.Error("expected marks from the first write")
	}
}

func TestWriterRelayAndQualityFlags(t *testing.T) {
	e := writerEnv(t)
	hk := uuid.New()
	e.exec(`INSERT INTO connections (id, user_id, provider_id, account_key, mode, status)
		VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'apple_health'), sha256('phone'), 'push', 'active')`, hk, e.user)
	out := Output{
		Origins: []Origin{{Key: "com.garmin.connect.mobile", Name: "Connect"}, {Key: "com.apple.health", Native: true}},
		Measurements: []Measurement{
			{Metric: "heart_rate", Kind: catalog.Sample, Start: ts("2026-06-15T07:00:00Z"), Value: 300, Unit: "bpm",
				Origin: "com.garmin.connect.mobile", Flags: FlagManualEntry | FlagImplausible, Key: Key{RecordType: "hk", ExternalID: "u1"}},
			{Metric: "heart_rate", Kind: catalog.Sample, Start: ts("2026-06-15T07:01:00Z"), Value: 60, Unit: "bpm",
				Origin: "com.apple.health", Flags: FlagImplausible, Key: Key{RecordType: "hk", ExternalID: "u2"}},
		},
	}
	e.write(hk, 1, out)
	if n := e.int(`SELECT count(*) FROM data_origins o JOIN providers p ON p.id = o.relayed_provider_id WHERE o.origin_key = 'com.garmin.connect.mobile' AND p.code = 'garmin'`); n != 1 {
		t.Error("relay origin not flagged from known_relay_origins")
	}
	if n := e.int(`SELECT count(*) FROM data_origins WHERE origin_key = 'com.apple.health' AND is_native AND relayed_provider_id IS NULL`); n != 1 {
		t.Error("native origin")
	}
	want := map[string]Flags{"u1": FlagManualEntry | FlagImplausible | FlagRelayed, "u2": 0}
	for id, f := range want {
		if got := Flags(e.int(`SELECT quality_flags FROM measurements WHERE external_id = $1`, id)); got != f {
			t.Errorf("%s flags = %b, want %b", id, got, f)
		}
	}
	// The second write reuses devices and origins.
	e.write(hk, 1, out)
	if n := e.int(`SELECT count(*) FROM data_origins`); n != 2 {
		t.Errorf("%d origins after replay", n)
	}
}

func TestWriterDuplicateKeyInPayload(t *testing.T) {
	e := writerEnv(t)
	f := fixture(61)
	m := f.Measurements[0]
	n := m
	n.Value = 62
	st := e.write(e.conn, 1, Output{Devices: f.Devices, Origins: f.Origins, Measurements: []Measurement{m, m, n}})
	if st.Inserted != 1 || len(st.Warnings) != 1 || st.Warnings[0].Code != "duplicate_key" {
		t.Fatalf("duplicates %+v", st)
	}
}

func TestRegisterVersions(t *testing.T) {
	e := newEnv(t)
	r, err := NewRegistry(fakeNorm{"a", 1, "s"}, fakeNorm{"b", 3, "t"})
	if err != nil {
		t.Fatal(err)
	}
	var first, second map[string]int32
	for _, dst := range []*map[string]int32{&first, &second} {
		if err := e.d.Tx(t.Context(), func(q *dbq.Queries) (err error) {
			*dst, err = RegisterVersions(t.Context(), q, r)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(first) != 2 || first["a"] != second["a"] || first["b"] != second["b"] || first["a"] == first["b"] {
		t.Errorf("ids %v then %v", first, second)
	}
	if sha := e.text(`SELECT git_sha FROM normalizer_versions WHERE name = 'b' AND version = 3`); sha == "" {
		t.Error("no git sha")
	}
}

// TestWriterConcurrentNewDevice: a second transaction naming a device and origin that a first,
// still open transaction just inserted waits on that insert and must then find the rows (a
// fallback SELECT on the statement's old snapshot used to return none).
func TestWriterConcurrentNewDevice(t *testing.T) {
	e := writerEnv(t)
	ctx := t.Context()
	out := fixture(61)
	src := func() Source {
		return Source{ConnectionID: e.conn, RawPayloadID: e.raw(e.conn), NormalizerVersionID: 1}
	}
	first, second := src(), src()
	inserted, release := make(chan struct{}), make(chan struct{})
	firstErr, secondErr := make(chan error, 1), make(chan error, 1)
	go func() {
		firstErr <- e.d.Tx(ctx, func(q *dbq.Queries) error {
			_, err := Write(ctx, q, first, out)
			close(inserted)
			<-release
			return err
		})
	}()
	<-inserted
	go func() {
		secondErr <- e.d.Tx(ctx, func(q *dbq.Queries) error {
			_, err := Write(ctx, q, second, Output{Devices: out.Devices, Origins: out.Origins})
			return err
		})
	}()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if e.int(`SELECT count(*) FROM pg_locks WHERE NOT granted AND locktype = 'transactionid'
			AND pid IN (SELECT pid FROM pg_stat_activity WHERE datname = current_database())`) > 0 {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("second writer never waited on the first")
		}
	}
	close(release)
	if err := <-firstErr; err != nil {
		t.Fatal(err)
	}
	if err := <-secondErr; err != nil {
		t.Fatal(err)
	}
	if n := e.int(`SELECT count(*) FROM devices`); n != 1 {
		t.Errorf("%d devices", n)
	}
}

// TestWriterHealthEvents: events follow the same rules as the other event tables: replay is
// unchanged, a change supersedes and links, a tombstone sets deleted_at, and nothing is dirty.
func TestWriterHealthEvents(t *testing.T) {
	e := writerEnv(t)
	ev := func(level string) Output {
		end := ts("2026-06-15T09:00:00Z")
		return Output{Events: []Event{{Code: "walking_steadiness_alert", Start: ts("2026-06-01T09:00:00Z"), End: &end,
			Level: level, Context: json.RawMessage(`{"k": 1}`), Flags: FlagManualEntry, Key: Key{RecordType: "ev", ExternalID: "e1"}}}}
	}
	if st := e.write(e.conn, 1, ev("initial_low")); st.Inserted != 1 {
		t.Fatalf("insert %+v", st)
	}
	if st := e.write(e.conn, 1, ev("initial_low")); st.Unchanged != 1 || st.Inserted != 0 {
		t.Errorf("replay %+v", st)
	}
	if st := e.write(e.conn, 1, ev("repeat_low")); st.Inserted != 1 || st.Superseded != 1 {
		t.Errorf("change %+v", st)
	}
	if n := e.int(`SELECT count(*) FROM health_events o JOIN health_events n ON n.id = o.superseded_by
		WHERE o.level = 'initial_low' AND n.level = 'repeat_low' AND n.superseded_at IS NULL AND n.local_date = '2026-06-01'
		AND n.quality_flags = 1`); n != 1 {
		t.Error("superseded event not linked to its successor")
	}
	if st := e.write(e.conn, 1, Output{Tombstones: []Key{{RecordType: "ev", ExternalID: "e1"}}}); st.Deleted != 1 {
		t.Errorf("tombstone %+v", st)
	}
	if n := e.int(`SELECT count(*) FROM resolution_dirty`); n != 0 {
		t.Errorf("events marked %d days dirty", n)
	}
	bad := ev("low")
	if _, err := Write(t.Context(), e.d.Q(), Source{ConnectionID: e.conn, RawPayloadID: e.raw(e.conn), NormalizerVersionID: 1}, bad); !errors.Is(err, ErrInvalidOutput) {
		t.Errorf("unknown level: %v", err)
	}
}
