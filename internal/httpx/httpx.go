// Package httpx is the outbound HTTP client for provider and AI calls. It sets timeouts
// and a User-Agent, caps response sizes, and guarantees that neither its errors nor its
// logs contain query strings, URL credentials, headers or bodies.
package httpx

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/KaanEmec/vitamux/internal/obs"
	"github.com/KaanEmec/vitamux/internal/version"
)

const (
	defaultTimeout  = 30 * time.Second
	defaultMaxBytes = 16 << 20
	maxRedirects    = 5
)

// ErrResponseTooLarge is returned by reads of a response body that exceeds Options.MaxResponseBytes.
var ErrResponseTooLarge = errors.New("httpx: response body too large")

// Options configures a Client. Zero values select safe defaults.
type Options struct {
	Timeout          time.Duration // whole exchange including the body read; default 30s
	MaxResponseBytes int64         // default 16 MiB
	UserAgent        string        // default "vitamux/<version>"
	Logger           *slog.Logger  // optional; one debug line per request, never headers or bodies
	Transport        http.RoundTripper
}

// Client wraps http.Client. Use Do; its errors and response bodies are already sanitised.
type Client struct {
	hc  *http.Client
	max int64
	ua  string
	log *slog.Logger
}

// New returns a Client.
func New(o Options) *Client {
	if o.Timeout <= 0 {
		o.Timeout = defaultTimeout
	}
	if o.MaxResponseBytes <= 0 {
		o.MaxResponseBytes = defaultMaxBytes
	}
	if o.UserAgent == "" {
		o.UserAgent = "vitamux/" + version.Version
	}
	if o.Transport == nil {
		o.Transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: o.Timeout,
			MaxIdleConns:          20,
			IdleConnTimeout:       90 * time.Second,
			ForceAttemptHTTP2:     true,
		}
	}
	return &Client{
		hc:  &http.Client{Timeout: o.Timeout, Transport: o.Transport, CheckRedirect: checkRedirect},
		max: o.MaxResponseBytes, ua: o.UserAgent, log: o.Logger,
	}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	switch {
	case len(via) >= maxRedirects:
		return errors.New("too many redirects")
	case via[0].URL.Scheme == "https" && req.URL.Scheme != "https":
		return errors.New("redirect from https to http refused")
	}
	return nil
}

// Do sends req. The response body fails with ErrResponseTooLarge past the size cap;
// the caller must close it. Status codes are not treated as errors.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", c.ua)
	}
	start := time.Now()
	res, err := c.hc.Do(req)
	if err != nil {
		err = sanitize(req, err)
		c.logCall(req, 0, start, err)
		return nil, err
	}
	c.logCall(req, res.StatusCode, start, nil)
	if res.ContentLength > c.max {
		_ = res.Body.Close()
		return nil, &Error{Method: req.Method, Target: target(req.URL), Err: ErrResponseTooLarge}
	}
	res.Body = &cappedBody{rc: res.Body, left: c.max}
	return res, nil
}

func (c *Client) logCall(req *http.Request, status int, start time.Time, err error) {
	if c.log == nil {
		return
	}
	attrs := []any{"method", req.Method, "target", target(req.URL), "status", status, "duration_ms", time.Since(start).Milliseconds()}
	if err != nil {
		attrs = append(attrs, "err", err)
	}
	c.log.Debug("outbound http", attrs...)
}

// Error is a failed request. Its text carries only the method, scheme, host and path.
// Unwrap exposes the cause so errors.Is(err, context.DeadlineExceeded) keeps working.
type Error struct {
	Method string
	Target string
	Err    error
	msg    string
}

func (e *Error) Error() string {
	if e.msg != "" {
		return fmt.Sprintf("%s %s: %s", e.Method, e.Target, e.msg)
	}
	return fmt.Sprintf("%s %s: %v", e.Method, e.Target, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// sanitize replaces *url.Error, which embeds the full URL (query and userinfo included),
// with an Error that names only scheme://host/path.
func sanitize(req *http.Request, err error) error {
	cause := err
	if ue := (*url.Error)(nil); errors.As(err, &ue) {
		cause = ue.Err
	}
	return &Error{Method: req.Method, Target: target(req.URL), Err: cause, msg: obs.RedactString(cause.Error())}
}

// target renders scheme://host/path. Do not put secrets in paths; use headers.
func target(u *url.URL) string {
	if u == nil {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

type cappedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.left < 0 {
		return 0, ErrResponseTooLarge
	}
	// Read one byte past the cap so overflow is detected rather than silently truncated.
	if int64(len(p)) > b.left+1 {
		p = p[:b.left+1]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	if b.left < 0 {
		return n + int(b.left), ErrResponseTooLarge
	}
	return n, err
}

func (b *cappedBody) Close() error { return b.rc.Close() }
