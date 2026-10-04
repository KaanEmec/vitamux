package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"syscall"
	"time"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/httpx"
)

// Limits of api/connector-sidecar.v1.yaml, enforced by the core.
const (
	MaxMessageBytes = 1 << 20  // describe and auth responses
	MaxPageBytes    = 64 << 20 // one fetch response
	MaxLineBytes    = 36 << 20 // one fetch line (a 25 MiB binary body, base64-encoded)

	callTimeout  = 30 * time.Second // describe and auth
	fetchTimeout = 5 * time.Minute

	defaultRetryAfter = time.Minute    // rate_limited without a usable delay
	maxRetryAfter     = 24 * time.Hour // as connectors.HTTPClient
)

// limits are the client's bounds; tests shrink them.
type limits struct {
	call, fetch         time.Duration
	message, page, line int64
}

var defaultLimits = limits{call: callTimeout, fetch: fetchTimeout, message: MaxMessageBytes, page: MaxPageBytes, line: MaxLineBytes}

// errNotPrivate refuses a sidecar address outside loopback, private and link-local ranges.
var errNotPrivate = errors.New("sidecar address is not loopback, private or link-local")

// client speaks the protocol to one sidecar: bearer secret, timeouts, size caps, and a dialer
// that connects to private addresses only, checked on the resolved address of every connection.
type client struct {
	base   *url.URL
	secret string
	call   *httpx.Client // describe and auth
	fetch  *httpx.Client
	line   int64
}

func newClient(base *url.URL, secret string, l limits) *client {
	tr := &http.Transport{
		Proxy: nil, // a proxy would dial for us and bypass the address check
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: privateOnly,
		}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        10,
		IdleConnTimeout:     90 * time.Second,
	}
	return &client{
		base: base, secret: secret, line: l.line,
		call:  httpx.New(httpx.Options{Timeout: l.call, MaxResponseBytes: l.message, Transport: tr}),
		fetch: httpx.New(httpx.Options{Timeout: l.fetch, MaxResponseBytes: l.page, Transport: tr}),
	}
}

// privateOnly is the dialer's Control hook: it sees the resolved address, so a name that
// resolves (or later re-resolves) to a public address is refused before connecting.
func privateOnly(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return errNotPrivate
	}
	if a := ap.Addr().Unmap(); !a.IsLoopback() && !a.IsPrivate() && !a.IsLinkLocalUnicast() {
		return errNotPrivate
	}
	return nil
}

// do POSTs in (GET when in is nil) to path and returns the 200 response. Any other status is
// the typed error of its problem body. The caller closes the body.
func (c *client) do(ctx context.Context, hc *httpx.Client, path string, in any) (*http.Response, error) {
	method, body := http.MethodGet, io.Reader(nil)
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("%w: sidecar request: %w", connectors.ErrPermanent, err)
		}
		method, body = http.MethodPost, bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.JoinPath(path).String(), body)
	if err != nil {
		return nil, fmt.Errorf("%w: sidecar request: %w", connectors.ErrPermanent, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := hc.Do(req) // httpx errors name only scheme, host and path
	if errors.Is(err, httpx.ErrResponseTooLarge) {
		return nil, violation("response over the size limit")
	} else if err != nil {
		return nil, fmt.Errorf("%w: sidecar: %w", connectors.ErrTransient, err)
	}
	if res.Header.Get(ProtocolHeader) != Protocol {
		_ = res.Body.Close()
		return nil, violation("response without " + ProtocolHeader + ": " + Protocol)
	}
	if res.StatusCode == http.StatusOK {
		return res, nil
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, MaxMessageBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: sidecar: reading problem: %w", connectors.ErrTransient, err)
	}
	return nil, withRetryAfter(DecodeProblem(b), res.Header.Get("Retry-After"))
}

// message calls a describe or auth endpoint and returns the 200 body.
func (c *client) message(ctx context.Context, path string, in any) ([]byte, error) {
	res, err := c.do(ctx, c.call, path, in)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if errors.Is(err, httpx.ErrResponseTooLarge) {
		return nil, violation("response over the size limit")
	} else if err != nil {
		return nil, fmt.Errorf("%w: sidecar: reading response: %w", connectors.ErrTransient, err)
	}
	return b, nil
}

// withRetryAfter completes a rate_limited error without retry_after_s from the Retry-After
// header (delta-seconds), else a minute; at most a day.
func withRetryAfter(err error, header string) error {
	var rl *connectors.RateLimitedError
	if !errors.As(err, &rl) {
		return err
	}
	if rl.RetryAfter <= 0 {
		if s, perr := strconv.ParseInt(header, 10, 64); perr == nil && s > 0 {
			rl.RetryAfter = time.Duration(min(s, int64(maxRetryAfter/time.Second))) * time.Second
		} else {
			rl.RetryAfter = defaultRetryAfter
		}
	}
	rl.RetryAfter = min(rl.RetryAfter, maxRetryAfter)
	return err
}
