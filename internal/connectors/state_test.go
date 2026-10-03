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

// FuzzStateVerify feeds arbitrary states and bindings to the verifier. The key is fixed so
// the seeds are real states. Invariants: no panic; errors are ErrAuthState; a state that
// verifies is exactly the one Sign makes for that id and binding; a minted state verifies
// under its binding only, and not after any single-byte change.
func FuzzStateVerify(f *testing.F) {
	s := NewStateSigner([]byte("synthetic-signing-key-0123456789"))
	id := uuid.MustParse("0192f0a0-0000-7000-8000-000000000000")
	f.Add(s.Sign(id, "browser-a"), "browser-a", uint16(0))
	f.Add(s.Sign(id, "browser-a"), "browser-b", uint16(63))
	f.Add("", "", uint16(1))
	f.Fuzz(func(t *testing.T, state, binding string, flip uint16) {
		got, err := s.Verify(state, binding)
		if err == nil {
			if again := s.Sign(got, binding); again != state {
				t.Fatalf("verified state is not the canonical string for its id")
			}
		} else if !errors.Is(err, ErrAuthState) {
			t.Fatalf("unexpected error type: %v", err)
		}

		minted := s.Sign(id, binding)
		if got, err := s.Verify(minted, binding); (binding == "") != (err != nil) || (err == nil && got != id) {
			t.Fatalf("minted state: %v %v", got, err)
		}
		if binding == "" {
			return
		}
		if _, err := s.Verify(minted, binding+"x"); err == nil {
			t.Fatal("state accepted under another binding")
		}
		b := []byte(minted)
		b[int(flip)%len(b)] ^= 1
		if _, err := s.Verify(string(b), binding); err == nil {
			t.Fatal("tampered state accepted")
		}
	})
}
