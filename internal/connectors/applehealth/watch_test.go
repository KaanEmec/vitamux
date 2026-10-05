package applehealth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// example normalizes one synthetic example of schemas/examples/healthkit-samples.v1 (J22.15).
func example(t *testing.T, name string) normalize.Output {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "schemas", "examples", "healthkit-samples.v1", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return normalizeBody(t, body)
}

func normalizeBody(t *testing.T, body []byte) normalize.Output {
	t.Helper()
	out, err := Normalizer{}.Normalize(t.Context(), normalize.RawPayload{Stream: StreamSamples, Body: body}, normalize.Env{Provider: Provider})
	if err != nil {
		t.Fatal(err)
	}
	if err := out.Validate(); err != nil {
		t.Fatal(err)
	}
	return out
}

func doc(t *testing.T, out normalize.Output, sum []byte) map[string]any {
	t.Helper()
	i := slices.IndexFunc(out.Files, func(f normalize.File) bool { return string(f.SHA256) == string(sum) })
	if i < 0 {
		t.Fatal("event file not in Files")
	}
	var m map[string]any
	if err := json.Unmarshal(out.Files[i].Doc, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func ctxOf(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestWatchExampleECG(t *testing.T) {
	out := example(t, "ecg")
	if len(out.Events) != 1 || len(out.Files) != 1 || len(out.Warnings) != 0 {
		t.Fatalf("one event with one waveform: %+v", out)
	}
	e := out.Events[0]
	if e.Code != "ecg_recording" || e.Level != "sinus_rhythm" || e.Value == nil || *e.Value != 64 ||
		e.Key.ExternalID != "6F0D0000-0000-4000-8000-000000000101" {
		t.Errorf("event: %+v", e)
	}
	c := ctxOf(t, e.Context)
	if c["symptoms_status"] != "none" || c["sampling_frequency_hz"] != 512.0 || c["voltage_count"] != 16.0 ||
		c["lead"] != "apple_watch_similar_to_lead_i" || c["algorithm_version"] != 2.0 {
		t.Errorf("context: %v", c)
	}
	d := doc(t, out, e.FileSHA256)
	if d["format"] != catalog.FileWaveform || d["unit"] != "µV" || len(d["values"].([]any)) != 16 || d["offsets_s"] != nil {
		t.Errorf("waveform: %v", d)
	}
	// The deleted recording tombstones its event.
	if len(out.Tombstones) != 1 || out.Tombstones[0] != (normalize.Key{RecordType: typeECG, ExternalID: "6F0D0000-0000-4000-8000-000000000102"}) {
		t.Errorf("tombstones: %+v", out.Tombstones)
	}
}

func TestWatchExampleBeats(t *testing.T) {
	out := example(t, "beats")
	// 8 beats: no interval before the first, and beat 5 follows a gap.
	if len(out.Measurements) != 6 {
		t.Fatalf("%d rr_interval rows, want 6", len(out.Measurements))
	}
	var idx []string
	for _, m := range out.Measurements {
		if m.Metric != metricRR || m.Kind != catalog.Sample || m.Unit != "s" || m.Value <= 0 || m.Value > 2 {
			t.Errorf("row: %+v", m)
		}
		_, n, _ := strings.Cut(m.Key.ExternalID, "#")
		idx = append(idx, n)
	}
	if !slices.Equal(idx, []string{"1", "2", "3", "4", "6", "7"}) {
		t.Errorf("beat indexes %v", idx)
	}
	start, _ := time.Parse(time.RFC3339, "2026-09-14T03:12:00.92+02:00")
	if first := out.Measurements[0]; !first.Start.Equal(start) || first.Value != 0.92 {
		t.Errorf("first interval at the second beat: %+v", first)
	}
	del := normalizeBody(t, []byte(`{"type": "`+typeHeartbeat+`", "anchor": {}, "samples": [], "deleted": [{"uuid": "6f0d0000-0000-4000-8000-000000000111"}]}`))
	if len(del.Tombstones) != 0 || len(del.SeriesTombstones) != 1 ||
		del.SeriesTombstones[0] != (normalize.SeriesKey{Metric: metricRR, ExternalID: "6F0D0000-0000-4000-8000-000000000111"}) {
		t.Errorf("a deleted series withdraws its beats: %+v", del)
	}
}

func TestWatchExampleRoute(t *testing.T) {
	out := example(t, "route")
	if len(out.Events) != 1 || len(out.Files) != 1 {
		t.Fatalf("%+v", out)
	}
	e := out.Events[0]
	c := ctxOf(t, e.Context)
	if e.Code != "workout_route" || c["workout_uuid"] != "6F0D0000-0000-4000-8000-000000000131" || c["point_count"] != 5.0 {
		t.Errorf("route event: %+v %v", e, c)
	}
	d := doc(t, out, e.FileSHA256)
	if d["format"] != catalog.FileRoute || d["count"] != 5.0 || len(d["latitude"].([]any)) != 5 || len(d["course_accuracy_deg"].([]any)) != 5 {
		t.Errorf("route document: %v", d)
	}
	// Arrays that disagree with count are refused, never trimmed.
	var p map[string]any
	body, _ := os.ReadFile(filepath.Join("..", "..", "..", "schemas", "examples", "healthkit-samples.v1", "route.json"))
	_ = json.Unmarshal(body, &p)
	p["samples"].([]any)[0].(map[string]any)["route"].(map[string]any)["speed_mps"] = []any{1.0}
	bad, _ := json.Marshal(p)
	if out := normalizeBody(t, bad); len(out.Events) != 0 || len(out.Warnings) != 1 || out.Warnings[0].Code != "bad_route" {
		t.Errorf("mismatched arrays: %+v", out)
	}
}

func TestWatchExampleStateOfMind(t *testing.T) {
	out := example(t, "state_of_mind")
	if len(out.Events) != 1 {
		t.Fatalf("%+v", out)
	}
	e := out.Events[0]
	c := ctxOf(t, e.Context)
	if e.Code != "state_of_mind" || e.Level != "daily_mood" || *e.Value != 0.35 || e.End != nil ||
		c["valence_classification"] != "slightly_pleasant" ||
		!slices.Equal(c["labels"].([]any), []any{"excited", "jealous"}) || !slices.Equal(c["associations"].([]any), []any{"fitness", "money"}) {
		t.Errorf("state of mind: %+v %v", e, c)
	}
}

func TestWatchExampleActivitySummary(t *testing.T) {
	out := example(t, "activity_summary")
	if len(out.Measurements) != 8 || len(out.Origins) != 1 || !out.Origins[0].Native || out.Origins[0].Key != activitySummaryBundle {
		t.Fatalf("two days of four daily values from the native marker origin: %+v", out)
	}
	m := out.Measurements[1]
	c := ctxOf(t, m.Context)
	if m.Metric != "move_time" || m.Kind != catalog.DailyValue || m.Value != 2400 || c["goal"] != 1800.0 ||
		c["move_mode"] != "active_energy" || c["paused"] != false ||
		m.Key != (normalize.Key{RecordType: typeActivitySummary, ExternalID: "E5D48049-0256-55FC-9C9B-8DC92A4B2531", Component: "move_time"}) {
		t.Errorf("move_time: %+v %v", m, c)
	}
	if m.Zone.OffsetMin == nil || *m.Zone.OffsetMin != 120 {
		t.Errorf("local_date comes from the day's own offset: %+v", m.Zone)
	}
}

func TestWatchExampleWorkoutDetail(t *testing.T) {
	out := example(t, "workout_detail")
	if len(out.Workouts) != 1 {
		t.Fatalf("%+v", out)
	}
	w := out.Workouts[0]
	if w.AvgHRBpm == nil || *w.AvgHRBpm != 141 || w.MaxHRBpm == nil || *w.MaxHRBpm != 172 {
		t.Errorf("heart-rate stats: %+v", w)
	}
	var kinds []string
	for _, s := range w.Segments {
		kinds = append(kinds, s.Kind)
	}
	if !slices.Equal(kinds, []string{"activity", "pause", "activity", "lap"}) {
		t.Errorf("segments %v", kinds)
	}
	if !w.Segments[1].End.Equal(w.Segments[2].Start) {
		t.Error("the pause lasts until the resume")
	}
}

func TestWatchExampleEffort(t *testing.T) {
	out := example(t, "workout_effort")
	if len(out.Measurements) != 1 {
		t.Fatalf("%+v", out)
	}
	m := out.Measurements[0]
	if m.Metric != "apple_workout_effort" || m.Kind != catalog.Interval || m.Value != 6 ||
		ctxOf(t, m.Context)["workout_uuid"] != "6F0D0000-0000-4000-8000-000000000131" {
		t.Errorf("effort: %+v", m)
	}
}

// workoutPage builds a workout page with the given events and activities.
func workoutPage(events, activities string) []byte {
	return []byte(`{"type": "HKWorkoutTypeIdentifier", "anchor": {}, "deleted": [], "samples": [{
		"uuid": "6F0D0000-0000-4000-8000-0000000001A1", "start": "2026-09-14T18:00:00Z", "end": "2026-09-14T19:00:00Z",
		"source_revision": {"bundle_id": "com.apple.health.synthetic", "name": "Synthetic Watch"}, "was_user_entered": false,
		"workout": {"activity_type": 37, "duration_s": 3600, "events": ` + events + `, "activities": ` + activities + `}}]}`)
}

func TestWorkoutSegments(t *testing.T) {
	ev := func(typ int, at string) string {
		return `{"type": ` + string(rune('0'+typ)) + `, "start": "2026-09-14T` + at + `Z", "end": "2026-09-14T` + at + `Z"}`
	}
	out := normalizeBody(t, workoutPage("["+strings.Join([]string{
		ev(evMotionPaused, "18:10:00"), ev(evMotionResumed, "18:11:00"), ev(evMarker, "18:20:00"),
		ev(evPauseOrResumeRequest, "18:30:00"), ev(evResume, "18:31:00"), ev(evPause, "18:50:00"), ev(9, "18:55:00"),
	}, ",")+"]",
		`[{"uuid": "6F0D0000-0000-4000-8000-0000000001A2", "activity_type": 37, "start": "2026-09-14T18:00:00Z", "end": "2026-09-14T19:00:00Z", "duration_s": 3600,
		   "events": [{"type": 3, "start": "2026-09-14T18:00:00Z", "end": "2026-09-14T18:05:00Z"}]}]`))
	w := out.Workouts[0]
	var got []string
	for _, s := range w.Segments {
		got = append(got, s.Kind+" "+string(s.Data))
	}
	want := []string{
		`lap {"activity_uuid":"6F0D0000-0000-4000-8000-0000000001A2"}`, // a single activity stores no activity segment
		`pause {"type":"motion_pause"}`,
		`marker {"type":"marker"}`,
		`marker {"type":"pause_or_resume_request"}`,
		`pause {"type":"pause"}`, // unresumed: lasts until the workout ends; the stray resume is ignored
	}
	if !slices.Equal(got, want) {
		t.Errorf("segments\n got %q\nwant %q", got, want)
	}
	if end := w.Segments[4].End; end == nil || !end.Equal(w.End) {
		t.Error("an open pause ends with the workout")
	}
	if len(out.Warnings) != 1 || out.Warnings[0].Code != "unknown_workout_event" {
		t.Errorf("warnings %+v", out.Warnings)
	}
}

func TestWatchEnumsAndRejects(t *testing.T) {
	page := func(typ, field string) []byte {
		return []byte(`{"type": "` + typ + `", "anchor": {}, "deleted": [], "samples": [{"uuid": "6F0D0000-0000-4000-8000-0000000001B1",
			"start": "2026-09-14T18:00:00Z", "end": "2026-09-14T18:00:00Z", "source_revision": {"bundle_id": "b", "name": "n"},
			"was_user_entered": false, ` + field + `}]}`)
	}
	for _, c := range []struct {
		name, typ, field, warn string
		events                 int
	}{
		{"unknown label keeps its number", typeStateOfMind, `"state_of_mind": {"kind": 1, "valence": -0.5, "labels": [99]}`, "unknown_enum_value", 1},
		{"unknown kind", typeStateOfMind, `"state_of_mind": {"kind": 7, "valence": 0}`, "bad_state_of_mind", 0},
		{"no state", typeStateOfMind, `"value": 1`, "bad_state_of_mind", 0},
		{"ecg in another unit", typeECG, `"ecg": {"classification": 1, "symptoms_status": 0, "voltage_count": 1, "voltage_unit": "V", "voltages": [0.1]}`, "bad_ecg", 0},
		{"ecg count mismatch", typeECG, `"ecg": {"classification": 1, "symptoms_status": 0, "voltage_count": 2, "voltage_unit": "mcV", "voltages": [0.1]}`, "bad_ecg", 0},
		{"unknown classification", typeECG, `"ecg": {"classification": 42, "symptoms_status": 0, "voltage_count": 1, "voltage_unit": "mcV", "voltages": [0.1]}`, "unknown_ecg_classification", 1},
		{"beats without gap flags", typeHeartbeat, `"beats": {"count": 2, "offsets_s": [0, 1], "preceded_by_gap": [false]}`, "bad_beats", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := normalizeBody(t, page(c.typ, c.field))
			if len(out.Events) != c.events || len(out.Warnings) != 1 || out.Warnings[0].Code != c.warn {
				t.Errorf("%+v", out)
			}
			if c.name == "unknown label keeps its number" && !strings.Contains(string(out.Events[0].Context), "unknown_99") {
				t.Errorf("context %s", out.Events[0].Context)
			}
		})
	}
}

// TestRegistryV2IsMapped is the drift check for the registry v2 identifiers the kit sends
// (apple/HealthBridgeKit Registry.swift): State of Mind and the effort scores.
func TestRegistryV2IsMapped(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "apple", "HealthBridgeKit", "Sources", "HealthBridgeHealthKit", "Registry.swift"))
	if err != nil {
		t.Skipf("registry source not available: %v", err)
	}
	if m := regexp.MustCompile(`stateOfMindID = "(\w+)"`).FindStringSubmatch(string(src)); m == nil || m[1] != typeStateOfMind {
		t.Errorf("State of Mind identifier %v, want %s", m, typeStateOfMind)
	}
	efforts := regexp.MustCompile(`effortScoreIDs = \("(\w+)", "(\w+)"\)`).FindStringSubmatch(string(src))
	if efforts == nil {
		t.Fatal("effortScoreIDs not found; has Registry.swift changed shape?")
	}
	for _, id := range efforts[1:] {
		if q, ok := quantities[id]; !ok || q.hkUnit != "appleEffortScore" {
			t.Errorf("effort score %s is not mapped", id)
		}
	}
	for _, typ := range []string{typeHeartbeat, typeRoute} {
		if !strings.Contains(string(src), strings.TrimPrefix(typ, "HK")) {
			t.Errorf("%s is not in the registry", typ)
		}
	}
}
