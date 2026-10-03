package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPasswordHash(t *testing.T) {
	const pw = "correct horse battery staple"
	h, err := HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") || strings.Contains(h, pw) {
		t.Fatalf("unexpected PHC string %q", h)
	}
	if ok, err := VerifyPassword(h, pw); !ok || err != nil {
		t.Fatalf("correct password: %v %v", ok, err)
	}
	if ok, _ := VerifyPassword(h, pw+"!"); ok {
		t.Fatal("wrong password accepted")
	}
	if h2, _ := HashPassword(pw); h2 == h {
		t.Fatal("salt is not random")
	}
	for _, bad := range []string{"", "$argon2i$v=19$m=65536,t=3,p=2$AAAA$AAAA", "$argon2id$v=19$m=0,t=3,p=2$AAAA$AAAA", strings.Replace(h, "$v=19$", "$v=18$", 1)} {
		if ok, err := VerifyPassword(bad, pw); ok || err == nil {
			t.Errorf("%q: ok=%v err=%v", bad, ok, err)
		}
	}
	if _, err := HashPassword("short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("short password: %v", err)
	}
}

func TestTokenRoundTrip(t *testing.T) {
	for _, prefix := range []string{PATPrefix, ClientPrefix} {
		id, token, hash, err := newToken(prefix)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(token, prefix+strings.ReplaceAll(id.String(), "-", "")+"_") {
			t.Fatalf("token %q does not embed id %s", token, id)
		}
		p, gotID, gotHash, err := ParseToken(token)
		if err != nil || p != prefix || gotID != id || string(gotHash) != string(hash) {
			t.Fatalf("parse %q: %v %v %v", token, p, gotID, err)
		}
		secret, _ := base64.RawURLEncoding.DecodeString(token[len(prefix)+33:])
		if sum := sha256.Sum256(secret); string(sum[:]) != string(hash) {
			t.Fatal("stored hash is not SHA-256 of the secret")
		}
	}
}

func TestParseTokenRejects(t *testing.T) {
	_, good, _, _ := newToken(PATPrefix)
	id := good[len(PATPrefix) : len(PATPrefix)+32]
	secret := good[len(PATPrefix)+33:]
	for name, tok := range map[string]string{
		"empty":        "",
		"prefix":       "vmx_xyz_" + id + "_" + secret,
		"upper hex":    PATPrefix + strings.ToUpper(id) + "_" + secret,
		"dashed id":    PATPrefix + uuid.New().String() + "_" + secret,
		"short secret": PATPrefix + id + "_" + secret[:42],
		"long secret":  PATPrefix + id + "_" + secret + "A",
		"bad secret":   PATPrefix + id + "_" + strings.Repeat("!", 43),
		"no separator": PATPrefix + id + "-" + secret,
	} {
		if _, _, _, err := ParseToken(tok); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestCSRF(t *testing.T) {
	s := &Service{csrfKey: []byte("0123456789abcdef0123456789abcdef")}
	tok := s.CSRFToken("session-a")
	if tok != s.CSRFToken("session-a") || tok == s.CSRFToken("session-b") {
		t.Fatal("CSRF token must be deterministic per session and differ between sessions")
	}
	if !s.CheckCSRF("session-a", tok) {
		t.Fatal("valid token rejected")
	}
	for _, c := range [][2]string{{"session-b", tok}, {"session-a", ""}, {"session-a", tok[:len(tok)-1]}, {"", s.CSRFToken("")}} {
		if s.CheckCSRF(c[0], c[1]) {
			t.Errorf("accepted %q for %q", c[1], c[0])
		}
	}
	other := &Service{csrfKey: []byte("another key, another deployment.")}
	if other.CheckCSRF("session-a", tok) {
		t.Fatal("token valid under a different key")
	}
}

func TestThrottle(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	th := newThrottle()
	th.now = func() time.Time { return now }
	keys := throttleKeys("Owner", netip.MustParseAddr("203.0.113.9"))
	for range freeFailures - 1 {
		th.Fail(keys...)
		if w := th.Wait(keys...); w != 0 {
			t.Fatalf("delay %s before %d failures", w, freeFailures)
		}
	}
	for i, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		th.Fail(keys...)
		if w := th.Wait(keys...); w != want {
			t.Fatalf("failure %d: wait %s, want %s", freeFailures+i, w, want)
		}
	}
	// The same address with another username, and the same username elsewhere, are both locked.
	if th.Wait(throttleKeys("other", netip.MustParseAddr("203.0.113.9"))...) == 0 ||
		th.Wait(throttleKeys("OWNER ", netip.MustParseAddr("198.51.100.1"))...) == 0 {
		t.Fatal("lockout must apply per username and per address")
	}
	now = now.Add(5 * time.Second)
	if w := th.Wait(keys...); w != 0 {
		t.Fatalf("still locked after the delay: %s", w)
	}
	for range 30 {
		th.Fail(keys...)
	}
	if w := th.Wait(keys...); w != maxLockout {
		t.Fatalf("cap: %s", w)
	}
	th.Reset(keys...)
	if w := th.Wait(keys...); w != 0 {
		t.Fatalf("after reset: %s", w)
	}

	a := throttleKeys("x", netip.MustParseAddr("2001:db8:1:2:aaaa::1"))
	b := throttleKeys("x", netip.MustParseAddr("2001:db8:1:2:bbbb::2"))
	if a[1] != b[1] {
		t.Fatalf("IPv6 addresses in one /64 must share a key: %v %v", a, b)
	}
}

func TestRecoveryCode(t *testing.T) {
	c, err := newRecoveryCode()
	if err != nil {
		t.Fatal(err)
	}
	if len(c) != 19 || strings.Count(c, "-") != 3 {
		t.Fatalf("format %q", c)
	}
	loose := strings.ToUpper(strings.ReplaceAll(c, "-", " "))
	if string(recoveryHash(loose)) != string(recoveryHash(c)) {
		t.Fatal("case, spaces and dashes must not matter")
	}
}

func TestPrincipalCan(t *testing.T) {
	sess := &Principal{Kind: OwnerSession}
	reader := &Principal{Kind: APIKey, Scopes: []Scope{ReadHealth}}
	admin := &Principal{Kind: APIKey, Scopes: []Scope{Admin}}
	conn := uuid.New()
	client := &Principal{Kind: Client, ConnectionID: conn}
	switch {
	case !sess.Can(WriteConfig), !reader.Can(ReadHealth), reader.Can(WriteConfig), reader.Can(Admin),
		!admin.Can(WriteConfig), client.Can(ReadHealth):
		t.Fatal("scope check")
	case !client.CanIngest(conn), client.CanIngest(uuid.New()), sess.CanIngest(conn):
		t.Fatal("ingest check")
	}
}
