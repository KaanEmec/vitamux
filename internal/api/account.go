package api

import (
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db"
)

// Owner account: password change and session management. Session only, like the rest of
// the sign-in state: an API key cannot change the password or end browser sessions.
func (rt *router) accountRoutes() {
	rt.handle("POST /api/v1/auth/password", session, rt.changePassword)
	rt.handle("GET /api/v1/auth/sessions", session, rt.listSessions)
	rt.handle("DELETE /api/v1/auth/sessions/{id}", session, rt.revokeSession)
}

func (rt *router) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	_, err := rt.opts.Auth.ChangePassword(r.Context(), auth.PrincipalFrom(r.Context()), in.CurrentPassword, in.NewPassword)
	var throttled *auth.ThrottledError
	switch {
	case errors.As(err, &throttled):
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(throttled.RetryAfter.Seconds()))))
		writeProblem(w, r, CodeRateLimited, "too many wrong passwords; try again later")
	case errors.Is(err, auth.ErrWeakPassword):
		writeProblem(w, r, CodeValidationFailed, "invalid new password",
			FieldError{Pointer: "/new_password", Detail: "must be at least " + strconv.Itoa(auth.MinPasswordLen) + " characters"})
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeProblem(w, r, CodeValidationFailed, "the current password is wrong",
			FieldError{Pointer: "/current_password", Detail: "does not match"})
	case err != nil:
		rt.internal(w, r, "change password", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (rt *router) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := rt.opts.Auth.ListSessions(r.Context(), auth.PrincipalFrom(r.Context()))
	if err != nil {
		rt.internal(w, r, "list sessions", err)
		return
	}
	writeJSON(rt.log, w, http.StatusOK, map[string]any{"sessions": sessions})
}

// revokeSession ends one session; ending the caller's own also clears its cookie.
func (rt *router) revokeSession(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err == nil {
		err = rt.opts.Auth.RevokeSession(r.Context(), p, id)
	} else {
		err = db.ErrNotFound
	}
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeProblem(w, r, CodeNotFound, "no session with this id")
	case err != nil:
		rt.internal(w, r, "revoke session", err)
	default:
		if id == p.ID {
			http.SetCookie(w, rt.sessionCookie(r, "", -1))
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
