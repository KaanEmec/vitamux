package api

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/KaanEmec/vitamux/web"
)

const sampleIndex = `<!doctype html><html><head>
<meta http-equiv="content-security-policy" content="default-src 'self'; script-src 'self' 'sha256-AAA='">
</head><body><div id="app"><script>
	boot();
</script></div></body></html>`

// inlineScriptHashes returns CSP source expressions for every inline script in a page.
func inlineScriptHashes(page []byte) []string {
	var out []string
	for _, m := range regexp.MustCompile(`(?is)<script>(.*?)</script>`).FindAllSubmatch(page, -1) {
		sum := sha256.Sum256(m[1])
		out = append(out, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return out
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	h, err := NewHandler(slog.New(slog.DiscardHandler), fstest.MapFS{
		"index.html":                    {Data: []byte(sampleIndex)},
		"favicon.svg":                   {Data: []byte("<svg/>")},
		"_app/immutable/entry/start.js": {Data: []byte("export{}")},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func get(t *testing.T, h http.Handler, path string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	res := rec.Result()
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func TestHealthz(t *testing.T) {
	res := get(t, newTestHandler(t), "/healthz")
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"status":"ok"`) {
		t.Fatalf("got %d %s", res.StatusCode, body)
	}
}

func TestSPAFallbackForDeepLinks(t *testing.T) {
	for _, p := range []string{"/", "/rules/steps", "/connections/conn_1/history", "/_app/immutable"} {
		res := get(t, newTestHandler(t), p)
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 || !strings.Contains(string(body), "boot();") {
			t.Fatalf("%s: got %d", p, res.StatusCode)
		}
		if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "'sha256-AAA='") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Fatalf("%s: csp %q", p, csp)
		}
	}
}

func TestAssetsAndCaching(t *testing.T) {
	h := newTestHandler(t)
	if res := get(t, h, "/_app/immutable/entry/start.js"); res.StatusCode != 200 || !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("immutable asset: %d %q", res.StatusCode, res.Header.Get("Cache-Control"))
	}
	if res := get(t, h, "/favicon.svg"); res.StatusCode != 200 || res.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("favicon: %d", res.StatusCode)
	}
	if res := get(t, h, "/_app/immutable/missing.js"); res.StatusCode != 404 {
		t.Fatalf("missing asset should 404, got %d", res.StatusCode)
	}
	if res := get(t, h, "/api/v1/unknown"); res.StatusCode != 404 {
		t.Fatalf("unknown API route must not serve the SPA, got %d", res.StatusCode)
	}
}

// TestShippedIndexCSP checks the page actually compiled into this binary: every
// inline script must be allowed by hash, so no 'unsafe-inline' is ever needed.
func TestShippedIndexCSP(t *testing.T) {
	uh, err := newUIHandler(web.Assets())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(uh.csp, "unsafe-inline") {
		t.Fatalf("CSP must not allow unsafe-inline: %s", uh.csp)
	}
	if strings.Contains(string(uh.index), " style=") {
		t.Fatal("inline style attributes are blocked by style-src 'self'")
	}
	for _, h := range inlineScriptHashes(uh.index) {
		if !strings.Contains(uh.csp, h) {
			t.Fatalf("inline script hash %s missing from CSP %q (embedded UI: %v)", h, uh.csp, web.Embedded)
		}
	}
}
