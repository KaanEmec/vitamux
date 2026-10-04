package connectors

import (
	"context"
	"io"
	"net/http"
	"slices"
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
	c *httpx.Client

	mu           sync.Mutex
	limits       []RateLimitSpec // the buckets' source; a sidecar's change when it is described
	buckets      []*bucket
	blockedUntil time.Time
}

// Wait admits one provider call: a RateLimitedError while the provider is blocked, else it
// waits for the provider's token buckets. Do calls it; a connector whose calls do not go
// through Do (a sidecar, which has its own client) calls it before each one.
func (h *HTTPClient) Wait(ctx context.Context) error {
	if until, ok := h.blocked(time.Now()); ok {
		return &RateLimitedError{RetryAfter: time.Until(until)}
	}
	h.mu.Lock()
	buckets := h.buckets
	h.mu.Unlock()
	for _, b := range buckets {
		if err := b.wait(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Do sends req; see HTTPClient. Status codes other than 429 are returned as responses.
func (h *HTTPClient) Do(req *http.Request) (*http.Response, error) {
	if err := h.Wait(req.Context()); err != nil {
		return nil, err
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
// its buckets and block. A provider's buckets are rebuilt when its rate limits change (a
// sidecar first described after its placeholder was used).
type providerClients struct {
	base *httpx.Client
	mu   sync.Mutex
	m    map[string]*HTTPClient
}

func (p *providerClients) get(d Descriptor) *HTTPClient {
	p.mu.Lock()
	defer p.mu.Unlock()
	h, ok := p.m[d.Provider]
	if !ok {
		h = NewHTTPClient(p.base, d.RateLimits)
		p.m[d.Provider] = h
	}
	h.setLimits(d.RateLimits)
	return h
}

// NewHTTPClient returns a client that sends through base within limits. The runtime keeps one
// per provider; tests build their own.
func NewHTTPClient(base *httpx.Client, limits []RateLimitSpec) *HTTPClient {
	h := &HTTPClient{c: base}
	h.setLimits(limits)
	return h
}

// setLimits rebuilds the buckets when limits differ from the current ones.
func (h *HTTPClient) setLimits(limits []RateLimitSpec) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.buckets != nil && slices.Equal(h.limits, limits) {
		return
	}
	h.limits, h.buckets = slices.Clone(limits), make([]*bucket, 0, len(limits))
	for _, l := range limits {
		h.buckets = append(h.buckets, newBucket(l))
	}
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
