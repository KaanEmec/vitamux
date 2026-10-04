package jobs

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRetryDelay(t *testing.T) {
	base, maxDelay := 30*time.Second, 30*time.Minute
	for attempt := range int32(100) {
		want := min(maxDelay, base*time.Duration(1<<min(attempt, 20)))
		for range 50 {
			d := RetryDelay(attempt, base, maxDelay)
			if d < want/2 || d > want {
				t.Fatalf("attempt %d: delay %s outside [%s, %s]", attempt, d, want/2, want)
			}
		}
	}
}

func TestSlotAt(t *testing.T) {
	next := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		now        time.Time
		slot, then string
	}{
		{next, "10:00", "11:00"},
		{next.Add(59 * time.Minute), "10:00", "11:00"},
		{next.Add(5*time.Hour + time.Second), "15:00", "16:00"}, // missed slots coalesce
	}
	for _, c := range cases {
		slot, after := slotAt(next, time.Hour, c.now)
		if got := slot.Format("15:04"); got != c.slot || after.Format("15:04") != c.then {
			t.Errorf("now %s: slot %s next %s, want %s %s", c.now.Format("15:04:05"), got, after.Format("15:04"), c.slot, c.then)
		}
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		mode, stream       string
		interval, lookback time.Duration
		ok                 bool
	}{
		{ModeIncremental, "measures", time.Hour, 0, true},
		{ModeCorrection, "measures", 24 * time.Hour, 7 * 24 * time.Hour, true},
		{ModeCorrection, "measures", 24 * time.Hour, 0, false},
		{ModeIncremental, "measures", 30 * time.Second, 0, false},
		{ModeIncremental, "", time.Hour, 0, false},
		{"daily", "measures", time.Hour, 0, false},
		{ModeIncremental, "measures", time.Hour, -time.Hour, false},
	}
	for _, c := range cases {
		err := validate(c.mode, c.stream, c.interval, c.lookback)
		if (err == nil) != c.ok || (err != nil && !errors.Is(err, ErrInvalidSchedule)) {
			t.Errorf("%+v: got %v", c, err)
		}
	}
}

type classified struct{}

func (classified) Error() string      { return "boom" }
func (classified) ErrorClass() string { return "transient" }

func TestErrorClassAndMessage(t *testing.T) {
	if got := errorClass(fmt.Errorf("wrap: %w", classified{})); got != "transient" {
		t.Errorf("class %q", got)
	}
	if got := errorClass(Permanent(errors.New("x"))); got != "permanent" {
		t.Errorf("class %q", got)
	}
	if got := errorClass(errors.New("x")); got != "error" {
		t.Errorf("class %q", got)
	}
	msg := errorMessage(errors.New("GET https://api.example.test/v2?access_token=SENTINEL failed " + strings.Repeat("é", 400)))
	if strings.Contains(msg, "SENTINEL") || len(msg) > maxErrorMessage {
		t.Errorf("message not sanitized: %d bytes", len(msg))
	}
}

func TestBroadcastWakesAll(t *testing.T) {
	var b broadcast
	w1, w2 := b.wait(), b.wait()
	b.signal()
	for _, w := range []<-chan struct{}{w1, w2} {
		select {
		case <-w:
		default:
			t.Fatal("waiter not woken")
		}
	}
	select {
	case <-b.wait():
		t.Fatal("a new wait must block until the next signal")
	default:
	}
}
