//go:build integration

package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/obs"
)

// Synthetic credentials; the log check below proves none of them is ever logged.
const (
	ownerName     = "owner"
	ownerPassword = "SENTINEL-password-0451-correct"
	wrongPassword = "SENTINEL-password-0451-wrong"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type authEnv struct {
	t       *testing.T
	exec    func(sql string, args ...any)
	count   func(sql string, args ...any) int
	d       *db.DB
	svc     *auth.Service
	h       http.Handler
	userID  uuid.UUID
	logs    *syncBuffer
	secrets []string
}

// newAuthEnv builds the real handler over a fresh database with one owner, plus test-only
// routes standing in for health and config endpoints that do not exist yet. When
// the test ends it asserts that no secret it used reached the logs (E03 acceptance).
func newAuthEnv(t *testing.T, development bool) *authEnv {
	t.Helper()
	_, pool := dbtest.Migrated(t)
	d := db.New(pool)
	keyPath := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.WriteKeyFile(keyPath); err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.Load(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.New(d, kr)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := auth.CreateOwner(t.Context(), d, ownerName, ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.Open(filepath.Join(t.TempDir(), "blobs"), kr)
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	rt, err := newRouter(obs.NewLogger(logs, slog.LevelDebug), newUITestFS(),
		Options{Auth: svc, Development: development, DB: d, Blobs: blobs})
	if err != nil {
		t.Fatal(err)
	}
	noContent := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	rt.handle("GET /api/v1/test/health", scope(auth.ReadHealth), noContent)
	rt.handle("PATCH /api/v1/test/config", scope(auth.WriteConfig), noContent)

	e := &authEnv{t: t, d: d, svc: svc, h: rt.handler(), userID: uid, logs: logs,
		secrets: []string{ownerPassword, wrongPassword}}
	e.exec = func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	e.count = func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	t.Cleanup(func() {
		out := logs.String()
		if !strings.Contains(out, `"route"`) {
			t.Error("no access log captured")
		}
		for _, s := range e.secrets {
			if s != "" && strings.Contains(out, s) {
				t.Errorf("secret %q appears in logs", s)
			}
		}
	})
	return e
}

type call struct {
	cookie, csrf, bearer string
}

func (e *authEnv) do(method, path, body string, c call) (*http.Response, map[string]any) {
	e.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := request(e.t, method, path, r)
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.cookie})
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	res := serve(e.t, e.h, req)
	var out map[string]any
	if b, _ := io.ReadAll(res.Body); len(b) > 0 {
		if err := json.Unmarshal(b, &out); err != nil {
			e.t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, b)
		}
	}
	return res, out
}

func (e *authEnv) expect(res *http.Response, body map[string]any, status int, code Code) {
	e.t.Helper()
	if res.StatusCode != status || (code != "" && body["code"] != string(code)) {
		e.t.Fatalf("got %d %v, want %d %s", res.StatusCode, body, status, code)
	}
}

func loginBody(password string, extra ...string) string {
	b, _ := json.Marshal(map[string]string{"username": ownerName, "password": password})
	s := string(b)
	for i := 0; i+1 < len(extra); i += 2 {
		s = strings.TrimSuffix(s, "}") + `,"` + extra[i] + `":"` + extra[i+1] + `"}`
	}
	return s
}

// login signs in and returns the session cookie value and CSRF token.
func (e *authEnv) login(extra ...string) (string, string) {
	e.t.Helper()
	res, body := e.do(http.MethodPost, "/api/v1/auth/login", loginBody(ownerPassword, extra...), call{})
	e.expect(res, body, http.StatusOK, "")
	var token string
	for _, c := range res.Cookies() {
		if c.Name == auth.SessionCookie {
			token = c.Value
		}
	}
	csrf, _ := body["csrf_token"].(string)
	if token == "" || csrf == "" {
		e.t.Fatalf("login gave no session: %v", body)
	}
	e.secrets = append(e.secrets, token, csrf)
	return token, csrf
}

func sha(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func TestLoginSessionLogout(t *testing.T) {
	e := newAuthEnv(t, false)

	res, bad := e.do(http.MethodPost, "/api/v1/auth/login", loginBody(wrongPassword), call{})
	e.expect(res, bad, http.StatusUnauthorized, CodeUnauthenticated)
	res, unknown := e.do(http.MethodPost, "/api/v1/auth/login", `{"username":"nobody","password":"`+wrongPassword+`"}`, call{})
	e.expect(res, unknown, http.StatusUnauthorized, CodeUnauthenticated)
	if bad["detail"] != unknown["detail"] {
		t.Fatal("unknown user and wrong password must look the same")
	}

	res, body := e.do(http.MethodPost, "/api/v1/auth/login", loginBody(ownerPassword), call{})
	e.expect(res, body, http.StatusOK, "")
	cookie := res.Header.Get("Set-Cookie")
	for _, attr := range []string{auth.SessionCookie + "=", "Path=/", "HttpOnly", "Secure", "SameSite=Strict", "Max-Age="} {
		if !strings.Contains(cookie, attr) {
			t.Errorf("session cookie %q lacks %s", cookie, attr)
		}
	}
	token, csrf := res.Cookies()[0].Value, body["csrf_token"].(string)
	e.secrets = append(e.secrets, token, csrf)
	if e.count("SELECT count(*) FROM sessions WHERE token_hash = $1", sha(token)) != 1 ||
		e.count("SELECT count(*) FROM sessions WHERE position($1::bytea IN token_hash) > 0", []byte(token)) != 0 {
		t.Fatal("session must be stored as the SHA-256 of its token only")
	}

	res, body = e.do(http.MethodGet, "/api/v1/auth/session", "", call{cookie: token})
	e.expect(res, body, http.StatusOK, "")
	if body["csrf_token"] != csrf || body["user"].(map[string]any)["username"] != ownerName {
		t.Fatalf("session: %v", body)
	}

	// A new login rotates: the cookie it was sent with stops working.
	token2, csrf2 := func() (string, string) {
		res, body := e.do(http.MethodPost, "/api/v1/auth/login", loginBody(ownerPassword), call{cookie: token})
		e.expect(res, body, http.StatusOK, "")
		e.secrets = append(e.secrets, res.Cookies()[0].Value)
		return res.Cookies()[0].Value, body["csrf_token"].(string)
	}()
	if token2 == token {
		t.Fatal("login must issue a new session token")
	}
	res, body = e.do(http.MethodGet, "/api/v1/auth/session", "", call{cookie: token})
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)

	res, body = e.do(http.MethodPost, "/api/v1/auth/logout", "", call{cookie: token2})
	e.expect(res, body, http.StatusForbidden, CodeForbidden) // no CSRF header
	res, body = e.do(http.MethodPost, "/api/v1/auth/logout", "", call{cookie: token2, csrf: csrf})
	e.expect(res, body, http.StatusForbidden, CodeForbidden) // CSRF token of another session
	res, _ = e.do(http.MethodPost, "/api/v1/auth/logout", "", call{cookie: token2, csrf: csrf2})
	if res.StatusCode != http.StatusNoContent || !strings.Contains(res.Header.Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout: %d %q", res.StatusCode, res.Header.Get("Set-Cookie"))
	}
	if e.count("SELECT count(*) FROM sessions") != 0 {
		t.Fatal("logout must delete the session row")
	}
	res, body = e.do(http.MethodGet, "/api/v1/auth/session", "", call{cookie: token2})
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)

	// Idle sessions end.
	token3, _ := e.login()
	e.exec("UPDATE sessions SET last_seen_at = now() - interval '13 hours'")
	res, body = e.do(http.MethodGet, "/api/v1/auth/session", "", call{cookie: token3})
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)

	if n := e.count("SELECT count(*) FROM audit_events WHERE action = 'auth.login_failed' AND detail::text NOT LIKE '%SENTINEL%'"); n != 2 {
		t.Fatalf("%d login_failed audit events without secrets, want 2", n)
	}
}

func TestDevelopmentCookieOverHTTP(t *testing.T) {
	e := newAuthEnv(t, true)
	res, _ := e.do(http.MethodPost, "/api/v1/auth/login", loginBody(ownerPassword), call{})
	e.secrets = append(e.secrets, res.Cookies()[0].Value)
	if c := res.Header.Get("Set-Cookie"); strings.Contains(c, "Secure") || !strings.Contains(c, "HttpOnly") || !strings.Contains(c, "SameSite=Strict") {
		t.Fatalf("development cookie over http: %q", c)
	}
}

func TestLoginThrottle(t *testing.T) {
	e := newAuthEnv(t, false)
	for i := range 5 {
		res, body := e.do(http.MethodPost, "/api/v1/auth/login", loginBody(wrongPassword), call{})
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d %v", i+1, res.StatusCode, body)
		}
	}
	// Locked out now, even with the right password.
	res, body := e.do(http.MethodPost, "/api/v1/auth/login", loginBody(ownerPassword), call{})
	e.expect(res, body, http.StatusTooManyRequests, CodeRateLimited)
	if res.Header.Get("Retry-After") == "" || res.Header.Get("Set-Cookie") != "" {
		t.Fatalf("throttled response: Retry-After %q, cookie %q", res.Header.Get("Retry-After"), res.Header.Get("Set-Cookie"))
	}
	time.Sleep(1100 * time.Millisecond) // the first lockout is one second
	e.login()
}

func TestTOTP(t *testing.T) {
	e := newAuthEnv(t, false)
	token, csrf := e.login()
	s := call{cookie: token, csrf: csrf}

	res, body := e.do(http.MethodPost, "/api/v1/auth/totp/enroll", "", s)
	e.expect(res, body, http.StatusOK, "")
	secret := body["secret"].(string)
	e.secrets = append(e.secrets, secret)
	if !strings.HasPrefix(body["otpauth_uri"].(string), "otpauth://totp/Vitamux:owner?") {
		t.Fatalf("uri %v", body["otpauth_uri"])
	}
	if e.count("SELECT count(*) FROM users WHERE totp_ciphertext IS NOT NULL AND position($1::bytea IN totp_ciphertext) = 0", []byte(secret)) != 1 {
		t.Fatal("TOTP secret must be sealed at rest")
	}
	e.login() // pending enrolment does not require a code yet

	res, body = e.do(http.MethodPost, "/api/v1/auth/totp/confirm", `{"code":"000000"}`, s)
	if res.StatusCode != http.StatusUnprocessableEntity {
		code, _ := totp.GenerateCode(secret, time.Now())
		if code != "000000" { // one in a million
			t.Fatalf("wrong confirm code: %d %v", res.StatusCode, body)
		}
	}
	now := time.Now()
	code, _ := totp.GenerateCode(secret, now)
	e.secrets = append(e.secrets, code)
	res, body = e.do(http.MethodPost, "/api/v1/auth/totp/confirm", `{"code":"`+code+`"}`, s)
	e.expect(res, body, http.StatusOK, "")
	recovery := body["recovery_codes"].([]any)
	if len(recovery) != 10 {
		t.Fatalf("recovery codes: %v", recovery)
	}
	for _, c := range recovery {
		e.secrets = append(e.secrets, c.(string))
	}
	if e.count("SELECT count(*) FROM recovery_codes WHERE code_hash = $1", sha(strings.ReplaceAll(recovery[0].(string), "-", ""))) != 1 {
		t.Fatal("recovery codes must be stored hashed")
	}

	res, body = e.do(http.MethodPost, "/api/v1/auth/login", loginBody(ownerPassword), call{})
	e.expect(res, body, http.StatusUnauthorized, CodeTOTPRequired)
	if res.Header.Get("Set-Cookie") != "" {
		t.Fatal("no session without the second factor")
	}
	res, body = e.do(http.MethodPost, "/api/v1/auth/login", loginBody(ownerPassword, "totp_code", code), call{})
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated) // replay of the confirm code

	next, _ := totp.GenerateCode(secret, now.Add(30*time.Second))
	e.secrets = append(e.secrets, next)
	e.login("totp_code", next)
	res, body = e.do(http.MethodPost, "/api/v1/auth/login", loginBody(ownerPassword, "totp_code", next), call{})
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated) // replay

	rc := recovery[1].(string)
	e.login("recovery_code", strings.ToUpper(rc))
	res, body = e.do(http.MethodPost, "/api/v1/auth/login", loginBody(ownerPassword, "recovery_code", rc), call{})
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated) // single use

	res, body = e.do(http.MethodPost, "/api/v1/auth/totp/enroll", "", s)
	e.expect(res, body, http.StatusConflict, CodeConflict)
	_, body = e.do(http.MethodGet, "/api/v1/auth/session", "", s)
	if body["user"].(map[string]any)["totp_enabled"] != true {
		t.Fatalf("session: %v", body)
	}

	res, body = e.do(http.MethodPost, "/api/v1/auth/totp/disable", `{"password":"`+wrongPassword+`","recovery_code":"`+recovery[2].(string)+`"}`, s)
	e.expect(res, body, http.StatusForbidden, CodeForbidden)
	res, body = e.do(http.MethodPost, "/api/v1/auth/totp/disable", `{"password":"`+ownerPassword+`"}`, s)
	e.expect(res, body, http.StatusForbidden, CodeForbidden) // a code is required too
	res, _ = e.do(http.MethodPost, "/api/v1/auth/totp/disable", `{"password":"`+ownerPassword+`","recovery_code":"`+recovery[3].(string)+`"}`, s)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("disable: %d", res.StatusCode)
	}
	if e.count("SELECT count(*) FROM recovery_codes") != 0 || e.count("SELECT count(*) FROM users WHERE totp_ciphertext IS NULL AND totp_enabled_at IS NULL") != 1 {
		t.Fatal("disable must clear the secret and recovery codes")
	}
	e.login()

	for _, action := range []string{"auth.totp_enable", "auth.totp_disable", "auth.recovery_code_used"} {
		if e.count("SELECT count(*) FROM audit_events WHERE action = $1", action) == 0 {
			t.Errorf("no %s audit event", action)
		}
	}
}

func TestAPIKeysAndScopes(t *testing.T) {
	e := newAuthEnv(t, false)
	token, csrf := e.login()
	s := call{cookie: token, csrf: csrf}

	create := func(c call, body string) (string, string) {
		t.Helper()
		res, out := e.do(http.MethodPost, "/api/v1/api-keys", body, c)
		e.expect(res, out, http.StatusCreated, "")
		tok := out["token"].(string)
		e.secrets = append(e.secrets, tok)
		return out["id"].(string), tok
	}
	res, body := e.do(http.MethodPost, "/api/v1/api-keys", `{"name":"r","scopes":["read:health"]}`, call{cookie: token})
	e.expect(res, body, http.StatusForbidden, CodeForbidden) // session without CSRF
	res, body = e.do(http.MethodPost, "/api/v1/api-keys", `{"name":"r","scopes":["superuser"]}`, s)
	e.expect(res, body, http.StatusUnprocessableEntity, CodeValidationFailed)

	readID, readTok := create(s, `{"name":"reader","scopes":["read:health"]}`)
	_, adminTok := create(s, `{"name":"admin","scopes":["admin"]}`)
	if !strings.HasPrefix(readTok, auth.PATPrefix+strings.ReplaceAll(readID, "-", "")+"_") {
		t.Fatalf("token %q does not carry id %s", readTok, readID)
	}
	secret := readTok[len(auth.PATPrefix)+33:]
	if e.count("SELECT count(*) FROM api_keys WHERE position($1 IN api_keys::text) > 0", secret) != 0 {
		t.Fatal("API key secret stored in clear")
	}

	// The secret is shown once: listing never returns it.
	res, body = e.do(http.MethodGet, "/api/v1/api-keys", "", s)
	e.expect(res, body, http.StatusOK, "")
	listed, _ := json.Marshal(body)
	if len(body["api_keys"].([]any)) != 2 || strings.Contains(string(listed), secret) || strings.Contains(string(listed), `"token"`) {
		t.Fatalf("list: %s", listed)
	}

	// read:health reads but cannot change config or manage keys; bearer needs no CSRF.
	res, _ = e.do(http.MethodGet, "/api/v1/test/health", "", call{bearer: readTok})
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("read:health read: %d", res.StatusCode)
	}
	res, body = e.do(http.MethodPatch, "/api/v1/test/config", "{}", call{bearer: readTok})
	e.expect(res, body, http.StatusForbidden, CodeForbidden)
	res, body = e.do(http.MethodGet, "/api/v1/api-keys", "", call{bearer: readTok})
	e.expect(res, body, http.StatusForbidden, CodeForbidden)
	res, body = e.do(http.MethodPost, "/api/ingest/v1/heartbeat", heartbeatBody(uuid.New(), "2026-09-14T09:00:00Z"), call{bearer: readTok})
	e.expect(res, body, http.StatusForbidden, CodeForbidden)
	if e.count("SELECT count(*) FROM api_keys WHERE id = $1 AND last_used_at IS NOT NULL", readID) != 1 {
		t.Fatal("last_used_at not recorded")
	}

	// admin implies the other scopes and may manage keys without a session or CSRF.
	res, _ = e.do(http.MethodPatch, "/api/v1/test/config", "{}", call{bearer: adminTok})
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("admin config: %d", res.StatusCode)
	}
	create(call{bearer: adminTok}, `{"name":"by-admin","scopes":["read:config"]}`)

	res, _ = e.do(http.MethodDelete, "/api/v1/api-keys/"+readID, "", s)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke: %d", res.StatusCode)
	}
	res, body = e.do(http.MethodGet, "/api/v1/test/health", "", call{bearer: readTok})
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)
	res, body = e.do(http.MethodDelete, "/api/v1/api-keys/"+readID, "", s)
	e.expect(res, body, http.StatusNotFound, CodeNotFound)

	_, expTok := create(s, `{"name":"soon","scopes":["read:health"],"expires_at":"`+time.Now().Add(time.Hour).Format(time.RFC3339)+`"}`)
	e.exec("UPDATE api_keys SET expires_at = now() - interval '1 second' WHERE name = 'soon'")
	tampered := adminTok[:len(adminTok)-1] + "A"
	if tampered == adminTok {
		tampered = adminTok[:len(adminTok)-1] + "B"
	}
	for _, bad := range []string{expTok, tampered, "vmx_pat_garbage", "not-a-token"} {
		res, body = e.do(http.MethodGet, "/api/v1/test/health", "", call{bearer: bad})
		e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)
	}

	if e.count("SELECT count(*) FROM audit_events WHERE action IN ('api_key.create', 'api_key.revoke')") != 5 {
		t.Fatal("API key create/revoke must be audited")
	}
}

func TestClientTokens(t *testing.T) {
	e := newAuthEnv(t, false)
	ctx := t.Context()
	conn := func() uuid.UUID {
		id := uuid.New()
		e.exec(`INSERT INTO connections (id, user_id, provider_id, mode, status)
			SELECT $1, $2, id, 'push', 'active' FROM providers WHERE code = 'apple_health'`, id, e.userID)
		return id
	}
	own, other := conn(), conn()
	_, tok, err := auth.CreateClientToken(ctx, e.d, e.userID, own, "device", "phone")
	if err != nil {
		t.Fatal(err)
	}
	e.secrets = append(e.secrets, tok)
	if !strings.HasPrefix(tok, auth.ClientPrefix) || e.count("SELECT count(*) FROM clients WHERE position($1 IN clients::text) > 0", tok[len(auth.ClientPrefix)+33:]) != 0 {
		t.Fatal("client token must be stored hashed")
	}
	c := call{bearer: tok}

	res, _ := e.do(http.MethodPost, "/api/ingest/v1/heartbeat", heartbeatBody(own, "2026-09-14T09:00:00Z"), c)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("report for own connection: %d", res.StatusCode)
	}
	res, body := e.do(http.MethodPost, "/api/ingest/v1/heartbeat", heartbeatBody(other, "2026-09-14T09:00:00Z"), c)
	e.expect(res, body, http.StatusForbidden, CodeForbidden)
	for _, path := range []string{"/api/v1/test/health", "/api/v1/api-keys", "/api/v1/auth/session"} {
		res, body = e.do(http.MethodGet, path, "", c)
		e.expect(res, body, http.StatusForbidden, CodeForbidden)
	}
	if e.count("SELECT count(*) FROM clients WHERE last_seen_at IS NOT NULL") != 1 {
		t.Fatal("client last_seen_at not recorded")
	}
	e.exec("UPDATE clients SET revoked_at = now()")
	res, body = e.do(http.MethodPost, "/api/ingest/v1/heartbeat", heartbeatBody(own, "2026-09-14T09:00:00Z"), c)
	e.expect(res, body, http.StatusUnauthorized, CodeUnauthenticated)
}
