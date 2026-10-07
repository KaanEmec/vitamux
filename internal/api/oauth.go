package api

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

// Connection authorization flow (docs/architecture/connectors.md#oauth-connection-flow): begin
// for a new account (by provider) or to reauthorize a connection (conn_ id), then the provider
// callback after a redirect step or continue after a prompt step; disconnecting is
// DELETE /connections/{id} in connections.go. Begin and continue need the owner session,
// because the state is bound to it. The provider callback is a public
// route: the session cookie (SameSite=Strict) does not come back on the provider's redirect,
// so the signed single-use state and the browser-binding cookie authorize it. An app session
// begins with {"return": "app"}: its redirect goes through the public start route, which sets
// the binding cookie in the app's auth browser, and the callback returns to appReturnURL.
func (rt *router) oauthRoutes() {
	rt.handle("POST /api/v1/providers/{provider}/auth/begin", session, rt.authBegin)
	rt.handle("POST /api/v1/connections/{id}/auth/begin", session, rt.authBegin)
	rt.handle("POST /api/v1/providers/{provider}/auth/continue", session, rt.authContinue)
	rt.handle("GET /oauth/{provider}/start", public, rt.oauthStart)
	rt.handle("GET /oauth/{provider}/callback", public, rt.oauthCallback)
}

// appReturnURL is where an app-originated flow ends (ADR-0023). It is a constant, never taken
// from the request, so neither the start route nor the callback is an open redirect.
const appReturnURL = "vitamux://connections"

// oauthCookie binds a pending authorization to the browser that began it. SameSite=Lax so the
// provider's top-level redirect carries it; short-lived and scoped to the two places that read
// it: the callback (/oauth/) and continue (/api/v1/providers/).
const oauthCookie = "vitamux_oauth"

const (
	callbackCookiePath = "/oauth/"
	continueCookiePath = "/api/v1/providers/"

	maxPromptValues = 16   // api/openapi.yaml AuthContinueInput
	maxPromptValue  = 4096 // bytes
)

func (rt *router) oauthBindingCookie(r *http.Request, path, value string, maxAge int) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // Secure is computed; false only in development over http
		Name: oauthCookie, Value: value, Path: path, MaxAge: maxAge, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: !rt.opts.Development || Scheme(r.Context()) != "http",
	}
}

// setBinding (re)sets the binding cookie for every path a later step may need.
func (rt *router) setBinding(w http.ResponseWriter, r *http.Request, binding string) {
	for _, p := range []string{callbackCookiePath, continueCookiePath} {
		http.SetCookie(w, rt.oauthBindingCookie(r, p, binding, int(connectors.StateTTL.Seconds())))
	}
}

// stepBody is an auth step as the API shows it: a redirect, or a prompt with its state.
// AuthStep.Session never leaves the server (the runtime clears it).
func stepBody(step connectors.AuthStep, state string) any {
	if step.Prompt == nil {
		return map[string]string{"redirect_url": step.RedirectURL}
	}
	fields := make([]map[string]string, len(step.Prompt.Fields))
	for i, f := range step.Prompt.Fields {
		fields[i] = map[string]string{"name": f.Name, "label": f.Label, "kind": f.Kind}
	}
	return map[string]any{"state": state, "prompt": map[string]any{"message": step.Prompt.Message, "fields": fields}}
}

func (rt *router) authBegin(w http.ResponseWriter, r *http.Request) {
	if rt.opts.Connectors == nil {
		writeProblem(w, r, CodeUnavailable, "connections are unavailable: the master key or data directory is missing")
		return
	}
	var body struct {
		Return string `json:"return"`
	}
	if !readOptionalJSON(w, r, &body) {
		return
	}
	p := auth.PrincipalFrom(r.Context())
	switch body.Return {
	case "", "browser":
	case "app":
		if !p.App {
			writeProblem(w, r, CodeForbidden, `"return": "app" needs an app session`)
			return
		}
	default:
		writeProblem(w, r, CodeValidationFailed, "unknown return", FieldError{Pointer: "/return", Detail: "browser or app"})
		return
	}
	req := connectors.AuthRequest{Provider: r.PathValue("provider"), App: body.Return == "app"}
	if v := r.PathValue("id"); v != "" {
		id, err := ingest.ParseConnectionID(v)
		if err != nil {
			writeProblem(w, r, CodeNotFound, "no such connection")
			return
		}
		req.ConnectionID = &id
	}
	var b [32]byte
	_, _ = rand.Read(b[:])
	req.UserID, req.SessionID, req.Binding = p.UserID, p.ID, base64.RawURLEncoding.EncodeToString(b[:])
	step, state, err := rt.opts.Connectors.BeginAuth(r.Context(), req)
	switch {
	case errors.Is(err, connectors.ErrAuthUnavailable):
		writeProblem(w, r, CodeUnavailable, "authorization is not available for this provider: unknown, or its client id and secret are not configured")
	case errors.Is(err, db.ErrNotFound):
		writeProblem(w, r, CodeNotFound, "no such connection")
	case err != nil:
		rt.internal(w, r, "begin authorization", err)
	default:
		rt.setBinding(w, r, req.Binding)
		writeJSON(rt.log, w, http.StatusOK, stepBody(step, state))
	}
}

// authContinue answers a prompt step: the next step, or the connection once authorized. Any
// failure ends the flow (the state is used up) and clears the binding cookie.
func (rt *router) authContinue(w http.ResponseWriter, r *http.Request) {
	if rt.opts.Connectors == nil {
		writeProblem(w, r, CodeUnavailable, "connections are unavailable: the master key or data directory is missing")
		return
	}
	var body struct {
		State  string            `json:"state"`
		Values map[string]string `json:"values"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	tooLong := false
	for _, v := range body.Values {
		tooLong = tooLong || len(v) > maxPromptValue
	}
	if tooLong || len(body.Values) > maxPromptValues {
		writeProblem(w, r, CodeValidationFailed, "too many or too long values", FieldError{Pointer: "/values", Detail: "at most 16 values of 4096 bytes"})
		return
	}
	binding := ""
	if c, err := r.Cookie(oauthCookie); err == nil {
		binding = c.Value
	}
	provider := r.PathValue("provider")
	step, state, id, err := rt.opts.Connectors.ContinueAuth(r.Context(), provider, body.State, binding, body.Values)
	if err == nil && id == uuid.Nil {
		rt.setBinding(w, r, binding)
		writeJSON(rt.log, w, http.StatusOK, stepBody(step, state))
		return
	}
	http.SetCookie(w, rt.oauthBindingCookie(r, continueCookiePath, "", -1))
	var rl *connectors.RateLimitedError
	switch {
	case err == nil:
		writeJSON(rt.log, w, http.StatusOK, map[string]string{"connection_id": ingest.FormatConnectionID(id)})
	case errors.Is(err, connectors.ErrAuthUnavailable):
		writeProblem(w, r, CodeUnavailable, "authorization is not available for this provider")
	case errors.Is(err, connectors.ErrAuthState):
		writeProblem(w, r, CodeValidationFailed, "the authorization expired or was already used; begin again",
			FieldError{Pointer: "/state", Detail: "invalid, expired or used"})
	case errors.Is(err, connectors.ErrAuthDenied):
		writeProblem(w, r, CodeValidationFailed, "the provider declined the authorization; begin again")
	case errors.Is(err, connectors.ErrAccountMismatch):
		writeProblem(w, r, CodeConflict, "signed in to another provider account than the connection's")
	case errors.As(err, &rl):
		retryAfter(w, max(rl.RetryAfter, time.Second))
		writeProblem(w, r, CodeRateLimited, "the provider is limiting sign-in attempts; wait, then begin again")
	case errors.Is(err, connectors.ErrReauthRequired):
		// A wrong password or code, or an expired sign-in at the provider.
		writeProblem(w, r, CodeValidationFailed, "the provider did not accept the sign-in details or the code; begin again")
	default:
		// Provider or sidecar failures; the error never includes the owner's values.
		rt.log.WarnContext(r.Context(), "auth continue", "provider", provider, "request_id", requestIDFrom(r.Context()), "err", err)
		writeProblem(w, r, CodeUnavailable, "the provider did not complete the authorization; begin again")
	}
}

// oauthStart uses the single-use ticket of an app redirect step: it sets the binding cookie in
// the auth browser and sends it on to the provider URL stored with the ticket. A bad, used or
// expired ticket ends at appReturnURL with an auth_error. HEAD answers 200 and touches
// nothing, so a probe never uses up a ticket.
func (rt *router) oauthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	provider := r.PathValue("provider")
	if rt.opts.Connectors != nil && !rt.opts.Connectors.HasProvider(provider) {
		writeProblem(w, r, CodeNotFound, "no such provider")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	reason := "unavailable"
	if rt.opts.Connectors != nil {
		target, binding, err := rt.opts.Connectors.StartAuth(r.Context(), provider, r.URL.Query().Get("ticket"))
		switch {
		case err == nil:
			http.SetCookie(w, rt.oauthBindingCookie(r, callbackCookiePath, binding, int(connectors.StateTTL.Seconds())))
			// security: not an open redirect; target is the provider URL BeginAuth stored with the ticket.
			http.Redirect(w, r, target, http.StatusSeeOther) //nolint:gosec // G710: the ticket only selects a server-stored URL
			return
		case errors.Is(err, connectors.ErrAuthState):
			reason = "invalid_state"
		case errors.Is(err, connectors.ErrAuthUnavailable):
		default:
			rt.log.WarnContext(r.Context(), "oauth start", "provider", provider, "request_id", requestIDFrom(r.Context()), "err", err)
		}
	}
	http.Redirect(w, r, authOutcome(appReturnURL, provider, reason), http.StatusSeeOther)
}

// authOutcome is the URL a finished flow returns to: base?connected=<provider>, or
// base?auth_error=<reason>&provider=<provider>.
func authOutcome(base, provider, reason string) string {
	v := url.Values{}
	if reason == "" {
		v.Set("connected", provider)
	} else {
		v.Set("auth_error", reason)
		v.Set("provider", provider)
	}
	return base + "?" + v.Encode()
}

// oauthCallback completes the flow and sends the browser back to the UI with the outcome:
// /connections?connected=<provider> or /connections?auth_error=<reason>&provider=<provider>,
// or the same query on appReturnURL for an app-originated state. HEAD answers 200
// (the Withings dashboard test refuses 204) and touches nothing, so a probe never uses up a state. A provider without a connector is 404.
func (rt *router) oauthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	provider := r.PathValue("provider")
	if rt.opts.Connectors != nil && !rt.opts.Connectors.HasProvider(provider) {
		writeProblem(w, r, CodeNotFound, "no such provider")
		return
	}
	binding := ""
	if c, err := r.Cookie(oauthCookie); err == nil {
		binding = c.Value
	}
	http.SetCookie(w, rt.oauthBindingCookie(r, callbackCookiePath, "", -1))
	reason, back := "unavailable", "/connections"
	if rt.opts.Connectors != nil {
		q := r.URL.Query()
		_, app, err := rt.opts.Connectors.CompleteAuth(r.Context(), provider, q.Get("state"), binding, q)
		if app {
			back = appReturnURL
		}
		switch {
		case err == nil:
			reason = ""
		case errors.Is(err, connectors.ErrAuthState):
			reason = "invalid_state"
		case errors.Is(err, connectors.ErrAuthDenied):
			reason = "denied"
		case errors.Is(err, connectors.ErrAccountMismatch):
			reason = "account_mismatch"
		case errors.Is(err, connectors.ErrAuthUnavailable):
		default:
			reason = "exchange_failed"
			rt.log.WarnContext(r.Context(), "oauth callback", "provider", provider, "request_id", requestIDFrom(r.Context()), "err", err)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, authOutcome(back, provider, reason), http.StatusSeeOther)
}
