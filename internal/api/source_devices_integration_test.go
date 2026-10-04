//go:build integration

package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// TestSourceDeviceMerge (J20.7): the owner types and names a device, then merges a second device
// of the same watch into it. Every record moves, later records of the merged fingerprint land on
// the target, and normalizing the same payload again changes nothing (dedupe keys keep the
// fingerprint). Synthetic fingerprints only.
func TestSourceDeviceMerge(t *testing.T) {
	e := newDeviceEnv(t)
	conn := uuid.New()
	e.exec(`INSERT INTO connections (id, user_id, provider_id, account_key, mode, status)
		VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'garmin'), sha256($3::bytea), 'push', 'active')`, conn, e.userID, conn[:])
	e.exec(`INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES (gen_random_uuid(), $1, 'Europe/Amsterdam', '2020-01-01Z')`, e.userID)
	t0 := time.Date(2026, 6, 15, 7, 0, 0, 0, time.UTC)
	out := normalize.Output{
		Devices: []normalize.Device{{Fingerprint: "synthetic:wearable", Type: "watch", Manufacturer: "Synthetic"}, {Fingerprint: "1000000001"}},
		Measurements: []normalize.Measurement{
			{Metric: "heart_rate", Kind: catalog.Sample, Start: t0, Value: 61, Unit: "bpm", Device: "synthetic:wearable"},
			{Metric: "steps", Kind: catalog.Interval, Start: t0, End: new(t0.Add(5 * time.Minute)), Value: 120, Unit: "count",
				Device: "1000000001", Key: normalize.Key{RecordType: "steps", ExternalID: "s1"}},
		},
		Sleep: []normalize.SleepSession{{Start: t0.Add(-8 * time.Hour), End: t0, Device: "synthetic:wearable",
			Totals: &normalize.SleepTotals{Asleep: new(int32(25200))}, Key: normalize.Key{RecordType: "sleep", ExternalID: "n1"}}},
		Workouts: []normalize.Workout{{Start: t0.Add(9 * time.Hour), End: t0.Add(10 * time.Hour), Sport: "running",
			Device: "1000000001", Key: normalize.Key{RecordType: "activity", ExternalID: "a1"}}},
	}
	write := func(out normalize.Output) normalize.WriteStats {
		t.Helper()
		batch := uuid.New()
		e.exec(`INSERT INTO blobs (sha256, size_bytes, stored_bytes, compression) VALUES (sha256($1::bytea), 1, 1, 'none')`, batch[:])
		e.exec(`INSERT INTO ingest_batches (id, user_id, connection_id, source_kind) VALUES ($1, $2, $3, 'push')`, batch, e.userID, conn)
		e.exec(`INSERT INTO raw_payloads (user_id, connection_id, batch_id, stream, external_key, content_sha256, content_type, fetched_at)
			VALUES ($1, $2, $3, 'synthetic', $4, sha256($5::bytea), 'application/json', now())`, e.userID, conn, batch, batch.String(), batch[:])
		raw := e.count(`SELECT id FROM raw_payloads WHERE batch_id = $1`, batch)
		var st normalize.WriteStats
		err := e.d.Tx(t.Context(), func(q *dbq.Queries) error {
			nv, err := q.RegisterNormalizerVersion(t.Context(), dbq.RegisterNormalizerVersionParams{Name: "synthetic", Version: 1, GitSha: "abc123"})
			if err != nil {
				return err
			}
			st, err = normalize.Write(t.Context(), q, normalize.Source{ConnectionID: conn, RawPayloadID: int64(raw), NormalizerVersionID: nv}, out)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	if st := write(out); st.Inserted != 4 {
		t.Fatalf("first write: %+v", st)
	}

	devices := func() map[string]map[string]any {
		t.Helper()
		st, body := e.call("GET /api/v1/source-devices", "/api/v1/source-devices?include=records", "", "")
		e.want(st, body, http.StatusOK, "")
		if !slices.Contains(body["device_types"].([]any), any("band")) {
			t.Fatalf("device_types %v", body["device_types"])
		}
		out := map[string]map[string]any{}
		for _, d := range body["devices"].([]any) {
			out[d.(map[string]any)["fingerprint"].(string)] = d.(map[string]any)
		}
		return out
	}
	list := devices()
	conns := list["1000000001"]["connections"].([]any)
	if len(conns) != 1 || conns[0].(map[string]any)["connection_id"] != ingest.FormatConnectionID(conn) ||
		conns[0].(map[string]any)["records"].(map[string]any)["workouts"] != float64(1) {
		t.Fatalf("records of the watch: %v", conns)
	}
	src, into := list["synthetic:wearable"]["id"].(string), list["1000000001"]["id"].(string)

	// Type and name: the vocabulary is enforced, and the owner's type survives the next write.
	patch := func(id, body string) (int, map[string]any) {
		return e.call("PATCH /api/v1/source-devices/{id}", "/api/v1/source-devices/"+id, body, "")
	}
	st, body := patch(into, `{"device_type":"watch","name":" Synthetic watch "}`)
	e.want(st, body, http.StatusNoContent, "")
	st, body = patch(src, `{"device_type":"band"}`)
	e.want(st, body, http.StatusNoContent, "")
	write(out)
	if d := devices(); d["1000000001"]["name"] != "Synthetic watch" || d["synthetic:wearable"]["device_type"] != "band" {
		t.Fatalf("patched devices: %v", d)
	}
	for _, bad := range []string{`{"device_type":"toaster"}`, `{"model":"x"}`, `{"name":"   "}`, `[]`} {
		st, body = patch(into, bad)
		e.want(st, body, http.StatusUnprocessableEntity, CodeValidationFailed)
	}
	st, body = patch("dev_"+strings.Repeat("0", 32), `{"name":null}`)
	e.want(st, body, http.StatusNotFound, CodeNotFound)
	if n := e.count(`SELECT count(*) FROM audit_events WHERE action = 'device.update'`); n != 2 {
		t.Errorf("%d device.update audit events, want 2", n)
	}

	merge := func(id, into string) (int, map[string]any) {
		return e.call("POST /api/v1/source-devices/{id}/merge", "/api/v1/source-devices/"+id+"/merge", `{"into":"`+into+`"}`, "")
	}
	st, body = merge(src, src)
	e.want(st, body, http.StatusUnprocessableEntity, CodeValidationFailed)
	e.exec(`DELETE FROM resolution_dirty`)
	st, body = merge(src, into)
	e.want(st, body, http.StatusOK, "")
	if m := body["moved"].(map[string]any); m["measurements"] != float64(1) || m["sleep_sessions"] != float64(1) || m["workouts"] != float64(0) {
		t.Fatalf("moved %v", m)
	}
	if n := e.count(`SELECT (SELECT count(*) FROM measurements WHERE device_id = $1) + (SELECT count(*) FROM sleep_sessions WHERE device_id = $1)`,
		mustDeviceID(t, src)); n != 0 {
		t.Fatalf("%d rows left on the merged device", n)
	}
	if n := e.count(`SELECT count(*) FROM resolution_dirty r JOIN metric_catalog c ON c.id = r.metric_id WHERE c.code IN ('heart_rate', 'sleep_total')`); n != 2 {
		t.Errorf("%d dirty marks for the moved heart rate and sleep, want 2", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_events WHERE action = 'device.merge' AND detail->>'into' = $1`, mustDeviceID(t, into).String()); n != 1 {
		t.Errorf("%d device.merge audit events", n)
	}
	if d := devices()["synthetic:wearable"]; d["merged_into"] != into || len(d["connections"].([]any)) != 0 {
		t.Fatalf("merged device: %v", d)
	}

	// Reprocessing is a no-op; a new record of the merged fingerprint lands on the target.
	if st := write(out); st.Inserted != 0 || st.Superseded != 0 || st.Unchanged != 4 {
		t.Fatalf("reprocess after merge: %+v", st)
	}
	out.Measurements = append(out.Measurements, normalize.Measurement{Metric: "heart_rate", Kind: catalog.Sample, Start: t0.Add(time.Minute),
		Value: 62, Unit: "bpm", Device: "synthetic:wearable"})
	if st := write(out); st.Inserted != 1 || st.Superseded != 0 {
		t.Fatalf("new record after merge: %+v", st)
	}
	if n := e.count(`SELECT count(*) FROM measurements WHERE device_id = $1`, mustDeviceID(t, into)); n != 3 {
		t.Errorf("%d measurements on the target, want 3", n)
	}

	// A merged device is neither edited, merged again nor a target; providers stay apart.
	st, body = merge(src, into)
	e.want(st, body, http.StatusConflict, CodeConflict)
	st, body = merge(into, src)
	e.want(st, body, http.StatusConflict, CodeConflict)
	st, body = patch(src, `{"name":"x"}`)
	e.want(st, body, http.StatusConflict, CodeConflict)
	e.exec(`INSERT INTO devices (id, user_id, provider_id, fingerprint) SELECT gen_random_uuid(), $1, id, 'synthetic-scale' FROM providers WHERE code = 'withings'`, e.userID)
	st, body = merge(devices()["synthetic-scale"]["id"].(string), into)
	e.want(st, body, http.StatusUnprocessableEntity, CodeValidationFailed)
}

func mustDeviceID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := parseDeviceID(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
