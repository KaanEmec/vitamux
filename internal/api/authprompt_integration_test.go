//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

const (
	promptProvider = "fake_mfa"
	promptPassword = "SENTINEL-sidecar-pass-0917"
	promptCode     = "SENTINEL-mfa-code-5531"
)

// promptFake is an unofficial sidecar-like connector: username and password, then an MFA
// code, each step with an opaque session the browser must never see.
type promptFake struct{}

func (promptFake) Describe() connectors.Descriptor {
	return connectors.Descriptor{
		Provider: promptProvider, Name: "Fake MFA", Version: "test", Remote: true,
		Upstream: &connectors.Upstream{Package: "synthetic-collector", Version: "1.2.3", SourceURL: "https://example.invalid/synthetic-collector"},
		AuthKind: connectors.AuthInteractiveMFA, Capabilities: connectors.Capabilities{Incremental: true},
		Streams: []connectors.StreamSpec{{Name: promptProvider + ".heart_rate", Interval: time.Hour}},
	}
}

func (promptFake) Plan(context.Context, connectors.Conn, connectors.PlanRequest) ([]connectors.WorkUnit, error) {
	return nil, nil
}

func (promptFake) Fetch(context.Context, connectors.Conn, connectors.Credentials, connectors.WorkUnit, *connectors.RawSink) (connectors.FetchResult, error) {
	return connectors.FetchResult{Done: true}, nil
}

func (promptFake) Refresh(_ context.Context, _ connectors.Conn, c connectors.Credentials) (connectors.Credentials, error) {
	return c, nil
}

func (promptFake) Begin(context.Context, connectors.AuthInput) (connectors.AuthStep, error) {
	return connectors.AuthStep{Prompt: &connectors.AuthPrompt{Message: "Sign in", Fields: []connectors.AuthField{
		{Name: "username", Label: "Username", Kind: connectors.FieldText},
		{Name: "password", Label: "Password", Kind: connectors.FieldPassword},
	}}, Session: []byte("synthetic-session-1")}, nil
}

func (promptFake) Continue(_ context.Context, _ connectors.Conn, in connectors.AuthInput) (connectors.Authorized, error) {
	switch {
	case string(in.Session) == "synthetic-session-1" && in.Values["password"] == promptPassword:
		return connectors.Authorized{Next: &connectors.AuthStep{Prompt: &connectors.AuthPrompt{Message: "Enter the code",
			Fields: []connectors.AuthField{{Name: "code", Label: "Code", Kind: connectors.FieldCode}}}, Session: []byte("synthetic-session-2")}}, nil
	case string(in.Session) == "synthetic-session-2" && in.Values["code"] == promptCode:
		return connectors.Authorized{AccountID: "synthetic-account", Credentials: connectors.Credentials{AccessToken: "synthetic-access"}}, nil
	}
	return connectors.Authorized{}, connectors.ErrAuthDenied
}

// The HTTP side of a prompt flow: providers list, begin → prompt → continue → MFA prompt →
// continue → a paused remote connection with its upstream. The connector session and the
// owner's values never appear in a response or the logs.
func TestPromptAuthAPI(t *testing.T) {
	e := newAuthEnv(t, false, func(o *Options, kr *crypto.Keyring) {
		reg, err := connectors.NewRegistry(promptFake{})
		if err != nil {
			t.Fatal(err)
		}
		public, _ := url.Parse("https://vitamux.example")
		o.Connectors = connectors.New(connectors.Config{DB: o.DB, Blobs: o.Blobs, Keys: kr, Registry: reg, PublicURL: public})
	})
	e.secrets = append(e.secrets, promptPassword, promptCode, "synthetic-session")
	if err := e.d.Q().RegisterProvider(t.Context(), dbq.RegisterProviderParams{Code: promptProvider, Name: "Fake MFA"}); err != nil {
		t.Fatal(err)
	}
	token, csrf := e.login()

	var binding string
	send := func(method, pattern, path, body string, want int) map[string]any {
		t.Helper()
		req := request(t, method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
		if binding != "" {
			req.AddCookie(&http.Cookie{Name: oauthCookie, Value: binding})
		}
		res := serve(t, e.h, req)
		raw := checkResponse(t, pattern, res)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d, want %d: %s", method, path, res.StatusCode, want, raw)
		}
		for _, s := range []string{"synthetic-session", promptPassword, promptCode, "synthetic-access"} {
			if strings.Contains(string(raw), s) {
				t.Fatalf("%s %s: response contains %q", method, path, s)
			}
		}
		for _, c := range res.Cookies() {
			if c.Name == oauthCookie && c.Path == continueCookiePath && c.MaxAge > 0 {
				binding = c.Value
			}
		}
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return out
	}

	got := send(http.MethodGet, "GET /api/v1/providers", "/api/v1/providers", "", http.StatusOK)
	ps, _ := got["providers"].([]any)
	if len(ps) != 1 {
		t.Fatalf("providers: %v", got)
	}
	if p := ps[0].(map[string]any); p["code"] != promptProvider || p["name"] != "Fake MFA" || p["official"] != false ||
		p["remote"] != true || p["available"] != true || p["auth_kind"] != "interactive_mfa" {
		t.Fatalf("provider: %v", p)
	}

	const (
		beginPat    = "POST /api/v1/providers/{provider}/auth/begin"
		continuePat = "POST /api/v1/providers/{provider}/auth/continue"
		continueURL = "/api/v1/providers/" + promptProvider + "/auth/continue"
	)
	step := send(http.MethodPost, beginPat, "/api/v1/providers/"+promptProvider+"/auth/begin", "", http.StatusOK)
	state1, _ := step["state"].(string)
	if state1 == "" || step["prompt"] == nil || binding == "" {
		t.Fatalf("begin: %v (binding cookie set: %v)", step, binding != "")
	}
	values := func(state string, kv ...string) string {
		v := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			v[kv[i]] = kv[i+1]
		}
		b, _ := json.Marshal(map[string]any{"state": state, "values": v})
		return string(b)
	}

	// Without the binding cookie the state does not verify (and stays usable).
	b := binding
	binding = ""
	send(http.MethodPost, continuePat, continueURL, values(state1, "username", "synthetic-user", "password", promptPassword), http.StatusUnprocessableEntity)
	binding = b

	step = send(http.MethodPost, continuePat, continueURL, values(state1, "username", "synthetic-user", "password", promptPassword), http.StatusOK)
	state2, _ := step["state"].(string)
	if state2 == "" || state2 == state1 {
		t.Fatalf("MFA step: %v", step)
	}
	send(http.MethodPost, continuePat, continueURL, values(state1, "username", "synthetic-user", "password", promptPassword), http.StatusUnprocessableEntity)

	done := send(http.MethodPost, continuePat, continueURL, values(state2, "code", promptCode), http.StatusOK)
	id, _ := done["connection_id"].(string)
	if !strings.HasPrefix(id, "conn_") {
		t.Fatalf("done: %v", done)
	}
	send(http.MethodPost, continuePat, continueURL, values(state2, "code", promptCode), http.StatusUnprocessableEntity)

	conn := send(http.MethodGet, "GET /api/v1/connections/{id}", "/api/v1/connections/"+id, "", http.StatusOK)
	up, _ := conn["upstream"].(map[string]any)
	if conn["mode"] != "remote" || conn["status"] != "paused" || conn["official"] != false || up["package"] != "synthetic-collector" {
		t.Fatalf("connection: %v", conn)
	}
	if n := e.count(`SELECT count(*) FROM oauth_states`); n != 0 {
		t.Fatalf("%d state rows left", n)
	}
}
