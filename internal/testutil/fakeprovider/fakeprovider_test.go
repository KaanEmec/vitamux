package fakeprovider_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/httpx"
	fp "github.com/KaanEmec/vitamux/internal/testutil/fakeprovider"
)

func do(t *testing.T, c *http.Client, method, u, body string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, u, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

// Each typed error class of docs/architecture/connectors.md#typed-errors can be scripted.
func TestScriptsEveryErrorClass(t *testing.T) {
	s := fp.New(t)
	s.Expect(
		fp.GET("/v1/ok", "synthetic-access-1", fp.JSON(200, map[string]any{"items": []int{1}})),
		fp.GET("/v1/limited", "synthetic-access-1", fp.RateLimited(90*time.Second)),
		fp.GET("/v1/limited-date", "synthetic-access-1", fp.RateLimitedAt(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))),
		fp.GET("/v1/boom", "synthetic-access-1", fp.ServerError(503)),
		fp.GET("/v1/expired", "synthetic-access-1", fp.Unauthorized()),
		fp.GET("/v1/drift", "synthetic-access-1", fp.JSON(200, map[string]any{"items": "now a string"})),
		fp.GET("/v1/broken", "synthetic-access-1", fp.Malformed()),
		fp.GET("/v1/gone", "synthetic-access-1", fp.Status(404)),
	)
	c := s.Client()
	hdr := map[string]string{"Authorization": "Bearer synthetic-access-1"}
	get := func(path string) (*http.Response, string) { return do(t, c, "GET", s.URL+path, "", hdr) }

	if res, body := get("/v1/ok"); res.StatusCode != 200 || body != `{"items":[1]}` {
		t.Fatalf("ok: %d %s", res.StatusCode, body)
	}
	if res, _ := get("/v1/limited"); res.StatusCode != 429 || res.Header.Get("Retry-After") != "90" {
		t.Fatalf("429: %d %q", res.StatusCode, res.Header.Get("Retry-After"))
	}
	if res, _ := get("/v1/limited-date"); res.StatusCode != 429 || res.Header.Get("Retry-After") != "Fri, 02 Jan 2026 03:04:05 GMT" {
		t.Fatalf("429 date: %q", res.Header.Get("Retry-After"))
	}
	if res, _ := get("/v1/boom"); res.StatusCode != 503 {
		t.Fatalf("5xx: %d", res.StatusCode)
	}
	if res, body := get("/v1/expired"); res.StatusCode != 401 || !strings.Contains(body, "invalid_token") {
		t.Fatalf("401: %d %s", res.StatusCode, body)
	}
	if _, body := get("/v1/drift"); !strings.Contains(body, "now a string") {
		t.Fatalf("drift body: %s", body)
	}
	if _, body := get("/v1/broken"); strings.HasSuffix(body, "}") {
		t.Fatalf("malformed body should be truncated: %s", body)
	}
	if res, _ := get("/v1/gone"); res.StatusCode != 404 {
		t.Fatalf("permanent: %d", res.StatusCode)
	}
	if n := len(s.Requests()); n != 8 {
		t.Fatalf("recorded %d requests", n)
	}
}

func TestDropAndDelayAreTransientNetworkFailures(t *testing.T) {
	s := fp.New(t)
	slow := fp.Step{Path: "/slow", Reply: fp.Response{Delay: 2 * time.Second}}
	s.Expect(fp.Step{Path: "/drop", Reply: fp.Drop()}, slow)

	c := httpx.New(httpx.Options{Timeout: 200 * time.Millisecond})
	for _, path := range []string{"/drop", "/slow"} {
		req, err := http.NewRequestWithContext(context.Background(), "GET", s.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := c.Do(req)
		if err == nil {
			_ = res.Body.Close()
			t.Fatalf("%s: expected a network error", path)
		}
	}
}

func TestTokenRotation(t *testing.T) {
	s := fp.New(t)
	s.Expect(
		fp.TokenRefresh("/oauth2/token", "synthetic-refresh-1", "synthetic-access-2", "synthetic-refresh-2", time.Hour),
		fp.GET("/v1/data", "synthetic-access-2", fp.JSON(200, map[string]any{})),
		fp.Step{Path: "/oauth2/token", Reply: fp.InvalidGrant()}, // a replay of the old token
	)
	c := s.Client()
	form := func(refresh string) string {
		return url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}.Encode()
	}
	post := func(refresh string) (*http.Response, string) {
		return do(t, c, "POST", s.URL+"/oauth2/token", form(refresh), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	}

	res, body := post("synthetic-refresh-1")
	if res.StatusCode != 200 || !strings.Contains(body, "synthetic-refresh-2") || !strings.Contains(body, `"expires_in":3600`) {
		t.Fatalf("rotation: %d %s", res.StatusCode, body)
	}
	do(t, c, "GET", s.URL+"/v1/data", "", map[string]string{"Authorization": "Bearer synthetic-access-2"})
	if res, body := post("synthetic-refresh-1"); res.StatusCode != 400 || !strings.Contains(body, "invalid_grant") {
		t.Fatalf("replay: %d %s", res.StatusCode, body)
	}
}

// Mismatches must fail the test; this checks them against a recording TB so the real test stays green.
func TestAssertionsFail(t *testing.T) {
	rec := &recorder{TB: t}
	s := fp.New(rec)
	s.Expect(
		fp.Step{Method: "POST", Path: "/a"},
		fp.Step{Path: "/b", Query: url.Values{"since": {"7"}}},
		fp.Step{Path: "/never"},
	)
	do(t, s.Client(), "GET", s.URL+"/a", "", nil)         // wrong method
	do(t, s.Client(), "GET", s.URL+"/b?since=8", "", nil) // wrong query
	do(t, s.Client(), "GET", s.URL+"/extra", "", nil)     // unexpected, wrong path for /never
	rec.runCleanups()

	want := []string{"expected method", "query parameter", "expected path"}
	if len(rec.errs) < len(want) {
		t.Fatalf("errors: %v", rec.errs)
	}
	for i, w := range want {
		if !strings.Contains(rec.errs[i], w) {
			t.Errorf("error %d = %q, want %q", i, rec.errs[i], w)
		}
	}
}

// recorder captures Errorf and Cleanup instead of failing the real test.
type recorder struct {
	testing.TB
	errs     []string
	cleanups []func()
}

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, strings.TrimSpace(fmt.Sprintf(format, args...)))
}
func (r *recorder) Cleanup(f func()) { r.cleanups = append(r.cleanups, f) }
func (r *recorder) runCleanups() {
	for _, v := range slices.Backward(r.cleanups) {
		v()
	}
}
