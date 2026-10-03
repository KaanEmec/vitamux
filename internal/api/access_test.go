package api

import (
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/crypto"
)

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

// TestEveryProtectedRouteRejectsAnonymous walks the route table: anything not declared
// public answers 401 problem+json to a request without credentials (E03 acceptance).
func TestEveryProtectedRouteRejectsAnonymous(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.WriteKeyFile(keyPath); err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.Load(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.New(nil, kr) // anonymous requests never reach the database
	if err != nil {
		t.Fatal(err)
	}
	for name, opts := range map[string]Options{"with auth": {Auth: svc}, "without master key": {}} {
		rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), opts)
		if err != nil {
			t.Fatal(err)
		}
		h := rt.handler()
		protected := 0
		for _, r := range rt.routes {
			if r.access.public {
				continue
			}
			protected++
			method, path, _ := strings.Cut(r.pattern, " ")
			path = pathParam.ReplaceAllString(path, "0192f0a0-0000-7000-8000-000000000000")
			res := serve(t, h, request(t, method, path, strings.NewReader("{}")))
			if res.StatusCode != http.StatusUnauthorized || decodeProblem(t, res).Code != CodeUnauthenticated {
				t.Errorf("%s: %s answered %d to anonymous", name, r.pattern, res.StatusCode)
			}
		}
		if protected < 8 {
			t.Fatalf("only %d protected routes registered", protected)
		}
	}
}

func TestAuthFailsClosedWithoutKey(t *testing.T) {
	h, err := NewHandler(slog.New(slog.DiscardHandler), newUITestFS(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	res := serve(t, h, request(t, http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"a","password":"b"}`)))
	if res.StatusCode != http.StatusServiceUnavailable || res.Header.Get("Set-Cookie") != "" {
		t.Fatalf("login without a master key: %d", res.StatusCode)
	}
	req := request(t, http.MethodGet, "/api/v1/api-keys", nil)
	req.Header.Set("Authorization", "Bearer vmx_pat_whatever")
	if res := serve(t, h, req); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bearer without a master key: %d", res.StatusCode)
	}
}
