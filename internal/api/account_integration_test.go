//go:build integration

package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const newPassword = "SENTINEL-password-0451-renewed"

func TestChangePassword(t *testing.T) {
	e := newAuthEnv(t, false)
	e.secrets = append(e.secrets, newPassword)
	check := func(pattern string, res *http.Response, body map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		checkBody(t, pattern, res.StatusCode, res.Header.Get("Content-Type"), raw)
	}
	const op = "POST /api/v1/auth/password"
	change := func(s call, current, next string) (*http.Response, map[string]any) {
		b, _ := json.Marshal(map[string]string{"current_password": current, "new_password": next})
		return e.do(http.MethodPost, "/api/v1/auth/password", string(b), s)
	}
	token, csrf := e.login()
	other, _ := e.login()
	s := call{cookie: token, csrf: csrf}

	res, body := change(s, ownerPassword, "short")
	e.expect(res, body, http.StatusUnprocessableEntity, CodeValidationFailed)
	check(op, res, body)
	if !strings.Contains(body["errors"].([]any)[0].(map[string]any)["pointer"].(string), "/new_password") {
		t.Fatalf("weak password: %v", body)
	}
	res, body = change(s, wrongPassword, newPassword)
	e.expect(res, body, http.StatusUnprocessableEntity, CodeValidationFailed)
	if body["errors"].([]any)[0].(map[string]any)["pointer"] != "/current_password" {
		t.Fatalf("wrong current password: %v", body)
	}
	res, body = change(call{cookie: token}, ownerPassword, newPassword)
	e.expect(res, body, http.StatusForbidden, CodeForbidden) // CSRF required

	res, body = change(s, ownerPassword, newPassword)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("change: %d %v", res.StatusCode, body)
	}
	check(op, res, body)
	res, body = e.do(http.MethodGet, "/api/v1/auth/session", "", call{cookie: token})
	e.expect(res, body, http.StatusOK, "") // this session stays
	res, body = e.do(http.MethodGet, "/api/v1/auth/session", "", call{cookie: other})
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated) // others end
	res, body = e.do(http.MethodPost, "/api/v1/auth/login", loginBody(ownerPassword), call{})
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)
	res, body = e.do(http.MethodPost, "/api/v1/auth/login", loginBody(newPassword), call{})
	e.expect(res, body, http.StatusOK, "")

	// Repeated wrong current passwords lock the change like a login.
	for range 5 {
		res, body = change(s, wrongPassword, newPassword+"x")
		e.expect(res, body, http.StatusUnprocessableEntity, CodeValidationFailed)
	}
	res, body = change(s, newPassword, newPassword+"x")
	e.expect(res, body, http.StatusTooManyRequests, CodeRateLimited)
	check(op, res, body)
	if res.Header.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}

	if n := e.count(`SELECT count(*) FROM audit_events WHERE action = 'auth.password_change' AND detail->>'sessions_ended' = '1'`); n != 1 {
		t.Fatalf("%d password_change audit events, want 1", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_events WHERE action = 'auth.password_change_failed'`); n != 6 {
		t.Fatalf("%d password_change_failed audit events, want 6", n)
	}
}

func TestSessionsListAndRevoke(t *testing.T) {
	e := newAuthEnv(t, false)
	token, csrf := e.login()
	other, _ := e.login()
	s := call{cookie: token, csrf: csrf}

	res, body := e.do(http.MethodGet, "/api/v1/auth/sessions", "", call{cookie: token})
	e.expect(res, body, http.StatusOK, "")
	raw, _ := json.Marshal(body)
	checkBody(t, "GET /api/v1/auth/sessions", res.StatusCode, res.Header.Get("Content-Type"), raw)
	list := body["sessions"].([]any)
	if len(list) != 2 {
		t.Fatalf("sessions: %v", body)
	}
	var current, otherID string
	for _, it := range list {
		m := it.(map[string]any)
		if m["current"] == true {
			current = m["id"].(string)
		} else {
			otherID = m["id"].(string)
		}
	}
	if current == "" || otherID == "" {
		t.Fatalf("want one current session: %v", body)
	}

	res, body = e.do(http.MethodDelete, "/api/v1/auth/sessions/"+otherID, "", call{cookie: token})
	e.expect(res, body, http.StatusForbidden, CodeForbidden) // CSRF required
	res, _ = e.do(http.MethodDelete, "/api/v1/auth/sessions/"+otherID, "", s)
	if res.StatusCode != http.StatusNoContent || res.Header.Get("Set-Cookie") != "" {
		t.Fatalf("revoke other: %d %q", res.StatusCode, res.Header.Get("Set-Cookie"))
	}
	res, body = e.do(http.MethodGet, "/api/v1/auth/session", "", call{cookie: other})
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)
	res, body = e.do(http.MethodDelete, "/api/v1/auth/sessions/"+otherID, "", s)
	e.expect(res, body, http.StatusNotFound, CodeNotFound)
	raw, _ = json.Marshal(body)
	checkBody(t, "DELETE /api/v1/auth/sessions/{id}", res.StatusCode, res.Header.Get("Content-Type"), raw)

	// Ending the current session clears its cookie.
	res, _ = e.do(http.MethodDelete, "/api/v1/auth/sessions/"+current, "", s)
	if res.StatusCode != http.StatusNoContent || !strings.Contains(res.Header.Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("revoke current: %d %q", res.StatusCode, res.Header.Get("Set-Cookie"))
	}
	res, body = e.do(http.MethodGet, "/api/v1/auth/sessions", "", call{cookie: token})
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)

	if n := e.count(`SELECT count(*) FROM audit_events WHERE action = 'auth.session_revoke'`); n != 2 {
		t.Fatalf("%d session_revoke audit events, want 2", n)
	}
}
