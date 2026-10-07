package normalize

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

type fakeNorm struct {
	id      string
	version int
	stream  string
}

func (f fakeNorm) ID() string                    { return f.id }
func (f fakeNorm) Version() int                  { return f.version }
func (f fakeNorm) Accepts(stream, _ string) bool { return stream == f.stream }
func (f fakeNorm) Normalize(context.Context, RawPayload, Env) (Output, error) {
	return Output{}, nil
}

func TestRegistry(t *testing.T) {
	a, b := fakeNorm{"b.one", 1, "s1"}, fakeNorm{"a.two", 2, "s2"}
	r, err := NewRegistry(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if r.list[0].ID() != "a.two" {
		t.Errorf("registry not sorted: %v", r.list)
	}
	if n, err := r.For("s1", ""); err != nil || n.ID() != "b.one" {
		t.Errorf("For(s1) = %v, %v", n, err)
	}
	if _, err := r.For("nope", ""); !errors.Is(err, ErrNoNormalizer) {
		t.Errorf("unknown stream: %v", err)
	}
	if n, ok := r.Get("a.two"); !ok || n.Version() != 2 {
		t.Error("Get")
	}
	if _, err := NewRegistry(a, fakeNorm{"b.one", 3, "s3"}); err == nil {
		t.Error("duplicate id accepted")
	}
	if _, err := NewRegistry(fakeNorm{"x", 0, "s"}); err == nil {
		t.Error("version 0 accepted")
	}
	amb, _ := NewRegistry(a, fakeNorm{"c", 1, "s1"})
	if _, err := amb.For("s1", ""); err == nil || errors.Is(err, ErrNoNormalizer) {
		t.Errorf("two normalizers for one stream: %v", err)
	}
}

func TestValidate(t *testing.T) {
	at := time.Date(2026, 6, 15, 7, 0, 0, 0, time.UTC)
	end := at.Add(time.Hour)
	hr := Measurement{Metric: "heart_rate", Kind: catalog.Sample, Start: at, Value: 60, Unit: "bpm"}
	with := func(f func(*Measurement)) Output {
		m := hr
		f(&m)
		return Output{Measurements: []Measurement{m}}
	}
	if err := (Output{Measurements: []Measurement{hr}}).Validate(); err != nil {
		t.Fatal(err)
	}
	for name, o := range map[string]Output{
		"unknown metric":          with(func(m *Measurement) { m.Metric = "nope" }),
		"kind not allowed":        with(func(m *Measurement) { m.Kind = catalog.DailyValue }),
		"sample with end":         with(func(m *Measurement) { m.End = &end }),
		"incompatible unit":       with(func(m *Measurement) { m.Unit = "kg" }),
		"unknown device":          with(func(m *Measurement) { m.Device = "d1" }),
		"id without type":         with(func(m *Measurement) { m.Key = Key{ExternalID: "x"} }),
		"group metric alone":      with(func(m *Measurement) { m.Metric, m.Unit = "bp_systolic", "mmHg" }),
		"bad zone":                with(func(m *Measurement) { m.Zone = Zone{TZ: "Mars/Base"} }),
		"group with other metric": {Groups: []Group{{Kind: "bp_reading", MeasuredAt: at, Components: []Measurement{hr}}}},
		"sleep backwards":         {Sleep: []SleepSession{{Start: end, End: at}}},
		"bad stage":               {Sleep: []SleepSession{{Start: at, End: end, Stages: []SleepStage{{Stage: "dozing", Start: at, End: end}}}}},
		"workout without sport":   {Workouts: []Workout{{Start: at, End: end}}},
		"tombstone without id":    {Tombstones: []Key{{RecordType: "x"}}},
	} {
		if err := o.Validate(); !errors.Is(err, ErrInvalidOutput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestDedupeKey(t *testing.T) {
	conn := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	s := newKeySource("withings", bytes.Repeat([]byte{0xab}, 32), conn)
	k := s.idKey(Key{RecordType: "measuregrp", ExternalID: "42", Component: "bp_systolic"})
	if len(k) != 16 {
		t.Fatalf("key length %d", len(k))
	}
	// The same account in another connection row gives the same key.
	if other := newKeySource("withings", bytes.Repeat([]byte{0xab}, 32), uuid.New()); !bytes.Equal(other.idKey(Key{RecordType: "measuregrp", ExternalID: "42", Component: "bp_systolic"}), k) {
		t.Error("key depends on the connection row")
	}
	// Pinned, so an accidental change to the v1 format fails here.
	want := dedupeKey("v1", "withings", "abababababababababababababababababababababababababababababababab", "measuregrp", "42", "bp_systolic")
	if !bytes.Equal(k, want) {
		t.Error("idKey format changed")
	}
	// Escaping keeps part boundaries: "a|b","c" differs from "a","b|c".
	if bytes.Equal(dedupeKey("a|b", "c"), dedupeKey("a", "b|c")) {
		t.Error("separator in a part collides")
	}
	// Natural keys use microseconds, so sub-microsecond noise does not matter, and the end is part of it.
	at := time.Date(2026, 6, 15, 7, 0, 0, 0, time.UTC)
	end := at.Add(time.Minute)
	n1 := s.naturalKey("steps", "interval", at, &end, "d", "o")
	if !bytes.Equal(n1, s.naturalKey("steps", "interval", at.Add(100*time.Nanosecond), &end, "d", "o")) {
		t.Error("sub-microsecond difference changed the key")
	}
	if bytes.Equal(n1, s.naturalKey("steps", "interval", at, nil, "d", "o")) {
		t.Error("end ignored")
	}
	// No account key: per-connection fallback.
	if bytes.Equal(newKeySource("withings", nil, conn).idKey(Key{RecordType: "r", ExternalID: "1"}),
		newKeySource("withings", nil, uuid.New()).idKey(Key{RecordType: "r", ExternalID: "1"})) {
		t.Error("connections without account key share keys")
	}
}

func TestSleepTotals(t *testing.T) {
	at := time.Date(2026, 6, 15, 22, 0, 0, 0, time.UTC)
	st := func(stage string, fromMin, toMin int) SleepStage {
		return SleepStage{stage, at.Add(time.Duration(fromMin) * time.Minute), at.Add(time.Duration(toMin) * time.Minute)}
	}
	basis, tt := sleepTotals(SleepSession{Stages: []SleepStage{st("light", 0, 60), st("deep", 60, 90), st("awake", 90, 100), st("light", 100, 160)}})
	if basis != "stages" || *tt.Light != 7200 || *tt.Deep != 1800 || *tt.REM != 0 || *tt.Awake != 600 || *tt.Asleep != 9000 || tt.Latency != nil {
		t.Errorf("staged: %s %+v", basis, tt)
	}
	// Only unspecified sleep: no deep/light/rem values rather than zeros.
	_, tt = sleepTotals(SleepSession{Stages: []SleepStage{st("asleep_unspecified", 0, 60)}})
	if tt.Deep != nil || tt.Light != nil || tt.REM != nil || *tt.Asleep != 3600 {
		t.Errorf("unspecified: %+v", tt)
	}
	// A stage no event has stays nil (awake included); the session's latency is carried over.
	lat := int32(480)
	_, tt = sleepTotals(SleepSession{Latency: &lat, Stages: []SleepStage{st("light", 0, 60), st("deep", 60, 90)}})
	if tt.Awake != nil || *tt.Light != 3600 || *tt.Latency != 480 {
		t.Errorf("no awake stage: %+v", tt)
	}
	// unknown is neither asleep nor awake; restless and out_of_bed are not asleep either.
	_, tt = sleepTotals(SleepSession{Stages: []SleepStage{st("light", 0, 60), st("unknown", 60, 70), st("restless", 70, 80), st("out_of_bed", 80, 90), st("awake", 90, 95)}})
	if *tt.Asleep != 3600 || *tt.Awake != 300 {
		t.Errorf("unknown, restless, out_of_bed: %+v", tt)
	}
	for _, stage := range []string{"unknown", "restless", "out_of_bed"} {
		o := Output{Sleep: []SleepSession{{Start: at, End: at.Add(time.Hour), Stages: []SleepStage{st(stage, 0, 60)}}}}
		if err := o.Validate(); err != nil {
			t.Errorf("stage %s is in the vocabulary: %v", stage, err)
		}
	}
	p := int32(5)
	if basis, tt := sleepTotals(SleepSession{Totals: &SleepTotals{Deep: &p}, Stages: []SleepStage{st("deep", 0, 60)}}); basis != "provider" || *tt.Deep != 5 {
		t.Errorf("provider totals: %s %+v", basis, tt)
	}
}
