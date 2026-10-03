package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"

	"github.com/KaanEmec/vitamux/internal/crypto"
)

// cursor is a page position: the sort key and id of the last row returned. Handlers format
// both as text (e.g. an RFC 3339 timestamp and a decimal id) and keep the order stable by
// sorting on (key, id).
type cursor struct {
	Key string `json:"k"`
	ID  string `json:"i"`
}

// errInvalidCursor means a cursor was malformed, tampered with, minted for another query,
// or signed with a key that has since rotated. Owner handlers may return it as is; it
// answers 422 validation_failed.
var errInvalidCursor = errors.New("invalid or expired cursor")

const (
	cursorVersion = 1
	cursorMACSize = 16
	maxCursorLen  = 1024 // encoded
)

// cursorCodec makes opaque, HMAC-protected cursors (docs/architecture/api.md#conventions).
// The MAC covers a query string the caller chooses (the operation and its filters), so a
// cursor only resumes the query that produced it.
type cursorCodec struct{ key []byte }

// newCursorCodec derives the cursor key from the session-signing key, domain-separated so
// cursors and CSRF tokens never share a MAC key. Without a keyring (no master key: every
// owner route answers 401 anyway) it uses a random per-process key.
func newCursorCodec(keys *crypto.Keyring) (cursorCodec, error) {
	var base []byte
	if keys == nil {
		base = make([]byte, 32)
		_, _ = rand.Read(base)
	} else {
		k, err := keys.PurposeKey(crypto.SessionSigning)
		if err != nil {
			return cursorCodec{}, err
		}
		base = k
	}
	m := hmac.New(sha256.New, base)
	m.Write([]byte("vitamux/api-cursor/v1"))
	return cursorCodec{key: m.Sum(nil)}, nil
}

// encode returns the base64url cursor for p within query.
func (c cursorCodec) encode(query string, p cursor) string {
	body, _ := json.Marshal(p) // two strings: cannot fail
	payload := append([]byte{cursorVersion}, body...)
	return base64.RawURLEncoding.EncodeToString(append(payload, c.mac(query, payload)...))
}

// decode verifies s against query and returns its position, or errInvalidCursor.
func (c cursorCodec) decode(query, s string) (cursor, error) {
	if len(s) > maxCursorLen {
		return cursor{}, errInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) < 1+cursorMACSize {
		return cursor{}, errInvalidCursor
	}
	payload, sum := raw[:len(raw)-cursorMACSize], raw[len(raw)-cursorMACSize:]
	if !hmac.Equal(sum, c.mac(query, payload)) || payload[0] != cursorVersion {
		return cursor{}, errInvalidCursor
	}
	var p cursor
	if err := json.Unmarshal(payload[1:], &p); err != nil {
		return cursor{}, errInvalidCursor
	}
	return p, nil
}

// mac is the truncated HMAC-SHA256 of the length-prefixed query followed by the payload.
func (c cursorCodec) mac(query string, payload []byte) []byte {
	m := hmac.New(sha256.New, c.key)
	m.Write(binary.AppendUvarint(nil, uint64(len(query))))
	m.Write([]byte(query))
	m.Write(payload)
	return m.Sum(nil)[:cursorMACSize]
}

// Page sizes from the spec's Limit parameter.
const (
	defaultPageLimit = 500
	maxPageLimit     = 10_000
)

// pageLimit applies the default to an optional limit parameter and rejects values outside
// 1..upper, where upper is the endpoint's cap (at most maxPageLimit).
func pageLimit(limit *int, upper int) (int, error) {
	if limit == nil {
		return min(defaultPageLimit, upper), nil
	}
	if *limit < 1 || *limit > upper {
		return 0, problemErr(CodeValidationFailed, "limit is out of range",
			FieldError{Pointer: "/limit", Detail: "must be between 1 and the endpoint maximum"})
	}
	return *limit, nil
}
