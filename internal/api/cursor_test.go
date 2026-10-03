package api

import (
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"

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
