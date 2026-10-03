//go:build integration

package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestAuthResponsesMatchSpec checks the hand-written auth and API key handlers against the
// operations documented for them in api/openapi.yaml.
func TestAuthResponsesMatchSpec(t *testing.T) {
	e := newAuthEnv(t, false)
	check := func(pattern string, res *http.Response, body map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		checkBody(t, pattern, res.StatusCode, res.Header.Get("Content-Type"), raw)
	}

	res, body := e.do(http.MethodPost, "/api/v1/auth/login", loginBody(wrongPassword), call{})
	check("POST /api/v1/auth/login", res, body)
	res, body = e.do(http.MethodPost, "/api/v1/auth/login", loginBody(ownerPassword), call{})
	check("POST /api/v1/auth/login", res, body)
	token, csrf := e.login()
	s := call{cookie: token, csrf: csrf}

	res, body = e.do(http.MethodGet, "/api/v1/auth/session", "", s)
	check("GET /api/v1/auth/session", res, body)
	res, body = e.do(http.MethodPost, "/api/v1/auth/totp/enroll", "", s)
	check("POST /api/v1/auth/totp/enroll", res, body)

	res, body = e.do(http.MethodPost, "/api/v1/api-keys", `{"name":"r","scopes":["superuser"]}`, s)
	check("POST /api/v1/api-keys", res, body)
	res, body = e.do(http.MethodPost, "/api/v1/api-keys", `{"name":"r","scopes":["read:health"]}`, s)
	check("POST /api/v1/api-keys", res, body)
	if tok, _ := body["token"].(string); tok != "" {
		e.secrets = append(e.secrets, tok)
	}
	id, _ := body["id"].(string)
	res, body = e.do(http.MethodGet, "/api/v1/api-keys", "", s)
	check("GET /api/v1/api-keys", res, body)
	res, body = e.do(http.MethodDelete, "/api/v1/api-keys/"+id, "", s)
	check("DELETE /api/v1/api-keys/{id}", res, body)

	res, body = e.do(http.MethodPost, "/api/v1/auth/logout", "", s)
	check("POST /api/v1/auth/logout", res, body)
}
