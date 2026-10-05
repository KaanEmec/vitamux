//go:build integration

package connectors

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

const (
	mfaProvider = "fake_mfa"
	testBinding = "synthetic-binding"
)

// mfaFake is an unofficial sidecar-like connector: a username and password prompt, then an
// MFA code prompt, each step carrying an opaque session; or, with redirect, one OAuth redirect
// whose session (a PKCE verifier) the callback must get back.
type mfaFake struct {
	official, redirect bool
	inputs             []AuthInput // what Continue saw
}

func (m *mfaFake) Describe() Descriptor {
	return Descriptor{
		Provider: mfaProvider, Name: "Fake MFA", Version: "test", Official: m.official, Remote: true,
		Upstream: &Upstream{Package: "synthetic-collector", Version: "1.2.3", SourceURL: "https://example.invalid/synthetic-collector"},
		AuthKind: AuthInteractiveMFA, Streams: []StreamSpec{{Name: mfaProvider + ".heart_rate", Interval: time.Hour}},
		Capabilities: Capabilities{Incremental: true},
	}
}

func (*mfaFake) Plan(_ context.Context, _ Conn, req PlanRequest) ([]WorkUnit, error) {
	return []WorkUnit{{Cursor: req.Cursor}}, nil
}

func (*mfaFake) Fetch(context.Context, Conn, Credentials, WorkUnit, *RawSink) (FetchResult, error) {
	return FetchResult{Done: true}, nil
}

func (*mfaFake) Refresh(_ context.Context, _ Conn, c Credentials) (Credentials, error) { return c, nil }

func (m *mfaFake) Begin(_ context.Context, in AuthInput) (AuthStep, error) {
	if in.State == "" {
		return AuthStep{}, errors.New("begin without a state")
	}
	if m.redirect {
		return AuthStep{RedirectURL: "https://provider.example/authorize?state=" + url.QueryEscape(in.State), Session: []byte("synthetic-verifier")}, nil
	}
	return AuthStep{Prompt: &AuthPrompt{Message: "Sign in to the synthetic service", Fields: []AuthField{
		{Name: "username", Label: "Username", Kind: FieldText}, {Name: "password", Label: "Password", Kind: FieldPassword},
	}}, Session: []byte("synthetic-session-1")}, nil
}

func (m *mfaFake) Continue(_ context.Context, _ Conn, in AuthInput) (Authorized, error) {
	m.inputs = append(m.inputs, in)
	done := Authorized{AccountID: "synthetic-account", Credentials: Credentials{AccessToken: "synthetic-access-1", RefreshToken: "synthetic-refresh-1"}}
	switch string(in.Session) {
	case "synthetic-verifier":
		if in.Callback.Get("code") != "synthetic-code" {
			return Authorized{}, ErrAuthDenied
		}
		return done, nil
	case "synthetic-session-1":
		if in.Values["username"] != "synthetic-user" || in.Values["password"] != "synthetic-pass" {
			return Authorized{}, ErrAuthDenied
		}
		return Authorized{Next: &AuthStep{Prompt: &AuthPrompt{Message: "Enter the code", Fields: []AuthField{
			{Name: "code", Label: "Code", Kind: FieldCode},
		}}, Session: []byte("synthetic-session-2")}}, nil
	case "synthetic-session-2":
		if in.Values["code"] != "123456" {
			return Authorized{}, ErrAuthDenied
		}
		return done, nil
	}
	return Authorized{}, fmt.Errorf("%w: unexpected session", ErrPermanent)
}

// authEnv is setup's database with the fake_mfa provider registered (through
// register_provider, as a sidecar's), an owner session, and a runtime serving m.
type authEnv struct {
	*env
	m       *mfaFake
	rt      *Runtime
	session uuid.UUID
}

func setupAuth(t *testing.T, m *mfaFake) *authEnv {
	t.Helper()
	e := setup(t, &fake{})
	if err := e.d.Q().RegisterProvider(t.Context(), dbq.RegisterProviderParams{Code: mfaProvider, Name: "Fake MFA"}); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry(m)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := url.Parse("https://vitamux.example")
	a := &authEnv{env: e, m: m, session: uuid.New(),
		rt: New(Config{DB: e.d, Blobs: e.blobs, Keys: e.keys, Registry: reg, PublicURL: public})}
	e.exec(`INSERT INTO sessions (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, now() + interval '1 day')`,
		a.session, e.user, a.session[:])
	return a
}

func (a *authEnv) begin(t *testing.T) (AuthStep, string) {
	t.Helper()
	step, state, err := a.rt.BeginAuth(t.Context(), AuthRequest{UserID: a.user, SessionID: a.session, Provider: mfaProvider, Binding: testBinding})
	if err != nil {
		t.Fatal(err)
	}
	if step.Session != nil || state == "" {
		t.Fatalf("begin: session leaked (%v) or no state", step.Session != nil)
	}
	return step, state
}

func (a *authEnv) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	a.scan(sql, args, &n)
	return n
}

var signIn = map[string]string{"username": "synthetic-user", "password": "synthetic-pass"}

// begin → prompt → continue → MFA prompt → continue → a paused remote connection, each state
// single use, the connector's session sealed at rest and handed back, never returned.
func TestPromptAuthFlow(t *testing.T) {
	ctx := t.Context()
	a := setupAuth(t, &mfaFake{})

	step, state1 := a.begin(t)
	if step.RedirectURL != "" || step.Prompt == nil || len(step.Prompt.Fields) != 2 || step.Prompt.Fields[1].Kind != FieldPassword {
		t.Fatalf("first step: %+v", step)
	}
	var sealed []byte
	a.scan(`SELECT session FROM oauth_states`, nil, &sealed)
	if len(sealed) == 0 || bytes.Contains(sealed, []byte("synthetic-session-1")) {
		t.Fatal("state session is not sealed")
	}

	// A foreign browser is refused without using the state up.
	if _, _, _, err := a.rt.ContinueAuth(ctx, mfaProvider, state1, "other-browser", signIn); !errors.Is(err, ErrAuthState) {
		t.Fatalf("other binding: %v", err)
	}
	step, state2, id, err := a.rt.ContinueAuth(ctx, mfaProvider, state1, testBinding, signIn)
	if err != nil || id != uuid.Nil {
		t.Fatalf("credentials step: id %v, err %v", id, err)
	}
	if step.Prompt == nil || len(step.Prompt.Fields) != 1 || step.Prompt.Fields[0].Kind != FieldCode || step.Session != nil || state2 == state1 {
		t.Fatalf("MFA step: %+v", step)
	}
	if in := a.m.inputs[0]; string(in.Session) != "synthetic-session-1" || in.State != state2 || in.RedirectURL != "https://vitamux.example/oauth/fake_mfa/callback" {
		t.Fatalf("continue input: session %q, state matches %v, redirect %s", in.Session, in.State == state2, in.RedirectURL)
	}
	if _, _, _, err := a.rt.ContinueAuth(ctx, mfaProvider, state1, testBinding, signIn); !errors.Is(err, ErrAuthState) {
		t.Fatalf("replayed state: %v", err)
	}

	_, state, id, err := a.rt.ContinueAuth(ctx, mfaProvider, state2, testBinding, map[string]string{"code": "123456"})
	if err != nil || id == uuid.Nil || state != "" {
		t.Fatalf("code step: id %v, state %q, err %v", id, state, err)
	}
	if string(a.m.inputs[1].Session) != "synthetic-session-2" {
		t.Fatalf("second continue got session %q", a.m.inputs[1].Session)
	}
	if _, _, _, err := a.rt.ContinueAuth(ctx, mfaProvider, state2, testBinding, map[string]string{"code": "123456"}); !errors.Is(err, ErrAuthState) {
		t.Fatalf("replayed final state: %v", err)
	}
	if n := a.count(t, `SELECT count(*) FROM oauth_states`); n != 0 {
		t.Fatalf("%d state rows left", n)
	}

	// Unofficial: paused, schedules ensured, no first sync; mode remote with its upstream.
	var mode, status, pkg string
	a.scan(`SELECT mode, status, upstream->>'package' FROM connections WHERE id = $1`, []any{id}, &mode, &status, &pkg)
	if mode != "remote" || status != "paused" || pkg != "synthetic-collector" {
		t.Fatalf("connection: mode %s, status %s, upstream package %q", mode, status, pkg)
	}
	if n := a.count(t, `SELECT count(*) FROM jobs WHERE kind = $1 AND connection_id = $2`, jobs.KindSync, id); n != 0 {
		t.Fatalf("%d sync jobs queued for a paused connection", n)
	}
	if n := a.count(t, `SELECT count(*) FROM schedules WHERE connection_id = $1`, id); n != 1 {
		t.Fatalf("%d schedules, want 1", n)
	}
	if n := a.count(t, `SELECT count(*) FROM audit_events WHERE action = 'connection.connected' AND target_id = $1`, id.String()); n != 1 {
		t.Fatalf("%d connected audit events", n)
	}
	row, err := a.d.Q().GetCredentials(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if c, err := a.rt.creds.open(id, row.Ciphertext); err != nil || c.AccessToken != "synthetic-access-1" {
		t.Fatalf("stored credentials: err %v, access token stored %v", err, c.AccessToken == "synthetic-access-1")
	}
}

// Any failure ends the flow: the state is used up.
func TestPromptAuthFailureEndsFlow(t *testing.T) {
	a := setupAuth(t, &mfaFake{})
	_, state := a.begin(t)
	wrong := map[string]string{"username": "synthetic-user", "password": "wrong"}
	if _, _, _, err := a.rt.ContinueAuth(t.Context(), mfaProvider, state, testBinding, wrong); !errors.Is(err, ErrAuthDenied) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, _, _, err := a.rt.ContinueAuth(t.Context(), mfaProvider, state, testBinding, signIn); !errors.Is(err, ErrAuthState) {
		t.Fatalf("state after a failure: %v", err)
	}
	if n := a.count(t, `SELECT count(*) FROM connections WHERE mode = 'remote'`); n != 0 {
		t.Fatalf("%d connections after a failed flow", n)
	}
}

// An official connector's new connection is active and synced at once; reconnecting the same
// account reuses it.
func TestPromptAuthOfficialStartsActive(t *testing.T) {
	a := setupAuth(t, &mfaFake{official: true})
	connect := func() uuid.UUID {
		t.Helper()
		_, state := a.begin(t)
		_, state, _, err := a.rt.ContinueAuth(t.Context(), mfaProvider, state, testBinding, signIn)
		if err != nil {
			t.Fatal(err)
		}
		_, _, id, err := a.rt.ContinueAuth(t.Context(), mfaProvider, state, testBinding, map[string]string{"code": "123456"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	id := connect()
	var status string
	a.scan(`SELECT status FROM connections WHERE id = $1`, []any{id}, &status)
	if status != "active" || a.count(t, `SELECT count(*) FROM jobs WHERE kind = $1 AND connection_id = $2`, jobs.KindSync, id) != 1 {
		t.Fatalf("official connection: status %s, first sync missing", status)
	}
	if again := connect(); again != id {
		t.Fatal("reconnecting the same account made a second connection")
	}
}

// The provider callback gets back the session its redirect step stored (e.g. a PKCE verifier).
func TestRedirectStepSessionReachesCallback(t *testing.T) {
	a := setupAuth(t, &mfaFake{redirect: true, official: true})
	step, state := a.begin(t)
	if step.Prompt != nil || step.RedirectURL == "" {
		t.Fatalf("redirect step: %+v", step)
	}
	id, _, err := a.rt.CompleteAuth(t.Context(), mfaProvider, state, testBinding, url.Values{"state": {state}, "code": {"synthetic-code"}})
	if err != nil || id == uuid.Nil {
		t.Fatalf("callback: %v", err)
	}
	if got := string(a.m.inputs[0].Session); got != "synthetic-verifier" {
		t.Fatalf("callback session %q", got)
	}
}

// Credentials a fetch rotates are stored with its page, before the next page uses them; a page
// that fails to commit keeps the old ones.
func TestRotatedCredentialsPersistWithPage(t *testing.T) {
	rotated := Credentials{AccessToken: "synthetic-access-2", RefreshToken: "synthetic-refresh-2", ExpiresAt: time.Now().Add(time.Hour)}
	f := &fake{
		auth: AuthOAuth2,
		refresh: func(context.Context, Conn, Credentials) (Credentials, error) {
			return Credentials{}, errors.New("unexpected refresh")
		},
		fetch: func(_ context.Context, _ Conn, cred Credentials, u WorkUnit, out *RawSink) (FetchResult, error) {
			if u.Cursor == nil {
				out.Put(item("page1"))
				return FetchResult{NextCursor: []byte(`"2"`), Credentials: &rotated}, nil
			}
			if cred.AccessToken != rotated.AccessToken {
				return FetchResult{}, fmt.Errorf("%w: second page used stale credentials", ErrPermanent)
			}
			out.Put(item("page2"))
			return FetchResult{Done: true}, nil
		},
	}
	t.Run("stored", func(t *testing.T) {
		e := setup(t, f)
		e.saveCreds(t, Credentials{AccessToken: "synthetic-access-1", RefreshToken: "synthetic-refresh-1", ExpiresAt: time.Now().Add(time.Hour)})
		j := e.drive(t, e.rt, e.enqueue(t, jobs.SyncPayload{}), 1)
		if j.Status != "succeeded" || e.raw(t) != "page1:stored,page2:stored" {
			t.Fatalf("job %s, raw %q", j.Status, e.raw(t))
		}
		if c, v := e.storedCreds(t); c.RefreshToken != rotated.RefreshToken || v != 2 {
			t.Fatalf("credentials version %d, rotated stored %v", v, c.RefreshToken == rotated.RefreshToken)
		}
	})
	t.Run("page rolled back", func(t *testing.T) {
		e := setup(t, f)
		e.saveCreds(t, Credentials{AccessToken: "synthetic-access-1", RefreshToken: "synthetic-refresh-1", ExpiresAt: time.Now().Add(time.Hour)})
		rt := e.newRuntime()
		rt.beforeCommit = func() error { return errors.New("synthetic crash") }
		e.drive(t, rt, e.enqueue(t, jobs.SyncPayload{}), 1)
		if c, v := e.storedCreds(t); c.AccessToken != "synthetic-access-1" || v != 1 || e.raw(t) != "" {
			t.Fatalf("credentials version %d, raw %q", v, e.raw(t))
		}
	})
}
