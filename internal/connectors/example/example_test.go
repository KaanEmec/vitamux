package example

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/normalize"
	"github.com/KaanEmec/vitamux/internal/normalize/normtest"
)

func TestDescriptorValidates(t *testing.T) {
	if _, err := connectors.NewRegistry(New(Config{})); err != nil {
		t.Fatal(err)
	}
}

func TestPlan(t *testing.T) {
	ctx := context.Background()
	stored := json.RawMessage(`{"since":"2026-09-14T08:00:00Z"}`)
	for _, mode := range []string{connectors.ModeIncremental, connectors.ModeManual} {
		units, err := New(Config{}).Plan(ctx, connectors.Conn{}, connectors.PlanRequest{Mode: mode, Stream: Stream, Cursor: stored})
		if err != nil || len(units) != 1 || string(units[0].Cursor) != string(stored) {
			t.Fatalf("%s: %+v, %v", mode, units, err)
		}
	}
	for _, mode := range []string{connectors.ModeCorrection, connectors.ModeBackfill} {
		_, err := New(Config{}).Plan(ctx, connectors.Conn{}, connectors.PlanRequest{Mode: mode, Stream: Stream})
		if !errors.Is(err, connectors.ErrPermanent) {
			t.Fatalf("%s: want ErrPermanent, got %v", mode, err)
		}
	}
	_, err := New(Config{}).Plan(ctx, connectors.Conn{}, connectors.PlanRequest{Mode: connectors.ModeManual, Stream: Stream, Cursor: json.RawMessage(`[]`)})
	if !errors.Is(err, connectors.ErrPermanent) {
		t.Fatalf("unreadable cursor: want ErrPermanent, got %v", err)
	}
}

func TestDecodePage(t *testing.T) {
	p, err := decodePage([]byte(`{"server_time":"2026-09-14T08:00:00Z","extra":1,
		"samples":[{"id":"hr-1","time":"2026-09-14T07:30:00Z","bpm":61,"device":"synthetic-band-01"}],"next_page":null}`))
	if err != nil || len(p.samples) != 1 || p.samples[0].id != "hr-1" || p.nextPage != "" ||
		!p.serverTime.Equal(time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v, %v", p, err)
	}
	if string(p.samples[0].body) != `{"id":"hr-1","time":"2026-09-14T07:30:00Z","bpm":61,"device":"synthetic-band-01"}` {
		t.Fatalf("sample body not verbatim: %s", p.samples[0].body)
	}
	for name, body := range map[string]string{
		"not json":             `{"server_time":`,
		"no server_time":       `{"samples":[]}`,
		"retyped server_time":  `{"server_time":17,"samples":[]}`,
		"no samples":           `{"server_time":"2026-09-14T08:00:00Z"}`,
		"retyped samples":      `{"server_time":"2026-09-14T08:00:00Z","samples":{}}`,
		"sample without id":    `{"server_time":"2026-09-14T08:00:00Z","samples":[{"time":"2026-09-14T07:30:00Z"}]}`,
		"retyped sample id":    `{"server_time":"2026-09-14T08:00:00Z","samples":[{"id":1,"time":"2026-09-14T07:30:00Z"}]}`,
		"sample without time":  `{"server_time":"2026-09-14T08:00:00Z","samples":[{"id":"hr-1"}]}`,
		"retyped next_page":    `{"server_time":"2026-09-14T08:00:00Z","samples":[],"next_page":2}`,
		"retyped sample time":  `{"server_time":"2026-09-14T08:00:00Z","samples":[{"id":"hr-1","time":false}]}`,
		"sample is not object": `{"server_time":"2026-09-14T08:00:00Z","samples":[1]}`,
	} {
		if _, err := decodePage([]byte(body)); !errors.Is(err, errDrift) {
			t.Errorf("%s: want drift, got %v", name, err)
		}
	}
}

func TestGoldenHeartRate(t *testing.T) {
	normtest.Golden(t, Normalizer{}, normalize.RawPayload{Stream: Stream, ContentType: "application/json"},
		normalize.Env{Provider: Provider})
}
