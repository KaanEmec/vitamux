package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"time"
)

// reqState is created once per request by the outermost middleware and shared through the
// context, so the access log sees values that inner layers learn (the matched route).
type reqState struct {
	id       string
	clientIP netip.Addr
	scheme   string
	route    string
}

type ctxKey struct{}

func stateFrom(ctx context.Context) *reqState {
	s, _ := ctx.Value(ctxKey{}).(*reqState)
	return s
}

func requestIDFrom(ctx context.Context) string {
	if s := stateFrom(ctx); s != nil {
		return s.id
	}
	return ""
}

// ClientAddr and Scheme describe the caller as seen through trusted proxies only.
// They are empty outside a request that passed the middleware chain.
func ClientAddr(ctx context.Context) netip.Addr {
	if s := stateFrom(ctx); s != nil {
		return s.clientIP
	}
	return netip.Addr{}
}

func Scheme(ctx context.Context) string {
	if s := stateFrom(ctx); s != nil {
		return s.scheme
	}
	return ""
}

// bodyClass sets the maximum request body for the paths it matches; the first match wins.
type bodyClass struct {
	prefix, suffix string
	max            int64
	json           bool // the body is JSON that handlers decode from r.Body: nesting is capped
}

const (
	kiB = 1 << 10
	miB = 1 << 20
)

var bodyClasses = []bodyClass{
	{prefix: "/api/ingest/v1/", suffix: "/blobs", max: 25 * miB}, // raw file parts
	{prefix: "/api/v1/documents", max: 25 * miB},                 // PDF upload: 20 MiB file plus multipart framing
	{prefix: "/api/ingest/v1/", max: 10 * miB},                   // gzip batches: depth is checked after decompression (checkJSONDepth)
	{prefix: "/api/", max: 1 * miB, json: true},                  // owner JSON
	{max: 64 * kiB}, // webhooks, OAuth callbacks, everything else
}

func bodyClassOf(path string) bodyClass {
	for _, c := range bodyClasses {
		if strings.HasPrefix(path, c.prefix) && strings.HasSuffix(path, c.suffix) {
			return c
		}
	}
	return bodyClasses[len(bodyClasses)-1]
}

func bodyLimit(path string) int64 { return bodyClassOf(path).max }

// middleware wraps h, outermost first: request state and id, access log, security headers,
// panic recovery, body limits, authentication.
func middleware(log *slog.Logger, o Options, mux *http.ServeMux) http.Handler {
	inner := bodyLimits(authenticate(log, o.Auth, mux))
	inner = recoverer(log, inner)
	inner = securityHeaders(o.HSTS, inner)
	inner = accessLog(log, mux, inner)
	return requestState(o.TrustedProxies, inner)
}

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

func requestState(trusted []netip.Prefix, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !validRequestID.MatchString(id) {
			var b [12]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		ip, scheme := clientOf(r, trusted)
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), ctxKey{}, &reqState{id: id, clientIP: ip, scheme: scheme})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// clientOf returns the caller's address and scheme. X-Forwarded-For and -Proto are honoured
// only when the direct peer is a trusted proxy; the client is then the rightmost
// address that is not itself a trusted proxy.
func clientOf(r *http.Request, trusted []netip.Prefix) (netip.Addr, string) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	var peer netip.Addr
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		peer = ap.Addr().Unmap()
	}
	if !isTrusted(peer, trusted) {
		return peer, scheme
	}
	if v := lastField(r.Header.Values("X-Forwarded-Proto")); v == "http" || v == "https" {
		scheme = v
	}
	client := peer
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for _, hop := range slices.Backward(hops) {
		a, err := netip.ParseAddr(strings.TrimSpace(hop))
		if err != nil {
			break // garbage from a proxy: keep the last address we could vouch for
		}
		client = a.Unmap()
		if !isTrusted(client, trusted) {
			break
		}
	}
	return client, scheme
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if a.IsValid() && p.Contains(a) {
			return true
		}
	}
	return false
}

func lastField(values []string) string {
	parts := strings.Split(strings.Join(values, ","), ",")
	return strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
}

// statusWriter records what was sent. Unwrap lets http.ResponseController reach the real writer.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// accessLog logs one line per request: method, route pattern (never the raw path of a
// matched route, so path-embedded tokens stay out), status, duration, id and bytes.
// Query strings, headers and bodies are never logged.
func accessLog(log *slog.Logger, mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		_, stateFrom(r.Context()).route = mux.Handler(r)
		next.ServeHTTP(sw, r)

		st := stateFrom(r.Context())
		route := st.route
		if route == "" {
			route = "unmatched"
		}
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case route == "GET /healthz" || route == "GET /readyz":
			level = slog.LevelDebug
		}
		log.LogAttrs(r.Context(), level, "request",
			slog.String("request_id", st.id), slog.String("method", r.Method), slog.String("route", route),
			slog.Int("status", status), slog.Int64("duration_ms", time.Since(start).Milliseconds()), slog.Int64("bytes", sw.bytes))
	})
}

const (
	apiCSP      = "default-src 'none'; frame-ancestors 'none'"
	permissions = "camera=(), microphone=(), geolocation=(), payment=(), usb=()"
)

// securityHeaders sets the defaults on every response. The SPA handler replaces the CSP
// for index.html with the one derived from the built page.
func securityHeaders(hsts bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", permissions)
		h.Set("Content-Security-Policy", apiCSP)
		if hsts {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rec)
			}
			log.ErrorContext(r.Context(), "panic in handler", "request_id", requestIDFrom(r.Context()),
				"panic", rec, "stack", string(debug.Stack()))
			if sw, ok := w.(*statusWriter); ok && sw.status != 0 {
				return // response already started; nothing sensible left to send
			}
			writeProblem(w, r, CodeInternal, "")
		}()
		next.ServeHTTP(w, r)
	})
}

// bodyLimits rejects bodies over the route class limit: up front when Content-Length is
// declared, otherwise when the handler reads past it (see writeBodyError). JSON classes also
// cap nesting as the handler reads.
func bodyLimits(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := bodyClassOf(r.URL.Path)
		if r.ContentLength > c.max {
			writeProblem(w, r, CodePayloadTooLarge, "request body exceeds the limit for this endpoint")
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, c.max)
			if c.json {
				r.Body = &depthReader{r: r.Body}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// maxJSONDepth caps array and object nesting in request bodies. Real payloads (rule specs,
// provider records, HealthKit samples) stay far below it; encoding/json alone allows 10,000
// levels, which is cheap to send and costly for every later recursive consumer.
const maxJSONDepth = 64

// errJSONTooDeep is a body nested deeper than maxJSONDepth; writeBodyError answers 422.
var errJSONTooDeep = errors.New("JSON is nested too deeply")

// depthReader fails a read with errJSONTooDeep once the bytes passing through open more than
// maxJSONDepth levels. It tracks strings and escapes, and stops caring about malformed JSON:
// the decoder reports that.
type depthReader struct {
	r     io.ReadCloser
	depth int
	inStr bool
	esc   bool
}

func (d *depthReader) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	for _, c := range p[:n] {
		switch {
		case d.esc:
			d.esc = false
		case d.inStr:
			d.esc, d.inStr = c == '\\', c != '"'
		case c == '"':
			d.inStr = true
		case c == '{' || c == '[':
			if d.depth++; d.depth > maxJSONDepth {
				return 0, errJSONTooDeep // not n: a decoder finishes a value it already holds before it looks at the error
			}
		case (c == '}' || c == ']') && d.depth > 0:
			d.depth--
		}
	}
	return n, err
}

func (d *depthReader) Close() error { return d.r.Close() }

// checkJSONDepth applies the same cap to a body that is already in memory (gzip ingest bodies
// are only readable after decompression).
func checkJSONDepth(b []byte) error {
	_, err := io.Copy(io.Discard, &depthReader{r: io.NopCloser(bytes.NewReader(b))})
	return err
}
