package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/connectors"
)

// Synthetic values of the fake sidecar; the log checks prove none of them is ever logged.
const (
	fakeProvider = "example_sidecar"
	fakeStream   = "example_sidecar.heart_rate"
	fakeSecret   = "synthetic-sidecar-secret-SENTINEL"
	fakeUser     = "synthetic-user"
	fakePass     = "synthetic-pass-SENTINEL"
	fakeCode     = "123456"
	fakeAccount  = "synthetic-account-1"
)

// fakeSidecar is a Go sidecar for protocol vitamux-connector/1: MFA sign-in, rotating
// refresh tokens, a paged synthetic heart-rate stream, and switches for every failure.
type fakeSidecar struct {
	t  *testing.T
	mu sync.Mutex

	desc     Describe
	override map[string]http.HandlerFunc // path → replaces the default handler
	noHeader bool

	samples  []fakeSample
	pageSize int
	issued   int    // tokens issued so far
	access   string // the valid access token; "" = none
	refresh  string // the valid refresh token
	revoked  bool   // refresh refused, every token dead
	rotate   bool   // every result line rotates the credentials
	limited  int64  // > 0: fetch answers rate_limited with this retry_after_s
	drift    bool   // fetch emits its raw lines, then a schema_drift error line
	hang     time.Duration
	calls    map[string]int
	lastMode string
}

type fakeSample struct {
	ID   string
	Time time.Time
	BPM  int
}

func newFake(t *testing.T) (*fakeSidecar, *httptest.Server) {
	f := &fakeSidecar{t: t, pageSize: 10, override: map[string]http.HandlerFunc{}, calls: map[string]int{}}
	f.desc = Describe{
		Protocol: Protocol, Provider: fakeProvider, Name: "Example sidecar", Version: "1.0.0", Official: false,
		AuthKind: string(connectors.AuthInteractiveMFA),
		Streams: []Stream{{Name: fakeStream, IntervalS: 3600, LookbackS: 86400, CorrectionEveryS: 86400,
			MaxBackfillS: 90 * 86400, UnitSizeS: 30 * 86400}},
		RateLimits:   []RateLimit{{Requests: 100, PerS: 60}},
		Capabilities: Capabilities{Incremental: true, Backfill: true, ManualSync: true},
		Upstream:     &Upstream{Package: "synthetic-collector", Version: "1.2.3", SourceURL: "https://example.test/collector"},
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeSidecar) do(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *fakeSidecar) add(n int, start time.Time) {
	f.do(func() {
		for range n {
			i := len(f.samples)
			f.samples = append(f.samples, fakeSample{ID: fmt.Sprintf("hr-%d", i), Time: start.Add(time.Duration(len(f.samples)) * 5 * time.Minute), BPM: 55 + (i*7)%30})
		}
	})
}

func (f *fakeSidecar) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	hang, h, noHeader := f.hang, f.override[r.URL.Path], f.noHeader
	f.calls[r.URL.Path]++
	f.mu.Unlock()
	if hang > 0 {
		select {
		case <-time.After(hang):
		case <-r.Context().Done():
			return
		}
	}
	if !noHeader {
		w.Header().Set(ProtocolHeader, Protocol)
	}
	if r.Header.Get("Authorization") != "Bearer "+fakeSecret {
		problem(w, http.StatusUnauthorized, Problem{Code: connectors.ClassPermanent, Detail: "wrong sidecar secret"})
		return
	}
	if h != nil {
		h(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/v1/describe":
		reply(w, f.desc)
	case "/v1/auth/begin":
		reply(w, AuthResponse{Step: &Step{Prompt: &Prompt{Message: "Sign in", Fields: []Field{
			{Name: "username", Label: "Username", Kind: "text"}, {Name: "password", Label: "Password", Kind: "password"},
		}}, Session: []byte("step-1")}})
	case "/v1/auth/continue":
		f.authContinue(w, r)
	case "/v1/auth/refresh":
		var in RefreshRequest
		readJSON(r, &in)
		if f.revoked || in.Credentials.RefreshToken != f.refresh {
			problem(w, http.StatusUnauthorized, Problem{Code: connectors.ClassReauthRequired})
			return
		}
		reply(w, RefreshResponse{Credentials: f.issue()})
	case "/v1/fetch":
		f.fetch(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeSidecar) authContinue(w http.ResponseWriter, r *http.Request) {
	var in AuthContinueRequest
	readJSON(r, &in)
	switch {
	case string(in.Session) == "step-1" && in.Values["username"] == fakeUser && in.Values["password"] == fakePass:
		reply(w, AuthResponse{Step: &Step{Prompt: &Prompt{Message: "Enter the code", Fields: []Field{
			{Name: "code", Label: "Code", Kind: "code"},
		}}, Session: []byte("step-2")}})
	case string(in.Session) == "step-2" && in.Values["code"] == fakeCode:
		f.revoked = false
		reply(w, AuthResponse{Authorized: &Authorized{AccountID: fakeAccount, Credentials: f.issue()}})
	default:
		problem(w, http.StatusUnauthorized, Problem{Code: connectors.ClassReauthRequired, Detail: "sign-in refused"})
	}
}

// issue rotates the token pair; the previous one is dead.
func (f *fakeSidecar) issue() connectors.Credentials {
	f.issued++
	f.access, f.refresh = fmt.Sprintf("synthetic-access-%d", f.issued), fmt.Sprintf("synthetic-refresh-%d", f.issued)
	return connectors.Credentials{AccessToken: f.access, RefreshToken: f.refresh, ExpiresAt: time.Now().Add(time.Hour).UTC().Truncate(time.Second)}
}

type fakeCursor struct {
	Since  time.Time `json:"since,omitzero"`
	Offset int       `json:"offset,omitempty"`
}

func (f *fakeSidecar) fetch(w http.ResponseWriter, r *http.Request) {
	var in FetchRequest
	readJSON(r, &in)
	f.lastMode = in.Mode
	switch {
	case f.revoked || in.Credentials.AccessToken != f.access:
		problem(w, http.StatusUnauthorized, Problem{Code: connectors.ClassReauthRequired})
		return
	case f.limited > 0:
		problem(w, http.StatusTooManyRequests, Problem{Code: connectors.ClassRateLimited, RetryAfterS: f.limited})
		return
	}
	var cur fakeCursor
	if len(in.Cursor) > 0 {
		if err := json.Unmarshal(in.Cursor, &cur); err != nil {
			problem(w, http.StatusBadRequest, Problem{Code: connectors.ClassPermanent, Detail: "unreadable cursor"})
			return
		}
	}
	var match []fakeSample
	for _, s := range f.samples {
		if in.From.IsZero() && s.Time.After(cur.Since) || !in.From.IsZero() && !s.Time.Before(in.From) && s.Time.Before(in.To) {
			match = append(match, s)
		}
	}
	page := match[min(cur.Offset, len(match)):min(cur.Offset+f.pageSize, len(match))]
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)
	res := ResultLine{Type: LineResult, Done: cur.Offset+f.pageSize >= len(match)}
	for _, s := range page {
		body, _ := json.Marshal(map[string]any{"synthetic": true, "id": s.ID, "time": s.Time, "bpm": s.BPM, "device": "synthetic-band-01"})
		_ = enc.Encode(RawLine{Type: LineRaw, ExternalKey: "sample:" + s.ID, ContentType: "application/json", Body: body,
			Request: &Request{Endpoint: "GET /v1/heart-rate", Params: map[string]any{"offset": cur.Offset}}})
		res.HighWatermark = s.Time
	}
	if f.drift {
		_ = enc.Encode(Problem{Type: LineError, Code: connectors.ClassSchemaDrift, Endpoint: "GET /v1/heart-rate", Fingerprint: "sha256:synthetic"})
		return
	}
	next := fakeCursor{Since: cur.Since, Offset: cur.Offset + f.pageSize}
	if res.Done {
		next = fakeCursor{Since: cur.Since}
		if len(page) > 0 {
			next.Since = page[len(page)-1].Time
		}
	}
	res.NextCursor, _ = json.Marshal(next)
	if f.rotate {
		c := f.issue()
		res.Credentials = &c
	}
	_ = enc.Encode(res)
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func problem(w http.ResponseWriter, status int, p Problem) {
	p.Status, p.Title = status, p.Code
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

func readJSON(r *http.Request, v any) {
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, v)
}

// newRemote is the connector for srv with limits l.
func newRemote(t *testing.T, srv *httptest.Server, l limits, onDescribe func(*connectors.Descriptor)) *Connector {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	o := Options{Name: fakeProvider, URL: u, Secret: fakeSecret}
	if onDescribe != nil {
		o.OnDescribe = func(_ context.Context, d connectors.Descriptor) error { onDescribe(&d); return nil }
	}
	return newConnector(o, l)
}

// ndjson joins lines into a fetch body.
func ndjson(lines ...string) string { return strings.Join(lines, "\n") + "\n" }
