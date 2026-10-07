//go:build integration

package applehealth

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// watchExample reads one synthetic example of schemas/examples/healthkit-samples.v1 (J22.15).
func watchExample(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "schemas", "examples", "healthkit-samples.v1", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// edit rewrites one field of sample 0 (or of its nested object) of a page.
func edit(t *testing.T, body []byte, sample int, fn func(s map[string]any)) []byte {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	fn(p["samples"].([]any)[sample].(map[string]any))
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (e *env) routeOf(workoutExternalID string) []byte {
	e.t.Helper()
	var user, id uuid.UUID
	if err := e.scan(&id, `SELECT id FROM workouts WHERE external_id = $1 AND superseded_at IS NULL`, workoutExternalID); err != nil {
		e.t.Fatal(err)
	}
	if err := e.scan(&user, `SELECT user_id FROM workouts WHERE id = $1`, id); err != nil {
		e.t.Fatal(err)
	}
	sum, err := e.d.Q().GetWorkoutRouteFile(e.t.Context(), dbq.GetWorkoutRouteFileParams{UserID: user, ID: id})
	if err != nil {
		return nil
	}
	return sum
}

// TestWatchRouteBeforeWorkout: a route that arrives before its workout is found once the
// workout is there, the workout row is never rewritten, and the document is in the blob store
// with one reference.
func TestWatchRouteBeforeWorkout(t *testing.T) {
	e := newEnv(t)
	route := watchExample(t, "route")
	e.write(e.raw(route), route)
	workout := watchExample(t, "workout_detail")
	e.write(e.raw(workout), workout)

	sum := e.routeOf("6F0D0000-0000-4000-8000-000000000131")
	if len(sum) != 32 {
		t.Fatal("the workout does not find its route")
	}
	doc, err := e.blobs.Get(sum)
	if s := sha256.Sum256(doc); err != nil || !bytes.Equal(s[:], sum) || !bytes.Contains(doc, []byte(`"format":"vitamux.route/1"`)) {
		t.Fatalf("route document: %v", err)
	}
	if n := e.int(`SELECT refcount FROM blobs WHERE sha256 = $1`, sum); n != 1 {
		t.Errorf("route blob refcount %d, want 1", n)
	}
	if n := e.int(`SELECT count(*) FROM workouts`); n != 1 {
		t.Errorf("%d workout rows, want 1 (never rewritten)", n)
	}
	if n := e.int(`SELECT count(*) FROM workout_segments WHERE kind IN ('activity', 'pause', 'lap')`); n != 4 {
		t.Errorf("%d workout segments, want 4", n)
	}

	// Without workout_uuid the route is found by time, from the same origin.
	other := edit(t, route, 0, func(s map[string]any) {
		delete(s, "workout_uuid")
		s["uuid"] = "6F0D0000-0000-4000-8000-0000000001C1"
	})
	e.exec(`UPDATE health_events SET deleted_at = now() WHERE code = 'workout_route'`)
	e.write(e.raw(other), other)
	if len(e.routeOf("6F0D0000-0000-4000-8000-000000000131")) != 32 {
		t.Error("a route without workout_uuid inside the workout is not found")
	}
}

// TestWatchBlobsReplayAndChange: replaying an ECG page rewrites nothing (same rows, same blob
// reference); a changed recording supersedes its event, and both versions hold a reference.
func TestWatchBlobsReplayAndChange(t *testing.T) {
	e := newEnv(t)
	ecg := watchExample(t, "ecg")
	id := e.raw(ecg)
	first := e.write(id, ecg)
	again := e.write(id, ecg)
	if first.Inserted != 1 || again.Inserted != 0 || again.Unchanged != 1 || again.Superseded != 0 {
		t.Errorf("first %+v, again %+v", first, again)
	}
	if n := e.int(`SELECT refcount FROM blobs b JOIN health_events h ON h.file_blob_sha256 = b.sha256`); n != 1 {
		t.Errorf("waveform refcount %d after a replay, want 1", n)
	}
	changed := edit(t, ecg, 0, func(s map[string]any) {
		s["ecg"].(map[string]any)["voltages"].([]any)[1] = 15.6
	})
	if st := e.write(e.raw(changed), changed); st.Inserted != 1 || st.Superseded != 1 {
		t.Errorf("changed recording: %+v", st)
	}
	if n := e.int(`SELECT count(DISTINCT file_blob_sha256) FROM health_events`); n != 2 {
		t.Errorf("%d waveforms, want the old and the new", n)
	}
	if n := e.int(`SELECT sum(refcount) FROM blobs WHERE sha256 IN (SELECT file_blob_sha256 FROM health_events)`); n != 2 {
		t.Errorf("waveform references %d, want one per row", n)
	}
}

// TestWatchSummaryDayChanges: a re-read summary day that changed supersedes only its changed
// values; the unchanged day stays as it was.
func TestWatchSummaryDayChanges(t *testing.T) {
	e := newEnv(t)
	day := watchExample(t, "activity_summary")
	if st := e.write(e.raw(day), day); st.Inserted != 8 {
		t.Fatalf("first read: %+v", st)
	}
	changed := edit(t, day, 1, func(s map[string]any) {
		s["activity_summary"].(map[string]any)["active_energy_kcal"] = 702.5
	})
	st := e.write(e.raw(changed), changed)
	if st.Inserted != 1 || st.Superseded != 1 || st.Unchanged != 7 {
		t.Errorf("changed day: %+v", st)
	}
	if n := e.int(`SELECT count(*) FROM measurements m JOIN metric_catalog c ON c.id = m.metric_id
		WHERE c.code = 'active_energy' AND m.superseded_at IS NULL AND m.kind = 'daily_value' AND m.local_date = '2026-09-14'
		AND m.value = 702.5 AND m.context ->> 'goal' = '600' AND m.context ->> 'move_mode' = 'active_energy'`); n != 1 {
		t.Error("the changed day's active row")
	}
	if n := e.int(`SELECT count(*) FROM data_origins WHERE origin_key = 'vitamux.activity-summary' AND is_native`); n != 1 {
		t.Error("the summary marker origin is native")
	}
}

// TestWatchBeatsDeletion: a heartbeat series is one row per beat; deleting the series
// withdraws them all, and the deletion replayed is a no-op.
func TestWatchBeatsDeletion(t *testing.T) {
	e := newEnv(t)
	beats := watchExample(t, "beats")
	if st := e.write(e.raw(beats), beats); st.Inserted != 6 {
		t.Fatalf("beats: %+v", st)
	}
	del := []byte(`{"type": "HKDataTypeIdentifierHeartbeatSeries", "anchor": {}, "samples": [], "deleted": [{"uuid": "6f0d0000-0000-4000-8000-000000000111"}]}`)
	id := e.raw(del)
	if st := e.write(id, del); st.Deleted != 6 {
		t.Errorf("deleted %d beats, want 6", st.Deleted)
	}
	if st := e.write(id, del); st.Deleted != 0 {
		t.Errorf("replayed deletion deleted %d", st.Deleted)
	}
	if n := e.int(`SELECT count(*) FROM resolution_dirty r JOIN metric_catalog c ON c.id = r.metric_id WHERE c.code = 'rr_interval'`); n == 0 {
		t.Error("the deleted beats' day is marked for the hourly aggregates")
	}
}
