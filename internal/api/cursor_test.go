package api

import (
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/KaanEmec/vitamux/internal/crypto"
)

func testKeyring(t *testing.T) *crypto.Keyring {
	t.Helper()
	path := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.WriteKeyFile(path); err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return kr
}

func TestCursorRoundTrip(t *testing.T) {
	c, err := newCursorCodec(testKeyring(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []cursor{
		{Key: "2026-09-14T06:00:00.123456Z", ID: "9182736"},
		{Key: "", ID: "0192f0a0-0000-7000-8000-000000000000"},
		{Key: "näme with \"quotes\" | and / slashes", ID: strings.Repeat("9", 19)},
	} {
		s := c.encode("listMeasurements|metric=steps", p)
		if strings.ContainsAny(s, "+/=") {
			t.Errorf("cursor %q is not unpadded base64url", s)
		}
		got, err := c.decode("listMeasurements|metric=steps", s)
		if err != nil || got != p {
			t.Errorf("round trip %+v: got %+v, %v", p, got, err)
		}
	}
}

func TestCursorRejectsTampering(t *testing.T) {
	kr := testKeyring(t)
	c, _ := newCursorCodec(kr)
	const q = "listMeasurements|metric=steps"
	s := c.encode(q, cursor{Key: "2026-09-14T06:00:00Z", ID: "42"})
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	for i := range raw { // flipping any bit of version, payload or MAC breaks it
		bad := append([]byte(nil), raw...)
		bad[i] ^= 0x01
		if _, err := c.decode(q, base64.RawURLEncoding.EncodeToString(bad)); !errors.Is(err, errInvalidCursor) {
			t.Errorf("byte %d flipped: %v", i, err)
		}
	}

	other, _ := newCursorCodec(testKeyring(t))
	random, _ := newCursorCodec(nil)
	for name, f := range map[string]func() error{
		"other query":  func() error { _, err := c.decode(q+"|provider=garmin", s); return err },
		"other key":    func() error { _, err := other.decode(q, s); return err },
		"random key":   func() error { _, err := random.decode(q, s); return err },
		"empty":        func() error { _, err := c.decode(q, ""); return err },
		"not base64":   func() error { _, err := c.decode(q, "!!!"); return err },
		"too short":    func() error { _, err := c.decode(q, "AQ"); return err },
		"truncated":    func() error { _, err := c.decode(q, s[:len(s)-2]); return err },
		"too long":     func() error { _, err := c.decode(q, strings.Repeat("A", maxCursorLen+1)); return err },
		"padded input": func() error { _, err := c.decode(q, s+"=="); return err },
	} {
		if err := f(); !errors.Is(err, errInvalidCursor) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCursorKeyIsStable(t *testing.T) {
	kr := testKeyring(t)
	a, _ := newCursorCodec(kr)
	b, _ := newCursorCodec(kr) // e.g. after a restart with the same master key
	p := cursor{Key: "k", ID: "1"}
	if got, err := b.decode("q", a.encode("q", p)); err != nil || got != p {
		t.Fatalf("same keyring: %+v, %v", got, err)
	}
	csrf, _ := kr.PurposeKey(crypto.SessionSigning)
	if string(a.key) == string(csrf) {
		t.Fatal("cursor key equals the session-signing key")
	}
}

func TestPageLimit(t *testing.T) {
	n := func(i int) *int { return &i }
	for _, tc := range []struct {
		in    *int
		upper int
		want  int
		ok    bool
	}{
		{nil, maxPageLimit, defaultPageLimit, true},
		{nil, 100, 100, true},
		{n(1), maxPageLimit, 1, true},
		{n(maxPageLimit), maxPageLimit, maxPageLimit, true},
		{n(0), maxPageLimit, 0, false},
		{n(-5), maxPageLimit, 0, false},
		{n(101), 100, 0, false},
	} {
		got, err := pageLimit(tc.in, tc.upper)
		if got != tc.want || (err == nil) != tc.ok {
			t.Errorf("pageLimit(%v, %d) = %d, %v", tc.in, tc.upper, got, err)
		}
	}
}

// FuzzCursorDecode feeds arbitrary cursor strings and positions to the codec. CI runs it
// briefly and nightly for longer (docs/plan/E13-hardening/J13.2-fuzzing-limits.md). The key is
// fixed so the seeds are real cursors. Invariants: no panic; a cursor that decodes encodes
// back to the same string; a minted cursor decodes to its position under its query only, and
// not after any single-byte change.
func FuzzCursorDecode(f *testing.F) {
	c := cursorCodec{key: []byte("synthetic fuzz key, 32 bytes....")}
	for _, p := range []cursor{{Key: "2026-09-14T06:00:00.123456Z", ID: "9182736"}, {}, {Key: "näme \"q\"", ID: "0192f0a0"}} {
		f.Add("listMeasurements|metric=steps", c.encode("listMeasurements|metric=steps", p), p.Key, p.ID, uint16(0))
	}
	f.Add("", "", "", "", uint16(7))
	f.Fuzz(func(t *testing.T, query, s, key, id string, flip uint16) {
		if got, err := c.decode(query, s); err == nil {
			if again := c.encode(query, got); again != s {
				t.Fatalf("decoded cursor re-encodes to a different string")
			}
		} else if !errors.Is(err, errInvalidCursor) {
			t.Fatalf("unexpected error type: %v", err)
		}

		want := cursor{Key: key, ID: id}
		minted := c.encode(query, want)
		got, err := c.decode(query, minted)
		switch {
		case len(minted) > maxCursorLen:
			if err == nil {
				t.Fatal("oversized cursor accepted")
			}
		case err != nil:
			t.Fatalf("minted cursor rejected: %v", err)
		case utf8.ValidString(key) && utf8.ValidString(id) && got != want:
			t.Fatalf("round trip changed the position")
		}
		if _, err := c.decode(query+"x", minted); err == nil {
			t.Fatal("cursor accepted under another query")
		}
		if b := []byte(minted); len(b) > 0 {
			b[int(flip)%len(b)] ^= 1
			if _, err := c.decode(query, string(b)); err == nil {
				t.Fatal("tampered cursor accepted")
			}
		}
	})
}
