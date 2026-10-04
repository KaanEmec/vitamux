package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

// confChecks run in this order. Each is independent: when its input comes from an earlier check
// that failed, it is skipped instead of failing for the same reason a second time.
var confChecks = []struct {
	name string
	run  func(*confRun, context.Context) (string, error)
}{
	{"protocol_header", (*confRun).checkProtocolHeader},
	{"describe", (*confRun).checkDescribe},
	{"bad_bearer", (*confRun).checkBadBearer},
	{"auth_flow", (*confRun).checkAuthFlow},
	{"auth_refresh", (*confRun).checkAuthRefresh},
	{"fetch_lines", (*confRun).checkFetchLines},
	{"fetch_paging", (*confRun).checkFetchPaging},
	{"fetch_replay", (*confRun).checkFetchReplay},
	{"rotated_credentials", (*confRun).checkRotated},
	{"typed_errors", (*confRun).checkTypedErrors},
	{"retry_after", (*confRun).checkRetryAfter},
	{"drift_report", (*confRun).checkDriftReport},
	{"no_secret_echo", (*confRun).checkNoSecretEcho},
}

// The error classes a scenario can provoke, in the order they are checked.
var confClasses = []string{connectors.ClassReauthRequired, connectors.ClassRateLimited, connectors.ClassTransient, connectors.ClassSchemaDrift}

func (r *confRun) checkProtocolHeader(ctx context.Context) (string, error) {
	resp, err := r.describe(ctx)
	if err != nil {
		return "", err
	}
	bad, err := r.call(ctx, http.MethodGet, "/v1/describe", nil, "", maxSmall, 30*time.Second)
	if err != nil {
		return "", err
	}
	for what, h := range map[string]http.Header{"describe": resp.header, "a rejected request": bad.header} {
		if got := h.Get(ProtocolHeader); got != Protocol {
			if got == "" {
				return "", fmt.Errorf("the response to %s lacks the %s header (want %s)", what, ProtocolHeader, Protocol)
			}
			return "", fmt.Errorf("the response to %s has %s %q, want %s", what, ProtocolHeader, got, Protocol)
		}
	}
	return "every response carries " + ProtocolHeader + ": " + Protocol, nil
}

// confStub lets connectors.Validate judge a descriptor as the core's registry will.
type confStub struct{ d connectors.Descriptor }

func (s confStub) Describe() connectors.Descriptor { return s.d }
func (confStub) Plan(context.Context, connectors.Conn, connectors.PlanRequest) ([]connectors.WorkUnit, error) {
	return nil, nil
}
func (confStub) Fetch(context.Context, connectors.Conn, connectors.Credentials, connectors.WorkUnit, *connectors.RawSink) (connectors.FetchResult, error) {
	return connectors.FetchResult{}, nil
}
func (confStub) Refresh(context.Context, connectors.Conn, connectors.Credentials) (connectors.Credentials, error) {
	return connectors.Credentials{}, nil
}

func (r *confRun) checkDescribe(ctx context.Context) (string, error) {
	resp, err := r.describe(ctx)
	switch {
	case err != nil:
		return "", err
	case resp.status != http.StatusOK:
		return "", failure("GET /v1/describe", resp)
	case mediaType(resp.header) != "application/json":
		return "", errors.New("GET /v1/describe is not application/json")
	}
	if err := r.valid("describe", resp.body); err != nil {
		return "", fmt.Errorf("describe does not match the schema (%w)", err)
	}
	d, err := DecodeDescribe(resp.body)
	if err != nil {
		return "", err
	}
	if err := connectors.Validate(confStub{d}, d); err != nil {
		return "", fmt.Errorf("the core would refuse this descriptor (%w)", err)
	}
	return fmt.Sprintf("describe matches the schema and the registry rules (%s, %d stream(s), auth %s)", d.Provider, len(d.Streams), d.AuthKind), nil
}

// checkBadBearer: a missing or wrong secret is a 401 permanent problem on every endpoint.
func (r *confRun) checkBadBearer(ctx context.Context) (string, error) {
	probes := []struct{ what, method, path, auth string }{
		{"without a secret", http.MethodGet, "/v1/describe", ""},
		{"with another secret", http.MethodGet, "/v1/describe", "Bearer synthetic-wrong-secret"},
		{"with the secret and a suffix", http.MethodGet, "/v1/describe", r.bearer() + "x"},
		{"with another secret", http.MethodPost, "/v1/fetch", "Bearer synthetic-wrong-secret"},
	}
	for _, p := range probes {
		var body any
		if p.method == http.MethodPost {
			body = map[string]any{"stream": r.stream(ctx), "credentials": map[string]any{}}
		}
		resp, err := r.call(ctx, p.method, p.path, body, p.auth, maxSmall, 30*time.Second)
		if err != nil {
			return "", err
		}
		if resp.status != http.StatusUnauthorized {
			return "", fmt.Errorf("%s %s %s answered HTTP %d, want 401", p.method, p.path, p.what, resp.status)
		}
		pr, err := r.problem(resp)
		if err != nil {
			return "", fmt.Errorf("%s %s %s: %w", p.method, p.path, p.what, err)
		}
		if pr.Code != connectors.ClassPermanent {
			return "", fmt.Errorf("%s %s %s has code %q, want permanent", p.method, p.path, p.what, pr.Code)
		}
	}
	return "a missing or wrong secret is rejected with 401 permanent on describe and fetch", nil
}

// Auth.

var errNoLogin = confSkip("the scenario has no login")

type confLogin struct {
	creds connectors.Credentials
	steps int
	err   error
}

// authReply checks a begin or continue response against the schema and the wire decoder.
func (r *confRun) authReply(what string, resp confResp, err error) (AuthResponse, error) {
	var out AuthResponse
	switch {
	case err != nil:
		return out, err
	case resp.status != http.StatusOK:
		return out, failure(what, resp)
	}
	if err := r.valid("auth_response", resp.body); err != nil {
		return out, fmt.Errorf("%s response does not match the schema (%w)", what, err)
	}
	if _, err := DecodeAuth(resp.body); err != nil {
		return out, fmt.Errorf("%s response: %w", what, err)
	}
	_ = json.Unmarshal(resp.body, &out)
	return out, nil
}

// signIn runs the subject's login: begin, then one continue per answer. A subject without a
// login only has its first step checked.
func (r *confRun) signIn(ctx context.Context, s Subject) confLogin {
	if s.Credentials != nil {
		r.addCredentials(*s.Credentials)
		return confLogin{creds: *s.Credentials}
	}
	resp, err := r.post(ctx, "/v1/auth/begin", BeginRequest(connectors.AuthInput{RedirectURL: confRedirect, State: "synthetic-state"}, nil))
	cur, err := r.authReply("auth/begin", resp, err)
	if err != nil {
		return confLogin{err: err}
	}
	if cur.Step == nil {
		return confLogin{err: errors.New("auth/begin did not return a step")}
	}
	if len(s.Login) == 0 {
		return confLogin{err: errNoLogin}
	}
	for i, a := range s.Login {
		if cur.Step == nil {
			return confLogin{err: fmt.Errorf("auth/continue authorized after %d of the scenario's %d answers", i, len(s.Login))}
		}
		if p := cur.Step.Prompt; p != nil {
			for _, f := range p.Fields {
				if f.Kind != "text" {
					r.addSecret(a.Values[f.Name])
				}
			}
		}
		req := AuthContinueRequest{RedirectURL: confRedirect, Callback: a.Callback, Values: a.Values, Session: cur.Step.Session}
		resp, err := r.post(ctx, "/v1/auth/continue", req)
		if cur, err = r.authReply(fmt.Sprintf("auth/continue (answer %d)", i+1), resp, err); err != nil {
			return confLogin{err: err}
		}
	}
	if cur.Authorized == nil {
		return confLogin{err: fmt.Errorf("auth/continue still asks for input after the scenario's %d answers", len(s.Login))}
	}
	r.addCredentials(cur.Authorized.Credentials)
	return confLogin{creds: cur.Authorized.Credentials, steps: len(s.Login)}
}

func (r *confRun) addCredentials(c connectors.Credentials) {
	r.addSecret(c.AccessToken)
	r.addSecret(c.RefreshToken)
}

// login signs in as the subject once per key. When the scenario's own login already failed
// (auth_flow reports it), other logins are skipped: they would only repeat the failure.
func (r *confRun) login(ctx context.Context, key string, s Subject) confLogin {
	if r.logins[key] == nil {
		l := r.signIn(ctx, s)
		if key != "" && l.err != nil {
			if m := r.login(ctx, "", r.scn.Subject); m.err != nil && !errors.Is(m.err, errNoLogin) {
				l.err = confSkip("auth_flow failed")
			}
		}
		r.logins[key] = &l
	}
	return *r.logins[key]
}

// fetchBroken: the scenario's own fetch broke the page protocol, which fetch_lines reports.
func (r *confRun) fetchBroken() bool {
	return slices.ContainsFunc(r.pages, func(p confPage) bool { return p.err != nil })
}

// replayCreds are all a scenario-less run has: only a sidecar in REPLAY mode serves them.
var replayCreds = connectors.Credentials{AccessToken: "synthetic-access-token", RefreshToken: "synthetic-refresh-token"}

// mainCreds are the credentials of the scenario's own subject, or a skip when sign-in failed.
func (r *confRun) mainCreds(ctx context.Context) (connectors.Credentials, error) {
	l := r.login(ctx, "", r.scn.Subject)
	switch {
	case errors.Is(l.err, errNoLogin):
		return replayCreds, nil
	case l.err != nil:
		return connectors.Credentials{}, confSkip("auth_flow failed, so there are no credentials")
	}
	return l.creds, nil
}

func (r *confRun) checkAuthFlow(ctx context.Context) (string, error) {
	if k := r.authKind(ctx); k == string(connectors.AuthNone) {
		return "", confSkip("auth kind none")
	}
	l := r.login(ctx, "", r.scn.Subject)
	switch {
	case errors.Is(l.err, errNoLogin):
		return "the first auth step is valid; the scenario has no login, so the later steps were not run", nil
	case l.err != nil:
		return "", l.err
	case r.scn.Credentials != nil:
		return "", confSkip("the scenario gives credentials instead of a login")
	}
	return fmt.Sprintf("auth/begin and %d auth/continue call(s) round-tripped the session and ended authorized", l.steps), nil
}

func (r *confRun) checkAuthRefresh(ctx context.Context) (string, error) {
	if r.authKind(ctx) == string(connectors.AuthNone) {
		return "", confSkip("auth kind none")
	}
	creds, err := r.mainCreds(ctx)
	switch {
	case err != nil:
		return "", err
	case creds.RefreshToken == "":
		return "", confSkip("the credentials have no refresh token")
	case creds.AccessToken == replayCreds.AccessToken:
		return "", confSkip("the scenario has no login")
	}
	refreshed, err := r.refresh(ctx, creds)
	if err != nil {
		return "", err
	}
	if refreshed.AccessToken == "" && refreshed.RefreshToken == "" {
		return "", errors.New("auth/refresh returned no tokens")
	}
	return "auth/refresh returned valid credentials", nil
}

func (r *confRun) refresh(ctx context.Context, c connectors.Credentials) (connectors.Credentials, error) {
	resp, err := r.post(ctx, "/v1/auth/refresh", RefreshRequest{Credentials: c})
	switch {
	case err != nil:
		return c, err
	case resp.status != http.StatusOK:
		return c, failure("auth/refresh", resp)
	}
	if err := r.valid("refresh_response", resp.body); err != nil {
		return c, fmt.Errorf("auth/refresh response does not match the schema (%w)", err)
	}
	var out RefreshResponse
	_ = json.Unmarshal(resp.body, &out)
	r.addCredentials(out.Credentials)
	return out.Credentials, nil
}

// Fetch.

// confPage is one parsed fetch response. err is a protocol violation, fail a typed error the
// sidecar ended the page with (a problem response, or an error line after raw lines).
type confPage struct {
	cursor json.RawMessage
	header http.Header // of an HTTP error response; nil for a page
	raws   []ingest.RawItem
	result *ResultLine
	fail   *Problem
	err    error
}

func (r *confRun) fetchRequest(ctx context.Context, creds connectors.Credentials, cursor json.RawMessage) FetchRequest {
	req := FetchRequest{Stream: r.stream(ctx), Mode: r.scn.Mode, Cursor: cursor, Credentials: creds}
	if req.Mode == "" {
		req.Mode = "incremental"
	}
	if req.Mode == "correction" || req.Mode == "backfill" {
		req.To = time.Now().UTC().Truncate(time.Second)
		req.From = req.To.Add(-24 * time.Hour)
	}
	return req
}

func (r *confRun) fetch(ctx context.Context, creds connectors.Credentials, cursor json.RawMessage) confPage {
	p := confPage{cursor: cursor}
	resp, err := r.call(ctx, http.MethodPost, "/v1/fetch", r.fetchRequest(ctx, creds, cursor), r.bearer(), maxPage, 5*time.Minute)
	if err != nil {
		p.err = err
		return p
	}
	if resp.status != http.StatusOK {
		p.header = resp.header
		pr, err := r.problem(resp)
		if err != nil {
			p.err = err
		} else {
			p.fail = &pr
		}
		return p
	}
	if mt := mediaType(resp.header); mt != "application/x-ndjson" {
		p.err = fmt.Errorf("fetch answered Content-Type %q, want application/x-ndjson", mt)
		return p
	}
	p.err = r.parseLines(&p, resp.body)
	return p
}

// parseLines checks the page grammar: raw lines, then exactly one result or error line.
func (r *confRun) parseLines(p *confPage, body []byte) error {
	lines := bytes.Split(bytes.TrimSuffix(body, []byte("\n")), []byte("\n"))
	for i, line := range lines {
		n := i + 1
		if p.result != nil || p.fail != nil {
			return fmt.Errorf("line %d follows the final line of the page", n)
		}
		if len(line) > maxLine {
			return fmt.Errorf("line %d is over 36 MiB", n)
		}
		var head struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &head) != nil {
			return fmt.Errorf("line %d is not JSON", n)
		}
		def := map[string]string{LineRaw: "raw_line", LineResult: "result_line", LineError: "error_line"}[head.Type]
		if def == "" {
			return fmt.Errorf("line %d has an unknown type", n)
		}
		if err := r.valid(def, line); err != nil {
			return fmt.Errorf("line %d does not match the %s schema (%w)", n, def, err)
		}
		switch head.Type {
		case LineRaw:
			l, err := DecodeLine(line)
			if err != nil {
				return fmt.Errorf("line %d: %w", n, err)
			}
			p.raws = append(p.raws, *l.Raw)
		case LineResult:
			p.result = &ResultLine{}
			_ = json.Unmarshal(line, p.result)
			if p.result.Credentials != nil {
				r.addCredentials(*p.result.Credentials)
			}
		case LineError:
			p.fail = &Problem{}
			_ = json.Unmarshal(line, p.fail)
			r.errBodies = append(r.errBodies, line)
		}
	}
	if p.result == nil && p.fail == nil {
		return errors.New("the page ends without a result or error line")
	}
	return nil
}

// cleanPage fetches one page for the credentials and fails when it is not a clean success.
func (r *confRun) cleanPage(ctx context.Context, creds connectors.Credentials, cursor json.RawMessage) (confPage, error) {
	p := r.fetch(ctx, creds, cursor)
	switch {
	case p.err != nil:
		return p, p.err
	case p.fail != nil:
		return p, fmt.Errorf("fetch ended with the error %q instead of a page", p.fail.Code)
	}
	return p, nil
}

// fetchAll pages through the main subject's stream until done, once for all fetch checks.
func (r *confRun) fetchAll(ctx context.Context) ([]confPage, error) {
	creds, err := r.mainCreds(ctx)
	if err != nil {
		return nil, err
	}
	if !r.pagesRan {
		r.pagesRan = true
		var cursor json.RawMessage
		seen := map[string]bool{}
		for range maxPages {
			p := r.fetch(ctx, creds, cursor)
			r.pages = append(r.pages, p)
			if p.err != nil || p.fail != nil || p.result.Done {
				return r.pages, nil
			}
			next := p.result.NextCursor
			switch key := compactJSON(next); {
			case next == nil || key == compactJSON(cursor):
				r.pageStop = "a page that is not done did not advance the cursor"
				return r.pages, nil
			case seen[key]:
				r.pageStop = "the cursor went back to one seen before"
				return r.pages, nil
			default:
				seen[key] = true
				cursor = next
			}
		}
		r.pageStop = fmt.Sprintf("the stream is not done after %d pages", maxPages)
	}
	return r.pages, nil
}

func (r *confRun) checkFetchLines(ctx context.Context) (string, error) {
	pages, err := r.fetchAll(ctx)
	if err != nil {
		return "", err
	}
	raws := 0
	for i, p := range pages {
		switch {
		case p.err != nil:
			return "", fmt.Errorf("page %d: %w", i+1, p.err)
		case p.fail != nil:
			return "", fmt.Errorf("page %d ended with the error %q for the scenario's login", i+1, p.fail.Code)
		}
		raws += len(p.raws)
	}
	return fmt.Sprintf("%d page(s), %d raw line(s): every line matches its schema and each page ends in one result line", len(pages), raws), nil
}

func (r *confRun) checkFetchPaging(ctx context.Context) (string, error) {
	pages, err := r.fetchAll(ctx)
	if err != nil {
		return "", err
	}
	if last := pages[len(pages)-1]; last.err != nil || last.fail != nil {
		return "", confSkip("fetch_lines failed")
	}
	if r.pageStop != "" {
		return "", fmt.Errorf("paging stopped on page %d: %s", len(pages), r.pageStop)
	}
	return fmt.Sprintf("%d page(s): the cursor advanced on every page and the last page was done", len(pages)), nil
}

// rawSig identifies a raw line by what a replay must reproduce (not fetched_at).
func rawSig(it ingest.RawItem) string {
	return strings.Join([]string{it.Stream, it.ExternalKey, it.ContentType, strconv.FormatBool(it.Quarantine), compactJSON(it.Body)}, "\x00")
}

func (r *confRun) checkFetchReplay(ctx context.Context) (string, error) {
	pages, err := r.fetchAll(ctx)
	if err != nil {
		return "", err
	}
	if last := pages[len(pages)-1]; last.err != nil || last.fail != nil || r.pageStop != "" {
		return "", confSkip("fetch_lines or fetch_paging failed")
	}
	creds, _ := r.mainCreds(ctx)
	picks := []int{0}
	if n := len(pages); n > 1 {
		picks = append(picks, n/2, n-1)
	}
	picks = slices.Compact(picks)
	for _, i := range picks {
		again, err := r.cleanPage(ctx, creds, pages[i].cursor)
		if err != nil {
			return "", fmt.Errorf("replaying the cursor of page %d: %w", i+1, err)
		}
		a, b := rawSigs(pages[i].raws), rawSigs(again.raws)
		if !slices.Equal(a, b) {
			return "", fmt.Errorf("the cursor of page %d returned different raw lines the second time", i+1)
		}
		if compactJSON(pages[i].result.NextCursor) != compactJSON(again.result.NextCursor) || pages[i].result.Done != again.result.Done {
			return "", fmt.Errorf("the cursor of page %d returned another next_cursor or done the second time", i+1)
		}
	}
	return fmt.Sprintf("the same cursor returned identical raw lines on %d page(s)", len(picks)), nil
}

func rawSigs(items []ingest.RawItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = rawSig(it)
	}
	return out
}

// Rotated credentials.

func (r *confRun) checkRotated(ctx context.Context) (string, error) {
	if r.scn.Rotated == nil {
		return "", confSkip("the scenario has no rotated login")
	}
	l := r.login(ctx, "rotated", *r.scn.Rotated)
	if l.err != nil {
		return "", fmt.Errorf("sign-in for the rotated scenario failed: %w", l.err)
	}
	p, err := r.cleanPage(ctx, l.creds, nil)
	switch {
	case err != nil && p.err != nil && r.fetchBroken():
		return "", confSkip("fetch_lines failed")
	case err != nil:
		return "", fmt.Errorf("fetch for the rotated scenario: %w", err)
	}
	got := p.result.Credentials
	switch {
	case got == nil:
		return "", errors.New("the result line of the rotated scenario carries no credentials")
	case got.AccessToken == l.creds.AccessToken && got.RefreshToken == l.creds.RefreshToken:
		return "", errors.New("the credentials on the result line are the ones that were sent, not rotated ones")
	}
	if l.creds.RefreshToken == "" {
		return "rotated credentials come back on the result line", nil
	}
	fresh, err := r.refresh(ctx, l.creds)
	if err != nil {
		return "", err
	}
	if fresh.AccessToken == l.creds.AccessToken && fresh.RefreshToken == l.creds.RefreshToken {
		return "", errors.New("auth/refresh returned the credentials it was given instead of new ones")
	}
	return "fetch hands back rotated credentials and auth/refresh returns new tokens", nil
}

// Typed errors.

// errPage is the first fetch page of the scenario's subject for one error class, once.
func (r *confRun) errPage(ctx context.Context, class string) (*confPage, error) {
	if p, ok := r.errPages[class]; ok {
		return p, nil
	}
	l := r.login(ctx, class, r.scn.Errors[class])
	if l.err != nil {
		return nil, fmt.Errorf("sign-in for the %s scenario failed: %w", class, l.err)
	}
	p := r.fetch(ctx, l.creds, nil)
	r.errPages[class] = &p
	return &p, nil
}

func (r *confRun) checkTypedErrors(ctx context.Context) (string, error) {
	var done []string
	for _, class := range confClasses {
		if _, ok := r.scn.Errors[class]; !ok {
			continue
		}
		p, err := r.errPage(ctx, class)
		switch {
		case err != nil:
			return "", err
		case p.err != nil && r.fetchBroken():
			return "", confSkip("fetch_lines failed")
		case p.err != nil:
			return "", fmt.Errorf("fetch for the %s scenario: %w", class, p.err)
		case p.fail == nil:
			return "", fmt.Errorf("fetch for the %s scenario succeeded, want the error %q", class, class)
		case p.fail.Code != class:
			return "", fmt.Errorf("fetch for the %s scenario ended with the error %q", class, p.fail.Code)
		}
		done = append(done, class)
	}
	if len(done) == 0 {
		return "", confSkip("the scenario has no error cases")
	}
	return strings.Join(done, ", ") + " each ended in the typed error the scenario provokes", nil
}

// typedError is the page of an error scenario that did end in that class, or a skip.
func (r *confRun) typedError(ctx context.Context, class string) (*confPage, error) {
	if _, ok := r.scn.Errors[class]; !ok {
		return nil, confSkip("the scenario has no " + class + " case")
	}
	p, err := r.errPage(ctx, class)
	if err != nil || p.err != nil || p.fail == nil || p.fail.Code != class {
		return nil, confSkip("typed_errors failed for " + class)
	}
	return p, nil
}

func (r *confRun) checkRetryAfter(ctx context.Context) (string, error) {
	p, err := r.typedError(ctx, connectors.ClassRateLimited)
	if err != nil {
		return "", err
	}
	if p.fail.RetryAfterS < 1 {
		return "", errors.New("rate_limited carries no retry_after_s")
	}
	if h := p.header.Get("Retry-After"); h != "" && h != strconv.FormatInt(p.fail.RetryAfterS, 10) {
		return "", fmt.Errorf("the Retry-After header %q differs from retry_after_s %d", h, p.fail.RetryAfterS)
	}
	return fmt.Sprintf("rate_limited asks to wait %d s", p.fail.RetryAfterS), nil
}

func (r *confRun) checkDriftReport(ctx context.Context) (string, error) {
	p, err := r.typedError(ctx, connectors.ClassSchemaDrift)
	if err != nil {
		return "", err
	}
	if p.fail.Endpoint == "" || p.fail.Fingerprint == "" {
		return "", errors.New("schema_drift names no endpoint or no fingerprint of the unexpected shape")
	}
	for _, it := range p.raws {
		if !it.Quarantine {
			return "", errors.New("a raw line before the schema_drift error is not marked quarantine")
		}
	}
	return fmt.Sprintf("schema_drift names the endpoint and fingerprint, and its %d raw line(s) are quarantined", len(p.raws)), nil
}

// Secrets.

// checkNoSecretEcho provokes more errors, with the real secrets in the request where the
// protocol allows, then looks for any of them in every error body and response header seen.
func (r *confRun) checkNoSecretEcho(ctx context.Context) (string, error) {
	values := map[string]string{}
	for s := range r.secrets {
		values["f"+strconv.Itoa(len(values))] = s
	}
	creds := connectors.Credentials{AccessToken: replayCreds.AccessToken}
	for s := range r.secrets {
		creds.AccessToken, creds.RefreshToken = s, s
		break
	}
	probes := []func() error{
		func() error {
			_, err := r.call(ctx, http.MethodGet, "/v1/no-such-endpoint", nil, r.bearer(), maxSmall, 30*time.Second)
			return err
		},
		func() error {
			_, err := r.call(ctx, http.MethodPost, "/v1/fetch", []byte("{"), r.bearer(), maxSmall, 30*time.Second)
			return err
		},
		func() error {
			_, err := r.post(ctx, "/v1/auth/continue", AuthContinueRequest{RedirectURL: confRedirect, Values: values, Session: []byte("synthetic")})
			return err
		},
		func() error {
			_, err := r.post(ctx, "/v1/fetch", FetchRequest{Stream: "no_such.stream", Credentials: creds})
			return err
		},
		func() error {
			_, err := r.call(ctx, http.MethodGet, "/v1/describe", nil, "Bearer "+strings.Repeat("a", 8), maxSmall, 30*time.Second)
			return err
		},
	}
	for _, probe := range probes {
		if err := probe(); err != nil {
			return "", err
		}
	}
	for secret := range r.secrets {
		for _, b := range r.errBodies {
			if bytes.Contains(b, []byte(secret)) {
				return "", errors.New("an error response echoes a secret (the bearer secret, a password, a code or a token)")
			}
		}
		for _, h := range r.headers {
			for k, vs := range h {
				for _, v := range vs {
					if strings.Contains(v, secret) {
						return "", fmt.Errorf("the %s response header echoes a secret", k)
					}
				}
			}
		}
	}
	return fmt.Sprintf("no secret appears in %d error response(s) or %d response header set(s)", len(r.errBodies), len(r.headers)), nil
}
