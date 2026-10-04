package whoop

import (
	"errors"
	"time"
	"testing"

	"github.com/KaanEmec/vitamux/internal/normalize"
	"github.com/KaanEmec/vitamux/internal/normalize/normtest"
)

func TestGoldenStreams(t *testing.T) {
	for _, n := range Normalizers() {
		t.Run(n.ID(), func(t *testing.T) {
			normtest.Golden(t, n, normalize.RawPayload{Stream: n.ID(), ContentType: "application/json", FetchedAt: time.Date(2026, 3, 2, 8, 30, 0, 0, time.UTC)},
				normalize.Env{Provider: Provider})
		})
	}
}

// TestRawOnlyStreams: every stored stream has exactly one normalizer (the strain deep dive's
// writes nothing); the unsynced journal and the removed steps have none.
func TestRawOnlyStreams(t *testing.T) {
	reg, err := normalize.NewRegistry(Normalizers()...)
	if err != nil {
		t.Fatal(err)
	}
	for stream := range versions {
		if n, err := reg.For(stream, ""); err != nil || n.ID() != stream {
			t.Errorf("%s: %v", stream, err)
		}
	}
	for _, stream := range []string{"whoop.journal", "whoop.steps"} {
		if _, err := reg.For(stream, ""); !errors.Is(err, normalize.ErrNoNormalizer) {
			t.Errorf("%s must have no normalizer: %v", stream, err)
		}
	}
}

func TestOffset(t *testing.T) {
	b := &builder{stream: StreamSleep}
	for in, want := range map[string]int16{"-05:00": -300, "+0530": 330, "+00:00": 0, "Z": 0} {
		if got, err := b.offset(in); err != nil || got != want {
			t.Errorf("offset(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "05:00", "+5:00", "+19:00", "-05:60", "EST", "+-05:00", "+05:-1", "+-5:00", "-05:+1"} {
		if _, err := b.offset(bad); err == nil {
			t.Errorf("offset(%q) should fail", bad)
		}
	}
}
