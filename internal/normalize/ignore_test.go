package normalize

import (
	"testing"
	"time"
)

func TestDropOrigins(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	file := File{SHA256: []byte("synthetic-file-hash-0000000000000"), Doc: []byte(`{}`)}
	out := Output{
		Devices: []Device{{Fingerprint: "hk:watch"}, {Fingerprint: "hk:band"}},
		Origins: []Origin{{Key: "com.apple.health.synthetic", Native: true}, {Key: "com.example.band"}},
		Measurements: []Measurement{
			{Metric: "heart_rate", Start: t1, Value: 61, Unit: "bpm", Device: "hk:watch", Origin: "com.apple.health.synthetic"},
			{Metric: "heart_rate", Start: t1, Value: 62, Unit: "bpm", Device: "hk:band", Origin: "com.example.band"},
			{Metric: "heart_rate", Start: t0, Value: 63, Unit: "bpm", Device: "hk:band", Origin: "com.example.band"},
			{Metric: "step_count", Start: t0, End: &t1, Value: 10, Unit: "count"},
		},
		Sleep:      []SleepSession{{Start: t0, End: t1, Origin: "com.example.band"}},
		Events:     []Event{{Code: "ecg_recording", Start: t0, Origin: "com.example.band", FileSHA256: file.SHA256}},
		Files:      []File{file},
		Tombstones: []Key{{RecordType: "HKQuantityTypeIdentifierHeartRate", ExternalID: "SYNTHETIC"}},
	}
	ignore := func(origin string) bool { return origin == "com.example.band" }

	kept, ignored := DropOrigins(out, ignore)

	if len(kept.Measurements) != 2 || kept.Measurements[0].Value != 61 || kept.Measurements[1].Metric != "step_count" {
		t.Fatalf("kept measurements %+v: the ignored origin's must go, the rest stay", kept.Measurements)
	}
	if len(kept.Sleep) != 0 || len(kept.Events) != 0 || len(kept.Files) != 0 {
		t.Fatalf("the ignored origin's sleep, events and their files must go: %+v", kept)
	}
	if len(kept.Origins) != 2 || len(kept.Tombstones) != 1 {
		t.Fatal("origins and tombstones stay")
	}
	if len(kept.Devices) != 1 || kept.Devices[0].Fingerprint != "hk:watch" {
		t.Fatalf("only devices of kept records stay: %+v", kept.Devices)
	}
	want := []Ignored{
		{Origin: "com.example.band", Kind: "event", Code: "ecg_recording", Records: 1, First: t0, Last: t0},
		{Origin: "com.example.band", Kind: "metric", Code: "heart_rate", Records: 2, First: t0, Last: t1},
		{Origin: "com.example.band", Kind: "sleep", Code: "sleep", Records: 1, First: t0, Last: t1},
	}
	if len(ignored) != len(want) {
		t.Fatalf("ignored %+v, want %+v", ignored, want)
	}
	for i := range want {
		if ignored[i] != want[i] {
			t.Fatalf("ignored[%d] = %+v, want %+v", i, ignored[i], want[i])
		}
	}
	if len(out.Measurements) != 4 {
		t.Fatal("the input output must not change")
	}

	same, none := DropOrigins(out, func(string) bool { return false })
	if none != nil || len(same.Measurements) != 4 || len(same.Devices) != 2 || len(same.Files) != 1 {
		t.Fatal("nothing ignored leaves the output as it is")
	}
}

func TestPageType(t *testing.T) {
	t.Parallel()
	if got := pageType("HKQuantityTypeIdentifierHeartRate:abc"); got != "HKQuantityTypeIdentifierHeartRate" {
		t.Fatal(got)
	}
	if got := pageType("nokey"); got != "nokey" {
		t.Fatal(got)
	}
}
