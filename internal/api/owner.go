package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db"
)

// Owner endpoints (/api/v1) are contract-first: `make openapi` generates request and response
// types, parameter binding and the strict server interface from api/openapi.yaml into
// package oapi. Each area implements its operations as methods on *owner in its own file
// and registers them like every other route, which keeps access deny-by-default:
//
//	rt.handle("GET /api/v1/system/version", scope(auth.ReadConfig), rt.ops.GetSystemVersion)
//
// The pattern must use the spec's path parameter names (TestRoutesMatchSpec checks it).
// Hand-written routes (auth, API keys, ingest) stay plain http.HandlerFuncs.

// owner implements the generated strict server. The embedded interface is nil: it only
// satisfies the compiler for operations no area implements yet. Those are never reached,
// because an operation is served only once its method is registered with rt.handle.
type owner struct {
	oapi.StrictServerInterface
	*router
	cursors cursorCodec
}

// newOwnerOps returns the generated wrapper whose per-operation handlers routes register.
// Parameter and body errors answer validation_failed (or 413); handler errors go through
// responseError.
func newOwnerOps(rt *router) (*oapi.ServerInterfaceWrapper, error) {
	cursors, err := newCursorCodec(rt.opts.Keys)
	if err != nil {
		return nil, err
	}
	strict := oapi.NewStrictHandlerWithOptions(&owner{router: rt, cursors: cursors}, nil, oapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  writeBodyError,
		ResponseErrorHandlerFunc: rt.responseError,
	})
	return &oapi.ServerInterfaceWrapper{
		Handler:            strict,
		HandlerMiddlewares: []oapi.MiddlewareFunc{noStore},
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeProblem(w, r, CodeValidationFailed, err.Error()) // names the parameter; no server data
		},
	}, nil
}

// apiError is a problem an owner handler returns as its error.
type apiError struct {
	code   Code
	detail string
	errs   []FieldError
}

func (e *apiError) Error() string { return string(e.code) + ": " + e.detail }

// problemErr returns an error that responseError writes as this problem. detail must be
// safe to show to the caller (see problem).
func problemErr(code Code, detail string, errs ...FieldError) error {
	return &apiError{code: code, detail: detail, errs: errs}
}

// responseError answers an owner handler's error: its problem, 429 with Retry-After when
// throttled, 422 for a bad cursor, 404 for db.ErrNotFound, and 500 (logged) otherwise.
func (rt *router) responseError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apiError
	var throttled *auth.ThrottledError
	switch {
	case errors.As(err, &ae):
		writeProblem(w, r, ae.code, ae.detail, ae.errs...)
	case errors.As(err, &throttled):
		retryAfter(w, throttled.RetryAfter)
		writeProblem(w, r, CodeRateLimited, "too many requests; try again later")
	case errors.Is(err, errInvalidCursor):
		writeProblem(w, r, CodeValidationFailed, err.Error(), FieldError{Pointer: "/cursor", Detail: "start again without a cursor"})
	case errors.Is(err, db.ErrNotFound):
		writeProblem(w, r, CodeNotFound, "")
	case errors.Is(err, context.Canceled):
		// The client went away; nothing useful to write.
	default:
		rt.internal(w, r, "owner api", err)
	}
}

// noStore keeps owner responses out of caches, like writeJSON does for hand-written routes.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
