//go:build integration

package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/KaanEmec/vitamux/internal/auth"
)

const appDevice = "Synthetic iPhone"

// appLogin signs in as the app and returns the bearer token; it fails the test unless the
// answer is an app session without a cookie.
func (e *authEnv) appLogin(extra ...string) string {
	e.t.Helper()
	res, body := e.do(http.MethodPost, "/api/v1/auth/login", loginBody(ownerPassword, append([]string{"client", "app", "device_name", appDevice}, extra...)...), call{})
	e.expect(res, body, http.StatusOK, "")
	raw, _ := json.Marshal(body)
	checkBody(e.t, "POST /api/v1/auth/login", res.StatusCode, res.Header.Get("Content-Type"), raw)
	tok, _ := body["token"].(string)
	if !strings.HasPrefix(tok, auth.SessionPrefix) || res.Header.Get("Set-Cookie") != "" || body["csrf_token"] != nil {
		e.t.Fatalf("app login: cookie %q, body %v", res.Header.Get("Set-Cookie"), body)
	}
	e.secrets = append(e.secrets, tok)
	return tok
}

// appSessionID returns the id of the only app session, as the panel lists it.
func (e *authEnv) appSessionID(s call) string {
	e.t.Helper()
	res, body := e.do(http.MethodGet, "/api/v1/auth/sessions", "", s)
	e.expect(res, body, http.StatusOK, "")
	for _, v := range body["sessions"].([]any) {
		if m := v.(map[string]any); m["kind"] == "app" {
			return m["id"].(string)
		}
	}
	e.t.Fatalf("no app session listed: %v", body)
	return ""
}

func TestAppSession(t *testing.T) {
	e := newAuthEnv(t, false)
	browser, csrf := e.login()
	s := call{cookie: browser, csrf: csrf}

	// client and device_name are checked before the credentials.
	for _, bad := range []string{
		loginBody(ownerPassword, "client", "app"),
		loginBody(ownerPassword, "client", "app", "device_name", "   "),
		loginBody(ownerPassword, "client", "app", "device_name", strings.Repeat("x", 101)),
		loginBody(ownerPassword, "client", "app", "device_name", `tab\there`),
		loginBody(ownerPassword, "device_name", appDevice),
		loginBody(ownerPassword, "client", "watch", "device_name", appDevice),
	} {
		res, body := e.do(http.MethodPost, "/api/v1/auth/login", bad, call{})
		e.expect(res, body, http.StatusUnprocessableEntity, CodeValidationFailed)
	}

	before := time.Now()
	res, body := e.do(http.MethodPost, "/api/v1/auth/login", loginBody(ownerPassword, "client", "app", "device_name", " "+appDevice+" "), call{cookie: browser})
	e.expect(res, body, http.StatusOK, "")
	tok := body["token"].(string)
	e.secrets = append(e.secrets, tok)
	exp, _ := time.Parse(time.RFC3339, body["expires_at"].(string))
	if d := exp.Sub(before); d < auth.AppSessionLifetime-time.Minute || d > auth.AppSessionLifetime+time.Minute {
		t.Fatalf("expires_at %v is not 90 days out", exp)
	}
	app := call{bearer: tok}
	secret := tok[len(auth.SessionPrefix)+33:]
	rawSecret, _ := base64.RawURLEncoding.DecodeString(secret)
	if e.count("SELECT count(*) FROM sessions WHERE kind = 'app' AND name = $1 AND token_hash = $2", appDevice, sha(string(rawSecret))) != 1 ||
		e.count("SELECT count(*) FROM sessions WHERE position($1 IN sessions::text) > 0 OR position($2 IN sessions::text) > 0", tok, secret) != 0 {
		t.Fatal("an app session must be stored as the SHA-256 of its secret only")
	}
	// An app login leaves the browser session it happened to carry alone.
	res, body = e.do(http.MethodGet, "/api/v1/auth/session", "", call{cookie: browser})
	e.expect(res, body, http.StatusOK, "")

	// The app session is an owner session: session-only and scoped routes, no CSRF token.
	res, body = e.do(http.MethodGet, "/api/v1/auth/session", "", app)
	e.expect(res, body, http.StatusOK, "")
	if body["csrf_token"] != "" || body["user"].(map[string]any)["username"] != ownerName {
		t.Fatalf("app session: %v", body)
	}
	for _, r := range []struct{ method, path string }{{http.MethodGet, "/api/v1/test/health"}, {http.MethodPatch, "/api/v1/test/config"}} {
		if res, _ = e.do(r.method, r.path, "{}", app); res.StatusCode != http.StatusNoContent {
			t.Fatalf("%s %s with an app session: %d", r.method, r.path, res.StatusCode)
		}
	}
	// Neither the token nor its secret works as a cookie.
	for _, c := range []string{tok, secret} {
		res, body = e.do(http.MethodGet, "/api/v1/auth/session", "", call{cookie: c})
		e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)
	}

	// The panel lists it with its kind and name, and the browser one as kind browser.
	res, body = e.do(http.MethodGet, "/api/v1/auth/sessions", "", s)
	e.expect(res, body, http.StatusOK, "")
	raw, _ := json.Marshal(body)
	checkBody(t, "GET /api/v1/auth/sessions", res.StatusCode, res.Header.Get("Content-Type"), raw)
	kinds := map[string]any{}
	for _, v := range body["sessions"].([]any) {
		m := v.(map[string]any)
		kinds[m["kind"].(string)] = m["name"]
	}
	if len(kinds) != 2 || kinds["app"] != appDevice || kinds["browser"] != nil {
		t.Fatalf("sessions: %s", raw)
	}

	// Signing out the app ends only its session and sets no cookie.
	res, _ = e.do(http.MethodPost, "/api/v1/auth/logout", "", app)
	if res.StatusCode != http.StatusNoContent || res.Header.Get("Set-Cookie") != "" {
		t.Fatalf("app logout: %d %q", res.StatusCode, res.Header.Get("Set-Cookie"))
	}
	res, body = e.do(http.MethodGet, "/api/v1/auth/session", "", app)
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)
	res, body = e.do(http.MethodGet, "/api/v1/auth/session", "", call{cookie: browser})
	e.expect(res, body, http.StatusOK, "")

	// Ending it in the panel: the app's next request is 401.
	app = call{bearer: e.appLogin()}
	res, _ = e.do(http.MethodDelete, "/api/v1/auth/sessions/"+e.appSessionID(s), "", s)
	if res.StatusCode != http.StatusNoContent || res.Header.Get("Set-Cookie") != "" {
		t.Fatalf("revoke app session: %d %q", res.StatusCode, res.Header.Get("Set-Cookie"))
	}
	res, body = e.do(http.MethodGet, "/api/v1/test/health", "", app)
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)

	// Logs (checked at cleanup) and audit events never hold a token.
	if n := e.count("SELECT count(*) FROM audit_events WHERE action = 'auth.login' AND detail->>'kind' = 'app'"); n != 2 {
		t.Fatalf("%d app login audit events, want 2", n)
	}
	for _, sec := range e.secrets {
		if e.count("SELECT count(*) FROM audit_events WHERE position($1 IN audit_events::text) > 0", sec) != 0 {
			t.Fatalf("a secret appears in audit_events")
		}
	}
}

func TestAppSessionLoginWithTOTP(t *testing.T) {
	e := newAuthEnv(t, false)
	browser, csrf := e.login()
	s := call{cookie: browser, csrf: csrf}
	res, body := e.do(http.MethodPost, "/api/v1/auth/totp/enroll", "", s)
	e.expect(res, body, http.StatusOK, "")
	secret := body["secret"].(string)
	e.secrets = append(e.secrets, secret)
	now := time.Now()
	code, _ := totp.GenerateCode(secret, now)
	e.secrets = append(e.secrets, code)
	res, body = e.do(http.MethodPost, "/api/v1/auth/totp/confirm", `{"code":"`+code+`"}`, s)
	e.expect(res, body, http.StatusOK, "")
	recovery := body["recovery_codes"].([]any)
	for _, c := range recovery {
		e.secrets = append(e.secrets, c.(string))
	}

	appBody := func(extra ...string) string {
		return loginBody(ownerPassword, append([]string{"client", "app", "device_name", appDevice}, extra...)...)
	}
	res, body = e.do(http.MethodPost, "/api/v1/auth/login", appBody(), call{})
	e.expect(res, body, http.StatusUnauthorized, CodeTOTPRequired)
	if body["token"] != nil || res.Header.Get("Set-Cookie") != "" {
		t.Fatal("no session without the second factor")
	}
	next, _ := totp.GenerateCode(secret, now.Add(30*time.Second))
	e.secrets = append(e.secrets, next)
	tok := e.appLogin("totp_code", next)
	res, body = e.do(http.MethodPost, "/api/v1/auth/login", appBody("totp_code", next), call{})
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated) // replay
	rc := recovery[0].(string)
	e.appLogin("recovery_code", rc)
	res, body = e.do(http.MethodPost, "/api/v1/auth/login", appBody("recovery_code", rc), call{})
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated) // single use

	res, body = e.do(http.MethodGet, "/api/v1/auth/session", "", call{bearer: tok})
	e.expect(res, body, http.StatusOK, "")
	if e.count("SELECT count(*) FROM audit_events WHERE action = 'auth.login' AND detail->>'kind' = 'app' AND detail->>'method' IN ('totp', 'recovery_code')") != 2 {
		t.Fatal("app logins with a second factor must be audited with their method")
	}
}

func TestAppSessionExpiry(t *testing.T) {
	e := newAuthEnv(t, false)
	app := call{bearer: e.appLogin()}

	// Idle beyond a browser session's 12 h, within the app's 30 days: still signed in.
	e.exec("UPDATE sessions SET last_seen_at = now() - interval '13 hours'")
	res, body := e.do(http.MethodGet, "/api/v1/test/health", "", app)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("app session after 13 h idle: %d %v", res.StatusCode, body)
	}
	// 30 days idle ends it.
	e.exec("UPDATE sessions SET last_seen_at = now() - interval '30 days 1 minute'")
	res, body = e.do(http.MethodGet, "/api/v1/test/health", "", app)
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)

	// The absolute end, however active.
	app = call{bearer: e.appLogin()}
	e.exec("UPDATE sessions SET expires_at = now() - interval '1 second' WHERE kind = 'app'")
	res, body = e.do(http.MethodGet, "/api/v1/test/health", "", app)
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)
	if e.count("SELECT count(*) FROM sessions WHERE kind = 'app'") != 1 {
		t.Fatal("the next login prunes stale app sessions")
	}

	// VITAMUX_APP_SESSION_IDLE and _MAX apply.
	e.svc.SetAppSessionLifetimes(time.Hour, 2*time.Hour)
	before := time.Now()
	res, body = e.do(http.MethodPost, "/api/v1/auth/login", loginBody(ownerPassword, "client", "app", "device_name", appDevice), call{})
	e.expect(res, body, http.StatusOK, "")
	tok := body["token"].(string)
	e.secrets = append(e.secrets, tok)
	exp, _ := time.Parse(time.RFC3339, body["expires_at"].(string))
	if d := exp.Sub(before); d < 2*time.Hour-time.Minute || d > 2*time.Hour+time.Minute {
		t.Fatalf("expires_at %v is not 2 h out", exp)
	}
	e.exec("UPDATE sessions SET last_seen_at = now() - interval '61 minutes' WHERE kind = 'app'")
	res, body = e.do(http.MethodGet, "/api/v1/test/health", "", call{bearer: tok})
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)
}

func TestPasswordChangeEndsAppSessions(t *testing.T) {
	e := newAuthEnv(t, false)
	e.secrets = append(e.secrets, newPassword)
	change := func(c call, current, next string) *http.Response {
		b, _ := json.Marshal(map[string]string{"current_password": current, "new_password": next})
		res, _ := e.do(http.MethodPost, "/api/v1/auth/password", string(b), c)
		return res
	}
	browser, csrf := e.login()
	app := call{bearer: e.appLogin()}
	if res := change(call{cookie: browser, csrf: csrf}, ownerPassword, newPassword); res.StatusCode != http.StatusNoContent {
		t.Fatalf("change from the browser: %d", res.StatusCode)
	}
	res, body := e.do(http.MethodGet, "/api/v1/test/health", "", app)
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)

	// From the app (no CSRF token): its own session stays, every other one ends.
	res, body = e.do(http.MethodPost, "/api/v1/auth/login", loginBody(newPassword, "client", "app", "device_name", appDevice), call{})
	e.expect(res, body, http.StatusOK, "")
	app = call{bearer: body["token"].(string)}
	e.secrets = append(e.secrets, app.bearer)
	if res := change(app, newPassword, ownerPassword); res.StatusCode != http.StatusNoContent {
		t.Fatalf("change from the app: %d", res.StatusCode)
	}
	res, body = e.do(http.MethodGet, "/api/v1/auth/session", "", call{cookie: browser})
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)
	res, body = e.do(http.MethodGet, "/api/v1/auth/session", "", app)
	e.expect(res, body, http.StatusOK, "")
}
