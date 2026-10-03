package connectors

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"

	"github.com/google/uuid"
)

const stateLabel = "vitamux/oauth-state/v1\x00"

// ErrAuthState is a missing, forged, expired, replayed or foreign authorization state.
var ErrAuthState = errors.New("connectors: invalid or expired authorization state")

// StateSigner makes the OAuth `state` parameter: a pending-authorization id with an HMAC over
// it and a browser binding, so a state only verifies in the browser that started the flow.
// Single use and session binding come from the oauth_states row the id names. Any OAuth
// provider uses it; the key is the `session-signing` purpose key.
type StateSigner struct{ key []byte }

// NewStateSigner returns a signer over key (crypto.Keyring.PurposeKey(crypto.SessionSigning)).
func NewStateSigner(key []byte) *StateSigner { return &StateSigner{key: key} }

func (s *StateSigner) mac(id uuid.UUID, binding string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(stateLabel))
	m.Write(id[:])
	m.Write([]byte(binding))
	return m.Sum(nil)
}

// Sign returns the state for id, bound to binding (a random per-browser value).
func (s *StateSigner) Sign(id uuid.UUID, binding string) string {
	return base64.RawURLEncoding.EncodeToString(append(id[:], s.mac(id, binding)...))
}

// Verify returns the id of a state made by Sign with the same binding, or ErrAuthState.
func (s *StateSigner) Verify(state, binding string) (uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(state)
	// The decoder skips CR and LF; accept the canonical spelling only.
	if err != nil || len(raw) != 16+sha256.Size || binding == "" || base64.RawURLEncoding.EncodeToString(raw) != state {
		return uuid.Nil, ErrAuthState
	}
	id := uuid.UUID(raw[:16])
	if !hmac.Equal(raw[16:], s.mac(id, binding)) {
		return uuid.Nil, ErrAuthState
	}
	return id, nil
}
