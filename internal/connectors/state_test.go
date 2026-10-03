package connectors

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestStateSigner(t *testing.T) {
	s := NewStateSigner([]byte("synthetic-signing-key-0123456789"))
	id := uuid.New()
	state := s.Sign(id, "browser-a")
	if got, err := s.Verify(state, "browser-a"); err != nil || got != id {
		t.Fatalf("round trip: %v", err)
	}
	other := NewStateSigner([]byte("synthetic-other-key-0123456789ab"))
	for name, check := range map[string]func() error{
		"other browser": func() error { _, err := s.Verify(state, "browser-b"); return err },
		"no binding":    func() error { _, err := s.Verify(state, ""); return err },
		"other key":     func() error { _, err := other.Verify(state, "browser-a"); return err },
		"truncated":     func() error { _, err := s.Verify(state[:len(state)-2], "browser-a"); return err },
		"garbage":       func() error { _, err := s.Verify("!!", "browser-a"); return err },
		"swapped id": func() error {
			_, err := s.Verify(s.Sign(uuid.New(), "browser-a")[:22]+state[22:], "browser-a")
			return err
		},
	} {
		if err := check(); !errors.Is(err, ErrAuthState) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
