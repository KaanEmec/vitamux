//go:build integration

package imports

import (
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/connectors/applehealth"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

type importEnv struct {
	t    *testing.T
	proc *normalize.Processor
	scan func(dest any, sql string, args ...any) error
	user uuid.UUID
	zip  string
}

func newImportEnv(t *testing.T) *importEnv {
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
	reg, err := normalize.NewRegistry(applehealth.Normalizer{}, applehealth.ExportNormalizer{})
	if err != nil {
		t.Fatal(err)
	}
	e := &importEnv{t: t, user: uuid.New(), zip: writeSyntheticExport(t, t.TempDir()),
		proc: &normalize.Processor{DB: db.New(app), Blobs: blobs, Registry: reg, Log: slog.New(slog.DiscardHandler)},
		scan: func(dest any, sql string, args ...any) error {
			return owner.QueryRow(t.Context(), sql, args...).Scan(dest)
		}}
	for _, sql := range []string{
		`INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')`,
		`INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES (gen_random_uuid(), $1, 'Europe/Amsterdam', '2020-01-01Z')`,
	} {
		if _, err := owner.Exec(t.Context(), sql, e.user); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e *importEnv) int(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.scan(&n, sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (e *importEnv) importExport() AppleHealthReport {
	e.t.Helper()
	in, err := OpenExport(e.zip, DefaultLimits)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	rep, err := ImportAppleHealth(e.t.Context(), in, AppleHealthOptions{Processor: e.proc, Limits: DefaultLimits, PageSize: 3})
	if err != nil {
		e.t.Fatal(err)
	}
	return rep
}

// appSync stores and normalizes one healthkit.samples.v1 page as the iPhone app would push it.
func (e *importEnv) appSync(body string) {
	e.t.Helper()
	ctx := e.t.Context()
	var rawID int64
	err := e.proc.DB.Tx(ctx, func(q *dbq.Queries) error {
		conn, err := q.OwnerApplePushConnection(ctx, e.user)
		if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
			conn, err = q.InsertPushConnection(ctx, dbq.InsertPushConnectionParams{ID: uuid.New(), UserID: e.user, Provider: applehealth.Provider})
		}
		if err != nil {
			return err
		}
		ref, err := ingest.CreateBatch(ctx, q, ingest.BatchInfo{UserID: e.user, ConnectionID: conn, SourceKind: ingest.SourcePush})
		if err != nil {
			return err
		}
		res, err := ingest.StoreRaw(ctx, q, e.proc.Blobs, ref, []ingest.RawItem{{Stream: applehealth.StreamSamples,
			ExternalKey: uuid.NewString(), ContentType: "application/json", FetchedAt: time.Now(), Body: []byte(body)}})
		if err != nil {
			return err
		}
		rawID = res[0].RawPayloadID
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	vers, err := normalize.RegisterVersions(ctx, e.proc.DB.Q(), e.proc.Registry)
	if err != nil {
		e.t.Fatal(err)
	}
	if res, err := e.proc.Process(ctx, rawID, vers); err != nil || res.Outcome != normalize.Normalized {
		e.t.Fatalf("app page: %+v %v", res, err)
	}
}

// appSample is one app sample of the synthetic watch; start and end carry sub-second
// precision, which the export drops.
func appSample(id, start, end, value, unit string) string {
	return `{"uuid": "` + id + `", "start": "` + start + `", "end": "` + end + `", "value": ` + value + `, "unit": "` + unit + `",
		"source_revision": {"bundle_id": "com.apple.health.00000000-0000-4000-8000-0000000000A1", "name": "Synthetic Watch"},
		"device": {"name": "Synthetic Watch", "manufacturer": "Apple Inc.", "model": "Watch", "hardware_version": "Watch7,1"}}`
}

func (e *importEnv) counts() map[string]int {
	out := map[string]int{}
	for _, table := range []string{"raw_payloads", "measurements", "measurement_groups", "sleep_sessions", "sleep_stages", "workouts", "health_events"} {
		out[table] = e.int(`SELECT count(*) FROM ` + table)
	}
	out["active measurements"] = e.int(`SELECT count(*) FROM measurements WHERE superseded_at IS NULL AND deleted_at IS NULL`)
	return out
}

func TestImportSyntheticExport(t *testing.T) {
	e := newImportEnv(t)
	// The app already synced one heart-rate sample and one sleep stage of the export.
	e.appSync(`{"type": "HKQuantityTypeIdentifierHeartRate", "anchor": {}, "deleted": [], "samples": [` +
		appSample("6F0D0000-0000-4000-8000-000000000001", "2026-09-14T07:31:05.120+02:00", "2026-09-14T07:31:05.120+02:00", "61", "count/min") + `]}`)
	e.appSync(`{"type": "HKCategoryTypeIdentifierSleepAnalysis", "anchor": {}, "deleted": [], "samples": [` +
		appSample("6F0D0000-0000-4000-8000-000000000002", "2026-09-15T01:00:00.250+02:00", "2026-09-15T01:45:00.250+02:00", "4", "") + `]}`)

	rep := e.importExport()
	if rep.Records != syntheticRecords || rep.NewPages != rep.Pages || rep.Normalized.Failed != 0 || rep.Normalized.Inserted == 0 {
		t.Fatalf("first import: %+v", rep)
	}
	hr, sleep := rep.Overlaps["HKQuantityTypeIdentifierHeartRate"], rep.Overlaps["HKCategoryTypeIdentifierSleepAnalysis"]
	if len(rep.Overlaps) != 2 || hr == nil || hr.Records != 1 || sleep == nil || sleep.Records != 1 ||
		!hr.From.Equal(time.Date(2026, 9, 14, 5, 31, 5, 0, time.UTC)) || !sleep.To.Equal(time.Date(2026, 9, 14, 23, 45, 0, 0, time.UTC)) {
		t.Fatalf("overlaps: %+v", rep.Overlaps)
	}
	// Overlapping records are kept raw but never become a second copy.
	if n := e.int(`SELECT count(*) FROM measurements m JOIN metric_catalog c ON c.id = m.metric_id WHERE c.code = 'heart_rate'`); n != 3 {
		t.Errorf("%d heart-rate rows, want the app's 1 and the export's other 2", n)
	}
	if n := e.int(`SELECT count(*) FROM sleep_stages WHERE stage = 'deep'`); n != 1 {
		t.Errorf("%d deep stages, want 1", n)
	}
	if n := e.int(`SELECT count(*) FROM sleep_sessions`); n != 2 {
		t.Errorf("%d sleep sessions, want the app's and the export's", n)
	}
	for what, sql := range map[string]string{
		"weight in kg, user entered": `SELECT count(*) FROM measurements m JOIN metric_catalog c ON c.id = m.metric_id
			WHERE c.code = 'weight' AND abs(m.value - 72.802) < 0.001 AND m.quality_flags & 1 = 1`,
		"blood pressure reading": `SELECT count(*) FROM measurement_groups g JOIN data_origins o ON o.id = g.origin_id
			WHERE g.kind = 'bp_reading' AND o.origin_key = 'export:Synthetic Cuff App' AND (SELECT count(*) FROM measurements WHERE group_id = g.id) = 2`,
		"workout": `SELECT count(*) FROM workouts WHERE sport = 'running' AND distance_m = 5200 AND energy_kcal = 310.5`,
		"event":   `SELECT count(*) FROM health_events WHERE code = 'high_heart_rate_alert' AND context ->> 'HKMetadataKeyHeartRateEventThreshold' = '120 count/min'`,
		"device":  `SELECT count(*) FROM devices WHERE device_type = 'watch' AND model = 'Watch' AND hardware_version = 'Watch7,1'`,
	} {
		if n := e.int(sql); n != 1 {
			t.Errorf("%s: %d rows, want 1", what, n)
		}
	}
	if n := e.int(`SELECT count(*) FROM raw_payloads WHERE stream = $1 AND status = 'normalized'`, applehealth.StreamExport); n != rep.Pages {
		t.Errorf("%d normalized export pages, want %d", n, rep.Pages)
	}

	// Importing the same export again is a no-op.
	before := e.counts()
	again := e.importExport()
	if again.NewPages != 0 || again.Normalized != (normalize.Summary{}) || again.Pages != rep.Pages || len(again.Overlaps) != 2 {
		t.Errorf("second import: %+v", again)
	}
	if after := e.counts(); len(after) != len(before) || after["raw_payloads"] != before["raw_payloads"] ||
		after["measurements"] != before["measurements"] || after["sleep_sessions"] != before["sleep_sessions"] {
		t.Errorf("second import changed rows: %v → %v", before, after)
	}

	// The app later syncs a sample the export already added: importing again withdraws the export's copy.
	e.appSync(`{"type": "HKQuantityTypeIdentifierHeartRate", "anchor": {}, "deleted": [], "samples": [` +
		appSample("6F0D0000-0000-4000-8000-000000000003", "2026-09-14T18:02:00.400+02:00", "2026-09-14T18:02:00.400+02:00", "142", "count/min") + `]}`)
	third := e.importExport()
	if third.NewPages != 1 || third.Normalized.Deleted != 1 || third.Overlaps["HKQuantityTypeIdentifierHeartRate"].Records != 2 {
		t.Errorf("third import: %+v", third)
	}
	if n := e.int(`SELECT count(*) FROM measurements m JOIN metric_catalog c ON c.id = m.metric_id
		WHERE c.code = 'heart_rate' AND m.deleted_at IS NULL AND m.superseded_at IS NULL`); n != 3 {
		t.Errorf("%d active heart-rate rows, want 3", n)
	}
	if n := e.int(`SELECT count(*) FROM import_runs WHERE source = 'apple_health_export' AND connection_id IS NOT NULL`); n != 3 {
		t.Errorf("%d import runs, want 3", n)
	}
}
