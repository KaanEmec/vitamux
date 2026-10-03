package connectors

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KaanEmec/vitamux/internal/httpx"
)

const (
	defaultRetryAfter = time.Minute    // a 429 without a usable Retry-After
	maxRetryAfter     = 24 * time.Hour // a bogus Retry-After must not block a provider for months
)

// HTTPClient is the provider client handed to connectors in Conn. It waits for the
// provider's token buckets before each call, refuses calls while the provider is blocked,
// and turns a 429 into a RateLimitedError (its body is discarded).
type HTTPClient struct {
	c       *httpx.Client
	buckets []*bucket

	mu           sync.Mutex
	blockedUntil time.Time
}

// Do sends req; see HTTPClient. Status codes other than 429 are returned as responses.
func (h *HTTPClient) Do(req *http.Request) (*http.Response, error) {
	if until, ok := h.blocked(time.Now()); ok {
		return nil, &RateLimitedError{RetryAfter: time.Until(until)}
	}
	for _, b := range h.buckets {
		if err := b.wait(req.Context()); err != nil {
			return nil, err
		}
	}
	res, err := h.c.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusTooManyRequests {
		return res, nil
	}
	now := time.Now()
	d := parseRetryAfter(res.Header.Get("Retry-After"), now)
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	_ = res.Body.Close()
	h.block(now.Add(d))
	return nil, &RateLimitedError{RetryAfter: d}
}

// block extends the in-process block; the runtime also persists it in provider_rate_state.
func (h *HTTPClient) block(until time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if until.After(h.blockedUntil) {
		h.blockedUntil = until
	}
}

func (h *HTTPClient) blocked(now time.Time) (time.Time, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.blockedUntil, h.blockedUntil.After(now)
}

// parseRetryAfter reads delta-seconds or an HTTP-date (RFC 9110 §10.2.3), clamped to
// [0, 24h]. A missing or unparsable value means one minute.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	var d time.Duration
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil && secs >= 0 {
		d = time.Duration(min(secs, int64(maxRetryAfter/time.Second))) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = max(t.Sub(now), 0)
	} else {
		d = defaultRetryAfter
	}
	return min(d, maxRetryAfter)
}

// providerClients holds one HTTPClient per provider, so all connections of a provider share
// its buckets and block.
type providerClients struct {
	base *httpx.Client
	mu   sync.Mutex
	m    map[string]*HTTPClient
}

func (p *providerClients) get(d Descriptor) *HTTPClient {
	p.mu.Lock()
	defer p.mu.Unlock()
	if h, ok := p.m[d.Provider]; ok {
		return h
	}
	h := &HTTPClient{c: p.base}
	for _, l := range d.RateLimits {
		h.buckets = append(h.buckets, newBucket(l))
	}
	p.m[d.Provider] = h
	return h
}

// bucket is a token bucket: Requests tokens refill evenly over Per, starting full.
type bucket struct {
	mu     sync.Mutex
	rate   float64 // tokens per second
	burst  float64
	tokens float64
	last   time.Time
}

func newBucket(l RateLimitSpec) *bucket {
	return &bucket{
		rate: float64(l.Requests) / l.Per.Seconds(), burst: float64(l.Requests),
		tokens: float64(l.Requests), last: time.Now(),
	}
}

// wait takes one token, sleeping until one is available or ctx ends.
func (b *bucket) wait(ctx context.Context) error {
	for {
		b.mu.Lock()
		now := time.Now()
		b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
		b.last = now
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return nil
		}
		d := time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
		b.mu.Unlock()
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}
