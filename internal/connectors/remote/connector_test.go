package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

var t0 = time.Date(2026, 9, 14, 7, 0, 0, 0, time.UTC)

// fetchPage fetches one incremental page from cursor and returns what reached the sink.
func fetchPage(t *testing.T, c *Connector, cred connectors.Credentials, cursor json.RawMessage) ([]ingest.RawItem, connectors.FetchResult, error) {
	t.Helper()
	var items []ingest.RawItem
	res, err := c.fetch(t.Context(), connectors.Conn{Provider: fakeProvider}, cred, connectors.WorkUnit{Stream: fakeStream, Cursor: cursor},
		func(it ingest.RawItem) { items = append(items, it) })
	return items, res, err
}

// signedIn describes c and returns the fake's current credentials.
func signedIn(t *testing.T, f *fakeSidecar, c *Connector) connectors.Credentials {
	t.Helper()
	if err := c.Discover(t.Context()); err != nil {
		t.Fatal(err)
	}
	var cred connectors.Credentials
	f.do(func() { cred = f.issue() })
	return cred
}

func TestDescribe(t *testing.T) {
	f, srv := newFake(t)
	var reported []connectors.Descriptor
	c := newRemote(t, srv, defaultLimits, func(d *connectors.Descriptor) { reported = append(reported, *d) })
	if d := c.current(); d.Available() || d.Provider != fakeProvider || !d.Remote {
		t.Fatalf("before describe: %+v", d)
	}
	if err := c.Discover(t.Context()); err != nil {
		t.Fatal(err)
	}
	d := c.Describe() // cached: no second call
	if !d.Available() || !d.Remote || d.Official || d.Name != "Example sidecar" || d.Upstream.Version != "1.2.3" ||
		d.Streams[0].Interval != time.Hour || d.AuthKind != connectors.AuthInteractiveMFA {
		t.Fatalf("descriptor: %+v", d)
	}
	if f.called("/v1/describe") != 1 || len(reported) != 1 {
		t.Fatalf("%d describes, %d reports", f.called("/v1/describe"), len(reported))
	}

	// A minute later the next use describes again; the same upstream is not reported twice,
	// a new one is.
	stale := func() { c.mu.Lock(); c.checked = time.Now().Add(-describeEvery); c.mu.Unlock() }
	stale()
	c.Describe()
	f.do(func() { f.desc.Upstream.Version = "1.2.4" })
	stale()
	if d := c.Describe(); d.Upstream.Version != "1.2.4" || f.called("/v1/describe") != 3 || len(reported) != 2 {
		t.Fatalf("after upstream bump: %+v, %d describes, %d reports", d.Upstream, f.called("/v1/describe"), len(reported))
	}

	// The sidecar goes away: the placeholder (unavailable) until it is back.
	srv.Close()
	stale()
	if d := c.Describe(); d.Available() {
		t.Fatal("unreachable sidecar still available")
	}
	if d := c.Describe(); d.Available() || f.called("/v1/describe") != 3 {
		t.Fatal("unreachable sidecar described again within a minute")
	}
}

func TestDescribeRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(f *fakeSidecar)
		want   string
	}{
		"other provider": {func(f *fakeSidecar) { f.desc.Provider = "someone_else" }, "another provider"},
		"other protocol": {func(f *fakeSidecar) { f.desc.Protocol = "vitamux-connector/2" }, "another protocol"},
		"no streams":     {func(f *fakeSidecar) { f.desc.Streams = nil }, "without streams"},
		"invalid":        {func(f *fakeSidecar) { f.desc.Streams[0].IntervalS = 5 }, "interval"},
		"no header":      {func(f *fakeSidecar) { f.noHeader = true }, ProtocolHeader},
		"wrong secret": {func(f *fakeSidecar) {
			f.override["/v1/describe"] = func(w http.ResponseWriter, _ *http.Request) {
				problem(w, http.StatusUnauthorized, Problem{Code: connectors.ClassPermanent})
			}
		}, "permanent"},
		"too large": {func(f *fakeSidecar) {
			f.override["/v1/describe"] = func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"protocol":"` + strings.Repeat("x", 2048) + `"}`))
			}
		}, "size limit"},
	} {
		t.Run(name, func(t *testing.T) {
			f, srv := newFake(t)
			tc.mutate(f)
			l := defaultLimits
			l.message = 1024
			c := newRemote(t, srv, l, func(*connectors.Descriptor) { t.Error("refused descriptor reported") })
			err := c.Discover(t.Context())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if c.Describe().Available() {
				t.Fatal("refused descriptor in use")
			}
		})
	}
}

func TestPrivateNetworkOnly(t *testing.T) {
	for _, a := range []string{"127.0.0.1:1", "[::1]:1", "10.1.2.3:80", "172.16.0.1:80", "192.168.1.1:80", "169.254.1.1:80", "[fd00::1]:80", "[fe80::1]:80", "[::ffff:10.0.0.1]:80"} {
		if err := privateOnly("tcp", a, nil); err != nil {
			t.Errorf("%s refused", a)
		}
	}
	for _, a := range []string{"8.8.8.8:53", "192.0.2.1:80", "[2001:db8::1]:80", "0.0.0.0:80", "100.64.0.1:80", "not-an-address"} {
		if err := privateOnly("tcp", a, nil); !errors.Is(err, errNotPrivate) {
			t.Errorf("%s allowed", a)
		}
	}
	// A public address is refused at dial time, before anything is sent.
	u, _ := url.Parse("http://192.0.2.1:9")
	c := newConnector(Options{Name: fakeProvider, URL: u, Secret: fakeSecret}, defaultLimits)
	err := c.Discover(t.Context())
	if !errors.Is(err, errNotPrivate) || !errors.Is(err, connectors.ErrTransient) {
		t.Fatalf("public sidecar: %v", err)
	}
}

func TestFetchPages(t *testing.T) {
	f, srv := newFake(t)
	f.add(25, t0)
	c := newRemote(t, srv, defaultLimits, nil)
	cred := signedIn(t, f, c)
	var cursor json.RawMessage
	var all []ingest.RawItem
	for page := 0; ; page++ {
		items, res, err := fetchPage(t, c, cred, cursor)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, items...)
		cursor = res.NextCursor
		if res.Done {
			if page != 2 || !res.HighWatermark.Equal(t0.Add(24*5*time.Minute)) {
				t.Fatalf("done after page %d at %s", page, res.HighWatermark)
			}
			break
		}
	}
	if len(all) != 25 || all[0].ExternalKey != "sample:hr-0" || all[0].ContentType != "application/json" ||
		all[0].Request.Endpoint != "GET /v1/heart-rate" || all[0].Quarantine {
		t.Fatalf("%d items, first %+v", len(all), all[0])
	}
	if f.lastMode != connectors.ModeIncremental {
		t.Fatalf("mode %q", f.lastMode)
	}
	// A window unit sends its window and no mode.
	_, err := c.fetch(t.Context(), connectors.Conn{}, cred, connectors.WorkUnit{Stream: fakeStream, From: t0, To: t0.Add(time.Hour)}, func(ingest.RawItem) {})
	if err != nil || f.lastMode != "" {
		t.Fatalf("window: %v, mode %q", err, f.lastMode)
	}

	// Rotated credentials come back with the page.
	f.do(func() { f.rotate = true })
	_, res, err := fetchPage(t, c, cred, nil)
	if err != nil || res.Credentials == nil || res.Credentials.AccessToken == cred.AccessToken {
		t.Fatalf("rotation: %v %v", err, res.Credentials)
	}
}

// Every way a page can break the protocol discards the whole page with a transient error.
func TestFetchViolations(t *testing.T) {
	raw := `{"type":"raw","external_key":"sample:1","content_type":"application/json","body":{"id":"hr-1"}}`
	result := `{"type":"result","done":true}`
	for name, tc := range map[string]struct {
		body   string
		limits func(*limits)
		want   string
	}{
		"malformed line":    {ndjson(raw, "{not json", result), nil, "malformed fetch line"},
		"unknown line":      {ndjson(raw, `{"type":"progress"}`, result), nil, "unknown fetch line type"},
		"missing result":    {ndjson(raw, raw), nil, "without a result or error line"},
		"empty":             {"", nil, "without a result or error line"},
		"after result":      {ndjson(raw, result, raw), nil, "after the result line"},
		"undeclared stream": {ndjson(strings.Replace(raw, `"type":"raw"`, `"type":"raw","stream":"other.stream"`, 1), result), nil, "undeclared stream"},
		"bad raw line":      {ndjson(`{"type":"raw","external_key":"k","content_type":"application/json","body":"text"}`, result), nil, "object or array"},
		"bad sha256": {ndjson(`{"type":"raw","external_key":"k","content_type":"application/pdf","body_base64":"AAEC","sha256":"00"}`, result), nil,
			"sha256"},
		"negative retry": {ndjson(raw, `{"type":"result","done":true,"retry_after_s":-1}`), nil, "malformed result line"},
		"line too long": {ndjson(raw, `{"type":"raw","external_key":"k","content_type":"application/json","body":{"x":"`+strings.Repeat("a", 300)+`"}}`, result),
			func(l *limits) { l.line = 256 }, "line over"},
		"page too large": {ndjson(raw, raw, raw, raw, raw, result), func(l *limits) { l.page = 400 }, "size limit"},
	} {
		t.Run(name, func(t *testing.T) {
			f, srv := newFake(t)
			l := defaultLimits
			if tc.limits != nil {
				tc.limits(&l)
			}
			c := newRemote(t, srv, l, nil)
			cred := signedIn(t, f, c)
			f.do(func() {
				f.override["/v1/fetch"] = func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/x-ndjson")
					_, _ = w.Write([]byte(tc.body))
				}
			})
			items, _, err := fetchPage(t, c, cred, nil)
			if !errors.Is(err, connectors.ErrTransient) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want transient %q", err, tc.want)
			}
			if len(items) != 0 {
				t.Fatalf("%d items of a broken page reached the sink", len(items))
			}
		})
	}
}

func TestFetchTimeoutAndHeader(t *testing.T) {
	f, srv := newFake(t)
	f.add(3, t0)
	l := defaultLimits
	l.fetch = 100 * time.Millisecond
	c := newRemote(t, srv, l, nil)
	cred := signedIn(t, f, c)

	// A page that stalls after its first lines times out as a whole.
	f.do(func() {
		f.override["/v1/fetch"] = func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(ndjson(`{"type":"raw","external_key":"k","content_type":"application/json","body":{}}`)))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
	})
	start := time.Now()
	items, _, err := fetchPage(t, c, cred, nil)
	if !errors.Is(err, connectors.ErrTransient) || len(items) != 0 || time.Since(start) > 5*time.Second {
		t.Fatalf("stalled page: %v, %d items", err, len(items))
	}

	// A response without the protocol header is refused.
	f.do(func() { delete(f.override, "/v1/fetch"); f.noHeader = true })
	if _, _, err := fetchPage(t, c, cred, nil); !errors.Is(err, connectors.ErrTransient) || !strings.Contains(err.Error(), ProtocolHeader) {
		t.Fatalf("no header: %v", err)
	}
}

// Problem responses and error lines map to the typed errors the runtime acts on.
func TestFetchTypedErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		header string
		body   string // problem+json, or NDJSON when status is 200
		check  func(t *testing.T, err error)
	}{
		"reauth": {401, "", `{"code":"reauth_required"}`, func(t *testing.T, err error) {
			if !errors.Is(err, connectors.ErrReauthRequired) {
				t.Fatal(err)
			}
		}},
		"permanent": {422, "", `{"code":"permanent","detail":"bad config"}`, func(t *testing.T, err error) {
			if !errors.Is(err, connectors.ErrPermanent) {
				t.Fatal(err)
			}
		}},
		"transient":          {503, "", `{"code":"transient"}`, isTransient},
		"unknown code":       {503, "", `{"code":"meteor_strike"}`, isTransient},
		"not a problem":      {502, "", `<html>bad gateway</html>`, isTransient},
		"rate limited":       {429, "120", `{"code":"rate_limited","retry_after_s":7}`, retryAfter(7 * time.Second)},
		"retry-after header": {429, "120", `{"code":"rate_limited"}`, retryAfter(2 * time.Minute)},
		"no delay":           {429, "", `{"code":"rate_limited"}`, retryAfter(time.Minute)},
		"bogus delay":        {429, "", `{"code":"rate_limited","retry_after_s":999999999}`, retryAfter(24 * time.Hour)},
		"drift problem": {502, "", `{"code":"schema_drift","endpoint":"GET /v1/x","fingerprint":"sha256:ab"}`, func(t *testing.T, err error) {
			var d *connectors.SchemaDriftError
			if !errors.As(err, &d) || d.Endpoint != "GET /v1/x" || d.Fingerprint != "sha256:ab" {
				t.Fatal(err)
			}
		}},
		"rate limited line": {200, "", ndjson(`{"type":"error","code":"rate_limited"}`), retryAfter(time.Minute)},
		"reauth line": {200, "", ndjson(`{"type":"error","code":"reauth_required"}`), func(t *testing.T, err error) {
			if !errors.Is(err, connectors.ErrReauthRequired) {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			f, srv := newFake(t)
			c := newRemote(t, srv, defaultLimits, nil)
			cred := signedIn(t, f, c)
			f.do(func() {
				f.override["/v1/fetch"] = func(w http.ResponseWriter, _ *http.Request) {
					if tc.header != "" {
						w.Header().Set("Retry-After", tc.header)
					}
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				}
			})
			items, _, err := fetchPage(t, c, cred, nil)
			tc.check(t, err)
			if len(items) != 0 {
				t.Fatal("items reached the sink")
			}
		})
	}
}

func isTransient(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, connectors.ErrTransient) {
		t.Fatalf("not transient: %v", err)
	}
}

func retryAfter(d time.Duration) func(*testing.T, error) {
	return func(t *testing.T, err error) {
		t.Helper()
		var rl *connectors.RateLimitedError
		if !errors.As(err, &rl) || rl.RetryAfter != d {
			t.Fatalf("got %v, want rate limited for %s", err, d)
		}
	}
}

// Raw lines before a schema_drift error line reach the sink (the runtime quarantines them);
// before any other error line they are dropped.
func TestFetchDrift(t *testing.T) {
	f, srv := newFake(t)
	f.add(4, t0)
	c := newRemote(t, srv, defaultLimits, nil)
	cred := signedIn(t, f, c)
	f.do(func() { f.drift = true })
	items, _, err := fetchPage(t, c, cred, nil)
	var d *connectors.SchemaDriftError
	if !errors.As(err, &d) || d.Fingerprint != "sha256:synthetic" || len(items) != 4 {
		t.Fatalf("drift: %v, %d items", err, len(items))
	}
	f.do(func() {
		f.override["/v1/fetch"] = func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(ndjson(`{"type":"raw","external_key":"k","content_type":"application/json","body":{}}`, `{"type":"error","code":"transient"}`)))
		}
	})
	if items, _, err := fetchPage(t, c, cred, nil); !errors.Is(err, connectors.ErrTransient) || len(items) != 0 {
		t.Fatalf("transient line: %v, %d items", err, len(items))
	}
}

func TestPlan(t *testing.T) {
	c := newConnector(Options{Name: fakeProvider, URL: &url.URL{Scheme: "http", Host: "127.0.0.1:1"}}, defaultLimits)
	cur := json.RawMessage(`{"since":"2026-09-14T07:00:00Z"}`)
	u, err := c.Plan(t.Context(), connectors.Conn{}, connectors.PlanRequest{Mode: connectors.ModeIncremental, Cursor: cur})
	if err != nil || len(u) != 1 || string(u[0].Cursor) != string(cur) || !u[0].From.IsZero() {
		t.Fatalf("incremental: %+v %v", u, err)
	}
	u, err = c.Plan(t.Context(), connectors.Conn{}, connectors.PlanRequest{Mode: connectors.ModeBackfill, Cursor: cur, From: t0, To: t0.Add(time.Hour)})
	if err != nil || len(u) != 1 || u[0].Cursor != nil || !u[0].From.Equal(t0) {
		t.Fatalf("backfill: %+v %v", u, err)
	}
	for _, req := range []connectors.PlanRequest{{Mode: connectors.ModeCorrection}, {Mode: "sideways"}} {
		if _, err := c.Plan(t.Context(), connectors.Conn{}, req); !errors.Is(err, connectors.ErrPermanent) {
			t.Fatalf("%s: %v", req.Mode, err)
		}
	}
}

func TestAuth(t *testing.T) {
	f, srv := newFake(t)
	c := newRemote(t, srv, defaultLimits, nil)
	ctx := t.Context()
	step, err := c.Begin(ctx, connectors.AuthInput{RedirectURL: "https://vitamux.example.test/oauth/example_sidecar/callback", State: "s"})
	if err != nil || step.Prompt == nil || len(step.Prompt.Fields) != 2 || string(step.Session) != "step-1" {
		t.Fatalf("begin: %+v %v", step, err)
	}
	a, err := c.Continue(ctx, connectors.Conn{}, connectors.AuthInput{Session: step.Session, Values: map[string]string{"username": fakeUser, "password": fakePass}})
	if err != nil || a.Next == nil || a.Next.Prompt.Fields[0].Kind != "code" {
		t.Fatalf("password: %+v %v", a, err)
	}
	if _, err := c.Continue(ctx, connectors.Conn{}, connectors.AuthInput{Session: a.Next.Session, Values: map[string]string{"code": "000000"}}); !errors.Is(err, connectors.ErrReauthRequired) {
		t.Fatalf("wrong code: %v", err)
	}
	a, err = c.Continue(ctx, connectors.Conn{}, connectors.AuthInput{Session: a.Next.Session, Values: map[string]string{"code": fakeCode}})
	if err != nil || a.Next != nil || a.AccountID != fakeAccount || a.Credentials.RefreshToken == "" {
		t.Fatalf("code: %+v %v", a, err)
	}

	// Refresh rotates; the old refresh token is dead.
	cred, err := c.Refresh(ctx, connectors.Conn{}, a.Credentials)
	if err != nil || cred.RefreshToken == a.Credentials.RefreshToken {
		t.Fatalf("refresh: %v", err)
	}
	if _, err := c.Refresh(ctx, connectors.Conn{}, a.Credentials); !errors.Is(err, connectors.ErrReauthRequired) {
		t.Fatalf("old refresh token: %v", err)
	}

	// Begin must return a step; a step with both a redirect and a prompt is a violation.
	f.do(func() {
		f.override["/v1/auth/begin"] = func(w http.ResponseWriter, _ *http.Request) {
			reply(w, AuthResponse{Authorized: &Authorized{AccountID: "x"}})
		}
		f.override["/v1/auth/refresh"] = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{")) }
	})
	if _, err := c.Begin(ctx, connectors.AuthInput{}); !errors.Is(err, connectors.ErrTransient) {
		t.Fatalf("begin without step: %v", err)
	}
	if _, err := c.Refresh(ctx, connectors.Conn{}, cred); !errors.Is(err, connectors.ErrTransient) {
		t.Fatalf("malformed refresh: %v", err)
	}
}

// Sidecar calls take the provider's token buckets like in-process calls: one request per
// 100 ms admits the first fetch at once and spaces the next ones.
func TestRateLimitsThrottleSidecarCalls(t *testing.T) {
	f, srv := newFake(t)
	f.add(3, t0)
	c := newRemote(t, srv, defaultLimits, nil)
	cred := signedIn(t, f, c)
	conn := connectors.Conn{Provider: fakeProvider, HTTP: connectors.NewHTTPClient(nil, []connectors.RateLimitSpec{{Requests: 1, Per: 100 * time.Millisecond}})}
	start := time.Now()
	for range 3 {
		if _, err := c.fetch(t.Context(), conn, cred, connectors.WorkUnit{Stream: fakeStream}, func(ingest.RawItem) {}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Refresh(t.Context(), conn, cred); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 280*time.Millisecond {
		t.Fatalf("4 calls in %s: not throttled", d)
	}

	// A caller that cannot wait for a token gives up without calling the sidecar.
	calls := f.called("/v1/fetch")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.fetch(ctx, conn, cred, connectors.WorkUnit{Stream: fakeStream}, func(ingest.RawItem) {}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("no token: %v", err)
	}
	if f.called("/v1/fetch") != calls {
		t.Fatal("the sidecar was called without a token")
	}
}
