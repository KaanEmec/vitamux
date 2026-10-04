//go:build integration

package withings

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/jobs"
	fp "github.com/KaanEmec/vitamux/internal/testutil/fakeprovider"
)

const binding = "synthetic-binding"

// begin starts an authorization and returns the state from the consent URL.
func (e *env) begin(t *testing.T, conn *uuid.UUID) string {
	t.Helper()
	step, state, err := e.rt.BeginAuth(t.Context(), connectors.AuthRequest{
		UserID: e.user, SessionID: e.session, Provider: Provider, ConnectionID: conn, Binding: binding,
	})
	if err != nil {
		t.Fatal(err)
	}
	if step.Prompt != nil || step.Session != nil {
		t.Fatalf("withings begins with a redirect only: %+v", step)
	}
	u, err := url.Parse(step.RedirectURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Path != "/oauth2_user/authorize2" || q.Get("response_type") != "code" || q.Get("client_id") != clientID ||
		q.Get("scope") != "user.metrics,user.activity" || q.Get("redirect_uri") != callback || q.Get("state") != state {
		t.Fatalf("consent URL has wrong parameters: path %s, keys %v", u.Path, keys(q))
	}
	return q.Get("state")
}

func keys(v url.Values) []string {
	var out []string
	for k := range v {
		out = append(out, k)
	}
	return out
}

// exchange scripts the code exchange; userid is sent as given (number or string).
func exchange(code string, userid any, access, refresh string) fp.Step {
	return fp.Step{
		Method: http.MethodPost, Path: "/v2/oauth2",
		Form: url.Values{"action": {"requesttoken"}, "grant_type": {"authorization_code"}, "code": {code},
			"client_id": {clientID}, "client_secret": {clientSecret}, "redirect_uri": {callback}},
		Reply: fp.JSON(http.StatusOK, map[string]any{"status": 0, "body": map[string]any{
			"userid": userid, "access_token": access, "refresh_token": refresh, "expires_in": 10800,
			"scope": "user.metrics,user.activity", "token_type": "Bearer",
		}}),
	}
}

func (e *env) complete(state, code string) (uuid.UUID, error) {
	cb := url.Values{"state": {state}}
	if code != "" {
		cb.Set("code", code)
	} else {
		cb.Set("error", "access_denied")
	}
	return e.rt.CompleteAuth(context.Background(), Provider, state, binding, cb)
}

func TestOAuthLifecycle(t *testing.T) {
	ctx := t.Context()
	fake := fp.New(t)
	e := setup(t, fake.URL)
	accountKey := sha256.Sum256([]byte("1234567"))

	// Connect: a new connection keyed by the hashed Withings userid, sealed credentials,
	// default schedules, one first sync, an audit event.
	state := e.begin(t, nil)
	fake.Expect(exchange("synthetic-code-1", 1234567, "synthetic-access-1", "synthetic-refresh-1"))
	id, err := e.complete(state, "synthetic-code-1")
	if err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM connections WHERE id = $1 AND account_key = $2 AND status = 'active' AND mode = 'in_process'`, id, accountKey[:]); n != 1 {
		t.Fatal("connection not stored with the account key")
	}
	if n := e.count(`SELECT count(*) FROM credentials WHERE connection_id = $1 AND position('synthetic'::bytea IN ciphertext) = 0`, id); n != 1 {
		t.Fatal("credentials missing or not sealed")
	}
	if n := e.count(`SELECT count(*) FROM schedules WHERE connection_id = $1`, id); n != 8 {
		t.Fatalf("%d schedules, want incremental and correction for each of the 4 streams", n)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE connection_id = $1 AND kind = $2 AND payload->>'mode' = 'manual'`, id, jobs.KindSync); n != 4 {
		t.Fatalf("%d first syncs queued, want one per stream", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_events WHERE action = 'connection.connected' AND target_id = $1`, id.String()); n != 1 {
		t.Fatal("connect not audited")
	}

	// Replayed state: refused before any provider call (the fake has nothing scripted).
	if _, err := e.complete(state, "synthetic-code-1"); !errors.Is(err, connectors.ErrAuthState) {
		t.Fatalf("replay: %v", err)
	}

	// Foreign browser: wrong binding is refused and does not use up the state.
	state = e.begin(t, nil)
	if _, err := e.rt.CompleteAuth(ctx, Provider, state, "other-browser", url.Values{"code": {"x"}}); !errors.Is(err, connectors.ErrAuthState) {
		t.Fatalf("foreign binding: %v", err)
	}
	if _, err := e.rt.CompleteAuth(ctx, Provider, state+"x", binding, url.Values{"code": {"x"}}); !errors.Is(err, connectors.ErrAuthState) {
		t.Fatalf("tampered state: %v", err)
	}

	// Reconnect the same account (userid as a string this time): same connection, new tokens.
	fake.Expect(exchange("synthetic-code-2", "1234567", "synthetic-access-2", "synthetic-refresh-2"))
	if again, err := e.complete(state, "synthetic-code-2"); err != nil || again != id {
		t.Fatalf("reconnect: %v, same connection %v", err, again == id)
	}
	if n := e.count(`SELECT count(*) FROM connections`); n != 1 {
		t.Fatalf("%d connections after reconnect", n)
	}
	if n := e.count(`SELECT version FROM credentials WHERE connection_id = $1`, id); n != 2 {
		t.Fatalf("credentials version %d after reconnect", n)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE connection_id = $1 AND kind = $2`, id, jobs.KindSync); n != 4 {
		t.Fatalf("%d syncs queued, want one per stream: the first-sync dedupe must hold", n)
	}

	// A state dies with its session (logout) and with its expiry.
	state = e.begin(t, nil)
	e.exec(`DELETE FROM sessions WHERE id = $1`, e.session)
	if _, err := e.complete(state, "synthetic-code-x"); !errors.Is(err, connectors.ErrAuthState) {
		t.Fatalf("ended session: %v", err)
	}
	e.newSession()
	state = e.begin(t, nil)
	e.exec(`UPDATE oauth_states SET expires_at = now() - interval '1 second'`)
	if _, err := e.complete(state, "synthetic-code-x"); !errors.Is(err, connectors.ErrAuthState) {
		t.Fatalf("expired state: %v", err)
	}

	// Declined at Withings: the state is used up.
	state = e.begin(t, nil)
	if _, err := e.complete(state, ""); !errors.Is(err, connectors.ErrAuthDenied) {
		t.Fatalf("denied: %v", err)
	}
	if _, err := e.complete(state, "synthetic-code-x"); !errors.Is(err, connectors.ErrAuthState) {
		t.Fatalf("state reused after denial: %v", err)
	}

	// Reauthorizing with another Withings account changes nothing.
	e.exec(`UPDATE connections SET status = 'needs_reauth', last_error_class = 'reauth_required' WHERE id = $1`, id)
	state = e.begin(t, &id)
	fake.Expect(exchange("synthetic-code-3", 7654321, "synthetic-access-3", "synthetic-refresh-3"))
	if _, err := e.complete(state, "synthetic-code-3"); !errors.Is(err, connectors.ErrAccountMismatch) {
		t.Fatalf("other account: %v", err)
	}
	if n := e.count(`SELECT version FROM credentials WHERE connection_id = $1`, id); n != 2 {
		t.Fatal("credentials changed by a mismatched reauthorization")
	}

	// Reauthorizing with the right account reactivates the connection.
	state = e.begin(t, &id)
	fake.Expect(exchange("synthetic-code-4", 1234567, "synthetic-access-4", "synthetic-refresh-4"))
	if _, err := e.complete(state, "synthetic-code-4"); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM connections WHERE id = $1 AND status = 'active' AND last_error_class IS NULL`, id); n != 1 {
		t.Fatal("reauthorized connection is not active")
	}

	// Disconnect keeps the row and its data, drops the credentials; reconnecting revives it.
	if err := e.rt.Disconnect(ctx, uuid.New(), id, "owner"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("disconnect by another user: %v", err)
	}
	if err := e.rt.Disconnect(ctx, e.user, id, "owner"); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM connections c WHERE id = $1 AND status = 'disabled' AND NOT EXISTS (SELECT 1 FROM credentials WHERE connection_id = c.id)`, id); n != 1 {
		t.Fatal("disconnect did not disable and drop credentials")
	}
	state = e.begin(t, nil)
	fake.Expect(exchange("synthetic-code-5", 1234567, "synthetic-access-5", "synthetic-refresh-5"))
	if again, err := e.complete(state, "synthetic-code-5"); err != nil || again != id {
		t.Fatalf("reconnect after disconnect: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM connections WHERE status = 'active'`); n != 1 {
		t.Fatal("reconnect after disconnect did not reactivate")
	}

	// Begin refuses unknown providers and other users' connections.
	if _, _, err := e.rt.BeginAuth(ctx, connectors.AuthRequest{UserID: e.user, SessionID: e.session, Provider: "oura", Binding: binding}); !errors.Is(err, connectors.ErrAuthUnavailable) {
		t.Fatalf("unknown provider: %v", err)
	}
	if _, _, err := e.rt.BeginAuth(ctx, connectors.AuthRequest{UserID: uuid.New(), SessionID: e.session, ConnectionID: &id, Binding: binding}); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("foreign connection: %v", err)
	}
}

// A refused access token is refreshed once through the runtime's single-flight refresh; the
// rotated pair is stored before use. A refused refresh token puts the connection in needs_reauth.
func TestRefreshRotation(t *testing.T) {
	ctx := t.Context()
	fake := fp.New(t)
	e := setup(t, fake.URL)
	state := e.begin(t, nil)
	fake.Expect(exchange("synthetic-code-1", 1234567, "synthetic-access-1", "synthetic-refresh-1"))
	id, err := e.complete(state, "synthetic-code-1")
	if err != nil {
		t.Fatal(err)
	}
	empty := map[string]any{"status": 0, "body": map[string]any{"updatetime": 1767225600, "timezone": "Europe/Berlin", "measuregrps": []any{}, "more": 0, "offset": 0}}
	refresh := func(old, access, next string) fp.Step {
		return fp.Step{Method: http.MethodPost, Path: "/v2/oauth2",
			Form: url.Values{"action": {"requesttoken"}, "grant_type": {"refresh_token"}, "refresh_token": {old}},
			Reply: fp.JSON(http.StatusOK, map[string]any{"status": 0, "body": map[string]any{
				"userid": 1234567, "access_token": access, "refresh_token": next, "expires_in": 10800}})}
	}
	fake.Expect(
		fp.Step{Method: http.MethodPost, Path: "/measure", Header: map[string]string{"Authorization": "Bearer synthetic-access-1"},
			Reply: fp.JSON(http.StatusOK, map[string]any{"status": 401, "error": "invalid_token"})},
		refresh("synthetic-refresh-1", "synthetic-access-2", "synthetic-refresh-2"),
		fp.Step{Method: http.MethodPost, Path: "/measure", Header: map[string]string{"Authorization": "Bearer synthetic-access-2"},
			Form: url.Values{"action": {"getmeas"}, "lastupdate": {"0"}, "category": {"1"}}, Reply: fp.JSON(http.StatusOK, empty)},
	)
	e.exec(`DELETE FROM jobs WHERE payload->>'stream' <> $1`, StreamMeasures) // the script serves measures only
	e.run(t)                                                                  // the first sync queued by the connect
	e.allSucceeded(t)
	if n := e.count(`SELECT version FROM credentials WHERE connection_id = $1`, id); n != 2 {
		t.Fatalf("credentials version %d, want the rotated pair stored", n)
	}
	if n := e.count(`SELECT count(*) FROM sync_cursors WHERE connection_id = $1 AND cursor->>'lastupdate' = '1767225600'`, id); n != 1 {
		t.Fatal("cursor not advanced to updatetime")
	}

	// Expired access token and a revoked refresh token: needs_reauth, no retries.
	if err := e.d.Tx(ctx, func(q *dbq.Queries) error {
		return e.rt.SaveCredentials(ctx, q, id, connectors.Credentials{
			AccessToken: "synthetic-access-2", RefreshToken: "synthetic-refresh-2", ExpiresAt: time.Now().Add(-time.Hour)})
	}); err != nil {
		t.Fatal(err)
	}
	fake.Expect(fp.Step{Method: http.MethodPost, Path: "/v2/oauth2", Form: url.Values{"refresh_token": {"synthetic-refresh-2"}},
		Reply: fp.JSON(http.StatusOK, map[string]any{"status": 503, "error": "Invalid Params: invalid refresh_token"})})
	if _, _, err := jobs.Enqueue(ctx, e.d.Q(), jobs.NewJob{Kind: jobs.KindSync, ConnectionID: &id, Exclusive: true,
		Payload: jobs.SyncPayload{Stream: StreamMeasures, Mode: connectors.ModeIncremental, Slot: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	e.run(t)
	if n := e.count(`SELECT count(*) FROM connections WHERE id = $1 AND status = 'needs_reauth'`, id); n != 1 {
		t.Fatal("revoked refresh token did not lead to needs_reauth")
	}
}
