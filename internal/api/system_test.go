package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/version"
)

// TestSystemVersionContract exercises the generated strict server behind rt.handle: access
// is enforced and the response matches the spec.
func TestSystemVersionContract(t *testing.T) {
	rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	const pattern = "GET /api/v1/system/version"
	call := func(p *auth.Principal) *http.Response {
		req := request(t, http.MethodGet, "/api/v1/system/version", nil)
		if p != nil {
			req = req.WithContext(auth.WithPrincipal(req.Context(), p))
		}
		return serve(t, rt.mux, req)
	}
	key := func(s ...auth.Scope) *auth.Principal {
		return &auth.Principal{Kind: auth.APIKey, UserID: uuid.New(), ID: uuid.New(), Scopes: s}
	}

	res := call(key(auth.ReadConfig))
	if res.StatusCode != http.StatusOK || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("read:config key: %d, Cache-Control %q", res.StatusCode, res.Header.Get("Cache-Control"))
	}
	if body := checkResponse(t, pattern, res); !bytes.Contains(body, []byte(`"version":"`+version.Version+`"`)) {
		t.Fatalf("body %s lacks the version", body)
	}
	if res := call(&auth.Principal{Kind: auth.OwnerSession, UserID: uuid.New()}); res.StatusCode != http.StatusOK {
		t.Fatalf("session: %d", res.StatusCode)
	}
	for name, p := range map[string]*auth.Principal{
		"read:health key": key(auth.ReadHealth),
		"client token":    {Kind: auth.Client, ConnectionID: uuid.New()},
		"anonymous":       nil,
	} {
		res := call(p)
		if res.StatusCode != http.StatusUnauthorized && res.StatusCode != http.StatusForbidden {
			t.Errorf("%s: %d", name, res.StatusCode)
		}
		checkResponse(t, pattern, res)
	}
}
