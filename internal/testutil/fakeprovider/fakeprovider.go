// Package fakeprovider is a scripted HTTP server for connector and runtime tests. A test queues
// the exact requests it expects, each with the response to give; the server asserts every request
// as it arrives and the test fails if the script is not consumed. Responses cover each typed
// error class of docs/architecture/connectors.md#typed-errors, as the provider would signal it:
//
//	ErrReauthRequired  Unauthorized(), InvalidGrant()
//	ErrRateLimited     RateLimited(d), RateLimitedAt(t)
//	ErrTransient       ServerError(code), Drop(), a Step.Delay beyond the client timeout
//	ErrSchemaDrift     JSON(200, differentShape), Malformed()
//	ErrPermanent       Status(400|403|404|...)
//
// Token rotation: queue TokenRefresh(...) before the API call that follows it. A second refresh
// (single-flight violated) or a refresh with a stale token fails the test. To script a provider
// that rejects a replayed token, queue a step replying InvalidGrant().
// Use synthetic token values only. Assertion failures name keys, never values.
package fakeprovider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Request is what the server received.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	Form   url.Values // parsed from an application/x-www-form-urlencoded body
	Header http.Header
	Body   []byte
}

// Response is what the server sends back.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
	Delay  time.Duration // sleep before answering, to provoke client timeouts
	Drop   bool          // close the connection without answering
}

// Step is one expected request and its scripted answer. Empty Method matches any method; Query,
// Form and Header are subsets that must be present with equal values; Check adds free-form assertions.
type Step struct {
	Method string
	Path   string
	Query  url.Values
	Form   url.Values
	Header map[string]string
	Check  func(*Request) error
	Reply  Response
}

// Server is the scripted server. Create with New.
type Server struct {
	URL string // base URL, no trailing slash

	t   testing.TB
	srv *httptest.Server
	mu  sync.Mutex
	q   []Step
	got []Request
}

// New starts a server that is closed, and its script verified as consumed, when the test ends.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{t: t}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	s.URL = s.srv.URL
	t.Cleanup(func() {
		s.srv.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, st := range s.q {
			t.Errorf("fakeprovider: expected request never arrived: %s %s", orAny(st.Method), st.Path)
		}
	})
	return s
}

// Expect appends steps to the script. Requests are matched strictly in order.
func (s *Server) Expect(steps ...Step) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.q = append(s.q, steps...)
}

// Requests returns a copy of every request received so far, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.got...)
}

// Client returns an http.Client for the server (plain HTTP, so any client works too).
func (s *Server) Client() *http.Client { return s.srv.Client() }

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	req := Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Header: r.Header, Body: body}
	if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
		req.Form, _ = url.ParseQuery(string(body))
	}

	s.mu.Lock()
	s.got = append(s.got, req)
	if len(s.q) == 0 {
		s.mu.Unlock()
		s.t.Errorf("fakeprovider: unexpected request %s %s (script exhausted)", r.Method, r.URL.Path)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
		return
	}
	st := s.q[0]
	s.q = s.q[1:]
	s.mu.Unlock()

	if err := st.match(&req); err != nil {
		s.t.Errorf("fakeprovider: %s %s: %v", r.Method, r.URL.Path, err)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
		return
	}
	if st.Reply.Delay > 0 {
		select {
		case <-time.After(st.Reply.Delay):
		case <-r.Context().Done():
			return
		}
	}
	if st.Reply.Drop {
		if c, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = c.Close()
		}
		return
	}
	for k, vs := range st.Reply.Header {
		w.Header()[k] = vs
	}
	status := st.Reply.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(st.Reply.Body)
}

func (st Step) match(r *Request) error {
	if st.Method != "" && st.Method != r.Method {
		return fmt.Errorf("expected method %s", st.Method)
	}
	if st.Path != r.Path {
		return fmt.Errorf("expected path %s", st.Path)
	}
	for k, want := range st.Query {
		if !sameValues(want, r.Query[k]) {
			return fmt.Errorf("query parameter %q differs", k)
		}
	}
	for k, want := range st.Form {
		if !sameValues(want, r.Form[k]) {
			return fmt.Errorf("form field %q differs", k)
		}
	}
	for k, want := range st.Header {
		if r.Header.Get(k) != want {
			return fmt.Errorf("header %q differs", k)
		}
	}
	if st.Check != nil {
		return st.Check(r)
	}
	return nil
}

func sameValues(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func orAny(m string) string {
	if m == "" {
		return "ANY"
	}
	return m
}

// --- Responses ---

// JSON answers status with v encoded as JSON. Use it for happy paths and for shape-changed (schema drift) bodies.
func JSON(status int, v any) Response {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return Response{Status: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: b}
}

// Raw answers status with an arbitrary body.
func Raw(status int, contentType, body string) Response {
	return Response{Status: status, Header: http.Header{"Content-Type": {contentType}}, Body: []byte(body)}
}

// Status answers a bare status code; 4xx other than 401 and 429 means ErrPermanent.
func Status(code int) Response { return Response{Status: code} }

// Unauthorized is a 401 for an invalid or expired access token (ErrReauthRequired after a failed refresh).
func Unauthorized() Response {
	return JSON(http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
}

// InvalidGrant is the OAuth token endpoint refusing a refresh token: revoked or already rotated (ErrReauthRequired).
func InvalidGrant() Response {
	return JSON(http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
}

// RateLimited is a 429 with Retry-After in delta-seconds.
func RateLimited(retryAfter time.Duration) Response {
	r := Status(http.StatusTooManyRequests)
	r.Header = http.Header{"Retry-After": {strconv.Itoa(int(retryAfter.Round(time.Second) / time.Second))}}
	return r
}

// RateLimitedAt is a 429 with Retry-After as an HTTP-date.
func RateLimitedAt(t time.Time) Response {
	r := Status(http.StatusTooManyRequests)
	r.Header = http.Header{"Retry-After": {t.UTC().Format(http.TimeFormat)}}
	return r
}

// ServerError is a 5xx (ErrTransient).
func ServerError(code int) Response { return Status(code) }

// Drop closes the connection mid-request (ErrTransient, network class).
func Drop() Response { return Response{Drop: true} }

// Malformed is a 200 with a truncated JSON body (decode failure, ErrSchemaDrift or ErrTransient per connector policy).
func Malformed() Response {
	return Raw(http.StatusOK, "application/json", `{"status":0,"body":{"items":[`)
}

// --- Steps ---

// TokenRefresh expects an OAuth refresh_token grant on path presenting oldRefresh and answers with the
// rotated pair. Presenting any other refresh token (a stale one after rotation) fails the test.
func TokenRefresh(path, oldRefresh, newAccess, newRefresh string, expiresIn time.Duration) Step {
	return Step{
		Method: http.MethodPost,
		Path:   path,
		Form:   url.Values{"grant_type": {"refresh_token"}},
		Check: func(r *Request) error {
			if r.Form.Get("refresh_token") != oldRefresh {
				return fmt.Errorf("refresh token is not the current one")
			}
			return nil
		},
		Reply: JSON(http.StatusOK, map[string]any{
			"access_token": newAccess, "refresh_token": newRefresh,
			"token_type": "Bearer", "expires_in": int(expiresIn / time.Second),
		}),
	}
}

// GET is shorthand for a GET step requiring a bearer token.
func GET(path, bearer string, reply Response) Step {
	return Step{Method: http.MethodGet, Path: path, Header: map[string]string{"Authorization": "Bearer " + bearer}, Reply: reply}
}
