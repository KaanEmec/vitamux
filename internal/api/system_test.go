package api

import (
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/version"
)

// TestSystemVersionContract exercises the generated strict server behind rt.handle: the
// handshake is public, the build is for read:config callers only, and both shapes match the spec.
func TestSystemVersionContract(t *testing.T) {
	rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	const pattern = "GET /api/v1/system/version"
	key := func(s ...auth.Scope) *auth.Principal {
		return &auth.Principal{Kind: auth.APIKey, UserID: uuid.New(), ID: uuid.New(), Scopes: s}
	}
	handshake := map[string]any{"product": "vitamux", "api_version": float64(version.APIVersion), "min_app_version": version.MinAppVersion}
	full := map[string]any{"version": version.Version, "commit": version.Commit}
	maps.Copy(full, handshake)
	tests := []struct {
		name string
		p    *auth.Principal
		want map[string]any
	}{
		{"anonymous", nil, handshake},
		{"read:health key", key(auth.ReadHealth), handshake},
		{"client token", &auth.Principal{Kind: auth.Client, ConnectionID: uuid.New()}, handshake},
		{"read:config key", key(auth.ReadConfig), full},
		{"session", &auth.Principal{Kind: auth.OwnerSession, UserID: uuid.New()}, full},
		{"app session", &auth.Principal{Kind: auth.OwnerSession, App: true, UserID: uuid.New()}, full},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := request(t, http.MethodGet, "/api/v1/system/version", nil)
			if tt.p != nil {
				req = req.WithContext(auth.WithPrincipal(req.Context(), tt.p))
			}
			res := serve(t, rt.mux, req)
			if res.StatusCode != http.StatusOK || res.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("%d, Cache-Control %q", res.StatusCode, res.Header.Get("Cache-Control"))
			}
			var got map[string]any
			if err := json.Unmarshal(checkResponse(t, pattern, res), &got); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("body %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Fatalf("body %v, want %v", got, tt.want)
				}
			}
		})
	}
}
