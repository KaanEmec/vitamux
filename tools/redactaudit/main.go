// Command redactaudit drives a running Vitamux server through every flow that handles a secret
// or a health value, each carrying a recognisable sentinel, and writes the sentinels for
// scripts/redaction-audit.sh to look for in logs, the database, metrics and an export (J13.6).
// Synthetic values only; the server must be on loopback with a dead HTTPS_PROXY so no provider
// call leaves the host.
//
//	go run ./tools/redactaudit -url http://127.0.0.1:18081 -user owner -lab DIR -out DIR
//
// Reads the owner password from REDACTION_AUDIT_PASSWORD and VITAMUX_DATABASE_URL (used once
// to issue an ingest client token: there is no pairing API before E15). Writes DIR/secrets.txt
// and DIR/health.txt (one sentinel per line, appended) and unpacks the export into DIR/export.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

func main() {
	base := flag.String("url", "", "server base URL")
	user := flag.String("user", "", "owner username")
	lab := flag.String("lab", "", "directory with the synthetic lab PDFs (go run ./tools/fixturegen labpdf)")
	out := flag.String("out", "", "output directory")
	aiModel := flag.String("ai-model", "", "model configured for openai_compatible and gemini (their calls must fail)")
	flag.Parse()
	a := &audit{base: strings.TrimRight(*base, "/"), out: *out}
	if err := a.run(*user, os.Getenv("REDACTION_AUDIT_PASSWORD"), *lab, *aiModel); err != nil {
		fmt.Fprintln(os.Stderr, "redactaudit:", err)
		os.Exit(1)
	}
}

type audit struct {
	base, out string
	session   *http.Client // cookie jar: owner session
	plain     *http.Client // no cookies: bearer tokens
	csrf      string
	secrets   []string
	health    []string
}

// failf aborts the run; the deferred recover in run turns it into an error.
type failure struct{ error }

func failf(format string, args ...any) { panic(failure{fmt.Errorf(format, args...)}) }

func sentinel(kind string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "vtmxsentinel" + kind + hex.EncodeToString(b)
}

// healthNumber is an unlikely decimal (6 places, last digit non-zero) in [lo, lo+40).
func healthNumber(lo int64) string {
	n, _ := rand.Int(rand.Reader, big.NewInt(40_000_000))
	v := lo*1_000_000 + n.Int64()
	if v%10 == 0 {
		v++
	}
	return fmt.Sprintf("%d.%06d", v/1_000_000, v%1_000_000)
}

// secret records a server-issued token: whole, and its secret part after the last '_'.
func (a *audit) secret(tok string) {
	a.secrets = append(a.secrets, tok)
	if i := strings.LastIndexByte(tok, '_'); i > 0 && len(tok)-i > 16 {
		a.secrets = append(a.secrets, tok[i+1:])
	}
}

type req struct {
	method, path string
	bearer       string // empty: the session client with X-CSRF-Token
	ctype        string
	body         any // []byte is sent as is, anything else as JSON
	header       map[string]string
}

func (a *audit) do(r req, want ...int) (int, []byte, http.Header) {
	var body io.Reader
	switch b := r.body.(type) {
	case nil:
	case []byte:
		body = bytes.NewReader(b)
	default:
		j, err := json.Marshal(b)
		if err != nil {
			failf("%s %s: %v", r.method, r.path, err)
		}
		body, r.ctype = bytes.NewReader(j), "application/json"
	}
	hr, err := http.NewRequestWithContext(context.Background(), r.method, a.base+r.path, body)
	if err != nil {
		failf("%s %s: %v", r.method, r.path, err)
	}
	if r.ctype != "" {
		hr.Header.Set("Content-Type", r.ctype)
	}
	for k, v := range r.header {
		hr.Header.Set(k, v)
	}
	c := a.session
	if r.bearer != "" {
		c = a.plain
		hr.Header.Set("Authorization", "Bearer "+r.bearer)
	} else if a.csrf != "" {
		hr.Header.Set("X-CSRF-Token", a.csrf)
	}
	res, err := c.Do(hr)
	if err != nil {
		failf("%s %s: %v", r.method, r.path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if len(want) > 0 && !slices.Contains(want, res.StatusCode) {
		failf("%s %s: status %d, want %v: %.300s", r.method, r.path, res.StatusCode, want, b)
	}
	return res.StatusCode, b, res.Header
}

func (a *audit) json(r req, want ...int) map[string]any {
	_, b, _ := a.do(r, want...)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		failf("%s %s: not JSON: %.200s", r.method, r.path, b)
	}
	return m
}

// poll calls f every 250 ms until it reports done, for at most 60 s.
func poll(what string, f func() bool) {
	for end := time.Now().Add(time.Minute); time.Now().Before(end); time.Sleep(250 * time.Millisecond) {
		if f() {
			return
		}
	}
	failf("timed out waiting for %s", what)
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func (a *audit) run(user, password, lab, aiModel string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			f, ok := r.(failure)
			if !ok {
				panic(r)
			}
			err = f
		}
	}()
	if a.base == "" || user == "" || password == "" || lab == "" || a.out == "" || aiModel == "" {
		return fmt.Errorf("need -url, -user, -lab, -out, -ai-model and REDACTION_AUDIT_PASSWORD")
	}
	jar, _ := cookiejar.New(nil)
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	a.session = &http.Client{Jar: jar, CheckRedirect: noRedirect, Timeout: 30 * time.Second}
	a.plain = &http.Client{CheckRedirect: noRedirect, Timeout: 30 * time.Second}

	// Sign-in: a wrong password, then the right one; TOTP enrolment; a recovery-code sign-in.
	wrong := sentinel("wrongpassword")
	a.secrets = append(a.secrets, wrong)
	a.do(req{method: "POST", path: "/api/v1/auth/login", body: map[string]string{"username": user, "password": wrong}}, 401)
	login := a.json(req{method: "POST", path: "/api/v1/auth/login", body: map[string]string{"username": user, "password": password}}, 200)
	a.csrf = str(login, "csrf_token")
	userID, err := uuid.Parse(str(login["user"].(map[string]any), "id"))
	if err != nil {
		return fmt.Errorf("login: user id: %w", err)
	}
	a.secrets = append(a.secrets, a.csrf)
	enrol := a.json(req{method: "POST", path: "/api/v1/auth/totp/enroll"}, 200)
	totpSecret := str(enrol, "secret")
	a.secrets = append(a.secrets, totpSecret, str(enrol, "otpauth_uri"))
	code, err := totp.GenerateCode(totpSecret, time.Now())
	if err != nil {
		return fmt.Errorf("totp: %w", err)
	}
	conf := a.json(req{method: "POST", path: "/api/v1/auth/totp/confirm", body: map[string]string{"code": code}}, 200)
	var recovery []string
	for _, c := range conf["recovery_codes"].([]any) {
		recovery = append(recovery, c.(string))
	}
	a.secrets = append(a.secrets, recovery...)
	a.do(req{method: "POST", path: "/api/v1/auth/logout"}, 204)
	a.csrf = ""
	login = a.json(req{method: "POST", path: "/api/v1/auth/login", body: map[string]string{"username": user, "password": password, "recovery_code": recovery[0]}}, 200)
	a.csrf = str(login, "csrf_token")
	a.secrets = append(a.secrets, a.csrf)
	for _, c := range jar.Cookies(mustURL(a.base)) {
		a.secrets = append(a.secrets, c.Value)
	}

	// Personal access token: issued, used, and a forged one with the same id refused.
	key := a.json(req{method: "POST", path: "/api/v1/api-keys", body: map[string]any{"name": "redaction audit",
		"scopes": []string{"read:health", "read:config", "write:config", "write:documents", "admin"}}}, 201)
	pat := str(key, "token")
	a.secret(pat)
	forged := pat[:strings.LastIndexByte(pat, '_')+1] + randomSecret()
	a.secret(forged)
	a.do(req{method: "GET", path: "/api/v1/connections", bearer: pat}, 200)
	a.do(req{method: "GET", path: "/api/v1/connections", bearer: forged}, 401)

	// Push ingestion: a batch with sentinel health values, a refused token, a rejected body.
	conn := a.json(req{method: "POST", path: "/api/v1/connections", bearer: pat, body: map[string]string{"provider": "apple_health"}}, 201)
	connID, err := ingest.ParseConnectionID(str(conn, "id"))
	if err != nil {
		return fmt.Errorf("connection id: %w", err)
	}
	client, err := clientToken(userID, connID)
	if err != nil {
		return err
	}
	a.secret(client)
	hr, device := healthNumber(120), sentinel("healthdevice")
	a.health = append(a.health, hr, device)
	batch := func(value string) []byte {
		return fmt.Appendf(nil, `{"schema":"vitamux.ingest.batch/1","connection_id":%q,"client":{"kind":"device","name":"redaction-audit","version":"1.0"},
"items":[{"stream":"healthkit.samples.v1","external_key":"audit:%s","fetched_at":"2026-09-14T09:02:11+02:00","content_type":"application/json",
"body":{"type":"HKQuantityTypeIdentifierHeartRate","samples":[{"uuid":"6F0D0000-0000-4000-8000-000000000001","start":"2026-09-14T07:31:05+02:00",
"end":"2026-09-14T07:31:05+02:00","value":%s,"unit":"count/min","device":{"name":%q}}]}}]}`, ingest.FormatConnectionID(connID), uuid.NewString(), value, device)
	}
	idem := map[string]string{"Idempotency-Key": uuid.NewString()}
	a.do(req{method: "POST", path: "/api/ingest/v1/batches", bearer: client, ctype: "application/json", body: batch(hr), header: idem}, 202)
	a.do(req{method: "POST", path: "/api/ingest/v1/batches", bearer: forged, ctype: "application/json", body: batch(hr), header: idem}, 401)
	bad := sentinel("healthbadvalue")
	a.health = append(a.health, bad)
	a.do(req{method: "POST", path: "/api/ingest/v1/batches", bearer: client, ctype: "application/json", header: map[string]string{"Idempotency-Key": uuid.NewString()},
		body: fmt.Appendf(nil, `{"schema":"vitamux.ingest.batch/1","connection_id":%q,"items":[],"unexpected":%q}`, ingest.FormatConnectionID(connID), bad)}, 400, 422)
	weight := healthNumber(70)
	a.health = append(a.health, weight)
	a.do(req{method: "POST", path: "/api/v1/measurements/manual", bearer: pat, ctype: "application/json",
		body: fmt.Appendf(nil, `{"metric":"weight","unit":"kg","value":%s,"start_at":"2026-09-14T07:00:00+02:00"}`, weight)}, 201)

	// Source setup (ADR-0021): the Withings app secret is entered through the API (write-only;
	// verify fails at the dead proxy), and a sidecar added in the panel gets a generated secret.
	appSecret := os.Getenv("REDACTION_AUDIT_WITHINGS_SECRET")
	if appSecret == "" {
		return errors.New("REDACTION_AUDIT_WITHINGS_SECRET is not set")
	}
	a.do(req{method: "PUT", path: "/api/v1/providers/withings/app-credentials", body: map[string]string{"client_id": "audit-client", "client_secret": appSecret}}, 200)
	a.do(req{method: "POST", path: "/api/v1/providers/withings/app-credentials/verify"}, 503)
	side := a.json(req{method: "POST", path: "/api/v1/sidecars", body: map[string]string{"name": "audit_sidecar", "url": "http://127.0.0.1:9"}}, 201)
	a.secrets = append(a.secrets, str(side, "secret"))
	a.do(req{method: "POST", path: "/api/v1/providers/audit_sidecar/probe"}, 200)
	a.do(req{method: "GET", path: "/api/v1/providers"}, 200)

	// Withings: the authorization begins, the token exchange (client secret, sentinel code)
	// fails at the dead proxy, and a webhook with an unknown token is refused.
	begin := a.json(req{method: "POST", path: "/api/v1/providers/withings/auth/begin"}, 200)
	redirect, err := url.Parse(str(begin, "redirect_url"))
	if err != nil {
		return fmt.Errorf("redirect_url: %w", err)
	}
	state, oauthCode := redirect.Query().Get("state"), sentinel("oauthcode")
	a.secrets = append(a.secrets, state, oauthCode)
	_, _, h := a.do(req{method: "GET", path: "/oauth/withings/callback?" + url.Values{"code": {oauthCode}, "state": {state}}.Encode()}, 303)
	if loc := h.Get("Location"); !strings.Contains(loc, "auth_error=exchange_failed") {
		failf("oauth callback: want exchange_failed, got %q", loc)
	}
	hook := sentinel("hooktoken")
	a.secrets = append(a.secrets, hook)
	a.do(req{method: "POST", path: "/webhooks/withings/" + hook, ctype: "application/x-www-form-urlencoded", body: []byte("userid=1&appli=4&startdate=1&enddate=2")}, 404)

	// Lab documents: the fake extraction succeeds; consented calls to openai_compatible (closed
	// local port) and gemini (dead proxy) fail. One document each: a failed run stays queued.
	extraction := func(pdfName string, body map[string]any, done func(map[string]any) bool) {
		pdf, err := os.ReadFile(filepath.Join(lab, pdfName)) //nolint:gosec // fixed fixture names in a CLI-given directory
		if err != nil {
			failf("%v", err)
		}
		filename := sentinel("healthfilename") + ".pdf"
		a.health = append(a.health, filename)
		var form bytes.Buffer
		mw := multipart.NewWriter(&form)
		fw, _ := mw.CreateFormFile("file", filename)
		_, _ = fw.Write(pdf)
		_ = mw.Close()
		doc := a.json(req{method: "POST", path: "/api/v1/documents", bearer: pat, ctype: mw.FormDataContentType(), body: form.Bytes()}, 201)
		run := a.json(req{method: "POST", path: "/api/v1/documents/" + str(doc, "id") + "/extractions", bearer: pat, body: body}, 202)
		poll("extraction "+str(body, "provider"), func() bool {
			return done(a.json(req{method: "GET", path: "/api/v1/extractions/" + str(run, "id"), bearer: pat}, 200))
		})
	}
	extraction("lab-01.pdf", map[string]any{"provider": "fake"}, func(m map[string]any) bool {
		if s := str(m, "status"); s == "failed" {
			failf("fake extraction failed: %s", str(m, "error_class"))
		}
		return str(m, "status") == "succeeded"
	})
	for i, provider := range []string{"openai_compatible", "gemini"} {
		a.do(req{method: "PATCH", path: "/api/v1/settings", bearer: pat, body: map[string]bool{"documents.external_ai." + provider + ".enabled": true}}, 200)
		consent := map[string]any{"provider": provider, "model": aiModel, "acknowledged_at": time.Now().UTC().Format(time.RFC3339)}
		extraction(fmt.Sprintf("lab-%02d.pdf", i+2), map[string]any{"provider": provider, "consent": consent},
			func(m map[string]any) bool { return str(m, "error_class") != "" })
	}

	// Export with raw payloads: the archive is scanned for secrets.
	exp := a.json(req{method: "POST", path: "/api/v1/exports", bearer: pat, body: map[string]any{"format": "ndjson", "include_raw": true}}, 202)
	var download string
	poll("export", func() bool {
		m := a.json(req{method: "GET", path: "/api/v1/exports/" + str(exp, "id"), bearer: pat}, 200)
		if str(m, "status") == "failed" {
			failf("export failed")
		}
		download = str(m, "download_url")
		return download != ""
	})
	if u, err := url.Parse(download); err == nil {
		for _, vs := range u.Query() {
			a.secrets = append(a.secrets, vs...)
		}
		download = u.RequestURI()
	}
	_, archive, _ := a.do(req{method: "GET", path: download, bearer: pat}, 200)
	if err := unzip(archive, filepath.Join(a.out, "export")); err != nil {
		return err
	}

	// Password change, then sign out.
	newPassword := sentinel("newpassword")
	a.secrets = append(a.secrets, newPassword)
	a.do(req{method: "POST", path: "/api/v1/auth/password", body: map[string]string{"current_password": password, "new_password": newPassword}}, 204)
	a.do(req{method: "POST", path: "/api/v1/auth/logout"}, 204)

	if err := appendLines(filepath.Join(a.out, "secrets.txt"), a.secrets); err != nil {
		return err
	}
	return appendLines(filepath.Join(a.out, "health.txt"), a.health)
}

// clientToken issues an ingest token directly; device pairing has no API yet (E15).
func clientToken(userID, connID uuid.UUID) (string, error) {
	ctx := context.Background()
	pool, err := db.Open(ctx, os.Getenv("VITAMUX_DATABASE_URL"), db.AppRole)
	if err != nil {
		return "", err
	}
	defer pool.Close()
	_, tok, err := auth.CreateClientToken(ctx, db.New(pool), userID, connID, "device", "redaction-audit")
	return tok, err
}

func randomSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		failf("url: %v", err)
	}
	return u
}

func unzip(b []byte, dir string) error {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return fmt.Errorf("export archive: %w", err)
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		path := filepath.Join(dir, filepath.Clean("/"+f.Name)) // rooted Clean drops any ".."
		rc, err := f.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func appendLines(path string, lines []string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // the CLI-given output directory
	if err != nil {
		return err
	}
	for _, l := range lines {
		if l != "" {
			_, _ = fmt.Fprintln(f, l)
		}
	}
	return f.Close()
}
