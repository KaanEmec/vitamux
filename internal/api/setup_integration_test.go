//go:build integration

package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/connectors/remote"
	"github.com/KaanEmec/vitamux/internal/connectors/withings"
	"github.com/KaanEmec/vitamux/internal/crypto"
	fp "github.com/KaanEmec/vitamux/internal/testutil/fakeprovider"
)

const (
	setupClientID = "synthetic-client-id"
	setupSecret   = "synthetic-app-secret-1"
)

type setupEnv struct {
	*authEnv
	fake          *fp.Server
	token, csrf   string
	sidecarSecret string // the generated secret the fake sidecar accepts
}

// newSetupEnv serves the Withings connector on the fake provider with app credentials from
// connectors.Apps (env as given), a bundled garmin sidecar that is off, and the sidecar manager.
func newSetupEnv(t *testing.T, env map[string]connectors.AppCredentials) *setupEnv {
	fake := fp.New(t)
	e := &setupEnv{fake: fake}
	e.authEnv = newAuthEnv(t, false, func(o *Options, kr *crypto.Keyring) {
		apps := connectors.NewApps(o.DB, kr, env)
		wc := withings.New(withings.Config{App: apps.Source(withings.Provider), APIURL: fake.URL, AuthURL: fake.URL + "/authorize"})
		reg, err := connectors.NewRegistry(wc)
		if err != nil {
			t.Fatal(err)
		}
		sidecars := remote.NewManager(o.DB, kr, reg, nil)
		off, _ := url.Parse("http://127.0.0.1:1")
		if err := sidecars.Start(t.Context(), []remote.EnvSidecar{{Name: "garmin", URL: off, Secret: "synthetic-sidecar-secret"}}); err != nil {
			t.Fatal(err)
		}
		o.Connectors = connectors.New(connectors.Config{DB: o.DB, Blobs: o.Blobs, Keys: kr, Registry: reg, PublicURL: o.PublicURL})
		o.Apps, o.Sidecars, o.Install = apps, sidecars, "compose"
	})
	e.secrets = append(e.secrets, setupSecret)
	e.token, e.csrf = e.login()
	return e
}

func (e *setupEnv) send(method, path, body string, want int) map[string]any {
	e.t.Helper()
	res, out := e.do(method, path, body, call{cookie: e.token, csrf: e.csrf})
	if res.StatusCode != want {
		e.t.Fatalf("%s %s: %d, want %d: %v", method, path, res.StatusCode, want, out)
	}
	return out
}

func (e *setupEnv) provider(code string) map[string]any {
	e.t.Helper()
	for _, p := range e.send(http.MethodGet, "/api/v1/providers", "", http.StatusOK)["providers"].([]any) {
		if m := p.(map[string]any); m["code"] == code {
			return m
		}
	}
	e.t.Fatalf("provider %s not listed", code)
	return nil
}

func (e *setupEnv) audits(action string) int {
	return e.count(`SELECT count(*) FROM audit_events WHERE action = $1`, action)
}

// getnonce answers the signed nonce request of verify with status.
func getnonce(secret string, status int) fp.Step {
	return fp.Step{Method: http.MethodPost, Path: "/v2/signature", Form: url.Values{"action": {"getnonce"}, "client_id": {setupClientID}},
		Check: func(r *fp.Request) error {
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write([]byte("getnonce," + setupClientID + "," + r.Form.Get("timestamp")))
			if r.Form.Get("signature") != hex.EncodeToString(mac.Sum(nil)) {
				return errors.New("signature does not match")
			}
			return nil
		},
		Reply: fp.JSON(http.StatusOK, map[string]any{"status": status, "body": map[string]any{"nonce": "synthetic-nonce"}})}
}

// Withings from no app to an authorization without a restart: setup state, write-only app
// credentials, verify, the next begin using them, and removal guarded by connections.
func TestProviderAppCredentialsAPI(t *testing.T) {
	e := newSetupEnv(t, nil)
	const appPath = "/api/v1/providers/withings/app-credentials"

	p := e.provider("withings")
	if p["setup_state"] != "needs_app_credentials" || p["callback_url"] != "https://vitamux.example.test/oauth/withings/callback" ||
		p["app_credentials"].(map[string]any)["set"] != false {
		t.Fatalf("withings before setup: %v", p)
	}
	e.send(http.MethodPost, "/api/v1/providers/withings/auth/begin", "", http.StatusServiceUnavailable)
	e.send(http.MethodPost, appPath+"/verify", "", http.StatusNotFound)

	e.send(http.MethodPut, appPath, `{"client_id":" ","client_secret":"x"}`, http.StatusUnprocessableEntity)
	e.send(http.MethodPut, "/api/v1/providers/garmin/app-credentials", `{"client_id":"a","client_secret":"b"}`, http.StatusNotFound)

	got := e.send(http.MethodPut, appPath, `{"client_id":"`+setupClientID+`","client_secret":"`+setupSecret+`"}`, http.StatusOK)
	app := got["app_credentials"].(map[string]any)
	if got["setup_state"] != "ready" || app["set"] != true || app["client_id"] != setupClientID || app["managed_by_environment"] != false || app["updated_at"] == nil {
		t.Fatalf("after PUT: %v", got)
	}
	if e.audits("provider_app.set") != 1 || e.count(`SELECT count(*) FROM provider_app_credentials WHERE position($1::bytea IN ciphertext) > 0`, []byte(setupSecret)) != 0 {
		t.Fatal("PUT: not audited, or the secret is stored in plain")
	}

	e.fake.Expect(getnonce(setupSecret, 0), getnonce(setupSecret, 503))
	if v := e.send(http.MethodPost, appPath+"/verify", "", http.StatusOK); v["result"] != "valid" {
		t.Fatalf("verify: %v", v)
	}
	if v := e.send(http.MethodPost, appPath+"/verify", "", http.StatusOK); v["result"] != "invalid" {
		t.Fatalf("verify refused: %v", v)
	}
	if e.audits("provider_app.verify") != 2 {
		t.Fatal("verify not audited")
	}

	begin := e.send(http.MethodPost, "/api/v1/providers/withings/auth/begin", "", http.StatusOK)
	if u, _ := url.Parse(begin["redirect_url"].(string)); u.Query().Get("client_id") != setupClientID {
		t.Fatalf("begin without the new client id: %v", begin)
	}

	// An authorized connection keeps the credentials until removal is confirmed.
	conn := uuid.New()
	e.exec(`INSERT INTO connections (id, user_id, provider_id, account_key, mode, status)
		VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'withings'), sha256($3::bytea), 'in_process', 'active')`, conn, e.userID, conn[:])
	e.exec(`INSERT INTO credentials (connection_id, ciphertext, key_id) VALUES ($1, '\x00', 'synthetic')`, conn)
	if p := e.provider("withings"); p["setup_state"] != "connected" || p["connections"] != float64(1) {
		t.Fatalf("connected: %v", p)
	}
	e.send(http.MethodDelete, appPath, "", http.StatusConflict)
	e.send(http.MethodDelete, appPath+"?confirm=true", "", http.StatusNoContent)
	e.send(http.MethodDelete, appPath, "", http.StatusNotFound)
	if e.audits("provider_app.delete") != 1 {
		t.Fatal("delete not audited")
	}
}

// An environment value wins, is reported as managed_by_environment and cannot be changed.
func TestProviderAppCredentialsFromEnvironment(t *testing.T) {
	e := newSetupEnv(t, map[string]connectors.AppCredentials{withings.Provider: {ClientID: setupClientID, ClientSecret: setupSecret}})
	p := e.provider("withings")
	if app := p["app_credentials"].(map[string]any); p["setup_state"] != "ready" || app["managed_by_environment"] != true || app["client_id"] != setupClientID {
		t.Fatalf("environment app: %v", p)
	}
	e.send(http.MethodPut, "/api/v1/providers/withings/app-credentials", `{"client_id":"a","client_secret":"b"}`, http.StatusConflict)
	e.send(http.MethodDelete, "/api/v1/providers/withings/app-credentials", "", http.StatusConflict)
}

// A bundled sidecar that is off is needs_sidecar with the enable line; a sidecar added in the
// panel gets a generated secret once, serves after describe, and can be removed.
func TestSidecarSetupAPI(t *testing.T) {
	e := newSetupEnv(t, nil)
	describe, err := os.ReadFile("../../schemas/examples/connector-sidecar/describe.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/describe" || e.sidecarSecret == "" || r.Header.Get("Authorization") != "Bearer "+e.sidecarSecret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set(remote.ProtocolHeader, remote.Protocol)
		_, _ = w.Write(describe)
	}))
	t.Cleanup(srv.Close)

	g := e.provider("garmin")
	sc := g["sidecar"].(map[string]any)
	enable := sc["enable"].([]any)
	if g["setup_state"] != "needs_sidecar" || sc["bundled"] != true || sc["source"] != "environment" || len(enable) != 1 ||
		enable[0].(map[string]any)["line"] != "COMPOSE_PROFILES=garmin" || g["problems"].([]any)[0].(map[string]any)["code"] != "sidecar_unreachable" {
		t.Fatalf("garmin while off: %v", g)
	}

	e.send(http.MethodPost, "/api/v1/sidecars", `{"name":"garmin","url":"http://127.0.0.1:2"}`, http.StatusConflict)
	e.send(http.MethodPost, "/api/v1/sidecars", `{"name":"Bad","url":"http://127.0.0.1:2"}`, http.StatusUnprocessableEntity)
	e.send(http.MethodPost, "/api/v1/sidecars", `{"name":"x","url":"http://u:p@127.0.0.1:2"}`, http.StatusUnprocessableEntity)
	created := e.send(http.MethodPost, "/api/v1/sidecars", `{"name":"example_sidecar","url":"`+srv.URL+`"}`, http.StatusCreated)
	secret, _ := created["secret"].(string)
	if len(secret) != 64 || created["sidecar"].(map[string]any)["source"] != "panel" {
		t.Fatalf("created: %v", created)
	}
	e.secrets = append(e.secrets, secret)
	e.send(http.MethodPost, "/api/v1/sidecars", `{"name":"example_sidecar","url":"`+srv.URL+`"}`, http.StatusConflict)
	if e.audits("sidecar.add") != 1 || e.count(`SELECT count(*) FROM sidecars WHERE position($1::bytea IN ciphertext) > 0`, []byte(secret)) != 0 {
		t.Fatal("add: not audited, or the secret is stored in plain")
	}
	if p := e.provider("example_sidecar"); p["setup_state"] != "needs_sidecar" {
		t.Fatalf("before the sidecar has its secret: %v", p)
	}

	e.sidecarSecret = secret // the owner configured the sidecar; Check again
	p := e.send(http.MethodPost, "/api/v1/providers/example_sidecar/probe", "", http.StatusOK)
	if p["setup_state"] != "ready" || p["available"] != true || p["auth_kind"] != "interactive_mfa" {
		t.Fatalf("after probe: %v", p)
	}
	list := e.send(http.MethodGet, "/api/v1/sidecars", "", http.StatusOK)["sidecars"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["name"] != "example_sidecar" || list[0].(map[string]any)["available"] != true {
		t.Fatalf("sidecars: %v", list)
	}

	e.send(http.MethodDelete, "/api/v1/sidecars/garmin", "", http.StatusConflict)
	e.send(http.MethodDelete, "/api/v1/sidecars/example_sidecar", "", http.StatusNoContent)
	e.send(http.MethodDelete, "/api/v1/sidecars/example_sidecar", "", http.StatusNotFound)
	if e.audits("sidecar.remove") != 1 || len(e.send(http.MethodGet, "/api/v1/sidecars", "", http.StatusOK)["sidecars"].([]any)) != 1 {
		t.Fatal("remove: not audited or still listed")
	}
}
