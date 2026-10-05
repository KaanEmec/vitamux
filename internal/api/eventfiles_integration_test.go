//go:build integration

package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/connectors/applehealth"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// TestWatchIngestToEndpoints (J22.17): the synthetic Apple Watch examples go through the push
// endpoint, are stored raw, normalized, and read back: the ECG waveform and the workout route
// (which arrives before its workout) as stored documents with their SHA-256 as ETag, the new
// segment kinds, the summary goals in context and the event's file hash. Reprocessing changes
// nothing, and no route coordinate or ECG voltage reaches the logs.
func TestWatchIngestToEndpoints(t *testing.T) {
	e := newPushEnv(t)
	ctx := t.Context()
	// Sentinels: a route coordinate and an ECG voltage as the examples spell them.
	e.secrets = append(e.secrets, "0.00030000000000000003", "-17.701")

	var items []string
	for i, name := range []string{"route", "workout_detail", "ecg", "beats", "state_of_mind", "activity_summary", "workout_effort"} {
		body, err := os.ReadFile(filepath.Join("..", "..", "schemas", "examples", "healthkit-samples.v1", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, `{"stream":"`+applehealth.StreamSamples+`","external_key":"watch-`+name+`","fetched_at":"2026-09-14T19:02:1`+
			string(rune('0'+i))+`Z","content_type":"application/json","body":`+string(body)+`}`)
	}
	batch := []byte(`{"schema":"vitamux.ingest.batch/1","connection_id":"` + ingest.FormatConnectionID(e.own) +
		`","client":{"kind":"device","name":"phone","version":"1.0"},"items":[` + strings.Join(items, ",") + `]}`)
	res, out := e.send(pushReq{method: http.MethodPost, path: "/api/ingest/v1/batches", key: "watch-1", body: batch})
	e.expectCode(res, out, http.StatusAccepted, "")

	reg, err := normalize.NewRegistry(applehealth.Normalizer{})
	if err != nil {
		t.Fatal(err)
	}
	proc := &normalize.Processor{DB: e.d, Blobs: e.blobs, Registry: reg, Log: slog.New(slog.DiscardHandler)}
	vers, err := normalize.RegisterVersions(ctx, e.d.Q(), reg)
	if err != nil {
		t.Fatal(err)
	}
	normalizeAll := func() (inserted int) {
		if n := e.count(`SELECT count(*) FROM raw_payloads WHERE connection_id = $1`, e.own); n != 7 {
			t.Fatalf("%d raw payloads stored, want 7 (raw first)", n)
		}
		var ids []int64
		for i := range 7 {
			ids = append(ids, int64(e.count(`SELECT id FROM raw_payloads WHERE connection_id = $1 ORDER BY id OFFSET $2 LIMIT 1`, e.own, i)))
		}
		for _, id := range ids {
			r, err := proc.Process(ctx, id, vers)
			if err != nil || r.Outcome != normalize.Normalized {
				t.Fatalf("raw %d: %+v %v", id, r, err)
			}
			inserted += r.Stats.Inserted
		}
		return inserted
	}
	if n := normalizeAll(); n == 0 {
		t.Fatal("nothing normalized")
	}

	session := func() func(*http.Request) {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		tok := base64.RawURLEncoding.EncodeToString(b)
		e.exec(`INSERT INTO sessions (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, now() + interval '1 day')`, uuid.New(), e.userID, sha(tok))
		e.secrets = append(e.secrets, tok)
		return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: tok}) }
	}()
	get := func(pattern, target string, want int) (*http.Response, []byte) {
		t.Helper()
		req := request(t, http.MethodGet, target, nil)
		session(req)
		res := serve(t, e.h, req)
		body := checkResponse(t, pattern, res)
		if res.StatusCode != want {
			t.Fatalf("GET %s: %d, want %d: %s", target, res.StatusCode, want, body)
		}
		return res, body
	}

	// The ECG event lists its file; the waveform is the stored document with its hash as ETag.
	_, body := get("GET /api/v1/events", "/api/v1/events?code=ecg_recording&code=state_of_mind&code=workout_route", http.StatusOK)
	var events struct {
		Events []struct {
			ID, Code, Level string
			FileSha256      *string `json:"file_sha256"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &events); err != nil || len(events.Events) != 3 {
		t.Fatalf("events: %s %v", body, err)
	}
	var ecgID, moodID string
	var ecgSum string
	for _, ev := range events.Events {
		switch ev.Code {
		case "ecg_recording":
			if ev.Level != "sinus_rhythm" || ev.FileSha256 == nil {
				t.Errorf("ecg event: %+v", ev)
			}
			ecgID, ecgSum = ev.ID, *ev.FileSha256
		case "state_of_mind":
			moodID = ev.ID
			if ev.FileSha256 != nil {
				t.Error("state of mind has no file")
			}
		}
	}
	pattern := "GET /api/v1/events/{id}/waveform"
	res, wave := get(pattern, "/api/v1/events/"+ecgID+"/waveform", http.StatusOK)
	sum := sha256.Sum256(wave)
	if hex.EncodeToString(sum[:]) != ecgSum || res.Header.Get("ETag") != `"`+ecgSum+`"` ||
		res.Header.Get("Content-Type") != "application/json" || !strings.Contains(string(wave), `"format":"vitamux.waveform/1"`) {
		t.Errorf("waveform: %s %v", wave, res.Header)
	}
	get(pattern, "/api/v1/events/"+moodID+"/waveform", http.StatusNotFound)
	get(pattern, "/api/v1/events/"+uuid.NewString()+"/waveform", http.StatusNotFound)
	get(pattern, "/api/v1/events/not-a-uuid/waveform", http.StatusNotFound)

	// The route arrived before its workout and is found from it.
	_, body = get("GET /api/v1/workouts", "/api/v1/workouts?include=segments", http.StatusOK)
	var workouts struct {
		Workouts []struct {
			ID       string
			AvgHrBpm *float64 `json:"avg_hr_bpm"`
			Segments []struct{ Kind string }
		} `json:"workouts"`
	}
	if err := json.Unmarshal(body, &workouts); err != nil || len(workouts.Workouts) != 1 {
		t.Fatalf("workouts: %s %v", body, err)
	}
	w := workouts.Workouts[0]
	if w.AvgHrBpm == nil || *w.AvgHrBpm != 141 || len(w.Segments) != 4 || w.Segments[1].Kind != "pause" {
		t.Errorf("workout: %s", body)
	}
	pattern = "GET /api/v1/workouts/{id}/route"
	res, route := get(pattern, "/api/v1/workouts/"+w.ID+"/route", http.StatusOK)
	if !strings.Contains(string(route), `"format":"vitamux.route/1"`) || !strings.HasPrefix(res.Header.Get("ETag"), `"`) {
		t.Errorf("route: %s", route)
	}
	get(pattern, "/api/v1/workouts/"+uuid.NewString()+"/route", http.StatusNotFound)

	// Summary goals and the effort score's workout come back in the measurement context.
	_, body = get("GET /api/v1/measurements", "/api/v1/measurements?metric=move_time&metric=apple_workout_effort", http.StatusOK)
	if !strings.Contains(string(body), `"goal":1800`) || !strings.Contains(string(body), `"workout_uuid":"6F0D0000-0000-4000-8000-000000000131"`) {
		t.Errorf("measurement context: %s", body)
	}
	_, body = get("GET /api/v1/metrics/{code}", "/api/v1/metrics/rr_interval", http.StatusOK)
	if !strings.Contains(string(body), `"unresolved":true`) || !strings.Contains(string(body), `"windows":[]`) {
		t.Errorf("rr_interval: %s", body)
	}

	// Reprocessing rewrites nothing and takes no extra blob reference.
	if n := normalizeAll(); n != 0 {
		t.Errorf("reprocess inserted %d rows", n)
	}
	if n := e.count(`SELECT sum(refcount) FROM blobs WHERE sha256 IN (SELECT file_blob_sha256 FROM health_events)`); n != 2 {
		t.Errorf("waveform and route references %d, want 2", n)
	}
}
