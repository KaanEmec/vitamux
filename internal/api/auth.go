package api

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db"
)

func (rt *router) authRoutes() {
	rt.handle("POST /api/v1/auth/login", public, rt.login)
	rt.handle("POST /api/v1/auth/logout", session, rt.logout)
	rt.handle("GET /api/v1/auth/session", session, rt.session)
	rt.handle("POST /api/v1/auth/totp/enroll", session, rt.totpEnroll)
	rt.handle("POST /api/v1/auth/totp/confirm", session, rt.totpConfirm)
	rt.handle("POST /api/v1/auth/totp/disable", session, rt.totpDisable)
	rt.handle("POST /api/v1/api-keys", scope(auth.Admin), rt.createAPIKey)
	rt.handle("GET /api/v1/api-keys", scope(auth.Admin), rt.listAPIKeys)
	rt.handle("DELETE /api/v1/api-keys/{id}", scope(auth.Admin), rt.revokeAPIKey)
}

// sessionCookie is HttpOnly, SameSite=Strict and Secure; Secure is dropped only in
// development on a plain-http request, so a local dev server still works.
func (rt *router) sessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // Secure is computed; false only in development over http
		Name: auth.SessionCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: !rt.opts.Development || Scheme(r.Context()) != "http",
	}
}

type sessionResponse struct {
	User      auth.User `json:"user"`
	CSRFToken string    `json:"csrf_token"`
}

// appSessionResponse answers an app login; the token appears in this response only.
type appSessionResponse struct {
	User      auth.User `json:"user"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// loginClient checks the client and device_name of a login body and returns the device name
// of an app login ("" for the browser).
func loginClient(client, deviceName string) (string, []FieldError) {
	switch client {
	case "", "browser":
		if deviceName != "" {
			return "", []FieldError{{Pointer: "/device_name", Detail: "only with client app"}}
		}
		return "", nil
	case "app":
		name := strings.TrimSpace(deviceName)
		if name == "" || utf8.RuneCountInString(name) > auth.MaxDeviceNameLen || strings.ContainsFunc(name, unicode.IsControl) {
			return "", []FieldError{{Pointer: "/device_name", Detail: "must be 1 to 100 characters without control characters"}}
		}
		return name, nil
	default:
		return "", []FieldError{{Pointer: "/client", Detail: "must be browser or app"}}
	}
}

// login answers 429 with Retry-After while the username or address is locked out, rather
// than holding the connection open for the delay. An app login (client app) sets no cookie
// and answers the bearer token instead.
func (rt *router) login(w http.ResponseWriter, r *http.Request) {
	svc := rt.opts.Auth
	if svc == nil {
		writeProblem(w, r, CodeUnavailable, "sign-in is unavailable: the master key is not loaded")
		return
	}
	var in struct {
		Username     string `json:"username"`
		Password     string `json:"password"`
		TOTPCode     string `json:"totp_code"`
		RecoveryCode string `json:"recovery_code"`
		Client       string `json:"client"`
		DeviceName   string `json:"device_name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	device, errs := loginClient(in.Client, in.DeviceName)
	if len(errs) > 0 {
		writeProblem(w, r, CodeValidationFailed, "invalid sign-in request", errs...)
		return
	}
	login := auth.Login{
		Username: in.Username, Password: in.Password, TOTPCode: in.TOTPCode, RecoveryCode: in.RecoveryCode,
		ClientIP: ClientAddr(r.Context()), DeviceName: device,
	}
	if device == "" {
		login.PreviousToken = sessionToken(r)
	}
	ns, err := svc.Login(r.Context(), login)
	var throttled *auth.ThrottledError
	switch {
	case errors.As(err, &throttled):
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(throttled.RetryAfter.Seconds()))))
		writeProblem(w, r, CodeRateLimited, "too many failed sign-in attempts; try again later")
		return
	case errors.Is(err, auth.ErrTOTPRequired):
		writeProblem(w, r, CodeTOTPRequired, "enter the code from your authenticator app or a recovery code")
		return
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeProblem(w, r, CodeUnauthenticated, "invalid username, password or code")
		return
	case err != nil:
		rt.internal(w, r, "login", err)
		return
	}
	user, err := svc.User(r.Context(), ns.UserID)
	if err != nil {
		rt.internal(w, r, "login", err)
		return
	}
	if device != "" {
		writeJSON(rt.log, w, http.StatusOK, appSessionResponse{User: user, Token: ns.Token, ExpiresAt: ns.ExpiresAt})
		return
	}
	http.SetCookie(w, rt.sessionCookie(r, ns.Token, int(time.Until(ns.ExpiresAt).Seconds())))
	writeJSON(rt.log, w, http.StatusOK, sessionResponse{User: user, CSRFToken: svc.CSRFToken(ns.Token)})
}

// logout ends the calling session; only a browser session has a cookie to clear.
func (rt *router) logout(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFrom(r.Context())
	if err := rt.opts.Auth.Logout(r.Context(), p); err != nil {
		rt.internal(w, r, "logout", err)
		return
	}
	if !p.App {
		http.SetCookie(w, rt.sessionCookie(r, "", -1))
	}
	w.WriteHeader(http.StatusNoContent)
}

// session answers the user and, for a browser session, its CSRF token; an app session
// needs none, so its csrf_token is empty.
func (rt *router) session(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFrom(r.Context())
	user, err := rt.opts.Auth.User(r.Context(), p.UserID)
	if err != nil {
		rt.internal(w, r, "session", err)
		return
	}
	out := sessionResponse{User: user}
	if !p.App {
		out.CSRFToken = rt.opts.Auth.CSRFToken(sessionToken(r))
	}
	writeJSON(rt.log, w, http.StatusOK, out)
}

func (rt *router) totpEnroll(w http.ResponseWriter, r *http.Request) {
	e, err := rt.opts.Auth.EnrollTOTP(r.Context(), auth.PrincipalFrom(r.Context()).UserID)
	if errors.Is(err, auth.ErrTOTPEnabled) {
		writeProblem(w, r, CodeConflict, "TOTP is already enabled; disable it first")
		return
	}
	if err != nil {
		rt.internal(w, r, "totp enroll", err)
		return
	}
	writeJSON(rt.log, w, http.StatusOK, e)
}

func (rt *router) totpConfirm(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	codes, err := rt.opts.Auth.ConfirmTOTP(r.Context(), auth.PrincipalFrom(r.Context()), in.Code)
	switch {
	case errors.Is(err, auth.ErrTOTPEnabled), errors.Is(err, auth.ErrTOTPNotPending):
		writeProblem(w, r, CodeConflict, "there is no pending TOTP enrolment")
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeProblem(w, r, CodeValidationFailed, "the code does not match", FieldError{Pointer: "/code", Detail: "does not match"})
	case err != nil:
		rt.internal(w, r, "totp confirm", err)
	default:
		writeJSON(rt.log, w, http.StatusOK, map[string]any{"recovery_codes": codes})
	}
}

func (rt *router) totpDisable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password     string `json:"password"`
		TOTPCode     string `json:"totp_code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	err := rt.opts.Auth.DisableTOTP(r.Context(), auth.PrincipalFrom(r.Context()), in.Password, in.TOTPCode, in.RecoveryCode)
	switch {
	case errors.Is(err, auth.ErrTOTPNotEnabled):
		writeProblem(w, r, CodeConflict, "TOTP is not enabled")
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeProblem(w, r, CodeForbidden, "wrong password or code")
	case err != nil:
		rt.internal(w, r, "totp disable", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (rt *router) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string       `json:"name"`
		Scopes    []auth.Scope `json:"scopes"`
		ExpiresAt *time.Time   `json:"expires_at"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	var errs []FieldError
	if in.Name == "" || len(in.Name) > 100 {
		errs = append(errs, FieldError{Pointer: "/name", Detail: "must be 1 to 100 characters"})
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) {
		errs = append(errs, FieldError{Pointer: "/expires_at", Detail: "must be in the future"})
	}
	if len(errs) > 0 {
		writeProblem(w, r, CodeValidationFailed, "invalid API key", errs...)
		return
	}
	key, token, err := rt.opts.Auth.CreateAPIKey(r.Context(), auth.PrincipalFrom(r.Context()), in.Name, in.Scopes, in.ExpiresAt)
	if errors.Is(err, auth.ErrBadScopes) {
		writeProblem(w, r, CodeValidationFailed, "invalid API key", FieldError{Pointer: "/scopes", Detail: "must be a non-empty list of known scopes"})
		return
	}
	if err != nil {
		rt.internal(w, r, "create api key", err)
		return
	}
	// The token appears in this response only.
	writeJSON(rt.log, w, http.StatusCreated, struct {
		auth.APIKeyInfo
		Token string `json:"token"`
	}{key, token})
}

func (rt *router) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := rt.opts.Auth.ListAPIKeys(r.Context(), auth.PrincipalFrom(r.Context()).UserID)
	if err != nil {
		rt.internal(w, r, "list api keys", err)
		return
	}
	writeJSON(rt.log, w, http.StatusOK, map[string]any{"api_keys": keys})
}

func (rt *router) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err == nil {
		err = rt.opts.Auth.RevokeAPIKey(r.Context(), auth.PrincipalFrom(r.Context()), id)
	} else {
		err = db.ErrNotFound
	}
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeProblem(w, r, CodeNotFound, "no active API key with this id")
	case err != nil:
		rt.internal(w, r, "revoke api key", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// readJSON decodes one JSON object into v, answering the problem itself when it cannot.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if tooLarge := (*http.MaxBytesError)(nil); errors.As(err, &tooLarge) || errors.Is(err, errJSONTooDeep) {
			writeBodyError(w, r, err)
		} else {
			writeProblem(w, r, CodeValidationFailed, "request body must be a JSON object with the documented fields")
		}
		return false
	}
	return true
}

// internal logs err (never request data) and answers 500.
func (rt *router) internal(w http.ResponseWriter, r *http.Request, op string, err error) {
	rt.log.ErrorContext(r.Context(), op, "request_id", requestIDFrom(r.Context()), "err", err)
	writeProblem(w, r, CodeInternal, "")
}
