//go:build integration

package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/connectors/withings"
	fp "github.com/KaanEmec/vitamux/internal/testutil/fakeprovider"
)

const withingsCallback = "https://vitamux.example.test/oauth/withings/callback"

// withingsExchange scripts the Withings code exchange of a new account.
func withingsExchange(code string) fp.Step {
	return fp.Step{
		Method: http.MethodPost, Path: "/v2/oauth2",
		Form: url.Values{"action": {"requesttoken"}, "grant_type": {"authorization_code"}, "code": {code},
			"client_id": {setupClientID}, "redirect_uri": {withingsCallback}},
		Reply: fp.JSON(http.StatusOK, map[string]any{"status": 0, "body": map[string]any{
			"userid": 1234567, "access_token": "synthetic-access-" + code, "refresh_token": "synthetic-refresh-" + code,
			"expires_in": 10800, "scope": "user.metrics,user.activity", "token_type": "Bearer",
		}}),
	}
}

// browse is a GET in a browser that holds only the given cookies (the app's
// ASWebAuthenticationSession, or the panel's browser); it returns the response and its
// Location.
func (e *setupEnv) browse(path string, cookies ...*http.Cookie) (*http.Response, string) {
	e.t.Helper()
	req := request(e.t, http.MethodGet, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	res := serve(e.t, e.h, req)
	if res.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("GET %s: %d, want 303", path, res.StatusCode)
	}
	return res, res.Header.Get("Location")
}

func bindingCookie(t *testing.T, res *http.Response, path string) *http.Cookie {
	t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == oauthCookie && c.Path == path && c.MaxAge > 0 && c.HttpOnly && c.Secure && c.SameSite == http.SameSiteLaxMode {
			return c
		}
	}
	t.Fatalf("no binding cookie for %s in %v", path, res.Header.Values("Set-Cookie"))
	return nil
}

// An app-originated Withings flow (J22.3): begin with {"return": "app"} over the bearer
// session, the single-use start ticket sets the binding cookie in the auth browser, and the
// callback ends at vitamux:// with the connection active. Replayed tickets and states fail; the
// browser flow is unchanged.
func TestAppOAuthFlow(t *testing.T) {
	e := newSetupEnv(t, map[string]connectors.AppCredentials{withings.Provider: {ClientID: setupClientID, ClientSecret: setupSecret}})
	app := call{bearer: e.appLogin()}
	const beginPath = "/api/v1/providers/withings/auth/begin"

	// The app return is for app sessions only; other values are refused.
	res, body := e.do(http.MethodPost, beginPath, `{"return":"app"}`, call{cookie: e.token, csrf: e.csrf})
	e.expect(res, body, http.StatusForbidden, CodeForbidden)
	for _, bad := range []string{`{"return":"vitamux://evil"}`, `{"return":"app","redirect":"x"}`} {
		res, body = e.do(http.MethodPost, beginPath, bad, app)
		e.expect(res, body, http.StatusUnprocessableEntity, CodeValidationFailed)
	}

	begin := func() string {
		t.Helper()
		res, body := e.do(http.MethodPost, beginPath, `{"return":"app"}`, app)
		e.expect(res, body, http.StatusOK, "")
		redirect, _ := body["redirect_url"].(string)
		u, err := url.Parse(redirect)
		if err != nil || u.Scheme != "https" || u.Host != "vitamux.example.test" || u.Path != "/oauth/withings/start" ||
			len(u.Query()) != 1 || u.Query().Get("ticket") == "" {
			t.Fatalf("begin: %v", body)
		}
		e.secrets = append(e.secrets, u.Query().Get("ticket"))
		return u.RequestURI()
	}

	// Start: the auth browser has no cookies yet; it gets the binding and goes to Withings.
	start := begin()
	ticket := strings.TrimPrefix(start, "/oauth/withings/start?ticket=")
	if e.count(`SELECT count(*) FROM oauth_states WHERE return_to = 'app' AND ticket_hash = sha256($1::bytea)
		AND position($1::bytea IN binding) = 0`, []byte(ticket)) != 1 {
		t.Fatal("the state row must keep the ticket's SHA-256 only, and a sealed binding")
	}
	res, loc := e.browse(start)
	bind := bindingCookie(t, res, callbackCookiePath)
	e.secrets = append(e.secrets, bind.Value)
	consent, err := url.Parse(loc)
	if err != nil || !strings.HasPrefix(loc, e.fake.URL+"/authorize?") || consent.Query().Get("redirect_uri") != withingsCallback {
		t.Fatalf("start redirected to %q", loc)
	}
	state := consent.Query().Get("state")
	e.secrets = append(e.secrets, state)

	// The ticket works once.
	if res, loc := e.browse(start); loc != "vitamux://connections?auth_error=invalid_state&provider=withings" || len(res.Cookies()) != 0 {
		t.Fatalf("replayed ticket: %q, cookies %v", loc, res.Cookies())
	}

	// Without the binding cookie the callback refuses the state.
	if _, loc := e.browse("/oauth/withings/callback?" + url.Values{"state": {state}, "code": {"synthetic-code-1"}}.Encode()); !strings.HasPrefix(loc, "/connections?auth_error=invalid_state") {
		t.Fatalf("callback without the binding: %q", loc)
	}

	// The callback completes the flow and returns to the app.
	e.fake.Expect(withingsExchange("synthetic-code-1"))
	callback := "/oauth/withings/callback?" + url.Values{"state": {state}, "code": {"synthetic-code-1"}}.Encode()
	if _, loc := e.browse(callback, bind); loc != "vitamux://connections?connected=withings" {
		t.Fatalf("callback: %q", loc)
	}
	if e.count(`SELECT count(*) FROM connections c JOIN providers p ON p.id = c.provider_id WHERE p.code = 'withings' AND c.status = 'active'`) != 1 {
		t.Fatal("no active Withings connection")
	}

	// A replayed state fails: the row is gone, so nothing tells it was the app's.
	if _, loc := e.browse(callback, bind); loc != "/connections?auth_error=invalid_state&provider=withings" {
		t.Fatalf("replayed state: %q", loc)
	}

	// An outcome of a known app state returns to the app as well.
	res, _ = e.browse(begin())
	bind = bindingCookie(t, res, callbackCookiePath)
	consent, _ = url.Parse(res.Header.Get("Location"))
	q := url.Values{"state": {consent.Query().Get("state")}, "error": {"access_denied"}}
	if _, loc := e.browse("/oauth/withings/callback?"+q.Encode(), bind); loc != "vitamux://connections?auth_error=denied&provider=withings" {
		t.Fatalf("denied: %q", loc)
	}

	// The ticket expires after connectors.TicketTTL, and with its session.
	start = begin()
	e.exec(`UPDATE oauth_states SET created_at = now() - interval '1 second' * $1::int`, int(connectors.TicketTTL.Seconds())+1)
	if _, loc := e.browse(start); loc != "vitamux://connections?auth_error=invalid_state&provider=withings" {
		t.Fatalf("expired ticket: %q", loc)
	}
	start = begin()
	if _, loc := e.browse(strings.Replace(start, "/withings/", "/garmin/", 1)); loc != "vitamux://connections?auth_error=invalid_state&provider=garmin" {
		t.Fatalf("ticket at another provider: %q", loc)
	}
	e.exec(`DELETE FROM sessions WHERE kind = 'app'`)
	if _, loc := e.browse(start); loc != "vitamux://connections?auth_error=invalid_state&provider=withings" {
		t.Fatalf("ticket of an ended session: %q", loc)
	}

	// The browser flow is unchanged: a provider URL at once, and back to the panel.
	res, body = e.do(http.MethodPost, beginPath, "", call{cookie: e.token, csrf: e.csrf})
	e.expect(res, body, http.StatusOK, "")
	consent, _ = url.Parse(body["redirect_url"].(string))
	if !strings.HasPrefix(consent.String(), e.fake.URL+"/authorize?") {
		t.Fatalf("browser begin: %v", body)
	}
	bind = bindingCookie(t, res, callbackCookiePath)
	e.fake.Expect(withingsExchange("synthetic-code-2"))
	q = url.Values{"state": {consent.Query().Get("state")}, "code": {"synthetic-code-2"}}
	if _, loc := e.browse("/oauth/withings/callback?"+q.Encode(), bind); loc != "/connections?connected=withings" {
		t.Fatalf("browser callback: %q", loc)
	}
	if e.count(`SELECT count(*) FROM oauth_states WHERE return_to = 'browser'`) != 0 {
		t.Fatal("browser state left behind")
	}
}
