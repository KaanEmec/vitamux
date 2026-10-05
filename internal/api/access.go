package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/auth"
)

// access is what a route demands of its caller, declared when the route is registered.
// The zero value admits nobody.
type access struct {
	public  bool       // no credentials needed
	session bool       // an owner session only (account and login state)
	scope   auth.Scope // an owner session, or an API key with this scope (or admin)
	ingest  bool       // a client token; a {connection} path value must be its connection
}

var (
	public  = access{public: true}
	session = access{session: true}
)

func scope(s auth.Scope) access { return access{scope: s} }

type route struct {
	pattern string
	access  access
}

// handle registers an API route behind its access check. Every /api route goes through here.
func (rt *router) handle(pattern string, a access, h http.HandlerFunc) {
	rt.routes = append(rt.routes, route{pattern, a})
	rt.mux.Handle(pattern, rt.authorize(a, h))
}

const csrfHeader = "X-CSRF-Token"

// authorize enforces a route's access: 401 without a principal, 403 for the wrong kind
// of credential or scope, and 403 for a cookie session request that changes state without
// the CSRF header. Bearer tokens, app sessions included, need no CSRF token: browsers never
// attach them on their own.
func (rt *router) authorize(a access, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.public {
			next(w, r)
			return
		}
		p := auth.PrincipalFrom(r.Context())
		if p == nil {
			writeProblem(w, r, CodeUnauthenticated, "sign in or send a bearer token")
			return
		}
		ok := false
		switch {
		case a.session:
			ok = p.Kind == auth.OwnerSession
		case a.ingest:
			ok = p.Kind == auth.Client
			if c := r.PathValue("connection"); c != "" {
				id, err := uuid.Parse(c)
				ok = err == nil && p.CanIngest(id)
			}
		case a.scope != "":
			ok = p.Can(a.scope)
		}
		if !ok {
			writeProblem(w, r, CodeForbidden, "this credential may not use this endpoint")
			return
		}
		if p.Kind == auth.OwnerSession && !p.App && !safeMethod(r.Method) && !rt.opts.Auth.CheckCSRF(sessionToken(r), r.Header.Get(csrfHeader)) {
			writeProblem(w, r, CodeForbidden, "missing or invalid "+csrfHeader+" header")
			return
		}
		next(w, r)
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func sessionToken(r *http.Request) string {
	if c, err := r.Cookie(auth.SessionCookie); err == nil {
		return c.Value
	}
	return ""
}

// authenticate sets the principal of /api requests from `Authorization: Bearer` (API key,
// client token or app session) or the session cookie. A bearer token that does not verify is refused at once; a stale session
// cookie only leaves the request anonymous, so login still works. Routes decide whether
// anonymous is enough (authorize).
func authenticate(log *slog.Logger, svc *auth.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if svc == nil || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		var p *auth.Principal
		var err error
		if h := r.Header.Get("Authorization"); h != "" {
			scheme, token, _ := strings.Cut(h, " ")
			if !strings.EqualFold(scheme, "Bearer") {
				writeProblem(w, r, CodeUnauthenticated, "unsupported authorization scheme")
				return
			}
			if p, err = svc.Bearer(r.Context(), strings.TrimSpace(token)); errors.Is(err, auth.ErrInvalidToken) {
				writeProblem(w, r, CodeUnauthenticated, "invalid, revoked or expired token")
				return
			}
		} else if t := sessionToken(r); t != "" {
			if p, err = svc.Session(r.Context(), t); errors.Is(err, auth.ErrInvalidToken) {
				p, err = nil, nil
			}
		}
		if err != nil {
			log.ErrorContext(r.Context(), "authenticate", "err", err)
			writeProblem(w, r, CodeInternal, "")
			return
		}
		if p != nil {
			r = r.WithContext(auth.WithPrincipal(r.Context(), p))
		}
		next.ServeHTTP(w, r)
	})
}
