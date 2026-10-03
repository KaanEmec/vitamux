package auth

import (
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Login throttling: after freeFailures consecutive failures for a key (a username or a client
// address), each further failure locks that key for 2^(n-freeFailures) seconds, capped at
// maxLockout; attempts during the lock are refused without checking the password. Success
// clears the key.
//
// State is in memory: Vitamux runs as one process, and a restart resetting the counters
// is acceptable next to argon2id and a long passphrase. Move it to PostgreSQL if the server
// ever runs more than one replica.
const (
	freeFailures  = 5
	maxLockout    = 15 * time.Minute
	forgetAfter   = 24 * time.Hour
	maxThrottleKV = 10000
)

type attempts struct {
	failures int
	until    time.Time
	last     time.Time
}

// throttle counts login failures per key.
type throttle struct {
	mu  sync.Mutex
	m   map[string]*attempts
	now func() time.Time
}

// newThrottle returns an empty throttle on the wall clock.
func newThrottle() *throttle { return &throttle{m: map[string]*attempts{}, now: time.Now} }

// throttleKeys returns the username and address keys of one login attempt. IPv6 clients
// are grouped by /64, which one host usually controls entirely.
func throttleKeys(username string, ip netip.Addr) []string {
	keys := []string{"user:" + strings.ToLower(strings.TrimSpace(username))}
	if ip.Is6() && !ip.Is4In6() {
		ip = netip.PrefixFrom(ip, 64).Masked().Addr()
	}
	if ip.IsValid() {
		keys = append(keys, "ip:"+ip.Unmap().String())
	}
	return keys
}

// Wait returns how long the caller must wait before the next attempt for any of keys.
func (t *throttle) Wait(keys ...string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	now, wait := t.now(), time.Duration(0)
	for _, k := range keys {
		if a := t.m[k]; a != nil && a.until.After(now) {
			wait = max(wait, a.until.Sub(now))
		}
	}
	return wait
}

// Fail records a failed attempt for each key.
func (t *throttle) Fail(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if len(t.m) >= maxThrottleKV {
		t.prune(now)
	}
	for _, k := range keys {
		a := t.m[k]
		if a == nil {
			a = &attempts{}
			t.m[k] = a
		}
		a.failures++
		a.last = now
		if n := a.failures - freeFailures; n >= 0 {
			a.until = now.Add(min(time.Second<<min(n, 20), maxLockout))
		}
	}
}

// Reset forgets the keys after a successful login.
func (t *throttle) Reset(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, k := range keys {
		delete(t.m, k)
	}
}

// prune drops idle entries; if that is not enough, it drops entries that are not locked,
// so a flood of new keys cannot grow the map without bound or evict active lockouts.
func (t *throttle) prune(now time.Time) {
	for k, a := range t.m {
		if now.Sub(a.last) > forgetAfter {
			delete(t.m, k)
		}
	}
	for k, a := range t.m {
		if len(t.m) < maxThrottleKV/2 {
			return
		}
		if !a.until.After(now) {
			delete(t.m, k)
		}
	}
}
