//go:build integration

package api

import (
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/auth"
)

// matrixPrincipal is one kind of caller TestAuthzMatrixEnforced tries on every route.
type matrixPrincipal struct {
	name string
	// admitted reports whether the matrix lets this caller through e.
	admitted func(e authzEntry) bool
	// set adds the caller's credentials to a fresh request.
	set func(req *http.Request)
}

// TestAuthzMatrixEnforced calls every served route in api/authz.yaml as every kind of
// principal: anonymous, owner session without and with the CSRF header, an API key of each
// scope, and client tokens of the connection the request targets and of another one.
// Admitted means authorization let the handler run, so any answer but 401, 403 or 500
// (bodies are minimal, so 404 and 422 are expected). Refused means 401 for anonymous and
// 403 for everyone else.
func TestAuthzMatrixEnforced(t *testing.T) {
	e := newPushEnv(t)
	ctx := t.Context()

	// A batch of the own connection, so {batch_id} names something only its client may use.
	res, out := e.send(pushReq{method: http.MethodPost, path: "/api/ingest/v1/batches", key: "authz-seed",
		body: batchBody(e.own, testItem{key: "seed", body: `{"v": 1}`})})
	batch := e.expectCode(res, out, http.StatusAccepted, "")["batch_id"].(string)

	// Each request gets its own session: logout, login and session revocation end them.
	newSession := func() string {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		token := base64.RawURLEncoding.EncodeToString(b)
		e.exec(`INSERT INTO sessions (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, now() + interval '1 day')`,
			uuid.New(), e.userID, sha(token))
		e.secrets = append(e.secrets, token)
		return token
	}
	bearer := func(tok string) func(*http.Request) {
		return func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+tok) }
	}
	freshClient := func(conn uuid.UUID) func(*http.Request) {
		return func(req *http.Request) {
			_, tok, err := auth.CreateClientToken(ctx, e.d, e.userID, conn, "device", "phone")
			if err != nil {
				t.Fatal(err)
			}
			e.secrets = append(e.secrets, tok)
			bearer(tok)(req)
		}
	}
	public := func(e authzEntry) bool { return e.Allow == "public" }
	principals := []matrixPrincipal{
		{"anonymous", public, func(*http.Request) {}},
		{"session without csrf", func(e authzEntry) bool { return public(e) || (e.Allow == "session" || e.scoped()) && !e.CSRF },
			func(req *http.Request) { req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: newSession()}) }},
		{"session", func(e authzEntry) bool { return public(e) || e.Allow == "session" || e.scoped() },
			func(req *http.Request) {
				tok := newSession()
				req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: tok})
				req.Header.Set(csrfHeader, e.svc.CSRFToken(tok))
			}},
		// Fresh tokens per request: rotate-token replaces the caller's token. A device's own
		// routes (/devices/self) admit any client.
		{"client of the connection", func(e authzEntry) bool { return public(e) || e.Allow == "client" }, freshClient(e.own)},
		{"client of another connection", public, freshClient(e.otherConn)},
	}
	owner := &auth.Principal{Kind: auth.OwnerSession, UserID: e.userID}
	for _, s := range auth.Scopes {
		_, tok, err := e.svc.CreateAPIKey(ctx, owner, "matrix "+string(s), []auth.Scope{s}, nil)
		if err != nil {
			t.Fatal(err)
		}
		e.secrets = append(e.secrets, tok)
		principals = append(principals, matrixPrincipal{"api key " + string(s), func(e authzEntry) bool {
			return public(e) || e.scoped() && (e.Allow == string(s) || s == auth.Admin)
		}, bearer(tok)})
	}

	matrix := loadAuthz(t)
	keys := make([]string, 0, len(matrix))
	for k, m := range matrix {
		if !m.Planned {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, key := range keys {
		entry := matrix[key]
		t.Run(key, func(t *testing.T) {
			for _, p := range principals {
				req := matrixRequest(t, key, batch, e.own)
				p.set(req)
				res := serve(t, e.h, req)
				body, _ := io.ReadAll(res.Body)
				switch {
				case p.admitted(entry) || p.name == "client of another connection" && strings.Contains(key, "/devices/self"):
					if s := res.StatusCode; s == http.StatusUnauthorized || s == http.StatusForbidden || s == http.StatusInternalServerError {
						t.Errorf("%s: want admitted, got %d %s", p.name, s, body)
					}
				case p.name == "anonymous":
					if res.StatusCode != http.StatusUnauthorized {
						t.Errorf("%s: want 401, got %d %s", p.name, res.StatusCode, body)
					}
				default:
					if res.StatusCode != http.StatusForbidden {
						t.Errorf("%s: want 403, got %d %s", p.name, res.StatusCode, body)
					}
				}
			}
		})
	}
	if len(keys) < 80 {
		t.Fatalf("only %d served routes in the matrix", len(keys))
	}
}

// matrixRequest builds a minimal request for a matrix key. Path parameters become the seeded
// batch, a known provider, or an id that exists nowhere; bodies are `{}` except where the
// handler would otherwise refuse before showing whether authorization passed (ingest checks
// the target connection in the body; login needs real credentials).
func matrixRequest(t *testing.T, key, batch string, conn uuid.UUID) *http.Request {
	t.Helper()
	method, path, _ := strings.Cut(key, " ")
	path = strings.NewReplacer("{batch_id}", batch, "{provider}", "withings").Replace(path)
	path = pathParam.ReplaceAllString(path, "0192f0a0-0000-7000-8000-000000000000")
	var body string
	switch key {
	case "POST /api/v1/auth/login":
		body = loginBody(ownerPassword)
	case "POST /api/ingest/v1/batches":
		body = string(batchBody(conn, testItem{key: uuid.NewString(), body: `{"v": 2}`}))
	case "POST /api/ingest/v1/batches/{batch_id}/blobs":
		body = "synthetic blob " + uuid.NewString()
	case "POST /api/ingest/v1/heartbeat":
		body = heartbeatBody(conn, "2026-09-14T09:00:00Z")
	default:
		if !safeMethod(method) {
			body = "{}"
		}
	}
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := request(t, method, path, r)
	if !safeMethod(method) {
		req.Header.Set(idempotencyHeader, uuid.NewString())
	}
	return req
}
