package withings

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/connectors"
)

func TestDescriptorIsValid(t *testing.T) {
	if _, err := connectors.NewRegistry(New(Config{})); err != nil {
		t.Fatal(err)
	}
}

func TestBeginNeedsClient(t *testing.T) {
	_, err := New(Config{}).Begin(t.Context(), connectors.AuthInput{RedirectURL: "https://x.example.test/cb", State: "s"})
	if !errors.Is(err, connectors.ErrAuthUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestPlan(t *testing.T) {
	w := New(Config{})
	from, to := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)
	// An interrupted call's offset is dropped: the call restarts from the stored lastupdate.
	us, err := w.Plan(t.Context(), connectors.Conn{}, connectors.PlanRequest{Mode: connectors.ModeIncremental,
		Cursor: json.RawMessage(`{"lastupdate":100,"offset":40,"updatetime":200}`)})
	if err != nil || len(us) != 1 || string(us[0].Cursor) != `{"lastupdate":100}` || !us[0].From.IsZero() {
		t.Fatalf("incremental: %v %+v", err, us)
	}
	us, err = w.Plan(t.Context(), connectors.Conn{}, connectors.PlanRequest{Mode: connectors.ModeManual})
	if err != nil || string(us[0].Cursor) != `{"lastupdate":0}` {
		t.Fatalf("first sync: %v %+v", err, us)
	}
	us, err = w.Plan(t.Context(), connectors.Conn{}, connectors.PlanRequest{Mode: connectors.ModeBackfill, From: from, To: to})
	if err != nil || len(us) != 1 || !us[0].From.Equal(from) || !us[0].To.Equal(to) || us[0].Cursor != nil {
		t.Fatalf("backfill: %v %+v", err, us)
	}
	if _, err := w.Plan(t.Context(), connectors.Conn{}, connectors.PlanRequest{Mode: connectors.ModeCorrection}); !errors.Is(err, connectors.ErrPermanent) {
		t.Fatalf("correction without window: %v", err)
	}
	// Activity and sleep answer without an updatetime: the next lastupdate is the slot minus the overlap.
	slot := time.Date(2025, 1, 31, 10, 20, 0, 0, time.UTC)
	us, err = w.Plan(t.Context(), connectors.Conn{}, connectors.PlanRequest{Mode: connectors.ModeIncremental, Stream: StreamSleep,
		Cursor: json.RawMessage(`{"lastupdate":100}`), To: slot})
	if err != nil || string(us[0].Cursor) != `{"lastupdate":100,"updatetime":1738315200}` {
		t.Fatalf("sleep: %v %s", err, us[0].Cursor)
	}
	// Intraday widens to whole hours: from the stored start, or the last week before the first sync.
	us, err = w.Plan(t.Context(), connectors.Conn{}, connectors.PlanRequest{Mode: connectors.ModeManual, Stream: StreamIntraday, To: slot})
	if err != nil || !us[0].From.Equal(time.Date(2025, 1, 24, 10, 0, 0, 0, time.UTC)) || !us[0].To.Equal(time.Date(2025, 1, 31, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("intraday first sync: %v %+v", err, us)
	}
	us, err = w.Plan(t.Context(), connectors.Conn{}, connectors.PlanRequest{Mode: connectors.ModeIncremental, Stream: StreamIntraday,
		Cursor: json.RawMessage(`{"start":1738306800}`), To: slot})
	if err != nil || us[0].From.Unix() != 1738306800 || us[0].Cursor != nil {
		t.Fatalf("intraday: %v %+v", err, us)
	}
}

func TestDecodePage(t *testing.T) {
	ok := `{"updatetime":"1735886460","timezone":"Europe/Berlin","more":true,"offset":20,"extra":1,
		"measuregrps":[{"grpid":1,"date":2,"measures":[{"value":3,"type":1,"unit":0,"new_field":"x"}]}]}`
	p, err := decodePage([]byte(ok))
	if err != nil || p.UpdateTime != 1735886460 || !p.More || p.Offset != 20 || len(p.groups) != 1 || p.groups[0].id != 1 {
		t.Fatalf("got %v %+v", err, p)
	}
	for name, body := range map[string]string{
		"no groups":       `{"updatetime":1}`,
		"no updatetime":   `{"measuregrps":[]}`,
		"retyped grpid":   `{"updatetime":1,"measuregrps":[{"grpid":"a","date":2,"measures":[]}]}`,
		"no measures":     `{"updatetime":1,"measuregrps":[{"grpid":1,"date":2}]}`,
		"measure no unit": `{"updatetime":1,"measuregrps":[{"grpid":1,"date":2,"measures":[{"value":3,"type":1}]}]}`,
	} {
		if _, err := decodePage([]byte(body)); !errors.Is(err, errDrift) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestStatusError(t *testing.T) {
	var rl *connectors.RateLimitedError
	for status, want := range map[int]func(error) bool{
		401:  func(err error) bool { return errors.Is(err, connectors.ErrReauthRequired) },
		343:  func(err error) bool { return errors.Is(err, connectors.ErrReauthRequired) },
		601:  func(err error) bool { return errors.As(err, &rl) },
		2554: func(err error) bool { return errors.Is(err, connectors.ErrTransient) },
		503:  func(err error) bool { return errors.Is(err, connectors.ErrPermanent) },
	} {
		if err := statusError("getmeas", status); !want(err) {
			t.Errorf("status %d: %v", status, err)
		}
	}
}

func TestGroupRecordKeepsGroupBytes(t *testing.T) {
	g := json.RawMessage(`{"grpid":1,"date":2,"measures":[]}`)
	got := string(groupRecord(json.RawMessage(`"Europe/Berlin"`), g))
	if got != `{"timezone":"Europe/Berlin","measuregrp":{"grpid":1,"date":2,"measures":[]}}` {
		t.Fatal(got)
	}
	if got := string(groupRecord(nil, g)); got != `{"timezone":null,"measuregrp":{"grpid":1,"date":2,"measures":[]}}` {
		t.Fatal(got)
	}
}
