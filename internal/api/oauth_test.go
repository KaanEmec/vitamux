package api

import (
	"log/slog"
	"net/http"
	"testing"
)

// The provider callback is public: HEAD is a side-effect-free 200, and GET always lands back
// in the UI, here with the reason the flow could not run.
func TestOAuthCallbackWithoutRuntime(t *testing.T) {
	h, err := NewHandler(slog.New(slog.DiscardHandler), newUITestFS(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	res := serve(t, h, request(t, http.MethodHead, "/oauth/withings/callback?code=synthetic-code&state=x", nil))
	if res.StatusCode != http.StatusOK || res.Header.Get("Set-Cookie") != "" || res.Header.Get("Location") != "" {
		t.Fatalf("HEAD: %d, cookie %q", res.StatusCode, res.Header.Get("Set-Cookie"))
	}
	res = serve(t, h, request(t, http.MethodGet, "/oauth/withings/callback?code=synthetic-code&state=x", nil))
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/connections?auth_error=unavailable&provider=withings" {
		t.Fatalf("GET: %d to %q", res.StatusCode, res.Header.Get("Location"))
	}
}

// The app start route is public too: HEAD touches nothing, and GET without a runtime ends in
// the app (the fixed scheme) with the reason, never at a URL taken from the request.
func TestOAuthStartWithoutRuntime(t *testing.T) {
	h, err := NewHandler(slog.New(slog.DiscardHandler), newUITestFS(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	res := serve(t, h, request(t, http.MethodHead, "/oauth/withings/start?ticket=x", nil))
	if res.StatusCode != http.StatusOK || res.Header.Get("Set-Cookie") != "" || res.Header.Get("Location") != "" {
		t.Fatalf("HEAD: %d, cookie %q", res.StatusCode, res.Header.Get("Set-Cookie"))
	}
	res = serve(t, h, request(t, http.MethodGet, "/oauth/withings/start?ticket=x&redirect_uri=https://evil.example", nil))
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "vitamux://connections?auth_error=unavailable&provider=withings" ||
		res.Header.Get("Set-Cookie") != "" {
		t.Fatalf("GET: %d to %q", res.StatusCode, res.Header.Get("Location"))
	}
}
