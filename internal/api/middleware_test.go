package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/KaanEmec/vitamux/internal/obs"
)

const sentinel = "SENTINEL-VALUE-0451"

func newUITestFS() fstest.MapFS {
	return fstest.MapFS{"index.html": {Data: []byte(sampleIndex)}}
}

func serve(t *testing.T, h http.Handler, req *http.Request) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func request(t *testing.T, method, target string, body io.Reader) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, body)
	req.RemoteAddr = "203.0.113.9:4000"
	return req
}

func decodeProblem(t *testing.T, res *http.Response) problem {
	t.Helper()
	if ct := res.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type %q, want problem+json", ct)
	}
	var p problem
	if err := json.NewDecoder(res.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.Status != res.StatusCode || p.Code == "" || p.Title == "" || p.Type == "" {
		t.Fatalf("incomplete problem: %+v (http %d)", p, res.StatusCode)
	}
	return p
}

// echoMux has routes that exercise the middleware: body reads, panics, and a client echo.
func echoMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/echo", func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			writeBodyError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/v1/panic", func(http.ResponseWriter, *http.Request) {
		panic(errors.New("boom https://x.test/?token=" + sentinel))
	})
	mux.HandleFunc("GET /whoami", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s %s", ClientAddr(r.Context()), Scheme(r.Context()))
	})
	mux.HandleFunc("GET /hook/{token}", func(w http.ResponseWriter, _ *http.Request) {})
	return mux
}

func TestSecurityHeadersSnapshot(t *testing.T) {
	want := map[string]string{
		"X-Content-Type-Options":     "nosniff",
		"Referrer-Policy":            "no-referrer",
		"X-Frame-Options":            "DENY",
		"Cross-Origin-Opener-Policy": "same-origin",
		"Permissions-Policy":         "camera=(), microphone=(), geolocation=(), payment=(), usb=()",
	}
	const apiCSP = "default-src 'none'; frame-ancestors 'none'"
	const spaCSP = "default-src 'self'; script-src 'self' 'sha256-AAA='; frame-ancestors 'none'"

	cases := []struct {
		name, path string
		hsts       bool
		csp        string
	}{
		{"healthz", "/healthz", false, apiCSP},
		{"unknown api route", "/api/v1/nope", false, apiCSP},
		{"spa shell", "/rules", false, spaCSP},
		{"spa asset 404", "/missing.js", false, apiCSP},
		{"hsts on https", "/healthz", true, apiCSP},
	}
	for _, c := range cases {
		h, err := NewHandler(slog.New(slog.DiscardHandler), newUITestFS(), Options{HSTS: c.hsts})
		if err != nil {
			t.Fatal(err)
		}
		res := serve(t, h, request(t, http.MethodGet, c.path, nil))
		for k, v := range want {
			if got := res.Header.Get(k); got != v {
				t.Errorf("%s: %s = %q, want %q", c.name, k, got, v)
			}
		}
		if got := res.Header.Get("Content-Security-Policy"); got != c.csp {
			t.Errorf("%s: CSP = %q, want %q", c.name, got, c.csp)
		}
		wantHSTS := ""
		if c.hsts {
			wantHSTS = "max-age=31536000"
		}
		if got := res.Header.Get("Strict-Transport-Security"); got != wantHSTS {
			t.Errorf("%s: HSTS = %q, want %q", c.name, got, wantHSTS)
		}
		if res.Header.Get("X-Request-Id") == "" {
			t.Errorf("%s: missing X-Request-Id", c.name)
		}
	}
}

func TestUnknownAPIRouteIsProblemJSON(t *testing.T) {
	res := serve(t, newTestHandler(t), request(t, http.MethodGet, "/api/v1/nope", nil))
	p := decodeProblem(t, res)
	if res.StatusCode != 404 || p.Code != CodeNotFound || p.RequestID != res.Header.Get("X-Request-Id") {
		t.Fatalf("got %d %+v", res.StatusCode, p)
	}
}

func TestOversizedBodyIs413(t *testing.T) {
	h := middleware(slog.New(slog.DiscardHandler), Options{}, echoMux())

	// Declared length: rejected before the handler runs.
	res := serve(t, h, request(t, http.MethodPost, "/api/v1/echo", strings.NewReader(strings.Repeat("x", 1<<20+1))))
	if res.StatusCode != 413 || decodeProblem(t, res).Code != CodePayloadTooLarge {
		t.Fatalf("declared: got %d", res.StatusCode)
	}

	// Undeclared (chunked) length: caught while the handler reads.
	req := request(t, http.MethodPost, "/api/v1/echo", io.NopCloser(strings.NewReader(strings.Repeat("x", 1<<20+1))))
	req.ContentLength = -1
	res = serve(t, h, req)
	if res.StatusCode != 413 || decodeProblem(t, res).Code != CodePayloadTooLarge {
		t.Fatalf("chunked: got %d", res.StatusCode)
	}

	if res := serve(t, h, request(t, http.MethodPost, "/api/v1/echo", strings.NewReader(strings.Repeat("x", 1<<20)))); res.StatusCode != 204 {
		t.Fatalf("body at the limit must pass, got %d", res.StatusCode)
	}
}

func TestBodyLimitClasses(t *testing.T) {
	for path, want := range map[string]int64{
		"/api/v1/connections":               1 * miB,
		"/api/v1/documents":                 25 * miB,
		"/api/ingest/v1/batches":            10 * miB,
		"/api/ingest/v1/batches/b1/blobs":   25 * miB,
		"/webhooks/withings/abc":            64 * kiB,
		"/oauth/withings/callback":          64 * kiB,
		"/api/ingest/v1/batches/blobs/more": 10 * miB,
	} {
		if got := bodyLimit(path); got != want {
			t.Errorf("%s: limit %d, want %d", path, got, want)
		}
	}
}

func TestTrustedProxyHandling(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	cases := []struct {
		name, peer, xff, proto string
		trusted                []netip.Prefix
		want                   string
	}{
		{"untrusted peer ignores XFF and proto", "203.0.113.9:4000", "198.51.100.1", "https", trusted, "203.0.113.9 http"},
		{"no trusted proxies configured", "10.1.2.3:4000", "198.51.100.1", "https", nil, "10.1.2.3 http"},
		{"trusted peer", "10.1.2.3:4000", "198.51.100.1", "https", trusted, "198.51.100.1 https"},
		{"client-forged leftmost hop is skipped", "10.1.2.3:4000", "6.6.6.6, 198.51.100.1, 10.9.9.9", "https", trusted, "198.51.100.1 https"},
		{"garbage XFF falls back to peer", "10.1.2.3:4000", "not-an-ip", "gopher", trusted, "10.1.2.3 http"},
		{"ipv6 mapped peer", "[::ffff:10.1.2.3]:4000", "2001:db8::1", "http", trusted, "2001:db8::1 http"},
	}
	for _, c := range cases {
		h := middleware(slog.New(slog.DiscardHandler), Options{TrustedProxies: c.trusted}, echoMux())
		req := request(t, http.MethodGet, "/whoami", nil)
		req.RemoteAddr = c.peer
		req.Header.Set("X-Forwarded-For", c.xff)
		req.Header.Set("X-Forwarded-Proto", c.proto)
		res := serve(t, h, req)
		if got, _ := io.ReadAll(res.Body); string(got) != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPanicRecovery(t *testing.T) {
	var logs bytes.Buffer
	log := obs.NewLogger(&logs, slog.LevelDebug)
	h := middleware(log, Options{HSTS: true}, echoMux())
	res := serve(t, h, request(t, http.MethodGet, "/api/v1/panic", nil))
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 500 || res.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("got %d %s", res.StatusCode, body)
	}
	if res.Header.Get("X-Frame-Options") != "DENY" || res.Header.Get("Strict-Transport-Security") == "" {
		t.Fatal("security headers missing on panic response")
	}
	if strings.Contains(string(body), "boom") || strings.Contains(logs.String(), sentinel) {
		t.Fatalf("panic text leaked.\nbody: %s\nlogs: %s", body, logs.String())
	}
	if !strings.Contains(logs.String(), "panic in handler") || !strings.Contains(logs.String(), `"status":500`) {
		t.Fatalf("panic and 500 access line should be logged: %s", logs.String())
	}
}

func TestRequestID(t *testing.T) {
	h := middleware(slog.New(slog.DiscardHandler), Options{}, echoMux())
	req := request(t, http.MethodGet, "/whoami", nil)
	req.Header.Set("X-Request-Id", "proxy-req-0001")
	if got := serve(t, h, req).Header.Get("X-Request-Id"); got != "proxy-req-0001" {
		t.Fatalf("valid incoming id should be kept, got %q", got)
	}
	req = request(t, http.MethodGet, "/whoami", nil)
	req.Header.Set("X-Request-Id", "bad id\nforged-log-line")
	if got := serve(t, h, req).Header.Get("X-Request-Id"); len(got) != 24 || strings.ContainsAny(got, " \n") {
		t.Fatalf("invalid incoming id must be replaced, got %q", got)
	}
}

func TestAccessLogHasNoQueryHeadersOrBodies(t *testing.T) {
	var logs bytes.Buffer
	log := obs.NewLogger(&logs, slog.LevelDebug)
	h := middleware(log, Options{}, echoMux())

	req := request(t, http.MethodPost, "/api/v1/echo?code="+sentinel, strings.NewReader("body-"+sentinel))
	req.Header.Set("Authorization", "Bearer "+sentinel)
	req.Header.Set("Cookie", "s="+sentinel)
	serve(t, h, req)
	serve(t, h, request(t, http.MethodGet, "/hook/"+sentinel, nil))
	serve(t, h, request(t, http.MethodGet, "/unrouted/"+sentinel+"?a=b", nil))

	out := logs.String()
	if strings.Contains(out, sentinel) {
		t.Fatalf("sentinel leaked:\n%s", out)
	}
	for _, want := range []string{`"route":"POST /api/v1/echo"`, `"route":"GET /hook/{token}"`, `"route":"unmatched"`, `"status":204`, `"request_id":`, `"duration_ms":`, `"bytes":`, `"method":"POST"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
}

func TestReadyz(t *testing.T) {
	var logs bytes.Buffer
	log := obs.NewLogger(&logs, slog.LevelDebug)
	ok := func(context.Context) error { return nil }
	leaky := func(context.Context) error { return errors.New("dial postgres://u:" + sentinel + "@db/x") }
	hung := func(ctx context.Context) error { <-ctx.Done(); time.Sleep(time.Second); return nil }

	get := func(checks ...ReadyCheck) (*http.Response, string) {
		h, err := NewHandler(log, newUITestFS(), Options{ReadyChecks: checks})
		if err != nil {
			t.Fatal(err)
		}
		res := serve(t, h, request(t, http.MethodGet, "/readyz", nil))
		b, _ := io.ReadAll(res.Body)
		return res, string(b)
	}

	res, body := get()
	if res.StatusCode != 200 || !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("no checks: %d %s", res.StatusCode, body)
	}
	res, body = get(ReadyCheck{"database", ok}, ReadyCheck{"data_dir", ok})
	if res.StatusCode != 200 || !strings.Contains(body, `"database":"ok"`) || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("all ok: %d %s", res.StatusCode, body)
	}
	res, body = get(ReadyCheck{"database", ok}, ReadyCheck{"schema", leaky})
	if res.StatusCode != 503 || !strings.Contains(body, `"schema":"fail"`) || !strings.Contains(body, `"database":"ok"`) {
		t.Fatalf("one failing: %d %s", res.StatusCode, body)
	}
	if strings.Contains(body, sentinel) || strings.Contains(body, "dial") || strings.Contains(logs.String(), sentinel) {
		t.Fatalf("failure detail leaked.\nbody: %s\nlogs: %s", body, logs.String())
	}
	if !strings.Contains(logs.String(), `"check":"schema"`) {
		t.Fatalf("failure should be logged: %s", logs.String())
	}

	start := time.Now()
	res, body = get(ReadyCheck{"slow", hung})
	if res.StatusCode != 503 || !strings.Contains(body, `"slow":"fail"`) || time.Since(start) > 2*readyTimeout {
		t.Fatalf("hung check: %d %s after %s", res.StatusCode, body, time.Since(start))
	}
}

func TestProblemRegistry(t *testing.T) {
	documented := []Code{CodeValidationFailed, CodeNotFound, CodeConflict, CodeRateLimited, CodeReauthRequired,
		CodeConsentRequired, CodeUnsupportedWindow, CodeRuleWarningUnacknowledged, CodePayloadTooLarge, CodeInternal,
		CodeUnauthenticated, CodeTOTPRequired, CodeForbidden, CodeUnavailable}
	for _, c := range documented {
		if _, ok := problemKinds[c]; !ok {
			t.Errorf("code %q not registered", c)
		}
	}
	if len(problemKinds) != len(documented) {
		t.Errorf("registry has %d entries, expected %d", len(problemKinds), len(documented))
	}

	rec := httptest.NewRecorder()
	writeProblem(rec, request(t, http.MethodGet, "/", nil), "made_up_code", "x")
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), `"code":"internal_error"`) {
		t.Fatalf("unregistered code must degrade to internal_error: %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	writeProblem(rec, request(t, http.MethodGet, "/", nil), CodeValidationFailed, "bad input", FieldError{Pointer: "/start", Detail: "required"})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), `"errors":[{"pointer":"/start","detail":"required"}]`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}
