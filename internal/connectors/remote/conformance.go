package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/schemas"
)

// The conformance kit behind `vitamux connector-test`: named checks that run a sidecar through
// the protocol (ADR-0017) with synthetic inputs and say, per check, what is wrong. The checks
// are in conformance_checks.go; this file has the runner, the scenario and the HTTP plumbing.

// Check outcomes.
const (
	StatusPass = "PASS"
	StatusFail = "FAIL"
	StatusSkip = "SKIP"
)

// ConformanceResult is the outcome of one check. Message is one sentence and never quotes a
// response body.
type ConformanceResult struct {
	Check, Status, Message string
}

// Answer is the owner's input to one auth step: prompt values or an OAuth callback query.
type Answer struct {
	Values   map[string]string   `json:"values,omitempty"`
	Callback map[string][]string `json:"callback,omitempty"`
}

// Subject is who the kit acts as: the answers to each auth step in turn, or ready-made credentials.
type Subject struct {
	Login       []Answer                `json:"login,omitempty"`
	Credentials *connectors.Credentials `json:"credentials,omitempty"`
}

// Scenario is the JSON file behind --scenario (schemas/connector-test-scenario.v1.json).
type Scenario struct {
	Subject
	Stream  string             `json:"stream,omitempty"`
	Mode    string             `json:"mode,omitempty"`
	Rotated *Subject           `json:"rotated,omitempty"`
	Errors  map[string]Subject `json:"errors,omitempty"`
}

// LoadScenario reads and validates a scenario file.
func LoadScenario(path string) (Scenario, error) {
	var s Scenario
	b, err := os.ReadFile(path) //nolint:gosec // the path is the --scenario flag
	if err != nil {
		return s, err
	}
	sch, err := compileSchema(schemas.ConnectorTestScenarioV1, "")
	if err != nil {
		return s, err
	}
	if err := validateDoc(sch, b); err != nil {
		return s, fmt.Errorf("scenario %s: %s", path, schemaMsg(err))
	}
	return s, json.Unmarshal(b, &s)
}

// ConformanceOptions say which sidecar to check and with what.
type ConformanceOptions struct {
	URL      string // http(s)://host:port of the sidecar
	Secret   string // the shared bearer secret
	Scenario Scenario
}

// RunConformance runs every check in order, reporting each result as it completes, and returns
// the number of failures. A sidecar that cannot be reached fails the first check and skips the rest.
func RunConformance(ctx context.Context, o ConformanceOptions, report func(ConformanceResult)) (int, error) {
	u, err := url.Parse(o.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return 0, fmt.Errorf("--url must be http(s)://host:port, got %q", o.URL)
	}
	if o.Secret == "" {
		return 0, errors.New("the secret is empty")
	}
	r := &confRun{
		base: strings.TrimRight(o.URL, "/"), secret: o.Secret, scn: o.Scenario,
		hc:      &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		secrets: map[string]bool{}, logins: map[string]*confLogin{}, errPages: map[string]*confPage{},
	}
	r.addSecret(o.Secret)
	failed := 0
	for _, c := range confChecks {
		res := ConformanceResult{Check: c.name}
		switch {
		case r.down:
			res.Status, res.Message = StatusSkip, "the sidecar is unreachable"
		default:
			msg, err := c.run(r, ctx)
			var skip confSkip
			switch {
			case errors.As(err, &skip):
				res.Status, res.Message = StatusSkip, string(skip)
			case err != nil:
				res.Status, res.Message = StatusFail, err.Error()
				failed++
			default:
				res.Status, res.Message = StatusPass, msg
			}
		}
		report(res)
	}
	return failed, nil
}

// confSkip is a check that cannot run, with the reason.
type confSkip string

func (s confSkip) Error() string { return string(s) }

type confRun struct {
	base, secret string
	scn          Scenario
	hc           *http.Client
	defs         map[string]*jsonschema.Schema // protocol definitions, compiled on first use

	// Everything a sidecar answered that must stay secret-free, and the values it must not echo.
	headers   []http.Header
	errBodies [][]byte
	secrets   map[string]bool
	down      bool

	describeResp *confResp
	describeErr  error
	logins       map[string]*confLogin // by error class, or "rotated"
	pages        []confPage
	pageStop     string // why paging stopped before done, "" when done
	pagesRan     bool
	errPages     map[string]*confPage
}

type confResp struct {
	status int
	header http.Header
	body   []byte
}

const (
	confRedirect = "http://localhost:8080/api/v1/connections/callback"
	maxSmall     = 1 << 20  // describe and auth responses
	maxPage      = 64 << 20 // a fetch page
	maxLine      = 36 << 20
	maxPages     = 100
)

func (r *confRun) addSecret(v string) {
	if len(v) >= 4 {
		r.secrets[v] = true
	}
}

// call sends one request. auth is the Authorization header value, "" for none. A transport
// failure marks the sidecar down; any HTTP answer is returned, whatever the status.
func (r *confRun) call(ctx context.Context, method, path string, body any, auth string, limit int64, timeout time.Duration) (confResp, error) {
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		enc, err := json.Marshal(b)
		if err != nil {
			return confResp{}, err
		}
		rd = bytes.NewReader(enc)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, r.base+path, rd)
	if err != nil {
		return confResp{}, err
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := r.hc.Do(req)
	if err != nil {
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err // drop the URL: it is the caller's own, and the message stays one sentence
		}
		r.down = true
		return confResp{}, fmt.Errorf("%s %s failed: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	switch {
	case err != nil:
		return confResp{}, fmt.Errorf("%s %s: reading the response failed: %w", method, path, err)
	case int64(len(b)) > limit:
		return confResp{}, fmt.Errorf("%s %s: response is over %d MiB", method, path, limit>>20)
	}
	out := confResp{status: resp.StatusCode, header: resp.Header, body: b}
	r.headers = append(r.headers, resp.Header)
	if out.status >= 400 {
		r.errBodies = append(r.errBodies, b)
	}
	return out, nil
}

func (r *confRun) bearer() string { return "Bearer " + r.secret }

// post is an authenticated JSON call with the limits of describe and auth.
func (r *confRun) post(ctx context.Context, path string, body any) (confResp, error) {
	return r.call(ctx, http.MethodPost, path, body, r.bearer(), maxSmall, 30*time.Second)
}

func (r *confRun) describe(ctx context.Context) (confResp, error) {
	if r.describeResp == nil && r.describeErr == nil {
		resp, err := r.call(ctx, http.MethodGet, "/v1/describe", nil, r.bearer(), maxSmall, 30*time.Second)
		if err == nil {
			r.describeResp = &resp
		}
		r.describeErr = err
	}
	if r.describeErr != nil {
		return confResp{}, r.describeErr
	}
	return *r.describeResp, nil
}

// stream is the stream to fetch: the scenario's, else the first the sidecar describes.
func (r *confRun) stream(ctx context.Context) string {
	if r.scn.Stream != "" {
		return r.scn.Stream
	}
	resp, err := r.describe(ctx)
	if err != nil {
		return ""
	}
	var d struct {
		Streams []struct {
			Name string `json:"name"`
		} `json:"streams"`
	}
	if json.Unmarshal(resp.body, &d) != nil || len(d.Streams) == 0 {
		return ""
	}
	return d.Streams[0].Name
}

func (r *confRun) authKind(ctx context.Context) string {
	resp, err := r.describe(ctx)
	if err != nil {
		return ""
	}
	var d struct {
		AuthKind string `json:"auth_kind"`
	}
	_ = json.Unmarshal(resp.body, &d)
	return d.AuthKind
}

// Schemas.

func compileSchema(doc []byte, def string) (*jsonschema.Schema, error) {
	root, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return nil, err
	}
	var id struct {
		ID string `json:"$id"`
	}
	if err := json.Unmarshal(doc, &id); err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	c.AssertContent()
	if err := c.AddResource(id.ID, root); err != nil {
		return nil, err
	}
	return c.Compile(id.ID + def)
}

func validateDoc(s *jsonschema.Schema, doc []byte) error {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return errors.New("not valid JSON")
	}
	return s.Validate(inst)
}

// valid checks doc against the protocol definition def.
func (r *confRun) valid(def string, doc []byte) error {
	s, ok := r.defs[def]
	if !ok {
		var err error
		if s, err = compileSchema(schemas.ConnectorSidecarV1, "#/$defs/"+def); err != nil {
			return err
		}
		if r.defs == nil {
			r.defs = map[string]*jsonschema.Schema{}
		}
		r.defs[def] = s
	}
	if err := validateDoc(s, doc); err != nil {
		return errors.New(schemaMsg(err))
	}
	return nil
}

// schemaMsg is the first violation of a schema error, e.g. "at '/auth_kind': value must be one of …".
func schemaMsg(err error) string {
	for line := range strings.SplitSeq(err.Error(), "\n") {
		if rest, ok := strings.CutPrefix(line, "- at "); ok {
			return "at " + rest
		}
	}
	return err.Error()
}

// Response helpers.

func mediaType(h http.Header) string {
	mt, _, _ := mime.ParseMediaType(h.Get("Content-Type"))
	return mt
}

// problem is the typed error of an HTTP error response that matches the problem schema.
func (r *confRun) problem(resp confResp) (Problem, error) {
	var p Problem
	if err := r.valid("problem", resp.body); err != nil {
		return p, fmt.Errorf("error response is not a valid problem (%w)", err)
	}
	_ = json.Unmarshal(resp.body, &p)
	if p.Status != resp.status {
		return p, fmt.Errorf("problem status %d differs from the HTTP status %d", p.Status, resp.status)
	}
	return p, nil
}

// failure describes an unexpected HTTP error status without quoting the body.
func failure(what string, resp confResp) error {
	var p Problem
	if json.Unmarshal(resp.body, &p) == nil && p.Code != "" {
		return fmt.Errorf("%s answered HTTP %d with code %q", what, resp.status, p.Code)
	}
	return fmt.Errorf("%s answered HTTP %d", what, resp.status)
}

func compactJSON(b []byte) string {
	var buf bytes.Buffer
	if json.Compact(&buf, b) != nil {
		return string(b)
	}
	return buf.String()
}
