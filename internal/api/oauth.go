package api

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

// OAuth connection flow (docs/architecture/connectors.md#oauth-connection-flow): begin for a
// new account (by provider) or to reauthorize a connection (conn_ id); disconnecting is
// DELETE /connections/{id} in connections.go. Begin needs the owner session,
// because the state is bound to it. The provider callback is a public
// route: the session cookie (SameSite=Strict) does not come back on the provider's redirect,
// so the signed single-use state and the browser-binding cookie authorize it.
func (rt *router) oauthRoutes() {
	rt.handle("POST /api/v1/providers/{provider}/auth/begin", session, rt.authBegin)
	rt.handle("POST /api/v1/connections/{id}/auth/begin", session, rt.authBegin)
	rt.handle("GET /oauth/{provider}/callback", public, rt.oauthCallback)
}

// oauthCookie binds a pending authorization to the browser that began it. SameSite=Lax so the
// provider's top-level redirect carries it; scoped to /oauth/ and short-lived.
const oauthCookie = "vitamux_oauth"

func (rt *router) oauthBindingCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // Secure is computed; false only in development over http
		Name: oauthCookie, Value: value, Path: "/oauth/", MaxAge: maxAge, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: !rt.opts.Development || Scheme(r.Context()) != "http",
	}
}

func (rt *router) authBegin(w http.ResponseWriter, r *http.Request) {
	if rt.opts.Connectors == nil {
		writeProblem(w, r, CodeUnavailable, "connections are unavailable: the master key or data directory is missing")
		return
	}
	req := connectors.AuthRequest{Provider: r.PathValue("provider")}
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
	p := auth.PrincipalFrom(r.Context())
	req.UserID, req.SessionID, req.Binding = p.UserID, p.ID, base64.RawURLEncoding.EncodeToString(b[:])
	target, err := rt.opts.Connectors.BeginAuth(r.Context(), req)
	switch {
	case errors.Is(err, connectors.ErrAuthUnavailable):
		writeProblem(w, r, CodeUnavailable, "authorization is not available for this provider: unknown, or its client id and secret are not configured")
	case errors.Is(err, db.ErrNotFound):
		writeProblem(w, r, CodeNotFound, "no such connection")
	case err != nil:
		rt.internal(w, r, "begin authorization", err)
	default:
		http.SetCookie(w, rt.oauthBindingCookie(r, req.Binding, int(connectors.StateTTL.Seconds())))
		writeJSON(rt.log, w, http.StatusOK, map[string]string{"redirect_url": target})
	}
}

// oauthCallback completes the flow and sends the browser back to the UI with the outcome:
// /connections?connected=<provider> or /connections?auth_error=<reason>. HEAD answers 204
// and touches nothing, so a probe never uses up a state. A provider without a connector is 404.
func (rt *router) oauthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusNoContent)
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
	http.SetCookie(w, rt.oauthBindingCookie(r, "", -1))
	reason := "unavailable"
	if rt.opts.Connectors != nil {
		q := r.URL.Query()
		_, err := rt.opts.Connectors.CompleteAuth(r.Context(), provider, q.Get("state"), binding, q)
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
	v := url.Values{}
	if reason == "" {
		v.Set("connected", provider)
	} else {
		v.Set("auth_error", reason)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/connections?"+v.Encode(), http.StatusSeeOther)
}
