package resolve

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestOverlappingWorkoutsFromTwoProviders(t *testing.T) {
	watch := Source{Provider: "garmin", DeviceType: "watch"}
	phone := Source{Provider: "apple_health", OriginKey: "com.apple.health.phone", DeviceType: "phone"}
	wo := func(id byte, src Source, sport, start, end string, segs ...WorkoutSegment) WorkoutInput {
		return WorkoutInput{ID: uuid.UUID{id}, Source: src, Sport: sport, Start: instant(start), End: instant(end), Segments: segs}
	}
	lap := WorkoutSegment{Seq: 1, Kind: "lap", Start: instant("2026-06-15T16:00:00Z"), End: instant("2026-06-15T16:30:00Z"), Data: json.RawMessage(`{"distance_m":5000}`)}
	run := wo(1, watch, "running", "2026-06-15T16:00:00Z", "2026-06-15T17:00:00Z", lap)
	runPhone := wo(2, phone, "running", "2026-06-15T16:02:00Z", "2026-06-15T16:58:00Z")
	generic := wo(3, phone, SportOther, "2026-06-15T16:05:00Z", "2026-06-15T16:55:00Z")
	walk := wo(4, phone, "walking", "2026-06-15T16:20:00Z", "2026-06-15T17:20:00Z")    // other sport; generic overlaps it by 0.7
	late := wo(5, phone, "running", "2026-06-15T16:40:00Z", "2026-06-15T17:40:00Z")    // overlaps 20 of 60 min
	evening := wo(6, watch, "cycling", "2026-06-15T18:00:00Z", "2026-06-15T19:00:00Z") // alone

	cs := ClusterWorkouts([]WorkoutInput{evening, late, walk, generic, runPhone, run})
	if len(cs) != 4 {
		t.Fatalf("%d clusters, want 4: %+v", len(cs), cs)
	}
	c := cs[0]
	if len(c.Workouts) != 3 || c.Workouts[0].ID != run.ID || !c.End.Equal(run.End) {
		t.Fatalf("first cluster = %+v, want run, runPhone and generic", c.Workouts)
	}
	for i, wantID := range []uuid.UUID{walk.ID, late.ID, evening.ID} {
		if got := cs[i+1].Workouts; len(got) != 1 || got[0].ID != wantID {
			t.Errorf("cluster %d = %+v, want only %v", i+1, got, wantID)
		}
	}

	r := &Rule{Groups: []Group{{ID: "watch", Match: []Selector{{DeviceType: "watch"}}}, {ID: "phone", Match: []Selector{{DeviceType: "phone"}}}}}
	p, ok := r.PickWorkout(c)
	if !ok || p.Workout.ID != run.ID || p.Group != 0 || len(p.Alternates) != 2 {
		t.Fatalf("pick = %+v, want the watch run with two alternates", p)
	}
	if len(p.Workout.Segments) != 1 || string(p.Workout.Segments[0].Data) != `{"distance_m":5000}` {
		t.Errorf("segments = %+v, want the watch's lap", p.Workout.Segments)
	}

	// Phone first: the longer phone workout wins, and it brings no segments of the watch.
	r.Groups[0], r.Groups[1] = r.Groups[1], r.Groups[0]
	if p, _ = r.PickWorkout(c); p.Workout.ID != runPhone.ID || p.Group != 0 || len(p.Workout.Segments) != 0 {
		t.Errorf("phone first: pick = %+v, want runPhone without segments", p.Workout)
	}

	// Excluded workouts are never picked.
	r.Exclude = []Selector{{DeviceType: "phone"}}
	if p, _ = r.PickWorkout(c); p.Workout.ID != run.ID {
		t.Errorf("phone excluded: pick = %v, want the watch run", p.Workout.ID)
	}
	if _, ok = r.PickWorkout(cs[1]); ok {
		t.Error("a cluster of excluded workouts has no pick")
	}
}
